use anyhow::Result;
use async_trait::async_trait;
use std::time::Instant;
use tracing::{instrument, debug};
use serde::{Serialize, Deserialize};
use swarm_core::{detection_metrics, record_detection};

use crate::signature_db::SignatureDb;
use crate::anomaly::AnomalyDetector;
#[cfg(feature = "onnx")] use crate::ml::OnnxModel;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RawEvent {
    pub id: String,
    pub bytes: Vec<u8>,
    pub ts: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PipelineOutcome {
    pub event_id: String,
    pub signature_match: Option<String>,
    pub anomaly_score: f64,
    pub ml_confidence: Option<f32>,
    pub threat: bool,
    pub latency_ms: StageLatencies,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct StageLatencies {
    pub ingestion_ms: f64,
    pub signature_ms: f64,
    pub anomaly_ms: f64,
    pub ml_ms: f64,
    pub total_ms: f64,
}

#[async_trait]
trait Stage {
    async fn run(&self, ctx: &mut EventContext) -> Result<()>;
}

struct EventContext {
    raw: RawEvent,
    normalized: Option<Normalized>,
    signature_match: Option<String>,
    anomaly_score: f64,
    ml_confidence: Option<f32>,
    lat: StageLatencies,
}

#[derive(Debug, Clone)]
struct Normalized {
    id: String,
    features: Vec<f32>,
}

pub struct DetectionPipeline {
    signature: SignatureDb,
    anomaly: AnomalyDetector,
    #[cfg(feature = "onnx")] model: OnnxModel,
}

impl DetectionPipeline {
    pub async fn new() -> Result<Self> {
        Ok(Self {
            signature: SignatureDb::open(Default::default())?,
            anomaly: AnomalyDetector::new(0.3, 0.05),
            #[cfg(feature = "onnx")] model: OnnxModel::load_env()?,
        })
    }

    #[instrument(skip(self, ev))]
    pub async fn process(&self, ev: RawEvent) -> Result<PipelineOutcome> {
        let start = Instant::now();
        let mut ctx = EventContext { raw: ev, normalized: None, signature_match: None, anomaly_score: 0.0, ml_confidence: None, lat: StageLatencies::default() };

        // Stage 1 ingestion/normalize
        let s = Instant::now();
        ctx.normalized = Some(self.normalize(&ctx.raw)?);
        ctx.lat.ingestion_ms = s.elapsed().as_secs_f64()*1000.0;

        // Stage 2 signature match
        let s = Instant::now();
        if let Some(norm) = &ctx.normalized { ctx.signature_match = self.signature.match_event(norm)?; }
        ctx.lat.signature_ms = s.elapsed().as_secs_f64()*1000.0;
        if ctx.signature_match.is_some() { detection_metrics().signature_total.add(1, &[]); }

        // Stage 3 anomaly detection
        let s = Instant::now();
        if let Some(norm) = &ctx.normalized { ctx.anomaly_score = self.anomaly.score(&norm.features); }
        ctx.lat.anomaly_ms = s.elapsed().as_secs_f64()*1000.0;
        if ctx.anomaly_score > self.anomaly.threshold() { detection_metrics().anomaly_total.add(1, &[]); }

        // Stage 4 ml inference (optional)
        #[cfg(feature = "onnx")] {
            let s = Instant::now();
            if let Some(norm) = &ctx.normalized { ctx.ml_confidence = self.model.infer(&norm.features)?; }
            ctx.lat.ml_ms = s.elapsed().as_secs_f64()*1000.0;
        }

        ctx.lat.total_ms = start.elapsed().as_secs_f64()*1000.0;
        detection_metrics().alert_latency_ms.record(ctx.lat.total_ms, &[]);
        detection_metrics().e2e_latency_ms.record(ctx.lat.total_ms, &[]);

        let threat = ctx.signature_match.is_some() || ctx.anomaly_score > self.anomaly.threshold() || ctx.ml_confidence.map(|c| c > 0.85).unwrap_or(false);
        record_detection(false);

        Ok(PipelineOutcome {
            event_id: ctx.raw.id,
            signature_match: ctx.signature_match,
            anomaly_score: ctx.anomaly_score,
            ml_confidence: ctx.ml_confidence,
            threat,
            latency_ms: ctx.lat,
        })
    }

    fn normalize(&self, ev: &RawEvent) -> Result<Normalized> {
        // Placeholder simple feature extraction
        let mut features = Vec::new();
        let len = ev.bytes.len() as f32;
        features.push(len.min(2048.0)/2048.0);
        features.push((len % 17.0)/17.0);
        Ok(Normalized { id: ev.id.clone(), features })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn basic_pipeline_runs() {
        let pipe = DetectionPipeline::new().await.unwrap();
        let ev = RawEvent { id: "e1".into(), bytes: vec![1,2,3,4,5,6,7,8], ts: 0 };
        let out = pipe.process(ev).await.unwrap();
        assert_eq!(out.event_id, "e1");
        assert!(out.latency_ms.total_ms >= 0.0);
    }
}
