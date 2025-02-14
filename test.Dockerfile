# syntax=docker/dockerfile:1.7-labs
FROM golang:1.23.6-alpine

RUN apk add bash

# Create and populate /var/src with the source code for the humio-operator repository
RUN mkdir /var/src
COPY --exclude=tmp --exclude=bin ./ /var/src
WORKDIR /var/src

RUN bash -c "rm -rf /var/src/tmp/*"
RUN bash -c "source /var/src/hack/functions.sh && install_ginkgo"
