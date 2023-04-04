#!/bin/bash

kind_image=kindest/node:v1.25.2@sha256:9be91e9e9cdf116809841fc77ebdb8845443c4c72fe5218f3ae9eb57fdb4bace

if ! type kind &>/dev/null; then
  echo "kind not found. Install with 'go install' or 'brew'"
  exit 1
fi

if ! kind get clusters | grep -qxF kind; then
  kind create cluster --name kind --image $kind_image
  sleep 5
fi

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

set -x

docker exec kind-control-plane sh -c 'echo nameserver 8.8.8.8 > /etc/resolv.conf'
docker exec kind-control-plane sh -c 'echo options ndots:0 >> /etc/resolv.conf'

kubectl label node --overwrite --all topology.kubernetes.io/zone=az1
