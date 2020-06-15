# Humio-Operator

[![Build Status](https://github.com/humio/humio-operator/workflows/CI/badge.svg)](https://github.com/humio/humio-operator/actions?query=workflow%3ACI)
[![Go Report Card](https://goreportcard.com/badge/github.com/humio/humio-operator)](https://goreportcard.com/report/github.com/humio/humio-operator)


**WARNING: The CRD/API has yet to be defined. Everything as of this moment is considered experimental.**

The Humio operator is a Kubernetes operator to automate provisioning, management, ~~autoscaling~~ and operations of [Humio](https://humio.com) clusters deployed to Kubernetes.

## Terminology

- CRD: Short for Custom Resource Definition. This is a way to extend the API of Kubernetes to allow new types of objects with clearly defined properties.
- CR: Custom Resource. Where CRD is the definition of the objects and their available properties, a CR is a specific instance of such an object.
- Controller and Operator: These are common terms within the Kubernetes ecosystem and they are implementations that take a defined desired state (e.g. from a CR of our HumioCluster CRD), and ensure the current state matches it. They typically includes what is called a reconciliation loop to help continuously ensuring the health of the system.
- Reconciliation loop: This is a term used for describing the loop running within controllers/operators to keep ensuring current state matches the desired state.

## Prerequisites

The Humio Operator expects a running Zookeeper and Kafka. There are many ways to run Zookeeper and Kafka but generally a good choice is the [Banzai Cloud Kafka Operator](https://operatorhub.io/operator/banzaicloud-kafka-operator). They also recommend using [Pravega's Zookeeper Operator](https://github.com/pravega/zookeeper-operator). If you are running in AWS, we generally recommend the MSK service.

## Installation

See [charts/humio-operator/README.md](charts/humio-operator/README.md).

## Running a Humio Cluster

See instructions at [docs/README.md](docs/README.md) and examples of custom resources at [examples/](examples/).

## Development

### Unit Testing

Tests can be run by executing:

```bash
make test
```

### E2E Testing (Kubernetes)

We use [kind](https://kind.sigs.k8s.io/) for local testing.

Note that for running zookeeper and kafka locally, we currently rely on the [cp-helm-charts](https://github.com/humio/cp-helm-charts) and that that repository is cloned into a directory `~/git/humio-cp-helm-charts`.

To run a e2e test locally using `kind`, execute:

```bash
make run-e2e-tests-local-kind
```

To stop the `kind` cluster again, execute:

```bash
hack/stop-kind.sh
```

### E2E Testing (OpenShift)

We use [crc](https://developers.redhat.com/products/codeready-containers/overview) for local testing.

Note that for running zookeeper and kafka locally, we currently rely on the [cp-helm-charts](https://github.com/humio/cp-helm-charts) and that that repository is cloned into a directory `~/git/humio-cp-helm-charts`.

Prerequisites:

- Download the `crc` binary, make it executable and ensure it is in `$PATH`.
- Populate a file named `.crc-pull-secret.txt` in the root of the repository with your pull secret for `crc`.


To run a e2e test locally using `crc`, execute:

```bash
make run-e2e-tests-local-crc
```

To stop the `crc` cluster again, execute:

```bash
hack/stop-crc.sh
```

## Publishing new releases

- Container image: Bump the version defined in [version/version.go](version/version.go).
- Helm chart: Bump the version defined in [charts/humio-operator/Chart.yaml](charts/humio-operator/Chart.yaml).
