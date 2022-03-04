FROM golang:1.17 as builder

ARG RELEASE_VERSION=master
ARG RELEASE_COMMIT=none
ARG RELEASE_DATE=unknown

WORKDIR /src
COPY . /src
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X 'main.version=$RELEASE_VERSION' -X 'main.commit=$RELEASE_COMMIT' -X 'main.date=$RELEASE_DATE'" -o /app /src/*.go

FROM scratch

LABEL "name"="humio-operator-helper"
LABEL "vendor"="humio"
LABEL "summary"="Humio Kubernetes Operator Helper"
LABEL "description"="Provides cluster and environmental information \
to the Humio pods in addition to faciliciting authentication bootstrapping \
for the Humio application."

COPY LICENSE /licenses/LICENSE

COPY --from=builder /app /
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENTRYPOINT ["/app"]
