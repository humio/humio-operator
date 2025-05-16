#!/bin/bash

set -euxo pipefail
PROJECT_ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )/../.."
cd $PROJECT_ROOT

source ./hack/functions.sh

trap "cleanup_helm_cluster" EXIT

declare -r docker=$(which docker)
declare -r humio_e2e_license=${HUMIO_E2E_LICENSE}
declare -r e2e_run_ref=${GITHUB_REF:-outside-github-$(hostname)}
declare -r e2e_run_id=${GITHUB_RUN_ID:-none}
declare -r e2e_run_attempt=${GITHUB_RUN_ATTEMPT:-none}
declare -r ginkgo_label_filter=dummy
declare -r humio_hostname=${E2E_LOGS_HUMIO_HOSTNAME:-none}
declare -r humio_ingest_token=${E2E_LOGS_HUMIO_INGEST_TOKEN:-none}
declare -r docker_username=${DOCKER_USERNAME:-none}
declare -r docker_password=${DOCKER_PASSWORD:-none}
declare -r dummy_logscale_image=${DUMMY_LOGSCALE_IMAGE:-true}
declare -r base_logscale_cluster_file="hack/helm-test/test-cases/base/test-logscale-cluster.yaml"
declare -r base_values_file="hack/helm-test/test-cases/base/values.yaml"
declare -r tmp_helm_test_case_dir="hack/helm-test/test-cases/tmp"

run_test_suite() {
    trap "cleanup_upgrade" RETURN

    yq eval -o=j hack/helm-test/test-cases.yaml | jq -c '.test_scenarios[]' | while IFS= read -r scenario; do
        local name=$(echo "$scenario" | jq -r '.name')
        local from_version=$(echo $scenario | jq -r '.from.version')
        local to_version=$(echo $scenario | jq -r '.to.version')
        local from_cluster=$(echo $scenario | jq -r '.from.cluster')
        local from_cluster_patch=$(echo $scenario | jq -r '.from.cluster_patch')
        local to_cluster=$(echo $scenario | jq -r '.to.cluster')
        local to_cluster_patch=$(echo $scenario | jq -r '.to.cluster_patch')
        local from_values=$(echo $scenario | jq -r '.from.values')
        local from_values_patch=$(echo $scenario | jq -r '.from.values_patch')
        local to_values=$(echo $scenario | jq -r '.to.values')
        local to_values_patch=$(echo $scenario | jq -r '.to.values_patch')
        local expect_restarts=$(echo $scenario | jq -r '.expect_restarts')
        local description=$(echo $scenario | jq -r '.description')

        echo "Running test: $name"
        echo "Description: $description"

        # Run test
        if test_upgrade "$from_version" "$to_version" "$expect_restarts" "$from_cluster" "$to_cluster" "$from_values" "$to_values" "$from_cluster_patch" "$to_cluster_patch" "$from_values_patch" "$to_values_patch"; then
            echo "✅ Test passed: $name"
        else
            echo "❌ Test failed: $name"
            exit 1
        fi
    done
}

cleanup_helm_cluster() {
  cleanup_upgrade
  cleanup_humiocluster
  cleanup_tmp_helm_test_case_dir
}

test_upgrade() {
    local from_version=$1
    local to_version=$2
    local expect_restarts=$3  # true/false
    local from_cluster=$4
    local to_cluster=$5
    local from_values=$6
    local to_values=$7
    local from_cluster_patch=$8
    local to_cluster_patch=$9
    local from_values_patch=${10}
    local to_values_patch=${11}

    mkdir -p $tmp_helm_test_case_dir

    if [ "$from_cluster_patch" != "null" ]; then
      from_cluster=$tmp_helm_test_case_dir/from-cluster-$(date +"%Y%m%dT%H%M%S").yaml
      yq eval-all '. as $item ireduce ({}; . * $item)' $base_logscale_cluster_file $from_cluster_patch > $from_cluster
    fi
    if [ "$to_cluster_patch" != "null" ]; then
      to_cluster=$tmp_helm_test_case_dir/to-cluster-$(date +"%Y%m%dT%H%M%S").yaml
      yq eval-all '. as $item ireduce ({}; . * $item)' $base_logscale_cluster_file $to_cluster_patch > $to_cluster
    fi
    if [ "$from_values_patch" != "null" ]; then
      from_values=$tmp_helm_test_case_dir/from-values-$(date +"%Y%m%dT%H%M%S").yaml
      yq eval-all '. as $item ireduce ({}; . * $item)' $base_values_file $from_values_patch > $from_values
    fi
    if [ "$to_values_patch" != "null" ]; then
      to_values=$tmp_helm_test_case_dir/to-values-$(date +"%Y%m%dT%H%M%S").yaml
      yq eval-all '. as $item ireduce ({}; . * $item)' $base_values_file $to_values_patch > $to_values
    fi

    if [ "$from_cluster" == "null" ]; then
      from_cluster=$base_logscale_cluster_file
    fi
    if [ "$from_cluster" == "null" ]; then
      to_cluster=$base_logscale_cluster_file
    fi
    if [ "$from_values" == "null" ]; then
      from_values=$base_values_file
    fi
    if [ "$to_values" == "null" ]; then
      to_values=$base_values_file
    fi

    echo "Testing upgrade from version: $from_version, to version: $to_version, from cluster: $from_cluster, to cluster: $to_cluster, from cluster patch: $from_cluster_patch, to cluster patch: $to_cluster_patch, from values: $from_values, to values: $to_values, expect restarts: $expect_restarts"

    kubectl create secret generic test-cluster-license --from-literal=data="${humio_e2e_license}"

    # Install initial version
    helm repo update
    helm repo add humio-operator https://humio.github.io/humio-operator

    if [ "${from_version}" == "present" ] || [ "${to_version}" == "present" ]; then
      make docker-build
      ./tmp/kind load docker-image controller:latest
    fi

    if [ "${from_version}" == "present" ]; then
      helm install --values $from_values --set operator.image.repository=controller --set operator.image.tag=latest humio-operator ./charts/humio-operator
    else
      helm install --values $from_values humio-operator humio-operator/humio-operator --version $from_version
    fi

    # Deploy test cluster
    kubectl apply -f $from_cluster

    # Wait for initial stability
    wait_for_cluster_ready

    # Capture initial pod states
    local initial_pods=$(capture_pod_states)

    # Perform upgrade
    if [ "${to_version}" == "present" ]; then
      helm upgrade --values $to_values --set operator.image.repository=controller --set operator.image.tag=latest humio-operator ./charts/humio-operator
    else
      helm upgrade --values $to_values humio-operator humio-operator/humio-operator --version $to_version
    fi

    # Update test cluster
    kubectl apply -f $to_cluster

    # Wait for operator upgrade
    kubectl wait --for=condition=available deployment/humio-operator --timeout=2m

    # Monitor pod changes
    verify_pod_restart_behavior "$initial_pods" "$expect_restarts"
}

