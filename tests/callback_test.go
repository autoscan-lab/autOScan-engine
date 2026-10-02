package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autoscan-lab/autoscan-engine/internal/callback"
)

func TestCallbackValidURLs(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://app.example.com/api/engine/runs/settled": true,
		"http://localhost:3000/cb":                        true,
		"ftp://example.com/cb":                            false,
		"/relative/path":                                  false,
		"not a url":                                       false,
	} {
		if got := callback.Valid(raw); got != want {
			t.Errorf("Valid(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestCallbackSendsRunIDWithSecret(t *testing.T) {
	var gotSecret, gotRunID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSecret = r.Header.Get("X-Autoscan-Secret")
		var body struct {
			RunID string `json:"run_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotRunID = body.RunID
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	notifier := callback.Notifier{Client: server.Client(), Secret: "s3cret"}
	if err := notifier.Notify(context.Background(), server.URL, "run-1"); err != nil {
		t.Fatal(err)
	}
	if gotSecret != "s3cret" || gotRunID != "run-1" {
		t.Fatalf("got secret %q run %q", gotSecret, gotRunID)
	}
}

func TestCallbackRetriesServerErrorsButNotRejections(t *testing.T) {
	var calls atomic.Int32
	status := http.StatusBadGateway
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 3 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	}))
	defer server.Close()

	notifier := callback.Notifier{Client: server.Client(), Delays: []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}}
	if err := notifier.Notify(context.Background(), server.URL, "run-1"); err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls.Load())
	}

	calls.Store(0)
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer rejecting.Close()
	notifier.Client = rejecting.Client()
	if err := notifier.Notify(context.Background(), rejecting.URL, "run-1"); err == nil {
		t.Fatal("expected a rejection error")
	}
	if calls.Load() != 1 {
		t.Fatalf("a 401 must not be retried, got %d attempts", calls.Load())
	}
}
