#[derive(Debug, Clone)]
pub struct AnomalyDetector {
    alpha: f64,
    mean: f64,
    var: f64,
    n: u64,
    threshold_z: f64,
}

impl AnomalyDetector {
    pub fn new(alpha: f64, threshold_z: f64) -> Self { Self { alpha, mean: 0.0, var: 0.0, n: 0, threshold_z } }

    pub fn score(&mut self, features: &[f32]) -> f64 {
        // Simple aggregate statistic: average of features
        if features.is_empty() { return 0.0; }
        let x = features.iter().copied().map(|v| v as f64).sum::<f64>() / features.len() as f64;
        self.update(x);
        if self.var <= 1e-9 { return 0.0; }
        let std = self.var.sqrt();
        ((x - self.mean)/std).abs()
    }

    fn update(&mut self, x: f64) {
        self.n += 1;
        if self.n == 1 { self.mean = x; self.var = 0.0; return; }
        // EWMA for mean
        self.mean = self.alpha * x + (1.0 - self.alpha) * self.mean;
        // Approx variance update (EWMA of squared diff)
        let diff = x - self.mean;
        self.var = self.alpha * diff * diff + (1.0 - self.alpha) * self.var;
    }

    pub fn threshold(&self) -> f64 { self.threshold_z }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn anomaly_score_changes() {
        let mut det = AnomalyDetector::new(0.3, 2.5);
        let base = det.score(&[0.1,0.1,0.1]);
        let later = det.score(&[5.0,5.0,5.0]);
        assert!(later >= base);
    }
}
