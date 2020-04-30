FROM golang:1.13.9-alpine3.11

LABEL "com.github.actions.name"="operator-sdk"
LABEL "com.github.actions.description"="operator-sdk image builder"
LABEL "com.github.actions.icon"="layers"
LABEL "com.github.actions.color"="red"

ENV KUBECTL_VERSION=1.15.11
ENV KIND_VERSION=0.8.0
ENV RELEASE_VERSION=v0.17.0
ENV HELM_VERSION=3.2.0

RUN apk update \
    && apk upgrade \
    && apk add --no-cache bash curl git openssh make mercurial openrc docker python3 git \
    && pip3 install --upgrade pip setuptools

RUN curl -Lo ./kind "https://kind.sigs.k8s.io/dl/v${KIND_VERSION}/kind-$(uname)-amd64" && chmod +x ./kind && mv ./kind /usr/bin/kind

RUN curl --max-time 300 -o /usr/local/bin/kubectl -L https://storage.googleapis.com/kubernetes-release/release/v${KUBECTL_VERSION}/bin/linux/amd64/kubectl \
  && chmod 755 /usr/local/bin/kubectl

RUN curl -L https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz -o /tmp/helm.tar.gz && tar -zxvf /tmp/helm.tar.gz -C /tmp && mv /tmp/linux-amd64/helm /bin/helm && rm -rf /tmp/*

RUN pip3 install operator-courier

RUN curl -OJL https://github.com/operator-framework/operator-sdk/releases/download/${RELEASE_VERSION}/operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu \
    && chmod +x operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu \
    && cp operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu /usr/local/bin/operator-sdk \
    && rm operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu

COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
