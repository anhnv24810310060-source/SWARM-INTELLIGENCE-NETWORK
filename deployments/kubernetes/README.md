# Kubernetes Manifests for SwarmGuard Intelligence Network

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        Ingress (NGINX)                            │
│                     TLS Termination + L7 LB                       │
└───────────────────┬─────────────────────────────────────────────┘
                    │
    ┌───────────────┼──────────────────┐
    │               │                  │
┌───▼────┐    ┌────▼─────┐    ┌──────▼──────┐
│  API   │    │ Control  │    │  Web Portal │
│Gateway │    │  Plane   │    │   (Next.js) │
└───┬────┘    └────┬─────┘    └──────┬──────┘
    │              │                  │
    ├──────────────┼──────────────────┤
    │              │                  │
┌───▼─────────┐ ┌──▼────────┐  ┌────▼─────┐
│ Orchestrator│ │  Policy   │  │ Billing  │
└─────────────┘ └───────────┘  └──────────┘
       │              │              │
       └──────────────┼──────────────┘
                      │
              ┌───────▼────────┐
              │   Federation   │
              │  (CRDT Sync)   │
              └────────────────┘
```

## Deployment Structure

```
deployments/kubernetes/
├── namespace.yaml              # Namespace isolation
├── configmaps/                 # Configuration
│   ├── api-gateway-config.yaml
│   ├── policy-rules.yaml
│   └── observability.yaml
├── secrets/                    # Sensitive data
│   ├── tls-certs.yaml
│   └── jwt-keys.yaml
├── services/                   # Service definitions
│   ├── api-gateway.yaml
│   ├── orchestrator.yaml
│   ├── policy-service.yaml
│   ├── federation.yaml
│   └── billing-service.yaml
├── deployments/                # Workload definitions
│   ├── api-gateway.yaml
│   ├── orchestrator.yaml
│   ├── policy-service.yaml
│   ├── federation.yaml
│   └── billing-service.yaml
├── ingress.yaml                # External access
├── hpa.yaml                    # Horizontal Pod Autoscaler
├── pdb.yaml                    # Pod Disruption Budget
├── network-policies.yaml       # Network segmentation
├── service-mesh/               # Istio/Linkerd configs
│   ├── virtual-services.yaml
│   ├── destination-rules.yaml
│   └── fault-injection.yaml
└── monitoring/                 # Observability stack
    ├── prometheus.yaml
    ├── grafana.yaml
    ├── dashboards/
    └── alerts/
```

## Quick Start

### Prerequisites
- Kubernetes 1.28+
- kubectl configured
- Helm 3.x (for monitoring stack)

### Deploy Core Services

```bash
# Create namespace
kubectl apply -f namespace.yaml

# Deploy ConfigMaps and Secrets
kubectl apply -f configmaps/
kubectl apply -f secrets/

# Deploy Services
kubectl apply -f deployments/
kubectl apply -f services/

# Setup Ingress
kubectl apply -f ingress.yaml

# Configure Autoscaling
kubectl apply -f hpa.yaml
kubectl apply -f pdb.yaml

# Apply Network Policies
kubectl apply -f network-policies.yaml
```

### Deploy Monitoring Stack

```bash
# Install Prometheus Operator
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm install prometheus prometheus-community/kube-prometheus-stack \
  --namespace swarm-system \
  --values monitoring/prometheus-values.yaml

# Deploy Grafana Dashboards
kubectl apply -f monitoring/dashboards/
```

## Resource Requirements

### Production Sizing

| Service | CPU Request | CPU Limit | Memory Request | Memory Limit | Replicas |
|---------|-------------|-----------|----------------|--------------|----------|
| api-gateway | 500m | 2000m | 512Mi | 2Gi | 3-10 (HPA) |
| orchestrator | 250m | 1000m | 256Mi | 1Gi | 2-5 (HPA) |
| policy-service | 100m | 500m | 128Mi | 512Mi | 2-4 (HPA) |
| federation | 200m | 1000m | 256Mi | 1Gi | 3 (StatefulSet) |
| billing-service | 100m | 500m | 128Mi | 512Mi | 2 |

### Storage

- Federation: 10Gi SSD (RWO) per pod
- Billing: 5Gi SSD (RWO) per pod
- Prometheus: 100Gi SSD (RWO)
- Grafana: 10Gi SSD (RWO)

## High Availability

### Pod Disruption Budgets
- API Gateway: minAvailable=2
- Orchestrator: minAvailable=1
- Federation: maxUnavailable=1

### Anti-Affinity Rules
Services spread across nodes and availability zones for fault tolerance.

## Security

### Network Policies
- Default deny-all ingress
- Allow only necessary service-to-service communication
- External access only through API Gateway

### Pod Security
- Run as non-root user (UID 65532)
- Read-only root filesystem
- Drop all capabilities
- Security context enforced

### mTLS
Service mesh provides automatic mTLS between services.

## Monitoring

### Key Metrics
- Request rate, latency (P50, P95, P99)
- Error rates (4xx, 5xx)
- Resource utilization (CPU, Memory)
- Circuit breaker states
- Federation sync lag

### Alerts
- High error rate (>5% 5xx)
- High latency (P99 > 1s)
- Pod crashes
- Resource saturation
- Circuit breakers open

## Scaling

### Horizontal Pod Autoscaler
- API Gateway: Scale on CPU >70% or custom metrics (RPS)
- Orchestrator: Scale on workflow queue depth
- Policy Service: Scale on evaluation rate

### Vertical Pod Autoscaler (Optional)
Automatically adjust resource requests/limits based on usage.

## Disaster Recovery

### Backup Strategy
- Federation state: Daily snapshots to S3-compatible storage
- Billing data: Hourly backups with 30-day retention
- Config: Git as source of truth

### Recovery Time Objectives
- RTO: 15 minutes (critical services)
- RPO: 1 hour (data loss tolerance)

## Troubleshooting

### Common Issues

1. **Pod CrashLoopBackOff**
   ```bash
   kubectl logs -n swarm-system <pod-name>
   kubectl describe pod -n swarm-system <pod-name>
   ```

2. **Service Discovery Issues**
   ```bash
   kubectl get svc -n swarm-system
   kubectl get endpoints -n swarm-system <service-name>
   ```

3. **Network Policy Blocking Traffic**
   ```bash
   kubectl get networkpolicies -n swarm-system
   kubectl describe networkpolicy -n swarm-system <policy-name>
   ```

## Performance Tuning

### API Gateway
- Increase connection pool size
- Tune rate limiter thresholds
- Enable HTTP/2 and keep-alive

### Federation
- Adjust sync interval based on load
- Tune CRDT merge frequency
- Enable compression for sync messages

### Policy Service
- Increase decision cache size
- Pre-compile frequently used policies
- Enable policy bundling

## Cost Optimization

### Resource Right-Sizing
Monitor actual usage and adjust requests/limits accordingly.

### Cluster Autoscaler
Automatically add/remove nodes based on pending pods.

### Spot Instances
Use spot/preemptible instances for non-critical workloads.

## Compliance

### Audit Logging
All API access logged with request ID for traceability.

### Data Residency
Deploy in specific regions to comply with data sovereignty requirements.

### Encryption
- Data at rest: Encrypted volumes
- Data in transit: mTLS between services
- Secrets: Encrypted in etcd

## Contact

For deployment support, contact DevOps team.
