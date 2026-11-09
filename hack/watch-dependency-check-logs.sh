#!/usr/bin/env bash

# Background watcher that continuously dumps init container logs as they become available

source hack/functions.sh

echo "=== Starting Dependency Check Log Watcher ==="

# Track which pods we've already dumped
DUMPED_PODS=""

while true; do
  for ns in e2e-clusters-1 e2e-clusters-2 e2e-clusters-3; do
    pods=$($kubectl get pods -n $ns -l app.kubernetes.io/name=humio -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)

    for pod in $pods; do
      # Skip if we already dumped this pod
      if echo "$DUMPED_PODS" | grep -q "$ns/$pod"; then
        continue
      fi

      # Check if init container has completed
      init_status=$($kubectl get pod -n $ns $pod -o jsonpath='{.status.initContainerStatuses[?(@.name=="humio-init")].state.terminated.exitCode}' 2>/dev/null)

      if [ -n "$init_status" ]; then
        echo ""
        echo "=== Init Container Logs for $pod in $ns (exit code: $init_status) ==="
        $kubectl logs -n $ns $pod -c humio-init 2>/dev/null || echo "Failed to fetch logs"
        echo "=== End logs for $pod ==="
        echo ""

        # Mark as dumped
        DUMPED_PODS="$DUMPED_PODS $ns/$pod"
      fi
    done
  done

  sleep 2
done
