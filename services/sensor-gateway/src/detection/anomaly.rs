use std::collections::VecDeque;
use parking_lot::Mutex;
use std::time::{Instant};

#[derive(Default, Clone, Debug)]
pub struct AnomalyStats {
    pub window_1m: u64,
    pub window_5m: u64,
    pub window_15m: u64,
}

#[derive(Clone)]
pub struct AnomalyDetector {
    inner: std::sync::Arc<Mutex<Inner>>,
    cfg: AnomalyConfig,
}

struct Inner {
    events: VecDeque<(Instant, usize)>, // (time, size)
    last_prune: Instant,
}

#[derive(Clone, Copy)]
pub struct AnomalyConfig {
    pub threshold_ratio: f64, // ratio vs 5m baseline considered anomaly
    pub min_events: u64,
}

impl Default for AnomalyConfig {
    fn default() -> Self { Self { threshold_ratio: 2.5, min_events: 50 } }
}

impl AnomalyDetector {
    pub fn new(cfg: AnomalyConfig) -> Self {
        Self { inner: std::sync::Arc::new(Mutex::new(Inner { events: VecDeque::new(), last_prune: Instant::now() })), cfg }
    }

    pub fn record(&self, size: usize) -> Option<bool> { // returns Some(is_anomaly)
        let now = Instant::now();
        let mut guard = self.inner.lock();
        guard.events.push_back((now, size));
        if now.duration_since(guard.last_prune).as_secs() > 10 {
            prune(&mut guard.events, 60*15);
            guard.last_prune = now;
        }
        let stats = compute_stats(&guard.events);
        if stats.window_5m >= self.cfg.min_events {
            let short = stats.window_1m as f64;
            let mid = (stats.window_5m as f64 / 5.0).max(1.0);
            if mid > 0.0 && (short / mid) >= self.cfg.threshold_ratio { return Some(true); }
        }
        Some(false)
    }
}

fn prune(events: &mut VecDeque<(Instant, usize)>, horizon_secs: u64) {
    let cutoff = Instant::now() - std::time::Duration::from_secs(horizon_secs);
    while let Some((t,_)) = events.front() { if *t < cutoff { events.pop_front(); } else { break; } }
}

fn compute_stats(events: &VecDeque<(Instant, usize)>) -> AnomalyStats {
    let now = Instant::now();
    let mut s1=0; let mut s5=0; let mut s15=0;
    for (t,_) in events.iter().rev() {
        let age = now.duration_since(*t).as_secs();
        if age <= 60 { s1+=1; }
        if age <= 60*5 { s5+=1; }
        if age <= 60*15 { s15+=1; }
        if age > 60*15 { break; }
    }
    AnomalyStats { window_1m: s1, window_5m: s5, window_15m: s15 }
}
