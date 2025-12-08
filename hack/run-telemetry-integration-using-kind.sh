#!/usr/bin/env bash

set -euxo pipefail
PROJECT_ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )/.."
cd $PROJECT_ROOT

source ./hack/functions.sh

trap "cleanup_kind_cluster" EXIT

declare -r ginkgo_nodes=${GINKGO_NODES:-1}
declare -r docker=$(which docker)
declare -r humio_e2e_license=${HUMIO_E2E_LICENSE:-""}
declare -r e2e_run_ref=${GITHUB_REF:-outside-github-$(hostname)}
declare -r e2e_run_id=${GITHUB_RUN_ID:-none}
declare -r e2e_run_attempt=${GITHUB_RUN_ATTEMPT:-none}
declare -r ginkgo_label_filter=real
declare -r humio_hostname=${E2E_LOGS_HUMIO_HOSTNAME:-none}
declare -r humio_ingest_token=${E2E_LOGS_HUMIO_INGEST_TOKEN:-none}
declare -r docker_username=${DOCKER_USERNAME:-none}
declare -r docker_password=${DOCKER_PASSWORD:-none}
declare -r dummy_logscale_image=${DUMMY_LOGSCALE_IMAGE:-false}
declare -r use_certmanager=${USE_CERTMANAGER:-false}  # Don't need cert manager for telemetry tests
declare -r preserve_kind_cluster=${PRESERVE_KIND_CLUSTER:-false}
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
preload_container_images
kubectl_create_dockerhub_secret

# For telemetry integration tests, we don't need Zookeeper/Kafka or cert-manager
# Just need the basic cluster with CRDs

# Clean up any existing CRDs that might be managed by Helm
if $kubectl get crd | grep -q "humio.com"; then
  echo "Cleaning up existing Humio CRDs..."
  $kubectl delete crd -l app.kubernetes.io/name=humio-operator || true
fi

# Apply CRDs for telemetry integration validation
$kubectl apply --server-side=true -k config/crd/

# Run telemetry integration tests
export KUBECONFIG="${HOME}/.kube/config"
ginkgo run --label-filter=real -vv --no-color --procs=1 -timeout 10m ./internal/controller/suite/telemetry/...