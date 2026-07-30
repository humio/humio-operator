#!/usr/bin/env bash

set -euxo pipefail
PROJECT_ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )/.."
cd $PROJECT_ROOT

source ./hack/functions.sh

trap "cleanup_kind_cluster" EXIT

declare -r ginkgo_nodes=${GINKGO_NODES:-1}
declare -r docker=$(which docker)
declare -r humio_e2e_license=${HUMIO_E2E_LICENSE}
declare -r e2e_run_ref=${GITHUB_REF:-outside-github-$(hostname)}
declare -r e2e_run_id=${GITHUB_RUN_ID:-none}
declare -r e2e_run_attempt=${GITHUB_RUN_ATTEMPT:-none}
declare -r ginkgo_label_filter=real
declare -r humio_hostname=${E2E_LOGS_HUMIO_HOSTNAME:-none}
declare -r humio_ingest_token=${E2E_LOGS_HUMIO_INGEST_TOKEN:-none}
declare -r docker_username=${DOCKER_USERNAME:-none}
declare -r docker_password=${DOCKER_PASSWORD:-none}
declare -r dummy_logscale_image=${DUMMY_LOGSCALE_IMAGE:-false}
declare -r use_certmanager=${USE_CERTMANAGER:-true}
declare -r preserve_kind_cluster=${PRESERVE_KIND_CLUSTER:-false}
declare -r local_helper_build=${LOCAL_HELPER_BUILD:-true}
declare -r humio_operator_default_humio_core_image=${HUMIO_OPERATOR_DEFAULT_HUMIO_CORE_IMAGE-}

if [ ! -x "${docker}" ] ; then
  echo "'docker' is not installed. Install it and rerun the script."
  exit 1
fi

if [ "${docker_username}" != "none" ] && [ "${docker_password}" != "none" ]; then
  echo "${docker_password}" | ${docker} login --username "${docker_username}" --password-stdin
fi

mkdir -p $bin_dir

install_kind
install_kubectl
install_helm

start_kind_cluster

# Build and load local helper image with latest code changes (unless disabled)
if [[ $local_helper_build == "true" ]]; then
  build_and_load_local_helper_image
fi

preload_container_images
kubectl_create_dockerhub_secret

# Install all helm charts in parallel for faster startup
helm_install_shippers &
if [[ $use_certmanager == "true" ]]; then
  helm_install_cert_manager &
fi
helm_install_zookeeper_and_kafka &

# Wait for all helm installs to complete
wait

wait_for_pod humio-cp-zookeeper-0
wait_for_pod humio-cp-kafka-0
if [[ $use_certmanager == "true" ]]; then
  wait_for_pod -l app.kubernetes.io/name=cert-manager
  wait_for_pod -l app.kubernetes.io/name=cainjector
  wait_for_pod -l app.kubernetes.io/name=webhook
fi

# Clean up any existing CRDs that might be managed by Helm
if $kubectl get crd | grep -q "humio.com"; then
  echo "Cleaning up existing Humio CRDs..."
  $kubectl delete crd -l app.kubernetes.io/name=humio-operator || true
fi

$kubectl apply --server-side=true -k config/crd/
$kubectl apply --server-side=true -k config/rbac/
$kubectl run test-pod --env="HUMIO_E2E_LICENSE=$humio_e2e_license" --env="GINKGO_NODES=$ginkgo_nodes" --env="GINKGO_FOCUS=${GINKGO_FOCUS:-}" --env="DOCKER_USERNAME=$docker_username" \
  --env="DOCKER_PASSWORD=$docker_password" --env="USE_CERTMANAGER=$use_certmanager" --env="PRESERVE_KIND_CLUSTER=$preserve_kind_cluster" \
  --env="HUMIO_OPERATOR_DEFAULT_HUMIO_CORE_IMAGE=$humio_operator_default_humio_core_image" --env="SUITE=$SUITE" \
  --labels="app=humio-operator,app.kubernetes.io/instance=humio-operator,app.kubernetes.io/component=webhook" \
  --restart=Never --image=testcontainer --image-pull-policy=Never -- sleep 86400
while [[ $($kubectl get pods test-pod -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]; do echo "waiting for pod" ; $kubectl describe pod test-pod ; sleep 1 ; done
# Run tests inside the pod in the background so TCP connection drops don't kill the test.
# The test writes to /tmp/test-output.log; we poll with short-lived kubectl exec calls.
$kubectl exec test-pod -- bash -c 'nohup bash hack/run-e2e-within-kind-test-pod.sh > /tmp/test-output.log 2>&1 &'
echo "Test process started inside pod, polling for completion..."

# Poll every 30s with short-lived connections that tolerate Colima TCP drops.
# Network errors are retried; only conclude "finished" when pgrep succeeds with no ginkgo process.
consecutive_no_process=0
while true; do
  sleep 30
  # Try to check if ginkgo is still running
  pgrep_output=$($kubectl exec --request-timeout=30 test-pod -- pgrep -f ginkgo 2>&1) && pgrep_rc=0 || pgrep_rc=$?

  # Check if this was a network error vs actual process check
  if echo "$pgrep_output" | grep -qi "timeout\|refused\|reset\|Unable to connect\|TLS handshake"; then
    echo "Network error during poll (will retry): $pgrep_output"
    consecutive_no_process=0
    continue
  fi

  if [ $pgrep_rc -eq 0 ]; then
    # Process is still running
    consecutive_no_process=0
    $kubectl exec --request-timeout=30 test-pod -- tail -5 /tmp/test-output.log || true
  else
    # pgrep returned non-zero and it's not a network error — process may be done
    consecutive_no_process=$((consecutive_no_process + 1))
    echo "No ginkgo process found (check $consecutive_no_process/3)..."

    # Require 3 consecutive checks to confirm test is really done (guards against transient errors)
    if [ $consecutive_no_process -ge 3 ]; then
      echo "Test process confirmed finished. Checking results..."
      $kubectl exec --request-timeout=30 test-pod -- tail -50 /tmp/test-output.log || true
      if $kubectl exec --request-timeout=30 test-pod -- grep -q "Test Suite Passed" /tmp/test-output.log; then
        echo "TEST SUITE PASSED"
        exit 0
      else
        echo "TEST SUITE FAILED"
        exit 1
      fi
    fi
  fi
done
