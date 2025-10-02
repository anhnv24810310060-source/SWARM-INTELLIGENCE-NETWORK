use std::collections::{HashMap, HashSet};
use std::sync::{Arc, RwLock};
use std::time::Instant;
use once_cell::sync::Lazy;
use sha2::{Digest, Sha256};
static DB: Lazy<Option<sled::Db>> = Lazy::new(|| {
    let path = std::env::var("CONSENSUS_DB_PATH").unwrap_or_else(|_| "./data/consensus".into());
    match sled::open(path) { Ok(db) => Some(db), Err(e) => { tracing::warn!(error=?e, "sled open failed - running ephemeral"); None } }
});
use async_trait::async_trait;
use tonic::{Request, Response, Status};
use swarm_proto::consensus::{pbft_server::Pbft, Proposal, Vote, Ack, ConsensusStateQuery, ConsensusState};
use tracing::instrument;
mod view_change;
use opentelemetry::metrics::Meter;
use once_cell::sync::Lazy;

static CONSENSUS_METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("consensus-core"));
static HEIGHT_GAUGE: once_cell::sync::Lazy<opentelemetry::metrics::ObservableGauge<u64>> = once_cell::sync::Lazy::new(|| {
    CONSENSUS_METER.u64_observable_gauge("swarm_blockchain_height")
        .with_description("Current committed blockchain height as observed by this node")
        .init()
});
static PREPARE_TOTAL: once_cell::sync::Lazy<opentelemetry::metrics::Counter<u64>> = once_cell::sync::Lazy::new(|| {
    CONSENSUS_METER.u64_counter("swarm_consensus_prepare_total").with_description("Total PREPARE votes observed").init()
});
static COMMIT_TOTAL: once_cell::sync::Lazy<opentelemetry::metrics::Counter<u64>> = once_cell::sync::Lazy::new(|| {
    CONSENSUS_METER.u64_counter("swarm_consensus_commit_total").with_description("Total COMMIT votes observed").init()
});
static ROUND_HIST_MS: once_cell::sync::Lazy<opentelemetry::metrics::Histogram<f64>> = once_cell::sync::Lazy::new(|| {
    CONSENSUS_METER.f64_histogram("consensus_round_progress_ms").with_description("Time from PRE-PREPARE to quorum COMMIT in ms").init()
});
static ROUND_HIST_S: once_cell::sync::Lazy<opentelemetry::metrics::Histogram<f64>> = once_cell::sync::Lazy::new(|| {
    CONSENSUS_METER.f64_histogram("swarm_consensus_round_duration_seconds").with_description("Consensus round duration seconds").init()
});

#[derive(Debug, Default, Clone)]
struct PhaseVotes {
    prepares: HashSet<String>,
    commits: HashSet<String>,
    pre_prepare_seen: bool,
    start: Option<Instant>,
}

#[derive(Debug, Default)]
pub struct PbftState {
    pub height: u64,
    pub round: u64,
    pub leader: String,
    pub validators: Vec<String>,
    pub stakes: HashMap<String, u64>, // validator -> stake weight
}

#[derive(Clone)]
pub struct PbftService {
    state: Arc<RwLock<PbftState>>,
    // phase tracking per (height,round)
    phases: Arc<RwLock<HashMap<(u64,u64), PhaseVotes>>>,
    round_starts: Arc<RwLock<HashMap<(u64,u64), Instant>>>, // retained for backwards compatibility metrics
}

impl PbftService {
    pub fn new() -> Self {
        let size: usize = std::env::var("VALIDATOR_SET_SIZE").ok().and_then(|v| v.parse().ok()).unwrap_or(4);
        let validators = (0..size).map(|i| format!("node-{}", i)).collect::<Vec<_>>();
        let stakes = parse_stakes_env(&validators);
        let leader = weighted_leader(0, 0, &validators, &stakes).unwrap_or_else(|| validators.first().cloned().unwrap_or_default());
    let svc = Self { state: Arc::new(RwLock::new(PbftState { validators: validators.clone(), leader, stakes, ..Default::default() })), phases: Arc::new(RwLock::new(HashMap::new())), round_starts: Arc::new(RwLock::new(HashMap::new())) };
    svc.load_votes(); // legacy votes loader (noop with new structure) kept for compatibility
        // spawn view change timer task
        svc.spawn_view_change_task();
        // register callback to report height gauge periodically
        let state_clone = svc.state.clone();
        let _ = CONSENSUS_METER.register_callback(&[HEIGHT_GAUGE.as_any()], move |obs| {
            if let Ok(st) = state_clone.read() { obs.observe_u64(&HEIGHT_GAUGE, st.height, &[]); }
        });
        svc
    }
    pub fn snapshot(&self) -> PbftState { self.state.read().unwrap().clone() }

    fn quorum(&self) -> usize {
        let st = self.state.read().unwrap();
        ((st.validators.len() * 2) / 3) + 1
    }

