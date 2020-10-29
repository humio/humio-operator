#!/usr/bin/env bash

set -x

declare -r tmp_kubeconfig=/tmp/kubeconfig
declare -r kubectl="kubectl --kubeconfig $tmp_kubeconfig"
declare -r envtest_assets_dir=${ENVTEST_ASSETS_DIR:-/tmp/envtest}
declare -r ginkgo=$(go env GOPATH)/bin/ginkgo
declare -r proxy_method=${PROXY_METHOD:-inject-tcp}

# Preload default humio-core container version
docker pull humio/humio-core:1.16.1
kind load docker-image --name kind humio/humio-core:1.16.1

# Preload humio-core used by e2e tests
docker pull humio/humio-core:1.14.5
kind load docker-image --name kind humio/humio-core:1.14.5

$kubectl apply -k config/crd/
$kubectl label node --overwrite --all topology.kubernetes.io/zone=az1

# TODO: add -p to automatically detect optimal number of test nodes, OR, -nodes=n to set parallelism, and add -stream to output logs from tests running in parallel.
# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
# Documentation for Go support states that inject-tcp method will not work. https://www.telepresence.io/howto/golang
USE_CERTMANAGER=true TEST_USE_EXISTING_CLUSTER=true telepresence --method $proxy_method --run $ginkgo -timeout 60m -skipPackage helpers -v ./... -covermode=count -coverprofile cover.out -progress
