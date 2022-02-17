FROM ubuntu:20.04

# Install make and curl
RUN apt update \
 && apt install -y build-essential curl

# Install go
RUN curl -s https://dl.google.com/go/go1.17.7.linux-amd64.tar.gz | tar -xz -C /usr/local
RUN ln -s /usr/local/go/bin/go /usr/bin/go

# Install kind
RUN curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.11.1/kind-linux-amd64 \
 && chmod +x ./kind \
 && mv ./kind /usr/bin/kind

# Install docker-ce-cli
RUN apt-get install -y \
    apt-transport-https \
    ca-certificates \
    curl \
    gnupg \
    lsb-release
RUN curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
RUN echo \
  "deb [arch=amd64 signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu \
  $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
RUN apt-get update \
 && apt-get install -y docker-ce-cli

# Create and populate /var/src with the source code for the humio-operator repository
RUN mkdir /var/src
COPY ./ /var/src
WORKDIR /var/src

# Install e2e dependencies
RUN /var/src/hack/install-e2e-dependencies.sh