    fn record_prepare(&self, height: u64, round: u64, node: &str) -> usize {
        let mut map = self.phases.write().unwrap();
        let entry = map.entry((height,round)).or_insert_with(PhaseVotes::default);
        entry.prepares.insert(node.to_string());
        PREPARE_TOTAL.add(1, &[]);
        if let Some(db) = &*DB { let _ = db.insert(format!("prepare:{}:{}:{}", height, round, node), &[]); }
        entry.prepares.len()
    }

    fn record_commit(&self, height: u64, round: u64, node: &str) -> usize {
        let mut map = self.phases.write().unwrap();
        let entry = map.entry((height,round)).or_insert_with(PhaseVotes::default);
        entry.commits.insert(node.to_string());
        COMMIT_TOTAL.add(1, &[]);
        if let Some(db) = &*DB { let _ = db.insert(format!("commit:{}:{}:{}", height, round, node), &[]); }
        entry.commits.len()
    }

    fn elect_leader(&self, height: u64, round: u64) {
        let mut st = self.state.write().unwrap();
        if st.validators.is_empty() { return; }
        if let Some(l) = weighted_leader(height, round, &st.validators, &st.stakes) { st.leader = l; }
    }

    fn load_votes(&self) {
        if let Some(db) = &*DB {
            let mut phases = self.phases.write().unwrap();
            let mut restored_prepare = 0usize; let mut restored_commit = 0usize;
            for kv in db.scan_prefix("prepare:") { if let Ok((k,_)) = kv { if let Ok(s) = std::str::from_utf8(&k) {
                let parts: Vec<&str> = s.split(':').collect();
                if parts.len()==4 { if let (Ok(h), Ok(r)) = (parts[1].parse::<u64>(), parts[2].parse::<u64>()) { phases.entry((h,r)).or_insert_with(PhaseVotes::default).prepares.insert(parts[3].to_string()); restored_prepare+=1; } }
            }}}
            for kv in db.scan_prefix("commit:") { if let Ok((k,_)) = kv { if let Ok(s) = std::str::from_utf8(&k) {
                let parts: Vec<&str> = s.split(':').collect();
                if parts.len()==4 { if let (Ok(h), Ok(r)) = (parts[1].parse::<u64>(), parts[2].parse::<u64>()) { phases.entry((h,r)).or_insert_with(PhaseVotes::default).commits.insert(parts[3].to_string()); restored_commit+=1; } }
            }}}
            tracing::info!(restored_prepare, restored_commit, "restored_phase_votes_from_persistence");
        }
    }
}

#[async_trait]
impl Pbft for PbftService {
    #[instrument(skip(self, request), fields(proposal.id = %request.get_ref().id))]
    async fn propose(&self, request: Request<Proposal>) -> Result<Response<Ack>, Status> {
        let prop = request.into_inner();
        let mut broadcast = None;
        {
            let mut st = self.state.write().unwrap();
            if prop.height > st.height { st.height = prop.height; st.round = prop.round; broadcast = Some((st.height, st.round)); }
        }
        if let Some((h,r)) = broadcast {
            // mark pre-prepare (start) for phase metrics
            let mut phases = self.phases.write().unwrap();
            let entry = phases.entry((h,r)).or_insert_with(PhaseVotes::default);
            entry.pre_prepare_seen = true;
            entry.start.get_or_insert(Instant::now());
            self.round_starts.write().unwrap().insert((h,r), Instant::now());
        }
        // Leader re-elected on new height
        if let Some((h,r)) = broadcast { self.elect_leader(h, r); }
        if let Some((h,r)) = broadcast { tokio::spawn(async move { super::publish_height_changed_versioned(h,r).await; }); }
        Ok(Response::new(Ack { accepted: true, reason: "accepted".into() }))
    }

    #[instrument(skip(self, request), fields(vote.proposal_id = %request.get_ref().proposal_id))]
    async fn cast_vote(&self, request: Request<Vote>) -> Result<Response<Ack>, Status> {
        let vote = request.into_inner();
        {
            let mut st = self.state.write().unwrap();
            if vote.height > st.height { st.height = vote.height; st.round = vote.round; }
        }
        let quorum = self.quorum();
        match vote.vote_type {
            0 => { // PREPARE
                let prepares = self.record_prepare(vote.height, vote.round, &vote.node_id);
                if prepares >= quorum { tracing::debug!(height=vote.height, round=vote.round, prepares, quorum, "prepare_quorum_reached"); }
                Ok(Response::new(Ack { accepted: true, reason: "prepare recorded".into() }))
            }
            1 => { // COMMIT
                let commits = self.record_commit(vote.height, vote.round, &vote.node_id);
                if commits >= quorum {
                    self.elect_leader(vote.height, vote.round);
                    tracing::info!(height=vote.height, round=vote.round, quorum, commits, leader=%self.snapshot().leader, "commit_quorum_reached");
                    // finalize round metrics
                    if let Some(start) = self.round_starts.write().unwrap().remove(&(vote.height, vote.round)) {
                        let elapsed = start.elapsed();
                        ROUND_HIST_MS.record(elapsed.as_secs_f64()*1000.0, &[]);
                        ROUND_HIST_S.record(elapsed.as_secs_f64(), &[]);
                    }
                    let h = vote.height; let r = vote.round;
                    tokio::spawn(async move { super::publish_round_changed(h,r).await; });
                }
                Ok(Response::new(Ack { accepted: true, reason: "commit recorded".into() }))
            }
            _ => Err(Status::invalid_argument("unknown vote type"))
        }
    }

