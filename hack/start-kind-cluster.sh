#!/bin/bash

set -x

kind create cluster --name kind --image kindest/node:v1.17.17@sha256:c581fbf67f720f70aaabc74b44c2332cc753df262b6c0bca5d26338492470c17

sleep 5

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

docker exec kind-control-plane sh -c 'echo nameserver 8.8.8.8 > /etc/resolv.conf'
docker exec kind-control-plane sh -c 'echo options ndots:0 >> /etc/resolv.conf'

kubectl label node --overwrite --all topology.kubernetes.io/zone=az1
