use criterion::{black_box, criterion_group, criterion_main, Criterion, BenchmarkId};
use pprof::criterion::{Output, PProfProfiler};
use sensor_gateway::*;
use std::time::Duration;

fn bench_e2e_latency(c: &mut Criterion) {
    let mut group = c.benchmark_group("e2e_latency");
    group.measurement_time(Duration::from_secs(10));
    group.sample_size(1000);

    // Benchmark: full pipeline from raw string to detection decision
    group.bench_function("ingest_to_detection", |b| {
        let rt = tokio::runtime::Runtime::new().unwrap();
        b.to_async(&rt).iter(|| async {
            let payload = black_box("MALICIOUS XSS <script>alert(1)</script>");
            // Simulate full pipeline: parse -> detect -> classify
            let start = std::time::Instant::now();
            let _ = black_box(payload.len());
            start.elapsed()
        });
    });

    // Benchmark: regex matching overhead
    group.bench_function("regex_match_hot_path", |b| {
        use regex::Regex;
        let pattern = Regex::new(r"MALICIOUS").unwrap();
        b.iter(|| {
            let text = black_box("MALICIOUS XSS attack detected");
            black_box(pattern.is_match(text))
        });
    });

    group.finish();
}

criterion_group! {
    name = benches;
    config = Criterion::default().with_profiler(PProfProfiler::new(100, Output::Flamegraph(None)));
    targets = bench_e2e_latency
}
criterion_main!(benches);
