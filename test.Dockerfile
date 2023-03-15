FROM ubuntu:20.04

# Install make and curl
RUN apt update \
 && apt install -y build-essential curl

# Create and populate /var/src with the source code for the humio-operator repository
RUN mkdir /var/src
COPY ./ /var/src
WORKDIR /var/src

# Install e2e dependencies
RUN /var/src/hack/install-e2e-dependencies.sh

# Install ginkgo
RUN cd /var/src \
 && make ginkgo
