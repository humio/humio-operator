#!/usr/bin/env bash

set -x

export PATH=$BIN_DIR:$PATH

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

kubectl patch clusterrolebinding cluster-admin --type='json' -p='[{"op": "add", "path": "/subjects/1", "value": {"kind": "ServiceAccount", "name": "default", "namespace": "default" } }]'
kubectl run test-pod --env="HUMIO_E2E_LICENSE=$HUMIO_E2E_LICENSE" --env="E2E_LOGS_HUMIO_HOSTNAME=$E2E_LOGS_HUMIO_HOSTNAME" --env="E2E_LOGS_HUMIO_INGEST_TOKEN=$E2E_LOGS_HUMIO_INGEST_TOKEN" --env="E2E_RUN_ID=$E2E_RUN_ID" --restart=Never --image=testcontainer --image-pull-policy=Never -- sleep 86400
while [[ $(kubectl get pods test-pod -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]; do echo "waiting for pod" ; kubectl describe pod test-pod ; sleep 1 ; done
kubectl exec test-pod -- hack/run-e2e-tests-kind.sh
