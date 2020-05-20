#!/usr/bin/env bash

set -x

# Ensure we use the correct working directory:
cd ~/go/src/github.com/humio/humio-operator

# Clean up old stuff
kubectl --context kind-kind delete humiocluster humiocluster-sample
helm template humio ~/git/humio-cp-helm-charts --namespace=default --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false | kubectl --context kind-kind delete -f -
kubectl --context kind-kind get pvc | grep -v ^NAME | cut -f1 -d' ' | xargs -I{} kubectl --context kind-kind delete pvc {}
kind delete cluster --name kind

# Wait a bit before we start everything up again
sleep 5

# Create new kind cluster, deploy Kafka and run operator
#kind create cluster --name kind --image kindest/node:v1.15.7
kind create cluster --name kind --image kindest/node:v1.17.2
docker exec kind-control-plane sh -c 'echo nameserver 8.8.8.8 > /etc/resolv.conf'
docker exec kind-control-plane sh -c 'echo options ndots:0 >> /etc/resolv.conf'

# Pre-load confluent images
docker pull confluentinc/cp-enterprise-kafka:5.4.1
docker pull confluentinc/cp-zookeeper:5.4.1
docker pull docker.io/confluentinc/cp-enterprise-kafka:5.4.1
docker pull docker.io/confluentinc/cp-zookeeper:5.4.1
docker pull solsson/kafka-prometheus-jmx-exporter@sha256:6f82e2b0464f50da8104acd7363fb9b995001ddff77d248379f8788e78946143
kind load docker-image --name kind confluentinc/cp-enterprise-kafka:5.4.1
kind load docker-image --name kind docker.io/confluentinc/cp-zookeeper:5.4.1
kind load docker-image --name kind solsson/kafka-prometheus-jmx-exporter@sha256:6f82e2b0464f50da8104acd7363fb9b995001ddff77d248379f8788e78946143

# Pre-load humio images
docker pull humio/humio-core:1.10.2
kind load docker-image --name kind humio/humio-core:1.10.2

# Use helm 3 to start up Kafka and Zookeeper
mkdir ~/git
git clone https://github.com/humio/cp-helm-charts.git ~/git/humio-cp-helm-charts
helm template humio ~/git/humio-cp-helm-charts --namespace=default --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false | kubectl --context kind-kind apply -f -

# Install CRD
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_humioexternalclusters_crd.yaml
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_humioclusters_crd.yaml
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_humioingesttokens_crd.yaml
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_humioparsers_crd.yaml
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_humiorepositories_crd.yaml

# Create a CR instance of HumioCluster
sleep 10
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_v1alpha1_humioexternalcluster_cr.yaml
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_v1alpha1_humiocluster_cr.yaml
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_v1alpha1_humioingesttoken_cr.yaml
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_v1alpha1_humioparser_cr.yaml
kubectl --context kind-kind apply -f deploy/crds/core.humio.com_v1alpha1_humiorepository_cr.yaml
