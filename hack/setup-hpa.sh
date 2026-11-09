#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${NAMESPACE:-default}"
HPA_NAME="${HPA_NAME:-ingest-only-hpa}"
TARGET_NODEPOOL="${TARGET_NODEPOOL:-example-humiocluster-ingest-only}"
MIN_REPLICAS="${MIN_REPLICAS:-1}"
MAX_REPLICAS="${MAX_REPLICAS:-2}"
CPU_TARGET="${CPU_TARGET:-50}"
KIND_CLUSTER="${KIND_CLUSTER:-kind}"
METRICS_SERVER_VERSION="${METRICS_SERVER_VERSION:-v0.8.1}"
METRICS_SERVER_IMAGE="registry.k8s.io/metrics-server/metrics-server:${METRICS_SERVER_VERSION}"

echo "==> Pre-loading metrics-server image into kind nodes"
docker pull "${METRICS_SERVER_IMAGE}" 2>/dev/null || true
kind load docker-image "${METRICS_SERVER_IMAGE}" --name "${KIND_CLUSTER}" 2>/dev/null || true

echo "==> Installing metrics-server (${METRICS_SERVER_VERSION} for k8s 1.33+)"
kubectl apply -f "https://github.com/kubernetes-sigs/metrics-server/releases/download/${METRICS_SERVER_VERSION}/components.yaml"

echo "==> Patching metrics-server for kind (--kubelet-insecure-tls)"
kubectl patch deployment metrics-server -n kube-system \
  --type=json \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'

echo "==> Waiting for metrics-server to be ready"
kubectl rollout status deployment/metrics-server -n kube-system --timeout=120s

echo "==> Waiting for metrics API to become available (~60s)..."
for i in $(seq 1 12); do
  if kubectl top nodes &>/dev/null; then
    echo "    Metrics API ready!"
    break
  fi
  if [ "$i" -eq 12 ]; then
    echo "    WARNING: Metrics API not ready yet. HPA will work once metrics-server starts scraping."
  fi
  sleep 10
done

echo "==> Creating HPA: ${HPA_NAME} targeting ${TARGET_NODEPOOL}"
cat <<EOF | kubectl apply -n "${NAMESPACE}" -f -
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: ${HPA_NAME}
spec:
  scaleTargetRef:
    apiVersion: core.humio.com/v1alpha1
    kind: HumioNodePool
    name: ${TARGET_NODEPOOL}
  minReplicas: ${MIN_REPLICAS}
  maxReplicas: ${MAX_REPLICAS}
  metrics:
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: ${CPU_TARGET}
EOF

echo "==> Current HPA status:"
kubectl get hpa "${HPA_NAME}" -n "${NAMESPACE}"

echo "==> Done. Monitor with: kubectl get hpa ${HPA_NAME} -n ${NAMESPACE} -w"
