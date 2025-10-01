# Critical Severity Surge Runbook

## Alert: CriticalSeveritySurge

**Severity:** Critical  
**Oncall Action:** Page  
**Component:** Detection Pipeline

---

## Trigger Conditions

- **Metric:** `rate(swarm_detection_critical_total[5m]) > 10`
- **Duration:** 2 minutes sustained
- **Threshold:** 10 critical detections per second

---

## What This Means

The system is detecting more than 10 critical-severity threats per second over a 5-minute window, sustained for at least 2 minutes. This indicates one of:

1. **Active coordinated attack** - Multiple sources executing similar attack patterns
2. **Zero-day exploit** - New vulnerability being actively exploited
3. **Rule misconfiguration** - Detection rule incorrectly marking benign traffic as critical
4. **Data pipeline issue** - Duplicate events or replay attack

---

## Immediate Actions (First 5 Minutes)

### 1. Verify Alert Legitimacy
```bash
# Check current critical detection rate
curl -s 'http://localhost:9090/api/v1/query?query=rate(swarm_detection_critical_total[5m])' | jq

# Get sample of recent critical detections
tail -n 50 /var/log/swarm/detections.log | jq 'select(.severity=="critical")'
```

### 2. Identify Attack Pattern
```bash
# Extract payload patterns from last 100 critical detections
tail -n 100 /var/log/swarm/detections.log \
  | jq -r 'select(.severity=="critical") | .payload_preview' \
  | sort | uniq -c | sort -rn | head -20
```

### 3. Check Source Distribution
```bash
# Analyze origin IPs (if available in logs)
tail -n 200 /var/log/swarm/detections.log \
  | jq -r 'select(.severity=="critical") | .origin' \
  | sort | uniq -c | sort -rn | head -10
```

---

## Diagnosis Decision Tree

### Pattern A: Single Rule ID Dominating (>80% of detections)
**Likely Cause:** Rule misconfiguration or overfitting  
**Action:**
```bash
# Identify dominant rule
tail -n 100 /var/log/swarm/detections.log \
  | jq -r 'select(.severity=="critical") | .rule_id' \
  | sort | uniq -c | sort -rn | head -5

# Temporarily disable rule (requires restart)
# Edit configs/detection-rules.yaml, comment out rule
# OR set env DETECTION_ENABLED=false for emergency shutdown
```

### Pattern B: Multiple Unique Payloads, Distributed Sources
**Likely Cause:** Legitimate coordinated attack  
**Action:**
- Engage security team immediately
- Preserve forensic evidence (copy logs before rotation)
- Consider upstream rate limiting or IP blocking
- Notify stakeholders via #security-incidents

### Pattern C: Duplicate payload_hash
**Likely Cause:** Event replay or pipeline loop  
**Action:**
```bash
# Check for duplicate hashes
tail -n 200 /var/log/swarm/detections.log \
  | jq -r '.payload_hash' | sort | uniq -c | sort -rn | head -10

# If duplicates found, check NATS consumer state
nats consumer ls ingest.v1.raw --server=nats://localhost:4222
```

---

## Mitigation Steps

### Emergency: Stop Detection Processing
```bash
# Set env var to disable detection
export DETECTION_ENABLED=false
# Restart sensor-gateway
systemctl restart sensor-gateway
# OR docker restart sensor-gateway
```

### Surgical: Disable Specific Rule
Edit `configs/detection-rules.yaml`:
```yaml
# Comment out problematic rule
# - id: R003
#   pattern: MALICIOUS
#   severity: critical
```
Reload rules (hot reload enabled):
```bash
touch configs/detection-rules.yaml  # Trigger file watcher
```

### Network-Level: Rate Limit at Edge
```bash
# If using nginx/haproxy
# Add rate limit directive to upstream config
limit_req_zone $binary_remote_addr zone=attack:10m rate=100r/s;
```

---

## Recovery Verification

After mitigation, verify metrics return to baseline:

```bash
# Check detection rate drops below threshold
watch -n 5 'curl -s "http://localhost:9090/api/v1/query?query=rate(swarm_detection_critical_total[5m])" | jq ".data.result[0].value[1]"'

# Confirm alert resolves in Alertmanager
curl -s http://localhost:9093/api/v2/alerts | jq '.[] | select(.labels.alertname=="CriticalSeveritySurge")'
```

Expected recovery time: **<5 minutes** after mitigation applied.

---

## Post-Incident

### 1. Generate Incident Report
```bash
# Capture metrics snapshot for time window
scripts/generate_incident_report.sh \
  --start "2025-10-01T20:00:00Z" \
  --end "2025-10-01T21:00:00Z" \
  --alert CriticalSeveritySurge \
  --output reports/incident-$(date +%Y%m%d-%H%M).md
```

### 2. Update Detection Rules
- Review false positive payloads
- Refine regex patterns to reduce overfitting
- Consider adding exclusion patterns for known benign traffic

### 3. Tune Alert Threshold (if needed)
If alert fires frequently with legitimate high traffic:
```yaml
# In infra/alert-rules.yml
- alert: CriticalSeveritySurge
  expr: rate(swarm_detection_critical_total[5m]) > 20  # Increase threshold
  for: 5m  # Increase duration to reduce flapping
```

### 4. Retrospective
- Schedule blameless postmortem within 48 hours
- Document root cause and preventive measures
- Update this runbook with lessons learned

---

## Escalation Path

1. **Oncall Engineer** (immediate - via PagerDuty)
2. **Security Lead** (if attack confirmed - #security-incidents)
3. **Platform Lead** (if infrastructure issue - #platform-oncall)
4. **VP Engineering** (if >30min MTTR or customer impact)

---

## Related Alerts

- `HighSeveritySpike` - Leading indicator, may fire 5-10 min before this alert
- `ExcessiveViewChanges` - May indicate DDoS affecting consensus
- `HighFalsePositiveRatio` - May indicate rule quality degradation

---

## Reference Links

- [Detection Rules Repository](../configs/detection-rules.yaml)
- [Grafana Detection Dashboard](http://localhost:3000/d/detection-metrics)
- [Prometheus Alert Rules](../infra/alert-rules.yml)
- [Security Incident Playbook](https://wiki.internal/security/incident-response)

---

**Last Updated:** 2025-10-01  
**Runbook Owner:** Security Operations Team  
**Review Cadence:** Quarterly or after each incident
