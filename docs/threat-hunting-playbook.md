# Threat Hunting Playbook - Swarm Intelligence Network

## Overview
This playbook provides structured procedures for proactive threat hunting using the Swarm Intelligence Network's Security & Intelligence Layer.

---

## 🎯 Hunting Methodology

### 1. Hypothesis-Driven Hunting
**Goal**: Test specific threat scenarios based on intelligence

**Process**:
1. **Formulate Hypothesis**
   - Example: "APT group X is targeting our network using technique Y"
   - Based on: Recent threat intel, industry trends, historical attacks

2. **Identify Indicators**
   - Use MITRE ATT&CK framework
   - Query threat-intel service for related IoCs
   - Review signature-engine rules matching hypothesis

3. **Search for Evidence**
   ```bash
   # Query threat intel for specific technique
   curl -X GET "http://threat-intel:8080/api/v1/indicators?type=technique&value=T1566.001"
   
   # Search audit logs for suspicious actions
   curl -X POST "http://audit-trail:8080/api/v1/query" \
     -H "Content-Type: application/json" \
     -d '{"action": "exec", "start_time": "2025-10-01T00:00:00Z"}'
   ```

4. **Analyze Results**
   - Correlate findings across services
   - Calculate risk scores
   - Determine true positive vs false positive

5. **Document & Remediate**
   - Log findings in audit trail
   - Update detection rules if needed
   - Escalate confirmed threats

---

## 🔍 Common Hunting Scenarios

### Scenario 1: Ransomware Indicators

**Objective**: Detect ransomware before encryption phase

**Indicators to Hunt**:
- File extension changes (.encrypted, .locked)
- High volume of file modifications
- Suspicious process names (cryptolocker, ryuk)
- Network traffic to known ransomware C2 servers

**Hunting Steps**:

1. **Check Threat Intelligence Feeds**
   ```bash
   # Query for known ransomware hashes
   curl "http://threat-intel:8080/api/v1/indicators?type=hash&source=virustotal&severity=critical"
   ```

2. **Scan for File System Anomalies**
   ```bash
   # Use anomaly-detection to find unusual file activity
   curl -X POST "http://anomaly-detection:8000/v1/predict" \
     -H "Content-Type: application/json" \
     -d '{
       "samples": [
         {"file_mods_per_sec": 500, "unique_extensions": 25, "network_io_mb": 10}
       ],
       "return_scores": true
     }'
   ```

3. **Search Audit Trail for Suspicious Commands**
   ```bash
   # Look for vssadmin (shadow copy deletion)
   curl -X POST "http://audit-trail:8080/api/v1/query" \
     -d '{"action": "exec", "resource": "vssadmin"}'
   ```

4. **Signature Scanning**
   ```bash
   # Scan suspicious files with signature engine
   curl -X POST "http://signature-engine:8081/scan" \
     -F "file=@suspicious.exe"
   ```

**Indicators of Compromise (IoCs)**:
- `Score > 8.0` from anomaly detector
- Match on ransomware signatures
- Presence in threat intel with `severity=critical`

---

### Scenario 2: Lateral Movement Detection

**Objective**: Identify attackers moving between systems

**Indicators**:
- Multiple failed login attempts
- Unusual remote access (RDP, SSH, PSExec)
- Credential dumping tools (Mimikatz)
- Service account abuse

**Hunting Steps**:

1. **Analyze Authentication Logs**
   ```bash
   # Find multiple failed logins from same source
   curl -X POST "http://audit-trail:8080/api/v1/query" \
     -d '{
       "action": "auth_failed",
       "start_time": "2025-10-02T00:00:00Z",
       "limit": 100
     }' | jq 'group_by(.actor) | map(select(length > 5))'
   ```

2. **Check for Known Lateral Movement Tools**
   ```bash
   # Query threat intel for lateral movement techniques
   curl "http://threat-intel:8080/api/v1/indicators?type=technique&value=T1021"
   ```

3. **Detect Anomalous Network Connections**
   ```bash
   # High number of internal connections from single host
   curl -X POST "http://anomaly-detection:8000/v1/predict" \
     -d '{
       "samples": [
         {"internal_connections": 500, "unique_targets": 50, "port_scan_score": 0.9}
       ]
     }'
   ```

