//! Resilience telemetry metrics (retry + circuit breaker) aligning with design doc sections 5.3 & 6.x.
use once_cell::sync::Lazy;
use opentelemetry::metrics::{Counter, Histogram, Meter, Unit};
use std::time::Duration;

static METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("swarm_resilience"));

#[derive(Clone)]
pub struct ResilienceMetrics {
    pub retry_attempts: Counter<u64>,
    pub retry_failures: Counter<u64>,
    pub breaker_open: Counter<u64>,
    pub breaker_half_open: Counter<u64>,
    pub breaker_closed: Counter<u64>,
    pub retry_delay_ms: Histogram<f64>,
}

/// Register and return resilience metrics (idempotent).
pub fn register_metrics() -> ResilienceMetrics {
    ResilienceMetrics {
        retry_attempts: METER
            .u64_counter("swarm_resilience_retry_attempts")
            .with_description("Total retry attempts executed")
            .init(),
        retry_failures: METER
            .u64_counter("swarm_resilience_retry_failures")
            .with_description("Total retries that exhausted all attempts")
            .init(),
        breaker_open: METER
            .u64_counter("swarm_resilience_breaker_open_total")
            .with_description("Circuit breaker transitions to OPEN")
            .init(),
        breaker_half_open: METER
            .u64_counter("swarm_resilience_breaker_half_open_total")
            .with_description("Circuit breaker transitions to HALF_OPEN")
            .init(),
        breaker_closed: METER
            .u64_counter("swarm_resilience_breaker_closed_total")
            .with_description("Circuit breaker transitions to CLOSED")
            .init(),
        retry_delay_ms: METER
            .f64_histogram("swarm_resilience_retry_delay_ms")
            .with_description("Observed retry backoff delays in milliseconds")
            .with_unit(Unit::new("ms"))
            .init(),
    }
}

pub fn record_delay(metrics: &ResilienceMetrics, d: Duration) { metrics.retry_delay_ms.record(d.as_millis() as f64, &[]); }
