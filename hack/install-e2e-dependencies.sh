#!/usr/bin/env bash

set -ex

declare -r go_version=1.18.7
declare -r ginkgo_version=2.7.0
declare -r helm_version=3.8.0
declare -r kubectl_version=1.23.3
declare -r bin_dir=${BIN_DIR:-/usr/local/bin}

install_go() {
  curl -s https://dl.google.com/go/go${go_version}.linux-amd64.tar.gz | tar -xz -C /tmp
  ln -s /tmp/go/bin/go ${bin_dir}/go
}

install_helm() {
  curl -L https://get.helm.sh/helm-v${helm_version}-linux-amd64.tar.gz -o /tmp/helm.tar.gz \
    && tar -zxvf /tmp/helm.tar.gz -C /tmp \
    && mv /tmp/linux-amd64/helm ${bin_dir}/helm
}

install_kubectl() {
  curl -L https://dl.k8s.io/release/v${kubectl_version}/bin/linux/amd64/kubectl -o ${bin_dir}/kubectl \
    && chmod +x ${bin_dir}/kubectl
}

start=$(date +%s)

install_go
install_helm
install_kubectl

end=$(date +%s)
echo "Installed E2E dependencies took $((end-start)) seconds"
