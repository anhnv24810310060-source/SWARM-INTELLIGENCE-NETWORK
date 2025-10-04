/// Integration tests for resilience patterns in production scenarios
/// 
/// Tests cover:
/// 1. Circuit breaker with real network failures
/// 2. Retry with exponential backoff under load
/// 3. Rate limiter with burst traffic
/// 4. Bulkhead preventing cascading failures
/// 5. Combined resilience patterns

use swarm_core::resilience_advanced::*;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::{Duration, Instant};
use tokio::time::sleep;
use tokio::task::JoinSet;

/// Simulate flaky service with configurable failure rate
struct FlakyService {
    failure_rate: f64,
    call_count: Arc<AtomicUsize>,
    success_count: Arc<AtomicUsize>,
}

impl FlakyService {
    fn new(failure_rate: f64) -> Self {
        Self {
            failure_rate,
            call_count: Arc::new(AtomicUsize::new(0)),
            success_count: Arc::new(AtomicUsize::new(0)),
        }
    }
    
    async fn call(&self) -> Result<String, String> {
        self.call_count.fetch_add(1, Ordering::SeqCst);
        
        sleep(Duration::from_millis(10)).await;
        
        if rand::random::<f64>() < self.failure_rate {
            Err("service error".to_string())
        } else {
            self.success_count.fetch_add(1, Ordering::SeqCst);
            Ok("success".to_string())
        }
    }
    
    fn stats(&self) -> (usize, usize) {
        (
            self.call_count.load(Ordering::SeqCst),
            self.success_count.load(Ordering::SeqCst),
        )
    }
}

#[tokio::test]
async fn test_circuit_breaker_protects_from_cascading_failures() {
    let service = Arc::new(FlakyService::new(0.8)); // 80% failure rate
    
    let cb = Arc::new(CircuitBreaker::new(
        "flaky_service".to_string(),
        CircuitBreakerConfig {
            failure_threshold: 0.5,
            min_requests: 5,
            success_threshold: 3,
            timeout: Duration::from_millis(500),
            window_size: Duration::from_secs(10),
            buckets: 10,
        },
    ));
    
    let mut rejected = 0;
    let mut total_calls = 0;
    
    // Make 100 calls
    for _ in 0..100 {
        if !cb.allow() {
            rejected += 1;
            continue;
        }
        
        total_calls += 1;
        let result = service.call().await;
        cb.record_result(result.is_ok());
        
        sleep(Duration::from_millis(5)).await;
    }
    
    let (calls, successes) = service.stats();
    let stats = cb.stats();
    
    println!("Circuit breaker test:");
    println!("  Total attempts: 100");
    println!("  Rejected by CB: {}", rejected);
    println!("  Service calls: {}", calls);
    println!("  Successes: {}", successes);
    println!("  CB state: {:?}", stats.state);
    println!("  Failure rate: {:.2}", stats.failure_rate);
    
    // Circuit breaker should have opened and rejected many requests
    assert!(rejected > 50, "Circuit breaker should reject requests");
    assert!(calls < 100, "Circuit breaker should prevent some calls");
    
    // Verify circuit opened
    assert_eq!(stats.state, CircuitState::Open);
}

#[tokio::test]
async fn test_retry_executor_eventual_success() {
    let attempt_counter = Arc::new(AtomicUsize::new(0));
    
    let retry = RetryExecutor::new(RetryConfig {
        max_attempts: 5,
        base_delay: Duration::from_millis(10),
        max_delay: Duration::from_millis(100),
        multiplier: 2.0,
        jitter: true,
    });
    
    let counter = attempt_counter.clone();
    let start = Instant::now();
    
    let result = retry
        .execute(|| {
            let c = counter.clone();
            async move {
                let attempt = c.fetch_add(1, Ordering::SeqCst) + 1;
                
                if attempt < 4 {
                    Err(format!("Attempt {} failed", attempt))
                } else {
                    Ok("Success!")
                }
            }
        })
        .await;
    
    let elapsed = start.elapsed();
    let attempts = attempt_counter.load(Ordering::SeqCst);
    
    println!("Retry executor test:");
    println!("  Attempts: {}", attempts);
    println!("  Result: {:?}", result);
    println!("  Elapsed: {:?}", elapsed);
    
    assert_eq!(result, Ok("Success!"));
    assert_eq!(attempts, 4);
    
    // Should have backoff delays: 10ms + 20ms + 40ms ≈ 70ms+ with jitter
    assert!(elapsed >= Duration::from_millis(60));
}

