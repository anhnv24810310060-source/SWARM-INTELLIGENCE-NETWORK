package resilience

import (
	"context"
	"math/rand"
	"time"

	"go.opentelemetry.io/otel"
)

// Retry executes fn with exponential backoff (base delay) + full jitter.
// delay acts as initial backoff; grows exponentially (x2) until attempts exhausted.
// Jitter: random duration in [0, currentDelay].
func Retry[T any](ctx context.Context, attempts int, delay time.Duration, fn func() (T, error)) (T, error) {
	var zero T
	if attempts <= 0 {
		return zero, nil
	}
	cur := delay
	var lastErr error
	meter := otel.Meter("swarm-go")
	attemptCounter, _ := meter.Int64Counter("swarm_resilience_retry_attempts_total")
	successCounter, _ := meter.Int64Counter("swarm_resilience_retry_success_total")
	failCounter, _ := meter.Int64Counter("swarm_resilience_retry_fail_total")
	for i := 0; i < attempts; i++ {
		v, err := fn()
		attemptCounter.Add(ctx, 1)
		if err == nil {
			successCounter.Add(ctx, 1)
			return v, nil
		}
		lastErr = err
		if i == attempts-1 {
			break
		}
		// exponential growth (cap at ~60s to avoid runaway)
		if cur > 60*time.Second {
			cur = 60 * time.Second
		}
		// full jitter
		sleep := time.Duration(rand.Int63n(int64(cur) + 1))
		select {
		case <-ctx.Done():
			failCounter.Add(ctx, 1)
			return zero, ctx.Err()
		case <-time.After(sleep):
		}
		cur *= 2
	}
	failCounter.Add(ctx, 1)
	return zero, lastErr
}
