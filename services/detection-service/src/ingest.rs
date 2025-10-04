use anyhow::Result;
use tokio::sync::mpsc;
use std::time::{Instant, Duration};
use once_cell::sync::Lazy;
use opentelemetry::metrics::{Meter, Counter, ObservableGauge};
use opentelemetry::KeyValue;
use std::sync::{Arc, atomic::{AtomicU64, Ordering}};

static INGEST_METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("swarm_ingest"));
static EVENTS_ACCEPT_TOTAL: Lazy<Counter<u64>> = Lazy::new(|| INGEST_METER.u64_counter("swarm_events_accept_total").with_description("Accepted events into pipeline").init());
static EVENTS_DROPPED_TOTAL: Lazy<Counter<u64>> = Lazy::new(|| INGEST_METER.u64_counter("swarm_events_dropped_total").with_description("Dropped events").init());
static QUEUE_DEPTH_ATOMIC: Lazy<AtomicU64> = Lazy::new(|| AtomicU64::new(0));
static QUEUE_DEPTH_GAUGE: Lazy<ObservableGauge<u64>> = Lazy::new(|| INGEST_METER.u64_observable_gauge("swarm_ingest_queue_depth").with_description("Current ingest queue depth").init());
static QUEUE_CB_INIT: Lazy<()> = Lazy::new(|| {
    INGEST_METER.register_callback(&[QUEUE_DEPTH_GAUGE.as_any()], |cx| {
        let v = QUEUE_DEPTH_ATOMIC.load(Ordering::Relaxed);
        cx.observe_u64(&*QUEUE_DEPTH_GAUGE, v, &[]);
    }).ok();
});

#[derive(Clone)]
pub struct IngestHandle {
    tx: mpsc::Sender<crate::pipeline::RawEvent>,
    rate: Arc<RateLimiter>,
    max_event: usize,
}

impl IngestHandle {
    pub fn try_push(&self, ev: crate::pipeline::RawEvent) -> Result<()> {
        // size check
        if ev.bytes.len() > self.max_event { EVENTS_DROPPED_TOTAL.add(1, &[opentelemetry::KeyValue::new("reason","oversize")]); return Ok(()); }
        if !self.rate.acquire() { EVENTS_DROPPED_TOTAL.add(1, &[opentelemetry::KeyValue::new("reason","rate_limit")]); return Ok(()); }
        match self.tx.try_send(ev) {
            Ok(_) => { EVENTS_ACCEPT_TOTAL.add(1,&[]); QUEUE_DEPTH_ATOMIC.fetch_add(1, Ordering::Relaxed); Ok(()) }
            Err(mpsc::error::TrySendError::Full(_)) => { EVENTS_DROPPED_TOTAL.add(1, &[opentelemetry::KeyValue::new("reason","queue_full")]); Ok(()) }
            Err(e) => Err(anyhow::anyhow!("ingest send error: {e}"))
        }
    }
}

pub fn start_ingest(cap: usize, rps_limit: u64, max_event: usize) -> (IngestHandle, mpsc::Receiver<crate::pipeline::RawEvent>) {
    Lazy::force(&QUEUE_CB_INIT); // ensure gauge callback registered
    let (tx, rx) = mpsc::channel(cap);
    let rate = Arc::new(RateLimiter::new(rps_limit));
    (IngestHandle { tx, rate, max_event }, rx)
}

struct RateLimiter { limit: u64, tokens: parking_lot::Mutex<Tokens> }
struct Tokens { available: u64, last_refill: Instant }
impl RateLimiter { fn new(limit: u64) -> Self { Self { limit, tokens: parking_lot::Mutex::new(Tokens { available: limit, last_refill: Instant::now() }) } }
    fn acquire(&self) -> bool {
        if self.limit == 0 { return true; }
        let mut t = self.tokens.lock();
        let now = Instant::now();
        let elapsed = now.duration_since(t.last_refill);
        if elapsed >= Duration::from_millis(200) { // refill slice
            let slices = (elapsed.as_millis()/200) as u64;
            if slices > 0 { let add = slices * (self.limit/5).max(1); t.available = (t.available + add).min(self.limit); t.last_refill = now; }
        }
        if t.available == 0 { return false; }
        t.available -=1; true
    }
}

pub fn on_dequeue() { QUEUE_DEPTH_ATOMIC.fetch_sub(1, Ordering::Relaxed); }