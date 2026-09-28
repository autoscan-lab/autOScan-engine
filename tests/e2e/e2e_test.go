//go:build e2e

// Package e2e drives a running engine stack end to end; see README.md.
package e2e

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	secret      = "local-test-secret"
	bucket      = "autoscan-local"
	fastStudent = "Student1_101_assignsubmission_file"
)

var (
	engineURL  = envOr("AUTOSCAN_E2E_ENGINE", "http://localhost:18080")
	s3Endpoint = envOr("AUTOSCAN_E2E_S3", "localhost:19000")
	store      *minio.Client
)

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func TestMain(m *testing.M) {
	if err := waitHealthy(60 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "engine not reachable at %s: %v\nstart it with: docker compose -f tests/e2e/compose.yml up -d --build\n", engineURL, err)
		os.Exit(1)
	}
	if err := seed(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "seeding %s: %v\n", s3Endpoint, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func TestGradeProducesPassingResults(t *testing.T) {
	run := gradeDone(t, "S2_BC", "fast")

	var result struct {
		Results []struct {
			Tests struct {
				Passed int `json:"passed"`
				Total  int `json:"total"`
			} `json:"tests"`
		} `json:"results"`
	}
	readJSON(t, "web/runs/"+run+"/result.json", &result)
	if len(result.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(result.Results))
	}
	for i, r := range result.Results {
		if r.Tests.Total != 1 || r.Tests.Passed != 1 {
			t.Errorf("submission %d tests = %d/%d, want 1/1", i, r.Tests.Passed, r.Tests.Total)
		}
	}
}

// Regression: a terminal used to get the files of whichever assignment was graded last.
func TestTerminalGetsTheRunsOwnPolicyFiles(t *testing.T) {
	bc := gradeDone(t, "S2_BC", "fast")
	gradeDone(t, "S2_AICE", "fast")

	out, _ := terminalRun(t, mintToken(bc, fastStudent, "S2_BC"), "ls -1")
	for _, want := range []string{"S2.c", "bc_lib.c", "bc_lib.h", "bc_input.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("terminal listing is missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "aice_") {
		t.Errorf("terminal on the S2_BC run shows S2_AICE files:\n%s", out)
	}
}

func TestSolutionTerminalBuildsFromThePolicy(t *testing.T) {
	out, _ := terminalRun(t, mintSolutionToken("S2_BC"), "ls -1 && gcc -Wall S2.c bc_lib.c -o S2 && ./S2 bc_input.txt")
	for _, want := range []string{"S2.c", "bc_lib.c", "bc_input.txt", "hello from BC"} {
		if !strings.Contains(out, want) {
			t.Errorf("solution terminal output is missing %q:\n%s", want, out)
		}
	}
}

func TestSolutionTerminalWithoutSolutionCloses(t *testing.T) {
	_, frames := dialTerminal(t, mintSolutionToken("S2_AICE"))
	timeout := time.After(15 * time.Second)
	for {
		select {
		case _, ok := <-frames:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("terminal for an assignment without solution files stayed open")
		}
	}
}

func TestGradesQueueWithoutBlockingTerminals(t *testing.T) {
	bc := gradeDone(t, "S2_BC", "fast")
	slow := startGrade(t, "S2_BC", "slow")
	waitStage(t, slow, "Running submissions")

	queued := startGrade(t, "S2_AICE", "fast")
	if p := progressOf(t, queued); p.Stage != "Queued" {
		t.Errorf("second grade stage = %q, want Queued while the first one runs", p.Stage)
	}

	_, elapsed := terminalRun(t, mintToken(bc, fastStudent, "S2_BC"), "true")
	if elapsed > 3*time.Second {
		t.Errorf("terminal took %s to answer while grades were in flight", elapsed)
	}

	if p := waitRun(t, slow); p.State != "done" {
		t.Errorf("slow grade = %+v, want done", p)
	}
	if p := waitRun(t, queued); p.State != "done" {
		t.Errorf("queued grade = %+v, want done", p)
	}
}

