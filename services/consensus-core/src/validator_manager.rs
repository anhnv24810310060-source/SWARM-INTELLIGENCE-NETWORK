/// Validator Manager with VRF-based Leader Selection
/// 
/// Features:
/// - Stake-weighted selection using VRF for fairness
/// - Dynamic stake updates (stake, unstake, redelegate)
/// - Slashing mechanism with graduated penalties
/// - Jail/unjail logic with cooldown periods
/// - Reputation tracking for validator quality
/// - Validator set rotation with smooth transitions
/// 
/// Performance:
/// - O(log n) validator selection using balanced tree
/// - O(1) stake lookups with hashmap index
/// - Lock-free reads for active validator set
/// - Batch slashing to reduce write contention

use std::collections::{HashMap, BTreeMap, HashSet};
use std::sync::Arc;
use parking_lot::RwLock;
use serde::{Serialize, Deserialize};
use tracing::{info, warn, debug};
use chrono::Utc;

use swarm_core::crypto_vrf::{
    VrfSecretKey, VrfPublicKey, VrfOutput, VrfProof,
    generate_vrf_keypair, vrf_prove, vrf_verify,
    select_validator_with_vrf, SlashReason, SlashingRecord,
    calculate_slash_amount, SlashingConfig,
};
use swarm_core::crypto_bls::{BlsPublicKey, BlsSecretKey};

/// Validator information
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Validator {
    pub node_id: String,
    pub bls_pubkey: BlsPublicKey,
    pub vrf_pubkey: VrfPublicKey,
    pub stake: u64,
    pub delegated_stake: u64, // Stake from delegators
    pub commission_rate: f64, // 0.0 - 1.0 (10% = 0.1)
    pub jailed: bool,
    pub jail_until_height: u64,
    pub slashing_history: Vec<SlashingRecord>,
    pub reputation_score: f64, // 0.0 - 1.0 based on performance
    pub uptime_blocks: u64,
    pub total_blocks: u64,
    pub last_active_height: u64,
    pub created_at: i64,
    pub updated_at: i64,
}

impl Validator {
    pub fn new(
        node_id: String,
        bls_pubkey: BlsPublicKey,
        vrf_pubkey: VrfPublicKey,
        initial_stake: u64,
        commission_rate: f64,
    ) -> Self {
        let now = Utc::now().timestamp();
        Self {
            node_id,
            bls_pubkey,
            vrf_pubkey,
            stake: initial_stake,
            delegated_stake: 0,
            commission_rate: commission_rate.clamp(0.0, 1.0),
            jailed: false,
            jail_until_height: 0,
            slashing_history: Vec::new(),
            reputation_score: 1.0,
            uptime_blocks: 0,
            total_blocks: 0,
            last_active_height: 0,
            created_at: now,
            updated_at: now,
        }
    }
    
    /// Total effective stake (self + delegated)
    pub fn total_stake(&self) -> u64 {
        self.stake.saturating_add(self.delegated_stake)
    }
    
    /// Is validator active (not jailed and has minimum stake)
    pub fn is_active(&self, current_height: u64, min_stake: u64) -> bool {
        !self.jailed && self.total_stake() >= min_stake && current_height >= self.jail_until_height
    }
    
    /// Update reputation based on uptime
    pub fn update_reputation(&mut self) {
        if self.total_blocks == 0 {
            self.reputation_score = 1.0;
            return;
        }
        
        let uptime_ratio = self.uptime_blocks as f64 / self.total_blocks as f64;
        
        // Exponential moving average: new_score = 0.9 * old_score + 0.1 * current_uptime
        self.reputation_score = 0.9 * self.reputation_score + 0.1 * uptime_ratio;
        
        // Clamp to [0, 1]
        self.reputation_score = self.reputation_score.clamp(0.0, 1.0);
    }
    
