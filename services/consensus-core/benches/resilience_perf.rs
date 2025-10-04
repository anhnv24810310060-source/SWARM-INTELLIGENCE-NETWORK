/// Performance benchmarks for resilience patterns
/// 
/// Benchmarks:
/// 1. Circuit breaker decision overhead
/// 2. Rate limiter throughput
/// 3. Bulkhead acquire/release latency
/// 4. Combined pattern overhead
/// 
/// Run with: cargo bench --bench resilience_perf

use criterion::{black_box, criterion_group, criterion_main, Criterion, BenchmarkId, Throughput};
use swarm_core::resilience_advanced::*;
use std::sync::Arc;
use std::time::Duration;

fn bench_circuit_breaker_allow(c: &mut Criterion) {
    let cb = Arc::new(CircuitBreaker::new(
        "bench".to_string(),
        CircuitBreakerConfig::default(),
    ));
    
    c.bench_function("circuit_breaker_allow_closed", |b| {
        b.iter(|| {
            black_box(cb.allow());
        });
    });
    
    // Open the circuit
    for _ in 0..20 {
        cb.record_result(false);
    }
    
    c.bench_function("circuit_breaker_allow_open", |b| {
        b.iter(|| {
            black_box(cb.allow());
        });
    });
}

fn bench_circuit_breaker_record(c: &mut Criterion) {
    let cb = Arc::new(CircuitBreaker::new(
        "bench".to_string(),
        CircuitBreakerConfig::default(),
    ));
    
    c.bench_function("circuit_breaker_record_result", |b| {
        b.iter(|| {
            cb.record_result(black_box(true));
        });
    });
}

fn bench_rate_limiter(c: &mut Criterion) {
    let mut group = c.benchmark_group("rate_limiter");
    
    for capacity in [100, 1000, 10000].iter() {
        let rl = Arc::new(RateLimiter::new(*capacity, *capacity as f64));
        
        group.throughput(Throughput::Elements(1));
        group.bench_with_input(
            BenchmarkId::from_parameter(capacity),
            capacity,
            |b, _| {
                b.iter(|| {
                    black_box(rl.acquire(1));
                });
            },
        );
    }
    
    group.finish();
}

fn bench_bulkhead(c: &mut Criterion) {
    let bh = Arc::new(Bulkhead::new(
        "bench".to_string(),
        100,
        10,
    ));
    
    c.bench_function("bulkhead_try_acquire", |b| {
        b.iter(|| {
            if let Some(permit) = bh.try_acquire() {
                drop(black_box(permit));
            }
        });
    });
}

fn bench_combined_overhead(c: &mut Criterion) {
    let mgr = Arc::new(ResilienceManager::new());
    
    let cb = mgr.register_circuit_breaker(
        "api".to_string(),
        CircuitBreakerConfig::default(),
    );
    
    let rl = mgr.register_rate_limiter("api".to_string(), 10000, 10000.0);
    let bh = mgr.register_bulkhead("api".to_string(), 100, 10);
    
    c.bench_function("combined_resilience_overhead", |b| {
        b.iter(|| {
            // Simulate full resilience check
            if !cb.allow() {
                return;
            }
            
            if !rl.acquire(1) {
                return;
            }
            
            if let Some(_permit) = bh.try_acquire() {
                // Simulate work
                black_box(42 + 42);
            }
        });
    });
}

fn bench_retry_no_failure(c: &mut Criterion) {
    let runtime = tokio::runtime::Runtime::new().unwrap();
    
    let retry = RetryExecutor::new(RetryConfig {
        max_attempts: 3,
        base_delay: Duration::from_millis(1),
        max_delay: Duration::from_millis(10),
        multiplier: 2.0,
        jitter: false,
    });
    
    c.bench_function("retry_executor_immediate_success", |b| {
        b.to_async(&runtime).iter(|| async {
            retry
                .execute(|| async { Ok::<_, String>("success") })
                .await
                .unwrap();
        });
    });
}

fn bench_circuit_stats(c: &mut Criterion) {
    let cb = Arc::new(CircuitBreaker::new(
        "bench".to_string(),
        CircuitBreakerConfig::default(),
    ));
    
    // Record some results
    for i in 0..100 {
        cb.record_result(i % 3 != 0);
    }
    
    c.bench_function("circuit_breaker_stats", |b| {
        b.iter(|| {
            black_box(cb.stats());
        });
    });
}

fn bench_resilience_manager_stats(c: &mut Criterion) {
    let mgr = Arc::new(ResilienceManager::new());
    
    for i in 0..10 {
        mgr.register_circuit_breaker(
            format!("api{}", i),
            CircuitBreakerConfig::default(),
        );
        mgr.register_bulkhead(format!("api{}", i), 100, 10);
    }
    
    c.bench_function("resilience_manager_stats_10_components", |b| {
        b.iter(|| {
            black_box(mgr.stats());
        });
    });
}

criterion_group!(
    benches,
    bench_circuit_breaker_allow,
    bench_circuit_breaker_record,
    bench_rate_limiter,
    bench_bulkhead,
    bench_combined_overhead,
    bench_retry_no_failure,
    bench_circuit_stats,
    bench_resilience_manager_stats
);

criterion_main!(benches);
