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
declare -r humio_operator_default_humio_core_image=${HUMIO_OPERATOR_DEFAULT_HUMIO_CORE_IMAGE-}

if [ ! -x "${docker}" ] ; then
  echo "'docker' is not installed. Install it and rerun the script."
  exit 1
fi
$docker login

mkdir -p $bin_dir

install_kind
install_kubectl
install_helm

start_kind_cluster
preload_container_images
kubectl_create_dockerhub_secret

helm_install_shippers
helm_install_cert_manager
helm_install_zookeeper_and_kafka

wait_for_pod humio-cp-zookeeper-0
wait_for_pod humio-cp-kafka-0
wait_for_pod -l app.kubernetes.io/name=cert-manager
wait_for_pod -l app.kubernetes.io/name=cainjector
wait_for_pod -l app.kubernetes.io/name=webhook

$kubectl create -k config/crd/
$kubectl run test-pod --env="HUMIO_E2E_LICENSE=$humio_e2e_license" --env="GINKGO_NODES=$ginkgo_nodes" --env="DOCKER_USERNAME=$docker_username" --env="DOCKER_PASSWORD=$docker_password" --env="HUMIO_OPERATOR_DEFAULT_HUMIO_CORE_IMAGE=$humio_operator_default_humio_core_image" --restart=Never --image=testcontainer --image-pull-policy=Never -- sleep 86400
while [[ $($kubectl get pods test-pod -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]; do echo "waiting for pod" ; $kubectl describe pod test-pod ; sleep 1 ; done
$kubectl exec test-pod -- hack/run-e2e-within-kind-test-pod.sh
