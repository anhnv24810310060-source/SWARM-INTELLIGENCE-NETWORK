package main
package main

import (
	"sync"
	"time"
)

// HybridRateLimiter combines token bucket (burst) + sliding window (smooth rate)
// This provides better protection against sustained attacks while allowing legitimate bursts
type HybridRateLimiter struct {
	mu sync.Mutex
	
	// Token bucket for burst capacity
	capacity int
	tokens   int
	refill   int
	interval time.Duration
	updated  time.Time
	
	// Sliding window for rate smoothing
	windowSize    time.Duration
	requestLimit  int
	requests      map[string]*slidingWindow
	
	// Memory management
	lastCleanup   time.Time
	cleanupPeriod time.Duration
}

type slidingWindow struct {
	timestamps []time.Time
	head       int
	size       int
}

// NewHybridRateLimiter creates limiter with both burst and rate limits
// capacity: max burst size (tokens available immediately)
// refill: tokens added per interval
// interval: how often to refill tokens
// windowSize: sliding window duration (e.g., 60s)
// requestLimit: max requests in window
func NewHybridRateLimiter(capacity, refill int, interval, windowSize time.Duration, requestLimit int) *HybridRateLimiter {
	return &HybridRateLimiter{
		capacity:      capacity,
		tokens:        capacity,
		refill:        refill,
		interval:      interval,
		updated:       time.Now(),
		windowSize:    windowSize,
		requestLimit:  requestLimit,
		requests:      make(map[string]*slidingWindow),
		lastCleanup:   time.Now(),
		cleanupPeriod: 5 * time.Minute,
	}
}

// Allow checks if request is allowed under both token bucket and sliding window
func (h *HybridRateLimiter) Allow(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	now := time.Now()
	
	// Token bucket check (burst protection)
	if !h.checkTokenBucket(now) {
		return false
	}
	
	// Sliding window check (sustained rate protection)
	if !h.checkSlidingWindow(key, now) {
		return false
	}
	
	// Both checks passed - consume token and record request
	h.tokens--
	h.recordRequest(key, now)
	
	// Periodic cleanup of old entries
	if now.Sub(h.lastCleanup) > h.cleanupPeriod {
		h.cleanup(now)
		h.lastCleanup = now
	}
	
	return true
}

func (h *HybridRateLimiter) checkTokenBucket(now time.Time) bool {
	// Refill tokens based on elapsed time
	elapsed := now.Sub(h.updated)
	if elapsed >= h.interval {
		periods := int(elapsed / h.interval)
		if periods > 0 {
			h.tokens += periods * h.refill
			if h.tokens > h.capacity {
				h.tokens = h.capacity
			}
			h.updated = now
		}
	}
	
	return h.tokens > 0
}

func (h *HybridRateLimiter) checkSlidingWindow(key string, now time.Time) bool {
	window, exists := h.requests[key]
	if !exists {
		return true // First request always allowed
	}
	
	// Count requests in current window
	cutoff := now.Add(-h.windowSize)
	count := 0
	
	for i := 0; i < window.size; i++ {
		idx := (window.head + i) % len(window.timestamps)
		if window.timestamps[idx].After(cutoff) {
			count++
		}
	}
	
	return count < h.requestLimit
}

func (h *HybridRateLimiter) recordRequest(key string, now time.Time) {
	window, exists := h.requests[key]
	if !exists {
		// Initialize sliding window with reasonable buffer
		window = &slidingWindow{
			timestamps: make([]time.Time, h.requestLimit*2),
		}
		h.requests[key] = window
	}
	
	// Add to circular buffer
	window.timestamps[window.head] = now
	window.head = (window.head + 1) % len(window.timestamps)
	if window.size < len(window.timestamps) {
		window.size++
	}
}

func (h *HybridRateLimiter) cleanup(now time.Time) {
	cutoff := now.Add(-h.windowSize * 2) // Keep some history
	
	for key, window := range h.requests {
		// Count active requests
		active := 0
		for i := 0; i < window.size; i++ {
			idx := (window.head + i) % len(window.timestamps)
			if window.timestamps[idx].After(cutoff) {
				active++
			}
		}
		
		// Remove completely inactive entries
		if active == 0 {
			delete(h.requests, key)
		}
	}
}

// Stats returns current limiter statistics
func (h *HybridRateLimiter) Stats() RateLimiterStats {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	return RateLimiterStats{
		Capacity:      h.capacity,
		CurrentTokens: h.tokens,
		TrackedKeys:   len(h.requests),
	}
}

type RateLimiterStats struct {
	Capacity      int
	CurrentTokens int
	TrackedKeys   int
}

// PerKeyRateLimiter manages independent rate limiters per key (user/IP)
type PerKeyRateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*HybridRateLimiter
	config   RateLimitConfig
	
	// Automatic cleanup
	lastCleanup   time.Time
	cleanupPeriod time.Duration
}

type RateLimitConfig struct {
	Capacity      int
	Refill        int
	Interval      time.Duration
	WindowSize    time.Duration
	RequestLimit  int
}

// NewPerKeyRateLimiter creates a pool of rate limiters
func NewPerKeyRateLimiter(config RateLimitConfig) *PerKeyRateLimiter {
	return &PerKeyRateLimiter{
		limiters:      make(map[string]*HybridRateLimiter),
		config:        config,
		lastCleanup:   time.Now(),
		cleanupPeriod: 10 * time.Minute,
	}
}

// Allow checks if request is allowed for given key
func (p *PerKeyRateLimiter) Allow(key string) bool {
	limiter := p.getLimiter(key)
	return limiter.Allow(key)
}

func (p *PerKeyRateLimiter) getLimiter(key string) *HybridRateLimiter {
	p.mu.RLock()
	limiter, exists := p.limiters[key]
	p.mu.RUnlock()
	
	if exists {
		return limiter
	}
	
	p.mu.Lock()
	defer p.mu.Unlock()
	
	// Double-check after write lock
	if limiter, exists := p.limiters[key]; exists {
		return limiter
	}
	
	limiter = NewHybridRateLimiter(
		p.config.Capacity,
		p.config.Refill,
		p.config.Interval,
		p.config.WindowSize,
		p.config.RequestLimit,
	)
	p.limiters[key] = limiter
	
	// Opportunistic cleanup
	now := time.Now()
	if now.Sub(p.lastCleanup) > p.cleanupPeriod {
		p.cleanupStale(now)
		p.lastCleanup = now
	}
	
	return limiter
}

func (p *PerKeyRateLimiter) cleanupStale(now time.Time) {
	// Remove limiters that haven't been used recently
	cutoff := now.Add(-30 * time.Minute)
	
	for key, limiter := range p.limiters {
		limiter.mu.Lock()
		lastUsed := limiter.updated
		limiter.mu.Unlock()
		
		if lastUsed.Before(cutoff) {
			delete(p.limiters, key)
		}
	}
}

// GetAllStats returns stats for all active limiters
func (p *PerKeyRateLimiter) GetAllStats() map[string]RateLimiterStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	
	stats := make(map[string]RateLimiterStats, len(p.limiters))
	for key, limiter := range p.limiters {
		stats[key] = limiter.Stats()
	}
	return stats
}
