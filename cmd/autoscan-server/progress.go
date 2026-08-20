package main

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

type progressTracker struct {
	mu      sync.Mutex
	entries map[string]progressEntry
}

type progressEntry struct {
	fraction  float64
	stage     string
	state     string // "" while running; "done" or "failed" once an async job ends
	detail    string
	updatedAt time.Time
}

const (
	progressTokenMaxLen = 64
	progressEntryTTL    = 15 * time.Minute
)

func newProgressTracker() *progressTracker {
	return &progressTracker{entries: map[string]progressEntry{}}
}

func validProgressToken(token string) bool {
	if token == "" || len(token) > progressTokenMaxLen {
		return false
	}
	for _, r := range token {
		ok := r == '-' ||
			(r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

func (t *progressTracker) set(token string, fraction float64, stage string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Never move a bar backwards; parallel compile callbacks can land out of order.
	if prev, ok := t.entries[token]; ok && prev.fraction > fraction {
		fraction = prev.fraction
	}
	t.entries[token] = progressEntry{fraction: fraction, stage: stage, updatedAt: time.Now()}
	for key, entry := range t.entries {
		if time.Since(entry.updatedAt) > progressEntryTTL {
			delete(t.entries, key)
		}
	}
}

func (t *progressTracker) finish(token string, ok bool, detail string) {
	if token == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entries[token]
	if ok {
		entry.fraction = 1
		entry.state = "done"
	} else {
		entry.state = "failed"
		entry.detail = detail
	}
	entry.updatedAt = time.Now()
	t.entries[token] = entry
}

func (t *progressTracker) drop(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, token)
}

func (t *progressTracker) get(token string) (progressEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.entries[token]
	return entry, ok
}

type progressReporter struct {
	tracker *progressTracker
	token   string
}

func (r progressReporter) report(fraction float64, stage string) {
	if r.tracker == nil || r.token == "" {
		return
	}
	r.tracker.set(r.token, fraction, stage)
}

// The synchronous response, not a 100% poll, tells the caller the run completed.
func (r progressReporter) done() {
	if r.tracker == nil || r.token == "" {
		return
	}
	r.tracker.drop(r.token)
}

// Callers must have run ParseMultipartForm/ParseForm before this reads the field.
func (s *server) progressReporterFor(r *http.Request) progressReporter {
	token := strings.TrimSpace(r.FormValue("progress_token"))
	if !validProgressToken(token) {
		token = ""
	}
	return progressReporter{tracker: s.progress, token: token}
}

func (s *server) progressStatus(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if !validProgressToken(token) {
		writeError(w, &httpError{status: 400, msg: "invalid progress token"})
		return
	}
	entry, ok := s.progress.get(token)
	if !ok {
		writeError(w, &httpError{status: 404, msg: "unknown progress token"})
		return
	}
	state := entry.state
	if state == "" {
		state = "running"
	}
	body := map[string]any{
		"fraction": entry.fraction,
		"stage":    entry.stage,
		"state":    state,
	}
	if entry.detail != "" {
		body["detail"] = entry.detail
	}
	writeJSON(w, http.StatusOK, body)
}
