package resilience

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// HybridRateLimiter combines Token Bucket (burst tolerance) and Leaky Bucket (rate smoothing)
// 
// Algorithm:
// - Token Bucket: Allows bursts up to capacity, tokens refill at constant rate
// - Leaky Bucket: Enforces strict output rate by queuing requests
// 
// Hybrid approach:
// 1. Check token bucket first (fast path for bursty traffic)
// 2. If no tokens, queue in leaky bucket (fair scheduling)
// 3. Background worker processes queue at constant rate
//
// Advantages:
// - Handles bursts gracefully (unlike pure leaky bucket)
// - Prevents sustained overload (unlike pure token bucket)
// - Predictable latency under load
type HybridRateLimiter struct {
	// Token bucket state
	tokens          float64
	capacity        float64
	refillRate      float64 // tokens per second
	lastRefill      time.Time
	tokenMu         sync.Mutex

	// Leaky bucket queue
	queue           chan *queuedRequest
	queueSize       int
	leakRate        time.Duration // interval between processing queue items
	stopCh          chan struct{}
	workerWg        sync.WaitGroup

	// Metrics
	allowedCounter  metric.Int64Counter
	deniedCounter   metric.Int64Counter
	queuedCounter   metric.Int64Counter
	tokensGauge     metric.Float64Gauge
	queueLenGauge   metric.Int64Gauge
}

type queuedRequest struct {
	doneCh chan struct{}
}

// NewHybridRateLimiter creates a hybrid rate limiter
//
// Parameters:
// - burstCapacity: max tokens (burst size)
// - refillRate: tokens/second
// - queueSize: max queued requests (excess denied)
// - leakRate: processing interval for queue
func NewHybridRateLimiter(burstCapacity int, refillRate float64, queueSize int, leakRate time.Duration) *HybridRateLimiter {
	meter := otel.GetMeterProvider().Meter("swarm-resilience")

	allowed, _ := meter.Int64Counter("swarm_ratelimit_hybrid_allowed_total")
	denied, _ := meter.Int64Counter("swarm_ratelimit_hybrid_denied_total")
	queued, _ := meter.Int64Counter("swarm_ratelimit_hybrid_queued_total")
	tokensGauge, _ := meter.Float64Gauge("swarm_ratelimit_hybrid_tokens_available")
	queueLen, _ := meter.Int64Gauge("swarm_ratelimit_hybrid_queue_length")

	rl := &HybridRateLimiter{
		tokens:          float64(burstCapacity),
		capacity:        float64(burstCapacity),
		refillRate:      refillRate,
		lastRefill:      time.Now(),
		queue:           make(chan *queuedRequest, queueSize),
		queueSize:       queueSize,
		leakRate:        leakRate,
		stopCh:          make(chan struct{}),
		allowedCounter:  allowed,
		deniedCounter:   denied,
		queuedCounter:   queued,
		tokensGauge:     tokensGauge,
		queueLenGauge:   queueLen,
	}

	// Start leaky bucket worker
	rl.workerWg.Add(1)
	go rl.leakyBucketWorker()

	// Start metrics reporter
	go rl.reportMetrics()

	return rl
}

// Allow checks if request can proceed immediately (token bucket)
//
// Returns:
// - true: proceed immediately (consumed token)
// - false: no tokens, caller should call Wait() to queue
func (rl *HybridRateLimiter) Allow(ctx context.Context) bool {
	rl.refillTokens()

	rl.tokenMu.Lock()
	defer rl.tokenMu.Unlock()

	if rl.tokens >= 1.0 {
		rl.tokens -= 1.0
		rl.allowedCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", "immediate")))
		return true
	}

	return false
}

// Wait queues request if no immediate tokens available (leaky bucket)
//
// Returns:
// - nil: request was queued and processed
// - error: queue full, request denied
func (rl *HybridRateLimiter) Wait(ctx context.Context) error {
	req := &queuedRequest{
		doneCh: make(chan struct{}),
	}

	// Try to queue
	select {
	case rl.queue <- req:
		rl.queuedCounter.Add(ctx, 1)

		// Wait for processing
		select {
		case <-req.doneCh:
			rl.allowedCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", "queued")))
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-rl.stopCh:
			return context.Canceled
		}

	default:
		// Queue full, deny request
		rl.deniedCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", "queue_full")))
		return ErrRateLimitExceeded
	}
}

// AllowOrWait combines Allow + Wait with single call
func (rl *HybridRateLimiter) AllowOrWait(ctx context.Context) error {
	if rl.Allow(ctx) {
		return nil
	}
	return rl.Wait(ctx)
}

// refillTokens adds tokens based on elapsed time
func (rl *HybridRateLimiter) refillTokens() {
	rl.tokenMu.Lock()
	defer rl.tokenMu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()

	if elapsed > 0 {
		tokensToAdd := elapsed * rl.refillRate
		rl.tokens = minFloat64(rl.capacity, rl.tokens+tokensToAdd)
		rl.lastRefill = now
	}
}

// leakyBucketWorker processes queue at constant rate
func (rl *HybridRateLimiter) leakyBucketWorker() {
	defer rl.workerWg.Done()

	ticker := time.NewTicker(rl.leakRate)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Process one item from queue
			select {
			case req := <-rl.queue:
				close(req.doneCh) // Signal completion
			default:
				// Queue empty, nothing to do
			}

		case <-rl.stopCh:
			return
		}
	}
}

// reportMetrics updates gauge metrics periodically
func (rl *HybridRateLimiter) reportMetrics() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx := context.Background()

			rl.tokenMu.Lock()
			tokens := rl.tokens
			rl.tokenMu.Unlock()

			rl.tokensGauge.Record(ctx, tokens)
			rl.queueLenGauge.Record(ctx, int64(len(rl.queue)))

		case <-rl.stopCh:
			return
		}
	}
}

// Stop gracefully shuts down the rate limiter
func (rl *HybridRateLimiter) Stop() {
	close(rl.stopCh)
	rl.workerWg.Wait()
}

// ErrRateLimitExceeded indicates request was denied
var ErrRateLimitExceeded = context.DeadlineExceeded

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
