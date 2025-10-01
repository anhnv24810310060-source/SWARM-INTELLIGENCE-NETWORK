// Generated protobuf modules via tonic's include_proto! macro.
// Each module corresponds to a proto package (e.g. package swarm.consensus -> module consensus).
// Usage example:
//   use swarm_proto::consensus::pbft_server::PbftServer;
//   use swarm_proto::consensus::{Proposal, Ack};

pub mod common { tonic::include_proto!("swarm.common"); }
pub mod consensus { tonic::include_proto!("swarm.consensus"); }
pub mod events { tonic::include_proto!("swarm.events"); }
pub mod federation { tonic::include_proto!("swarm.federation"); }
pub mod ingestion { tonic::include_proto!("swarm.ingestion"); }

// Re-export frequently used consensus types at crate root (optional convenience)
pub use consensus::*;
