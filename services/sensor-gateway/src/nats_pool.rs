use async_nats::Client;
use std::sync::Arc;
use tokio::sync::Semaphore;
use parking_lot::Mutex;

/// NATS connection pool for high-throughput publishing
pub struct NatsPool {
    connections: Vec<Arc<Client>>,
    semaphore: Arc<Semaphore>,
    next_index: Arc<Mutex<usize>>,
}

impl NatsPool {
    /// Create pool with specified size
    pub async fn new(url: &str, pool_size: usize) -> Result<Self, async_nats::Error> {
        let mut connections = Vec::with_capacity(pool_size);
        for _ in 0..pool_size {
            let client = async_nats::connect(url).await?;
            connections.push(Arc::new(client));
        }
        
        Ok(Self {
            connections,
            semaphore: Arc::new(Semaphore::new(pool_size)),
            next_index: Arc::new(Mutex::new(0)),
        })
    }

    /// Get next connection using round-robin
    pub fn get_connection(&self) -> Arc<Client> {
        let mut index = self.next_index.lock();
        let conn = self.connections[*index % self.connections.len()].clone();
        *index = (*index + 1) % self.connections.len();
        conn
    }

    /// Publish with automatic connection selection
    pub async fn publish(&self, subject: impl Into<String>, payload: Vec<u8>) -> Result<(), async_nats::Error> {
        let _permit = self.semaphore.acquire().await.unwrap();
        let conn = self.get_connection();
        conn.publish(subject.into(), payload.into()).await
    }

    /// Publish batch of messages
    pub async fn publish_batch(&self, messages: Vec<(String, Vec<u8>)>) -> Result<(), async_nats::Error> {
        let _permit = self.semaphore.acquire().await.unwrap();
        let conn = self.get_connection();
        
        for (subject, payload) in messages {
            conn.publish(subject, payload.into()).await?;
        }
        
        Ok(())
    }

    /// Get pool size
    pub fn size(&self) -> usize {
        self.connections.len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    #[ignore] // Requires NATS server
    async fn test_pool_creation() {
        let pool = NatsPool::new("127.0.0.1:4222", 4).await;
        assert!(pool.is_ok());
        assert_eq!(pool.unwrap().size(), 4);
    }

    #[tokio::test]
    #[ignore]
    async fn test_round_robin() {
        let pool = NatsPool::new("127.0.0.1:4222", 3).await.unwrap();
        
        // Get connections and verify round-robin
        let c1 = pool.get_connection();
        let c2 = pool.get_connection();
        let c3 = pool.get_connection();
        let c4 = pool.get_connection();
        
        // c4 should be same as c1 (wrapped around)
        assert!(Arc::ptr_eq(&c1, &c4));
    }
}
