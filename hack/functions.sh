#!/usr/bin/env bash
declare -r kindest_node_image_multiplatform_amd64_arm64=${E2E_KIND_K8S_VERSION:-kindest/node:v1.33.1@sha256:050072256b9a903bd914c0b2866828150cb229cea0efe5892e2b644d5dd3b34f}
declare -r kind_version=0.29.0
declare -r go_version=1.23.6
declare -r helm_version=3.14.4
declare -r kubectl_version=1.23.3
declare -r jq_version=1.7.1
declare -r yq_version=4.45.2
declare -r default_cert_manager_version=1.12.12
declare -r bin_dir=$(pwd)/tmp
declare -r kubectl=$bin_dir/kubectl
declare -r helm=$bin_dir/helm
declare -r kind=$bin_dir/kind
declare -r jq=$bin_dir/jq
declare -r yq=$bin_dir/yq
declare -r go=$bin_dir/go

PATH=$bin_dir/goinstall/bin:$bin_dir:/usr/local/go/bin:$PATH
GOBIN=$bin_dir

start_kind_cluster() {
  if $kind get clusters | grep kind ; then
    if ! $kubectl get daemonset -n kube-system kindnet ; then
      echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
      exit 1
    fi

    return
  fi

  $kind create cluster --name kind --config hack/kind-config.yaml --image $kindest_node_image_multiplatform_amd64_arm64 --wait 300s

  sleep 5

  if ! $kubectl get daemonset -n kube-system kindnet ; then
    echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
    exit 1
  fi

  $kubectl patch clusterrolebinding cluster-admin --type='json' -p='[{"op": "add", "path": "/subjects/1", "value": {"kind": "ServiceAccount", "name": "default", "namespace": "default" } }]'
}

cleanup_kind_cluster() {
  if [[ $preserve_kind_cluster == "true" ]]; then
    $kubectl delete --grace-period=1 pod test-pod
    $kubectl delete -k config/crd/
  else
    $kind delete cluster --name kind
  fi
}

install_kind() {
  if [ -f $kind ]; then
    $kind version | grep -E "^kind v${kind_version}" && return
  fi

  if [ $(uname -o) = Darwin ]; then
    # For Intel Macs
    [ $(uname -m) = x86_64 ] && curl -Lo $kind https://kind.sigs.k8s.io/dl/v${kind_version}/kind-darwin-amd64
    # For M1 / ARM Macs
    [ $(uname -m) = arm64 ] && curl -Lo $kind https://kind.sigs.k8s.io/dl/v${kind_version}/kind-darwin-arm64
  else
    echo "Assuming Linux"
    # For AMD64 / x86_64
    [ $(uname -m) = x86_64 ] && curl -Lo $kind https://kind.sigs.k8s.io/dl/v${kind_version}/kind-linux-amd64
    # For ARM64
    [ $(uname -m) = aarch64 ] && curl -Lo $kind https://kind.sigs.k8s.io/dl/v${kind_version}/kind-linux-arm64
  fi
  chmod +x $kind
  $kind version
}

install_kubectl() {
  if [ -f $kubectl ]; then
    $kubectl version --client | grep "GitVersion:\"v${kubectl_version}\"" && return
  fi

  if [ $(uname -o) = Darwin ]; then
    # For Intel Macs
    [ $(uname -m) = x86_64 ] && curl -Lo $kubectl https://dl.k8s.io/release/v${kubectl_version}/bin/darwin/amd64/kubectl
    # For M1 / ARM Macs
    [ $(uname -m) = arm64 ] && curl -Lo $kubectl https://dl.k8s.io/release/v${kubectl_version}/bin/darwin/arm64/kubectl
  else
    echo "Assuming Linux"
    # For AMD64 / x86_64
    [ $(uname -m) = x86_64 ] && curl -Lo $kubectl https://dl.k8s.io/release/v${kubectl_version}/bin/linux/amd64/kubectl
    # For ARM64
    [ $(uname -m) = aarch64 ] && curl -Lo $kubectl https://dl.k8s.io/release/v${kubectl_version}/bin/linux/arm64/kubectl
  fi
  chmod +x $kubectl
  $kubectl version --client
}

install_helm() {
  if [ -f $helm ]; then
    $helm version --short | grep -E "^v${helm_version}" && return
  fi

  if [ $(uname -o) = Darwin ]; then
    # For Intel Macs
    [ $(uname -m) = x86_64 ] && curl -Lo $helm.tar.gz https://get.helm.sh/helm-v${helm_version}-darwin-amd64.tar.gz && tar -zxvf $helm.tar.gz -C $bin_dir && mv $bin_dir/darwin-amd64/helm $helm && rm -r $bin_dir/darwin-amd64
    # For M1 / ARM Macs
    [ $(uname -m) = arm64 ] && curl -Lo $helm.tar.gz https://get.helm.sh/helm-v${helm_version}-darwin-arm64.tar.gz && tar -zxvf $helm.tar.gz -C $bin_dir && mv $bin_dir/darwin-arm64/helm $helm && rm -r $bin_dir/darwin-arm64
  else
    echo "Assuming Linux"
    # For AMD64 / x86_64
    [ $(uname -m) = x86_64 ] && curl -Lo $helm.tar.gz https://get.helm.sh/helm-v${helm_version}-linux-amd64.tar.gz && tar -zxvf $helm.tar.gz -C $bin_dir && mv $bin_dir/linux-amd64/helm $helm && rm -r $bin_dir/linux-amd64
    # For ARM64
    [ $(uname -m) = aarch64 ] && curl -Lo $helm.tar.gz https://get.helm.sh/helm-v${helm_version}-linux-arm64.tar.gz && tar -zxvf $helm.tar.gz -C $bin_dir && mv $bin_dir/linux-arm64/helm $helm && rm -r $bin_dir/linux-arm64
  fi
  rm $helm.tar.gz
  chmod +x $helm
  $helm version
}

