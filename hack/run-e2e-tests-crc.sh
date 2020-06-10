#!/bin/bash

set -x

declare -r operator_namespace=${NAMESPACE:-humio-operator}
declare -r kubectl="oc --context default/api-crc-testing:6443/kube:admin"
declare -r git_rev=$(git rev-parse --short HEAD)
declare -r operator_image=humio/humio-operator:local-$git_rev
declare -r bin_dir=${BIN_DIR:-/usr/local/bin}
declare -r namespaced_manifest=/tmp/namespaced.yaml
declare -r global_manifest=/tmp/global.yaml
declare -r helm_chart_dir=./charts/humio-operator
declare -r helm_chart_values_file=values.yaml


cleanup() {
  $kubectl delete namespace $operator_namespace
  docker rmi -f $operator_image
}

export PATH=$BIN_DIR:$PATH

trap cleanup EXIT

eval $(crc oc-env)
eval $(crc console --credentials | grep "To login as an admin, run" | cut -f2 -d"'")

$kubectl create namespace $operator_namespace

operator-sdk build $operator_image

# TODO: Figure out how to use the image without pushing the image to Docker Hub
docker push $operator_image

# Populate global.yaml with CRD's, ClusterRole, ClusterRoleBinding (and SecurityContextConstraints for OpenShift, though SecurityContextConstraint should be moved to code as they should be managed on a per-cluster basis)
>$global_manifest
make crds
grep -v "{{" ./charts/humio-operator/templates/crds.yaml >> $global_manifest
for JSON in $(
  helm template humio-operator $helm_chart_dir --set installCRDs=true --namespace $operator_namespace -f $helm_chart_dir/$helm_chart_values_file | \
  $kubectl apply --dry-run --selector=operator-sdk-test-scope=per-operator -o json -f - | \
  jq -c '.items[]'
)
do
  echo -E $JSON | \
  python -c 'import sys, yaml, json; j=json.loads(sys.stdin.read()); print("---") ; print(yaml.safe_dump(j))' | \
  grep -vE "resourceVersion"
done >> $global_manifest

# namespaced.yaml should be: service_account, role, role_binding, deployment
>$namespaced_manifest
for JSON in $(
  helm template humio-operator $helm_chart_dir --set operator.image.tag=local-$git_rev --set installCRDs=true --namespace $operator_namespace -f $helm_chart_dir/$helm_chart_values_file | \
  $kubectl apply --dry-run --selector=operator-sdk-test-scope=per-test -o json -f - | \
  jq -c '.items[]'
)
do
  echo -E $JSON | \
  python -c 'import sys, yaml, json; j=json.loads(sys.stdin.read()); print("---") ; print(yaml.safe_dump(j))' | \
  grep -vE "resourceVersion"
done >> $namespaced_manifest

# NB: The YAML files cannot contain unnamed "List" objects as the parsing with operator-sdk failes with that.

operator-sdk test local ./test/e2e \
--global-manifest=$global_manifest \
--namespaced-manifest=$namespaced_manifest \
--operator-namespace=$operator_namespace

