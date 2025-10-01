//! Federated learning coordination primitives.
//!
//! Supports basic aggregation strategies (FedAvg, FedProx, FedNova placeholder).
//! Future work: secure aggregation, differential privacy, version negotiation.

use anyhow::Result;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ModelGradient {
    pub node_id: String,
    pub layer_gradients: Vec<Vec<f32>>, // layers -> weights
    pub sample_count: usize,
    pub timestamp: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GlobalModel {
    pub version: u64,
    pub weights: Vec<Vec<f32>>, // layers -> weights
    pub updated_at: i64,
}

#[derive(Debug, Clone)]
pub enum AggregationMethod {
    FedAvg,
    FedProx,
    FedNova,
}

pub struct FederatedLearningCoordinator {
    aggregation_method: AggregationMethod,
    min_participants: usize,
    gradient_buffer: Vec<ModelGradient>,
    round: u64,
    mu: f32, // FedProx proximal term
}

impl FederatedLearningCoordinator {
    pub fn new(method: AggregationMethod, min_participants: usize) -> Self {
        Self { aggregation_method: method, min_participants, gradient_buffer: Vec::new(), round: 0, mu: 0.01 }
    }

    pub fn submit_gradient(&mut self, g: ModelGradient) -> Result<()> {
        // Basic validation
        if !self.gradient_buffer.is_empty() && g.layer_gradients.len() != self.gradient_buffer[0].layer_gradients.len() {
            anyhow::bail!("layer mismatch");
        }
        self.gradient_buffer.push(g);
        Ok(())
    }

    pub fn aggregate(&mut self) -> Result<Option<GlobalModel>> {
        if self.gradient_buffer.len() < self.min_participants { return Ok(None); }
        self.round += 1;
        let model = match self.aggregation_method {
            AggregationMethod::FedAvg => self.fed_avg()?,
            AggregationMethod::FedProx => self.fed_prox()?,
            AggregationMethod::FedNova => self.fed_nova()?,
        };
        self.gradient_buffer.clear();
        Ok(Some(model))
    }

    fn fed_avg(&self) -> Result<GlobalModel> {
        let total_samples: usize = self.gradient_buffer.iter().map(|g| g.sample_count).sum();
        let num_layers = self.gradient_buffer[0].layer_gradients.len();
        let mut agg: Vec<Vec<f32>> = Vec::with_capacity(num_layers);
        for layer in 0..num_layers {
            let size = self.gradient_buffer[0].layer_gradients[layer].len();
            let mut layer_vec = vec![0.0f32; size];
            for g in &self.gradient_buffer {
                let w = g.sample_count as f32 / total_samples as f32;
                for (i, val) in g.layer_gradients[layer].iter().enumerate() { layer_vec[i] += *val * w; }
            }
            agg.push(layer_vec);
        }
        Ok(GlobalModel { version: self.round, weights: agg, updated_at: chrono::Utc::now().timestamp() })
    }

    fn fed_prox(&self) -> Result<GlobalModel> { self.fed_avg() }
    fn fed_nova(&self) -> Result<GlobalModel> { self.fed_avg() }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fedavg_aggregates() {
        let mut coord = FederatedLearningCoordinator::new(AggregationMethod::FedAvg, 2);
        coord.submit_gradient(ModelGradient { node_id: "n1".into(), layer_gradients: vec![vec![0.1,0.2,0.3]], sample_count: 10, timestamp: 0 }).unwrap();
        coord.submit_gradient(ModelGradient { node_id: "n2".into(), layer_gradients: vec![vec![0.2,0.4,0.6]], sample_count: 30, timestamp: 0 }).unwrap();
        let model = coord.aggregate().unwrap().expect("should aggregate");
        // Weighted: (0.1*10 + 0.2*30)/40 = 0.175
        assert!((model.weights[0][0] - 0.175).abs() < 1e-6);
    }
}