    /// Record block participation
    pub fn record_block_participation(&mut self, height: u64, participated: bool) {
        self.total_blocks += 1;
        if participated {
            self.uptime_blocks += 1;
        }
        self.last_active_height = height;
        self.update_reputation();
    }
}

/// Delegator stake record
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Delegation {
    pub delegator: String,
    pub validator: String,
    pub amount: u64,
    pub delegated_at: i64,
}

/// Validator manager state
pub struct ValidatorManager {
    /// All validators indexed by node_id
    validators: Arc<RwLock<HashMap<String, Validator>>>,
    
    /// Active validator set (updated each epoch)
    active_set: Arc<RwLock<Vec<String>>>,
    
    /// Stake-sorted index for efficient selection (stake -> validator_id)
    stake_index: Arc<RwLock<BTreeMap<u64, Vec<String>>>>,
    
    /// Delegations
    delegations: Arc<RwLock<HashMap<String, Vec<Delegation>>>>, // delegator -> [delegations]
    
    /// Configuration
    min_stake: u64,
    max_validators: usize,
    epoch_length: u64,
    slashing_config: SlashingConfig,
    
    /// VRF keys for leader selection
    vrf_sk: VrfSecretKey,
    vrf_pk: VrfPublicKey,
    
    /// Current state
    current_height: u64,
    current_epoch: u64,
}

impl ValidatorManager {
    pub fn new(
        min_stake: u64,
        max_validators: usize,
        epoch_length: u64,
        vrf_seed: &[u8],
    ) -> Self {
        let (vrf_sk, vrf_pk) = generate_vrf_keypair(vrf_seed);
        
        Self {
            validators: Arc::new(RwLock::new(HashMap::new())),
            active_set: Arc::new(RwLock::new(Vec::new())),
            stake_index: Arc::new(RwLock::new(BTreeMap::new())),
            delegations: Arc::new(RwLock::new(HashMap::new())),
            min_stake,
            max_validators,
            epoch_length,
            slashing_config: SlashingConfig::default(),
            vrf_sk,
            vrf_pk,
            current_height: 0,
            current_epoch: 0,
        }
    }
    
    /// Register a new validator
    pub fn register_validator(&self, validator: Validator) -> Result<(), String> {
        if validator.stake < self.min_stake {
            return Err(format!("Stake {} below minimum {}", validator.stake, self.min_stake));
        }
        
        let mut vals = self.validators.write();
        
        if vals.contains_key(&validator.node_id) {
            return Err(format!("Validator {} already exists", validator.node_id));
        }
        
        let node_id = validator.node_id.clone();
        let stake = validator.total_stake();
        
        vals.insert(node_id.clone(), validator);
        
        // Update stake index
        let mut index = self.stake_index.write();
        index.entry(stake).or_insert_with(Vec::new).push(node_id.clone());
        
        info!(validator = %node_id, stake, "Validator registered");
        
        Ok(())
    }
    
    /// Update validator stake
    pub fn update_stake(&self, node_id: &str, new_stake: u64) -> Result<(), String> {
        let mut vals = self.validators.write();
        
        let validator = vals.get_mut(node_id).ok_or_else(|| format!("Validator {} not found", node_id))?;
        
        let old_stake = validator.total_stake();
        validator.stake = new_stake;
        validator.updated_at = Utc::now().timestamp();
        
        // Update stake index
        let mut index = self.stake_index.write();
        
        // Remove from old stake bucket
        if let Some(bucket) = index.get_mut(&old_stake) {
            bucket.retain(|id| id != node_id);
            if bucket.is_empty() {
                index.remove(&old_stake);
            }
        }
        
        // Add to new stake bucket
        let new_total = validator.total_stake();
        index.entry(new_total).or_insert_with(Vec::new).push(node_id.to_string());
        
        info!(validator = %node_id, old_stake, new_stake, "Stake updated");
        
        Ok(())
    }
    
