package main

import (
	"net/http"
	"sync"
	"time"
)

// Global request limits for the secret-protected endpoints.
const (
	defaultRateLimitPerSecond = 5
	defaultRateLimitBurst     = 10
)

// rateLimiter is a token bucket shared across all requests.
type rateLimiter struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	refill float64 // tokens per second
	last   time.Time
}

func newRateLimiter(perSecond, burst float64) *rateLimiter {
	return &rateLimiter{
		tokens: burst,
		max:    burst,
		refill: perSecond,
		last:   time.Now(),
	}
}

// allow consumes a token and reports whether the request may proceed.
func (rl *rateLimiter) allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	rl.tokens += now.Sub(rl.last).Seconds() * rl.refill
	if rl.tokens > rl.max {
		rl.tokens = rl.max
	}
	rl.last = now

	if rl.tokens >= 1 {
		rl.tokens--
		return true
	}
	return false
}

// limitRequests rejects requests with 429 once the bucket is empty.
func limitRequests(rl *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
