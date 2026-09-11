# Pod Lifecycle Management with expireAfter

## Overview

The `expireAfter` field on `HumioNodeSpec` enables time-based pod rotation. When set, the operator automatically replaces pods that exceed the configured maximum lifetime, cycling them one at a time using the same safety gates as a rolling update.

Common use cases:

- **Security patching** — ensure nodes periodically pull fresh base images without a full cluster upgrade
- **Memory leak mitigation** — reclaim memory from long-running JVM processes on a schedule
- **Operational hygiene** — enforce a maximum pod age policy across all node pools

## Configuration

Set `expireAfter` on the cluster or on individual node pools. The value uses Go duration format.

### Cluster-wide (applies to all node pools)

```yaml
apiVersion: core.humio.com/v1alpha1
kind: HumioCluster
metadata:
  name: my-cluster
spec:
  expireAfter: "168h"   # 7 days
```

### Per-pool (different durations per pool)

```yaml
spec:
  nodePools:
    - name: ingest
      expireAfter: "24h"    # cycle ingest nodes daily
    - name: query
      expireAfter: "168h"   # cycle query nodes weekly
```

## Behavior

When `expireAfter` is set, the operator checks pod ages on every reconcile and deletes pods that exceed the configured duration. Deletions respect all existing safety gates:

- **MaxUnavailable** — at most N pods are unavailable at any time
- **MinReadySeconds** — a replacement must be ready for this duration before the next deletion proceeds
- **Zone awareness** — when `updateStrategy.enableZoneAwareness: true`, the operator applies zone-scoped batching described below

The operator requeues itself at the nearest upcoming expiry so it acts promptly without busy-polling.

### Deletion algorithm

On each reconcile where `expireAfter` is set and the cluster is in `Running` state with all pods ready:

1. All pods with `age > expireAfter` are collected and sorted oldest-first by `pod.metadata.creationTimestamp`.
2. The **zone** of the oldest expired pod determines the target zone for this reconcile. Only pods on nodes in that zone are eligible for deletion in this round.
3. The **deletion budget** is `floor(nodeCount × maxUnavailable) − currentlyNotReadyPods`. At most this many pods are evicted per cycle.
4. The operator issues a direct `DELETE` on the pod (not the Kubernetes Eviction subresource). This is intentional: it bypasses PDB admission so the operator's own eviction-protection PDB (see [Karpenter interaction](#interaction-with-karpenter-consolidation-enableevictionprotectionduringmaintenance-pr-1052)) does not block the very cycling that creates it. **A consequence:** user-defined PDBs on the pool selector do NOT gate `expireAfter` cycling. If you need to freeze cycling during a maintenance window, remove `expireAfter` from the CR — do not rely on a user PDB.
5. After each deletion, the operator stores the deletion timestamp in an in-memory map keyed by `namespace/nodePoolName`. Subsequent reconciles triggered before `minReadySeconds` has elapsed since the last deletion are skipped, preventing cache-race double-deletes.
6. The operator requeues at `minReadySeconds` after the last deletion, or at the duration until the next pod expires (whichever is sooner).

## Operational notes

### Thundering herd on initial deployment

When `expireAfter` is first applied to a cluster where all pods were created at the same time (e.g. initial install), all pods expire simultaneously. MaxUnavailable prevents a full outage but the rolling restart will run continuously until all pods have been cycled. This is expected. If you want to stagger the effect, you can set a longer initial `expireAfter` and shorten it later.

### Resizing the cluster while expireAfter is active

Changing `nodeCount` while `expireAfter` is active causes both the scale-down reconcile path and the expiry-based cycling path to run in the same window. The `waitingOnPods` gate prevents availability from dropping below the MinAvailable floor, but you may temporarily hit that floor rather than staying comfortably above it. To avoid this:

1. Remove `expireAfter` before changing `nodeCount`
2. Wait for the resize to complete and all pods to be ready
3. Re-apply `expireAfter`

### Minimum duration

The API enforces a minimum of `1h`. Values closer to `1h` on clusters where pod startup + minReadySeconds approaches the expiry window may cause continuous rolling restarts. For most production use cases `24h` or longer is appropriate.

### Interaction with OnDelete update strategy

