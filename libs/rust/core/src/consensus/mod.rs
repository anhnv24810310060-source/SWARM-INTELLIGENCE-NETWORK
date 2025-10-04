//! Consensus module (PBFT skeleton) – production-oriented structure.

pub mod pbft;
pub use pbft::{PBFTConsensus, PBFTConfig, PBFTMessage, PBFTPhase, Digest, ViewNumber, SequenceNumber};

use uuid::Uuid;
use serde::{Serialize, Deserialize};
use std::fmt::{Display, Formatter};

#[derive(Clone, Copy, Debug, Hash, Eq, PartialEq, Serialize, Deserialize)]
pub struct NodeId(Uuid);

impl NodeId {
    pub fn generate() -> Self { Self(Uuid::new_v4()) }
    pub fn from_uuid(u: Uuid) -> Self { Self(u) }
    pub fn as_uuid(&self) -> Uuid { self.0 }
}

impl Display for NodeId {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result { write!(f, "{}", self.0) }
}