    #[instrument(skip(self), fields(query.height = %request.get_ref().height))]
    async fn get_state(&self, request: Request<ConsensusStateQuery>) -> Result<Response<ConsensusState>, Status> {
        let q = request.into_inner();
        let st = self.state.read().unwrap();
        if q.height != 0 && q.height != st.height { return Err(Status::not_found("height not found")); }
        Ok(Response::new(ConsensusState { height: st.height, round: st.round, leader: st.leader.clone() }))
    }
}

// --- PoS Weighted Leader Selection Utilities ---

fn parse_stakes_env(validators: &[String]) -> HashMap<String, u64> {
    let mut map: HashMap<String, u64> = HashMap::new();
    let raw = std::env::var("CONSENSUS_VALIDATOR_STAKES").unwrap_or_default();
    if !raw.is_empty() {
        for part in raw.split(',') {
            let kv: Vec<&str> = part.split('=').collect();
            if kv.len()==2 { if let Ok(v) = kv[1].parse::<u64>() { map.insert(kv[0].trim().to_string(), v.max(1)); } }
        }
    }
    // ensure every validator present with at least 1
    for v in validators { map.entry(v.clone()).or_insert(1); }
    map
}

/// Deterministic weighted leader via exponential race method: score = -ln(U)/stake; pick minimal score.
fn weighted_leader(height: u64, round: u64, validators: &[String], stakes: &HashMap<String,u64>) -> Option<String> {
    if validators.is_empty() { return None; }
    let mut best: Option<(f64, String)> = None;
    let mut seed_bytes = [0u8; 16];
    seed_bytes[..8].copy_from_slice(&height.to_le_bytes());
    seed_bytes[8..].copy_from_slice(&round.to_le_bytes());
    for v in validators {
        let stake = *stakes.get(v).unwrap_or(&1) as f64;
        let mut hasher = Sha256::new();
        hasher.update(&seed_bytes);
        hasher.update(v.as_bytes());
        let h = hasher.finalize();
        // Use first 8 bytes as u64 to make U in (0,1]
        let mut arr = [0u8;8]; arr.copy_from_slice(&h[..8]);
        let raw = u64::from_le_bytes(arr);
        let u = ((raw as f64) / (u64::MAX as f64)).clamp(1e-12, 0.999_999_999_999); // avoid 0
        let score = -u.ln() / stake; // smaller better
        match &best { Some((b, _)) if *b <= score => {}, _ => { best = Some((score, v.clone())); } }
    }
    best.map(|(_,v)| v)
}

#[cfg(test)]
mod pos_tests {
    use super::*;

    #[test]
    fn weighted_leader_bias() {
        let validators = vec!["node-0".to_string(), "node-1".to_string()];
        let stakes = HashMap::from([(validators[0].clone(), 100u64), (validators[1].clone(), 1u64)]);
        let mut count0 = 0u64;
        for h in 1..500u64 { // vary height as entropy source
            let l = weighted_leader(h, 0, &validators, &stakes).unwrap();
            if l == "node-0" { count0 += 1; }
        }
        // Expect high-stake validator wins majority strongly
        assert!(count0 as f64 / 499.0 > 0.80, "bias too low: {}", count0);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tonic::Code;

    #[tokio::test]
    async fn propose_increases_height() {
        let svc = PbftService::new();
        for h in 1..=5 { let _ = svc.propose(Request::new(Proposal { id: h.to_string(), payload: vec![], height: h, round: 0 })).await.unwrap(); }
        let snap = svc.snapshot();
        assert_eq!(snap.height, 5);
    }

    #[tokio::test]
    async fn get_state_not_found_for_future_height() {
        let svc = PbftService::new();
        let resp = svc.get_state(Request::new(ConsensusStateQuery { height: 10 })).await;
        assert!(matches!(resp, Err(Status{ code: c, .. }) if c == Code::NotFound));
    }

    #[tokio::test]
    async fn cast_vote_updates_round() {
        let svc = PbftService::new();
        // first propose sets height 1 round 0
        let _ = svc.propose(Request::new(Proposal { id: "p1".into(), payload: vec![], height: 1, round: 0 })).await.unwrap();
        // vote with higher round increments round
        let _ = svc.cast_vote(Request::new(Vote { proposal_id: "p1".into(), node_id: "n1".into(), height: 1, round: 2, vote_type: 0 })).await.unwrap();
        let snap = svc.snapshot();
        assert_eq!(snap.round, 2);
        assert_eq!(snap.height, 1);
    }
}