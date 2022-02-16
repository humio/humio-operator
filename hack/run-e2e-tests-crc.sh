#!/usr/bin/env bash

set -x

declare -r tmp_kubeconfig=$HOME/.crc/machines/crc/kubeconfig
declare -r kubectl="oc --kubeconfig $tmp_kubeconfig"
declare -r git_rev=$(git rev-parse --short HEAD)
declare -r ginkgo=$(go env GOPATH)/bin/ginkgo

echo "Script needs rework after we're no longer using Telepresence. Aborting..."
exit 1

if ! kubectl get namespace -n openshift ; then
  echo "Cluster unavailable or not using a crc/openshift cluster. Only crc clusters are supported!"
  exit 1
fi

if [[ -z "${HUMIO_E2E_LICENSE}" ]]; then
  echo "Environment variable HUMIO_E2E_LICENSE not set. Aborting."
  exit 1
fi

export PATH=$BIN_DIR:$PATH

eval $(crc oc-env)
eval $(crc console --credentials | grep "To login as an admin, run" | cut -f2 -d"'")

$kubectl apply -k config/crd/
$kubectl label node --overwrite --all topology.kubernetes.io/zone=az1

# https://github.com/telepresenceio/telepresence/issues/1309
oc adm policy add-scc-to-user anyuid -z default # default in this command refers to the service account name that is used

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

# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
OPENSHIFT_SCC_NAME=default-humio-operator KUBECONFIG=$tmp_kubeconfig USE_CERTMANAGER=true TEST_USE_EXISTING_CLUSTER=true $ginkgo -timeout 90m --skip-package helpers -v ./... -covermode=count -coverprofile cover.out -progress
