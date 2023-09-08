#!/usr/bin/env bash

set -x

declare -r e2e_kind_k8s_version=${E2E_KIND_K8S_VERSION:-unknown}
declare -r e2e_run_ref=${GITHUB_REF:-outside-github-$(hostname)}
declare -r e2e_run_id=${GITHUB_RUN_ID:-none}
declare -r e2e_run_attempt=${GITHUB_RUN_ATTEMPT:-none}
declare -r humio_hostname=${E2E_LOGS_HUMIO_HOSTNAME:-none}
declare -r humio_ingest_token=${E2E_LOGS_HUMIO_INGEST_TOKEN:-none}

export PATH=$BIN_DIR:$PATH

start=$(date +%s)

if ! kubectl get daemonset -n kube-system kindnet ; then
  echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
  exit 1
fi

if [[ $DOCKER_USERNAME != "" ]] && [[ $DOCKER_PASSWORD != "" ]]; then
  kubectl create secret docker-registry regcred --docker-server="https://index.docker.io/v1/" --docker-username=$DOCKER_USERNAME --docker-password=$DOCKER_PASSWORD --namespace default
fi

if [[ $humio_hostname != "none" ]] && [[ $humio_ingest_token != "none" ]]; then

  export E2E_FILTER_TAG=$(cat <<EOF
[FILTER]
    Name    modify
    Match   kube.*
    Set E2E_KIND_K8S_VERSION $e2e_kind_k8s_version
    Set E2E_RUN_REF $e2e_run_ref
    Set E2E_RUN_ID $e2e_run_id
    Set E2E_RUN_ATTEMPT $e2e_run_attempt
EOF
)

  helm repo add shipper https://humio.github.io/humio-helm-charts
  helm_install_command="helm install log-shipper shipper/humio-helm-charts --namespace=default \
  --set humio-fluentbit.enabled=true \
  --set humio-fluentbit.es.port=443 \
  --set humio-fluentbit.es.tls=true \
  --set humio-fluentbit.humioRepoName=operator-e2e \
  --set humio-fluentbit.humioHostname=$humio_hostname \
  --set humio-fluentbit.token=$humio_ingest_token \
  --set humio-metrics.enabled=true \
  --set humio-metrics.es.port=9200 \
  --set humio-metrics.es.tls=true \
  --set humio-metrics.es.tls_verify=true \
  --set humio-metrics.es.autodiscovery=false \
  --set humio-metrics.publish.enabled=false \
  --set humio-metrics.humioHostname=$humio_hostname \
  --set humio-metrics.token=$humio_ingest_token"

  if [[ $DOCKER_USERNAME != "" ]] && [[ $DOCKER_PASSWORD != "" ]]; then
    helm_install_command="${helm_install_command} \
    --set humio-fluentbit.imagePullSecrets[0].name=regcred \
    --set humio-metrics.imagePullSecrets[0].name=regcred"
  fi
  # $E2E_FILTER_TAG is specified here rather than in $helm_install_command due to issues with variable expansion
  $helm_install_command --set humio-fluentbit.customFluentBitConfig.e2eFilterTag="$E2E_FILTER_TAG"
fi

kubectl create namespace cert-manager
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm_install_command="helm install cert-manager jetstack/cert-manager --namespace cert-manager \
--version v1.10.2 \
--set installCRDs=true"

if [[ $DOCKER_USERNAME != "" ]] && [[ $DOCKER_PASSWORD != "" ]]; then
  kubectl create secret docker-registry regcred --docker-server="https://index.docker.io/v1/" --docker-username=$DOCKER_USERNAME --docker-password=$DOCKER_PASSWORD --namespace cert-manager
  helm_install_command="${helm_install_command} \
  --set global.imagePullSecrets[0].name=regcred"
fi

$helm_install_command

helm repo add humio https://humio.github.io/cp-helm-charts
helm_install_command="helm install humio humio/cp-helm-charts --namespace=default \
--set cp-zookeeper.servers=1 --set cp-zookeeper.prometheus.jmx.enabled=false \
--set cp-kafka.brokers=1 --set cp-kafka.prometheus.jmx.enabled=false \
--set cp-schema-registry.enabled=false \
--set cp-kafka-rest.enabled=false \
--set cp-kafka-connect.enabled=false \
--set cp-ksql-server.enabled=false \
--set cp-control-center.enabled=false"

if [[ $DOCKER_USERNAME != "" ]] && [[ $DOCKER_PASSWORD != "" ]]; then
  helm_install_command="${helm_install_command} \
  --set cp-zookeeper.imagePullSecrets[0].name=regcred \
  --set cp-kafka.imagePullSecrets[0].name=regcred"
fi

$helm_install_command

while [[ $(kubectl get pods -n default humio-cp-zookeeper-0 -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for humio-cp-zookeeper-0 pod to become Ready"
  kubectl get pods -A
  kubectl describe pod -n default humio-cp-zookeeper-0
  sleep 10
done

while [[ $(kubectl get pods -n default humio-cp-kafka-0 -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for humio-cp-kafka-0 pod to become Ready"
  kubectl get pods -A
  kubectl describe pod -n default humio-cp-kafka-0
  sleep 10
done

while [[ $(kubectl get pods -n cert-manager -l app.kubernetes.io/name=cert-manager -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for cert-manager pod to become Ready"
  kubectl get pods -n cert-manager
  kubectl describe pod -n cert-manager -l app.kubernetes.io/name=cert-manager
  sleep 10
done

while [[ $(kubectl get pods -n cert-manager -l app.kubernetes.io/name=cainjector -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for cert-manager cainjector pod to become Ready"
  kubectl get pods -n cert-manager
  kubectl describe pod -n cert-manager -l app.kubernetes.io/name=cainjector
  sleep 10
done

while [[ $(kubectl get pods -n cert-manager -l app.kubernetes.io/name=webhook -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
do
  echo "Waiting for cert-manager webhook pod to become Ready"
  kubectl get pods -n cert-manager
  kubectl describe pod -n cert-manager -l app.kubernetes.io/name=webhook
  sleep 10
done

end=$(date +%s)
echo "Installing Helm chart dependencies took $((end-start)) seconds"
