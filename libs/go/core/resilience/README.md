Resilience Library
==================

Components:
- Adaptive Circuit Breaker: failure-rate based with rolling window & half-open probes.
- Rate Limiter: token bucket + sliding window hard cap; metrics for drops.
- Retry helper: generic retry with attempt metrics.

Metrics Emitted:
- swarm_resilience_circuit_open_total
- swarm_resilience_circuit_closed_total
- swarm_ratelimiter_token_drops_total
- swarm_ratelimiter_window_drops_total
- swarm_resilience_retry_attempts_total

Usage Example:
```go
cb := NewCircuitBreakerAdaptive(10*time.Second, 10, 20, 0.5, 2*time.Second, 3)
if cb.Allow() {
    res, err := call()
    cb.RecordResult(err == nil)
}

rl := NewRateLimiter(100, 50, time.Second, 500)
if !rl.Allow() { /* shed load */ }
```

Design Notes:
- Rolling window uses fixed bucket time slicing for O(1) updates.
- Half-open uses limited probe count to reduce thundering herd.
- Rate limiter lazy-refills to avoid background goroutines.
