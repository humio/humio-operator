#!/usr/bin/env bash

set -x

# Ensure we use the correct working directory and KUBECONFIG:
cd ~/go/src/github.com/humio/humio-operator
export KUBECONFIG="$(kind get kubeconfig-path --name="kind")"

# Clean up old stuff
kubectl delete humiocluster humiocluster-sample
helm template ~/git/cp-helm-charts --name humio --namespace=default --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false | kubectl delete -f -
kubectl get pvc | grep -v ^NAME | cut -f1 -d' ' | xargs -I{} kubectl delete pvc {}
kind delete cluster
