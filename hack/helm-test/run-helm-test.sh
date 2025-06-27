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
        local namespace=$(echo $scenario | jq -r '.namespace')

        echo "Running test: $name"
        echo "Description: $description"

        # Run test
        if test_upgrade "$from_version" "$to_version" "$expect_restarts" "$from_cluster" "$to_cluster" "$from_values" "$to_values" "$from_cluster_patch" "$to_cluster_patch" "$from_values_patch" "$to_values_patch" "$namespace"; then
            echo "✅ Test passed: $name"
        else
            echo "❌ Test failed: $name"
            exit 1
        fi
    done
}

cleanup_helm_cluster() {
  cleanup_upgrade
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
    local namespace=${12}

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
    if [ "$to_cluster" == "null" ]; then
      to_cluster=$base_logscale_cluster_file
    fi
    if [ "$from_values" == "null" ]; then
      from_values=$base_values_file
    fi
    if [ "$to_values" == "null" ]; then
      to_values=$base_values_file
    fi

    echo "Testing upgrade from version: $from_version, to version: $to_version, from cluster: $from_cluster, to cluster: $to_cluster, from cluster patch: $from_cluster_patch, to cluster patch: $to_cluster_patch, from values: $from_values, to values: $to_values, expect restarts: $expect_restarts"


    # Install initial version
    helm repo update
    helm repo add humio-operator https://humio.github.io/humio-operator

    if [ "${from_version}" == "present" ] || [ "${to_version}" == "present" ]; then
      make docker-build
      ./tmp/kind load docker-image controller:latest
    fi

    if [ "$namespace" != "null" ]; then
      kubectl create namespace $namespace
    else
      namespace=default
    fi

    # Only create secret if it doesn't exist
    if ! kubectl get secret test-cluster-license -n $namespace >/dev/null 2>&1; then
      kubectl --namespace $namespace create secret generic test-cluster-license --from-literal=data="${humio_e2e_license}"
    fi

    # Check if helm release exists, upgrade if it does, install if it doesn't
    if helm list -n $namespace | grep -q "^humio-operator"; then
      if [ "${from_version}" == "present" ]; then
        helm upgrade -n $namespace --values $from_values --set operator.image.repository=controller --set operator.image.tag=latest humio-operator ./charts/humio-operator
      else
        helm upgrade -n $namespace --values $from_values humio-operator humio-operator/humio-operator --version $from_version
      fi
    else
      if [ "${from_version}" == "present" ]; then
        helm install -n $namespace --values $from_values --set operator.image.repository=controller --set operator.image.tag=latest humio-operator ./charts/humio-operator
      else
        helm install -n $namespace --values $from_values humio-operator humio-operator/humio-operator --version $from_version
      fi
    fi

    # Deploy test cluster
    kubectl apply -f $from_cluster

    # Wait for initial stability (only HumioCluster for now)
    wait_for_cluster_ready_humiocluster_only $namespace

    # Capture initial pod states (HumioCluster only since PDF service doesn't exist yet)
    local initial_pods=$(capture_humiocluster_pod_states)

    # Perform upgrade
    if [ "${to_version}" == "present" ]; then
      helm upgrade --values $to_values --set operator.image.repository=controller --set operator.image.tag=latest humio-operator ./charts/humio-operator
    else
      helm upgrade --values $to_values humio-operator humio-operator/humio-operator --version $to_version
    fi

    # Update test cluster
    kubectl apply -f $to_cluster

    # Wait for operator upgrade
    kubectl --namespace $namespace wait --for=condition=available deployment/humio-operator --timeout=2m

    # Apply any missing CRDs that are needed for the current operator version
    echo "Applying updated CRDs from current operator version..."
    kubectl apply -f charts/humio-operator/crds/ || true

    # Deploy PDF Render Service if CRD is available and operator has permissions
    if kubectl get crd humiopdfrenderservices.core.humio.com >/dev/null 2>&1; then
        echo "PDF Render Service CRD found, deploying PDF service..."
        kubectl apply -f hack/helm-test/test-cases/pdf-render-service-patch.yaml
        # Note: PDF service deployment may not work due to RBAC permissions in test environment
        wait_for_cluster_ready_humiocluster_only $namespace
    else
        echo "PDF Render Service CRD not available, skipping PDF service deployment"
        wait_for_cluster_ready_humiocluster_only $namespace
    fi

    # Monitor pod changes for restart behavior
    verify_pod_restart_behavior "$initial_pods" "$expect_restarts"
}

cleanup_upgrade() {
  helm delete humio-operator || true
  # Only try to delete PDF service if it exists
  if kubectl get humiopdfrenderservice test-pdf-service >/dev/null 2>&1; then
    kubectl delete -f hack/helm-test/test-cases/pdf-render-service-patch.yaml || true
  fi
}

cleanup_tmp_helm_test_case_dir() {
  rm -rf $tmp_helm_test_case_dir
}

capture_humiocluster_pod_states() {
    # Capture pod details including UID and restart count for HumioCluster pods only
    kubectl --namespace $namespace get pods -l app.kubernetes.io/instance=test-cluster,app.kubernetes.io/managed-by=humio-operator -o json | jq -r '.items[] | "\(.metadata.uid) \(.status.containerStatuses[0].restartCount)"'
}

