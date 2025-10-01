use std::collections::{HashMap, HashSet};
use std::sync::{Arc, RwLock};
use std::time::Instant;
use once_cell::sync::Lazy;
static DB: Lazy<Option<sled::Db>> = Lazy::new(|| {
    let path = std::env::var("CONSENSUS_DB_PATH").unwrap_or_else(|_| "./data/consensus".into());
    match sled::open(path) { Ok(db) => Some(db), Err(e) => { tracing::warn!(error=?e, "sled open failed - running ephemeral"); None } }
});
use async_trait::async_trait;
use tonic::{Request, Response, Status};
use swarm_proto::consensus::{pbft_server::Pbft, Proposal, Vote, Ack, ConsensusStateQuery, ConsensusState};
use tracing::instrument;
mod view_change;

#[derive(Debug, Default)]
pub struct PbftState {
    pub height: u64,
    pub round: u64,
    pub leader: String,
    pub validators: Vec<String>,
}

#[derive(Clone)]
pub struct PbftService {
    state: Arc<RwLock<PbftState>>,
    votes: Arc<RwLock<HashMap<(u64,u64), HashSet<String>>>>, // (height,round) -> voters
    round_starts: Arc<RwLock<HashMap<(u64,u64), Instant>>>, // track start time for (height,round)
}

impl PbftService {
    pub fn new() -> Self {
        let size: usize = std::env::var("VALIDATOR_SET_SIZE").ok().and_then(|v| v.parse().ok()).unwrap_or(4);
        let validators = (0..size).map(|i| format!("node-{}", i)).collect::<Vec<_>>();
        let leader = validators.first().cloned().unwrap_or_default();
    let svc = Self { state: Arc::new(RwLock::new(PbftState { validators: validators.clone(), leader, ..Default::default() })), votes: Arc::new(RwLock::new(HashMap::new())), round_starts: Arc::new(RwLock::new(HashMap::new())) };
        svc.load_votes();
        // spawn view change timer task
        svc.spawn_view_change_task();
        svc
    }
    pub fn snapshot(&self) -> PbftState { self.state.read().unwrap().clone() }

    fn quorum(&self) -> usize {
        let st = self.state.read().unwrap();
        ((st.validators.len() * 2) / 3) + 1
    }

    fn record_vote(&self, height: u64, round: u64, node: &str) -> usize {
        let mut map = self.votes.write().unwrap();
        let entry = map.entry((height, round)).or_insert_with(HashSet::new);
        entry.insert(node.to_string());
        // persist single vote (idempotent based on key)
        if let Some(db) = &*DB { let _ = db.insert(format!("vote:{}:{}:{}", height, round, node), &[]); }
        entry.len()
    }

    fn elect_leader(&self, height: u64, round: u64) {
        let mut st = self.state.write().unwrap();
        if st.validators.is_empty() { return; }
        let idx = (height + round) as usize % st.validators.len();
        st.leader = st.validators[idx].clone();
    }

    fn load_votes(&self) {
        if let Some(db) = &*DB {
            let mut map = self.votes.write().unwrap();
            for kv in db.scan_prefix("vote:") { if let Ok((k,_)) = kv {
                if let Ok(s) = std::str::from_utf8(&k) { // vote:height:round:node
                    let parts: Vec<&str> = s.split(':').collect();
                    if parts.len()==4 { if let (Ok(h), Ok(r)) = (parts[1].parse::<u64>(), parts[2].parse::<u64>()) {
                        map.entry((h,r)).or_insert_with(HashSet::new).insert(parts[3].to_string());
                    }}
                }
            }}
            tracing::info!(restored_votes=map.len(), "restored_votes_from_persistence");
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
        // record round start time (height,round)
        if let Some((h,r)) = broadcast {
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
        let count = self.record_vote(vote.height, vote.round, &vote.node_id);
        let quorum = self.quorum();
        if count >= quorum {
            self.elect_leader(vote.height, vote.round);
            tracing::info!(height=vote.height, round=vote.round, quorum=%quorum, votes=%count, leader=%self.snapshot().leader, "quorum_reached");
            // record round progress duration metric
            if let Some(start) = self.round_starts.write().unwrap().remove(&(vote.height, vote.round)) {
                let dur_ms = start.elapsed().as_secs_f64() * 1000.0;
                let meter = opentelemetry::global::meter("consensus-core");
                let hist = meter.f64_histogram("consensus_round_progress_ms").with_description("Time from propose to quorum for a (height,round)").init();
                hist.record(dur_ms, &[]);
            }
            let h = vote.height; let r = vote.round;
            tokio::spawn(async move { super::publish_round_changed(h,r).await; });
        }
        Ok(Response::new(Ack { accepted: true, reason: "vote recorded".into() }))
    }

    #[instrument(skip(self), fields(query.height = %request.get_ref().height))]
    async fn get_state(&self, request: Request<ConsensusStateQuery>) -> Result<Response<ConsensusState>, Status> {
        let q = request.into_inner();
        let st = self.state.read().unwrap();
        if q.height != 0 && q.height != st.height { return Err(Status::not_found("height not found")); }
        Ok(Response::new(ConsensusState { height: st.height, round: st.round, leader: st.leader.clone() }))
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