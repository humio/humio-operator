FROM golang:1.15.1-alpine3.12

LABEL "com.github.actions.name"="operator-sdk"
LABEL "com.github.actions.description"="operator-sdk image builder"
LABEL "com.github.actions.icon"="layers"
LABEL "com.github.actions.color"="red"

ENV RELEASE_VERSION=v1.3.0
ENV OPERATOR_COURIER_VERSION=2.1.10

RUN apk update \
    && apk upgrade \
    && apk add --no-cache bash curl git openssh make mercurial openrc docker python3 git py-pip gcc \
    && pip3 install --upgrade pip setuptools

RUN pip3 install operator-courier==${OPERATOR_COURIER_VERSION}

RUN curl -OJL https://github.com/operator-framework/operator-sdk/releases/download/${RELEASE_VERSION}/operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu \
    && chmod +x operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu \
    && cp operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu /usr/local/bin/operator-sdk \
    && rm operator-sdk-${RELEASE_VERSION}-x86_64-linux-gnu

COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
