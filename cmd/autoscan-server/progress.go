package main

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// progressTracker holds best-effort progress for in-flight /grade and
// /sandbox/analyze requests, keyed by a caller-supplied opaque token
// (progress_token form field). Entries live only in this process's memory —
// the web app polls GET /progress/{token} while its synchronous engine call
// is in flight, and a missing token just means "no progress to show".
type progressTracker struct {
	mu      sync.Mutex
	entries map[string]progressEntry
}

type progressEntry struct {
	fraction  float64
	stage     string
	updatedAt time.Time
}

const (
	progressTokenMaxLen = 64
	progressEntryTTL    = 15 * time.Minute
)

func newProgressTracker() *progressTracker {
	return &progressTracker{entries: map[string]progressEntry{}}
}

// validProgressToken keeps tokens to simple opaque ids (the app sends UUIDs).
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
	// Drop abandoned entries (client vanished mid-run) so the map can't grow.
	for key, entry := range t.entries {
		if time.Since(entry.updatedAt) > progressEntryTTL {
			delete(t.entries, key)
		}
	}
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

// progressReporter binds one request's token so pipeline code can report
// without carrying the tracker and token around. A reporter whose request had
// no (or an invalid) progress_token is a no-op.
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

// done removes the entry once the owning request finishes; the synchronous
// response, not a 100% poll, is what tells the caller the run completed.
func (r progressReporter) done() {
	if r.tracker == nil || r.token == "" {
		return
	}
	r.tracker.drop(r.token)
}

// progressReporterFor reads the progress_token form field of an already-parsed
// request. Callers must have run ParseMultipartForm/ParseForm first.
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
	writeJSON(w, http.StatusOK, map[string]any{
		"fraction": entry.fraction,
		"stage":    entry.stage,
	})
}
