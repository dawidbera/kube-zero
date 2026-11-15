#!/usr/bin/env bash
# End-to-End (E2E) verification script for KubeZero on a local k3d Kubernetes cluster.
# Automates cluster provisioning, Helm deployment, sleep cycle, and wake-on-traffic HTTP testing.

set -euo pipefail

CLUSTER_NAME="kubezero-e2e"
SKIP_CLEANUP="${SKIP_CLEANUP:-false}"

echo "=================================================="
echo " Starting KubeZero End-to-End (E2E) Test Suite"
echo "=================================================="

# Verify prerequisites
for cmd in docker k3d kubectl helm; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "[-] Error: Required tool '$cmd' is not installed or not in PATH."
    exit 1
  fi
done

cleanup() {
  if [ "$SKIP_CLEANUP" != "true" ]; then
    echo "==> Cleaning up k3d cluster '${CLUSTER_NAME}'..."
    k3d cluster delete "${CLUSTER_NAME}" >/dev/null 2>&1 || true
    echo "[+] Cleanup complete."
  else
    echo "[i] Skipping cleanup: k3d cluster '${CLUSTER_NAME}' remains active."
  fi
}
trap cleanup EXIT

# 1. Create local k3d cluster
echo "==> Step 1: Provisioning local k3d cluster '${CLUSTER_NAME}'..."
k3d cluster delete "${CLUSTER_NAME}" >/dev/null 2>&1 || true
k3d cluster create "${CLUSTER_NAME}" \
  --port "8080:8080@loadbalancer" \
  --port "9091:9091@loadbalancer" \
  --wait

# 2. Import Docker images
echo "==> Step 2: Importing KubeZero images into k3d cluster..."
k3d image import \
  kube-zero/controller:latest \
  kube-zero/wake-proxy:latest \
  -c "${CLUSTER_NAME}"

# 3. Install KubeZero via Helm
echo "==> Step 3: Installing KubeZero Helm chart..."
helm install kube-zero ./charts/kube-zero \
  -n kube-zero --create-namespace \
  --set controller.image.repository=kube-zero/controller \
  --set controller.image.tag=latest \
  --set controller.image.pullPolicy=Never \
  --set controller.syncInterval="2s" \
  --set wakeProxy.image.repository=kube-zero/wake-proxy \
  --set wakeProxy.image.tag=latest \
  --set wakeProxy.image.pullPolicy=Never \
  --set wakeProxy.replicaCount=1 \
  --set wakeProxy.targetUrl="http://sample-app:8080" \
  --set wakeProxy.targetNamespace="default" \
  --set wakeProxy.targetDeployment="sample-app"

# 4. Wait for CRD and Deployments
echo "==> Step 4: Waiting for KubeZero operator pods to be Ready..."
kubectl wait --for=condition=Established crd/zeropolicies.finops.kubezero.io --timeout=30s
kubectl rollout status deployment/kube-zero-controller -n kube-zero --timeout=90s
kubectl rollout status deployment/kube-zero-wake-proxy -n kube-zero --timeout=90s

# 5. Deploy test upstream application
echo "==> Step 5: Deploying demo upstream service (sample-app)..."
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: sample-app
  namespace: default
spec:
  replicas: 2
  selector:
    matchLabels:
      app: sample-app
  template:
    metadata:
      labels:
        app: sample-app
    spec:
      containers:
        - name: app
          image: hashicorp/http-echo:latest
          args: ["-listen=:8080", "-text=Hello from KubeZero Target App!"]
          ports:
            - containerPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: sample-app
  namespace: default
spec:
  selector:
    app: sample-app
  ports:
    - name: http
      port: 8080
      targetPort: 8080
EOF

kubectl rollout status deployment/sample-app -n default --timeout=60s
echo "[+] sample-app is deployed with 2 ready replicas."

# 6. Apply ZeroPolicy to trigger scale-to-zero
echo "==> Step 6: Applying ZeroPolicy for sample-app..."
kubectl apply -f - <<EOF
apiVersion: finops.kubezero.io/v1alpha1
kind: ZeroPolicy
metadata:
  name: sample-app-sleep-policy
  namespace: default
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: sample-app
  wakeOnTraffic:
    enabled: true
    servicePort: 8080
    targetPort: 8080
    readinessTimeout: "30s"
  schedule:
    sleepCron: "* * * * *"
EOF

# 7. Verify scale-to-zero and Service traffic redirection
echo "==> Step 7: Verifying scale-down and Service selector redirection..."
SLEEP_SUCCESS=false
for i in $(seq 1 15); do
  REPLICAS=$(kubectl get deployment sample-app -n default -o jsonpath='{.spec.replicas}')
  SVC_SELECTOR=$(kubectl get service sample-app -n default -o jsonpath='{.spec.selector.app\.kubernetes\.io/name}' 2>/dev/null || echo "")

  if [ "$REPLICAS" -eq 0 ] && [ "$SVC_SELECTOR" = "kubezero-wake-proxy" ]; then
    echo "[+] Workload scaled to 0 replicas and Service redirected to kubezero-wake-proxy!"
    SLEEP_SUCCESS=true
    break
  fi
  sleep 2
done

if [ "$SLEEP_SUCCESS" != "true" ]; then
  echo "[-] Error: Workload did not scale to 0 within timeout. Current replicas: ${REPLICAS}"
  exit 1
fi

# Switch schedule to off-hours window so workload stays awake after wake-on-traffic
echo "==> Updating schedule to off-hours (0 19 * * 1-5) to verify post-wake persistence..."
kubectl patch zeropolicy sample-app-sleep-policy -n default --type=merge -p '{"spec":{"schedule":{"sleepCron":"0 19 * * 1-5","wakeCron":"0 7 * * 1-5"}}}'

# 8. Trigger Wake-on-Traffic via HTTP request to wake-proxy
echo "==> Step 8: Triggering Wake-on-Traffic via HTTP request..."
WAKE_OUTPUT=$(kubectl run curl-trigger --image=curlimages/curl:latest --rm -i --restart=Never -- http://kube-zero-wake-proxy.kube-zero.svc.cluster.local:8080/ 2>/dev/null || true)

echo "Received HTTP response: ${WAKE_OUTPUT}"

if echo "$WAKE_OUTPUT" | grep -q "Hello from KubeZero Target App!"; then
  echo "[+] Success! Wake-proxy intercepted request, revived sample-app pod, and transparently proxied HTTP response."
else
  echo "[-] Error: Unexpected response body: ${WAKE_OUTPUT}"
  exit 1
fi

# 9. Verify deployment restored
sleep 3
WOKEN_REPLICAS=$(kubectl get deployment sample-app -n default -o jsonpath='{.spec.replicas}')
if [ "$WOKEN_REPLICAS" -gt 0 ]; then
  echo "[+] Post-wake deployment replica count: ${WOKEN_REPLICAS}"
else
  echo "[-] Error: Deployment replica count was not restored!"
  exit 1
fi

RESTORED_SELECTOR=$(kubectl get service sample-app -n default -o jsonpath='{.spec.selector.app}' 2>/dev/null || echo "")
if [ "$RESTORED_SELECTOR" = "sample-app" ]; then
  echo "[+] Success! Service selector successfully restored to target application pod!"
fi

echo "=================================================="
echo " KubeZero E2E Verification Passed Successfully!"
echo "=================================================="
