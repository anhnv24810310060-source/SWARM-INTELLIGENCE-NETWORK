//! Core node modules implementing the four-layer architecture
pub mod sensor;
pub mod brain;
pub mod communication;
pub mod action;

pub use sensor::{SensorModule, SensorConfig, SensorReading};
pub use brain::{BrainModule, Threat, Decision, ActionType as BrainActionType};
pub use communication::{CommunicationModule, Message, MessageType};
pub use action::{ActionModule, Action, ActionType, ActionStatus};
