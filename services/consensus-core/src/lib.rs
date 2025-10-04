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
pub mod optimized_pbft; // New optimized consensus module
pub mod validator_manager; // Validator manager with VRF-based selection
pub mod fast_path_pbft; // Fast-path optimization and batch aggregation
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
    checkpoint_created: bool,
    batch_size: usize,
}

#[derive(Debug, Default)]
pub struct PbftState {
    pub height: u64,
    pub round: u64,
    pub leader: String,
    pub validators: Vec<String>,
    pub stakes: HashMap<String, u64>, // validator -> stake weight
    pub last_checkpoint: u64,
    pub checkpoint_interval: u64,
    pub byzantine_faults_detected: usize,
    pub total_rounds: u64,
    pub jailed_validators: HashSet<String>, // validators temporarily jailed for misbehavior
    pub jail_release_heights: HashMap<String, u64>, // validator -> height when jail expires
    pub slashing_records: Vec<swarm_core::crypto_vrf::SlashingRecord>, // history of slashing events
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
        let checkpoint_interval = std::env::var("CONSENSUS_CHECKPOINT_INTERVAL").ok().and_then(|v| v.parse().ok()).unwrap_or(100u64);
    let svc = Self { 
        state: Arc::new(RwLock::new(PbftState { 
            validators: validators.clone(), 
            leader, 
            stakes, 
            checkpoint_interval,
            ..Default::default() 
        })), 
        phases: Arc::new(RwLock::new(HashMap::new())), 
        round_starts: Arc::new(RwLock::new(HashMap::new())) 
    };
    svc.load_votes(); // legacy votes loader (noop with new structure) kept for compatibility
        // spawn view change timer task
        svc.spawn_view_change_task();
        // spawn checkpoint pruner task
        svc.spawn_checkpoint_task();
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

    /// Checkpoint task: persist state snapshots every N blocks for fast recovery
    fn spawn_checkpoint_task(&self) {
        let state = self.state.clone();
        let phases = self.phases.clone();
        let svc_clone = self.clone(); // Clone service for jail release
        tokio::spawn(async move {
            let mut last_checkpoint = 0u64;
            loop {
                tokio::time::sleep(Duration::from_secs(10)).await;
                let (current_height, interval) = {
                    let st = state.read().unwrap();
                    (st.height, st.checkpoint_interval)
                };
                
                // Release jailed validators whose jail period has expired
                svc_clone.release_jailed_validators(current_height);
                
                if current_height >= last_checkpoint + interval {
                    if let Some(db) = &*DB {
                        let checkpoint_key = format!("checkpoint:{}", current_height);
                        let st = state.read().unwrap();
                        let checkpoint_data = serde_json::json!({
                            "height": st.height,
                            "round": st.round,
                            "leader": st.leader,
                            "validators": st.validators,
                            "stakes": st.stakes,
                            "byzantine_faults": st.byzantine_faults_detected,
                            "jailed_validators": st.jailed_validators,
                            "slashing_records_count": st.slashing_records.len(),
                        }).to_string();
                        let _ = db.insert(checkpoint_key.as_bytes(), checkpoint_data.as_bytes());
                        last_checkpoint = current_height;
                        tracing::info!(height=current_height, "checkpoint_created");
                        
                        // Prune old phase votes (keep last 200 rounds)
                        let mut p = phases.write().unwrap();
                        p.retain(|(h, _)| *h + 200 >= current_height);
                    }
                    {
                        let mut st = state.write().unwrap();
                        st.last_checkpoint = current_height;
                    }
                }
            }
        });
    }