install_jq() {
  if [ $(uname -o) = Darwin ]; then
    # For Intel Macs
    [ $(uname -m) = x86_64 ] && curl -Lo $jq https://github.com/jqlang/jq/releases/download/jq-${jq_version}/jq-macos-amd64
    # For M1 / ARM Macs
    [ $(uname -m) = arm64 ] && curl -Lo $jq https://github.com/jqlang/jq/releases/download/jq-${jq_version}/jq-macos-arm64
  else
    echo "Assuming Linux"
    # For AMD64 / x86_64
    [ $(uname -m) = x86_64 ] && curl -Lo $jq https://github.com/jqlang/jq/releases/download/jq-${jq_version}/jq-linux-amd64
    # For ARM64
    [ $(uname -m) = aarch64 ] && curl -Lo $jq https://github.com/jqlang/jq/releases/download/jq-${jq_version}/jq-linux-arm64
  fi
  chmod +x $jq
  $jq --version
}

install_yq() {
  if [ $(uname -o) = Darwin ]; then
    # For Intel Macs
    [ $(uname -m) = x86_64 ] && curl -Lo $yq https://github.com/mikefarah/yq/releases/download/v${yq_version}/yq_darwin_amd64
    # For M1 / ARM Macs
    [ $(uname -m) = arm64 ] && curl -Lo $yq https://github.com/mikefarah/yq/releases/download/v${yq_version}/yq_darwin_arm64
  else
    echo "Assuming Linux"
    # For AMD64 / x86_64
    [ $(uname -m) = x86_64 ] && curl -Lo $yq https://github.com/mikefarah/yq/releases/download/v${yq_version}/yq_linux_amd64
    # For ARM64
    [ $(uname -m) = aarch64 ] && curl -Lo $yq https://github.com/mikefarah/yq/releases/download/v${yq_version}/yq_linux_arm64
  fi
  chmod +x $yq
  $yq --version
}

install_ginkgo() {
  go get github.com/onsi/ginkgo/v2/ginkgo
  go install github.com/onsi/ginkgo/v2/ginkgo
  ginkgo version
}

wait_for_pod() {
  while [[ $($kubectl get pods $@ -o 'jsonpath={..status.conditions[?(@.type=="Ready")].status}') != "True" ]]
  do
    echo "Waiting for pod to become Ready"
    $kubectl get pods -A
    $kubectl describe pod $@
    sleep 10
  done
}

preload_container_images() {
  if [[ $dummy_logscale_image == "true" ]]; then
    # Build dummy images and preload them
    make docker-build-dummy IMG=humio/humio-core:dummy
    make docker-build-helper IMG=humio/humio-operator-helper:dummy
    $kind load docker-image humio/humio-core:dummy &
    $kind load docker-image humio/humio-operator-helper:dummy &
    grep --only-matching --extended-regexp "humio/humio-core:[0-9.]+" internal/controller/versions/versions.go | awk '{print $1"-dummy"}' | xargs -I{} docker tag humio/humio-core:dummy {}
    grep --only-matching --extended-regexp "humio/humio-core:[0-9.]+" internal/controller/versions/versions.go | awk '{print $1"-dummy"}' | xargs -I{} kind load docker-image {}
    grep --only-matching --extended-regexp "humio/humio-operator-helper:[^\"]+" internal/controller/versions/versions.go | awk '{print $1"-dummy"}' | xargs -I{} docker tag humio/humio-operator-helper:dummy {}
    grep --only-matching --extended-regexp "humio/humio-operator-helper:[^\"]+" internal/controller/versions/versions.go | awk '{print $1"-dummy"}' | xargs -I{} kind load docker-image {}
  else
    # Extract container image tags used by tests from go source
    TEST_CONTAINER_IMAGES=$(grep 'Version\s*=\s*"' internal/controller/versions/versions.go | grep -v oldUnsupportedHumioVersion | grep -v 1.x.x | cut -d '"' -f 2 | sort -u)

    # Preload image used by e2e tests
    for image in $TEST_CONTAINER_IMAGES
    do
      $docker pull $image
      $kind load docker-image --name kind $image &
    done
  fi

  # Preload image we will run e2e tests from within
  $docker build --no-cache --pull -t testcontainer -f test.Dockerfile .
  $kind load docker-image testcontainer
}

