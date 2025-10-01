use criterion::{criterion_group, criterion_main, Criterion, BatchSize};
use std::time::SystemTime;
use prost::Message;
use swarm_proto::ingestion::RawEvent;

fn encode_event(payload: &str) -> Vec<u8> {
    let evt = RawEvent {
        id: format!("{}", SystemTime::now().elapsed().unwrap().as_nanos()),
        observed_ts: SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_millis() as u64,
        source_type: "bench".into(),
        origin: "local".into(),
        payload: payload.as_bytes().to_vec(),
        content_type: "text/plain".into(),
    };
    let mut buf = Vec::with_capacity(evt.encoded_len());
    evt.encode(&mut buf).unwrap();
    buf
}

fn bench_encode(c: &mut Criterion) {
    let payload = "X".repeat(256);
    c.bench_function("raw_event_encode_256B", |b| {
        b.iter(|| {
            let _ = encode_event(&payload);
        })
    });

    c.bench_function("raw_event_encode_1KB", |b| {
        let payload = "X".repeat(1024);
        b.iter(|| { let _ = encode_event(&payload); })
    });

    c.bench_function("raw_event_encode_batch_100", |b| {
        let payload = "X".repeat(256);
        b.iter_batched(
            || payload.clone(),
            |p| { for _ in 0..100 { let _ = encode_event(&p); } },
            BatchSize::SmallInput
        )
    });
}

criterion_group!(name=ingest; config=Criterion::default(); targets=bench_encode);
criterion_main!(ingest);
