#!/usr/bin/env bash

set -x

# Ensure we use the correct working directory:
cd ~/go/src/github.com/humio/humio-operator

# Stop an existing operator
kubectl --context kind-kind delete deploy humio-operator

# Build the operator
operator-sdk build humio/humio-operator:dev

# Run operator locally
kind load docker-image --name kind humio/humio-operator:dev
docker rmi humio/humio-operator:dev
export WATCH_NAMESPACE=default
kubectl --context kind-kind apply -f deploy/role.yaml
kubectl --context kind-kind apply -f deploy/service_account.yaml
kubectl --context kind-kind apply -f deploy/role_binding.yaml
kubectl --context kind-kind apply -f deploy/operator.yaml
sleep 5
kubectl --context kind-kind logs -f -n default deploy/humio-operator
