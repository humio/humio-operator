FROM golang:1.14.3 as builder
WORKDIR /src
COPY . /src
RUN CGO_ENABLED=0 go build -o /app /src/*.go

FROM registry.access.redhat.com/ubi8/ubi-minimal

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
