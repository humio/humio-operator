#!/bin/bash

set -x

declare -r operator_namespace=${NAMESPACE:-humio-operator}
declare -r tmp_kubeconfig=/tmp/kubeconfig
declare -r kubectl="kubectl --kubeconfig $tmp_kubeconfig"
declare -r git_rev=$(git rev-parse --short HEAD)
declare -r operator_image=humio/humio-operator:$git_rev
declare -r bin_dir=${BIN_DIR:-/usr/local/bin}

cleanup() {
  $kubectl delete namespace $operator_namespace
  $kubectl delete -f deploy/cluster_role.yaml
  $kubectl delete -f deploy/cluster_role_binding.yaml
  docker rmi -f $operator_image
}

export PATH=$BIN_DIR:$PATH

trap cleanup EXIT

kind get kubeconfig > $tmp_kubeconfig

$kubectl create namespace $operator_namespace
$kubectl apply -f deploy/cluster_role.yaml
sed -e "s/namespace:.*/namespace: $operator_namespace/g" deploy/cluster_role_binding.yaml | $kubectl apply -f -

operator-sdk build $operator_image

kind load docker-image --name kind $operator_image

>/tmp/cr.yaml
for c in $(find deploy/crds/ -iname '*crd.yaml'); do
  echo "---" >> /tmp/cr.yaml
  cat $c >> /tmp/cr.yaml
done

operator-sdk test local ./test/e2e \
--global-manifest /tmp/cr.yaml \
--kubeconfig $tmp_kubeconfig \
--image=$operator_image \
--operator-namespace=$operator_namespace
