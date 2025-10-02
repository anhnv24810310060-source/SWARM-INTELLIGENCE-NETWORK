use anyhow::{Result, anyhow};
use async_trait::async_trait;
use std::time::Instant;
use tracing::{instrument};
use serde::{Serialize, Deserialize};
use swarm_core::{detection_metrics, record_detection};
use opentelemetry::metrics::{Histogram, Meter};
use once_cell::sync::Lazy;
use crate::signature_db::{SignatureDb, SignatureMeta};
use crate::anomaly::{AnomalyDetector, AnomalyScore};
#[cfg(feature = "onnx") ] use crate::ml::OnnxModel;
use itertools::Itertools;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RawEvent {
    pub id: String,
    pub bytes: Vec<u8>,
    pub ts: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PipelineOutcome {
    pub event_id: String,
    pub signature_hits: Vec<String>,
    pub anomaly: Option<AnomalyScore>,
    pub ml_confidence: Option<f32>,
    pub threat: bool,
    pub latency_ms: StageLatencies,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct StageLatencies { pub ingestion_ms: f64, pub signature_ms: f64, pub anomaly_ms: f64, pub ml_ms: f64, pub total_ms: f64 }

#[async_trait]
trait Stage {
    async fn run(&self, ctx: &mut EventContext) -> Result<()>;
}

struct EventContext {
    raw: RawEvent,
    feats: Option<FeatureVector>,
    signature_hits: Vec<SignatureMeta>,
    anomaly_score: Option<AnomalyScore>,
    ml_confidence: Option<f32>,
    lat: StageLatencies,
}

#[derive(Debug, Clone)]
pub struct FeatureVector { pub id: String, pub values: Vec<f32> }

fn entropy(bytes: &[u8]) -> f32 { if bytes.is_empty(){return 0.0;} let mut freq=[0usize;256]; for b in bytes { freq[*b as usize]+=1; } let len = bytes.len() as f32; let mut h=0.0; for f in freq.iter().copied() { if f==0 { continue; } let p = f as f32 / len; h -= p * p.ln(); } h }

fn extract_features(raw: &RawEvent) -> FeatureVector {
    let data = &raw.bytes;
    let len = data.len() as f32;
    let len_norm = (len.min(65535.0) / 65535.0) as f32;
    let ent = entropy(data) / 8.0; // normalize approx
    let printable = data.iter().filter(|b| b.is_ascii_graphic()).count() as f32 / (len.max(1.0));
    let digits = data.iter().filter(|b| b.is_ascii_digit()).count() as f32 / (len.max(1.0));
    let hex = data.iter().filter(|b| matches!(b, b'0'..=b'9'|b'a'..=b'f'|b'A'..=b'F')).count() as f32 / (len.max(1.0));
    let crc = crc32fast::hash(data) as f32 / (u32::MAX as f32);
    let base_feats = vec![len_norm, ent, printable, digits, hex, crc];
    FeatureVector { id: raw.id.clone(), values: base_feats }
}

pub struct DetectionPipeline { signature: SignatureDb, anomaly: AnomalyDetector, #[cfg(feature = "onnx")] model: OnnxModel }

static STAGE_METER: Lazy<Meter> = Lazy::new(|| opentelemetry::global::meter("swarm_detection"));
static STAGE_LAT_HISTO: Lazy<Histogram<f64>> = Lazy::new(|| STAGE_METER.f64_histogram("detection_stage_latency_ms")
    .with_description("Latency per detection pipeline stage (ms)")
    .init());

impl DetectionPipeline {
    pub async fn new() -> Result<Self> {
        Ok(Self { signature: SignatureDb::open(Default::default())?, anomaly: AnomalyDetector::new(3.5, 2.5, 257), #[cfg(feature = "onnx")] model: OnnxModel::load_env()? })
    }

    pub fn anomaly_debug_snapshot(&self) -> impl Fn() -> AnomalyDebug + Send + Sync + 'static {
        use std::sync::Mutex;
        let mut_det = Mutex::new(self.anomaly.clone());
        move || {
            let mut a = mut_det.lock().unwrap();
            AnomalyDebug {
                hard_threshold: a.hard_threshold(),
                current_quantile: a.current_quantile(),
                adjustments: a.adjust_history().to_vec(),
            }
        }
    }

    pub fn signature_load(&self, path: &str) -> Result<()> { self.signature.load_rules_file(path) }

    #[instrument(skip(self, ev))]
    pub async fn process(&self, ev: RawEvent) -> Result<PipelineOutcome> {
        let start = Instant::now();
        let max_size = std::env::var("SWARM__DETECTION__EVENT_MAX_BYTES").ok().and_then(|v| v.parse::<usize>().ok()).unwrap_or(262_144);
        if ev.bytes.len() > max_size { 
            // record dropped metric via detection_metrics if we add one later; for now use anomaly_total as placeholder tag NOTE: production should add dedicated counter
            // returning benign outcome
            return Ok(PipelineOutcome { event_id: ev.id, signature_hits: vec![], anomaly: None, ml_confidence: None, threat: false, latency_ms: StageLatencies::default() });
        }
        let mut ctx = EventContext { raw: ev, feats: None, signature_hits: Vec::new(), anomaly_score: None, ml_confidence: None, lat: StageLatencies::default() };

    // Stage 1 ingestion / feature extraction
        let s = Instant::now();
        ctx.feats = Some(extract_features(&ctx.raw));
        ctx.lat.ingestion_ms = s.elapsed().as_secs_f64()*1000.0;
    STAGE_LAT_HISTO.record(ctx.lat.ingestion_ms, &[opentelemetry::KeyValue::new("stage", "ingest")]);

        // Stage 2 signature match
        let s = Instant::now();
    if let Some(_f) = &ctx.feats { ctx.signature_hits = self.signature.match_bytes(&ctx.raw.bytes); }
        ctx.lat.signature_ms = s.elapsed().as_secs_f64()*1000.0;
        if !ctx.signature_hits.is_empty() { detection_metrics().signature_total.add(ctx.signature_hits.len() as u64, &[]); }
    STAGE_LAT_HISTO.record(ctx.lat.signature_ms, &[opentelemetry::KeyValue::new("stage", "signature")]);

        // Stage 3 anomaly detection
        let s = Instant::now();
    if let Some(f) = &ctx.feats { ctx.anomaly_score = Some(self.anomaly.score(&f.values)); }
        ctx.lat.anomaly_ms = s.elapsed().as_secs_f64()*1000.0;
        if let Some(a) = &ctx.anomaly_score { if self.anomaly.is_anomaly(a) { detection_metrics().anomaly_total.add(1, &[]); } }
    STAGE_LAT_HISTO.record(ctx.lat.anomaly_ms, &[opentelemetry::KeyValue::new("stage", "anomaly")]);

        // Stage 4 ml inference (optional)
        #[cfg(feature = "onnx")] {
            let s = Instant::now();
            if let Some(f) = &ctx.feats { ctx.ml_confidence = self.model.infer_batched(&f.values).await?.map(|r| r.confidence); }
            ctx.lat.ml_ms = s.elapsed().as_secs_f64()*1000.0;
            STAGE_LAT_HISTO.record(ctx.lat.ml_ms, &[opentelemetry::KeyValue::new("stage", "ml")]);
        }

        ctx.lat.total_ms = start.elapsed().as_secs_f64()*1000.0;
        detection_metrics().alert_latency_ms.record(ctx.lat.total_ms, &[]);
        detection_metrics().e2e_latency_ms.record(ctx.lat.total_ms, &[]);

        let anomaly_flag = ctx.anomaly_score.as_ref().map(|a| self.anomaly.is_anomaly(a)).unwrap_or(false);
        let ml_flag = ctx.ml_confidence.map(|c| c > 0.85).unwrap_or(false);
        let threat = !ctx.signature_hits.is_empty() || anomaly_flag || ml_flag;
        record_detection(false);

        Ok(PipelineOutcome {
            event_id: ctx.raw.id,
            signature_hits: ctx.signature_hits.iter().map(|m| m.id.clone()).collect_vec(),
            anomaly: ctx.anomaly_score,
            ml_confidence: ctx.ml_confidence,
            threat,
            latency_ms: ctx.lat,
        })
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct AnomalyDebug { pub hard_threshold: f64, pub current_quantile: Option<f64>, pub adjustments: Vec<(u64,f64)> }

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
