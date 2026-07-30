#!/usr/bin/env bash

set -euxo pipefail
PROJECT_ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )/.."
cd "$PROJECT_ROOT"

source ./hack/functions.sh

# --- Configuration defaults ---
declare -r docker=$(which docker)
declare -r docker_username=${DOCKER_USERNAME:-none}
declare -r docker_password=${DOCKER_PASSWORD:-none}
declare -r dummy_logscale_image=${DUMMY_LOGSCALE_IMAGE:-false}
declare -r use_certmanager=${USE_CERTMANAGER:-true}
declare -r local_helper_build=${LOCAL_HELPER_BUILD:-true}
declare -r sample=${SAMPLE:-core_v1alpha1_humiocluster-kind-local.yaml}
declare -r sample_path="config/samples/${sample}"
declare -r pod_wait_timeout=300  # 5 minutes (MEASURED -- max seconds to wait for any single pod)


# --- Validate prerequisites ---
if [ ! -x "${docker}" ]; then
  echo "ERROR: 'docker' is not installed or not executable. Install Docker and rerun."
  exit 1
fi

if ! ${docker} info >/dev/null 2>&1; then
  echo "ERROR: Docker daemon is not running. Start Docker and rerun."
  exit 1
fi

# --- Validate SAMPLE file exists before cluster creation (fail-fast) ---
if [[ ! -f "$sample_path" || ! -r "$sample_path" ]]; then
  echo "ERROR: Sample file does not exist or is not readable: $sample_path"
  echo "Available samples in config/samples/:"
  ls config/samples/*.yaml 2>/dev/null | head -10 || true
  exit 1
fi

# --- Setup Phase ---
if [ "${docker_username}" != "none" ] && [ "${docker_password}" != "none" ]; then
  echo "${docker_password}" | ${docker} login --username "${docker_username}" --password-stdin
fi

mkdir -p "$bin_dir"

install_kind
install_kubectl
install_helm

start_kind_cluster
kubectl_create_dockerhub_secret

if [[ "$use_certmanager" == "true" ]]; then
  helm_install_cert_manager
fi
helm_install_zookeeper_and_kafka

# --- Wait for dependencies with timeout ---
wait_for_pod_with_timeout() {
  local pod_selector="$@"
  local elapsed=0
  local interval=10

  while [[ $elapsed -lt $pod_wait_timeout ]]; do
    local statuses
    statuses=$($kubectl get pods $pod_selector -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
    if [[ -n "$statuses" && "$statuses" != *"False"* ]]; then
      echo "Pod(s) ready: $pod_selector"
      return 0
    fi
    echo "Waiting for pod ($pod_selector) ... ${elapsed}s/${pod_wait_timeout}s"
    sleep $interval
    elapsed=$((elapsed + interval))
  done

  echo "ERROR: Timed out waiting for pod: $pod_selector (${pod_wait_timeout}s)"
  echo "--- kubectl describe pod $pod_selector ---"
  $kubectl describe pod $pod_selector || true
  echo "--- kubectl get pods -A ---"
  $kubectl get pods -A || true
  exit 1
}

echo "==> Waiting for ZooKeeper..."
wait_for_pod_with_timeout humio-cp-zookeeper-0

echo "==> Waiting for Kafka..."
wait_for_pod_with_timeout humio-cp-kafka-0

if [[ "$use_certmanager" == "true" ]]; then
  echo "==> Waiting for cert-manager..."
  wait_for_pod_with_timeout -l app.kubernetes.io/name=cert-manager
  wait_for_pod_with_timeout -l app.kubernetes.io/name=cainjector
  wait_for_pod_with_timeout -l app.kubernetes.io/name=webhook
fi

# --- Deploy Phase: Build and deploy operator ---
# Use a non-"latest" tag so Kubernetes defaults imagePullPolicy to IfNotPresent
declare -r local_img="${IMG:-controller:local}"
declare -r local_img_repo="${local_img%%:*}"
declare -r local_img_tag="${local_img##*:}"

echo "==> Building operator image..."
docker build -f Dockerfile.operator -t "$local_img" .
$kind load docker-image "$local_img" --name kind

echo "==> Building webhook image..."
docker build -f Dockerfile.webhook -t "${local_img_repo}-webhook:${local_img_tag}" .
$kind load docker-image "${local_img_repo}-webhook:${local_img_tag}" --name kind

if [[ "$local_helper_build" == "true" ]]; then
  echo "==> Building helper image..."
  cp LICENSE images/helper/
  docker build -t humio/humio-operator-helper:latest images/helper
  $kind load docker-image humio/humio-operator-helper:latest --name kind
fi

if [[ "$dummy_logscale_image" == "true" ]]; then
  echo "==> Building dummy LogScale image..."
  docker build -t humio/humio-core:dummy images/logscale-dummy
  $kind load docker-image humio/humio-core:dummy --name kind
fi

echo "==> Deploying operator via Helm..."
# Pre-apply CRDs (large, can overwhelm API server if done via helm)
$kubectl apply --server-side=true -f charts/humio-operator/crds/
sleep 5
# Use system helm if available (avoids rate limiter bug in helm 3.14.x)
local_helm="/opt/homebrew/bin/helm"
if [[ ! -x "$local_helm" ]]; then
  local_helm="$helm"
fi
$local_helm upgrade --install humio-operator ./charts/humio-operator \
  --namespace humio-operator-system \
  --create-namespace \
  --set operator.image.repository="$local_img_repo" \
  --set operator.image.tag="$local_img_tag" \
  --set operator.image.pullPolicy=IfNotPresent \
  --set operator.certmanager=true \
  --set operator.rbac.create=true \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key=kubernetes.io/os' \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator=In' \
  --set 'operator.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].values[0]=linux' \
  --skip-crds \
  --qps=100 --burst-limit=400

echo "==> Waiting for operator pod..."
sleep 10
$kubectl get pods -n humio-operator-system
$kubectl logs -n humio-operator-system -l app.kubernetes.io/name=humio-operator --tail=20 || true
wait_for_pod_with_timeout -n humio-operator-system -l app.kubernetes.io/name=humio-operator

# --- Pre-apply hook: create secrets needed by the sample CR ---
if [[ -n "${HUMIO_LICENSE:-}" ]]; then
  echo "==> Creating license secret from HUMIO_LICENSE env var"
  $kubectl create secret generic example-humiocluster-license \
    --from-literal=data="$HUMIO_LICENSE" \
    --dry-run=client -o yaml | $kubectl apply -f -
fi

# --- Apply sample (non-fatal per KD-4) ---
echo "==> Applying sample: $sample_path"
if ! $kubectl apply -f "$sample_path"; then
  echo ""
  echo "WARNING: Failed to apply sample $sample_path"
  echo "The cluster is still usable. You can fix the issue and re-apply manually:"
  echo "  kubectl apply -f $sample_path"
  echo ""
fi

# --- Connection Info Banner ---
echo ""
echo "============================================================"
echo "  LOCAL KIND CLUSTER READY"
echo "============================================================"
echo ""
echo "  Cluster:    kind"
echo "  Context:    kind-kind"
echo "  Sample CR:  $sample_path"
echo ""
echo "  Useful commands:"
echo "    kubectl get pods -A"
echo "    kubectl get humiocluster -o yaml"
echo "    kubectl logs -f -l control-plane=controller-manager"
echo "    kubectl describe humiocluster"
echo ""
echo "  To tear down:  kind delete cluster --name kind"
echo "============================================================"
echo ""
