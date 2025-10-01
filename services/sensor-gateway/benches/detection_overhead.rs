use criterion::{criterion_group, criterion_main, Criterion, BenchmarkId, black_box};
use sensor_gateway::{run}; // if not public API, we create a lightweight internal encode function below.
use std::time::Duration;

// Fallback simple encode path replicating process_line logic subset if needed.
use swarm_proto::ingestion::RawEvent;
use prost::Message;

fn encode_event(i: u64, payload: &str) -> usize {
    let evt = RawEvent {
        id: format!("bench-{i}"),
        observed_ts: i,
        source_type: "bench".into(),
        origin: "bench-host".into(),
        payload: payload.as_bytes().to_vec(),
        content_type: "text/plain".into(),
    };
    let mut buf = Vec::with_capacity(evt.encoded_len());
    evt.encode(&mut buf).unwrap();
    buf.len()
}

fn detection_overhead(c: &mut Criterion) {
    let mut group = c.benchmark_group("detection_overhead");
    group.measurement_time(Duration::from_secs(5));
    group.sample_size(40);
    let base_payload = "synthetic-event-123 benign baseline";
    // Baseline encode only
    group.bench_with_input(BenchmarkId::new("encode_only", 0), &0, |b, _| {
        b.iter(|| {
            let len = encode_event(42, base_payload);
            black_box(len);
        })
    });
    // Simulate detection scan cost by running regex over payload multiple times (approximation)
    let detector_regex = regex::Regex::new("synthetic-event-123").unwrap();
    group.bench_with_input(BenchmarkId::new("encode_plus_detection", 0), &0, |b, _| {
        b.iter(|| {
            let len = encode_event(42, base_payload);
            let m = detector_regex.is_match(base_payload);
            black_box((len, m));
        })
    });
    group.finish();
}

criterion_group!(benches, detection_overhead);
criterion_main!(benches);
