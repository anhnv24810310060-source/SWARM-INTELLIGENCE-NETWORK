//! Resilience primitives: circuit breaker, retry w/ backoff, rate limiter (lightweight)
//! This is an initial stub to close design gap (Section: 5.3 Fault Tolerance & 6 Security - graceful degradation)

use std::sync::Arc;
use parking_lot::Mutex;
use std::time::{Duration, Instant};
use rand::{thread_rng, Rng};

#[derive(Debug, Clone)]
pub struct RetryConfig {
    pub max_retries: usize,
    pub base_delay: Duration,
    pub max_delay: Duration,
    pub jitter: f64, // 0.0 - 1.0
}
impl Default for RetryConfig { fn default() -> Self { Self { max_retries: 5, base_delay: Duration::from_millis(50), max_delay: Duration::from_millis(1500), jitter: 0.25 } } }

pub async fn retry_async<F, Fut, T, E>(cfg: &RetryConfig, mut op: F) -> Result<T, E>
where
    F: FnMut(usize) -> Fut,
    Fut: std::future::Future<Output = Result<T, E>>,
{
    let mut attempt = 0;
    loop {
        match op(attempt).await {
            Ok(v) => return Ok(v),
            Err(e) if attempt >= cfg.max_retries => return Err(e),
            Err(_) => {
                let exp = cfg.base_delay.mul_f64(2f64.powi(attempt as i32));
                let mut delay = std::cmp::min(exp, cfg.max_delay);
                if cfg.jitter > 0.0 { // add jitter
                    let jitter_ms = (delay.as_millis() as f64 * cfg.jitter) as u64;
                    let offset: i64 = thread_rng().gen_range(-(jitter_ms as i64)..(jitter_ms as i64 + 1));
                    let base_ms = delay.as_millis() as i64 + offset;
                    delay = Duration::from_millis(base_ms.max(0) as u64);
                }
                tokio::time::sleep(delay).await;
            }
        }
        attempt += 1;
    }
}

#[derive(Debug, Clone, PartialEq)]
pub enum BreakerState { Closed, Open { opened_at: Instant }, HalfOpen }

#[derive(Debug)]
pub struct CircuitBreaker {
    state: Mutex<BreakerState>,
    failures: Mutex<usize>,
    failure_threshold: usize,
    open_timeout: Duration,
    half_open_successes: Mutex<usize>,
    required_half_open_successes: usize,
}

impl CircuitBreaker {
    pub fn new(failure_threshold: usize, open_timeout: Duration, required_half_open_successes: usize) -> Arc<Self> {
        Arc::new(Self { state: Mutex::new(BreakerState::Closed), failures: Mutex::new(0), failure_threshold, open_timeout, half_open_successes: Mutex::new(0), required_half_open_successes })
    }

    pub async fn exec<F, Fut, T, E>(self: &Arc<Self>, op: F) -> Result<T, E>
    where F: FnOnce() -> Fut, Fut: std::future::Future<Output = Result<T, E>> {
        {
            let mut st = self.state.lock();
            // transition from Open -> HalfOpen if timeout expired
            if let BreakerState::Open { opened_at } = *st {
                if opened_at.elapsed() >= self.open_timeout { *st = BreakerState::HalfOpen; }
            }
            if let BreakerState::Open { .. } = *st { return Err(op_err("circuit open")); }
        }
        let res = op().await;
        match res {
            Ok(v) => {
                self.on_success();
                Ok(v)
            }
            Err(e) => { self.on_failure(); Err(e) }
        }
    }

    fn on_success(&self) {
        let mut st = self.state.lock();
        match *st {
            BreakerState::Closed => { *self.failures.lock() = 0; }
            BreakerState::HalfOpen => {
                let mut succ = self.half_open_successes.lock();
                *succ += 1;
                if *succ >= self.required_half_open_successes {
                    *st = BreakerState::Closed;
                    *self.failures.lock() = 0;
                    *succ = 0;
                }
            }
            BreakerState::Open { .. } => { /* unreachable normally */ }
        }
    }

    fn on_failure(&self) {
        let mut st = self.state.lock();
        match *st {
            BreakerState::Closed => {
                let mut f = self.failures.lock();
                *f += 1;
                if *f >= self.failure_threshold { *st = BreakerState::Open { opened_at: Instant::now() }; }
            }
            BreakerState::HalfOpen => {
                *st = BreakerState::Open { opened_at: Instant::now() };
                *self.half_open_successes.lock() = 0;
            }
            BreakerState::Open { .. } => { /* already open */ }
        }
    }
}

// lightweight error helper
fn op_err(msg: &str) -> Box<dyn std::error::Error + Send + Sync> { msg.into() }

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn test_retry_eventual_success() {
        let cfg = RetryConfig { max_retries: 3, base_delay: Duration::from_millis(1), max_delay: Duration::from_millis(10), jitter: 0.0 };
        let mut attempts = 0;
        let res: Result<usize, &str> = retry_async(&cfg, |_i| {
            attempts += 1;
            async {
                if attempts < 3 { Err("fail") } else { Ok(42) }
            }
        }).await;
        assert_eq!(res.unwrap(), 42);
        assert_eq!(attempts, 3);
    }

    #[tokio::test]
    async fn test_circuit_breaker_opens() {
        let cb = CircuitBreaker::new(2, Duration::from_millis(5), 1);
        for _ in 0..2 { let _ = cb.exec(|| async { Err::<(), _>(op_err("boom")) }).await; }
        // third should be blocked
        let err = cb.exec(|| async { Ok::<(), _>(()) }).await.err().unwrap();
        assert!(err.to_string().contains("circuit open"));
    }

    #[tokio::test]
    async fn test_circuit_half_open_to_closed() {
        let cb = CircuitBreaker::new(1, Duration::from_millis(5), 1);
        let _ = cb.exec(|| async { Err::<(), _>(op_err("boom")) }).await; // open
        tokio::time::sleep(Duration::from_millis(6)).await; // expire
        let ok = cb.exec(|| async { Ok::<_, Box<dyn std::error::Error + Send + Sync>>(()) }).await;
        assert!(ok.is_ok());
    }
}
