//! Resilience utilities: retry + circuit breaker (minimal MVP)
use std::time::{Duration, Instant};
use thiserror::Error;
use parking_lot::Mutex;
use futures::Future;
use tracing::{warn, debug};
use opentelemetry::{global, metrics::Counter};
use once_cell::sync::Lazy;

static RETRY_ATTEMPTS: Lazy<Counter<u64>> = Lazy::new(|| {
    global::meter("swarm-resilience")
        .u64_counter("swarm_resilience_retry_attempts_total")
        .with_description("Total retry attempts executed")
        .init()
});

static CIRCUIT_OPEN: Lazy<Counter<u64>> = Lazy::new(|| {
    global::meter("swarm-resilience")
        .u64_counter("swarm_resilience_circuit_open_total")
        .with_description("Number of times circuit transitioned to open")
        .init()
});

#[derive(Debug, Error)]
pub enum ResilienceError { #[error("circuit open")] CircuitOpen }

pub async fn retry_async<F, Fut, T, E>(mut f: F, attempts: usize, delay: Duration) -> Result<T, E>
where F: FnMut() -> Fut, Fut: Future<Output = Result<T, E>> {
    let mut last_err = None;
    for i in 0..attempts {
        RETRY_ATTEMPTS.add(1, &[]);
        match f().await { Ok(v) => return Ok(v), Err(e) => { last_err = Some(e); if i+1 < attempts { tokio::time::sleep(delay).await; } } }
    }
    Err(last_err.unwrap())
}

pub struct CircuitBreaker {
    state: Mutex<State>,
    half_open_after: Duration,
    failure_threshold: u32,
}

struct State { failures: u32, opened_at: Option<Instant> }

impl CircuitBreaker {
    pub fn new(failure_threshold: u32, half_open_after: Duration) -> Self { Self { state: Mutex::new(State { failures:0, opened_at: None }), half_open_after, failure_threshold } }
    pub fn allow(&self) -> bool {
        let mut st = self.state.lock();
        if let Some(opened) = st.opened_at { if opened.elapsed() >= self.half_open_after { debug!("half-open trial"); st.opened_at=None; st.failures=0; return true; } else { return false; } }
        true
    }
    pub fn record_success(&self) { let mut st = self.state.lock(); st.failures=0; }
    pub fn record_failure(&self) { let mut st = self.state.lock(); st.failures+=1; if st.failures >= self.failure_threshold { if st.opened_at.is_none() { st.opened_at = Some(Instant::now()); CIRCUIT_OPEN.add(1, &[]); warn!("circuit opened"); } } }
}

#[cfg(test)]
mod tests { use super::*; #[tokio::test] async fn test_retry() { let mut c = 0; let res: Result<u32, &'static str> = retry_async(|| { c+=1; async move { if c<3 { Err("e") } else { Ok(42) } } }, 5, Duration::from_millis(1)).await; assert_eq!(res.unwrap(), 42); } }
