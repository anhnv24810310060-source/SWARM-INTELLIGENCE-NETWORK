# Runbook: Detection Quality Degradation

## Alert: LowDetectionRate / HighFalsePositiveRate

### Severity
**Critical** - Impacts core security function

### Description
Detection rate has fallen below SLO threshold (98%) or false positive rate has exceeded threshold (2%).

### Impact
- Threats may go undetected (security risk)
- Legitimate traffic may be blocked (availability risk)
- User trust degradation
- SLA breach potential

### Diagnosis

#### 1. Check Current Metrics
```bash
# Detection rate
curl -s http://prometheus:9090/api/v1/query \
  --data-urlencode 'query=swarm_detection_true_positives_total/(swarm_detection_true_positives_total+swarm_detection_false_negatives_total)'

# False positive rate
curl -s http://prometheus:9090/api/v1/query \
  --data-urlencode 'query=swarm_detection_false_positives_total/(swarm_detection_true_positives_total+swarm_detection_false_positives_total)'
```

#### 2. Check Detection Rules Status
```bash
# View current detection rules
kubectl exec -it deployment/sensor-gateway -- cat /app/configs/detection-rules.yaml

# Check rule hot-reload logs
kubectl logs -l app=sensor-gateway --tail=100 | grep "reload"
```

#### 3. Check Model Version
```bash
# Current model version
curl http://model-registry:8000/models/threat-classifier/version

# Model metrics
curl http://inference-gateway:9102/metrics | grep model
```

#### 4. Verify Data Quality
```bash
# Check ingestion errors
kubectl logs -l app=sensor-gateway --tail=100 | grep ERROR

# Check data distribution
curl http://prometheus:9090/api/v1/query \
  --data-urlencode 'query=rate(swarm_ingest_payload_bytes[5m])'
```

### Resolution Steps

#### For Low Detection Rate

**Step 1: Review Recent Changes**
- Check if detection rules were recently updated
- Verify model deployment history
- Review feature engineering pipeline changes

**Step 2: Rule Adjustment**
```bash
# Backup current rules
kubectl cp sensor-gateway-xxx:/app/configs/detection-rules.yaml ./backup-rules.yaml

# Update sensitivity thresholds
vim configs/detection-rules.yaml
# Decrease confidence thresholds for critical patterns

# Hot reload (file watch should trigger automatically, or manually)
kubectl exec -it deployment/sensor-gateway -- kill -HUP 1
```

**Step 3: Model Rollback** (if recent model deployment)
```bash
# List recent model versions
curl http://model-registry:8000/models/threat-classifier/versions

# Rollback to previous version
curl -X POST http://model-registry:8000/models/threat-classifier/rollback \
  -d '{"target_version": "v1.2.3"}'

# Monitor detection rate recovery
watch -n 5 'curl -s http://prometheus:9090/api/v1/query --data-urlencode "query=swarm_detection_rate"'
```

**Step 4: Emergency Override**
```bash
# Enable aggressive detection mode
kubectl set env deployment/sensor-gateway DETECTION_AGGRESSIVE_MODE=true

# This will:
# - Lower confidence thresholds
# - Enable all rule categories
# - Increase anomaly sensitivity
```

#### For High False Positive Rate

**Step 1: Identify FP Sources**
```bash
# Check FP by pattern
kubectl logs -l app=sensor-gateway | grep "false_positive" | \
  jq -r '.pattern' | sort | uniq -c | sort -rn | head -20

# Check FP by source
kubectl logs -l app=sensor-gateway | grep "false_positive" | \
  jq -r '.source' | sort | uniq -c | sort -rn | head -20
```

**Step 2: Tune Rules**
```bash
# Increase confidence thresholds for noisy patterns
vim configs/detection-rules.yaml

# Example:
# - pattern: "*.exe"
#   confidence: 0.85  # Increase from 0.7
#   severity: high
```

**Step 3: Add Whitelists**
```bash
# Add known-good patterns to whitelist
cat >> configs/detection-rules.yaml <<EOF
whitelists:
  - type: domain
    values:
      - legitimate-cdn.com
      - trusted-service.io
  - type: ip
    values:
      - 10.0.0.0/8
      - 172.16.0.0/12
EOF
```

**Step 4: Model Retraining** (if systematic)
```bash
# Mark recent FP for retraining
curl -X POST http://federated-orchestrator:8000/feedback \
  -H "Content-Type: application/json" \
  -d '{
    "event_ids": ["evt-123", "evt-456"],
    "label": "benign",
    "reason": "legitimate_traffic"
  }'

# Trigger federated learning round
curl -X POST http://federated-orchestrator:8000/rounds/start \
  -H "Content-Type: application/json" \
  -d '{
    "round_id": 42,
    "participants": ["node-1", "node-2", "node-3"],
    "min_participants": 3
  }'
```

### Escalation

**When to Escalate:**
- Detection rate < 90% for > 15 minutes
- FP rate > 5% for > 15 minutes
- Multiple resolution attempts failed
- Evidence of adversarial attack

**Escalation Path:**
1. **L2 Security Engineer** (15 min)
2. **ML Engineer** (if model-related, 30 min)
3. **Incident Commander** (if systemic, 45 min)

**Escalation Contact:**
```
Slack: #swarm-oncall
PagerDuty: swarm-security-team
Email: security-oncall@swarmguard.io
```

### Post-Incident

1. **Document Root Cause**
   - What triggered the degradation?
   - Which component failed?
   - Timeline of events

2. **Update Runbook**
   - Add new diagnostic steps
   - Refine resolution procedures
   - Update escalation criteria

3. **Preventive Measures**
   - Add monitoring for identified gap
   - Create automated remediation
   - Schedule model retraining
   - Update detection rules

4. **Post-Mortem** (within 48h)
   - Incident timeline
   - Root cause analysis
   - Action items with owners

### Related Runbooks
- [Critical Severity Surge](./critical-severity-surge.md)
- [Model Deployment Issues](./model-deployment-issues.md)
- [Consensus Failure](./consensus-failure.md)

### Validation
After resolution, verify:
- [ ] Detection rate > 98% for 10 minutes
- [ ] FP rate < 2% for 10 minutes
- [ ] No pending alerts in Prometheus
- [ ] Logs show normal operation
- [ ] SLO dashboard shows green

### Notes
- Always backup before changing rules
- Document all changes in incident ticket
- Monitor for 30 minutes after resolution
- Consider gradual rollout for major changes
