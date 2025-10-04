# Anomaly Detection Service

Purpose: online inference + periodic retraining for network / behavioral anomalies.

Architecture:
- FastAPI HTTP API (inference + explain)
- Async background scheduler (daily retrain stub)
- Model registry local (future: MLflow integration)
- Feature pipeline placeholder (transform raw JSON events → feature vector)

Endpoints:
- GET /health
- POST /v1/predict  {"samples": [...]} returns anomaly scores [0,1]
- GET /v1/explain/{id} returns placeholder SHAP-like explanation

Metrics (OTel):
- swarm_ml_inference_latency_ms (histogram)
- swarm_anomaly_score_percentile (will map to quantile estimator later)

Next Steps:
1. Hook into message bus for streaming scoring
2. Implement IsolationForest (scikit-learn) & AE (PyTorch) selection
3. Add model version header in responses
4. Add p95/p99 latency distribution export
