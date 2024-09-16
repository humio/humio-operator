FROM golang:1.23 AS builder

WORKDIR /app/humio
COPY . /app/humio
RUN go run "$(go env GOROOT)/src/crypto/tls/generate_cert.go" -host dummy
RUN chmod a+r key.pem
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/humio/dummy /app/humio/*.go