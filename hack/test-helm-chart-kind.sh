#!/usr/bin/env bash

################################################################
# The purpose of this script is to test the following process: #
# 0. Delete existing Kubernetes cluster with kind              #
# 1. Spin up a kubernetes cluster with kind                    #
# 2. Start up Kafka and Zookeeper                              #
# 3. Install humio-operator using Helm                         #
# 4. Create CR's to test the operator behaviour                #
################################################################

# This script assumes you have installed the following tools:
# - Git: https://git-scm.com/book/en/v2/Getting-Started-Installing-Git
# - Helm v3: https://helm.sh/docs/intro/install/
# - Operator SDK: https://docs.openshift.com/container-platform/4.4/operators/operator_sdk/osdk-getting-started.html#osdk-installing-cli_osdk-getting-started
# - kubectl: https://kubernetes.io/docs/tasks/tools/install-kubectl/
# - kind: https://kind.sigs.k8s.io/docs/user/quick-start#installation


set -x

declare -r operator_namespace=${NAMESPACE:-default}
declare -r kubectl="kubectl --context kind-kind"
declare -r git_rev=$(git rev-parse --short HEAD)
declare -r operator_image=humio/humio-operator:local-$git_rev
declare -r helm_chart_dir=./charts/humio-operator
declare -r helm_chart_values_file=values.yaml

# Clean up old stuff
$kubectl delete humiocluster humiocluster-sample
helm template humio ~/git/humio-cp-helm-charts --namespace=$operator_namespace --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false | $kubectl delete -f -
$kubectl get pvc | grep -v ^NAME | cut -f1 -d' ' | xargs -I{} $kubectl delete pvc {}
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
docker pull humio/humio-core:1.13.4
kind load docker-image --name kind humio/humio-core:1.13.4

# Use helm 3 to install cert-manager
$kubectl create namespace cert-manager
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm install cert-manager jetstack/cert-manager --namespace cert-manager --version v0.16.0 --set installCRDs=true

# Use helm 3 to start up Kafka and Zookeeper
mkdir ~/git
git clone https://github.com/humio/cp-helm-charts.git ~/git/humio-cp-helm-charts
helm template humio ~/git/humio-cp-helm-charts --namespace=$operator_namespace --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false | $kubectl apply -f -

# Create a CR instance of HumioCluster
sleep 10

# Ensure we use the most recent CRD's
make crds

# Build and pre-load the image into the cluster
operator-sdk build humio/humio-operator:local-$git_rev

kind load docker-image humio/humio-operator:local-$git_rev

kubectl create namespace $operator_namespace

helm upgrade --install humio-operator $helm_chart_dir \
  --namespace $operator_namespace \
  --set operator.image.tag=local-$git_rev \
  --set installCRDs=true \
  --values $helm_chart_dir/$helm_chart_values_file


sleep 10

$kubectl apply -f deploy/crds/core.humio.com_v1alpha1_humioexternalcluster_cr.yaml
$kubectl apply -f deploy/crds/core.humio.com_v1alpha1_humiocluster_cr.yaml
$kubectl apply -f deploy/crds/core.humio.com_v1alpha1_humioingesttoken_cr.yaml
$kubectl apply -f deploy/crds/core.humio.com_v1alpha1_humioparser_cr.yaml
$kubectl apply -f deploy/crds/core.humio.com_v1alpha1_humiorepository_cr.yaml

while [[ $($kubectl get humiocluster example-humiocluster -o 'jsonpath={..status.state}') != "Running" ]]
do
  echo "Waiting for example-humiocluster humiocluster to become Running"
  sleep 10
done
