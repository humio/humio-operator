FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

LABEL "name"="humio-operator"
LABEL "vendor"="humio"
LABEL "summary"="Humio Kubernetes Operator"
LABEL "description"="A Kubernetes operatator to run and maintain \
Humio clusters running in a Kubernetes cluster."

ENV OPERATOR=/usr/local/bin/humio-operator \
    USER_UID=1001 \
    USER_NAME=humio-operator

# install operator binary
COPY build/_output/bin/humio-operator ${OPERATOR}

# copy license
COPY LICENSE /licenses/LICENSE

COPY build/bin /usr/local/bin
RUN  /usr/local/bin/user_setup

ENTRYPOINT ["/usr/local/bin/entrypoint"]

USER ${USER_UID}
