# Build the manager binary
FROM golang:1.16 as builder

ARG RELEASE_VERSION=master
ARG RELEASE_COMMIT=none
ARG RELEASE_DATE=unknown

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -ldflags="-X 'main.version=$RELEASE_VERSION' -X 'main.commit=$RELEASE_COMMIT' -X 'main.date=$RELEASE_DATE'" -a -o manager main.go

# Use ubi8 as base image to package the manager binary to comply with Red Hat image certification requirements
FROM registry.access.redhat.com/ubi8/ubi-minimal:latest
LABEL "name"="humio-operator"
LABEL "vendor"="humio"
LABEL "summary"="Humio Kubernetes Operator"
LABEL "description"="A Kubernetes operatator to run and maintain \
Humio clusters running in a Kubernetes cluster."

RUN microdnf update && \
    microdnf upgrade
RUN mkdir /licenses
COPY LICENSE /licenses/LICENSE

WORKDIR /
COPY --from=builder /workspace/manager .
USER 1001

ENTRYPOINT ["/manager"]
