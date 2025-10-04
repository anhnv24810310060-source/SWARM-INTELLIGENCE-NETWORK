/// Resilience Library Demo - Production Usage Examples
/// 
/// Run with: cargo run --example resilience_demo

use swarm_core::resilience_advanced::*;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::time::sleep;

#[tokio::main]
async fn main() {
    println!("=== SwarmGuard Resilience Library Demo ===\n");
    
    // Demo 1: Circuit Breaker
    demo_circuit_breaker().await;
    
    // Demo 2: Rate Limiter
    demo_rate_limiter().await;
    
    // Demo 3: Bulkhead
    demo_bulkhead().await;
    
    // Demo 4: Retry Executor
    demo_retry().await;
    
    // Demo 5: Full Stack
    demo_full_stack().await;
    
    println!("\n=== All demos completed successfully ===");
}

async fn demo_circuit_breaker() {
    println!("--- Circuit Breaker Demo ---");
    
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
    
    // Simulate failures
    println!("Simulating service failures...");
    for i in 0..10 {
        if cb.allow() {
            // Simulate 70% failure rate
            let success = i % 10 < 3;
            cb.record_result(success);
            
            if success {
                println!("  ✓ Request {} succeeded", i);
            } else {
                println!("  ✗ Request {} failed", i);
            }
            
            sleep(Duration::from_millis(50)).await;
        } else {
            println!("  ⊗ Request {} blocked by circuit breaker", i);
        }
    }
    
    let stats = cb.stats();
    println!("\nCircuit Breaker Stats:");
    println!("  State: {:?}", stats.state);
    println!("  Successes: {}", stats.successes);
    println!("  Failures: {}", stats.failures);
    println!("  Failure Rate: {:.1}%\n", stats.failure_rate * 100.0);
}

async fn demo_rate_limiter() {
    println!("--- Rate Limiter Demo ---");
    
    let rl = Arc::new(RateLimiter::new(10, 5.0)); // 10 tokens, refill 5/sec
    
    println!("Sending burst of 20 requests...");
    let mut allowed = 0;
    let mut rejected = 0;
    
    for i in 0..20 {
        if rl.acquire(1) {
            allowed += 1;
            println!("  ✓ Request {} allowed", i);
        } else {
            rejected += 1;
            println!("  ✗ Request {} rate limited", i);
        }
        
        sleep(Duration::from_millis(50)).await;
    }
    
    println!("\nRate Limiter Stats:");
    println!("  Allowed: {}", allowed);
    println!("  Rejected: {}", rejected);
    println!("  Available tokens: {}\n", rl.available());
}

async fn demo_bulkhead() {
    println!("--- Bulkhead Demo ---");
    
    let bh = Arc::new(Bulkhead::new(
        "worker_pool".to_string(),
        3,  // max 3 concurrent
        2,  // queue size 2
    ));
    
    println!("Spawning 8 concurrent tasks (max 3, queue 2)...");
    
    let mut handles = vec![];
    
    for i in 0..8 {
        let bh_clone = bh.clone();
        
        let handle = tokio::spawn(async move {
            match bh_clone.try_acquire() {
                Some(_permit) => {
                    println!("  ✓ Task {} acquired permit, working...", i);
                    sleep(Duration::from_millis(200)).await;
                    println!("  ✓ Task {} completed", i);
                    true
                }
                None => {
                    println!("  ✗ Task {} rejected (bulkhead full)", i);
                    false
                }
            }
        });
        
        handles.push(handle);
        sleep(Duration::from_millis(50)).await;
    }
    
    let mut completed = 0;
    let mut rejected = 0;
    
    for handle in handles {
        if handle.await.unwrap() {
            completed += 1;
        } else {
            rejected += 1;
        }
    }
    
    println!("\nBulkhead Stats:");
    println!("  Completed: {}", completed);
    println!("  Rejected: {}", rejected);
    println!("  {:?}\n", bh.stats());
}

