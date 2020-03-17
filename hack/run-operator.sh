#!/usr/bin/env bash

set -x

# Ensure we use the correct working directory and KUBECONFIG:
cd ~/go/src/github.com/humio/humio-operator
export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"

# Stop an existing operator
kubectl delete deploy humio-operator

# Build the operator
operator-sdk build humio/humio-operator:dev

# Run operator locally
kind load docker-image --name kind humio/humio-operator:dev
export WATCH_NAMESPACE=default
kubectl apply -f deploy/role.yaml
kubectl apply -f deploy/service_account.yaml
kubectl apply -f deploy/role_binding.yaml
kubectl apply -f deploy/operator.yaml
sleep 5
kubectl logs -f -n default deploy/humio-operator