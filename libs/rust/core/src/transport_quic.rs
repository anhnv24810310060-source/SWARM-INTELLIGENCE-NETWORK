//! QUIC transport abstraction skeleton.
//!
//! Placeholder to align with design (Section 2.3.3 Streaming Protocol / QUIC).
//! Real implementation will use the `quinn` crate and integrate OTEL tracing.

#[derive(Debug)]
pub struct QuicConfig {
    pub alpn: Vec<String>,
    pub idle_timeout_secs: u64,
}

impl Default for QuicConfig {
    fn default() -> Self { Self { alpn: vec!["swarm/1".into()], idle_timeout_secs: 30 } }
}

#[derive(Debug)]
pub struct QuicConnectionHandle {
    pub peer_id: String,
}

pub struct QuicTransport {
    cfg: QuicConfig,
}

impl QuicTransport {
    pub fn new(cfg: QuicConfig) -> Self { Self { cfg } }
    pub async fn connect(&self, peer: &str) -> anyhow::Result<QuicConnectionHandle> { Ok(QuicConnectionHandle { peer_id: peer.to_string() }) }
    pub async fn open_stream(&self, _conn: &QuicConnectionHandle, _logical: &str) -> anyhow::Result<()> { Ok(()) }
    pub async fn send(&self, _conn: &QuicConnectionHandle, _data: &[u8]) -> anyhow::Result<()> { Ok(()) }
    pub async fn recv(&self, _conn: &QuicConnectionHandle) -> anyhow::Result<Vec<u8>> { Ok(Vec::new()) }
}