#[tokio::test]
async fn test_rate_limiter_burst_protection() {
    let limiter = Arc::new(RateLimiter::new(10, 10.0)); // 10 tokens, refill 10/sec
    
    let allowed = Arc::new(AtomicUsize::new(0));
    let rejected = Arc::new(AtomicUsize::new(0));
    
    // Burst of 100 requests
    let mut tasks = JoinSet::new();
    
    for _ in 0..100 {
        let limiter = limiter.clone();
        let allowed = allowed.clone();
        let rejected = rejected.clone();
        
        tasks.spawn(async move {
            if limiter.acquire(1) {
                allowed.fetch_add(1, Ordering::SeqCst);
            } else {
                rejected.fetch_add(1, Ordering::SeqCst);
            }
        });
    }
    
    while tasks.join_next().await.is_some() {}
    
    let allowed_count = allowed.load(Ordering::SeqCst);
    let rejected_count = rejected.load(Ordering::SeqCst);
    
    println!("Rate limiter test (burst):");
    println!("  Allowed: {}", allowed_count);
    println!("  Rejected: {}", rejected_count);
    println!("  Available tokens: {}", limiter.available());
    
    // Should allow ~10 initially, reject rest
    assert_eq!(allowed_count + rejected_count, 100);
    assert!(allowed_count <= 15, "Should not exceed capacity significantly");
    assert!(rejected_count >= 85, "Should reject most burst requests");
    
    // Wait for refill
    sleep(Duration::from_millis(200)).await;
    
    // Should refill ~2 tokens
    assert!(limiter.available() >= 2);
    assert!(limiter.acquire(2));
}

#[tokio::test]
async fn test_bulkhead_isolates_resources() {
    let bulkhead = Arc::new(Bulkhead::new(
        "worker_pool".to_string(),
        5,  // max 5 concurrent
        2,  // queue size 2
    ));
    
    let completed = Arc::new(AtomicUsize::new(0));
    let rejected = Arc::new(AtomicUsize::new(0));
    
    let mut tasks = JoinSet::new();
    
    // Spawn 20 tasks
    for i in 0..20 {
        let bulkhead = bulkhead.clone();
        let completed = completed.clone();
        let rejected = rejected.clone();
        
        tasks.spawn(async move {
            match bulkhead.try_acquire() {
                Some(_permit) => {
                    // Simulate work
                    sleep(Duration::from_millis(100)).await;
                    completed.fetch_add(1, Ordering::SeqCst);
                }
                None => {
                    rejected.fetch_add(1, Ordering::SeqCst);
                }
            }
        });
        
        sleep(Duration::from_millis(5)).await;
    }
    
    while tasks.join_next().await.is_some() {}
    
    let completed_count = completed.load(Ordering::SeqCst);
    let rejected_count = rejected.load(Ordering::SeqCst);
    
    println!("Bulkhead test:");
    println!("  Completed: {}", completed_count);
    println!("  Rejected: {}", rejected_count);
    println!("  Stats: {:?}", bulkhead.stats());
    
    assert_eq!(completed_count + rejected_count, 20);
    assert!(rejected_count > 0, "Should reject excess concurrent requests");
}