async fn demo_retry() {
    println!("--- Retry Executor Demo ---");
    
    let retry = RetryExecutor::new(RetryConfig {
        max_attempts: 5,
        base_delay: Duration::from_millis(100),
        max_delay: Duration::from_secs(2),
        multiplier: 2.0,
        jitter: true,
    });
    
    let mut attempt_count = 0;
    
    println!("Attempting flaky operation (succeeds on 3rd try)...");
    let start = Instant::now();
    
    let result = retry
        .execute(|| {
            attempt_count += 1;
            let current_attempt = attempt_count;
            
            async move {
                println!("  → Attempt {}", current_attempt);
                
                if current_attempt < 3 {
                    Err(format!("Attempt {} failed", current_attempt))
                } else {
                    Ok("Success!")
                }
            }
        })
        .await;
    
    let elapsed = start.elapsed();
    
    println!("\nRetry Executor Stats:");
    println!("  Result: {:?}", result);
    println!("  Total attempts: {}", attempt_count);
    println!("  Total time: {:?}\n", elapsed);
}

async fn demo_full_stack() {
    println!("--- Full Protection Stack Demo ---");
    
    let mgr = Arc::new(ResilienceManager::new());
    
    let cb = mgr.register_circuit_breaker(
        "payment_api".to_string(),
        CircuitBreakerConfig {
            failure_threshold: 0.3,
            min_requests: 5,
            success_threshold: 3,
            timeout: Duration::from_millis(500),
            window_size: Duration::from_secs(10),
            buckets: 10,
        },
    );
    
    let rl = mgr.register_rate_limiter("payment_api".to_string(), 20, 10.0);
    let bh = mgr.register_bulkhead("payment_api".to_string(), 5, 2);
    
    println!("Sending 30 requests through full protection stack...");
    
    let mut stats = ProtectionStats::default();
    
    for i in 0..30 {
        // Circuit breaker check
        if !cb.allow() {
            stats.circuit_blocked += 1;
            println!("  ⊗ Request {} blocked by circuit breaker", i);
            continue;
        }
        
        // Rate limiter check
        if !rl.acquire(1) {
            stats.rate_limited += 1;
            println!("  ⊗ Request {} rate limited", i);
            continue;
        }
        
        // Bulkhead check
        let permit = match bh.try_acquire() {
            Some(p) => p,
            None => {
                stats.bulkhead_full += 1;
                println!("  ⊗ Request {} rejected (bulkhead full)", i);
                continue;
            }
        };
        
        // Simulate request
        let success = i % 5 != 0; // 80% success rate
        cb.record_result(success);
        
        if success {
            stats.succeeded += 1;
            println!("  ✓ Request {} succeeded", i);
        } else {
            stats.failed += 1;
            println!("  ✗ Request {} failed", i);
        }
        
        drop(permit);
        sleep(Duration::from_millis(50)).await;
    }
    
    println!("\n=== Protection Stack Stats ===");
    println!("Total requests: 30");
    println!("  ✓ Succeeded: {}", stats.succeeded);
    println!("  ✗ Failed: {}", stats.failed);
    println!("  ⊗ Circuit blocked: {}", stats.circuit_blocked);
    println!("  ⊗ Rate limited: {}", stats.rate_limited);
    println!("  ⊗ Bulkhead full: {}", stats.bulkhead_full);
    
    let resilience_stats = mgr.stats();
    println!("\n=== Component Stats ===");
    if let Some(cb_stats) = resilience_stats.circuit_breakers.get("payment_api") {
        println!("Circuit Breaker:");
        println!("  State: {:?}", cb_stats.state);
        println!("  Failure rate: {:.1}%", cb_stats.failure_rate * 100.0);
    }
    
    if let Some(bh_stats) = resilience_stats.bulkheads.get("payment_api") {
        println!("Bulkhead:");
        println!("  Current: {}/{}", bh_stats.current, bh_stats.max_concurrent);
        println!("  Waiting: {}/{}", bh_stats.waiting, bh_stats.queue_size);
    }
}

#[derive(Default)]
struct ProtectionStats {
    succeeded: usize,
    failed: usize,
    circuit_blocked: usize,
    rate_limited: usize,
    bulkhead_full: usize,
}
