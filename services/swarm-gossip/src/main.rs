use anyhow::Result;
use tracing::{info, warn, debug};
use swarm_core::{init_tracing, start_health_server, init_metrics};
use std::{collections::{HashSet}, sync::Arc, time::{Duration, Instant}};
use parking_lot::RwLock;
use rand::{seq::IteratorRandom, thread_rng};
use serde::{Serialize, Deserialize};
use opentelemetry::global;
use sha2::{Sha256, Digest};

#[derive(Debug, Clone, Serialize, Deserialize)]
struct GossipEnvelope<T> {
    msg_id: String,
    kind: String,
    ts: u64,
    payload: T,
    hops: u8,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
struct GossipHello { node_id: String }

#[derive(Debug)]
// --- Bloom filter for duplicate suppression (aging) ---
struct BloomDupFilter {
    bits: Vec<u8>,
    mask: usize,
    last_reset: Instant,
    reset_after: Duration,
}

impl BloomDupFilter {
    fn new(size_pow2: usize, reset_after: Duration) -> Self {
        let size = size_pow2.next_power_of_two();
        Self { bits: vec![0; size/8], mask: size - 1, last_reset: Instant::now(), reset_after }
    }
    fn maybe_reset(&mut self) {
        if self.last_reset.elapsed() >= self.reset_after {
            for b in &mut self.bits { *b = 0; }
            self.last_reset = Instant::now();
        }
    }
    fn seen_or_insert(&mut self, id: &str) -> bool { // returns true if new (probabilistic)
        self.maybe_reset();
        use std::hash::{Hash, Hasher};
        let mut h1 = std::collections::hash_map::DefaultHasher::new();
        id.hash(&mut h1);
        let mut h2 = std::collections::hash_map::DefaultHasher::new();
        (0x9e3779b97f4a7c15u64).hash(&mut h2);
        id.as_bytes().iter().rev().for_each(|b| b.hash(&mut h2));
        let a = (h1.finish() as usize) & self.mask;
        let b = (h2.finish() as usize) & self.mask;
        let hit_a = self.set_bit(a);
        let hit_b = self.set_bit(b);
        !(hit_a && hit_b)
    }
    fn set_bit(&mut self, idx: usize) -> bool {
        let byte = idx >> 3; let bit = idx & 7; let m = 1u8 << bit; let prev = self.bits[byte] & m != 0; self.bits[byte] |= m; prev
    }
}

struct GossipState {
    peers: HashSet<String>,
    dup_filter: BloomDupFilter,
    node_id: String,
}

impl GossipState {
    fn new(node_id: String) -> Self { Self { peers: HashSet::new(), dup_filter: BloomDupFilter::new(1<<17, Duration::from_secs(60)), node_id } }
    fn add_peer(&mut self, p: String) { if p != self.node_id { self.peers.insert(p); } }
    fn record(&mut self, id: &str) -> bool { self.dup_filter.seen_or_insert(id) }
    fn random_fanout(&self, fanout: usize) -> Vec<String> {
        if self.peers.is_empty() { return vec![]; }
        let mut rng = thread_rng();
        self.peers.iter().cloned().choose_multiple(&mut rng, fanout)
    }
}

async fn publish_gossip<T: Serialize>(nc: &async_nats::Client, subject: &str, env: &GossipEnvelope<T>) {
    match serde_json::to_vec(env) { Ok(bytes) => { let _ = nc.publish(subject.into(), bytes.into()).await; }, Err(e) => warn!(error=?e, "serialize_error") }
}

fn make_msg_id(bytes: &[u8]) -> String {
    let mut h = Sha256::new(); h.update(bytes); format!("{:x}", h.finalize())
}

async fn run_gossip_loop(nc: async_nats::Client, state: Arc<RwLock<GossipState>>, subject_prefix: String) {
    let sub = match nc.subscribe(format!("{subject_prefix}.inbox")).await { Ok(s) => s, Err(e) => { warn!(error=?e, "subscribe_failed"); return; } };
    let meter = global::meter("swarm-gossip");
    let dup_counter = meter.u64_counter("gossip_duplicates_total").with_description("Total duplicate gossip messages seen").init();
    let fwd_counter = meter.u64_counter("gossip_forwarded_total").with_description("Total gossip messages forwarded").init();
    let recv_counter = meter.u64_counter("gossip_received_total").with_description("Total gossip messages received (unique)").init();
    let fanout_hist = meter.i64_histogram("gossip_fanout_size").with_description("Fanout size per forwarded message").init();
    let start = Instant::now();
    while let Some(msg) = sub.next().await {
        if let Ok(txt) = std::str::from_utf8(&msg.payload) {
            if let Ok(val) = serde_json::from_str::<serde_json::Value>(txt) {
                let id_opt = val.get("msg_id").and_then(|v| v.as_str());
                if let Some(id) = id_opt {
                    let mut st = state.write();
                    if !st.record(id) { dup_counter.add(1, &[]); continue; }
                    recv_counter.add(1, &[]);
                    // forward if hops < ttl
                    let hops = val.get("hops").and_then(|h| h.as_u64()).unwrap_or(0) as u8;
                    let ttl: u8 = std::env::var("GOSSIP_TTL_HOPS").ok().and_then(|v| v.parse().ok()).unwrap_or(8);
                    if hops < ttl { // forward
                        let fanout_cfg: usize = std::env::var("GOSSIP_FANOUT").ok().and_then(|v| v.parse().ok()).unwrap_or(4);
                        let targets = st.random_fanout(fanout_cfg);
                        let mut forwarded = 0;
                        for peer in targets.iter() {
                            let mut clone = val.clone();
                            if let Some(h) = clone.get_mut("hops") { *h = serde_json::Value::from((hops + 1) as u64); }
                            else { clone["hops"] = serde_json::json!((hops + 1) as u64); }
                            if let Ok(buf) = serde_json::to_vec(&clone) { let _ = nc.publish(format!("{subject_prefix}.peer.{peer}"), buf.into()).await; forwarded +=1; }
                        }
                        if forwarded>0 { fwd_counter.add(1, &[]); fanout_hist.record(forwarded, &[]); }
                    }
                }
            } else {
                debug!(payload=%txt, "non_json_ignored");
            }
        }
    }
    info!(elapsed=?start.elapsed(), "gossip_loop_ended");
}

async fn run_peer_listener(nc: async_nats::Client, state: Arc<RwLock<GossipState>>, subject_prefix: String) {
    // listen on peer direct subjects
    let pattern = format!("{subject_prefix}.peer.");
    let sub = match nc.subscribe(format!("{subject_prefix}.>")).await { Ok(s) => s, Err(e) => { warn!(error=?e, "subscribe_failed"); return; } };
    while let Some(msg) = sub.next().await {
        if let Some(last) = msg.subject.split('.').last() {
            if last == "inbox" { continue; }
            if last != "peer" { // treat last as peer id or message variant
                // quick heuristic: if payload is hello, add peer
                if let Ok(txt) = std::str::from_utf8(&msg.payload) {
                    if let Ok(env) = serde_json::from_str::<serde_json::Value>(txt) { if env.get("kind").and_then(|v| v.as_str()) == Some("hello") {
                        let node_id = env.get("payload").and_then(|p| p.get("node_id")).and_then(|v| v.as_str()).unwrap_or("");
                        if !node_id.is_empty() { state.write().add_peer(node_id.to_string()); }
                    }}
                }
            }
        }
    }
}

async fn send_hello(nc: &async_nats::Client, state: &Arc<RwLock<GossipState>>, subject_prefix: &str) {
    let st = state.read();
    let env = GossipEnvelope { msg_id: make_msg_id(st.node_id.as_bytes()), kind: "hello".into(), ts: chrono::Utc::now().timestamp_millis() as u64, payload: GossipHello { node_id: st.node_id.clone() }, hops: 0 };
    publish_gossip(nc, &format!("{subject_prefix}.inbox"), &env).await;
}

#[tokio::main]
async fn main() -> Result<()> {
    init_tracing("swarm-gossip")?;
    init_metrics()?;
    start_health_server(8081).await?;
    let nats_url = std::env::var("NATS_URL").unwrap_or_else(|_| "127.0.0.1:4222".into());
    let node_id = std::env::var("NODE_ID").unwrap_or_else(|_| format!("node-{}", uuid::Uuid::new_v4().simple()));
    let subject_prefix = std::env::var("GOSSIP_SUBJECT_PREFIX").unwrap_or_else(|_| "swarm.gossip".into());
    info!(target: "swarm-gossip", %nats_url, %node_id, %subject_prefix, "Starting swarm-gossip service");
    let nc = async_nats::connect(nats_url).await?;
    let state = Arc::new(RwLock::new(GossipState::new(node_id.clone())));
    send_hello(&nc, &state, &subject_prefix).await;
    // spawn loops
    tokio::spawn(run_gossip_loop(nc.clone(), state.clone(), subject_prefix.clone()));
    tokio::spawn(run_peer_listener(nc.clone(), state.clone(), subject_prefix.clone()));
    // periodic hello (keep alive + membership)
    let hello_nc = nc.clone();
    let hello_state = state.clone();
    let hp = subject_prefix.clone();
    tokio::spawn(async move {
        let mut interval = tokio::time::interval(Duration::from_secs(15));
        loop { interval.tick().await; send_hello(&hello_nc, &hello_state, &hp).await; }
    });
    // idle loop until ctrl-c
    tokio::signal::ctrl_c().await?;
    info!("shutdown_signal_received");
    Ok(())
}

// Tracing & metrics handled by swarm-core