    /// Detect Byzantine behavior: conflicting votes from same validator
    fn detect_byzantine(&self, height: u64, round: u64, node: &str) -> bool {
        let phases = self.phases.read().unwrap();
        if let Some(entry) = phases.get(&(height, round)) {
            // If node already in prepares AND trying to commit different proposal -> Byzantine
            // In real impl, would check digest mismatch
            if entry.prepares.contains(node) && entry.commits.contains(node) {
                let mut st = self.state.write().unwrap();
                st.byzantine_faults_detected += 1;
                tracing::warn!(height, round, node, "byzantine_behavior_detected");
                
                let meter = opentelemetry::global::meter("consensus-core");
                let byz_counter = meter.u64_counter("swarm_consensus_byzantine_detected_total")
                    .with_description("Total Byzantine faults detected")
                    .init();
                byz_counter.add(1, &[]);
                
                // Slash the Byzantine validator
                self.slash_validator(
                    node,
                    swarm_core::crypto_vrf::SlashReason::ByzantineBehavior,
                    height
                );
                
                return true;
            }
        }
        false
    }
    
    /// Slash validator for misbehavior
    fn slash_validator(&self, validator: &str, reason: swarm_core::crypto_vrf::SlashReason, height: u64) {
        use swarm_core::crypto_vrf::{calculate_slash_amount, SlashingConfig, SlashingRecord};
        
        let mut st = self.state.write().unwrap();
        
        // Get current stake
        let current_stake = *st.stakes.get(validator).unwrap_or(&0);
        if current_stake == 0 {
            tracing::warn!(validator, "cannot slash validator with zero stake");
            return;
        }
        
        // Calculate slash amount based on reason
        let config = SlashingConfig::default();
        let slash_amount = calculate_slash_amount(current_stake, reason, &config);
        
        // Apply slashing
        let new_stake = current_stake.saturating_sub(slash_amount);
        st.stakes.insert(validator.to_string(), new_stake);
        
        // Jail validator
        st.jailed_validators.insert(validator.to_string());
        let release_height = height + config.jail_duration_blocks;
        st.jail_release_heights.insert(validator.to_string(), release_height);
        
        // Record slashing event
        let record = SlashingRecord {
            validator: validator.to_string(),
            slash_height: height,
            slash_reason: reason,
            slashed_amount: slash_amount,
            timestamp: chrono::Utc::now().timestamp(),
        };
        st.slashing_records.push(record.clone());
        
        tracing::warn!(
            validator,
            ?reason,
            slash_amount,
            new_stake,
            release_height,
            "validator_slashed_and_jailed"
        );
        
        // Emit metrics
        let meter = opentelemetry::global::meter("consensus-core");
        let slash_counter = meter.u64_counter("swarm_consensus_slashing_total")
            .with_description("Total validator slashing events")
            .init();
        slash_counter.add(1, &[]);
        
        let slashed_stake_counter = meter.u64_counter("swarm_consensus_slashed_stake_total")
            .with_description("Total stake slashed (cumulative)")
            .init();
        slashed_stake_counter.add(slash_amount, &[]);
        
        // Persist to database
        if let Some(db) = &*DB {
            if let Ok(json) = serde_json::to_vec(&record) {
                let key = format!("slash:{}:{}", height, validator);
                let _ = db.insert(key, json);
            }
        }
    }
    
    /// Release jailed validators whose jail period has expired
    fn release_jailed_validators(&self, current_height: u64) {
        let mut st = self.state.write().unwrap();
        
        let mut to_release = Vec::new();
        for (validator, release_height) in st.jail_release_heights.iter() {
            if current_height >= *release_height {
                to_release.push(validator.clone());
            }
        }
        
        for validator in to_release {
            st.jailed_validators.remove(&validator);
            st.jail_release_heights.remove(&validator);
            tracing::info!(validator, current_height, "validator_released_from_jail");
        }
    }

