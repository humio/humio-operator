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
declare -r humio_operator_default_humio_core_image=${HUMIO_OPERATOR_DEFAULT_HUMIO_CORE_IMAGE-}

if [ ! -x "${docker}" ] ; then
  echo "'docker' is not installed. Install it and rerun the script."
  exit 1
fi
$docker login

mkdir -p $bin_dir

install_kind
