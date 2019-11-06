#!/usr/bin/env bash

set -x

# Ensure we use the correct working directory and KUBECONFIG:
cd ~/go/src/github.com/humio/humio-operator
export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"

# Run operator locally
telepresence --method inject-tcp --run make run
