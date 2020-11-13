#!/usr/bin/env bash

################################################################
# The purpose of this script is to test the following process: #
# 0. Delete existing Kubernetes cluster with kind              #
# 1. Spin up a kubernetes cluster with kind                    #
# 2. Start up cert-manager, Kafka and Zookeeper                #
# 3. Install humio-operator using Helm                         #
# 4. Create CR to test the operator behaviour                  #
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
declare -r operator_image_tag=local-$git_rev`date +%s`
declare -r operator_image=humio/humio-operator:${operator_image_tag}
declare -r helm_chart_dir=./charts/humio-operator
declare -r helm_chart_values_file=values.yaml
declare -r hack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Ensure we start from scratch
source ${hack_dir}/delete-kind-cluster.sh

# Wait a bit before we start everything up again
sleep 5

# Create new kind cluster
source ${hack_dir}/start-kind-cluster.sh

# Use helm to install cert-manager, Kafka and Zookeeper
source ${hack_dir}/install-helm-chart-dependencies-kind.sh

# Create a CR instance of HumioCluster
sleep 10

# Ensure we use the most recent CRD's
make manifests

# Build and pre-load the image into the cluster
make docker-build-operator IMG=$operator_image

kind load docker-image $operator_image

$kubectl create namespace $operator_namespace

helm upgrade --install humio-operator $helm_chart_dir \
  --namespace $operator_namespace \
  --set operator.image.tag=${operator_image_tag} \
  --set installCRDs=true \
  --values $helm_chart_dir/$helm_chart_values_file


sleep 10

$kubectl apply -f config/samples/core_v1alpha1_humiocluster.yaml

while [[ $($kubectl get humiocluster example-humiocluster -o 'jsonpath={..status.state}') != "Running" ]]
do
  echo "Waiting for example-humiocluster humiocluster to become Running"
  sleep 10
done
