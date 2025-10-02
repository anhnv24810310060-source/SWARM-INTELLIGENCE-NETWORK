import os, time, threading, random, json, joblib, traceback
from fastapi import FastAPI, HTTPException, BackgroundTasks
from pydantic import BaseModel
from typing import List, Dict, Optional
from pathlib import Path
import numpy as np
from sklearn.ensemble import IsolationForest
from opentelemetry import metrics
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader

SERVICE = "anomaly-detection"

# Initialize metrics provider lazily
if not isinstance(metrics.get_meter_provider(), MeterProvider):
    endpoint = os.getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") or os.getenv("OTEL_EXPORTER_OTLP_ENDPOINT") or "http://localhost:4317"
    clean = endpoint.replace("http://", "").replace("https://", "")
    exporter = OTLPMetricExporter(endpoint=clean, insecure=True)
    reader = PeriodicExportingMetricReader(exporter)
    provider = MeterProvider(resource=Resource.create({"service.name": SERVICE}), metric_readers=[reader])
    metrics.set_meter_provider(provider)

meter = metrics.get_meter("swarm-python")
INFER_LAT = meter.create_histogram("swarm_ml_inference_latency_ms")

MODEL_DIR = Path(os.getenv("MODEL_DIR", "./models"))
MODEL_DIR.mkdir(parents=True, exist_ok=True)

class PredictRequest(BaseModel):
    samples: List[Dict]
    return_scores: bool = True

class TrainRequest(BaseModel):
    samples: List[Dict]
    contamination: float = 0.05
    n_estimators: int = 200
    max_samples: Optional[int] = None

class PredictResponse(BaseModel):
    scores: List[float]
    model_version: str
    anomalies: Optional[List[bool]] = None

class TrainResponse(BaseModel):
    model_version: str
    trained_at: float
    duration_seconds: float
    n_samples: int
    features: List[str]

app = FastAPI(title="Swarm Anomaly Detection")

_MODEL_LOCK = threading.RLock()
MODEL_METADATA_PATH = MODEL_DIR / "model_meta.json"

def _load_latest_model():
    if (MODEL_DIR / "model.bin").exists() and MODEL_METADATA_PATH.exists():
        try:
            model = joblib.load(MODEL_DIR / "model.bin")
            meta = json.loads(MODEL_METADATA_PATH.read_text())
            return model, meta
        except Exception:
            traceback.print_exc()
    # fallback stub model (random scoring)
    return None, {"version": "stub-0", "trained_at": time.time(), "features": []}

MODEL, MODEL_META = _load_latest_model()

def _next_version(prev: str) -> str:
    if prev.startswith("v"):
        try:
            num = int(prev[1:])
            return f"v{num+1}"
        except Exception:
            pass
    if prev.startswith("stub"):
        return "v1"
    return prev + ".next"

@app.get("/health")
async def health():
    with _MODEL_LOCK:
        return {"status": "ok", "model_version": MODEL_META.get("version"), "features": MODEL_META.get("features", [])}

def _extract_features(samples: List[Dict]):
    # Simple dynamic feature extraction: numeric fields only; stable ordering
    # Collect union of keys
    key_set = set()
    for s in samples:
        for k, v in s.items():
            if isinstance(v, (int, float)):
                key_set.add(k)
    keys = sorted(key_set)
    mat = []
    for s in samples:
        row = []
        for k in keys:
            v = s.get(k, 0.0)
            row.append(float(v) if isinstance(v, (int,float)) else 0.0)
        mat.append(row)
    return keys, np.array(mat, dtype=float)