#[tokio::test]
async fn test_resilience_manager_integration() {
    let mgr = Arc::new(ResilienceManager::new());
    
    // Register all patterns for API service
    let cb = mgr.register_circuit_breaker(
        "api".to_string(),
        CircuitBreakerConfig::default(),
    );
    
    let rl = mgr.register_rate_limiter("api".to_string(), 100, 10.0);
    let bh = mgr.register_bulkhead("api".to_string(), 10, 5);
    
    // Simulate traffic
    let service = Arc::new(FlakyService::new(0.2)); // 20% failure
    
    let mut success = 0;
    let mut rate_limited = 0;
    let mut circuit_open = 0;
    let mut bulkhead_full = 0;
    
    for _ in 0..200 {
        // Check circuit breaker
        if !cb.allow() {
            circuit_open += 1;
            continue;
        }
        
        // Check rate limiter
        if !rl.acquire(1) {
            rate_limited += 1;
            continue;
        }
        
        // Check bulkhead
        let permit = match bh.try_acquire() {
            Some(p) => p,
            None => {
                bulkhead_full += 1;
                continue;
            }
        };
        
        // Make request
        let result = service.call().await;
        cb.record_result(result.is_ok());
        
        if result.is_ok() {
            success += 1;
        }
        
        drop(permit);
        sleep(Duration::from_millis(5)).await;
    }
    
    let stats = mgr.stats();
    
    println!("\nResilience manager integration:");
    println!("  Total requests: 200");
    println!("  Successes: {}", success);
    println!("  Circuit open: {}", circuit_open);
    println!("  Rate limited: {}", rate_limited);
    println!("  Bulkhead full: {}", bulkhead_full);
    println!("\nCircuit breaker stats: {:?}", stats.circuit_breakers.get("api"));
    println!("Bulkhead stats: {:?}", stats.bulkheads.get("api"));
    
    // Should have some successful requests
    assert!(success > 0);
    
    // Rate limiter should kick in for burst
    assert!(rate_limited > 50);
}

#[tokio::test]
async fn test_combined_patterns_under_load() {
    // Test all patterns together under realistic load
    let mgr = Arc::new(ResilienceManager::new());
    
    let cb = mgr.register_circuit_breaker(
        "backend".to_string(),
        CircuitBreakerConfig {
            failure_threshold: 0.3,
            min_requests: 10,
            success_threshold: 5,
            timeout: Duration::from_millis(200),
            window_size: Duration::from_secs(5),
            buckets: 10,
        },
    );
    
    let rl = mgr.register_rate_limiter("backend".to_string(), 50, 50.0);
    let bh = mgr.register_bulkhead("backend".to_string(), 20, 10);
    
    let retry = Arc::new(RetryExecutor::new(RetryConfig {
        max_attempts: 3,
        base_delay: Duration::from_millis(5),
        max_delay: Duration::from_millis(50),
        multiplier: 2.0,
        jitter: true,
    }));
    
    let service = Arc::new(FlakyService::new(0.15)); // 15% failure
    
    let start = Instant::now();
    let mut tasks = JoinSet::new();
    
    // Spawn 500 requests
    for _ in 0..500 {
        let cb = cb.clone();
        let rl = rl.clone();
        let bh = bh.clone();
        let retry = retry.clone();
        let service = service.clone();
        
        tasks.spawn(async move {
            // Circuit breaker check
            if !cb.allow() {
                return Err("circuit_open");
            }
            
            // Rate limiter check
            if !rl.acquire(1) {
                return Err("rate_limited");
            }
            
            // Bulkhead check
            let _permit = match bh.try_acquire() {
                Some(p) => p,
                None => return Err("bulkhead_full"),
            };
            
            // Retry wrapper
            let result = retry
                .execute(|| {
                    let svc = service.clone();
                    async move { svc.call().await }
                })
                .await;
            
            cb.record_result(result.is_ok());
            
            match result {
                Ok(_) => Ok("success"),
                Err(_) => Err("failed"),
            }
        });
    }
    
    let mut results = HashMap::new();
    while let Some(result) = tasks.join_next().await {
        match result {
            Ok(Ok(status)) => *results.entry(status).or_insert(0) += 1,
            Ok(Err(status)) => *results.entry(status).or_insert(0) += 1,
            Err(_) => *results.entry("panic").or_insert(0) += 1,
        }
    }
    
    let elapsed = start.elapsed();
    let (calls, successes) = service.stats();
    
    println!("\nCombined patterns load test:");
    println!("  Duration: {:?}", elapsed);
    println!("  Results: {:?}", results);
    println!("  Service calls: {}", calls);
    println!("  Service successes: {}", successes);
    
    let stats = mgr.stats();
    println!("\nFinal stats:");
    println!("  CB: {:?}", stats.circuit_breakers.get("backend"));
    println!("  Bulkhead: {:?}", stats.bulkheads.get("backend"));
    
    // Verify system handled load gracefully
    assert!(results.get("success").unwrap_or(&0) > &0);
    assert!(elapsed < Duration::from_secs(30), "Should complete reasonably fast");
}

use std::collections::HashMap;
