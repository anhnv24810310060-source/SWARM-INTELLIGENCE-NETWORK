use super::{RuleSet, AnomalyDetector};
use serde::Serialize;
use parking_lot::RwLock;
use std::sync::Arc;
use sha2::{Sha256, Digest};

#[derive(Debug, Serialize, Clone)]
pub struct DetectionEvent {
    pub rule_id: Option<String>,
    pub kind: String, // signature|anomaly
    pub severity: String,
    pub payload_preview: String,
    pub payload_hash: String, // SHA-256 hex digest for exact matching
}

#[derive(Clone)]
pub struct DetectionEngine {
    pub rules: RuleSet,
    pub anomaly: AnomalyDetector,
    pub enabled: bool,
    anomaly_enabled: bool,
    signature_enabled: bool,
    pub last_events: Arc<RwLock<Vec<DetectionEvent>>>,
}

impl DetectionEngine {
    pub fn new(rules: RuleSet, anomaly: AnomalyDetector, enabled: bool, anomaly_enabled: bool, signature_enabled: bool) -> Self {
        Self { rules, anomaly, enabled, anomaly_enabled, signature_enabled, last_events: Arc::new(RwLock::new(Vec::new())) }
    }

    pub fn scan(&self, line: &str) -> Vec<DetectionEvent> {
        if !self.enabled { return vec![]; }
        let mut out = Vec::new();
        // Compute canonical hash once for all detections on this payload
        let mut hasher = Sha256::new();
        hasher.update(line.as_bytes());
        let hash = format!("{:x}", hasher.finalize());
        
        if self.signature_enabled {
            for cr in self.rules.rules.read().iter() {
                if cr.regex.is_match(line) {
                    out.push(DetectionEvent { 
                        rule_id: Some(cr.raw.id.clone()), 
                        kind: "signature".into(), 
                        severity: cr.raw.severity.clone().unwrap_or_else(|| "info".into()), 
                        payload_preview: line.chars().take(120).collect(),
                        payload_hash: hash.clone(),
                    });
                }
            }
        }
        if self.anomaly_enabled {
            if let Some(true) = self.anomaly.record(line.len()) {
                out.push(DetectionEvent { 
                    rule_id: None, 
                    kind: "anomaly".into(), 
                    severity: "medium".into(), 
                    payload_preview: line.chars().take(120).collect(),
                    payload_hash: hash.clone(),
                });
            }
        }
        if !out.is_empty() { *self.last_events.write() = out.clone(); }
        out
    }
}
