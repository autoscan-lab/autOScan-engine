package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/autoscan-lab/autoscan-engine/internal/terminal"
	"github.com/autoscan-lab/autoscan-engine/pkg/engine"
)

const (
	maxUploadBytes int64 = 256 * 1024 * 1024
)

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func main() {
	// Sandbox host modes run with no config or secrets — dispatch before loadConfig.
	if len(os.Args) >= 3 && os.Args[1] == "pane-host" {
		os.Exit(terminal.RunPaneHost(os.Args[2]))
	}
	if len(os.Args) == 2 && os.Args[1] == engine.ScenarioHostArg {
		os.Exit(engine.RunScenarioHost())
	}

	cfg := loadConfig()

	if err := cfg.requireSecret(); err != nil {
		log.Fatalf("config: %v", err)
	}

	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		log.Fatalf("creating data dir: %v", err)
	}
	pruneOldRuns(cfg)

	srv := &server{
		cfg:       cfg,
		progress:  newProgressTracker(),
		activity:  newActivity(),
		gradeSlot: make(chan struct{}, 1),
		aiRefresh: &aiRefreshProgress{},
	}

	protected := func(h http.HandlerFunc) http.Handler {
		return withSecret(cfg.engineSecret, h)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", srv.health)
	mux.Handle("POST /grade", protected(srv.grade))
	mux.Handle("DELETE /grade/{run_id}", protected(srv.cancelGrade))
	mux.Handle("POST /sandbox/analyze", protected(srv.sandboxAnalyze))
	mux.Handle("GET /progress/{token}", protected(srv.progressStatus))
	mux.Handle("GET /ai-reanalysis", protected(srv.aiReanalysisStatus))
	// Token-authenticated instead of withSecret: the browser connects directly and cannot carry the engine secret.
	mux.HandleFunc("GET /terminal", srv.terminal)

	httpSrv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           trackRequests(srv.activity, logRequests(mux)),
		ReadHeaderTimeout: 30 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go srv.refreshSavedAI(ctx)

	if cfg.idleExit > 0 {
		go exitWhenIdle(ctx, srv.activity, cfg.idleExit, stop)
	}

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
	terminal.TeardownAll("server shutting down")
}

type server struct {
	cfg config
	// Grade jobs run one at a time: compile and test timeouts are wall-clock, so jobs sharing the CPU would time out students' programs.
	gradeSlot chan struct{}
	progress  *progressTracker
	activity  *activity
	// run id -> context.CancelFunc for in-flight async grade jobs.
	jobs      sync.Map
	aiRefresh *aiRefreshProgress
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) grade(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, &httpError{status: 400, msg: "invalid multipart form: " + err.Error()})
		return
	}
	s.gradeAsync(w, r)
}

func withSecret(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Autoscan-Secret")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
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
	writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": "internal server error"})
}
