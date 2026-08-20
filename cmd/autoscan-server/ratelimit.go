package main

import (
	"net/http"
	"sync"
	"time"
)

const (
	defaultRateLimitPerSecond = 5
	defaultRateLimitBurst     = 10
)

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

func limitRequests(rl *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.allow() {
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
