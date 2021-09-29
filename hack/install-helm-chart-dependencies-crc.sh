#!/usr/bin/env bash

set -x

declare -r tmp_kubeconfig=$HOME/.crc/machines/crc/kubeconfig

export PATH=$BIN_DIR:$PATH

eval $(crc oc-env)

oc --kubeconfig=$tmp_kubeconfig create namespace cert-manager
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install --kubeconfig=$tmp_kubeconfig cert-manager jetstack/cert-manager --namespace cert-manager \
--version v1.5.3 \
--set installCRDs=true

helm repo add humio https://humio.github.io/cp-helm-charts
helm install --kubeconfig=$tmp_kubeconfig humio humio/cp-helm-charts --namespace=default \
--set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false \
--set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false \
--set cp-ksql-server.enabled=false --set cp-control-center.enabled=false

while [[ $(oc --kubeconfig=$tmp_kubeconfig get pods humio-cp-zookeeper-0 -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for humio-cp-zookeeper-0 pod to become Ready"
  sleep 10
done

while [[ $(oc --kubeconfig=$tmp_kubeconfig get pods humio-cp-kafka-0 -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for humio-cp-kafka-0 pod to become Ready"
  sleep 10
done
