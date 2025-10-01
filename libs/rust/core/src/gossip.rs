//! Gossip protocol skeleton (epidemic dissemination) per design Section 2.3.1.
//!
//! Features (implemented / planned):
//! - [x] Message envelope struct with id, hops, ttl
//! - [x] Fanout config (ENV: GOSSIP_FANOUT, default 4)
//! - [x] TTL hops enforcement (ENV: GOSSIP_TTL_HOPS, default 8)
//! - [ ] Duplicate suppression bloom filter (placeholder HashSet impl)
//! - [ ] Adaptive fanout based on duplicate ratio
//! - [ ] QUIC transport integration
//! - [ ] Trace context propagation
//!
//! This is a lightweight in-memory skeleton; networking & persistence are out of scope
//! for this initial scaffold.

use std::{collections::{HashSet, HashMap}, time::{Instant, Duration}, sync::Arc};
use parking_lot::RwLock;
use rand::{seq::IteratorRandom, thread_rng};
use uuid::Uuid;

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct GossipId(pub String);

#[derive(Debug, Clone)]
pub enum GossipKind { Alert, Intelligence, ModelUpdate, Membership, Custom(String) }

#[derive(Debug, Clone)]
pub struct GossipMessage {
    pub id: GossipId,
    pub kind: GossipKind,
    pub ts: Instant,
    pub hops: u8,
    pub ttl: u8,
    pub payload: Vec<u8>,
}

impl GossipMessage {
    pub fn new(kind: GossipKind, ttl: u8, payload: Vec<u8>) -> Self {
        Self { id: GossipId(Uuid::new_v4().to_string()), kind, ts: Instant::now(), hops: 0, ttl, payload }
    }
}

#[derive(Default)]
pub struct GossipStats {
    pub received: u64,
    pub forwarded: u64,
    pub duplicates: u64,
}

pub struct GossipEngine {
    peers: Arc<RwLock<HashSet<String>>>,
    seen: Arc<RwLock<HashSet<GossipId>>>,
    stats: Arc<RwLock<GossipStats>>,
    fanout: usize,
    ttl: u8,
}

impl GossipEngine {
    pub fn new() -> Self {
        let fanout = std::env::var("GOSSIP_FANOUT").ok().and_then(|v| v.parse().ok()).unwrap_or(4);
        let ttl = std::env::var("GOSSIP_TTL_HOPS").ok().and_then(|v| v.parse().ok()).unwrap_or(8);
        Self { peers: Arc::new(RwLock::new(HashSet::new())), seen: Arc::new(RwLock::new(HashSet::new())), stats: Arc::new(RwLock::new(GossipStats::default())), fanout, ttl }
    }

    pub fn add_peer(&self, id: String) { self.peers.write().insert(id); }

    pub fn ingest(&self, mut msg: GossipMessage) -> Vec<(String, GossipMessage)> {
        let mut stats = self.stats.write();
        stats.received += 1;
        // duplicate suppression
        let mut seen = self.seen.write();
        if !seen.insert(msg.id.clone()) {
            stats.duplicates += 1;
            return Vec::new();
        }
        drop(seen);
        if msg.hops >= msg.ttl { return Vec::new(); }
        msg.hops += 1;
        let peers = self.peers.read();
        let mut rng = thread_rng();
        let selected: Vec<String> = peers.iter().cloned().choose_multiple(&mut rng, self.fanout.min(peers.len()));
        let mut out = Vec::with_capacity(selected.len());
        for p in selected { out.push((p, msg.clone())); }
        stats.forwarded += out.len() as u64;
        out
    }

    pub fn publish(&self, kind: GossipKind, payload: Vec<u8>) -> Vec<(String, GossipMessage)> {
        let msg = GossipMessage::new(kind, self.ttl, payload);
        self.ingest(msg)
    }

    pub fn stats(&self) -> GossipStats { self.stats.read().clone() }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn basic_fanout() {
        let engine = GossipEngine::new();
        for i in 0..10 { engine.add_peer(format!("peer-{}", i)); }
        let forwards = engine.publish(GossipKind::Alert, b"hello".to_vec());
        assert!(!forwards.is_empty());
        let st = engine.stats();
        assert_eq!(st.received, 1);
        assert!(st.forwarded as usize <= 4); // default fanout
    }
}
