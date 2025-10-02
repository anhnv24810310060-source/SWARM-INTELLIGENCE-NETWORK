use std::collections::VecDeque;
use once_cell::sync::Lazy;
use std::sync::atomic::{AtomicU64, Ordering};
use opentelemetry::metrics::Meter;

#[derive(Debug, Clone)]
pub struct AnomalyDetector {
    features_stats: Vec<Welford>,
    window: usize,
    med_buffers: Vec<VecDeque<f64>>, // per feature window for median
    hard_threshold: f64,
    soft_threshold: f64,
    // Adaptive thresholding
    adaptive: bool,
    target_quantile: f64,
    quantile_est: P2Quantile,
    adjust_history: Vec<(u64,f64)>,
    seen: u64,
    min_hard: f64,
    max_hard: f64,
}

#[derive(Debug, Clone, Copy)]
struct Welford { n: u64, mean: f64, m2: f64 }
impl Welford { fn new() -> Self { Self { n:0, mean:0.0, m2:0.0 } } fn update(&mut self, x: f64){ self.n+=1; let delta = x-self.mean; self.mean += delta/(self.n as f64); let delta2 = x-self.mean; self.m2 += delta*delta2;} fn variance(&self)->f64{ if self.n<2 {0.0} else { self.m2/ (self.n as f64 -1.0) } } }

#[derive(Debug, Clone)]
pub struct AnomalyScore { pub composite: f64, pub z_scores: Vec<f64>, pub mad_scores: Vec<f64> }

impl AnomalyDetector {
    pub fn new(hard_threshold: f64, soft_threshold: f64, window: usize) -> Self { 
        Lazy::force(&THRESHOLD_GAUGE_INIT);
        let detector = Self { features_stats: Vec::new(), window, med_buffers: Vec::new(), hard_threshold, soft_threshold, adaptive: true, target_quantile: 0.995, quantile_est: P2Quantile::new(0.995), adjust_history: Vec::with_capacity(64), seen:0, min_hard: soft_threshold.max(1.5), max_hard: hard_threshold*4.0 };
        HARD_THRESHOLD_ATOMIC.store((hard_threshold*1_000_000.0) as u64, Ordering::Relaxed);
        detector
    }

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
        let score = AnomalyScore { composite, z_scores, mad_scores };
        self.seen += 1;
        if self.adaptive { self.quantile_est.observe(score.composite); if self.seen % 100 == 0 { self.adjust(); } }
        score
    }

    pub fn is_anomaly(&self, score: &AnomalyScore) -> bool { score.composite >= self.hard_threshold || (score.composite >= self.soft_threshold) }
    pub fn hard_threshold(&self) -> f64 { self.hard_threshold }
    pub fn current_quantile(&self) -> Option<f64> { self.quantile_est.current() }
    pub fn adjust_history(&self) -> &[(u64,f64)] { &self.adjust_history }

    fn adjust(&mut self) {
        if let Some(qv) = self.quantile_est.current() {
            // Set hard threshold to max( qv, current hard ) but clamp
            let new_hard = qv.max(self.hard_threshold).clamp(self.min_hard, self.max_hard);
            if (new_hard - self.hard_threshold).abs() > 1e-6 { 
                self.hard_threshold = new_hard;
                self.adjust_history.push((self.seen, new_hard));
                if self.adjust_history.len()>256 { self.adjust_history.remove(0); }
                HARD_THRESHOLD_ATOMIC.store((self.hard_threshold*1_000_000.0) as u64, Ordering::Relaxed);
            }
        }
    }
}

// Metrics for anomaly threshold gauge
static ANOMALY_METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("swarm_anomaly"));
static HARD_THRESHOLD_ATOMIC: Lazy<AtomicU64> = Lazy::new(|| AtomicU64::new(0));
static THRESHOLD_GAUGE_INIT: Lazy<()> = Lazy::new(|| {
    let gauge = ANOMALY_METER.f64_observable_gauge("swarm_anomaly_hard_threshold").with_description("Current adaptive hard threshold").init();
    let atom = HARD_THRESHOLD_ATOMIC.clone();
    ANOMALY_METER.register_callback(&[gauge.as_any()], move |obs| {
        let v = atom.load(Ordering::Relaxed) as f64 / 1_000_000.0;
        obs.observe_f64(&gauge, v, &[]);
    }).ok();
});

// P² quantile estimator
#[derive(Debug, Clone)]
struct P2Quantile { p: f64, initialized: bool, n: [f64;5], q: [f64;5], np: [f64;5], dn: [f64;5], count: usize }
impl P2Quantile {
    fn new(p: f64) -> Self { Self { p, initialized: false, n: [0.0;5], q: [0.0;5], np: [0.0;5], dn: [0.0;5], count:0 } }
    fn observe(&mut self, x: f64) {
        if !self.initialized {
            self.q[self.count] = x; self.count +=1; if self.count==5 { self.q.sort_by(|a,b| a.partial_cmp(b).unwrap()); self.n=[1.0,2.0,3.0,4.0,5.0]; self.np=[1.0, 1.0+2.0*self.p, 1.0+4.0*self.p, 3.0+2.0*self.p, 5.0]; self.dn=[0.0, self.p/2.0, self.p, (1.0+self.p)/2.0, 1.0]; self.initialized=true; }
            return;
        }
        // Update markers
        let k = if x < self.q[0] { self.q[0]=x; 0 } else if x < self.q[1] {1} else if x < self.q[2] {2} else if x < self.q[3] {3} else if x <= self.q[4] {4} else { self.q[4]=x; 4 };
        for i in k+1..5 { self.n[i]+=1.0; }
        for i in 0..5 { self.np[i]+= self.dn[i]; }
        for i in 1..4 { let d = self.np[i]-self.n[i]; if (d>=1.0 && self.n[i+1]-self.n[i]>1.0) || (d<=-1.0 && self.n[i]-self.n[i-1]>1.0) { let dsign = d.signum(); let qn = self.parabolic(i, dsign); if self.q[i-1] < qn && qn < self.q[i+1] { self.q[i]=qn; } else { self.q[i] = self.linear(i, dsign); } self.n[i]+= dsign; } }
    }
    fn parabolic(&self, i: usize, d: f64) -> f64 { let n0=self.n[i-1]; let n1=self.n[i]; let n2=self.n[i+1]; let q0=self.q[i-1]; let q1=self.q[i]; let q2=self.q[i+1]; q1 + d/(n2-n0) * ((n1-n0+d)*(q2-q1)/(n2-n1) + (n2-n1-d)*(q1-q0)/(n1-n0)) }
    fn linear(&self, i: usize, d: f64) -> f64 { self.q[i] + d*(self.q[i+d as usize]-self.q[i - (1.0-d) as usize])/(self.n[i+d as usize]-self.n[i - (1.0-d) as usize]) }
    fn current(&self) -> Option<f64> { if self.initialized { Some(self.q[2]) } else { None } }
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

