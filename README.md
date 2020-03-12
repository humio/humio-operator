# Humio-Operator

[![Build Status](https://github.com/humio/humio-operator/workflows/CI/badge.svg)](https://github.com/humio/humio-operator/actions?query=workflow%3ACI)

**WARNING: The CRD/API has yet to be defined. Everything as of this moment is considered experimental.**

The Humio operator is a Kubernetes operator to automate provisioning, management, ~~autoscaling~~ and operations of [Humio](https://humio.com) clusters deployed to Kubernetes.

## Terminology

- CRD: Short for Custom Resource Definition. This is a way to extend the API of Kubernetes to allow new types of objects with clearly defined properties.
- CR: Custom Resource. Where CRD is the definition of the objects and their available properties, a CR is a specific instance of such an object.
- Controller and Operator: These are common terms within the Kubernetes ecosystem and they are implementations that take a defined desired state (e.g. from a CR of our HumioCluster CRD), and ensure the current state matches it. They typically includes what is called a reconciliation loop to help continuously ensuring the health of the system.
- Reconciliation loop: This is a term used for describing the loop running within controllers/operators to keep ensuring current state matches the desired state.

## Example usage

This shows how we can currently leverage the Humio operator to provision a Humio cluster.

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: humiocluster-sample
spec:
  image: humio/humio-core
  version: "1.9.0"
  targetReplicationFactor: 2
```

