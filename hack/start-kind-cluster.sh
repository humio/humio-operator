#!/bin/bash

set -x

kind create cluster --name kind --image kindest/node:v1.25.8@sha256:b5ce984f5651f44457edf263c1fe93459df8d5d63db7f108ccf5ea4b8d4d9820

sleep 5

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

docker exec kind-control-plane sh -c 'echo nameserver 8.8.8.8 > /etc/resolv.conf'
docker exec kind-control-plane sh -c 'echo options ndots:0 >> /etc/resolv.conf'

kubectl label node --overwrite --all topology.kubernetes.io/zone=az1
