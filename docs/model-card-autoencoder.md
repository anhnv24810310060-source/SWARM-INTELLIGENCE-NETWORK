# Model Card: Swarm Anomaly Detection Autoencoder

## Model Details

**Model Name**: Swarm Autoencoder v1.0  
**Model Type**: Deep Autoencoder for Anomaly Detection  
**Framework**: PyTorch → ONNX  
**Owner**: Swarm Security & Intelligence Team  
**Last Updated**: 2025-10-03  
**License**: Internal Use Only

---

## Intended Use

### Primary Use Cases
- Real-time network traffic anomaly detection
- Behavioral analysis of system events
- Zero-day threat detection
- Deviation from normal operational patterns

### Out-of-Scope Use Cases
- Financial fraud detection (different feature space)
- Medical diagnosis (not trained on healthcare data)
- Natural language processing tasks

---

## Architecture

### Model Structure
```
Input Layer: Dynamic (50-200 features)
  ↓
Encoder:
  - Dense (128 units) + ReLU + BatchNorm + Dropout(0.2)
  - Dense (64 units) + ReLU + BatchNorm
  - Dense (32 units) - Latent Space
  ↓
Decoder:
  - Dense (64 units) + ReLU + BatchNorm
  - Dense (128 units) + ReLU + BatchNorm + Dropout(0.2)
  - Dense (output_dim) - Reconstruction
```

### Training Details
- **Loss Function**: Mean Squared Error (MSE)
- **Optimizer**: Adam (lr=0.001)
- **Batch Size**: 256
- **Epochs**: 50 (early stopping enabled)
- **Regularization**: L2, Dropout
- **Hardware**: NVIDIA A100 GPU / CPU fallback

### Feature Engineering
**Network Traffic Features** (example: 50 features):
1. `bytes_in`: Inbound traffic volume
2. `bytes_out`: Outbound traffic volume  
3. `packets_in`: Inbound packet count
4. `packets_out`: Outbound packet count
5. `duration_ms`: Connection duration
6. `connection_rate`: Connections per second
7. `error_rate`: Packet error ratio
8. `latency_p95`: 95th percentile latency
9. `retransmit_rate`: TCP retransmission ratio
10. `syn_flood_score`: SYN flood detection score
... (40 more features)

**Normalization**: Min-Max scaling (0-1 range)

---

## Training Data

### Dataset Characteristics
- **Size**: 10,000+ samples (95% normal, 5% anomalies)
- **Source**: Production network telemetry (anonymized)
- **Time Period**: Last 90 days of traffic
- **Labeling**: Semi-supervised (known attacks + normal baseline)

### Data Collection
- Collected from edge sensors across 50+ geographic locations
- Aggregated at 1-minute intervals
- PII redacted before training
- Balanced across different network segments (DMZ, internal, external)

### Known Limitations
- **Geographic Bias**: Primarily North American traffic
- **Protocol Coverage**: Heavy on HTTP/HTTPS, limited on exotic protocols
- **Temporal Bias**: Training data from Oct 2024 - Dec 2024 (winter traffic patterns)

---

## Performance Metrics

### Evaluation Results (Test Set: 2000 samples)

| Metric | Score |
|--------|-------|
| **Accuracy** | 96.5% |
| **Precision** | 94.2% |
| **Recall** | 93.8% |
| **F1 Score** | 94.0% |
| **AUC-ROC** | 0.982 |
| **False Positive Rate** | 5.8% |
| **False Negative Rate** | 6.2% |

### Threshold Tuning
- **Default Threshold**: 0.8 (95th percentile of reconstruction error)
- **High Sensitivity**: 0.7 (more false positives, fewer false negatives)
- **High Specificity**: 0.9 (fewer false positives, more false negatives)

### Inference Performance
- **Latency (CPU)**: ~15ms per batch (128 samples)
- **Latency (GPU)**: ~3ms per batch (128 samples)
- **Throughput**: ~8,500 samples/sec (GPU)
- **Memory Usage**: 150MB (model + runtime)

---

## Ethical Considerations

### Privacy
- **PII Handling**: All training data anonymized, no personal identifiers
- **Data Retention**: Training data deleted after 90 days
- **GDPR Compliance**: No data exported outside designated regions

### Fairness
- **Bias Testing**: Evaluated across different network segments, no significant bias detected
- **Demographic Impact**: N/A (network traffic, not individual users)

### Security
- **Model Poisoning**: Training pipeline includes data validation and anomaly checks
- **Adversarial Robustness**: Limited testing conducted (future work)
- **Model Access**: Restricted to authorized services only

---

## Limitations & Risks

### Known Limitations
1. **Concept Drift**: Model degrades over time as attack patterns evolve
   - **Mitigation**: Daily retraining with recent data
   
2. **Novel Attacks**: May miss zero-day exploits with no similar training examples
   - **Mitigation**: Ensemble with signature-based detection
   
3. **High False Positive Rate**: ~5-8% in diverse network environments
   - **Mitigation**: Tunable threshold, analyst review for high-risk alerts
   
4. **Feature Dependence**: Requires consistent feature extraction pipeline
   - **Mitigation**: Strict schema validation at inference time

### Risk Assessment

| Risk | Severity | Likelihood | Mitigation |
|------|----------|------------|------------|
| False negatives (missed attacks) | High | Medium | Ensemble detection, human review |
| False positives (alert fatigue) | Medium | High | Threshold tuning, feedback loop |
| Model drift (degraded accuracy) | Medium | High | Automated retraining, monitoring |
| Adversarial evasion | High | Low | Adversarial training (roadmap) |
| Privacy breach | Critical | Very Low | Encryption, access control, audit logs |

---

## Maintenance & Monitoring

### Retraining Schedule
- **Daily**: Incremental update with last 24h data
- **Weekly**: Full retraining with last 7 days
- **Monthly**: Model architecture review and hyperparameter tuning

### Performance Monitoring
Track in production:
- `swarm_ml_inference_latency_ms` (target: p99 < 50ms)
- `swarm_anomaly_score_percentile` (distribution shifts)
- `swarm_ml_accuracy_weekly` (degradation detection)
- `swarm_false_positive_rate_daily` (alert quality)

### A/B Testing
- New model versions deployed to 10% traffic initially
- Gradual rollout over 7 days if metrics improve
- Rollback if accuracy drops > 2% or latency increases > 20%

---

## Model Versioning

| Version | Release Date | Changes | Accuracy | Notes |
|---------|--------------|---------|----------|-------|
| v1.0 | 2025-10-03 | Initial production release | 96.5% | Baseline model |
| v1.1 | TBD | Add 20 new features | TBD | Planned |
| v2.0 | TBD | Switch to Transformer architecture | TBD | Research phase |

---

## Contact & Support

**Model Owner**: security-ml-team@swarm.local  
**On-Call Support**: #swarm-security Slack channel  
**Bug Reports**: https://jira.swarm.local/SECURITY-ML  
**Model Registry**: http://model-registry.swarm.local/anomaly-detection

---

## References

1. Chandola, V., Banerjee, A., & Kumar, V. (2009). "Anomaly Detection: A Survey"
2. Goodfellow, I., et al. (2016). "Deep Learning" - Autoencoder chapter
3. MITRE ATT&CK Framework: https://attack.mitre.org/
4. ONNX Runtime Documentation: https://onnxruntime.ai/

---

## Changelog

**v1.0 - 2025-10-03**
- Initial model card documentation
- Production deployment to 5 edge clusters
- Baseline performance metrics established

---

**Document Version**: 1.0  
**Classification**: Internal  
**Review Frequency**: Quarterly  
**Next Review**: 2026-01-03
