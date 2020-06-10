#!/usr/bin/env bash

################################################################
# The purpose of this script is to test the following process: #
# 0. Delete existing OpenShift cluster with crc                #
# 1. Spin up an OpenShift cluster with crc                     #
# 2. Start up Kafka and Zookeeper                              #
# 3. Install humio-operator using Helm                         #
# 4. Create CR's to test the operator behaviour                #
################################################################

# This script assumes you have installed the following tools:
# - Git: https://git-scm.com/book/en/v2/Getting-Started-Installing-Git
# - Helm v3: https://helm.sh/docs/intro/install/
# - Operator SDK: https://docs.openshift.com/container-platform/4.4/operators/operator_sdk/osdk-getting-started.html#osdk-installing-cli_osdk-getting-started
# - OpenShift CLI: https://docs.openshift.com/container-platform/4.4/cli_reference/openshift_cli/getting-started-cli.html#installing-the-cli
# - Red Hat CodeReady Containers: https://developers.redhat.com/products/codeready-containers/overview
#   - You have put a file named `.crc-pull-secret.txt` in the root of the humio-operator Git repository.

set -x

declare -r operator_namespace=${NAMESPACE:-default}
declare -r kubectl="oc --context default/api-crc-testing:6443/kube:admin"
declare -r git_rev=$(git rev-parse --short HEAD)
declare -r operator_image=humio/humio-operator:local-$git_rev
declare -r helm_chart_dir=./charts/humio-operator
declare -r helm_chart_values_file=values.yaml

# Clean up old stuff
$kubectl delete humiocluster humiocluster-sample
helm template humio ~/git/humio-cp-helm-charts --namespace=$operator_namespace --set cp-zookeeper.servers=1 --set cp-kafka.brokers=1 --set cp-schema-registry.enabled=false --set cp-kafka-rest.enabled=false --set cp-kafka-connect.enabled=false --set cp-ksql-server.enabled=false --set cp-control-center.enabled=false | $kubectl delete -f -
$kubectl get pvc | grep -v ^NAME | cut -f1 -d' ' | xargs -I{} $kubectl delete pvc {}
crc delete --force

# Wait a bit before we start everything up again
sleep 5

# Create new kind cluster, deploy Kafka and run operator
crc setup
crc start --pull-secret-file=.crc-pull-secret.txt
eval $(crc oc-env)
eval $(crc console --credentials | grep "To login as an admin, run" | cut -f2 -d"'")

# Pre-load confluent images
#docker pull confluentinc/cp-enterprise-kafka:5.4.1
#docker pull confluentinc/cp-zookeeper:5.4.1
#docker pull docker.io/confluentinc/cp-enterprise-kafka:5.4.1
#docker pull docker.io/confluentinc/cp-zookeeper:5.4.1
#docker pull solsson/kafka-prometheus-jmx-exporter@sha256:6f82e2b0464f50da8104acd7363fb9b995001ddff77d248379f8788e78946143
#oc import-image confluentinc/cp-enterprise-kafka:5.4.1
#oc import-image docker.io/confluentinc/cp-zookeeper:5.4.1
#oc import-image solsson/kafka-prometheus-jmx-exporter@sha256:6f82e2b0464f50da8104acd7363fb9b995001ddff77d248379f8788e78946143

# Pre-load humio images
#docker pull humio/humio-core:1.12.0
#oc import-image humio/humio-core:1.12.0

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
# TODO: Figure out how to use the image without pushing the image to Docker Hub
docker push humio/humio-operator:local-$git_rev

oc create namespace $operator_namespace

helm upgrade --install humio-operator $helm_chart_dir \
  --namespace $operator_namespace \
  --set operator.image.tag=local-$git_rev \
  --set installCRDs=true \
  --set openshift=true \
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
