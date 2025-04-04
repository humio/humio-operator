#!/bin/bash
set -e

source ./functions.sh

declare -r e2e_license=${HUMIO_E2E_LICENSE}

test_upgrade() {
    local from_version=$1
    local to_version=$2
    local expect_restarts=$3  # true/false
    local cluster=$4

    echo "Testing upgrade from $from_version to $to_version (expect restarts: $expect_restarts)"

    kubectl create secret generic test-cluster-license --from-literal=data="${e2e_license}"

    # Install initial version
    helm repo update
    helm repo add humio-operator https://humio.github.io/humio-operator

    helm install --values values.yaml humio-operator humio-operator/humio-operator --version $from_version

    # Deploy test cluster
    kubectl apply -f $cluster

    # Wait for initial stability
    wait_for_cluster_ready

    # Capture initial pod states
    local initial_pods=$(capture_pod_states)

    # Perform upgrade
    if [ "${to_version}" == "present" ]; then
      pushd .. && make docker-build && popd
      # todo: fix
      #$kind load docker-image controller:latest
      ../tmp/kind load docker-image controller:latest
      helm upgrade --values values.yaml --set operator.image.repository=controller --set operator.image.tag=latest humio-operator ../charts/humio-operator
    else
      helm upgrade --values values.yaml humio-operator humio-operator/humio-operator --version $to_version
    fi

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

    #while [ $elapsed -lt $timeout ]; do
    #  sleep $interval
    #  elapsed=$((elapsed + interval))

    #  if kubectl wait --for=condition=ready humiocluster/test-cluster --timeout=5m; then
    #    break
    #  fi
    #done

    while [ $elapsed -lt $timeout ]; do
      sleep $interval
      elapsed=$((elapsed + interval))

      if kubectl wait --for=condition=ready -l app.kubernetes.io/instance=test-cluster pod --timeout=5m; then
        sleep 30
        break
      fi
    done
}

