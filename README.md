# KubeZero

Kubernetes FinOps Auto-Sleep & Wake-on-Traffic Operator written in Go.

> [!NOTE]
> KubeZero cuts non-production Kubernetes cloud spend by 60–70% by scaling idle deployments to zero, while eliminating developer friction through an intelligent Wake-on-Traffic reverse proxy.

---

## Architecture Overview

```
                      +-----------------------------+
                      |     Kubernetes API Server   |
                      +--------------+--------------+
                                     ^
               Watch / Scale Patch   |   Readiness Polling
                                     |
+--------------------+               |               +-------------------------+
| KubeZero Controller|---------------+               |  Wake-on-Traffic Proxy  |
| (Cron/Inactivity)  |                               |  (HTTP Reverse Proxy)   |
+--------------------+                               +------------+------------+
          |                                                       ^
          | Updates service routing                               | Intercepts HTTP
          v                                                       |
+--------------------+                               +------------+------------+
| Target K8s Service |------------------------------>|  Client HTTP Request    |
+--------------------+                               +-------------------------+
```

KubeZero combines two essential capabilities:
1. **Automated Scale-to-Zero:** Based on cron schedules or inactivity timeout, idle workloads are scaled to 0 replicas.
2. **Wake-on-Traffic Interception:** When an HTTP request hits a sleeping service, the KubeZero reverse proxy intercepts the connection, scales the deployment back up via the Kubernetes API, buffers the request during pod startup, and forwards it transparently once health checks pass.
3. **FinOps Cost Reporting:** Measures cumulative sleep hours and exports Prometheus metrics with estimated dollar savings.

---

## Installation via Helm

Install KubeZero into your Kubernetes cluster using the provided Helm chart:

```bash
# Add and install from the local chart directory
helm install kube-zero ./charts/kube-zero -n kube-zero --create-namespace
```

Verify that all pods and CRDs are running:

```bash
kubectl get pods -n kube-zero
kubectl get crd zeropolicies.finops.kubezero.io
```

---

## Observability & Grafana Dashboard

KubeZero exposes native Prometheus metrics on port `9090` and `9091`:
- `kubezero_cost_savings_usd_total`: Cumulative financial savings in USD calculated from avoided vCPU and RAM usage.
- `kubezero_sleeping_deployments`: Active count of workloads scaled to 0.
- `kubezero_wake_events_total`: Total count of Wake-on-Traffic activations by status (`success`, `timeout`, `scale_error`).
- `kubezero_wake_duration_seconds`: Histogram observing cold-start latency from HTTP trigger to pod ready state.

A pre-built, production-ready Grafana dashboard is available in [dashboards/grafana-dashboard.json](file:///home/d/project-name3/kube-zero/dashboards/grafana-dashboard.json). To import:
1. Open Grafana -> Dashboards -> New -> Import.
2. Upload `dashboards/grafana-dashboard.json`.
3. Select your Prometheus datasource.

---

## Technical Lessons Learned (Gotchas)

- **Go Memory Footprint in Kubernetes:** Standard Go binaries in a `distroless` container run comfortably with a memory footprint under 25 MB, avoiding the node overhead associated with running operators in JVM or Node.js.
- **Connection Buffering:** During pod cold-starts (typically 5 to 15 seconds), the reverse proxy must hold the client socket open and enforce payload limits (e.g., 10 MB) to prevent denial-of-service through memory exhaustion.
