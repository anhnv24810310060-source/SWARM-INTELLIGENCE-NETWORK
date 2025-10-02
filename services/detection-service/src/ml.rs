use anyhow::{Result, anyhow};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, Instant, Duration};
use parking_lot::RwLock;
use arc_swap::ArcSwap;
use std::sync::Arc;
#[cfg(feature = "onnx")] use tract_onnx::prelude::*;
#[cfg(feature = "onnx")] use tokio::sync::{mpsc, oneshot};
#[cfg(feature = "onnx")] use tokio::task::JoinHandle;

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
    #[cfg(feature = "onnx")] batcher: Option<InferenceBatcher>,
}

#[cfg(feature = "onnx")]
#[derive(Clone)]
struct InferenceBatcher {
    tx: mpsc::Sender<BatchReq>,
    dim: usize,
}

#[cfg(feature = "onnx")]
struct BatchReq {
    input: Vec<f32>,
    resp: oneshot::Sender<Result<Option<MLResult>>>,
}

#[cfg(feature = "onnx")]
impl InferenceBatcher {
    fn new(dim: usize, model: OnnxModelHandle, timeout_ms: u64, batch_window: Duration, max_batch: usize) -> Self {
        let (tx, mut rx) = mpsc::channel::<BatchReq>(1024);
        // Spawn worker
        tokio::spawn(async move {
            while let Some(first) = rx.recv().await {
                let mut reqs = vec![first];
                let start_wait = Instant::now();
                while reqs.len() < max_batch && start_wait.elapsed() < batch_window {
                    let remain = batch_window.checked_sub(start_wait.elapsed()).unwrap_or_default();
                    if remain.is_zero() { break; }
                    let fut = rx.recv();
                    match tokio::time::timeout(remain, fut).await {
                        Ok(Some(r)) => reqs.push(r),
                        _ => break,
                    }
                }
                // Build batch tensor
                let batch = reqs.len();
                let mut flat: Vec<f32> = Vec::with_capacity(batch * dim);
                let mut valid_idx: Vec<usize> = Vec::with_capacity(batch);
                for (i,r) in reqs.iter().enumerate() { if r.input.len()==dim { flat.extend_from_slice(&r.input); valid_idx.push(i); } else { let _ = r.resp.send(Ok(None)); } }
                if flat.is_empty() {
                    continue;
                }
                let run_res: Result<Vec<Option<MLResult>>> = (|| {
                    let output = model.run_batch(&flat, batch, dim)?;
                    Ok(output)
                })();
                match run_res {
                    Ok(results) => {
                        // Map back results
                        let mut iter = results.into_iter();
                        for (i, r) in reqs.into_iter().enumerate() {
                            if !valid_idx.contains(&i) { continue; }
                            let _ = r.resp.send(Ok(iter.next().unwrap_or(None)));
                        }
                    }
                    Err(e) => {
                        for r in reqs { let _ = r.resp.send(Err(anyhow!("batch inference failed: {e}"))); }
                    }
                }
            }
        });
        Self { tx, dim }
    }

    async fn infer(&self, input: Vec<f32>) -> Result<Option<MLResult>> {
        if input.len() != self.dim { return Ok(None); }
        let (resp_tx, resp_rx) = oneshot::channel();
        let req = BatchReq { input, resp: resp_tx };
        if self.tx.send(req).await.is_err() { return Err(anyhow!("batcher channel closed")); }
        match resp_rx.await { Ok(r) => r, Err(_) => Err(anyhow!("batcher worker dropped")) }
    }
}

#[cfg(feature = "onnx")]
#[derive(Clone)]
struct OnnxModelHandle {
    inner: ArcSwap<Arc<ModelInner>>,
    timeout_ms: u64,
}

#[cfg(feature = "onnx")]
impl OnnxModelHandle {
    fn run_batch(&self, flat: &[f32], batch: usize, dim: usize) -> Result<Vec<Option<MLResult>>> {
        let inner = self.inner.load();
        let start = Instant::now();
        let tensor = Tensor::from_shape(&[batch as i64, dim as i64], flat)?;
        let outputs = inner.model.run(tvec!(tensor))?;
        let elapsed = start.elapsed().as_millis() as u64;
        if elapsed > self.timeout_ms { return Ok(vec![None; batch]); }
        let tensor = &outputs[0];
        let arr = tensor.to_array_view::<f32>()?; // shape [batch, classes]
        let shape = arr.shape().to_vec();
        let (b, classes) = if shape.len()==2 { (shape[0], shape[1]) } else { (1usize, arr.len()) };
        let slice: Vec<f32> = arr.iter().copied().collect();
        let mut out = Vec::with_capacity(b);
        for row in 0..b {
            let offset = row * classes;
            if offset + classes > slice.len() { out.push(None); continue; }
            let mut probs = slice[offset..offset+classes].to_vec();
            softmax(&mut probs);
            if let Some((idx, conf)) = probs.iter().enumerate().max_by(|a,b| a.1.partial_cmp(b.1).unwrap()) { 
                let mut topk: Vec<(usize,f32)> = probs.iter().enumerate().collect();
                topk.sort_by(|a,b| b.1.partial_cmp(a.1).unwrap());
                topk.truncate(5);
                out.push(Some(MLResult { class_id: idx, confidence: *conf, topk }));
            } else { out.push(None); }
        }
        Ok(out)
    }
}