cleanup_upgrade() {
  helm delete humio-operator || true
}

cleanup_humiocluster() {
  kubectl delete secret test-cluster-license || true
  kubectl delete humiocluster test-cluster || true
}

cleanup_tmp_helm_test_case_dir() {
  rm -rf $tmp_helm_test_case_dir
}

capture_pod_states() {
    # Capture pod details including UID and restart count
    kubectl get pods -l app.kubernetes.io/instance=test-cluster,app.kubernetes.io/managed-by=humio-operator -o json | jq -r '.items[] | "\(.metadata.uid) \(.status.containerStatuses[0].restartCount)"'
}

verify_pod_restart_behavior() {
    local initial_pods=$1
    local expect_restarts=$2
    local timeout=300  # 5 minutes
    local interval=10  # 10 seconds
    local elapsed=0

    echo "Monitoring pod changes for ${timeout}s..."

    while [ $elapsed -lt $timeout ]; do
        sleep $interval
        elapsed=$((elapsed + interval))

        local current_pods=$(capture_pod_states)

        if [ "$expect_restarts" = "true" ]; then
            if pod_restarts_occurred "$initial_pods" "$current_pods"; then
                echo "✅ Expected pod restarts detected"
                return 0
            fi
        else
            if ! pod_restarts_occurred "$initial_pods" "$current_pods"; then
                if [ $elapsed -ge 60 ]; then  # Wait at least 1 minute to confirm stability
                    echo "✅ No unexpected pod restarts detected"
                    return 0
                fi
            else
                echo "❌ Unexpected pod restarts detected"
                return 1
            fi
        fi
    done

    if [ "$expect_restarts" = "true" ]; then
        echo "❌ Expected pod restarts did not occur"
        return 1
    fi
}

pod_restarts_occurred() {
    local initial_pods=$1
    local current_pods=$2

    # Compare UIDs and restart counts
    local changes=$(diff <(echo "$initial_pods") <(echo "$current_pods") || true)
    if [ ! -z "$changes" ]; then
        return 0  # Changes detected
    fi
    return 1  # No changes
}

wait_for_cluster_ready() {
    local timeout=300  # 5 minutes
    local interval=10  # 10 seconds
    local elapsed=0

    while [ $elapsed -lt $timeout ]; do
      sleep $interval
      elapsed=$((elapsed + interval))

      if kubectl wait --for=condition=ready -l app.kubernetes.io/instance=test-cluster pod --timeout=30s; then
        sleep 10
        break
      fi

      kubectl get pods -l app.kubernetes.io/instance=test-cluster
      kubectl describe pods -l app.kubernetes.io/instance=test-cluster
      kubectl logs -l app.kubernetes.io/instance=test-cluster | tail -100
    done
}

wait_for_kafka_ready () {
    local timeout=300  # 5 minutes
    local interval=10  # 10 seconds
    local elapsed=0

    zookeeper_ready=
    kafka_ready=

    while [ $elapsed -lt $timeout ]; do
      sleep $interval
      elapsed=$((elapsed + interval))
      if kubectl wait --for=condition=ready -l app=cp-zookeeper pod --timeout=30s; then
        zookeeper_ready="true"
      fi
      if kubectl wait --for=condition=ready -l app=cp-kafka pod --timeout=30s; then
        kafka_ready="true"
      fi
      if [ "${zookeeper_ready}" == "true" ] && [ "${kafka_ready}" == "true" ]; then
        sleep 2
        break
      fi
    done
}

if [ ! -d $bin_dir ]; then
  mkdir -p $bin_dir
fi

install_kind
install_kubectl
install_helm
install_jq
install_yq

start_kind_cluster
preload_container_images
kubectl_create_dockerhub_secret
wait_for_kafka_ready

run_test_suite
