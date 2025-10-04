use anyhow::Result;
use tracing::info;
use swarm_core::{FederatedLearningCoordinator, AggregationMethod};

pub struct FederationModule {
    coordinator: FederatedLearningCoordinator,
}

impl FederationModule {
    pub async fn new() -> Result<Self> {
        let coordinator = FederatedLearningCoordinator::new(10, AggregationMethod::FedAvg);
        info!("federation_module_initialized");
        Ok(Self { coordinator })
    }

    pub async fn shutdown(&self) -> Result<()> {
        info!("federation_module_shutdown");
        Ok(())
    }
}
