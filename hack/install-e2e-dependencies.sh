#!/usr/bin/env bash

set -ex

declare -r helm_version=3.5.4
declare -r operator_sdk_version=1.7.1
declare -r telepresence_version=2.2.1
declare -r bin_dir=${BIN_DIR:-/usr/local/bin}

install_helm() {
  curl -L https://get.helm.sh/helm-v${helm_version}-linux-amd64.tar.gz -o /tmp/helm.tar.gz \
    && tar -zxvf /tmp/helm.tar.gz -C /tmp \
    && mv /tmp/linux-amd64/helm ${bin_dir}/helm
}

install_operator_sdk() {
  curl -OJL https://github.com/operator-framework/operator-sdk/releases/download/v${operator_sdk_version}/operator-sdk-v${operator_sdk_version}-x86_64-linux-gnu \
    && chmod +x operator-sdk-v${operator_sdk_version}-x86_64-linux-gnu \
    && cp operator-sdk-v${operator_sdk_version}-x86_64-linux-gnu ${bin_dir}/operator-sdk \
    && rm operator-sdk-v${operator_sdk_version}-x86_64-linux-gnu
}

install_telepresence() {
  curl -fL https://app.getambassador.io/download/tel2/linux/amd64/${telepresence_version}/telepresence -o ${bin_dir}/telepresence \
    && chmod a+x ${bin_dir}/telepresence
}

install_ginkgo() {
  go get github.com/onsi/ginkgo/ginkgo
  go get github.com/onsi/gomega/...
}

install_helm
install_operator_sdk
install_telepresence
install_ginkgo
