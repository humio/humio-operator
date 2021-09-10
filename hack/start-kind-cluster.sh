#!/bin/bash

set -x

kind create cluster --name kind --image kindest/node:v1.21.2@sha256:9d07ff05e4afefbba983fac311807b3c17a5f36e7061f6cb7e2ba756255b2be4

sleep 5

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

docker exec kind-control-plane sh -c 'echo nameserver 8.8.8.8 > /etc/resolv.conf'
docker exec kind-control-plane sh -c 'echo options ndots:0 >> /etc/resolv.conf'

kubectl label node --overwrite --all topology.kubernetes.io/zone=az1
