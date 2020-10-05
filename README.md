# Humio-Operator

[![Build Status](https://github.com/humio/humio-operator/workflows/CI/badge.svg)](https://github.com/humio/humio-operator/actions?query=workflow%3ACI)
[![Go Report Card](https://goreportcard.com/badge/github.com/humio/humio-operator)](https://goreportcard.com/report/github.com/humio/humio-operator)

The Humio operator is a Kubernetes operator to automate provisioning, management, ~~autoscaling~~ and operations of [Humio](https://humio.com) clusters deployed to Kubernetes.

## Terminology

- CRD: Short for Custom Resource Definition. This is a way to extend the API of Kubernetes to allow new types of objects with clearly defined properties.
- CR: Custom Resource. Where CRD is the definition of the objects and their available properties, a CR is a specific instance of such an object.
- Controller and Operator: These are common terms within the Kubernetes ecosystem and they are implementations that take a defined desired state (e.g. from a CR of our HumioCluster CRD), and ensure the current state matches it. They typically includes what is called a reconciliation loop to help continuously ensuring the health of the system.
- Reconciliation loop: This is a term used for describing the loop running within controllers/operators to keep ensuring current state matches the desired state.

## Installation

See the [Installation Guide](https://docs.humio.com/installation/kubernetes/operator/installation). There is also a step-by-step [Quick Start](https://docs.humio.com/installation/kubernetes/operator/quick_start/) guide that walks through creating a cluster on AWS.

## Running a Humio Cluster

See instructions and examples in the [Humio Operator Resources](https://docs.humio.com/installation/kubernetes/operator/resources/) section of the docs.

## Development

### Unit Testing

Tests can be run by executing:

```bash
make test
```

### E2E Testing (Kubernetes)

We use [kind](https://kind.sigs.k8s.io/) for local testing.

Note that for running zookeeper and kafka locally, we currently rely on the [cp-helm-charts](https://github.com/humio/cp-helm-charts) and that that repository is cloned into a directory `~/git/humio-cp-helm-charts`.

To run a E2E tests locally using `kind`, execute:

```bash
make run-e2e-tests-local-kind
```

We also have a script to start up `kind` cluster, deploy to it with Helm and spin up a basic Humio cluster:

```bash
hack/test-helm-chart-crc.sh
```

To delete the `kind` cluster again, execute:

```bash
hack/stop-kind-cluster.sh
```

### E2E Testing (OpenShift)

We use [crc](https://developers.redhat.com/products/codeready-containers/overview) for local testing.

Note that for running zookeeper and kafka locally, we currently rely on the [cp-helm-charts](https://github.com/humio/cp-helm-charts) and that that repository is cloned into a directory `~/git/humio-cp-helm-charts`.

Prerequisites:

- Download the `crc` binary, make it executable and ensure it is in `$PATH`.
- Populate a file named `.crc-pull-secret.txt` in the root of the repository with your pull secret for `crc`.


To run a e2e tests locally using `crc`, execute:

```bash
make run-e2e-tests-local-crc
```

We also provide a script to start up `crc` cluster, deploy to it with Helm and spin up a basic Humio cluster:

```bash
hack/test-helm-chart-crc.sh
```

To delete the `crc` cluster again, execute:

```bash
hack/stop-crc-cluster.sh
```

## Publishing new releases

In order to publish new release of the different components, we have the following procedures we can follow:

- Operator container image: Bump the version defined in [VERSION](VERSION).
- Helper container image: Bump the version defined in [images/helper/version.go](images/helper/version.go).
- Helm chart: Bump the version defined in [charts/humio-operator/Chart.yaml](charts/humio-operator/Chart.yaml).

Note: For now, we only release one component at a time due to how our workflows in GitHub Actions.

## License

[Apache License 2.0](https://github.com/humio/humio-operator/blob/master/LICENSE)
