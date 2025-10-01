//! Sensor Module - Eyes & Ears của Node
//! Thu thập dữ liệu network traffic, system behavior, user activity
use anyhow::Result;
use tracing::{info, debug, warn};
use std::sync::Arc;
use tokio::sync::RwLock;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SensorReading {
    pub timestamp: u64,
    pub sensor_type: SensorType,
    pub data: Vec<u8>,
    pub metadata: std::collections::HashMap<String, String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub enum SensorType {
    NetworkTraffic,
    SystemBehavior,
    UserActivity,
    ThreatIntel,
}

pub struct SensorModule {
    config: SensorConfig,
    readings: Arc<RwLock<Vec<SensorReading>>>,
    enabled: bool,
}

#[derive(Debug, Clone)]
pub struct SensorConfig {
    pub buffer_size: usize,
    pub sampling_rate_ms: u64,
    pub enable_network: bool,
    pub enable_system: bool,
    pub enable_user: bool,
}

impl Default for SensorConfig {
    fn default() -> Self {
        Self {
            buffer_size: 1000,
            sampling_rate_ms: 100,
            enable_network: true,
            enable_system: true,
            enable_user: false,
        }
    }
}

impl SensorModule {
    pub fn new(config: SensorConfig) -> Self {
        Self {
            config,
            readings: Arc::new(RwLock::new(Vec::with_capacity(1000))),
            enabled: true,
        }
    }

    pub async fn start(&self) -> Result<()> {
        info!("Starting sensor module");
        let readings = self.readings.clone();
        let config = self.config.clone();
        
        tokio::spawn(async move {
            loop {
                if config.enable_network {
                    Self::collect_network_data(&readings).await;
                }
                if config.enable_system {
                    Self::collect_system_data(&readings).await;
                }
                tokio::time::sleep(std::time::Duration::from_millis(config.sampling_rate_ms)).await;
            }
        });
        
        Ok(())
    }

    async fn collect_network_data(readings: &Arc<RwLock<Vec<SensorReading>>>) {
        let reading = SensorReading {
            timestamp: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_millis() as u64,
            sensor_type: SensorType::NetworkTraffic,
            data: vec![], // TODO: Implement actual network capture
            metadata: std::collections::HashMap::new(),
        };
        
        let mut r = readings.write().await;
        if r.len() >= 1000 {
            r.remove(0);
        }
        r.push(reading);
    }

    async fn collect_system_data(readings: &Arc<RwLock<Vec<SensorReading>>>) {
        let reading = SensorReading {
            timestamp: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_millis() as u64,
            sensor_type: SensorType::SystemBehavior,
            data: vec![], // TODO: Implement system metrics collection
            metadata: std::collections::HashMap::new(),
        };
        
        let mut r = readings.write().await;
        if r.len() >= 1000 {
            r.remove(0);
        }
        r.push(reading);
    }

    pub async fn get_recent_readings(&self, count: usize) -> Vec<SensorReading> {
        let r = self.readings.read().await;
        let start = if r.len() > count { r.len() - count } else { 0 };
        r[start..].to_vec()
    }

    pub fn enable(&mut self) {
        self.enabled = true;
        info!("Sensor module enabled");
    }

    pub fn disable(&mut self) {
        self.enabled = false;
        warn!("Sensor module disabled");
    }
}