func TestCancelQueuedGrade(t *testing.T) {
	slow := startGrade(t, "S2_BC", "slow")
	waitStage(t, slow, "Running submissions")
	queued := startGrade(t, "S2_AICE", "fast")

	if status := engineRequest(t, http.MethodDelete, "/grade/"+queued, nil, "").StatusCode; status != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204", status)
	}
	if p := waitRun(t, queued); p.State != "failed" || p.Detail != "grading cancelled" {
		t.Errorf("cancelled grade = %+v, want failed/grading cancelled", p)
	}
	if p := waitRun(t, slow); p.State != "done" {
		t.Errorf("running grade = %+v, want done", p)
	}
}

// Several graders each poll progress every second; none of those polls may be rejected.
func TestProgressBurstIsNotRateLimited(t *testing.T) {
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		statuses = map[int]int{}
	)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := -1
			req, _ := http.NewRequest(http.MethodGet, engineURL+"/progress/000000000000000000000000", nil)
			req.Header.Set("X-Autoscan-Secret", secret)
			if res, err := http.DefaultClient.Do(req); err == nil {
				res.Body.Close()
				status = res.StatusCode
			}
			mu.Lock()
			statuses[status]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if statuses[http.StatusTooManyRequests] > 0 || statuses[http.StatusNotFound] != 40 {
		t.Errorf("40 progress polls -> %v, want all 404", statuses)
	}
}

func TestTerminalKeystrokeEcho(t *testing.T) {
	bc := gradeDone(t, "S2_BC", "fast")
	conn, frames := dialTerminal(t, mintToken(bc, fastStudent, "S2_BC"))
	defer conn.Close(websocket.StatusNormalClosure, "")
	drain(frames, 500*time.Millisecond)

	var samples []time.Duration
	for i := 0; i < 30; i++ {
		start := time.Now()
		if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("x")); err != nil {
			t.Fatalf("write: %v", err)
		}
		select {
		case <-frames:
		case <-time.After(5 * time.Second):
			t.Fatal("no echo within 5s")
		}
		samples = append(samples, time.Since(start))
		drain(frames, 20*time.Millisecond)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50, p95 := samples[len(samples)/2], samples[len(samples)*95/100]
	t.Logf("keystroke echo p50=%s p95=%s", p50, p95)
	if p95 > 250*time.Millisecond {
		t.Errorf("keystroke echo p95 = %s, want under 250ms locally", p95)
	}
}

type progress struct {
	Fraction float64 `json:"fraction"`
	Stage    string  `json:"stage"`
	State    string  `json:"state"`
	Detail   string  `json:"detail"`
}

func uploadKey(name string) string {
	return "web/uploads/staging/" + name + ".zip"
}

func gradeDone(t *testing.T, assignment, upload string) string {
	t.Helper()
	run := startGrade(t, assignment, upload)
	if p := waitRun(t, run); p.State != "done" {
		t.Fatalf("grade %s/%s = %+v, want done", assignment, upload, p)
	}
	return run
}

func startGrade(t *testing.T, assignment, upload string) string {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for key, value := range map[string]string{
		"assignment":        assignment,
		"r2_key":            uploadKey(upload),
		"result_key_prefix": "web/runs",
		"export_key_prefix": "web/runs",
	} {
		_ = form.WriteField(key, value)
	}
	_ = form.Close()

	res := engineRequest(t, http.MethodPost, "/grade", &body, form.FormDataContentType())
	defer res.Body.Close()
	var payload struct {
		RunID string `json:"run_id"`
	}
	if res.StatusCode != http.StatusAccepted || json.NewDecoder(res.Body).Decode(&payload) != nil || payload.RunID == "" {
		t.Fatalf("POST /grade %s: status %d", assignment, res.StatusCode)
	}
	return payload.RunID
}

func progressOf(t *testing.T, run string) progress {
	t.Helper()
	res := engineRequest(t, http.MethodGet, "/progress/"+run, nil, "")
	defer res.Body.Close()
	var p progress
	if res.StatusCode != http.StatusOK || json.NewDecoder(res.Body).Decode(&p) != nil {
		t.Fatalf("progress %s: status %d", run, res.StatusCode)
	}
	return p
}

