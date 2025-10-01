//! Simplified PBFT-style consensus state machine (local, no networking yet).
//!
//! Phases: Idle -> Proposed -> Prepared -> Committed -> Executed -> Idle
//! Safety: requires external transport to guarantee message integrity / ordering.
//!
//! Future extensions: view change, batching, durability, authentication, telemetry.

use anyhow::Result;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;
use std::sync::Arc;
use tokio::sync::RwLock;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq, Hash)]
pub struct NodeId(pub String);

#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum ConsensusMessage {
    Proposal { id: u64, data: Vec<u8>, proposer: NodeId },
    Prepare { id: u64, node: NodeId },
    Commit { id: u64, node: NodeId },
    Execute { id: u64 },
}

#[derive(Debug, Clone, PartialEq)]
pub enum ConsensusPhase { Idle, Proposed, Prepared, Committed, Executed }

struct ConsensusState {
    phase: ConsensusPhase,
    proposal_id: u64,
    proposal_data: Option<Vec<u8>>,
    prepare_votes: HashSet<NodeId>,
    commit_votes: HashSet<NodeId>,
}

pub struct PBFTConsensus {
    node_id: NodeId,
    total_nodes: usize,
    state: Arc<RwLock<ConsensusState>>,
}

impl PBFTConsensus {
    pub fn new(node_id: NodeId, total_nodes: usize) -> Self {
        Self { node_id, total_nodes, state: Arc::new(RwLock::new(ConsensusState { phase: ConsensusPhase::Idle, proposal_id: 0, proposal_data: None, prepare_votes: HashSet::new(), commit_votes: HashSet::new() })) }
    }

    pub async fn propose(&self, data: Vec<u8>) -> Result<ConsensusMessage> {
        let mut st = self.state.write().await;
        st.proposal_id += 1;
        st.proposal_data = Some(data.clone());
        st.phase = ConsensusPhase::Proposed;
        Ok(ConsensusMessage::Proposal { id: st.proposal_id, data, proposer: self.node_id.clone() })
    }

    pub async fn handle_message(&self, msg: ConsensusMessage) -> Result<Option<ConsensusMessage>> {
        match msg {
            ConsensusMessage::Proposal { id, data, proposer } => self.handle_proposal(id, data, proposer).await,
            ConsensusMessage::Prepare { id, node } => self.handle_prepare(id, node).await,
            ConsensusMessage::Commit { id, node } => self.handle_commit(id, node).await,
            ConsensusMessage::Execute { id } => self.handle_execute(id).await,
        }
    }

    async fn handle_proposal(&self, id: u64, data: Vec<u8>, _proposer: NodeId) -> Result<Option<ConsensusMessage>> {
        let mut st = self.state.write().await;
        if st.phase != ConsensusPhase::Idle { return Ok(None); }
        st.proposal_id = id;
        st.proposal_data = Some(data);
        st.phase = ConsensusPhase::Proposed;
        Ok(Some(ConsensusMessage::Prepare { id, node: self.node_id.clone() }))
    }

    async fn handle_prepare(&self, id: u64, node: NodeId) -> Result<Option<ConsensusMessage>> {
        let mut st = self.state.write().await;
        if st.proposal_id != id || st.phase != ConsensusPhase::Proposed { return Ok(None); }
        st.prepare_votes.insert(node);
        let required = (self.total_nodes * 2) / 3 + 1;
        if st.prepare_votes.len() >= required {
            st.phase = ConsensusPhase::Prepared;
            return Ok(Some(ConsensusMessage::Commit { id, node: self.node_id.clone() }));
        }
        Ok(None)
    }

    async fn handle_commit(&self, id: u64, node: NodeId) -> Result<Option<ConsensusMessage>> {
        let mut st = self.state.write().await;
        if st.proposal_id != id || st.phase != ConsensusPhase::Prepared { return Ok(None); }
        st.commit_votes.insert(node);
        let required = (self.total_nodes * 2) / 3 + 1;
        if st.commit_votes.len() >= required {
            st.phase = ConsensusPhase::Committed;
            return Ok(Some(ConsensusMessage::Execute { id }));
        }
        Ok(None)
    }

    async fn handle_execute(&self, id: u64) -> Result<Option<ConsensusMessage>> {
        let mut st = self.state.write().await;
        if st.proposal_id != id || st.phase != ConsensusPhase::Committed { return Ok(None); }
        st.phase = ConsensusPhase::Executed;
        tracing::info!(proposal_id = id, "Consensus executed");
        st.prepare_votes.clear();
        st.commit_votes.clear();
        st.proposal_data = None;
        st.phase = ConsensusPhase::Idle;
        Ok(None)
    }

    pub async fn get_phase(&self) -> ConsensusPhase { self.state.read().await.phase.clone() }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn proposal_flow() {
        let c = PBFTConsensus::new(NodeId("n1".into()), 4);
        let p = c.propose(vec![1,2,3]).await.unwrap();
        matches!(p, ConsensusMessage::Proposal { .. });
        assert_eq!(c.get_phase().await, ConsensusPhase::Proposed);
    }
}
