# Humio-Operator

[![Build Status](https://github.com/humio/humio-operator/workflows/CI/badge.svg)](https://github.com/humio/humio-operator/actions?query=workflow%3ACI)

**WARNING: The CRD/API has yet to be defined. Everything as of this moment is considered experimental.**

The Humio operator is a Kubernetes operator to automate provisioning, management, ~~autoscaling~~ and operations of [Humio](https://humio.com) clusters deployed to Kubernetes.

## Terminology

- CRD: Short for Custom Resource Definition. This is a way to extend the API of Kubernetes to allow new types of objects with clearly defined properties.
- CR: Custom Resource. Where CRD is the definition of the objects and their available properties, a CR is a specific instance of such an object.
- Controller and Operator: These are common terms within the Kubernetes ecosystem and they are implementations that take a defined desired state (e.g. from a CR of our HumioCluster CRD), and ensure the current state matches it. They typically includes what is called a reconciliation loop to help continuously ensuring the health of the system.
- Reconciliation loop: This is a term used for describing the loop running within controllers/operators to keep ensuring current state matches the desired state.

## Prerequisites

The Humio Operator expects a running Zookeeper and Kafka. There are many ways to run Zookeeper and Kafka but generally a good choice is the [Banzai Cloud Kafka Operator](https://operatorhub.io/operator/banzaicloud-kafka-operator). They also recommend using [Pravega's Zookeeper Operator](https://github.com/pravega/zookeeper-operator). If you are running in AWS, we generally recommend the MSK service.

## Example usage

This shows how we can currently leverage the Humio operator to provision a Humio cluster.

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: humiocluster-sample
spec:
  image: "humio/humio-core:1.9.2"
  environmentVariables:
    - name: "ZOOKEEPER_URL"
      value: "<zookeeper url>"
    - name: "KAFKA_SERVERS"
      value: "<kafka url>"
```

For a full list of examples, see the [examples directory](https://github.com/humio/humio-operator/tree/master/examples).

## Development

### Local Cluster

We use [kind](https://kind.sigs.k8s.io/) for local testing.

Note that for running zookeeper and kafka locally, we currently rely on the [cp-helm-charts](https://github.com/humio/cp-helm-charts) and that repository is cloned into a directory `~/git/humio-cp-helm-charts`.

To run a local cluster using kind, execute:

```bash
./hack/restart-k8s.sh 
```

Once the cluster is up, run the operator by executing:
```bash
./hack/run-operator.sh
```

### Testing

Tests can be run by executing:

```bash
make test
```