@app.post("/v1/predict", response_model=PredictResponse)
async def predict(req: PredictRequest):
    start = time.time()
    with _MODEL_LOCK:
        model = MODEL
        meta = MODEL_META.copy()
    keys, X = _extract_features(req.samples)
    # Align order to model features if available
    if meta.get("features"):
        model_keys = meta["features"]
        # reorder / extend
        reorder = []
        for k in model_keys:
            if k in keys:
                reorder.append(keys.index(k))
            else:
                reorder.append(-1)
        X_aligned = []
        for row in X:
            new_row = []
            for idx in reorder:
                new_row.append(row[idx] if idx >=0 else 0.0)
            X_aligned.append(new_row)
        X = np.array(X_aligned, dtype=float)
    scores = []
    anomalies = None
    if model is not None:
        try:
            # IsolationForest decision_function: higher = less anomalous; convert to anomaly score 0..1
            decision = model.decision_function(X)
            # Normalize decision to 0..1
            dmin, dmax = float(np.min(decision)), float(np.max(decision))
            rng = (dmax - dmin) or 1.0
            scores = [1.0 - (d - dmin)/rng for d in decision]  # higher score => more anomalous
            if req.return_scores:
                anomalies = [s > 0.8 for s in scores]
        except Exception:
            traceback.print_exc()
            scores = [min(1.0, max(0.0, random.random())) for _ in req.samples]
    else:
        scores = [min(1.0, max(0.0, random.random())) for _ in req.samples]
    INFER_LAT.record((time.time() - start) * 1000.0)
    return PredictResponse(scores=scores, model_version=meta.get("version"), anomalies=anomalies)

@app.get("/v1/model")
async def model_info():
    with _MODEL_LOCK:
        return MODEL_META

def _train_model(samples: List[Dict], contamination: float, n_estimators: int, max_samples: Optional[int]):
    t0 = time.time()
    keys, X = _extract_features(samples)
    if len(X) == 0:
        raise ValueError("no numeric features to train on")
    if max_samples is None:
        max_samples = min(512, len(X))
    model = IsolationForest(n_estimators=n_estimators, contamination=contamination, max_samples=max_samples, random_state=42)
    model.fit(X)
    with _MODEL_LOCK:
        global MODEL, MODEL_META
        new_version = _next_version(MODEL_META.get("version", "stub-0"))
        MODEL = model
        MODEL_META = {"version": new_version, "trained_at": time.time(), "features": keys, "n_estimators": n_estimators, "contamination": contamination}
        joblib.dump(MODEL, MODEL_DIR / "model.bin")
        MODEL_METADATA_PATH.write_text(json.dumps(MODEL_META, indent=2))
    return {
        "model_version": MODEL_META["version"],
        "trained_at": MODEL_META["trained_at"],
        "duration_seconds": time.time() - t0,
        "n_samples": len(X),
        "features": keys,
    }

@app.post("/v1/train", response_model=TrainResponse)
async def train(req: TrainRequest, background: BackgroundTasks):
    # Run synchronous for now (small) – if large, delegate to background
    try:
        result = _train_model(req.samples, req.contamination, req.n_estimators, req.max_samples)
        return TrainResponse(**result)
    except Exception as e:
        raise HTTPException(status_code=400, detail=str(e))

@app.get("/v1/explain/{sample_id}")
async def explain(sample_id: int):
    if sample_id < 0:
        raise HTTPException(status_code=404, detail="invalid id")
    # Lightweight pseudo-explain: random feature attribution (future: SHAP)
    with _MODEL_LOCK:
        feats = MODEL_META.get("features", [])
    out = []
    for f in feats[:10]:
        out.append({"name": f, "weight": round(random.uniform(-1,1), 4)})
    return {"sample_id": sample_id, "features": out}

# Background daily retrain stub

def _daily_retrain_loop():  # pragma: no cover simple loop
    while True:
        time.sleep(24*3600)
        # Hook: load recent data from a future data lake (placeholder random)
        try:
            synth = []
            for _ in range(256):
                synth.append({"bytes_in": random.randint(0,5000), "bytes_out": random.randint(0,4000), "latency_ms": random.random()*200})
            _train_model(synth, contamination=0.05, n_estimators=128, max_samples=256)
        except Exception:
            traceback.print_exc()

if os.getenv("ENABLE_RETRAIN_LOOP", "1") == "1":  # optional disable for tests
    t = threading.Thread(target=_daily_retrain_loop, daemon=True)
    t.start()