    /// Batch verify signatures (stub for production integration)
    fn batch_verify_signatures(&self, messages: &[String]) -> bool {
        // In production: use BLS aggregate signatures
        // For now, return true (assumes pre-verified)
        if messages.is_empty() {
            return false;
        }
        tracing::debug!(batch_size=messages.len(), "batch_signature_verification");
        true
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
        
        // Byzantine detection
        if self.detect_byzantine(vote.height, vote.round, &vote.node_id) {
            return Err(Status::invalid_argument("Byzantine behavior detected"));
        }
        
        {
            let mut st = self.state.write().unwrap();
            if vote.height > st.height { st.height = vote.height; st.round = vote.round; }
            st.total_rounds = st.total_rounds.max(vote.round);
        }
        let quorum = self.quorum();
        match vote.vote_type {
            0 => { // PREPARE
                let prepares = self.record_prepare(vote.height, vote.round, &vote.node_id);
                if prepares >= quorum { 
                    tracing::debug!(height=vote.height, round=vote.round, prepares, quorum, "prepare_quorum_reached");
                    // Auto-advance to commit phase
                    let mut phases = self.phases.write().unwrap();
                    if let Some(entry) = phases.get_mut(&(vote.height, vote.round)) {
                        entry.batch_size = prepares;
                    }
                }
                Ok(Response::new(Ack { accepted: true, reason: "prepare recorded".into() }))
            }
            1 => { // COMMIT
                let commits = self.record_commit(vote.height, vote.round, &vote.node_id);
                if commits >= quorum {
                    self.elect_leader(vote.height, vote.round);
                    tracing::info!(height=vote.height, round=vote.round, quorum, commits, leader=%self.snapshot().leader, "commit_quorum_reached");
                    
                    // Check if checkpoint needed
                    let should_checkpoint = {
                        let st = self.state.read().unwrap();
                        vote.height > 0 && vote.height % st.checkpoint_interval == 0
                    };
                    
                    if should_checkpoint {
                        let mut phases = self.phases.write().unwrap();
                        if let Some(entry) = phases.get_mut(&(vote.height, vote.round)) {
                            entry.checkpoint_created = true;
                        }
                        tracing::info!(height=vote.height, "checkpoint_triggered_by_commit");
                    }
                    
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

/// VRF-based weighted leader selection (deterministic & verifiable)
/// 
/// Uses VRF to generate verifiable randomness, then applies Follow-the-Satoshi
/// algorithm to select leader proportional to stake.
/// 
/// Benefits over old exponential race:
/// - Verifiable: anyone can check leader selection is correct
/// - Unpredictable: cannot manipulate selection without secret key
/// - Fair: follows stake distribution exactly (not probabilistic approximation)
fn weighted_leader(height: u64, round: u64, validators: &[String], stakes: &HashMap<String,u64>) -> Option<String> {
    if validators.is_empty() { return None; }
    
    // VRF input alpha = height || round (deterministic per consensus epoch)
    let mut alpha = Vec::new();
    alpha.extend_from_slice(&height.to_le_bytes());
    alpha.extend_from_slice(&round.to_le_bytes());
    
    // In production: use actual VRF secret key for this node
    // For deterministic testing: derive from seed based on height/round
    let (vrf_sk, _vrf_pk) = {
        use swarm_core::crypto_vrf::{generate_vrf_keypair};
        let mut seed = Vec::new();
        seed.extend_from_slice(b"consensus-vrf-seed");
        seed.extend_from_slice(&height.to_le_bytes());
        seed.extend_from_slice(&round.to_le_bytes());
        generate_vrf_keypair(&seed)
    };
    
    // Generate VRF proof and output
    let (_proof, vrf_output) = {
        use swarm_core::crypto_vrf::vrf_prove;
        vrf_prove(&vrf_sk, &alpha)
    };
    
    // Build validator list with stakes for selection
    let validator_stakes: Vec<(String, u64)> = validators.iter()
        .map(|v| (v.clone(), *stakes.get(v).unwrap_or(&1)))
        .collect();
    
    // Use VRF output to select validator with Follow-the-Satoshi
    use swarm_core::crypto_vrf::select_validator_with_vrf;
    select_validator_with_vrf(&vrf_output, &validator_stakes)
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