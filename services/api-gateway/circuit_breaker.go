package main
package main

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Circuit breaker states
type cbState int

const (
	cbClosed cbState = iota
	cbOpen
	cbHalfOpen
)

var (
	// ErrCircuitOpen is returned when circuit is open
	ErrCircuitOpen = errors.New("circuit breaker open")
	// ErrTimeout is returned on operation timeout
	ErrTimeout = errors.New("operation timeout")
)

// CircuitBreaker implements adaptive circuit breaker pattern
// Uses exponential moving average for latency tracking and dynamic threshold adjustment
type CircuitBreaker struct {
	mu sync.Mutex

	// Configuration
	maxFailures      int           // consecutive failures to open
	timeout          time.Duration // operation timeout
	cooldownDuration time.Duration // how long to stay open
	halfOpenRequests int           // requests to test in half-open

	// State
	state            cbState
	failures         int
	successes        int
	lastFailTime     time.Time
	lastStateChange  time.Time
	halfOpenAttempts int

	// Adaptive threshold (exponential moving average)
	avgLatency float64 // milliseconds
	emaAlpha   float64 // smoothing factor (0.2 = weight recent 20%)
	
	// Metrics
	totalRequests uint64
	totalFailures uint64
	totalTimeouts uint64
}

// NewCircuitBreaker creates a circuit breaker with adaptive thresholds
func NewCircuitBreaker(maxFailures int, timeout, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:      maxFailures,
		timeout:          timeout,
		cooldownDuration: cooldown,
		halfOpenRequests: 3,
		state:            cbClosed,
		emaAlpha:         0.2,
		avgLatency:       100.0, // initial estimate: 100ms
	}
}

// Execute runs the operation with circuit breaker protection
func (cb *CircuitBreaker) Execute(ctx context.Context, op func(context.Context) error) error {
	cb.mu.Lock()
	cb.totalRequests++
	
	// Check circuit state
	switch cb.state {
	case cbOpen:
		// Check if cooldown expired
		if time.Since(cb.lastStateChange) > cb.cooldownDuration {
			cb.transitionTo(cbHalfOpen)
		} else {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}
	case cbHalfOpen:
		if cb.halfOpenAttempts >= cb.halfOpenRequests {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}
		cb.halfOpenAttempts++
	}
	cb.mu.Unlock()

	// Execute with timeout
	start := time.Now()
	errCh := make(chan error, 1)
	
	opCtx, cancel := context.WithTimeout(ctx, cb.timeout)
	defer cancel()

	go func() {
		errCh <- op(opCtx)
	}()

	var err error
	select {
	case err = <-errCh:
		// Operation completed
	case <-opCtx.Done():
		err = ErrTimeout
	}

	latency := float64(time.Since(start).Milliseconds())

	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Update adaptive latency threshold using EMA
	cb.avgLatency = cb.emaAlpha*latency + (1-cb.emaAlpha)*cb.avgLatency

	if err != nil {
		cb.recordFailure(err, latency)
	} else {
		cb.recordSuccess(latency)
	}

	return err
}

func (cb *CircuitBreaker) recordSuccess(latency float64) {
	cb.successes++
	cb.failures = 0

	// If in half-open and got enough successes, close the circuit
	if cb.state == cbHalfOpen && cb.successes >= cb.halfOpenRequests {
		cb.transitionTo(cbClosed)
	}
}

func (cb *CircuitBreaker) recordFailure(err error, latency float64) {
	cb.totalFailures++
	if errors.Is(err, ErrTimeout) {
		cb.totalTimeouts++
	}

	cb.failures++
	cb.successes = 0
	cb.lastFailTime = time.Now()

	// Adaptive threshold: if latency > 3x average, count as slow failure
	slowThreshold := cb.avgLatency * 3.0
	isSlow := latency > slowThreshold

	// Open circuit if failures exceed threshold OR consistent slow requests
	if cb.failures >= cb.maxFailures || (isSlow && cb.failures >= cb.maxFailures/2) {
		cb.transitionTo(cbOpen)
	}
}

func (cb *CircuitBreaker) transitionTo(newState cbState) {
	if cb.state == newState {
		return
	}

	cb.state = newState
	cb.lastStateChange = time.Now()
	cb.halfOpenAttempts = 0

	switch newState {
	case cbClosed:
		cb.failures = 0
		cb.successes = 0
	case cbOpen:
		cb.successes = 0
	case cbHalfOpen:
		cb.successes = 0
		cb.failures = 0
	}
}

// Stats returns current circuit breaker statistics
func (cb *CircuitBreaker) Stats() CircuitBreakerStats {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return CircuitBreakerStats{
		State:          cb.state.String(),
		TotalRequests:  cb.totalRequests,
		TotalFailures:  cb.totalFailures,
		TotalTimeouts:  cb.totalTimeouts,
		AvgLatencyMs:   cb.avgLatency,
		Failures:       cb.failures,
		Successes:      cb.successes,
		LastStateChange: cb.lastStateChange,
	}
}

// CircuitBreakerStats holds metrics
type CircuitBreakerStats struct {
	State           string
	TotalRequests   uint64
	TotalFailures   uint64
	TotalTimeouts   uint64
	AvgLatencyMs    float64
	Failures        int
	Successes       int
	LastStateChange time.Time
}

func (s cbState) String() string {
	switch s {
	case cbClosed:
		return "closed"
	case cbOpen:
		return "open"
	case cbHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// CircuitBreakerPool manages multiple circuit breakers for different services
type CircuitBreakerPool struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
	config   CircuitBreakerConfig
}

// CircuitBreakerConfig holds default configuration
type CircuitBreakerConfig struct {
	MaxFailures int
	Timeout     time.Duration
	Cooldown    time.Duration
}

// NewCircuitBreakerPool creates a pool with default config
func NewCircuitBreakerPool(config CircuitBreakerConfig) *CircuitBreakerPool {
	return &CircuitBreakerPool{
		breakers: make(map[string]*CircuitBreaker),
		config:   config,
	}
}

// Get returns or creates a circuit breaker for service
func (p *CircuitBreakerPool) Get(service string) *CircuitBreaker {
	p.mu.RLock()
	cb, exists := p.breakers[service]
	p.mu.RUnlock()

	if exists {
		return cb
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists := p.breakers[service]; exists {
		return cb
	}

	cb = NewCircuitBreaker(p.config.MaxFailures, p.config.Timeout, p.config.Cooldown)
	p.breakers[service] = cb
	return cb
}

// GetAllStats returns stats for all circuit breakers
func (p *CircuitBreakerPool) GetAllStats() map[string]CircuitBreakerStats {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := make(map[string]CircuitBreakerStats, len(p.breakers))
	for name, cb := range p.breakers {
		stats[name] = cb.Stats()
	}
	return stats
}