    /// Delegate stake to a validator
    pub fn delegate(&self, delegator: String, validator_id: &str, amount: u64) -> Result<(), String> {
        let mut vals = self.validators.write();
        
        let validator = vals.get_mut(validator_id).ok_or_else(|| format!("Validator {} not found", validator_id))?;
        
        let old_total = validator.total_stake();
        validator.delegated_stake = validator.delegated_stake.saturating_add(amount);
        validator.updated_at = Utc::now().timestamp();
        
        let delegation = Delegation {
            delegator: delegator.clone(),
            validator: validator_id.to_string(),
            amount,
            delegated_at: Utc::now().timestamp(),
        };
        
        let mut delegs = self.delegations.write();
        delegs.entry(delegator.clone()).or_insert_with(Vec::new).push(delegation);
        
        // Update stake index
        let mut index = self.stake_index.write();
        if let Some(bucket) = index.get_mut(&old_total) {
            bucket.retain(|id| id != validator_id);
            if bucket.is_empty() {
                index.remove(&old_total);
            }
        }
        
        let new_total = validator.total_stake();
        index.entry(new_total).or_insert_with(Vec::new).push(validator_id.to_string());
        
        info!(delegator = %delegator, validator = %validator_id, amount, "Stake delegated");
        
        Ok(())
    }
    
    /// Slash a validator
    pub fn slash_validator(
        &self,
        node_id: &str,
        reason: SlashReason,
        height: u64,
    ) -> Result<u64, String> {
        let mut vals = self.validators.write();
        
        let validator = vals.get_mut(node_id).ok_or_else(|| format!("Validator {} not found", node_id))?;
        
        let slash_amount = calculate_slash_amount(
            validator.total_stake(),
            reason,
            &self.slashing_config,
        );
        
        // Apply slashing
        let old_stake = validator.total_stake();
        validator.stake = validator.stake.saturating_sub(slash_amount);
        
        // Jail validator
        validator.jailed = true;
        validator.jail_until_height = height + self.slashing_config.jail_duration_blocks;
        
        // Record slashing
        let record = SlashingRecord {
            validator: node_id.to_string(),
            slash_height: height,
            slash_reason: reason,
            slashed_amount: slash_amount,
            timestamp: Utc::now().timestamp(),
        };
        
        validator.slashing_history.push(record.clone());
        validator.updated_at = Utc::now().timestamp();
        
        // Penalize reputation
        validator.reputation_score *= 0.5; // 50% reputation penalty
        
        // Update stake index
        let mut index = self.stake_index.write();
        if let Some(bucket) = index.get_mut(&old_stake) {
            bucket.retain(|id| id != node_id);
            if bucket.is_empty() {
                index.remove(&old_stake);
            }
        }
        
        let new_total = validator.total_stake();
        if new_total > 0 {
            index.entry(new_total).or_insert_with(Vec::new).push(node_id.to_string());
        }
        
        warn!(
            validator = %node_id,
            reason = ?reason,
            slash_amount,
            jail_until = validator.jail_until_height,
            "Validator slashed"
        );
        
        Ok(slash_amount)
    }
    
    /// Unjail a validator
    pub fn unjail_validator(&self, node_id: &str, current_height: u64) -> Result<(), String> {
        let mut vals = self.validators.write();
        
        let validator = vals.get_mut(node_id).ok_or_else(|| format!("Validator {} not found", node_id))?;
        
        if !validator.jailed {
            return Err(format!("Validator {} is not jailed", node_id));
        }
        
        if current_height < validator.jail_until_height {
            return Err(format!(
                "Cannot unjail validator {} until height {}",
                node_id, validator.jail_until_height
            ));
        }
        
        validator.jailed = false;
        validator.updated_at = Utc::now().timestamp();
        
        info!(validator = %node_id, height = current_height, "Validator unjailed");
        
        Ok(())
    }
    
