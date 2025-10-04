use anyhow::Result;
use tracing::info;
use swarm_core::{PBFTConsensus, NodeId};

pub struct ConsensusModule {
    consensus: PBFTConsensus,
}

impl ConsensusModule {
    pub async fn new() -> Result<Self> {
        let node_id = NodeId::generate();
        let consensus = PBFTConsensus::new(node_id, 10);
        info!(%node_id, "consensus_module_initialized");
        Ok(Self { consensus })
    }

    pub async fn shutdown(&self) -> Result<()> {
        info!("consensus_module_shutdown");
        Ok(())
    }
}
