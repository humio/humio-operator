#!/usr/bin/env bash

################################################################
# The purpose of this script is to test the following process: #
# 0. Delete existing OpenShift cluster with crc                #
# 1. Spin up an OpenShift cluster with crc                     #
# 2. Start up cert-manager, Kafka and Zookeeper                #
# 3. Install humio-operator using Helm                         #
# 4. Create CR to test the operator behaviour                  #
################################################################

# This script assumes you have installed the following tools:
# - Git: https://git-scm.com/book/en/v2/Getting-Started-Installing-Git
# - Helm v3: https://helm.sh/docs/intro/install/
# - Operator SDK: https://docs.openshift.com/container-platform/4.5/operators/operator_sdk/osdk-getting-started.html#osdk-installing-cli_osdk-getting-started
# - OpenShift CLI: https://docs.openshift.com/container-platform/4.5/cli_reference/openshift_cli/getting-started-cli.html#installing-the-cli
# - Red Hat CodeReady Containers: https://developers.redhat.com/products/codeready-containers/overview
#   - NOTE: You have put a file named `.crc-pull-secret.txt` in the root of the humio-operator Git repository.

set -x

declare -r operator_namespace=${NAMESPACE:-default}
declare -r tmp_kubeconfig=$HOME/.crc/machines/crc/kubeconfig
declare -r kubectl="oc --kubeconfig $tmp_kubeconfig"
declare -r git_rev=$(git rev-parse --short HEAD)
declare -r operator_image=humio/humio-operator:local-$git_rev
declare -r helm_chart_dir=./charts/humio-operator
declare -r helm_chart_values_file=values.yaml
declare -r hack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Script needs rework after we're no longer using Telepresence. Aborting..."
exit 1

# Ensure we start from scratch
source ${hack_dir}/delete-crc-cluster.sh

# Wait a bit before we start everything up again
sleep 5

# Create new crc cluster
source ${hack_dir}/start-crc-cluster.sh

# Use helm to install cert-manager, Kafka and Zookeeper
source ${hack_dir}/install-helm-chart-dependencies-crc.sh

# Create a CR instance of HumioCluster
sleep 10

# Ensure we use the most recent CRD's
make manifests

# Build and pre-load the image into the cluster
make docker-build-operator IMG=$operator_image
# TODO: Figure out how to use the image without pushing the image to Docker Hub
make docker-push IMG=$operator_image

$kubectl create namespace $operator_namespace

helm upgrade --install humio-operator $helm_chart_dir \
  --namespace $operator_namespace \
  --set operator.image.tag=local-$git_rev \
  --set installCRDs=true \
  --set openshift=true \
  --values $helm_chart_dir/$helm_chart_values_file

sleep 10

$kubectl apply -f config/samples/core_v1alpha1_humiocluster.yaml

while [[ $($kubectl get humiocluster example-humiocluster -o 'jsonpath={..status.state}') != "Running" ]]
do
  echo "Waiting for example-humiocluster humiocluster to become Running"
  sleep 10
done
