use serde::{Serialize, Deserialize};
use std::time::{Instant, Duration};
use parking_lot::RwLock;
use opentelemetry::metrics::{Counter, Histogram, Meter};
use once_cell::sync::Lazy;

pub type RoundId = u64;
pub type ModelVersion = u64;

#[derive(Clone, Debug, Serialize, Deserialize)]
pub enum AggregationMethod { FedAvg }

#[derive(Clone, Debug)]
pub struct ClientUpdate {
    pub node_id: String,
    pub round: RoundId,
    pub weights: Vec<f32>,
    pub sample_count: u64,
}

#[derive(Clone, Debug)]
pub struct AggregatedModel {
    pub version: ModelVersion,
    pub weights: Vec<f32>,
    pub aggregated_at: Instant,
}

#[derive(Debug)]
struct RoundState { started_at: Instant, updates: Vec<ClientUpdate> }

struct FedMetrics {
    updates_total: Counter<u64>,
    rounds_completed: Counter<u64>,
    aggregation_latency_ms: Histogram<f64>,
}

static FED_METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("swarm_federation"));

impl FedMetrics {
    fn new() -> Self {
        Self {
            updates_total: FED_METER.u64_counter("fed_updates_total").with_description("Total federated learning updates received").init(),
            rounds_completed: FED_METER.u64_counter("fed_rounds_completed_total").with_description("Federated rounds completed").init(),
            aggregation_latency_ms: FED_METER.f64_histogram("fed_aggregation_latency_ms").with_description("Aggregation latency ms").init(),
        }
    }
}

pub struct FederatedLearningCoordinator {
    method: AggregationMethod,
    min_participants: usize,
    round_timeout: Duration,
    inner: RwLock<Inner>,
    metrics: FedMetrics,
}

#[derive(Debug)]
struct Inner {
    current_round: RoundId,
    model_version: ModelVersion,
    model_weights: Vec<f32>,
    round_state: RoundState,
}

impl FederatedLearningCoordinator {
    pub fn new(min_participants: usize, method: AggregationMethod) -> Self {
        Self { method, min_participants, round_timeout: Duration::from_secs(300), inner: RwLock::new(Inner { current_round: 1, model_version: 1, model_weights: vec![], round_state: RoundState { started_at: Instant::now(), updates: Vec::new() } }), metrics: FedMetrics::new() }
    }

    pub fn submit_update(&self, update: ClientUpdate) -> Option<AggregatedModel> {
        let mut inner = self.inner.write();
        if update.round != inner.current_round { return None; }
        self.metrics.updates_total.add(1, &[]);
        inner.round_state.updates.push(update);
        if inner.round_state.updates.len() >= self.min_participants { let latency_start = Instant::now(); let aggregated = self.aggregate_locked(&inner.round_state.updates); let latency = latency_start.elapsed().as_secs_f64()*1000.0; self.metrics.aggregation_latency_ms.record(latency, &[]); inner.model_version += 1; inner.model_weights = aggregated.clone(); self.metrics.rounds_completed.add(1, &[]); let result = AggregatedModel { version: inner.model_version, weights: aggregated, aggregated_at: Instant::now() }; inner.current_round += 1; inner.round_state = RoundState { started_at: Instant::now(), updates: Vec::new() }; return Some(result); }
        None
    }

    fn aggregate_locked(&self, updates: &[ClientUpdate]) -> Vec<f32> {
        if updates.is_empty() { return Vec::new(); }
        match self.method { AggregationMethod::FedAvg => { let dim = updates[0].weights.len(); let mut acc = vec![0f32; dim]; let mut total_samples = 0f64; for u in updates { if u.weights.len()!=dim { continue; } let w = u.sample_count as f64; total_samples += w; for (i,v) in u.weights.iter().enumerate() { acc[i] += (*v as f64 * w) as f32; } } if total_samples>0.0 { for v in &mut acc { *v /= total_samples as f32; } } acc } }
    }

    pub fn current_round(&self) -> RoundId { self.inner.read().current_round }
    pub fn model_version(&self) -> ModelVersion { self.inner.read().model_version }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn round_advances() {
        let fed = FederatedLearningCoordinator::new(2, AggregationMethod::FedAvg);
        let r = fed.current_round();
        let upd1 = ClientUpdate { node_id: "n1".into(), round: r, weights: vec![1.0,2.0], sample_count: 10 };
        assert!(fed.submit_update(upd1).is_none());
        let upd2 = ClientUpdate { node_id: "n2".into(), round: r, weights: vec![3.0,4.0], sample_count: 10 };
        let agg = fed.submit_update(upd2);
        assert!(agg.is_some());
        assert_eq!(fed.model_version(), 2);
    }
}
