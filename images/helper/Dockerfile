FROM golang:1.16 as builder

ARG RELEASE_VERSION=master
ARG RELEASE_COMMIT=none
ARG RELEASE_DATE=unknown

WORKDIR /src
COPY . /src
RUN CGO_ENABLED=0 go build -ldflags="-X 'main.version=$RELEASE_VERSION' -X 'main.commit=$RELEASE_COMMIT' -X 'main.date=$RELEASE_DATE'" -o /app /src/*.go

FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

LABEL "name"="humio-operator-helper"
LABEL "vendor"="humio"
LABEL "summary"="Humio Kubernetes Operator Helper"
LABEL "description"="Provides cluster and environmental information \
to the Humio pods in addition to faciliciting authentication bootstrapping \
for the Humio application."

# copy license
COPY LICENSE /licenses/LICENSE

COPY --from=builder /app /
ENTRYPOINT ["/app"]
