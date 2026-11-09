#!/usr/bin/env bash
set -euo pipefail

IMG="${IMG:-humio-operator:dev}"
KIND_CLUSTER="${KIND_CLUSTER:-kind}"
NAMESPACE="${NAMESPACE:-humio-operator-system}"
RELEASE="${RELEASE:-humio-operator}"

REPO="${IMG%%:*}"
TAG="${IMG##*:}"
WEBHOOK_IMG="${REPO}-webhook:${TAG}"

echo "==> Building operator image: ${IMG}"
docker build -t "${IMG}" -f Dockerfile.operator .

echo "==> Building webhook image: ${WEBHOOK_IMG}"
docker build -t "${WEBHOOK_IMG}" -f Dockerfile.webhook .

echo "==> Loading images into kind cluster: ${KIND_CLUSTER}"
kind load docker-image "${IMG}" "${WEBHOOK_IMG}" --name "${KIND_CLUSTER}"

echo "==> Applying CRDs"
kubectl apply --server-side --force-conflicts -f charts/humio-operator/crds/

echo "==> Installing/upgrading helm release: ${RELEASE}"
helm upgrade --install "${RELEASE}" ./charts/humio-operator \
  --namespace "${NAMESPACE}" \
  --create-namespace \
  --skip-crds \
  --set operator.image.repository="${REPO}" \
  --set operator.image.tag="${TAG}" \
  --set operator.image.pullPolicy=Never \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key=kubernetes.io/arch' \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator=In' \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].values[0]=arm64' \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[1].key=kubernetes.io/os' \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[1].operator=In' \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[1].values[0]=linux'

echo "==> Restarting deployments"
kubectl rollout restart deployment/"${RELEASE}" deployment/"${RELEASE}-webhook" -n "${NAMESPACE}"
kubectl rollout status deployment/"${RELEASE}" -n "${NAMESPACE}" --timeout=90s
kubectl rollout status deployment/"${RELEASE}-webhook" -n "${NAMESPACE}" --timeout=90s

echo "==> Done. Operator running with ${IMG}"
