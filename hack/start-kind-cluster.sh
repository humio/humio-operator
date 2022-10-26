#!/bin/bash

set -x

kind create cluster --name kind --image kindest/node:v1.25.2@sha256:9be91e9e9cdf116809841fc77ebdb8845443c4c72fe5218f3ae9eb57fdb4bace

sleep 5

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

docker exec kind-control-plane sh -c 'echo nameserver 8.8.8.8 > /etc/resolv.conf'
docker exec kind-control-plane sh -c 'echo options ndots:0 >> /etc/resolv.conf'

kubectl label node --overwrite --all topology.kubernetes.io/zone=az1
