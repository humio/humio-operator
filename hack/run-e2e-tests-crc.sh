#!/usr/bin/env bash

set -x

declare -r tmp_kubeconfig=$HOME/.crc/machines/crc/kubeconfig
declare -r kubectl="oc --kubeconfig $tmp_kubeconfig"
declare -r git_rev=$(git rev-parse --short HEAD)
declare -r ginkgo=$(go env GOPATH)/bin/ginkgo
declare -r proxy_method=${PROXY_METHOD:-inject-tcp}

eval $(crc oc-env)
eval $(crc console --credentials | grep "To login as an admin, run" | cut -f2 -d"'")

$kubectl apply -k config/crd/
$kubectl label node --overwrite --all failure-domain.beta.kubernetes.io/zone=az1

# https://github.com/telepresenceio/telepresence/issues/1309
oc adm policy add-scc-to-user anyuid -z default

# TODO: add -p to automatically detect optimal number of test nodes, OR, -nodes=n to set parallelism, and add -stream to output logs from tests running in parallel.
# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
# Documentation for Go support states that inject-tcp method will not work. https://www.telepresence.io/howto/golang
TELEPRESENCE_USE_OCP_IMAGE=NO OPENSHIFT_SCC_NAME=default-humio-operator USE_CERTMANAGER=true TEST_USE_EXISTING_CLUSTER=true telepresence --method $proxy_method --run $ginkgo -timeout 60m -skipPackage helpers -v ./... -covermode=count -coverprofile cover.out -progress
