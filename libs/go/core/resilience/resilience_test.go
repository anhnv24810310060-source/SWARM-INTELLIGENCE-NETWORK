package resilience
package resilience

import (
	"testing"
	"time"
)

func TestRateLimiterBasic(t *testing.T) {
	rl := NewRateLimiter(5, 5, time.Second, 10)
	// consume 5
	for i := 0; i < 5; i++ {
		if !rl.Allow() { t.Fatalf("expected allow %d", i) }
	}
	if rl.Allow() { t.Fatalf("expected deny after capacity") }
	// wait refill
	time.Sleep(1100 * time.Millisecond)
	if !rl.Allow() { t.Fatalf("expected allow after refill") }
}

func TestCircuitBreakerAdaptive(t *testing.T) {
	cb := NewCircuitBreakerAdaptive(2*time.Second, 4, 4, 0.5, 500*time.Millisecond, 2)
	// 4 failures -> open
	for i := 0; i < 4; i++ {
		if !cb.Allow() { t.Fatalf("should allow while closed") }
		cb.RecordResult(false)
	}
	if cb.Allow() { t.Fatalf("should be open and deny") }
	// wait half-open
	time.Sleep(600 * time.Millisecond)
	if !cb.Allow() { t.Fatalf("half-open probe should allow") }
	cb.RecordResult(true)
	if !cb.Allow() { t.Fatalf("second probe should allow") }
	cb.RecordResult(true)
	// after two successes should be closed again
	if !cb.Allow() { t.Fatalf("breaker should be closed after successful probes") }
}
