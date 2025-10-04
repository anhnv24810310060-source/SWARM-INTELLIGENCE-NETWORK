/// Standalone tests for resilience_advanced module
/// Run with: cargo test --test resilience_standalone

mod resilience_advanced;

use resilience_advanced::*;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, Instant};
use tokio::time::sleep;

#[tokio::test]
async fn test_circuit_breaker_basic() {
    let cb = CircuitBreaker::new(
        "test".to_string(),
        CircuitBreakerConfig {
            failure_threshold: 0.5,
            min_requests: 2,
            success_threshold: 2,
            timeout: Duration::from_millis(100),
            window_size: Duration::from_secs(10),
            buckets: 5,
        },
    );
    
    // Initially closed
    assert_eq!(cb.state(), CircuitState::Closed);
    assert!(cb.allow());
    
    // Record failures
    cb.record_result(false);
    cb.record_result(false);
    
    // Should be open
    assert_eq!(cb.state(), CircuitState::Open);
    assert!(!cb.allow());
    
    println!("Circuit breaker opened successfully");
}

#[tokio::test]
async fn test_rate_limiter_basic() {
    let rl = RateLimiter::new(10, 10.0);
    
    // Should allow 10 requests
    for i in 0..10 {
        assert!(rl.acquire(1), "Failed to acquire token {}", i);
    }
    
    // 11th should fail
    assert!(!rl.acquire(1));
    
    println!("Rate limiter working correctly");
}

#[tokio::test]
async fn test_bulkhead_basic() {
    let bh = Bulkhead::new("test".to_string(), 2, 1);
    
    let permit1 = bh.try_acquire().unwrap();
    let permit2 = bh.try_acquire().unwrap();
    
    // Should fail (at capacity)
    assert!(bh.try_acquire().is_none());
    
    drop(permit1);
    
    // Should succeed after release
    assert!(bh.try_acquire().is_some());
    
    println!("Bulkhead working correctly");
}

#[tokio::test]
async fn test_retry_executor() {
    let retry = RetryExecutor::new(RetryConfig {
        max_attempts: 3,
        base_delay: Duration::from_millis(10),
        max_delay: Duration::from_millis(100),
        multiplier: 2.0,
        jitter: false,
    });
    
    let counter = Arc::new(AtomicUsize::new(0));
    let counter_clone = counter.clone();
    
    let result = retry
        .execute(|| {
            let c = counter_clone.clone();
            async move {
                let attempt = c.fetch_add(1, Ordering::SeqCst) + 1;
                if attempt < 3 {
                    Err(format!("Attempt {} failed", attempt))
                } else {
                    Ok("Success!")
                }
            }
        })
        .await;
    
    assert_eq!(result, Ok("Success!"));
    assert_eq!(counter.load(Ordering::SeqCst), 3);
    
    println!("Retry executor working correctly");
}

#[tokio::test]
async fn test_performance_circuit_breaker() {
    let cb = CircuitBreaker::new(
        "perf_test".to_string(),
        CircuitBreakerConfig::default(),
    );
    
    let iterations = 10000;
    let start = Instant::now();
    
    for _ in 0..iterations {
        let _ = cb.allow();
    }
    
    let elapsed = start.elapsed();
    let avg_ns = elapsed.as_nanos() / iterations;
    
    println!("Circuit breaker allow() average: {}ns", avg_ns);
    assert!(avg_ns < 10_000, "Circuit breaker too slow: {}ns", avg_ns);
}

#[test]
fn test_resilience_manager_stats() {
    let mgr = ResilienceManager::new();
    
    let _cb = mgr.register_circuit_breaker(
        "api1".to_string(),
        CircuitBreakerConfig::default(),
    );
    
    let _cb2 = mgr.register_circuit_breaker(
        "api2".to_string(),
        CircuitBreakerConfig::default(),
    );
    
    let stats = mgr.stats();
    
    assert_eq!(stats.circuit_breakers.len(), 2);
    assert!(stats.circuit_breakers.contains_key("api1"));
    assert!(stats.circuit_breakers.contains_key("api2"));
    
    println!("Resilience manager stats: {:?}", stats);
}
