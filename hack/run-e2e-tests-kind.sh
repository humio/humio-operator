#!/usr/bin/env bash

set -x

declare -r tmp_kubeconfig=/tmp/kubeconfig
declare -r kubectl="kubectl --kubeconfig $tmp_kubeconfig"
declare -r envtest_assets_dir=${ENVTEST_ASSETS_DIR:-/tmp/envtest}
declare -r ginkgo=$(go env GOPATH)/bin/ginkgo
declare -r proxy_method=${PROXY_METHOD:-inject-tcp}

# Extract humio images and tags from go source
DEFAULT_IMAGE=$(grep '^\s*image' controllers/humiocluster_defaults.go | cut -d '"' -f 2)
PRE_UPDATE_IMAGE=$(grep '^\s*toCreate\.Spec\.Image' controllers/humiocluster_controller_test.go | cut -d '"' -f 2)

# Preload default image used by tests
docker pull $DEFAULT_IMAGE
kind load docker-image --name kind $DEFAULT_IMAGE

# Preload image used by e2e update tests
docker pull $PRE_UPDATE_IMAGE
kind load docker-image --name kind $PRE_UPDATE_IMAGE

$kubectl apply -k config/crd/
$kubectl label node --overwrite --all topology.kubernetes.io/zone=az1

# We skip the helpers package as those tests assumes the environment variable USE_CERT_MANAGER is not set.
# Documentation for Go support states that inject-tcp method will not work. https://www.telepresence.io/howto/golang
USE_CERTMANAGER=true TEST_USE_EXISTING_CLUSTER=true telepresence --method $proxy_method --run $ginkgo -timeout 60m -nodes=3 -stream --slowSpecThreshold=5 -skipPackage helpers -v ./... -covermode=count -coverprofile cover.out -progress
