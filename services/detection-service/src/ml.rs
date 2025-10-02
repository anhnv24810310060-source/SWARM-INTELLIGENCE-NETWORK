use anyhow::{Result, anyhow};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, Instant};
use parking_lot::RwLock;
use arc_swap::ArcSwap;
use std::sync::Arc;
#[cfg(feature = "onnx")] use tract_onnx::prelude::*;

#[derive(Clone, Debug)]
pub struct MLResult {
    pub class_id: usize,
    pub confidence: f32,
    pub topk: Vec<(usize,f32)>,
}

#[derive(Debug)]
struct ModelInner {
    #[cfg(feature = "onnx")] model: SimplePlan<TypedFact, Box<dyn TypedOp>, Graph<TypedFact, Box<dyn TypedOp>>>,
    mtime: SystemTime,
    input_dim: usize,
}

#[derive(Clone)]
pub struct OnnxModel {
    path: PathBuf,
    inner: ArcSwap<Arc<ModelInner>>,
    warm: Arc<RwLock<bool>>,
    timeout_ms: u64,
}

impl OnnxModel {
    pub fn load_env() -> Result<Self> {
        let path = std::env::var("SWARM__DETECTION__ML__MODEL_PATH").unwrap_or_else(|_| "models/model.onnx".into());
        let timeout_ms = std::env::var("SWARM__DETECTION__ML__TIMEOUT_MS").ok().and_then(|v| v.parse().ok()).unwrap_or(40);
        Self::load(Path::new(&path), timeout_ms)
    }

    pub fn load(path: &Path, timeout_ms: u64) -> Result<Self> {
        #[cfg(feature = "onnx")] {
            let metadata = std::fs::metadata(path)?;
            let mtime = metadata.modified()?;
            let model = tract_onnx::onnx().model_for_path(path)?
                .into_optimized()?
                .into_runnable()?;
            // Determine input dim heuristically (first input tensor total elements)
            let input_dim = model
                .model
                .inputs
                .get(0)
                .and_then(|i| model.model.outlet_fact(*i).ok())
                .and_then(|f| f.shape.as_concrete().map(|s| s.iter().product::<usize>()))
                .unwrap_or(256);
            let inner = Arc::new(ModelInner { model, mtime, input_dim });
            let this = Self { path: path.to_path_buf(), inner: ArcSwap::from(inner), warm: Arc::new(RwLock::new(false)), timeout_ms };
            this.warmup()?;
            Ok(this)
        }
        #[cfg(not(feature = "onnx"))]
        {
            Ok(Self { path: path.to_path_buf(), inner: ArcSwap::from(Arc::new(ModelInner { mtime: SystemTime::now(), input_dim: 256 })), warm: Arc::new(RwLock::new(true)), timeout_ms })
        }
    }

    pub fn reload_if_changed(&self) -> Result<()> {
        #[cfg(feature = "onnx")] {
            let meta = std::fs::metadata(&self.path)?;
            let mtime = meta.modified()?;
            if mtime > self.inner.load().mtime { // changed
                let model = tract_onnx::onnx().model_for_path(&self.path)?
                    .into_optimized()?.into_runnable()?;
                let input_dim = model.model.inputs.get(0)
                    .and_then(|i| model.model.outlet_fact(*i).ok())
                    .and_then(|f| f.shape.as_concrete().map(|s| s.iter().product::<usize>()))
                    .unwrap_or(256);
                let inner = Arc::new(ModelInner { model, mtime, input_dim });
                self.inner.store(inner);
                self.warmup()?;
            }
        }
        Ok(())
    }

    fn warmup(&self) -> Result<()> {
        let mut w = self.warm.write();
        if *w { return Ok(()); }
        #[cfg(feature = "onnx"))]
        {
            // run a dummy inference
            let inner = self.inner.load();
            let dim = inner.input_dim;
            let input: Vec<f32> = vec![0.0; dim];
            let _ = inner.model.run(tvec!(Tensor::from_shape(&[1, dim as i64], &input)?));
        }
        *w = true;
        Ok(())
    }

    pub fn input_dim(&self) -> usize { self.inner.load().input_dim }

    pub fn infer(&self, feats: &[f32]) -> Result<Option<MLResult>> {
        self.reload_if_changed().ok();
        #[cfg(feature = "onnx")] {
            use std::time::Duration; use tokio::runtime::Handle;
            if feats.len() != self.input_dim() { return Ok(None); }
            let inner = self.inner.load();
            let start = Instant::now();
            // blocking run (tract not async). Consider spawn_blocking for heavy.
            let output = inner.model.run(tvec!(Tensor::from_shape(&[1, feats.len() as i64], feats)?))?;
            let elapsed = start.elapsed().as_millis() as u64;
            if elapsed > self.timeout_ms { return Ok(None); }
            let tensor = &output[0];
            let slice: &[f32] = tensor.to_array_view::<f32>()?.iter().copied().collect::<Vec<_>>().leak();
            let mut probs = slice.to_vec();
            softmax(&mut probs);
            if let Some((idx, conf)) = probs.iter().enumerate().max_by(|a,b| a.1.partial_cmp(b.1).unwrap()) { 
                let mut topk: Vec<(usize,f32)> = probs.iter().enumerate().collect();
                topk.sort_by(|a,b| b.1.partial_cmp(a.1).unwrap());
                topk.truncate(5);
                return Ok(Some(MLResult { class_id: idx, confidence: *conf, topk }));
            }
            return Ok(None);
        }
        #[cfg(not(feature = "onnx"))]
        { Ok(None) }
    }
}

fn softmax(v: &mut [f32]) {
    if v.is_empty() { return; }
    let max = v.iter().cloned().fold(f32::NEG_INFINITY, f32::max);
    let mut sum = 0.0;
    for x in v.iter_mut() { *x = (*x - max).exp(); sum += *x; }
    if sum > 0.0 { for x in v.iter_mut() { *x /= sum; } }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn softmax_basic() {
        let mut v = vec![1.0,2.0,3.0];
        softmax(&mut v);
        let s: f32 = v.iter().sum();
        assert!((s - 1.0).abs() < 1e-5);
    }
}
