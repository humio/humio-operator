FROM golang:1.13.9-alpine3.11

LABEL "com.github.actions.name"="operator-sdk"
LABEL "com.github.actions.description"="operator-sdk image builder"
LABEL "com.github.actions.icon"="layers"
LABEL "com.github.actions.color"="red"

RUN apk update \
    && apk upgrade \
    && apk add --no-cache bash curl git openssh make mercurial openrc docker

ARG RELEASE_VERSION=v0.16.0

RUN curl -OJL https://github.com/operator-framework/operator-sdk/releases/download/${RELEASE_VERSION}/operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu \
    && chmod +x operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu \
    && cp operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu /usr/local/bin/operator-sdk \
    && rm operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu

COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]