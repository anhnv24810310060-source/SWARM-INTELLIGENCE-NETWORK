//! Communication Module - Nervous System của Node
//! P2P messaging, consensus participation, knowledge sharing
use anyhow::Result;
use tracing::{info, debug, warn};
use serde::{Deserialize, Serialize};
use std::sync::Arc;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Message {
    pub id: String,
    pub from: String,
    pub to: Option<String>, // None = broadcast
    pub msg_type: MessageType,
    pub payload: Vec<u8>,
    pub timestamp: u64,
    pub ttl: u8,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum MessageType {
    Alert,
    Intelligence,
    Consensus,
    Heartbeat,
    ModelUpdate,
    PolicyUpdate,
}

pub struct CommunicationModule {
    node_id: String,
    peers: Arc<tokio::sync::RwLock<Vec<String>>>,
    message_queue: Arc<tokio::sync::RwLock<Vec<Message>>>,
    nats_client: Option<async_nats::Client>,
}

impl CommunicationModule {
    pub async fn new(node_id: String, nats_url: &str) -> Result<Self> {
        let nats_client = match async_nats::connect(nats_url).await {
            Ok(client) => {
                info!("Connected to NATS at {}", nats_url);
                Some(client)
            }
            Err(e) => {
                warn!("Failed to connect to NATS: {}", e);
                None
            }
        };

        Ok(Self {
            node_id,
            peers: Arc::new(tokio::sync::RwLock::new(Vec::new())),
            message_queue: Arc::new(tokio::sync::RwLock::new(Vec::new())),
            nats_client,
        })
    }

    /// Broadcast message tới tất cả peers (Gossip protocol)
    pub async fn broadcast(&self, msg_type: MessageType, payload: Vec<u8>) -> Result<()> {
        let msg = Message {
            id: uuid::Uuid::new_v4().to_string(),
            from: self.node_id.clone(),
            to: None,
            msg_type,
            payload,
            timestamp: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_millis() as u64,
            ttl: 10,
        };

        if let Some(client) = &self.nats_client {
            let subject = format!("swarm.gossip.{:?}", msg.msg_type).to_lowercase();
            let data = serde_json::to_vec(&msg)?;
            client.publish(subject, data.into()).await?;
            debug!("Broadcast message {} to swarm", msg.id);
        }

        Ok(())
    }

    /// Send direct message tới một peer cụ thể
    pub async fn send_direct(&self, peer_id: &str, msg_type: MessageType, payload: Vec<u8>) -> Result<()> {
        let msg = Message {
            id: uuid::Uuid::new_v4().to_string(),
            from: self.node_id.clone(),
            to: Some(peer_id.to_string()),
            msg_type,
            payload,
            timestamp: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_millis() as u64,
            ttl: 10,
        };

        if let Some(client) = &self.nats_client {
            let subject = format!("swarm.direct.{}", peer_id);
            let data = serde_json::to_vec(&msg)?;
            client.publish(subject, data.into()).await?;
            debug!("Sent direct message {} to peer {}", msg.id, peer_id);
        }

        Ok(())
    }

    /// Subscribe và xử lý incoming messages
    pub async fn start_listening(&self) -> Result<()> {
        if let Some(client) = &self.nats_client {
            let node_id = self.node_id.clone();
            let client = client.clone();
            
            tokio::spawn(async move {
                // Subscribe to gossip messages
                if let Ok(mut sub) = client.subscribe("swarm.gossip.>".into()).await {
                    while let Some(msg) = sub.next().await {
                        if let Ok(message) = serde_json::from_slice::<Message>(&msg.payload) {
                            debug!("Received gossip message: {:?}", message.msg_type);
                            // TODO: Process message
                        }
                    }
                }
            });

            let node_id2 = self.node_id.clone();
            let client2 = client.clone();
            
            tokio::spawn(async move {
                // Subscribe to direct messages
                let subject = format!("swarm.direct.{}", node_id2);
                if let Ok(mut sub) = client2.subscribe(subject.into()).await {
                    while let Some(msg) = sub.next().await {
                        if let Ok(message) = serde_json::from_slice::<Message>(&msg.payload) {
                            debug!("Received direct message from: {}", message.from);
                            // TODO: Process message
                        }
                    }
                }
            });
        }

        Ok(())
    }

    /// Discover và thêm peers mới
    pub async fn discover_peers(&self) -> Result<Vec<String>> {
        // TODO: Implement peer discovery protocol
        Ok(vec![])
    }

    /// Add peer vào danh sách
    pub async fn add_peer(&self, peer_id: String) -> Result<()> {
        let mut peers = self.peers.write().await;
        if !peers.contains(&peer_id) {
            peers.push(peer_id.clone());
            info!("Added peer: {}", peer_id);
        }
        Ok(())
    }

    /// Remove peer khỏi danh sách
    pub async fn remove_peer(&self, peer_id: &str) -> Result<()> {
        let mut peers = self.peers.write().await;
        peers.retain(|p| p != peer_id);
        info!("Removed peer: {}", peer_id);
        Ok(())
    }

    /// Get số lượng peers hiện tại
    pub async fn peer_count(&self) -> usize {
        self.peers.read().await.len()
    }
}