helm_install_shippers() {
  $helm get metadata log-shipper && return

  # Install components to get observability during execution of tests
  if [[ $humio_hostname != "none" ]] && [[ $humio_ingest_token != "none" ]]; then
    e2eFilterTag=$(cat <<EOF
[FILTER]
    Name    modify
    Match   kube.*
    Set E2E_KIND_K8S_VERSION $kindest_node_image_multiplatform_amd64_arm64
    Set E2E_RUN_REF $e2e_run_ref
    Set E2E_RUN_ID $e2e_run_id
    Set E2E_RUN_ATTEMPT $e2e_run_attempt
    Set GINKGO_LABEL_FILTER $ginkgo_label_filter
EOF
)

    $helm repo add shipper https://humio.github.io/humio-helm-charts
    helm_install_command=(
      $helm install log-shipper shipper/humio-helm-charts
      --set humio-fluentbit.enabled=true
      --set humio-fluentbit.es.port=443
      --set humio-fluentbit.es.tls=true
      --set humio-fluentbit.humioRepoName=operator-e2e
      --set humio-fluentbit.humioHostname=$humio_hostname
      --set humio-fluentbit.token=$humio_ingest_token
      --set humio-metrics.enabled=true
      --set humio-metrics.es.port=9200
      --set humio-metrics.es.tls=true
      --set humio-metrics.es.tls_verify=true
      --set humio-metrics.es.autodiscovery=false
      --set humio-metrics.publish.enabled=false
      --set humio-metrics.humioHostname=$humio_hostname
      --set humio-metrics.token=$humio_ingest_token
    )

    if [[ $docker_username != "none" ]] && [[ $docker_password != "none" ]]; then
      helm_install_command+=(
        --set humio-fluentbit.imagePullSecrets[0].name=regcred
        --set humio-metrics.imagePullSecrets[0].name=regcred
      )
    fi
    ${helm_install_command[@]} --set humio-fluentbit.customFluentBitConfig.e2eFilterTag="$e2eFilterTag"
  fi
}

helm_install_cert_manager() {
  $helm get metadata cert-manager && return

  k8s_server_version=$($kubectl version --short=true | grep "Server Version:" | awk '{print $NF}' | sed 's/v//' | cut -d. -f1-2)
  cert_manager_version=v${default_cert_manager_version}
  if [[ ${k8s_server_version} < 1.27 ]] ; then cert_manager_version=v1.11.5 ; fi
  $helm repo add jetstack https://charts.jetstack.io
  $helm repo update
  helm_install_command=(
    $helm install cert-manager jetstack/cert-manager
    --version $cert_manager_version
    --set installCRDs=true
  )

  if [[ $docker_username != "none" ]] && [[ $docker_password != "none" ]]; then
    helm_install_command+=(--set global.imagePullSecrets[0].name=regcred)
  fi

  ${helm_install_command[@]}
}

helm_install_zookeeper_and_kafka() {
  $helm get metadata humio && return

  # Install test dependency: Zookeeper and Kafka
  $helm repo add --force-update humio https://humio.github.io/cp-helm-charts
  helm_install_command=(
    $helm install humio humio/cp-helm-charts
    --set cp-zookeeper.servers=1
    --set cp-zookeeper.prometheus.jmx.enabled=false
    --set cp-kafka.brokers=1
    --set cp-kafka.prometheus.jmx.enabled=false
    --set cp-schema-registry.enabled=false
    --set cp-kafka-rest.enabled=false
    --set cp-kafka-connect.enabled=false
    --set cp-ksql-server.enabled=false
    --set cp-control-center.enabled=false
  )

  if [[ $docker_username != "none" ]] && [[ $docker_password != "none" ]]; then
    helm_install_command+=(
      --set cp-zookeeper.imagePullSecrets[0].name=regcred
      --set cp-kafka.imagePullSecrets[0].name=regcred
    )
  fi

  ${helm_install_command[@]}
}

wait_for_kafka_ready () {
    local timeout=300  # 5 minutes
    local interval=10  # 10 seconds
    local elapsed=0

    zookeeper_ready=
    kafka_ready=

    while [ $elapsed -lt $timeout ]; do
      sleep $interval
      elapsed=$((elapsed + interval))
      if kubectl wait --for=condition=ready -l app=cp-zookeeper pod --timeout=30s; then
        zookeeper_ready="true"
      fi
      if kubectl wait --for=condition=ready -l app=cp-kafka pod --timeout=30s; then
        kafka_ready="true"
      fi
      if [ "${zookeeper_ready}" == "true" ] && [ "${kafka_ready}" == "true" ]; then
        sleep 2
        break
      fi
    done
}

kubectl_create_dockerhub_secret() {
  if [[ $docker_username != "none" ]] && [[ $docker_password != "none" ]]; then
    $kubectl create secret docker-registry regcred --docker-server="https://index.docker.io/v1/" --docker-username=$docker_username --docker-password=$docker_password
  fi
}