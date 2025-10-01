//! Node bootstrap finite state machine (FSM) per design Section 5.1.1.
//!
//! Phases:
//! - HardwareInit
//! - NetworkDiscovery
//! - KnowledgeSync
//! - Operational
//!
//! Tracks timestamps for each transition and offers readiness evaluation.

use std::time::{Instant, Duration};
use serde::{Serialize, Deserialize};

#[derive(Debug, Clone, Copy, Serialize, Deserialize, PartialEq, Eq)]
pub enum BootstrapPhase { HardwareInit, NetworkDiscovery, KnowledgeSync, Operational }

#[derive(Debug)]
pub struct BootstrapState {
    phase: BootstrapPhase,
    started_at: Instant,
    phase_started_at: Instant,
    phase_durations: Vec<(BootstrapPhase, Duration)>,
}

impl BootstrapState {
    pub fn new() -> Self { Self { phase: BootstrapPhase::HardwareInit, started_at: Instant::now(), phase_started_at: Instant::now(), phase_durations: Vec::new() } }
    pub fn phase(&self) -> BootstrapPhase { self.phase }
    pub fn advance(&mut self) {
        let now = Instant::now();
        let dur = now - self.phase_started_at;
        self.phase_durations.push((self.phase, dur));
        self.phase = match self.phase { BootstrapPhase::HardwareInit => BootstrapPhase::NetworkDiscovery, BootstrapPhase::NetworkDiscovery => BootstrapPhase::KnowledgeSync, BootstrapPhase::KnowledgeSync => BootstrapPhase::Operational, BootstrapPhase::Operational => BootstrapPhase::Operational };
        self.phase_started_at = now;
    }
    pub fn is_ready(&self) -> bool { self.phase == BootstrapPhase::Operational }
    pub fn durations(&self) -> &Vec<(BootstrapPhase, Duration)> { &self.phase_durations }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn fsm_progresses() {
        let mut st = BootstrapState::new();
        assert_eq!(st.phase(), BootstrapPhase::HardwareInit);
        st.advance();
        st.advance();
        st.advance();
        assert!(st.is_ready());
    }
}
