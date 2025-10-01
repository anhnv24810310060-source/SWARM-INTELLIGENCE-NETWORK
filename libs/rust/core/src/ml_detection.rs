//! Multi-stage ML-based threat detection pipeline for SwarmGuard.
//!
//! Stages:
//! 1. Signature-based detection (constant time hash lookups)
//! 2. Statistical anomaly detection (simple distribution deviation)
//! 3. ML classification (placeholder heuristic until model inference integrated)
//!
//! Metrics integration:
//! - Increments signature/anomaly counters when early exits
//! - Records alert latency per stage and end-to-end latency

use anyhow::Result;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::time::Instant;

use crate::DETECTION_METRICS;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ThreatEvent {
    pub timestamp: i64,
    pub source_ip: String,
    pub dest_ip: String,
    pub protocol: String,
    pub payload_size: usize,
    pub features: Vec<f32>,
}

#[derive(Debug, Clone, PartialEq)]
pub enum ThreatLevel {
    Benign,
    Suspicious,
    Malicious,
    Critical,
}

#[derive(Debug, Clone)]
pub struct DetectionResult {
    pub level: ThreatLevel,
    pub confidence: f32,
    pub attack_type: Option<String>,
    pub latency_ms: f64,
}

pub struct MLDetectionPipeline {
    signature_cache: HashMap<String, bool>,
    anomaly_threshold: f32,
    ml_threshold: f32,
}

impl MLDetectionPipeline {
    pub fn new() -> Self {
        Self {
            signature_cache: HashMap::new(),
            anomaly_threshold: 0.7,
            ml_threshold: 0.8,
        }
    }

    /// Stage 1: Signature-based detection (< 10ms)
    pub async fn signature_match(&self, event: &ThreatEvent) -> Result<Option<DetectionResult>> {
        let start = Instant::now();
        let hash = format!("{}:{}:{}", event.source_ip, event.dest_ip, event.protocol);
        if self.signature_cache.contains_key(&hash) {
            let latency = start.elapsed().as_secs_f64() * 1000.0;
            DETECTION_METRICS.signature_total.add(1, &[]);
            DETECTION_METRICS.alert_latency_ms.record(latency, &[]);
            DETECTION_METRICS.e2e_latency_ms.record(latency, &[]);
            return Ok(Some(DetectionResult {
                level: ThreatLevel::Malicious,
                confidence: 1.0,
                attack_type: Some("known_threat".to_string()),
                latency_ms: latency,
            }));
        }
        Ok(None)
    }

    /// Stage 2: Anomaly detection (< 100ms)
    pub async fn anomaly_detect(&self, event: &ThreatEvent) -> Result<Option<DetectionResult>> {
        let start = Instant::now();
        if event.features.is_empty() { return Ok(None); }
        let anomaly_score = self.calculate_anomaly_score(&event.features);
        if anomaly_score > self.anomaly_threshold {
            let latency = start.elapsed().as_secs_f64() * 1000.0;
            DETECTION_METRICS.anomaly_total.add(1, &[]);
            DETECTION_METRICS.alert_latency_ms.record(latency, &[]);
            return Ok(Some(DetectionResult {
                level: ThreatLevel::Suspicious,
                confidence: anomaly_score,
                attack_type: Some("anomaly".to_string()),
                latency_ms: latency,
            }));
        }
        Ok(None)
    }

    /// Stage 3: ML classification (< 1s) placeholder heuristic
    pub async fn ml_classify(&self, event: &ThreatEvent) -> Result<DetectionResult> {
        let start = Instant::now();
        if event.features.is_empty() {
            return Ok(DetectionResult { level: ThreatLevel::Benign, confidence: 0.0, attack_type: None, latency_ms: 0.0 });
        }
        let (confidence, attack_type) = self.neural_inference(&event.features);
        let level = if confidence > self.ml_threshold {
            ThreatLevel::Malicious
        } else if confidence > 0.5 {
            ThreatLevel::Suspicious
        } else {
            ThreatLevel::Benign
        };
        Ok(DetectionResult {
            level,
            confidence,
            attack_type: Some(attack_type),
            latency_ms: start.elapsed().as_secs_f64() * 1000.0,
        })
    }

    /// Full pipeline detection (records E2E latency)
    pub async fn detect(&self, event: &ThreatEvent) -> Result<DetectionResult> {
        let e2e_start = Instant::now();
        if let Some(res) = self.signature_match(event).await? { return Ok(res); }
        if let Some(res) = self.anomaly_detect(event).await? { return Ok(res); }
        let res = self.ml_classify(event).await?;
        DETECTION_METRICS.e2e_latency_ms.record(e2e_start.elapsed().as_secs_f64() * 1000.0, &[]);
        Ok(res)
    }

    fn calculate_anomaly_score(&self, features: &[f32]) -> f32 {
        let mean: f32 = features.iter().sum::<f32>() / features.len() as f32;
        let variance: f32 = features.iter().map(|x| (x - mean).powi(2)).sum::<f32>() / features.len() as f32;
        let std_dev = variance.sqrt().max(1e-6);
        features.iter().map(|x| ((x - mean) / std_dev).abs()).sum::<f32>() / features.len() as f32 / 3.0
    }

    fn neural_inference(&self, features: &[f32]) -> (f32, String) {
        let score: f32 = features.iter().sum::<f32>() / features.len() as f32;
        let attack_type = if score > 0.8 { "ddos" } else if score > 0.6 { "port_scan" } else { "unknown" };
        (score, attack_type.to_string())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn pipeline_runs() {
        let pipeline = MLDetectionPipeline::new();
        let evt = ThreatEvent { timestamp: 0, source_ip: "1.1.1.1".into(), dest_ip: "2.2.2.2".into(), protocol: "TCP".into(), payload_size: 128, features: vec![0.5,0.6,0.7,0.8] };
        let res = pipeline.detect(&evt).await.unwrap();
        assert!(matches!(res.level, ThreatLevel::Benign | ThreatLevel::Suspicious | ThreatLevel::Malicious));
    }
}