impl OnnxModel {
    pub fn load_env() -> Result<Self> {
        let path = std::env::var("SWARM__DETECTION__ML__MODEL_PATH").unwrap_or_else(|_| "models/model.onnx".into());
        let timeout_ms = std::env::var("SWARM__DETECTION__ML__TIMEOUT_MS").ok().and_then(|v| v.parse().ok()).unwrap_or(40);
        let batch_enabled = std::env::var("SWARM__DETECTION__ML__BATCH").map(|v| v=="1"|| v.eq_ignore_ascii_case("true")).unwrap_or(false);
        let batch_window_ms = std::env::var("SWARM__DETECTION__ML__BATCH_WINDOW_MS").ok().and_then(|v| v.parse().ok()).unwrap_or(5u64);
        let max_batch = std::env::var("SWARM__DETECTION__ML__MAX_BATCH").ok().and_then(|v| v.parse().ok()).unwrap_or(16usize);
        Self::load(Path::new(&path), timeout_ms).map(|mut m| { #[cfg(feature="onnx")] if batch_enabled { m.init_batcher(batch_window_ms, max_batch); } m })
    }

    pub fn load(path: &Path, timeout_ms: u64) -> Result<Self> {
        #[cfg(feature = "onnx")] {
            let metadata = std::fs::metadata(path)?;
            let mtime = metadata.modified()?;
            if let Ok(expect) = std::env::var("SWARM__DETECTION__ML__MODEL_SHA256") {
                use sha2::{Sha256, Digest}; use std::io::Read;
                let mut f = std::fs::File::open(path)?; let mut buf = Vec::new(); f.read_to_end(&mut buf)?; let mut h=Sha256::new(); h.update(&buf); let got = format!("{:x}", h.finalize());
                if !expect.is_empty() && !expect.eq_ignore_ascii_case(&got) { return Err(anyhow!("model hash mismatch expected={expect} got={got}")); }
            }
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
            let this = Self { path: path.to_path_buf(), inner: ArcSwap::from(inner), warm: Arc::new(RwLock::new(false)), timeout_ms, batcher: None };
            this.warmup()?;
            Ok(this)
        }
        #[cfg(not(feature = "onnx"))]
        {
            Ok(Self { path: path.to_path_buf(), inner: ArcSwap::from(Arc::new(ModelInner { mtime: SystemTime::now(), input_dim: 256 })), warm: Arc::new(RwLock::new(true)), timeout_ms, })
        }
    }

    #[cfg(feature = "onnx")]
    fn init_batcher(&mut self, window_ms: u64, max_batch: usize) {
        if self.batcher.is_some() { return; }
        let handle = OnnxModelHandle { inner: self.inner.clone(), timeout_ms: self.timeout_ms };
        let batcher = InferenceBatcher::new(self.input_dim(), handle, self.timeout_ms, Duration::from_millis(window_ms), max_batch);
        self.batcher = Some(batcher);
    }

    pub fn reload_if_changed(&self) -> Result<()> {
        #[cfg(feature = "onnx")] {
            let meta = std::fs::metadata(&self.path)?;
            let mtime = meta.modified()?;
            if mtime > self.inner.load().mtime { // changed
                if let Ok(expect) = std::env::var("SWARM__DETECTION__ML__MODEL_SHA256") {
                    use sha2::{Sha256, Digest}; use std::io::Read; let mut f = std::fs::File::open(&self.path)?; let mut buf=Vec::new(); f.read_to_end(&mut buf)?; let mut h=Sha256::new(); h.update(&buf); let got = format!("{:x}", h.finalize()); if !expect.is_empty() && !expect.eq_ignore_ascii_case(&got) { return Err(anyhow!("model reload hash mismatch expected={expect} got={got}")); }
                }
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

    #[cfg(feature = "onnx")]
    pub async fn infer_batched(&self, feats: &[f32]) -> Result<Option<MLResult>> {
        if let Some(b) = &self.batcher { return b.infer(feats.to_vec()).await; }
        // fallback to sync path in blocking
        self.infer(feats)
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
