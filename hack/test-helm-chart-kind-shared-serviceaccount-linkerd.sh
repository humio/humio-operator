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
declare -r operator_image=humio/humio-operator:local-$git_rev
declare -r helm_chart_dir=./charts/humio-operator
declare -r helm_chart_values_file=values.yaml
declare -r hack_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v linkerd &> /dev/null
then
    echo "linkerd could not be found. It's a requirement for this script"
    exit
fi

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
  --set operator.image.tag=local-$git_rev \
  --set installCRDs=true \
  --values $helm_chart_dir/$helm_chart_values_file

# Install linkerd and verify the control plane is up and running
linkerd install | kubectl apply -f -
linkerd check

sleep 10

# As we opt out of the indiviual service account, we need to provide a service account, and correct roles for all containers

## Service Account to be used
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: humio
  namespace: default
EOF

cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: humio
  namespace: default
rules:
- apiGroups:
  - ""
  resources:
  - secrets
  verbs:
  - get
  - list
  - watch
  - create
  - update
  - delete
EOF

cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: humio
  namespace: default
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: humio
subjects:
- kind: ServiceAccount
  name: humio
  namespace: default
EOF

cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: humio
rules:
- apiGroups:
  - ""
  resources:
  - nodes
  verbs:
  - get
  - list
  - watch
EOF

cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: humio
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: humio
subjects:
- kind: ServiceAccount
  name: humio
  namespace: default
EOF


$kubectl apply -f config/samples/core_v1alpha1_humiocluster_shared_serviceaccount.yaml

while [[ $($kubectl get humiocluster example-humiocluster -o 'jsonpath={..status.state}') != "Running" ]]
do
  echo "Waiting for example-humiocluster humiocluster to become Running"
  sleep 10
done
