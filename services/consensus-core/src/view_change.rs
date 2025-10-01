use std::time::{Duration, Instant};
use std::sync::{Arc, RwLock};
use crate::{PbftState, PbftService};
use tracing::{info, warn};

/// Configuration for view change timeouts.
pub struct ViewChangeConfig {
    pub round_timeout_ms: u64,
}

impl Default for ViewChangeConfig {
    fn default() -> Self { Self { round_timeout_ms: 3000 } }
}

impl PbftService {
    pub fn spawn_view_change_task(&self) {
        let enabled = std::env::var("CONSENSUS_VIEW_CHANGE_ENABLED").ok().map(|v| v=="1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(true);
        if !enabled { tracing::info!("view change task disabled via CONSENSUS_VIEW_CHANGE_ENABLED"); return; }
        let timeout: u64 = std::env::var("CONSENSUS_ROUND_TIMEOUT_MS").ok().and_then(|v| v.parse().ok()).unwrap_or(ViewChangeConfig::default().round_timeout_ms);
        let state = self.state.clone();
        // Metrics setup
        let meter = opentelemetry::global::meter("consensus-core");
        let vc_counter = meter.u64_counter("consensus_view_changes_total").with_description("Total view changes triggered by timeout").init();
        let vc_hist = meter.f64_histogram("consensus_view_change_interval_ms").with_description("Interval between view changes (ms)").init();
        tokio::spawn(async move {
            let mut last_change = Instant::now();
            let dur = Duration::from_millis(timeout);
            loop {
                tokio::time::sleep(dur).await;
                let (h_before, r_before, leader_before, validators) = {
                    let st = state.read().unwrap();
                    (st.height, st.round, st.leader.clone(), st.validators.clone())
                };
                let mut changed = false;
                {
                    let mut st = state.write().unwrap();
                    if !validators.is_empty() {
                        let idx = (st.round + 1) as usize % validators.len();
                        st.round += 1;
                        st.leader = validators[idx].clone();
                        changed = true;
                    }
                }
                if changed {
                    let st = state.read().unwrap();
                    let elapsed_ms = last_change.elapsed().as_secs_f64() * 1000.0;
                    vc_counter.add(1, &[]);
                    vc_hist.record(elapsed_ms, &[]);
                    last_change = Instant::now();
                    info!(height=st.height, round=st.round, leader=%st.leader, interval_ms=elapsed_ms, "view_change_timeout_triggered");
                } else {
                    warn!(height=h_before, round=r_before, leader=%leader_before, "view_change_timeout_noop");
                }
            }
        });
    }
}
