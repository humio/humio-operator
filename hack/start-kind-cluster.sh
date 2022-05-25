#!/bin/bash

set -x

kind create cluster --name kind --image kindest/node:v1.24.0@sha256:406fd86d48eaf4c04c7280cd1d2ca1d61e7d0d61ddef0125cb097bc7b82ed6a1

sleep 5

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

docker exec kind-control-plane sh -c 'echo nameserver 8.8.8.8 > /etc/resolv.conf'
docker exec kind-control-plane sh -c 'echo options ndots:0 >> /etc/resolv.conf'

kubectl label node --overwrite --all topology.kubernetes.io/zone=az1
