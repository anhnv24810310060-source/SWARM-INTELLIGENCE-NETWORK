use serde::{Serialize, Deserialize};
use std::collections::{HashMap, HashSet};
use std::time::{Instant, Duration};
use parking_lot::RwLock;
use opentelemetry::metrics::{Counter, Meter};
use once_cell::sync::Lazy;
use tracing::{debug, info, instrument};
use super::NodeId;

pub type ViewNumber = u64;
pub type SequenceNumber = u64;
pub type Digest = [u8; 32];

#[derive(Clone, Debug, Serialize, Deserialize)]
pub enum PBFTPhase { PrePrepare, Prepare, Commit }

#[derive(Clone, Debug, Serialize, Deserialize)]
pub enum PBFTMessage {
    PrePrepare { view: ViewNumber, seq: SequenceNumber, digest: Digest, proposer: NodeId },
    Prepare { view: ViewNumber, seq: SequenceNumber, digest: Digest, node: NodeId },
    Commit { view: ViewNumber, seq: SequenceNumber, digest: Digest, node: NodeId },
    ViewChange { new_view: ViewNumber, node: NodeId },
}

#[derive(Clone, Debug)]
pub struct PBFTConfig {
    pub node_id: NodeId,
    pub peers: Vec<NodeId>,
    pub view: ViewNumber,
    pub f: usize,
    pub checkpoint_interval: u64,
    pub msg_timeout: Duration,
}

impl PBFTConfig {
    pub fn quorum(&self) -> usize { (2 * self.f) + 1 }
    pub fn total_nodes(&self) -> usize { self.peers.len() + 1 }
    pub fn is_leader(&self) -> bool {
        let idx = (self.view % self.total_nodes() as u64) as usize;
        // leader index 0 => self.node_id if not in peers
        if idx == 0 { return !self.peers.contains(&self.node_id); }
        self.peers.get(idx - 1).map(|p| *p == self.node_id).unwrap_or(false)
    }
}

static CONSENSUS_METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("swarm_consensus"));

#[derive(Clone)]
struct PBFTMetrics {
    messages_total: Counter<u64>,
    view_changes_total: Counter<u64>,
    decisions_total: Counter<u64>,
    rejected_total: Counter<u64>,
}

impl PBFTMetrics {
    fn new() -> Self {
        Self {
            messages_total: CONSENSUS_METER.u64_counter("pbft_messages_total").with_description("Total PBFT messages processed").init(),
            view_changes_total: CONSENSUS_METER.u64_counter("pbft_view_change_total").with_description("Total view changes").init(),
            decisions_total: CONSENSUS_METER.u64_counter("pbft_decisions_total").with_description("Total committed decisions").init(),
            rejected_total: CONSENSUS_METER.u64_counter("pbft_rejected_total").with_description("Messages rejected (invalid / stale)").init(),
        }
    }
}

pub struct PBFTConsensus {
    cfg: PBFTConfig,
    state: RwLock<State>,
    metrics: PBFTMetrics,
}

#[derive(Debug)]
struct InFlight {
    view: ViewNumber,
    seq: SequenceNumber,
    digest: Digest,
    preprepare_time: Instant,
    prepares: HashSet<NodeId>,
    commits: HashSet<NodeId>,
}

#[derive(Debug)]
struct State {
    current_view: ViewNumber,
    next_seq: SequenceNumber,
    inflight: HashMap<SequenceNumber, InFlight>,
    committed: HashMap<SequenceNumber, Digest>,
}

impl State { fn new(view: ViewNumber) -> Self { Self { current_view: view, next_seq: 1, inflight: HashMap::new(), committed: HashMap::new() } } }

impl PBFTConsensus {
    pub fn new(node_id: NodeId, max_nodes: usize) -> Self {
        let peers = Vec::new();
        let f = ((max_nodes as f32 - 1.0) / 3.0).floor() as usize;
        let cfg = PBFTConfig { node_id, peers, view: 0, f, checkpoint_interval: 50, msg_timeout: Duration::from_secs(5) };
        Self { cfg, state: RwLock::new(State::new(0)), metrics: PBFTMetrics::new() }
    }

    pub fn with_config(cfg: PBFTConfig) -> Self { Self { state: RwLock::new(State::new(cfg.view)), cfg, metrics: PBFTMetrics::new() } }

    pub fn propose(&self, digest: Digest) -> Option<PBFTMessage> {
        if !self.cfg.is_leader() { return None; }
        let mut st = self.state.write();
        let seq = st.next_seq; st.next_seq += 1;
        let inflight = InFlight { view: st.current_view, seq, digest, preprepare_time: Instant::now(), prepares: HashSet::new(), commits: HashSet::new() };
        st.inflight.insert(seq, inflight);
        Some(PBFTMessage::PrePrepare { view: st.current_view, seq, digest, proposer: self.cfg.node_id })
    }

    #[instrument(skip(self, msg), level = "debug")]
    pub fn handle_message(&self, msg: PBFTMessage) -> Vec<PBFTMessage> {
        self.metrics.messages_total.add(1, &[]);
        let mut out = Vec::new();
        match msg {
            PBFTMessage::PrePrepare { view, seq, digest, proposer: _ } => {
                let mut st = self.state.write();
                if view != st.current_view { self.metrics.rejected_total.add(1, &[]); return out; }
                st.inflight.entry(seq).or_insert(InFlight { view, seq, digest, preprepare_time: Instant::now(), prepares: HashSet::new(), commits: HashSet::new() });
                out.push(PBFTMessage::Prepare { view, seq, digest, node: self.cfg.node_id });
            }
            PBFTMessage::Prepare { view, seq, digest, node } => {
                let mut st = self.state.write();
                if let Some(inf) = st.inflight.get_mut(&seq) { if inf.view == view && inf.digest == digest { inf.prepares.insert(node); if inf.prepares.len() + 1 >= self.cfg.quorum() { out.push(PBFTMessage::Commit { view, seq, digest, node: self.cfg.node_id }); } } }
            }
            PBFTMessage::Commit { view, seq, digest, node } => {
                let mut st = self.state.write();
                if let Some(inf) = st.inflight.get_mut(&seq) { if inf.view == view && inf.digest == digest { inf.commits.insert(node); if inf.commits.len() + 1 >= self.cfg.quorum() { st.committed.insert(seq, digest); self.metrics.decisions_total.add(1, &[]); debug!(seq, "Commit finalized"); } } }
            }
            PBFTMessage::ViewChange { new_view, node: _ } => {
                let mut st = self.state.write();
                if new_view > st.current_view { st.current_view = new_view; self.metrics.view_changes_total.add(1, &[]); info!(view = new_view, "View change accepted"); }
            }
        }
        out
    }

    pub fn committed(&self, seq: SequenceNumber) -> Option<Digest> { self.state.read().committed.get(&seq).copied() }
    pub fn current_view(&self) -> ViewNumber { self.state.read().current_view }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn propose_maybe() {
        let node = PBFTConsensus::new(NodeId::generate(), 4);
        let _ = node.propose([1u8;32]);
    }
}
