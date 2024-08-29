#!/usr/bin/env bash
declare -r kindest_node_image_multiplatform_amd64_arm64=${E2E_KIND_K8S_VERSION:-kindest/node:v1.29.2@sha256:51a1434a5397193442f0be2a297b488b6c919ce8a3931be0ce822606ea5ca245}
declare -r kind_version=0.22.0
declare -r go_version=1.22.2
declare -r helm_version=3.14.4
declare -r kubectl_version=1.23.3
declare -r default_cert_manager_version=1.12.12

declare -r bin_dir=$(pwd)/tmp
declare -r kubectl=$bin_dir/kubectl
declare -r helm=$bin_dir/helm
declare -r kind=$bin_dir/kind
declare -r go=$bin_dir/go

PATH=$bin_dir/goinstall/bin:$bin_dir:/usr/local/go/bin:$PATH
GOBIN=$bin_dir

start_kind_cluster() {
  $kind create cluster --name kind --image $kindest_node_image_multiplatform_amd64_arm64 --wait 300s

  sleep 5

  if ! $kubectl get daemonset -n kube-system kindnet ; then
    echo "Cluster unavailable or not using a kind cluster. Only kind clusters are supported!"
    exit 1
  fi

  $kubectl label node --overwrite --all topology.kubernetes.io/zone=az1
  $kubectl patch clusterrolebinding cluster-admin --type='json' -p='[{"op": "add", "path": "/subjects/1", "value": {"kind": "ServiceAccount", "name": "default", "namespace": "default" } }]'
}

cleanup_kind_cluster() {
  $kind delete cluster --name kind
}

install_kind() {
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

install_go() {
  if [ $(uname -o) = Darwin ]; then
    # For Intel Macs
    [ $(uname -m) = x86_64 ] && curl -Lo $go.tar.gz https://dl.google.com/go/go${go_version}.darwin-amd64.tar.gz && tar -zxvf $go.tar.gz -C $bin_dir && mv $bin_dir/go $bin_dir/goinstall && ln -s $bin_dir/goinstall/bin/go $go
    # For M1 / ARM Macs
    [ $(uname -m) = arm64 ] && curl -Lo $go.tar.gz https://dl.google.com/go/go${go_version}.darwin-arm64.tar.gz && tar -zxvf $go.tar.gz -C $bin_dir && mv $bin_dir/go $bin_dir/goinstall && ln -s $bin_dir/goinstall/bin/go $go
  else
    echo "Assuming Linux"
    # For AMD64 / x86_64
    [ $(uname -m) = x86_64 ] && curl -Lo $go.tar.gz https://dl.google.com/go/go${go_version}.linux-amd64.tar.gz && tar -zxvf $go.tar.gz -C $bin_dir && mv $bin_dir/go $bin_dir/goinstall && ln -s $bin_dir/goinstall/bin/go $go
    # For ARM64
    [ $(uname -m) = aarch64 ] && curl -Lo $go.tar.gz https://dl.google.com/go/go${go_version}.linux-arm64.tar.gz && tar -zxvf $go.tar.gz -C $bin_dir && mv $bin_dir/go $bin_dir/goinstall && ln -s $bin_dir/goinstall/bin/go $go
  fi
  rm $go.tar.gz
  $go version
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
    grep --only-matching --extended-regexp "humio/humio-core:[0-9.]+" controllers/versions/versions.go | awk '{print $1"-dummy"}' | xargs -I{} docker tag humio/humio-core:dummy {}
    grep --only-matching --extended-regexp "humio/humio-core:[0-9.]+" controllers/versions/versions.go | awk '{print $1"-dummy"}' | xargs -I{} kind load docker-image {}
    grep --only-matching --extended-regexp "humio/humio-operator-helper:[^\"]+" controllers/versions/versions.go | awk '{print $1"-dummy"}' | xargs -I{} docker tag humio/humio-operator-helper:dummy {}
    grep --only-matching --extended-regexp "humio/humio-operator-helper:[^\"]+" controllers/versions/versions.go | awk '{print $1"-dummy"}' | xargs -I{} kind load docker-image {}
  else
    # Extract container image tags used by tests from go source
    TEST_CONTAINER_IMAGES=$(grep 'Version\s*=\s*"' controllers/versions/versions.go | grep -v oldUnsupportedHumioVersion | grep -v 1.x.x | cut -d '"' -f 2 | sort -u)

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
  # Install test dependency: Zookeeper and Kafka
  $helm repo add humio https://humio.github.io/cp-helm-charts
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

kubectl_create_dockerhub_secret() {
  if [[ $docker_username != "none" ]] && [[ $docker_password != "none" ]]; then
    $kubectl create secret docker-registry regcred --docker-server="https://index.docker.io/v1/" --docker-username=$docker_username --docker-password=$docker_password
  fi
}
