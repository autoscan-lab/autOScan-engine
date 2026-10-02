package tests

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The real HTTP server uses a local S3 fixture; no production configuration or execution is involved.
func TestAIReanalysisServerBacksUpRetriesConflictsAndResumes(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "autoscan-server")
	if output, err := exec.Command("go", "build", "-o", binary, "../cmd/autoscan-server").CombinedOutput(); err != nil {
		t.Fatalf("build server: %v %s", err, output)
	}
	original := savedAIResult(t)
	objects := map[string][]byte{"web/runs/a/result.json": original, "web/runs/b/result.json": original, "web/runs/bad/result.json": []byte("invalid JSON")}
	var mu sync.Mutex
	conflict := true
	etag := func(body []byte) string { sum := md5.Sum(body); return hex.EncodeToString(sum[:]) }
	precondition := func(w http.ResponseWriter) {
		w.WriteHeader(412)
		_, _ = io.WriteString(w, "<Error><Code>PreconditionFailed</Code><Message>changed</Message></Error>")
	}
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/local/")
		if r.Method == "GET" && r.URL.Query().Get("list-type") == "2" {
			type content struct {
				Key          string
				Size         int
				ETag         string
				LastModified string
			}
			response := struct {
				XMLName     xml.Name `xml:"ListBucketResult"`
				Name        string
				Prefix      string
				IsTruncated bool
				Contents    []content
			}{Name: "local", Prefix: r.URL.Query().Get("prefix")}
			keys := make([]string, 0, len(objects))
			for key := range objects {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if strings.HasPrefix(key, response.Prefix) {
					response.Contents = append(response.Contents, content{key, len(objects[key]), etag(objects[key]), time.Now().UTC().Format(time.RFC3339)})
				}
			}
			_ = xml.NewEncoder(w).Encode(response)
			return
		}
		body, exists := objects[key]
		switch r.Method {
		case "HEAD", "GET":
			if !exists {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, "<Error><Code>NoSuchKey</Code></Error>")
				return
			}
			if match := r.Header.Get("If-Match"); match != "" && strings.Trim(match, "\"") != etag(body) {
				precondition(w)
				return
			}
			w.Header().Set("ETag", "\""+etag(body)+"\"")
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			if r.Method == "GET" {
				_, _ = w.Write(body)
			}
		case "PUT":
			incoming, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(500)
				return
			}
			if strings.Contains(key, "/ai-revisions/") {
				if r.Header.Get("If-None-Match") != "*" {
					t.Error("backup missing create-only condition")
				}
				if exists {
					precondition(w)
					return
				}
			} else {
				if strings.Trim(r.Header.Get("If-Match"), "\"") != etag(body) {
					t.Error("result missing matching ETag")
					precondition(w)
					return
				}
				if key == "web/runs/b/result.json" && conflict {
					conflict = false
					var fields map[string]json.RawMessage
					_ = json.Unmarshal(body, &fields)
					fields["concurrent_update"] = json.RawMessage(`{"keep":true}`)
					objects[key], _ = json.Marshal(fields)
					precondition(w)
					return
				}
			}
			objects[key] = incoming
			w.Header().Set("ETag", "\""+etag(incoming)+"\"")
		default:
			w.WriteHeader(400)
		}
	}))
	defer s3.Close()
	start := func() (string, func()) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
		_ = listener.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, binary)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "AUTOSCAN_DATA_DIR=" + filepath.Join(root, "data"), "PORT=" + port, "ENGINE_SECRET=local-secret", "R2_ACCOUNT_ID=local", "R2_ACCESS_KEY_ID=local", "R2_SECRET_ACCESS_KEY=local", "R2_BUCKET_NAME=local", "R2_ENDPOINT=" + s3.URL}
		log, err := os.CreateTemp(root, "server-*.log")
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		var once sync.Once
		stop := func() { once.Do(func() { cancel(); _ = cmd.Wait(); _ = log.Close() }) }
		t.Cleanup(stop)
		return "http://127.0.0.1:" + port, stop
	}
	waitPass := func(base string) map[string]any {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			request, _ := http.NewRequest("GET", base+"/ai-reanalysis", nil)
			request.Header.Set("X-Autoscan-Secret", "local-secret")
			response, err := http.DefaultClient.Do(request)
			if err == nil {
				var state map[string]any
				_ = json.NewDecoder(response.Body).Decode(&state)
				_ = response.Body.Close()
				if state["state"] == "retrying" || state["state"] == "complete" {
					return state
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		logs, _ := filepath.Glob(filepath.Join(root, "server-*.log"))
		for _, name := range logs {
			body, _ := os.ReadFile(name)
			t.Log(string(body))
		}
		t.Fatal("backfill never finished")
		return nil
	}
	base, stop := start()
	state := waitPass(base)
	if state["state"] != "retrying" || state["updated"] != float64(1) || state["failed"] != float64(2) {
		t.Fatalf("first pass: %+v", state)
	}
	response, err := http.Get(base + "/ai-reanalysis")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("exposed progress without engine secret")
	}
	stop()
	mu.Lock()
	var conflicted map[string]any
	_ = json.Unmarshal(objects["web/runs/b/result.json"], &conflicted)
	if conflicted["concurrent_update"] == nil || conflicted["ai_reanalysis"] != nil {
		t.Fatal("overwrote concurrent update despite failed condition")
	}
	delete(objects, "web/runs/bad/result.json")
	mu.Unlock()
	base, stop = start()
	state = waitPass(base)
	if state["state"] != "complete" || state["updated"] != float64(1) || state["current"] != float64(1) {
		t.Fatalf("resumed pass: %+v", state)
	}
	stop()
	mu.Lock()
	defer mu.Unlock()
	var updated map[string]any
	_ = json.Unmarshal(objects["web/runs/b/result.json"], &updated)
	if updated["concurrent_update"] == nil || updated["ai_reanalysis"] == nil {
		t.Fatal("retry lost concurrent data or methodology")
	}
	backups := 0
	for key, body := range objects {
		if strings.Contains(key, "/ai-revisions/") {
			backups++
			if !json.Valid(body) {
				t.Fatal("invalid backup")
			}
		}
	}
	if backups != 3 {
		t.Fatalf("expected backup per distinct old payload, got %d", backups)
	}
	if _, err := os.Stat(filepath.Join(root, "data", "ai-reanalysis", "contextual-v2", "status.json")); err != nil {
		t.Fatal("progress was not persisted")
	}
}
