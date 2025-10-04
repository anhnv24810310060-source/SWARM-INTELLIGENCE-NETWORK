package resilience

import (
	"context"
	"math"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
)

// CircuitBreaker is an adaptive circuit breaker that opens based on failure rate over a rolling window
// and supports half-open probes.
type CircuitBreaker struct {
	mu sync.Mutex

	// config
	minSamples        int           // minimum requests before evaluating
	failureRateOpen   float64       // baseline failure rate threshold to open (0-1)
	halfOpenAfter     time.Duration // cool-down period
	maxHalfOpenProbes int           // number of allowed test requests in half-open
	adaptive          bool          // enable adaptive thresholding based on recent error volatility
	minAdaptiveOpen   float64       // lower bound for adaptive threshold
	maxAdaptiveOpen   float64       // upper bound for adaptive threshold
	lastEval          time.Time     // last adaptive evaluation
	evalInterval      time.Duration // how often to recompute adaptive threshold
	dynamicThreshold  float64       // current computed threshold

	// state
	openedAt       time.Time
	state          breakerState
	window         *slidingWindow
	halfOpenProbes int
}

type breakerState int

const (
	stateClosed breakerState = iota
	stateOpen
	stateHalfOpen
)

// NewCircuitBreakerAdaptive constructs a breaker using a rolling window of size with bucket resolution.
func NewCircuitBreakerAdaptive(windowSize time.Duration, buckets int, minSamples int, failureRateOpen float64, halfOpenAfter time.Duration, maxHalfOpenProbes int) *CircuitBreaker {
	if buckets <= 0 {
		buckets = 1
	}
	return &CircuitBreaker{
		minSamples:        minSamples,
		failureRateOpen:   math.Min(math.Max(failureRateOpen, 0), 1),
		halfOpenAfter:     halfOpenAfter,
		maxHalfOpenProbes: maxHalfOpenProbes,
		state:             stateClosed,
		window:            newSlidingWindow(windowSize, buckets),
		adaptive:          true,
		minAdaptiveOpen:   math.Min(math.Max(failureRateOpen*0.5, 0.05), failureRateOpen),
		maxAdaptiveOpen:   math.Min(0.95, math.Max(failureRateOpen*1.5, failureRateOpen)),
		evalInterval:      5 * time.Second,
		dynamicThreshold:  failureRateOpen,
	}
}

// Allow returns whether a request is permitted.
func (c *CircuitBreaker) Allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.state {
	case stateOpen:
		if time.Since(c.openedAt) >= c.halfOpenAfter {
			c.state = stateHalfOpen
			c.halfOpenProbes = 0
		} else {
			return false
		}
	case stateHalfOpen:
		if c.halfOpenProbes >= c.maxHalfOpenProbes {
			return false
		}
		c.halfOpenProbes++
	}
	return true
}

// RecordResult records a success or failure outcome.
func (c *CircuitBreaker) RecordResult(success bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.window.add(success)

	// Recompute adaptive threshold periodically if enabled
	if c.adaptive && time.Since(c.lastEval) >= c.evalInterval {
		total, failures := c.window.stats()
		if total > 0 {
			fr := float64(failures) / float64(total)
			// Adjust threshold leaning away from transient spikes: EMA-like smoothing
			// If current failure rate high, threshold clamps to minAdaptiveOpen to trip faster.
			// If low failure rate sustained, gradually raise threshold (up to maxAdaptiveOpen) to avoid flapping.
			if fr > c.failureRateOpen {
				c.dynamicThreshold = math.Max(c.minAdaptiveOpen, c.dynamicThreshold*0.7)
			} else {
				c.dynamicThreshold = math.Min(c.maxAdaptiveOpen, c.dynamicThreshold*1.05)
			}
		}
		c.lastEval = time.Now()
	}

	switch c.state {
	case stateClosed:
		total, failures := c.window.stats()
		if total >= c.minSamples {
			threshold := c.failureRateOpen
			if c.adaptive {
				threshold = c.dynamicThreshold
			}
			if float64(failures)/float64(total) >= threshold {
				c.transitionToOpen()
			}
		}
	case stateHalfOpen:
		if !success {
			c.transitionToOpen()
		} else if c.halfOpenProbes >= c.maxHalfOpenProbes {
			// all probes succeeded
			c.reset()
		}
	case stateOpen:
		// nothing, Allow handles timing
	}
}

func (c *CircuitBreaker) transitionToOpen() {
	meter := otel.GetMeterProvider().Meter("swarm-go")
	c.state = stateOpen
	c.openedAt = time.Now()
	counter, _ := meter.Int64Counter("swarm_resilience_circuit_open_total")
	counter.Add(context.Background(), 1)
}

func (c *CircuitBreaker) reset() {
	meter := otel.GetMeterProvider().Meter("swarm-go")
	c.state = stateClosed
	c.openedAt = time.Time{}
	c.window.reset()
	counter, _ := meter.Int64Counter("swarm_resilience_circuit_closed_total")
	counter.Add(context.Background(), 1)
}

// slidingWindow implements fixed-size time buckets storing success/failure counts.
type slidingWindow struct {
	size     time.Duration
	buckets  int
	interval time.Duration
	data     []bucket
	nowFn    func() time.Time
}

type bucket struct{ success, fail int }

func newSlidingWindow(size time.Duration, buckets int) *slidingWindow {
	return &slidingWindow{
		size:     size,
		buckets:  buckets,
		interval: size / time.Duration(buckets),
		data:     make([]bucket, buckets),
		nowFn:    time.Now,
	}
}

func (w *slidingWindow) currentIndex(now time.Time) int {
	return int(now.UnixNano()/w.interval.Nanoseconds()) % w.buckets
}

func (w *slidingWindow) add(success bool) {
	now := w.nowFn()
	idx := w.currentIndex(now)
	// reset bucket when interval changed
	w.data[idx] = bucket{}
	if success {
		w.data[idx].success++
	} else {
		w.data[idx].fail++
	}
}

func (w *slidingWindow) stats() (total int, failures int) {
	for _, b := range w.data {
		total += b.success + b.fail
		failures += b.fail
	}
	return
}

func (w *slidingWindow) reset() {
	for i := range w.data {
		w.data[i] = bucket{}
	}
}
