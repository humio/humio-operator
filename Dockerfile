# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

ARG BUILDPLATFORM
ARG TARGETPLATFORM
ARG RELEASE_VERSION=master
ARG RELEASE_COMMIT=none
ARG RELEASE_DATE=unknown

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# cache deps before building and copying source
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY internal/ internal/

# Build
RUN export GOOS=$(echo $TARGETPLATFORM | cut -d'/' -f1) \
    && export GOARCH=$(echo $TARGETPLATFORM | cut -d'/' -f2) \
    && CGO_ENABLED=0 GO111MODULE=on go build \
    -ldflags="-s -w -X 'main.version=$RELEASE_VERSION' -X 'main.commit=$RELEASE_COMMIT' -X 'main.date=$RELEASE_DATE'" \
    -a -o manager main.go

# Use scratch as base image
FROM scratch
LABEL "name"="humio-operator"
LABEL "vendor"="humio"
LABEL "summary"="Humio Kubernetes Operator"
LABEL "description"="A Kubernetes operatator to run and maintain \
Humio clusters running in a Kubernetes cluster."

COPY LICENSE /licenses/LICENSE

WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 1001

ENTRYPOINT ["/manager"]
