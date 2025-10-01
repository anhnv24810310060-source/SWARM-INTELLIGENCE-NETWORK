package resilience

import (
	"context"
	"time"
)

func Retry[T any](ctx context.Context, attempts int, delay time.Duration, fn func() (T, error)) (T, error) {
	var zero T
	var err error
	for i := 0; i < attempts; i++ {
		var v T
		v, err = fn()
		if err == nil {
			return v, nil
		}
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}
	return zero, err
}
