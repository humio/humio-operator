#!/usr/bin/env bash

set -x

declare -r envtest_assets_dir=${ENVTEST_ASSETS_DIR:-/tmp/envtest}
declare -r ginkgo=$(go env GOPATH)/bin/ginkgo

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

if [[ -z "${HUMIO_E2E_LICENSE}" ]]; then
  echo "Environment variable HUMIO_E2E_LICENSE not set. Aborting."
  exit 1
fi

export PATH=$BIN_DIR:$PATH

kubectl apply -k config/crd/
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

# TODO: add -p to automatically detect optimal number of test nodes, OR, -nodes=n to set parallelism, and add -stream to output logs from tests running in parallel.
# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
USE_CERTMANAGER=true TEST_USE_EXISTING_CLUSTER=true $ginkgo -timeout 90m -nodes=3 -skipPackage helpers -v ./... -covermode=count -coverprofile cover.out -progress | tee /proc/1/fd/1
