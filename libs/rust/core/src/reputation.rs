//! Reputation scoring service per design (weighted voting, decay, misbehavior penalty).
//!
//! This skeleton provides in-memory scoring with exponential decay placeholder.
//! Future additions: persistence, cryptographic attestation linkage, consensus integration.

use std::{collections::HashMap, time::{Instant, Duration}};
use parking_lot::RwLock;
use std::sync::Arc;

#[derive(Debug, Clone)]
pub struct ReputationEntry {
    pub score: f64,
    pub last_update: Instant,
}

pub struct ReputationConfig {
    pub half_life_secs: u64,
    pub min_score: f64,
    pub max_score: f64,
    pub penalty: f64,
    pub reward: f64,
}

impl Default for ReputationConfig { fn default() -> Self { Self { half_life_secs: 3600, min_score: 0.0, max_score: 1000.0, penalty: 50.0, reward: 10.0 } } }

pub struct ReputationService {
    cfg: ReputationConfig,
    entries: Arc<RwLock<HashMap<String, ReputationEntry>>>,
}

impl ReputationService {
    pub fn new(cfg: ReputationConfig) -> Self { Self { cfg, entries: Arc::new(RwLock::new(HashMap::new())) } }

    pub fn get(&self, node: &str) -> f64 { self.entries.read().get(node).map(|e| self.decayed_score(e)).unwrap_or(self.cfg.max_score / 2.0) }

    fn decayed_score(&self, e: &ReputationEntry) -> f64 {
        let elapsed = e.last_update.elapsed().as_secs_f64();
        let hl = self.cfg.half_life_secs as f64;
        let decay_factor = 0.5_f64.powf(elapsed / hl);
        (e.score * decay_factor).clamp(self.cfg.min_score, self.cfg.max_score)
    }

    pub fn reward(&self, node: &str) { self.adjust(node, self.cfg.reward); }
    pub fn penalize(&self, node: &str) { self.adjust(node, -self.cfg.penalty); }

    fn adjust(&self, node: &str, delta: f64) {
        let mut map = self.entries.write();
        let entry = map.entry(node.to_string()).or_insert(ReputationEntry { score: self.cfg.max_score / 2.0, last_update: Instant::now() });
        let current = self.decayed_score(entry);
        entry.score = (current + delta).clamp(self.cfg.min_score, self.cfg.max_score);
        entry.last_update = Instant::now();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn reward_and_penalty() {
        let svc = ReputationService::new(ReputationConfig::default());
        let base = svc.get("n1");
        svc.reward("n1");
        let after = svc.get("n1");
        assert!(after > base);
        svc.penalize("n1");
        let after_penalty = svc.get("n1");
        assert!(after_penalty < after);
    }
}