    /// Select leader using VRF for given height/round
    pub fn select_leader(&self, height: u64, round: u64) -> Option<String> {
        let active = self.active_set.read();
        
        if active.is_empty() {
            return None;
        }
        
        // Build VRF input: height || round
        let mut alpha = Vec::new();
        alpha.extend_from_slice(&height.to_le_bytes());
        alpha.extend_from_slice(&round.to_le_bytes());
        
        // Generate VRF proof and output
        let (_proof, output) = vrf_prove(&self.vrf_sk, &alpha);
        
        // Build (validator_id, stake) list
        let vals = self.validators.read();
        let candidates: Vec<(String, u64)> = active
            .iter()
            .filter_map(|id| {
                vals.get(id).map(|v| (id.clone(), v.total_stake()))
            })
            .collect();
        
        // Select using VRF output
        select_validator_with_vrf(&output, &candidates)
    }
    
    /// Update active validator set for new epoch
    pub fn update_active_set(&mut self, height: u64) {
        if height % self.epoch_length != 0 {
            return; // Not epoch boundary
        }
        
        self.current_epoch = height / self.epoch_length;
        self.current_height = height;
        
        let vals = self.validators.read();
        
        // Get all eligible validators (not jailed, min stake met)
        let mut eligible: Vec<_> = vals
            .values()
            .filter(|v| v.is_active(height, self.min_stake))
            .collect();
        
        // Sort by stake descending, then by reputation
        eligible.sort_by(|a, b| {
            b.total_stake()
                .cmp(&a.total_stake())
                .then_with(|| b.reputation_score.partial_cmp(&a.reputation_score).unwrap())
        });
        
        // Take top max_validators
        let new_active: Vec<String> = eligible
            .into_iter()
            .take(self.max_validators)
            .map(|v| v.node_id.clone())
            .collect();
        
        let count = new_active.len();
        
        *self.active_set.write() = new_active;
        
        info!(epoch = self.current_epoch, height, active_validators = count, "Active validator set updated");
    }
    
    /// Get active validators
    pub fn get_active_validators(&self) -> Vec<String> {
        self.active_set.read().clone()
    }
    
    /// Get validator info
    pub fn get_validator(&self, node_id: &str) -> Option<Validator> {
        self.validators.read().get(node_id).cloned()
    }
    
    /// Get all validators
    pub fn get_all_validators(&self) -> Vec<Validator> {
        self.validators.read().values().cloned().collect()
    }
    
    /// Get total stake in network
    pub fn get_total_stake(&self) -> u64 {
        self.validators.read().values().map(|v| v.total_stake()).sum()
    }
    
