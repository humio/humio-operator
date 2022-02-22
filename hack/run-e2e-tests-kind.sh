#!/usr/bin/env bash

set -x -o pipefail

declare -r ginkgo=$(go env GOPATH)/bin/ginkgo
declare -r ginkgo_nodes=${GINKGO_NODES:-1}

start=$(date +%s)

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

if [[ -z "${HUMIO_E2E_LICENSE}" ]]; then
  echo "Environment variable HUMIO_E2E_LICENSE not set. Aborting."
  exit 1
fi

export PATH=$BIN_DIR:$PATH

kubectl create -k config/crd/
kubectl label node --overwrite --all topology.kubernetes.io/zone=az1

iterations=0
while ! curl -k https://kubernetes.default
do
  let "iterations+=1"
  echo curl failed $iterations times
  if [ $iterations -ge 30 ]; then
    exit 1
  fi
  sleep 2
done

make ginkgo

# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
USE_CERTMANAGER=true TEST_USE_EXISTING_CLUSTER=true $ginkgo --always-emit-ginkgo-writer -slow-spec-threshold=5s -timeout 90m -nodes=$ginkgo_nodes --skip-package helpers -race -v ./... -covermode=count -coverprofile cover.out -progress | tee /proc/1/fd/1
TEST_EXIT_CODE=$?

end=$(date +%s)
echo "Running e2e tests took $((end-start)) seconds"

exit "$TEST_EXIT_CODE"
