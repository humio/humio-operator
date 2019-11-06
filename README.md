# Humio-Operator

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
  version: "1.6.5"
  singleUserPassword: "develop3r"
  targetReplicationFactor: 2
  nodePools:
  - name: all
    firstNodeID: 1
    types: ["ingest","digest","storage"]
    nodeCount: 1
    nodeDiskSizeGB: 10
  - name: ingest
    firstNodeID: 101
    types: ["ingest"]
    nodeCount: 1
    nodeDiskSizeGB: 10
  - name: digest
    firstNodeID: 201
    types: ["digest"]
    nodeCount: 1
    nodeDiskSizeGB: 10
  - name: storage
    firstNodeID: 301
    types: ["storage"]
    nodeCount: 1
    nodeDiskSizeGB: 10
```

## Working on the operator

### Prerequisites

- [`docker`](https://docs.docker.com/docker-for-mac/install)
- [`kind`](https://github.com/kubernetes-sigs/kind#installation-and-usage)
- [`helm`](https://helm.sh/docs/using_helm/#installing-helm)
- [`kubectl`](https://kubernetes.io/docs/tasks/tools/install-kubectl/#install-kubectl-on-macos)
- [`kubebuilder`](https://book.kubebuilder.io/quick-start.html#installation)
- [`telepresence`](https://www.telepresence.io/reference/install)
- Clone the relevant repositories:

```bash
mkdir -p ~/git ~/go/src/github.com/humio
git clone https://github.com/humio/cp-helm-charts.git ~/git/humio-cp-helm-charts
git clone https://github.com/humio/humio-operator.git ~/go/src/github.com/humio/humio-operator
```

### Running the operator

We will use a few shell scripts stored in the `hack/` folder:

- `restart-k8s.sh`: This cleans up any existing running components (if any), and starts up a fresh `kind` cluster with Zookeeper & Kafka. It also installs the `CustomResourceDefinition` to the cluster.
- `run-operator.sh`: Applies a `HumioCluster` `CR`, then runs the operator locally using Telepresence.
  - When operator proxies traffic through Kubernetes API server, then we can remove the port-forward dependency.
- `stop.sh`: Essentially just stopping whatever is running, cleaning up any `PVC`'s, and finally destroying the `kind` cluster.
