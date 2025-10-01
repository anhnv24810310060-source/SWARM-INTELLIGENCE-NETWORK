//! Adaptive auto-scaling heuristics for SwarmGuard services.
//!
//! Evaluates rolling resource metrics & threat volume to decide scale actions.
//! Conservative scale-in to avoid thrash; proportional scale-out.

use anyhow::Result;
use serde::{Deserialize, Serialize};
use std::time::{Duration, Instant};
use tokio::sync::RwLock;
use std::sync::Arc;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ResourceMetrics {
    pub cpu_utilization: f32,      // 0.0 - 1.0
    pub memory_utilization: f32,   // 0.0 - 1.0
    pub network_throughput: f64,   // bytes/sec
    pub threat_volume: u64,        // events/sec
    pub timestamp: i64,
}

#[derive(Debug, Clone)]
pub struct ScalingThresholds {
    pub cpu_scale_out: f32,
    pub cpu_scale_in: f32,
    pub memory_scale_out: f32,
    pub memory_scale_in: f32,
    pub scale_out_duration: Duration,
    pub scale_in_duration: Duration,
}

impl Default for ScalingThresholds {
    fn default() -> Self {
        Self {
            cpu_scale_out: 0.80,
            cpu_scale_in: 0.30,
            memory_scale_out: 0.90,
            memory_scale_in: 0.50,
            scale_out_duration: Duration::from_secs(5 * 60),
            scale_in_duration: Duration::from_secs(15 * 60),
        }
    }
}

#[derive(Debug, Clone, PartialEq)]
pub enum ScalingDecision { ScaleOut(u32), ScaleIn(u32), NoAction }

pub struct AutoScaler {
    thresholds: ScalingThresholds,
    metrics_history: Arc<RwLock<Vec<(Instant, ResourceMetrics)>>>,
    last_scale_action: Arc<RwLock<Option<Instant>>>,
    cooldown_period: Duration,
}

impl AutoScaler {
    pub fn new(thresholds: ScalingThresholds) -> Self {
        Self { thresholds, metrics_history: Arc::new(RwLock::new(Vec::new())), last_scale_action: Arc::new(RwLock::new(None)), cooldown_period: Duration::from_secs(5 * 60) }
    }

    pub async fn record_metrics(&self, m: ResourceMetrics) -> Result<()> {
        let mut h = self.metrics_history.write().await;
        h.push((Instant::now(), m));
        let cutoff = Instant::now() - Duration::from_secs(30 * 60);
        h.retain(|(t, _)| *t > cutoff);
        Ok(())
    }

    pub async fn evaluate(&self) -> Result<ScalingDecision> {
        if let Some(last) = *self.last_scale_action.read().await { if last.elapsed() < self.cooldown_period { return Ok(ScalingDecision::NoAction); } }
        let h = self.metrics_history.read().await;
        if h.is_empty() { return Ok(ScalingDecision::NoAction); }
        if self.should_scale_out(&h) { let n = self.calculate_scale_out_amount(&h); *self.last_scale_action.write().await = Some(Instant::now()); return Ok(ScalingDecision::ScaleOut(n)); }
        if self.should_scale_in(&h) { let n = self.calculate_scale_in_amount(&h); *self.last_scale_action.write().await = Some(Instant::now()); return Ok(ScalingDecision::ScaleIn(n)); }
        Ok(ScalingDecision::NoAction)
    }

    fn should_scale_out(&self, h: &[(Instant, ResourceMetrics)]) -> bool {
        let recent = self.get_recent(h, self.thresholds.scale_out_duration);
        if recent.is_empty() { return false; }
        let avg_cpu = recent.iter().map(|m| m.cpu_utilization).sum::<f32>() / recent.len() as f32;
        let avg_mem = recent.iter().map(|m| m.memory_utilization).sum::<f32>() / recent.len() as f32;
        avg_cpu > self.thresholds.cpu_scale_out || avg_mem > self.thresholds.memory_scale_out
    }

    fn should_scale_in(&self, h: &[(Instant, ResourceMetrics)]) -> bool {
        let recent = self.get_recent(h, self.thresholds.scale_in_duration);
        if recent.is_empty() { return false; }
        let avg_cpu = recent.iter().map(|m| m.cpu_utilization).sum::<f32>() / recent.len() as f32;
        let avg_mem = recent.iter().map(|m| m.memory_utilization).sum::<f32>() / recent.len() as f32;
        avg_cpu < self.thresholds.cpu_scale_in && avg_mem < self.thresholds.memory_scale_in
    }

    fn calculate_scale_out_amount(&self, h: &[(Instant, ResourceMetrics)]) -> u32 {
        let recent = self.get_recent(h, Duration::from_secs(60));
        if recent.is_empty() { return 1; }
        let avg_cpu = recent.iter().map(|m| m.cpu_utilization).sum::<f32>() / recent.len() as f32;
        if avg_cpu > 0.95 { 3 } else if avg_cpu > 0.85 { 2 } else { 1 }
    }

    fn calculate_scale_in_amount(&self, _h: &[(Instant, ResourceMetrics)]) -> u32 { 1 }

    fn get_recent(&self, h: &[(Instant, ResourceMetrics)], dur: Duration) -> Vec<ResourceMetrics> {
        let cutoff = Instant::now() - dur;
        h.iter().filter(|(t, _)| *t > cutoff).map(|(_, m)| m.clone()).collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn scale_out_trigger() {
        let scaler = AutoScaler::new(ScalingThresholds::default());
        for _ in 0..5 { scaler.record_metrics(ResourceMetrics { cpu_utilization: 0.9, memory_utilization: 0.4, network_throughput: 0.0, threat_volume: 0, timestamp: 0 }).await.unwrap(); }
        match scaler.evaluate().await.unwrap() { ScalingDecision::ScaleOut(n) => assert!(n >= 1), _ => panic!("expected scale out") }
    }
}
