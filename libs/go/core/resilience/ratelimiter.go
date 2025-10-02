package resilience

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
)

// RateLimiter implements a token bucket with a secondary sliding window tracker for burst & fairness.
// High performance: lock-free fast path using atomic when possible (here simplified with mutex for clarity; can optimize later).
// Refill occurs lazily on each Allow check based on elapsed time.
type RateLimiter struct {
	mu           sync.Mutex
	capacity     int64         // bucket capacity
	fillRate     float64       // tokens per second
	available    float64       // current tokens
	lastRefill   time.Time     // last refill time
	windowStart  time.Time     // sliding window start
	windowDur    time.Duration // sliding window length
	windowCount  int64         // requests in current window
	maxPerWindow int64         // hard cap per window (sliding window limiter)
}

// NewRateLimiter creates a combined token bucket + sliding window limiter.
func NewRateLimiter(capacity int64, fillRate float64, windowDur time.Duration, maxPerWindow int64) *RateLimiter {
	return &RateLimiter{
		capacity:     capacity,
		fillRate:     fillRate,
		available:    float64(capacity),
		lastRefill:   time.Now(),
		windowStart:  time.Now(),
		windowDur:    windowDur,
		maxPerWindow: maxPerWindow,
	}
}

// Allow returns whether one token can be consumed now.
func (r *RateLimiter) Allow() bool {
	return r.AllowN(1)
}

// AllowN attempts to consume n tokens.
func (r *RateLimiter) AllowN(n int64) bool {
	if n <= 0 {
		return true
	}
	now := time.Now()
	meter := otel.GetMeterProvider().Meter("swarm-go")

	r.mu.Lock()
	defer r.mu.Unlock()

	// Refill tokens
	elapsed := now.Sub(r.lastRefill).Seconds()
	if elapsed > 0 {
		refill := elapsed * r.fillRate
		if refill > 0 {
			r.available = minFloat(float64(r.capacity), r.available+refill)
			r.lastRefill = now
		}
	}

	// Sliding window rotation
	if now.Sub(r.windowStart) >= r.windowDur {
		r.windowStart = now
		r.windowCount = 0
	}

	// Check window cap first
	if r.maxPerWindow > 0 && r.windowCount+n > r.maxPerWindow {
		counter, _ := meter.Int64Counter("swarm_ratelimiter_window_drops_total")
		counter.Add(context.Background(), 1)
		return false
	}

	// Token bucket availability
	if float64(n) <= r.available {
		r.available -= float64(n)
		r.windowCount += n
		return true
	}
	counter, _ := meter.Int64Counter("swarm_ratelimiter_token_drops_total")
	counter.Add(context.Background(), 1)
	return false
}

// ReserveAfter returns the duration after which n tokens will be available.
func (r *RateLimiter) ReserveAfter(n int64) time.Duration {
	if n <= 0 {
		return 0
	}
	_rnow := time.Now()
	_rneed := float64(n)

	r.mu.Lock()
	defer r.mu.Unlock()

	elapsed := _rnow.Sub(r.lastRefill).Seconds()
	if elapsed > 0 {
		refill := elapsed * r.fillRate
		if refill > 0 {
			r.available = minFloat(float64(r.capacity), r.available+refill)
			r.lastRefill = _rnow
		}
	}

	if r.available >= _rneed {
		return 0
	}
	shortfall := _rneed - r.available
	seconds := shortfall / r.fillRate
	return time.Duration(seconds * float64(time.Second))
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
