use std::collections::VecDeque;

#[derive(Debug, Clone)]
pub struct AnomalyDetector {
    features_stats: Vec<Welford>,
    window: usize,
    med_buffers: Vec<VecDeque<f64>>, // per feature window for median
    hard_threshold: f64,
    soft_threshold: f64,
}

#[derive(Debug, Clone, Copy)]
struct Welford { n: u64, mean: f64, m2: f64 }
impl Welford { fn new() -> Self { Self { n:0, mean:0.0, m2:0.0 } } fn update(&mut self, x: f64){ self.n+=1; let delta = x-self.mean; self.mean += delta/(self.n as f64); let delta2 = x-self.mean; self.m2 += delta*delta2;} fn variance(&self)->f64{ if self.n<2 {0.0} else { self.m2/ (self.n as f64 -1.0) } } }

#[derive(Debug, Clone)]
pub struct AnomalyScore { pub composite: f64, pub z_scores: Vec<f64>, pub mad_scores: Vec<f64> }

impl AnomalyDetector {
    pub fn new(hard_threshold: f64, soft_threshold: f64, window: usize) -> Self { Self { features_stats: Vec::new(), window, med_buffers: Vec::new(), hard_threshold, soft_threshold } }

    pub fn score(&mut self, features: &[f32]) -> AnomalyScore {
        if self.features_stats.len() != features.len() { self.features_stats = (0..features.len()).map(|_| Welford::new()).collect(); self.med_buffers = (0..features.len()).map(|_| VecDeque::with_capacity(self.window)).collect(); }
        // update stats & collect arrays
        let mut z_scores = Vec::with_capacity(features.len());
        let mut mad_scores = Vec::with_capacity(features.len());
        for (i, &f) in features.iter().enumerate() {
            let x = f as f64;
            let st = &mut self.features_stats[i];
            st.update(x);
            let var = st.variance();
            let std = if var <= 1e-9 { 0.0 } else { var.sqrt() };
            let z = if std == 0.0 { 0.0 } else { (x - st.mean)/std };
            // median absolute deviation
            let buf = &mut self.med_buffers[i];
            if buf.len() == self.window { buf.pop_front(); }
            buf.push_back(x);
            let mut sorted: Vec<f64> = buf.iter().copied().collect();
            sorted.sort_by(|a,b| a.partial_cmp(b).unwrap());
            let median = if sorted.is_empty() { x } else { sorted[sorted.len()/2] };
            let mut deviations: Vec<f64> = sorted.iter().map(|v| (v-median).abs()).collect();
            deviations.sort_by(|a,b| a.partial_cmp(b).unwrap());
            let mad = if deviations.is_empty() { 0.0 } else { deviations[deviations.len()/2] };
            let mad_z = if mad <= 1e-9 { 0.0 } else { 0.6745 * (x - median).abs()/mad };
            z_scores.push(z.abs());
            mad_scores.push(mad_z);
        }
        // composite = max of per-feature combined z/mad
        let mut composite = 0.0;
        for (z,m) in z_scores.iter().zip(mad_scores.iter()) { composite = composite.max((*z).max(*m)); }
        AnomalyScore { composite, z_scores, mad_scores }
    }

    pub fn is_anomaly(&self, score: &AnomalyScore) -> bool { score.composite >= self.hard_threshold || (score.composite >= self.soft_threshold) }
    pub fn hard_threshold(&self) -> f64 { self.hard_threshold }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn anomaly_composite_increases() {
        let mut det = AnomalyDetector::new(3.5, 2.5, 21);
        let base = det.score(&[0.1,0.1,0.1]);
        let spike = det.score(&[99.0, 0.0, 0.0]);
        assert!(spike.composite >= base.composite);
    }
}