**Response Actions**:
- Isolate compromised hosts
- Reset credentials
- Review session logs for exfiltration

---

### Scenario 3: Data Exfiltration

**Objective**: Detect unauthorized data transfers

**Indicators**:
- Unusual outbound traffic volume
- Connections to known bad IPs/domains
- Large file uploads to cloud services
- DNS tunneling patterns

**Hunting Steps**:

1. **Traffic Volume Analysis**
   ```bash
   # Find hosts with high egress traffic
   curl -X POST "http://anomaly-detection:8000/v1/predict" \
     -d '{
       "samples": [
         {"bytes_out": 10000000000, "upload_sessions": 50, "avg_packet_size": 1400}
       ]
     }'
   ```

2. **Check Destination Reputation**
   ```bash
   # Query threat intel for destination IPs
   curl "http://threat-intel:8080/api/v1/indicators?type=ip&value=203.0.113.42"
   ```

3. **DNS Query Analysis**
   ```bash
   # Look for unusually long DNS queries (tunneling)
   curl -X POST "http://signature-engine:8081/scan" \
     -d '{"data": "aaaaaaaaaaaaaaaa.bbbbbbbbbbbbbbb.evil.com"}'
   ```

---

## 📊 Threat Hunting Metrics

Track hunting effectiveness:

| Metric | Target | Current |
|--------|--------|---------|
| Hunts per week | 5 | - |
| True positives found | > 20% | - |
| Mean time to detect (MTTD) | < 2 hours | - |
| Mean time to respond (MTTR) | < 1 hour | - |
| False positive rate | < 5% | - |

---

## 🛠️ Tools & Queries

### Useful API Endpoints

**Threat Intelligence**:
```bash
# Get all critical threats
GET /api/v1/threats?severity=critical

# Correlate indicator
POST /api/v1/correlate
Body: {"type": "hash", "value": "<SHA256>"}

# Get threat score
GET /api/v1/score?indicator=<value>
```

**Anomaly Detection**:
```bash
# Batch prediction
POST /v1/predict
Body: {"samples": [...]}

# Model info
GET /v1/model

# Training (admin only)
POST /v1/train
Body: {"samples": [...], "contamination": 0.05}
```

**Signature Engine**:
```bash
# Scan data
POST /scan
Body: multipart/form-data

# Hot reload rules
POST /reload

# Rule stats
GET /stats
```

**Audit Trail**:
```bash
# Query logs
POST /api/v1/query
Body: {"actor": "...", "action": "...", "start_time": "..."}

# Verify integrity
GET /api/v1/verify

# Compliance check
POST /api/v1/compliance
Body: {"policy": "gdpr", "entries": [...]}
```

---

## 🚨 Alerting & Escalation

### Alert Severity Levels

1. **Info** (Score 0-2): Log only
2. **Low** (Score 2-4): Team notification
3. **Medium** (Score 4-6): Analyst review required
4. **High** (Score 6-8): Immediate investigation
5. **Critical** (Score 8-10): Emergency response, executive notification

### Escalation Matrix

| Severity | Response Time | Escalation |
|----------|--------------|------------|
| Critical | 15 minutes | SOC Lead → CISO → CEO |
| High | 1 hour | SOC Analyst → SOC Lead |
| Medium | 4 hours | SOC Analyst |
| Low | 24 hours | Automated response |
| Info | N/A | Logged only |

---

## 📚 Additional Resources

- **MITRE ATT&CK**: https://attack.mitre.org/
- **AlienVault OTX**: https://otx.alienvault.com/
- **VirusTotal**: https://www.virustotal.com/
- **Swarm Internal Wiki**: http://wiki.swarm.local/threat-hunting

---

## 🔄 Continuous Improvement

**Weekly Review**:
- Review all hunts conducted
- Calculate metrics
- Update playbook with new techniques
- Train team on findings

**Monthly**:
- Benchmark against industry standards
- Update threat models
- Review and tune detection rules
- Conduct tabletop exercises

**Quarterly**:
- External threat intelligence integration
- Red team exercises
- Tool evaluation and upgrades
- Compliance audits

---

**Document Version**: 1.0  
**Last Updated**: 2025-10-03  
**Owner**: Security & Intelligence Team (Nhân viên B)  
**Review Frequency**: Monthly
