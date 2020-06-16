FROM golang:1.14.3 as builder
WORKDIR /src
COPY . /src
RUN CGO_ENABLED=0 go build -o /app /src/*.go

FROM registry.access.redhat.com/ubi8/ubi-minimal
COPY --from=builder /app /
ENTRYPOINT ["/app"]