func waitRun(t *testing.T, run string) progress {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if p := progressOf(t, run); p.State == "done" || p.State == "failed" {
			return p
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("run %s did not finish within 2 minutes", run)
	return progress{}
}

func waitStage(t *testing.T, run, stage string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		p := progressOf(t, run)
		if p.Stage == stage {
			return
		}
		if p.State != "" && p.State != "running" {
			t.Fatalf("run %s finished (%+v) before reaching %q", run, p, stage)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s never reached %q", run, stage)
}

func engineRequest(t *testing.T, method, path string, body io.Reader, contentType string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, engineURL+path, body)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("X-Autoscan-Secret", secret)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

// Mirrors the web app's /api/terminal token: base64url JSON claims, then base64url HMAC-SHA256.
func mintToken(run, submission, assignment string) string {
	claims, _ := json.Marshal(map[string]any{
		"run_id":        run,
		"submission_id": submission,
		"assignment":    assignment,
		"student":       "e2e",
		"session_id":    fmt.Sprintf("e2e-%d", time.Now().UnixNano()),
		"panes":         1,
		"exp":           time.Now().Add(time.Minute).Unix(),
	})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func mintSolutionToken(assignment string) string {
	claims, _ := json.Marshal(map[string]any{
		"assignment": assignment,
		"solution":   true,
		"student":    "solution",
		"session_id": fmt.Sprintf("e2e-%d", time.Now().UnixNano()),
		"panes":      1,
		"exp":        time.Now().Add(time.Minute).Unix(),
	})
	payload := base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func dialTerminal(t *testing.T, token string) (*websocket.Conn, <-chan []byte) {
	t.Helper()
	wsURL := strings.Replace(engineURL, "http", "ws", 1) + "/terminal?token=" + token
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial terminal: %v", err)
	}
	frames := make(chan []byte, 256)
	go func() {
		defer close(frames)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			frames <- data
		}
	}()
	return conn, frames
}

func drain(frames <-chan []byte, quiet time.Duration) {
	for {
		select {
		case <-frames:
		case <-time.After(quiet):
			return
		}
	}
}

// Runs command in a fresh terminal and returns its output and how long the shell took to answer.
func terminalRun(t *testing.T, token, command string) (string, time.Duration) {
	t.Helper()
	conn, frames := dialTerminal(t, token)
	defer conn.Close(websocket.StatusNormalClosure, "")

	marker := fmt.Sprintf("E2E_DONE_%d", time.Now().UnixNano())
	// Quoting splits the marker so the shell's echo of the typed line can't match it.
	line := fmt.Sprintf("%s; echo %s''%s\n", command, marker[:4], marker[4:])
	start := time.Now()
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte(line)); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out strings.Builder
	timeout := time.After(30 * time.Second)
	for !strings.Contains(out.String(), marker) {
		select {
		case data, ok := <-frames:
			if !ok {
				t.Fatalf("terminal closed before answering:\n%s", out.String())
			}
			out.Write(data)
		case <-timeout:
			t.Fatalf("terminal did not answer within 30s:\n%s", out.String())
		}
	}
	return out.String(), time.Since(start)
}

func readJSON(t *testing.T, key string, into any) {
	t.Helper()
	object, err := store.GetObject(context.Background(), bucket, key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer object.Close()
	if err := json.NewDecoder(object).Decode(into); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
}

func waitHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		res, err := http.Get(engineURL + "/health")
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("status %d", res.StatusCode)
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// Uploads testdata/bucket as the bucket root and zips each testdata/submissions/<name> to its staging key.
func seed(ctx context.Context) error {
	client, err := minio.New(s3Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4("local-access-key", "local-secret-key", ""),
		Region: "auto",
	})
	if err != nil {
		return err
	}
	store = client

	var exists bool
	for attempt := 0; attempt < 20; attempt++ {
		if exists, err = client.BucketExists(ctx, bucket); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return err
		}
	}

	root := filepath.Join("testdata", "bucket")
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		_, err = client.FPutObject(ctx, bucket, filepath.ToSlash(rel), path, minio.PutObjectOptions{})
		return err
	})
	if err != nil {
		return err
	}

	for _, name := range []string{"fast", "slow"} {
		data, err := zipDir(filepath.Join("testdata", "submissions", name))
		if err != nil {
			return err
		}
		_, err = client.PutObject(ctx, bucket, uploadKey(name), bytes.NewReader(data), int64(len(data)),
			minio.PutObjectOptions{ContentType: "application/zip"})
		if err != nil {
			return err
		}
	}
	return nil
}

func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	archive := zip.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		w, err := archive.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