capture_pod_states() {
    # Capture pod details including UID and restart count for both Humio cluster and PDF render service
    (
        kubectl --namespace $namespace get pods -l app.kubernetes.io/instance=test-cluster,app.kubernetes.io/managed-by=humio-operator -o json | jq -r '.items[] | "\(.metadata.uid) \(.status.containerStatuses[0].restartCount)"'
        kubectl --namespace $namespace get pods -l app=humio-pdf-render-service -o json | jq -r '.items[] | "\(.metadata.uid) \(.status.containerStatuses[0].restartCount)"'
    )
}

verify_pod_restart_behavior() {
    local initial_pods=$1
    local expect_restarts=$2
    local timeout=300  # 5 minutes
    local interval=10  # 10 seconds
    local elapsed=0

    echo "Monitoring pod changes for ${timeout}s..."
    echo "Initial pods (HumioCluster only):"
    echo "$initial_pods"

    while [ $elapsed -lt $timeout ]; do
        sleep $interval
        elapsed=$((elapsed + interval))

        local current_pods=$(capture_pod_states)
        
        echo "Current pods (both HumioCluster and PDF service):"
        echo "$current_pods"

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

    # Extract UIDs from initial pods to track only those pods we started with
    local initial_uids=$(echo "$initial_pods" | awk '{print $1}' | sort)
    
    # For each initial pod UID, check if it still exists and if restart count changed
    local restarts_detected=false
    
    while IFS= read -r initial_uid; do
        if [ ! -z "$initial_uid" ]; then
            # Find this UID in current pods
            local initial_line=$(echo "$initial_pods" | grep "^$initial_uid ")
            local current_line=$(echo "$current_pods" | grep "^$initial_uid " || echo "")
            
            if [ -z "$current_line" ]; then
                # Pod UID no longer exists - this indicates a restart (new pod with new UID)
                echo "Pod restart detected: UID $initial_uid no longer exists (pod was recreated)"
                restarts_detected=true
            else
                # Pod UID still exists, compare restart counts
                local initial_restart_count=$(echo "$initial_line" | awk '{print $2}')
                local current_restart_count=$(echo "$current_line" | awk '{print $2}')
                
                if [ "$current_restart_count" -gt "$initial_restart_count" ]; then
                    echo "Pod restart detected: UID $initial_uid restart count increased from $initial_restart_count to $current_restart_count"
                    restarts_detected=true
                fi
            fi
        fi
    done <<< "$initial_uids"
    
    if [ "$restarts_detected" = true ]; then
        return 0  # Restarts detected
    fi
    return 1  # No restarts
}

wait_for_cluster_ready_humiocluster_only() {
    local timeout=300  # 5 minutes
    local interval=10  # 10 seconds
    local elapsed=0
    local namespace=$1

    echo "Waiting for HumioCluster pods to be ready..."

    while [ $elapsed -lt $timeout ]; do
      sleep $interval
      elapsed=$((elapsed + interval))

      if kubectl --namespace $namespace wait --for=condition=ready -l app.kubernetes.io/instance=test-cluster pod --timeout=30s; then
        echo "✅ HumioCluster pods are ready"
        sleep 10
        break
      fi

      echo "Waiting for HumioCluster pods to be ready..."
      kubectl --namespace $namespace get pods -l app.kubernetes.io/instance=test-cluster
      kubectl --namespace $namespace describe pods -l app.kubernetes.io/instance=test-cluster
      kubectl --namespace $namespace logs -l app.kubernetes.io/instance=test-cluster | tail -50
    done
}

wait_for_cluster_ready() {
    local timeout=300  # 5 minutes
    local interval=10  # 10 seconds
    local elapsed=0
    local namespace=$1

    echo "Waiting for HumioCluster and PDF Render Service pods to be ready..."

    while [ $elapsed -lt $timeout ]; do
      sleep $interval
      elapsed=$((elapsed + interval))

      # Check if HumioCluster pods are ready
      local humio_ready=false
      if kubectl --namespace $namespace wait --for=condition=ready -l app.kubernetes.io/instance=test-cluster pod --timeout=30s; then
        humio_ready=true
      fi

      # Check if PDF Render Service pods are ready
      local pdf_ready=false
      if kubectl --namespace $namespace wait --for=condition=ready -l app=humio-pdf-render-service pod --timeout=30s; then
        pdf_ready=true
      fi

      # If both services are ready, we can proceed
      if [ "$humio_ready" = true ] && [ "$pdf_ready" = true ]; then
        echo "✅ Both HumioCluster and PDF Render Service pods are ready"
        sleep 10
        break
      fi

      echo "Waiting for pods to be ready..."
      kubectl --namespace $namespace get pods -l app.kubernetes.io/instance=test-cluster
      kubectl --namespace $namespace get pods -l app=humio-pdf-render-service
      
      # Show pod details if they're not ready
      if [ "$humio_ready" != true ]; then
        kubectl --namespace $namespace describe pods -l app.kubernetes.io/instance=test-cluster
        kubectl --namespace $namespace logs -l app.kubernetes.io/instance=test-cluster | tail -100
      fi
      
      if [ "$pdf_ready" != true ]; then
        kubectl --namespace $namespace describe pods -l app=humio-pdf-render-service
        kubectl --namespace $namespace logs -l app=humio-pdf-render-service | tail -100
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
helm_install_shippers
helm_install_zookeeper_and_kafka
wait_for_kafka_ready

run_test_suite
