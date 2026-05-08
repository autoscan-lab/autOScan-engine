package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/autoscan-lab/autoscan-engine/pkg/domain"
)

const (
	maxUploadBytes int64 = 256 * 1024 * 1024 // 256 MiB
)

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func main() {
	cfg := loadConfig()

	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		log.Fatalf("creating data dir: %v", err)
	}

	srv := &server{cfg: cfg}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", srv.health)
	mux.Handle("POST /setup/{assignment}", withSecret(cfg.engineSecret, http.HandlerFunc(srv.setup)))
	mux.Handle("POST /grade", withSecret(cfg.engineSecret, http.HandlerFunc(srv.grade)))
	mux.Handle("POST /analyze/similarity", withSecret(cfg.engineSecret, http.HandlerFunc(srv.analyzeSimilarity)))
	mux.Handle("POST /analyze/ai-detection", withSecret(cfg.engineSecret, http.HandlerFunc(srv.analyzeAIDetection)))

	httpSrv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("autoscan-server listening on :%s", cfg.port)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}

type server struct {
	cfg config
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	assignment := strings.TrimSpace(r.PathValue("assignment"))
	if assignment == "" || strings.ContainsAny(assignment, "/\\") {
		writeError(w, &httpError{status: 400, msg: "invalid assignment name"})
		return
	}

	r2, err := newR2Client(r.Context(), s.cfg)
	if err != nil {
		writeError(w, err)
		return
	}

	result, err := setupAssignment(r.Context(), s.cfg, r2, assignment)
	if err != nil {
		writeError(w, err)
		return
	}

	log.Printf("activated assignment %s with %d files", assignment, result.FilesDownloaded)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":           "ok",
		"assignment":       result.Assignment,
		"files_downloaded": result.FilesDownloaded,
		"config_dir":       result.ConfigDir,
	})
}

func (s *server) grade(w http.ResponseWriter, r *http.Request) {
	if err := ensureActiveConfig(s.cfg); err != nil {
		writeError(w, err)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, &httpError{status: 400, msg: "invalid multipart form: " + err.Error()})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, &httpError{status: 400, msg: "missing 'file' upload field"})
		return
	}
	defer file.Close()

	runID, err := newRunID()
	if err != nil {
		writeError(w, err)
		return
	}

	runBase, err := runBasePath(s.cfg, runID)
	if err != nil {
		writeError(w, err)
		return
	}
	cleanupRun := true
	defer func() {
		if cleanupRun {
			_ = os.RemoveAll(runBase)
		}
	}()

	if err := os.MkdirAll(runBase, 0o755); err != nil {
		writeError(w, err)
		return
	}

	archivePath, err := runUploadPath(s.cfg, runID, filepath.Ext(header.Filename))
	if err != nil {
		writeError(w, err)
		return
	}
	workspaceDir, err := runWorkspacePath(s.cfg, runID)
	if err != nil {
		writeError(w, err)
		return
	}

	out, err := os.Create(archivePath)
	if err != nil {
		writeError(w, err)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		writeError(w, err)
		return
	}
	out.Close()

	if err := extractZip(archivePath, workspaceDir); err != nil {
		writeError(w, err)
		return
	}
	_ = os.Remove(archivePath)

	exportKey := ""
	if prefix := strings.TrimSpace(r.FormValue("export_key_prefix")); prefix != "" {
		exportKey = strings.TrimRight(prefix, "/") + "/" + runID + "/export.zip"
	}
	resp, err := runGradingPipeline(r.Context(), s.cfg, workspaceDir, exportKey)
	if err != nil {
		writeError(w, err)
		return
	}
	resp.RunID = runID

	submissions := make([]domain.Submission, len(resp.Results))
	for index, result := range resp.Results {
		submissions[index] = result.Submission
	}
	if err := saveRunState(s.cfg, runState{
		RunID:       runID,
		SourceFile:  resp.SourceFile,
		Submissions: submissions,
	}); err != nil {
		writeError(w, err)
		return
	}

	cleanupRun = false

	log.Printf("processed grading request run_id=%s with %d submissions", runID, len(resp.Results))
	writeJSON(w, http.StatusOK, resp)
}

func withSecret(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secret != "" && r.Header.Get("X-Autoscan-Secret") != secret {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		log.Printf("encoding response failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	var he *httpError
	if errors.As(err, &he) {
		writeJSON(w, he.status, map[string]string{"detail": he.msg})
		return
	}
	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
}
