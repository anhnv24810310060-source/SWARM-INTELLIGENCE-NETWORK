//! Federated Learning module (round-based coordinator skeleton)

pub mod coordinator;
pub use coordinator::{
    FederatedLearningCoordinator,
    AggregationMethod,
    RoundId,
    ModelVersion,
    ClientUpdate,
    AggregatedModel,
};