`expireAfter` respects `updateStrategy.type: OnDelete`. If OnDelete is set, the operator will not delete expired pods automatically — deletions must be performed manually. This is consistent with how spec-drift deletions behave under OnDelete.

## Metrics

The following Prometheus metrics are exported by the operator when `expireAfter` is configured:

| Metric | Type | Labels | Description |
|---|---|---|---|
| `humio_operator_expire_after_deletions_total` | Counter | `namespace`, `pool` | Total pods deleted by the expireAfter cycling mechanism (direct DELETE; not the Eviction subresource) |
| `humio_operator_next_pod_expiry_seconds` | Gauge | `namespace`, `pool` | Unix timestamp of the next pod expiry in a pool (0 if expireAfter not set or all pods currently expired) |
| `humio_operator_expire_after_skips_total` | Counter | `namespace`, `pool`, `reason` | Reconciles where cycling was skipped. `reason` ∈ {`on_delete_strategy`, `cluster_not_running`, `min_ready_throttle`, `waiting_on_pods`}. Primary diagnostic for stalled cycling. |
| `humio_operator_eviction_protection_pdb_ttl_expired_total` | Counter | `namespace`, `pool` | Safety-valve fired: the operator's eviction-protection PDB was removed after TTL because the cluster remained unstable. A non-zero rate here means a cluster is wedged. |

All metrics carry both `namespace` and `pool` labels; two HumioClusters with the same name in different namespaces produce distinct time series.

### Example queries

```promql
# Rate of pod cycling per pool
rate(humio_operator_expire_after_deletions_total[1h])

# Time until next pod expires (human-readable minutes)
(humio_operator_next_pod_expiry_seconds - time()) / 60

# Stalled cycling — surface reasons and pools
sum by (namespace, pool, reason) (rate(humio_operator_expire_after_skips_total[15m]))

# Wedged clusters (safety-valve fired within the last day)
increase(humio_operator_eviction_protection_pdb_ttl_expired_total[1d]) > 0
```

These metrics are scraped from the operator's metrics endpoint (`:8080/metrics` by default) and can be forwarded to LogScale using the existing telemetry pipeline.

### Interaction with Karpenter consolidation (`enableEvictionProtectionDuringMaintenance`, PR #1052)

When `featureFlags.enableEvictionProtectionDuringMaintenance: true` is set alongside `expireAfter`, the two features interact as follows:

**Configuration:**
```yaml
spec:
  expireAfter: "168h"
  featureFlags:
    enableEvictionProtectionDuringMaintenance: true
```

**Lifecycle during an expireAfter cycle:**

1. **Pre-cycle (cluster `Running`)**: No eviction-protection PDB exists. Karpenter may consolidate underutilized nodes freely.
2. **Deletion fired**: `expireAfter` deletes the oldest expired pod via a direct DELETE (bypasses PDB admission). The cluster transitions to `Restarting` once the deletion is observed.
3. **Maintenance window**: The operator creates a temporary PDB (`<pool>-eviction-protection`, `maxUnavailable: 0`) per node pool. Karpenter uses the Eviction subresource for consolidation, so it receives HTTP 429 and cannot drain any node covered by this PDB.
4. **Replacement ready**: Once the replacement pod is healthy and past `minReadySeconds`, the cluster returns to `Running` and the operator removes the eviction-protection PDB.
5. **Post-cycle**: Karpenter may consolidate again.

**Key properties:**
- The eviction-protection PDB has a TTL (`evictionProtectionPDBTTL`, default 2h) so stale PDBs from interrupted reconciles cannot block indefinitely. Removal after TTL increments `humio_operator_eviction_protection_pdb_ttl_expired_total` and is logged at Warn.
- `expireAfter` uses a direct DELETE, not the Eviction subresource. That is deliberate: the operator's own eviction-protection PDB would otherwise block the very cycling that creates it. As a side effect, **user-defined PDBs on the pool selector do not gate `expireAfter`** — the eviction-protection PDB only affects third-party evicters like Karpenter.
- The eviction-protection PDB is created/removed by the operator — it is not user-managed and should not be manually deleted.

**Testing:** Verified on GKE cluster with Karpenter v0.6.0 (`cloudpilot-ai/karpenter-provider-gcp`). The eviction-protection PDB correctly blocks Karpenter consolidation attempts with HTTP 429 during the cycling window.
