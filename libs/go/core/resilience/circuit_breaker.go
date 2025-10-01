package resilience

import (
	"sync"
	"time"
)

type CircuitBreaker struct {
	mu               sync.Mutex
	failures         int
	failureThreshold int
	openedAt         time.Time
	halfOpenAfter    time.Duration
}

func NewCircuitBreaker(threshold int, halfOpenAfter time.Duration) *CircuitBreaker {
	return &CircuitBreaker{failureThreshold: threshold, halfOpenAfter: halfOpenAfter}
}

func (c *CircuitBreaker) Allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.openedAt.IsZero() {
		if time.Since(c.openedAt) >= c.halfOpenAfter {
			c.openedAt = time.Time{}
			c.failures = 0
			return true
		}
		return false
	}
	return true
}

func (c *CircuitBreaker) RecordSuccess() { c.mu.Lock(); c.failures = 0; c.mu.Unlock() }
func (c *CircuitBreaker) RecordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= c.failureThreshold && c.openedAt.IsZero() {
		c.openedAt = time.Now()
	}
}
