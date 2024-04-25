FROM golang:1.22.2

# Create and populate /var/src with the source code for the humio-operator repository
RUN mkdir /var/src
COPY ./ /var/src
WORKDIR /var/src

RUN bash -c "rm -rf /var/src/tmp/*"
RUN bash -c "source /var/src/hack/functions.sh && install_ginkgo"
