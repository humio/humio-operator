# Autoscaling Local Development

Local kind cluster environment for developing and testing HPA autoscaling with shadow node pools.

## Prerequisites

- `kind` installed
- `helm` installed
- `kubectl` installed
- A valid LogScale license key

## Quick Start

```bash
# Set your license (required for LogScale to function)
export HUMIO_LICENSE="<your-logscale-license-key>"

# Create the cluster, deploy operator, apply sample CR, install metrics-server + HPA
make local-kind-hpa
```

This runs two steps:
1. `hack/run-local-kind.sh` — creates kind cluster, deploys operator via helm, creates license secret, applies sample CR
2. `hack/setup-hpa.sh` — installs metrics-server and creates the HPA resource

## How It Works

The autoscaling feature uses **shadow HumioNodePool CRDs** that expose a `/scale` subresource. This allows a standard Kubernetes HPA to target them. The operator reconciles the shadow pool's `spec.replicas` back into the HumioCluster's node pool configuration.

Key pieces:
- `HumioNodePoolSpec.Autoscaling` — enables autoscaling on a node pool (sets `EnableIndependentHumioNodePools` feature flag)
- Shadow `HumioNodePool` CRD — created automatically when autoscaling is enabled; exposes the scale subresource
- `HumioNodeSpec.NodeCount` (`*int32`) — when `nil`, the pool is HPA-managed; when set explicitly, it's a user override
- `resolveEffectiveNodeCount` — returns the effective replica count by checking spec override → status desired → autoscaling min → default (2)

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HUMIO_LICENSE` | (none) | LogScale license key. Creates `example-humiocluster-license` secret before CR apply. |
| `SAMPLE` | `core_v1alpha1_humiocluster-kind-local.yaml` | Sample CR filename from `config/samples/`. |
| `DUMMY_LOGSCALE_IMAGE` | `false` | Set `true` to build/load a dummy logscale image instead of pulling from registry. |
| `LOCAL_HELPER_BUILD` | `true` | Build the helper image locally. |
| `USE_CERTMANAGER` | `true` | Install cert-manager. |

## Sample CRs

| File | Description |
|------|-------------|
| `config/samples/core_v1alpha1_humiocluster-kind-local.yaml` | Basic single-node cluster (no autoscaling) |
| `config/samples/core_v1alpha1_humiocluster-hpa-kind-local.yaml` | Cluster with ingest-only node pool configured for autoscaling |
| `config/samples/hpa_ingest-only.yaml` | HPA resource targeting the ingest-only shadow HumioNodePool |

## HPA Testing Workflow

After `make local-kind-hpa` completes:

```bash
# Watch HPA, pods, and node pools
watch kubectl get hpa,pods,humionodepools

# The HPA targets 50% CPU utilization on the ingest-only pool.
# With idle pods, it will scale down to minReplicas (1) after the
# stabilization window (~5 minutes).

# To trigger scale-up, generate CPU load on an ingest-only pod:
kubectl exec -it <ingest-only-pod> -- sh -c 'dd if=/dev/urandom | bzip2 > /dev/null &'
```

## Rebuild & Redeploy (without recreating cluster)

```bash
make local-kind-redeploy
# or directly:
hack/reload-kind.sh
```

This rebuilds the operator image, loads it into kind, and restarts the deployment.

## Tear Down

```bash
kind delete cluster --name kind
```

## Troubleshooting

**Pods in CrashLoopBackOff after changing node roles:**
LogScale tracks node roles in Kafka. If you change `NODE_ROLES` on an existing pool,
you must unregister the old nodes via GraphQL before they can restart:

```bash
ADMIN_TOKEN=$(kubectl get secret example-humiocluster-admin-token -o jsonpath='{.data.token}' | base64 -d)
# List members to find the stale vhost ID
kubectl exec <running-main-pod> -- wget -qO- --header="Authorization: Bearer $ADMIN_TOKEN" http://localhost:8080/api/v1/clusterconfig/members
# Unregister the stale node (replace NODE_ID with the vhost number)
kubectl exec <running-main-pod> -- wget -qO- --header="Authorization: Bearer $ADMIN_TOKEN" --header="Content-Type: application/json" --post-data='{"query":"mutation { clusterUnregisterNode(nodeID: NODE_ID, force: true) { __typename } }"}' http://localhost:8080/graphql
```

Then delete the affected pods and PVCs so they recreate fresh.