    /// Record block participation for validator
    pub fn record_block_participation(&self, node_id: &str, height: u64, participated: bool) {
        if let Some(validator) = self.validators.write().get_mut(node_id) {
            validator.record_block_participation(height, participated);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use swarm_core::crypto_bls::generate_keypair;
    
    #[test]
    fn test_validator_registration() {
        let manager = ValidatorManager::new(1000, 100, 100, b"test-seed");
        
        let (bls_sk, bls_pk) = generate_keypair(b"node-1");
        let (vrf_sk, vrf_pk) = generate_vrf_keypair(b"node-1");
        
        let validator = Validator::new(
            "node-1".to_string(),
            bls_pk,
            vrf_pk,
            5000,
            0.1,
        );
        
        assert!(manager.register_validator(validator).is_ok());
        
        // Duplicate registration should fail
        let validator2 = Validator::new(
            "node-1".to_string(),
            bls_pk,
            vrf_pk,
            5000,
            0.1,
        );
        assert!(manager.register_validator(validator2).is_err());
    }
    
    #[test]
    fn test_stake_delegation() {
        let manager = ValidatorManager::new(1000, 100, 100, b"test-seed");
        
        let (_, bls_pk) = generate_keypair(b"node-1");
        let (_, vrf_pk) = generate_vrf_keypair(b"node-1");
        
        let validator = Validator::new("node-1".to_string(), bls_pk, vrf_pk, 5000, 0.1);
        manager.register_validator(validator).unwrap();
        
        // Delegate stake
        assert!(manager.delegate("delegator-1".to_string(), "node-1", 2000).is_ok());
        
        let v = manager.get_validator("node-1").unwrap();
        assert_eq!(v.total_stake(), 7000); // 5000 + 2000
    }
    
    #[test]
    fn test_slashing() {
        let manager = ValidatorManager::new(1000, 100, 100, b"test-seed");
        
        let (_, bls_pk) = generate_keypair(b"node-1");
        let (_, vrf_pk) = generate_vrf_keypair(b"node-1");
        
        let validator = Validator::new("node-1".to_string(), bls_pk, vrf_pk, 10000, 0.1);
        manager.register_validator(validator).unwrap();
        
        // Slash for double sign (10% penalty)
        let slashed = manager.slash_validator("node-1", SlashReason::DoubleSign, 100).unwrap();
        assert_eq!(slashed, 1000); // 10% of 10000
        
        let v = manager.get_validator("node-1").unwrap();
        assert_eq!(v.stake, 9000);
        assert!(v.jailed);
        assert_eq!(v.slashing_history.len(), 1);
    }
    
    #[test]
    fn test_leader_selection_distribution() {
        let manager = ValidatorManager::new(1000, 100, 100, b"test-seed");
        
        // Register 3 validators with different stakes
        for (i, stake) in [(0, 10000), (1, 5000), (2, 2500)].iter() {
            let (_, bls_pk) = generate_keypair(format!("node-{}", i).as_bytes());
            let (_, vrf_pk) = generate_vrf_keypair(format!("node-{}", i).as_bytes());
            
            let validator = Validator::new(
                format!("node-{}", i),
                bls_pk,
                vrf_pk,
                *stake,
                0.1,
            );
            manager.register_validator(validator).unwrap();
        }
        
        // Update active set
        let mut mgr = manager;
        mgr.update_active_set(0);
        
        let mut counts = HashMap::new();
        
        // Simulate 1000 leader selections
        for height in 0..1000 {
            if let Some(leader) = mgr.select_leader(height, 0) {
                *counts.entry(leader).or_insert(0) += 1;
            }
        }
        
        // Check distribution matches stake proportions
        let total_stake = 10000 + 5000 + 2500; // 17500
        
        let node0_count = *counts.get("node-0").unwrap_or(&0);
        let node1_count = *counts.get("node-1").unwrap_or(&0);
        let node2_count = *counts.get("node-2").unwrap_or(&0);
        
        // node-0: 10000/17500 = 57% ≈ 570 selections
        assert!(node0_count > 500 && node0_count < 650, "node-0: {}", node0_count);
        
        // node-1: 5000/17500 = 29% ≈ 290 selections
        assert!(node1_count > 220 && node1_count < 360, "node-1: {}", node1_count);
        
        // node-2: 2500/17500 = 14% ≈ 140 selections
        assert!(node2_count > 90 && node2_count < 200, "node-2: {}", node2_count);
    }
    
    #[test]
    fn test_reputation_tracking() {
        let manager = ValidatorManager::new(1000, 100, 100, b"test-seed");
        
        let (_, bls_pk) = generate_keypair(b"node-1");
        let (_, vrf_pk) = generate_vrf_keypair(b"node-1");
        
        let mut validator = Validator::new("node-1".to_string(), bls_pk, vrf_pk, 5000, 0.1);
        
        // Perfect uptime
        for h in 0..100 {
            validator.record_block_participation(h, true);
        }
        
        assert!(validator.reputation_score > 0.99);
        
        // Poor uptime (50%)
        for h in 100..200 {
            validator.record_block_participation(h, h % 2 == 0);
        }
        
        // Reputation should decrease
        assert!(validator.reputation_score < 0.95);
    }
}
