#!/bin/bash

set -x

declare -r bin_dir=${BIN_DIR:-/usr/local/bin}


export PATH=$BIN_DIR:$PATH
# this is different because we do not specify kubeconfig and rely on crc login command to set up kubeconfig


helm repo add humio https://humio.github.io/cp-helm-charts
helm install humio humio/cp-helm-charts --namespace=default \
--set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false \
--set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false \
--set cp-ksql-server.enabled=false --set cp-control-center.enabled=false

while [[ $(oc get pods humio-cp-zookeeper-0 -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for humio-cp-zookeeper-0 pod to become Ready"
  sleep 10
done

while [[ $(oc get pods humio-cp-kafka-0 -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for humio-cp-kafka-0 pod to become Ready"
  sleep 10
done
