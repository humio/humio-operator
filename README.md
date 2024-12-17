# Humio-Operator

[![Build Status](https://github.com/humio/humio-operator/workflows/CI/badge.svg)](https://github.com/humio/humio-operator/actions?query=workflow%3ACI)
[![Go Report Card](https://goreportcard.com/badge/github.com/humio/humio-operator)](https://goreportcard.com/report/github.com/humio/humio-operator)

The Humio operator is a Kubernetes operator to automate provisioning, management, ~~autoscaling~~ and operations of [Humio](https://humio.com) clusters deployed to Kubernetes.

## Terminology

- **CRD**: Short for Custom Resource Definition. This is a way to extend the API of Kubernetes to allow new types of objects with clearly defined properties.
- **CR**: Custom Resource. Where CRD is the definition of the objects and their available properties, a CR is a specific instance of such an object.
- **Controller and Operator**: These are common terms within the Kubernetes ecosystem and they are implementations that take a defined desired state (e.g. from a CR of our HumioCluster CRD), and ensure the current state matches it. They typically includes what is called a reconciliation loop to help continuously ensuring the health of the system.
- **Reconciliation loop**: This is a term used for describing the loop running within controllers/operators to keep ensuring current state matches the desired state.

## Installation

See the [Installation Guide](https://library.humio.com/falcon-logscale-self-hosted/installation-containers-kubernetes-operator-install.html). There is also a step-by-step [Quick Start](https://library.humio.com/falcon-logscale-self-hosted/installation-containers-kubernetes-operator-aws-install.html) guide that walks through creating a cluster on AWS.

## Running a Humio Cluster

See instructions and examples in the [Humio Operator Resources](https://library.humio.com/falcon-logscale-self-hosted/installation-containers-kubernetes-operator-resources.html) section of the docs.

## Development

### Unit Testing

Tests can be run by executing:

```bash
make test
```

### E2E Testing (Kubernetes)

We use [kind](https://kind.sigs.k8s.io/) for local testing.

Note that for running zookeeper and kafka locally, we currently rely on the [cp-helm-charts](https://github.com/humio/cp-helm-charts) and that that repository is cloned into a directory `~/git/humio-cp-helm-charts`.

Prerequisites:

- The environment variable `HUMIO_E2E_LICENSE` must be populated with a valid Humio license.

To run a E2E tests locally using `kind`, execute:

```bash
make run-e2e-tests-local-kind
```

## Publishing new releases

In order to publish new release of the different components, we have the following procedures we can follow:

- Operator container image: Bump the version defined in [VERSION](VERSION).
- Helm chart: Bump the version defined in [charts/humio-operator/Chart.yaml](charts/humio-operator/Chart.yaml).

Note: For now, we only release one component at a time due to how our workflows in GitHub Actions.

## License

[Apache License 2.0](https://github.com/humio/humio-operator/blob/master/LICENSE)
