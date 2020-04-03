#!/usr/bin/env bash

set -x

# Ensure we use the correct working directory:
cd ~/go/src/github.com/humio/humio-operator

# Clean up old stuff
kubectl --context kind-kind delete -f deploy/operator.yaml
kubectl --context kind-kind delete -f deploy/role_binding.yaml
kubectl --context kind-kind delete -f deploy/service_account.yaml
kubectl --context kind-kind delete -f deploy/role.yaml

kubectl --context kind-kind delete humioingesttoken example-humioingesttoken
kubectl --context kind-kind delete humiocluster example-humiocluster
helm template humio ~/git/humio-cp-helm-charts --namespace=default --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false | kubectl --context kind-kind delete -f -
kubectl --context kind-kind get pvc | grep -v ^NAME | cut -f1 -d' ' | xargs -I{} kubectl --context kind-kind delete pvc {}
kind delete cluster --name kind
