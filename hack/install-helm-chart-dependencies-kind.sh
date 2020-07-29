#!/bin/bash

set -x

declare -r bin_dir=${BIN_DIR:-/usr/local/bin}
declare -r tmp_kubeconfig=/tmp/kubeconfig

export PATH=$BIN_DIR:$PATH

kind get kubeconfig > $tmp_kubeconfig

kubectl --kubeconfig=$tmp_kubeconfig create namespace cert-manager
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install --kubeconfig=$tmp_kubeconfig cert-manager jetstack/cert-manager --namespace cert-manager \
--version v0.16.0 \
--set installCRDs=true

helm repo add humio https://humio.github.io/cp-helm-charts
helm install --kubeconfig=$tmp_kubeconfig humio humio/cp-helm-charts --namespace=default \
--set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false \
--set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false \
--set cp-ksql-server.enabled=false --set cp-control-center.enabled=false

while [[ $(kubectl --kubeconfig=$tmp_kubeconfig get pods humio-cp-zookeeper-0 -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for humio-cp-zookeeper-0 pod to become Ready"
  sleep 10
done

while [[ $(kubectl --kubeconfig=$tmp_kubeconfig get pods humio-cp-kafka-0 -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for humio-cp-kafka-0 pod to become Ready"
  sleep 10
done
