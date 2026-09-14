package main

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

// Fly's proxy only sees requests, not background grade jobs, so the server decides when it is idle and exits itself.
type activity struct {
	mu       sync.Mutex
	active   int
	lastDone time.Time
}

func newActivity() *activity {
	return &activity{lastDone: time.Now()}
}

func (a *activity) begin() {
	a.mu.Lock()
	a.active++
	a.mu.Unlock()
}

func (a *activity) end() {
	a.mu.Lock()
	a.active--
	a.lastDone = time.Now()
	a.mu.Unlock()
}

func (a *activity) idleFor() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.active > 0 {
		return 0
	}
	return time.Since(a.lastDone)
}

// Terminal WebSockets stay inside their handler for the socket's lifetime, so they count as active too.
func trackRequests(a *activity, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.begin()
		defer a.end()
		next.ServeHTTP(w, r)
	})
}

// Exit code 0 leaves the Fly machine stopped (restart policy on-failure); the proxy starts it on the next request.
func exitWhenIdle(ctx context.Context, a *activity, timeout time.Duration, stop context.CancelFunc) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if a.idleFor() >= timeout {
				log.Printf("idle for %s, exiting", timeout)
				stop()
				return
			}
		}
	}
}
