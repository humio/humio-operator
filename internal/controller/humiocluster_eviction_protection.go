/*
Copyright 2020 Humio https://humio.com
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	evictionProtectionPDBSuffix       = "-eviction-protection"
	evictionProtectionCreatedAtAnnKey = "humio.com/eviction-protection-created-at"

	// evictionProtectionTTLFiredAtAnnKey marks a PDB whose TTL fired while the cluster
	// was still unstable. Presence blocks re-arm in ensureEvictionProtectionPDBExists;
	// cleared by full delete when the cluster recovers or the feature is disabled.
	evictionProtectionTTLFiredAtAnnKey = "humio.com/eviction-protection-ttl-fired-at"

	// evictionProtectionManagedByLabelKey / Value scope the orphan sweep to PDBs
	// created by this feature (see reconcileEvictionProtectionOrphanedPDBs).
	evictionProtectionManagedByLabelKey   = "humio.com/managed-by"
	evictionProtectionManagedByLabelValue = "eviction-protection"

	// Event reasons — external tooling (kube-eventer, alerting bridges) filters on these.
	evictionProtectionActivatedReason  = "EvictionProtectionActivated"
	evictionProtectionReleasedReason   = "EvictionProtectionReleased"
	evictionProtectionTTLExpiredReason = "EvictionProtectionTTLExpired"
)

// evictionProtectionPDBTTL bounds how long a restrictive PDB can persist while the
// cluster remains unstable; on expiry the PDB is neutered rather than left in place.
var evictionProtectionPDBTTL = 2 * time.Hour

// evictionProtectionNow is overridable in tests.
var evictionProtectionNow = time.Now

func evictionProtectionPDBName(hnp *HumioNodePool) string {
	return fmt.Sprintf("%s%s", hnp.GetNodePoolName(), evictionProtectionPDBSuffix)
}

// isClusterStable reports whether the cluster and every non-empty node pool are Running
// and no pods are currently terminating. The terminating-pods check ensures the
// eviction-protection PDB is created during expireAfter cycling even though the cluster
// state stays Running throughout the cycle.
func (r *HumioClusterReconciler) isClusterStable(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) bool {
	if hc.Status.State != humiov1alpha1.HumioClusterStateRunning {
		return false
	}

	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if pool.GetState() != humiov1alpha1.HumioClusterStateRunning {
			return false
		}
		// Unstable if any pod is terminating; err on the side of caution on list failure.
		pods, err := kubernetes.ListPods(ctx, r, pool.GetNamespace(), pool.GetNodePoolLabels())
		if err != nil {
			r.Log.Error(err, "failed to list pods for stability check; treating as unstable", "nodePool", pool.GetNodePoolName())
			return false
		}
		for _, pod := range pods {
			if pod.DeletionTimestamp != nil {
				return false
			}
		}
	}

	return true
}

// evictionProtectionPDBExpired reports whether pdb has outlived ttl. Missing or malformed
// created-at annotations are treated as expired so stale PDBs get cleaned up.
func evictionProtectionPDBExpired(pdb *policyv1.PodDisruptionBudget, ttl time.Duration, now time.Time) bool {
	if pdb == nil {
		return false
	}
	raw, ok := pdb.Annotations[evictionProtectionCreatedAtAnnKey]
	if !ok || raw == "" {
		return true
	}
	createdAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return now.Sub(createdAt) >= ttl
}

// ensureEvictionProtectionPDBExists creates a maxUnavailable=0 PDB for the given node
// pool, stamping evictionProtectionCreatedAtAnnKey on first create and preserving it
// across reconciles so the TTL clock is anchored to original creation.
// Returns early when:
//   - no pods currently match the pool selector (empty PDBs are noise)
//   - an existing PDB carries the TTL-fired tombstone (re-arming would defeat the safety valve)
func (r *HumioClusterReconciler) ensureEvictionProtectionPDBExists(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	pdbName := evictionProtectionPDBName(hnp)

	pods, err := kubernetes.ListPods(ctx, r, hc.Namespace,
		kubernetes.MatchingLabelsForHumioNodePool(hc.Name, hnp.GetNodePoolName()))
	if err != nil {
		return fmt.Errorf("failed to list pods for eviction-protection PDB %s/%s: %w", hc.Namespace, pdbName, err)
	}
	if len(pods) == 0 {
		r.Log.Info("no pods found for node pool, skipping eviction-protection PDB creation",
			"nodePool", hnp.GetNodePoolName(),
			"pdb", pdbName)
		return nil
	}

	var existing policyv1.PodDisruptionBudget
	if err := r.Get(ctx, client.ObjectKey{Name: pdbName, Namespace: hc.Namespace}, &existing); err == nil {
		if _, tombstoned := existing.Annotations[evictionProtectionTTLFiredAtAnnKey]; tombstoned {
			r.Log.Info("skipping eviction-protection PDB re-arm; TTL safety valve previously fired, waiting for cluster to return to Running",
				"pdb", pdbName,
				"ttlFiredAt", existing.Annotations[evictionProtectionTTLFiredAtAnnKey])
			return nil
		}
	} else if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to peek at eviction-protection PDB %s/%s: %w", hc.Namespace, pdbName, err)
	}

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pdbName,
			Namespace: hc.Namespace,
		},
	}

	desiredMaxUnavailable := intstr.FromInt32(0)
	desiredSelector := &metav1.LabelSelector{
		MatchLabels: kubernetes.MatchingLabelsForHumioNodePool(hc.Name, hnp.GetNodePoolName()),
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, pdb, func() error {
		if err := controllerutil.SetControllerReference(hc, pdb, r.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference on eviction-protection PDB %s: %w", pdbName, err)
		}
		pdb.Labels = hnp.GetNodePoolLabels()
		if pdb.Labels == nil {
			pdb.Labels = map[string]string{}
		}
		pdb.Labels[evictionProtectionManagedByLabelKey] = evictionProtectionManagedByLabelValue

		if pdb.Annotations == nil {
			pdb.Annotations = map[string]string{}
		}
		// Stamp created-at only on first create; preserving anchors the TTL to original creation.
		if _, ok := pdb.Annotations[evictionProtectionCreatedAtAnnKey]; !ok {
			pdb.Annotations[evictionProtectionCreatedAtAnnKey] = evictionProtectionNow().UTC().Format(time.RFC3339)
		}

		pdb.Spec.MaxUnavailable = &desiredMaxUnavailable
		pdb.Spec.MinAvailable = nil
		pdb.Spec.Selector = desiredSelector
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update eviction-protection PDB %s/%s: %w", hc.Namespace, pdbName, err)
	}

	switch op {
	case controllerutil.OperationResultCreated:
		r.Log.Info("created eviction-protection PDB",
			"pdb", pdbName,
			"createdAt", pdb.Annotations[evictionProtectionCreatedAtAnnKey],
			"ttl", evictionProtectionPDBTTL.String())
		r.Recorder.Eventf(hc, corev1.EventTypeNormal, evictionProtectionActivatedReason,
			"Created PodDisruptionBudget %q (maxUnavailable=0) for node pool %q; TTL=%s",
			pdbName, hnp.GetNodePoolName(), evictionProtectionPDBTTL.String())
	case controllerutil.OperationResultUpdated:
		r.Log.Info("updated eviction-protection PDB",
			"pdb", pdbName,
			"createdAt", pdb.Annotations[evictionProtectionCreatedAtAnnKey])
	}
	return nil
}

// reconcileEvictionProtectionPDBsAddOnly arms one PDB per node pool when the feature is
// enabled and the cluster is unstable. No-op otherwise. Never deletes.
func (r *HumioClusterReconciler) reconcileEvictionProtectionPDBsAddOnly(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) error {
	if !hc.Spec.OperatorFeatureFlags.EnableEvictionProtectionDuringMaintenance {
		return nil
	}

	if r.isClusterStable(ctx, hc, humioNodePools) {
		return nil
	}

	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if err := r.ensureEvictionProtectionPDBExists(ctx, hc, pool); err != nil {
			return err
		}
	}

	return nil
}

// reconcileEvictionProtectionPDBsRemove tears down eviction-protection PDBs. Behavior:
//   - feature disabled or cluster stable → full delete
//   - TTL expired while cluster unstable → neuter in place (maxUnavailable=100%) and
//     stamp the TTL-fired tombstone; deleting here would let the add-only path immediately
//     re-arm the maxUnavailable=0 lock and defeat the safety valve
//   - all other cases → keep the PDB
//
// Also sweeps orphaned PDBs whose node pool was removed from spec. Emits Kubernetes
// Events on the HumioCluster for every transition (Normal for routine removal;
// Warning for TTL expiry).
func (r *HumioClusterReconciler) reconcileEvictionProtectionPDBsRemove(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) error {
	featureEnabled := hc.Spec.OperatorFeatureFlags.EnableEvictionProtectionDuringMaintenance
	clusterStable := r.isClusterStable(ctx, hc, humioNodePools)
	onlyExpired := featureEnabled && !clusterStable

	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		pdbName := evictionProtectionPDBName(pool)

		existingPDB := &policyv1.PodDisruptionBudget{}
		err := r.Get(ctx, client.ObjectKey{Name: pdbName, Namespace: hc.Namespace}, existingPDB)
		if k8serrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to get eviction-protection PDB %s/%s: %w", hc.Namespace, pdbName, err)
		}

		_, tombstoned := existingPDB.Annotations[evictionProtectionTTLFiredAtAnnKey]
		expired := evictionProtectionPDBExpired(existingPDB, evictionProtectionPDBTTL, evictionProtectionNow())
		if onlyExpired && (!expired || tombstoned) {
			continue
		}

		// TTL expired while cluster unstable: neuter in place, do not delete.
		if expired && !clusterStable {
			neutered := intstr.FromString("100%")
			existingPDB.Spec.MaxUnavailable = &neutered
			existingPDB.Spec.MinAvailable = nil
			if existingPDB.Annotations == nil {
				existingPDB.Annotations = map[string]string{}
			}
			existingPDB.Annotations[evictionProtectionTTLFiredAtAnnKey] = evictionProtectionNow().UTC().Format(time.RFC3339)
			if err := r.Update(ctx, existingPDB); err != nil {
				return fmt.Errorf("failed to neuter eviction-protection PDB %s/%s: %w", hc.Namespace, pdbName, err)
			}
			r.Log.Info("eviction-protection PDB neutered by TTL safety valve; cluster still not stable, voluntary evictions delegated to any user PDB",
				"pdb", pdbName,
				"ttl", evictionProtectionPDBTTL.String(),
				"createdAt", existingPDB.Annotations[evictionProtectionCreatedAtAnnKey],
				"clusterState", hc.Status.State)
			r.Recorder.Eventf(hc, corev1.EventTypeWarning, evictionProtectionTTLExpiredReason,
				"PodDisruptionBudget %q neutered (maxUnavailable=100%%) after TTL %s expired; cluster state=%s; voluntary evictions re-enabled — investigate cluster health",
				pdbName, evictionProtectionPDBTTL.String(), hc.Status.State)
			EvictionProtectionPDBTTLExpiredTotal.WithLabelValues(pool.GetNamespace(), pool.GetNodePoolName()).Inc()
			continue
		}

		// Cluster stable or feature disabled: full delete (also clears any tombstone).
		if err := r.Delete(ctx, existingPDB); err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to delete eviction-protection PDB %s/%s: %w", hc.Namespace, pdbName, err)
		}

		switch {
		case !featureEnabled:
			r.Log.Info("deleted eviction-protection PDB because feature is disabled", "pdb", pdbName)
			r.Recorder.Eventf(hc, corev1.EventTypeNormal, evictionProtectionReleasedReason,
				"Removed PodDisruptionBudget %q; feature disabled", pdbName)
		default:
			r.Log.Info("deleted eviction-protection PDB, cluster is stable", "pdb", pdbName)
			r.Recorder.Eventf(hc, corev1.EventTypeNormal, evictionProtectionReleasedReason,
				"Removed PodDisruptionBudget %q; cluster reached stable state", pdbName)
		}
	}

	if err := r.reconcileEvictionProtectionOrphanedPDBs(ctx, hc, humioNodePools); err != nil {
		return err
	}

	return nil
}

// reconcileEvictionProtectionOrphanedPDBs removes eviction-protection PDBs whose node
// pool is no longer in the HumioCluster spec — owner-ref GC only fires on HumioCluster
// deletion, not on pool removal. Candidates are found by managed-by label and gated on
// metav1.IsControlledBy to keep multi-HumioCluster namespaces isolated.
func (r *HumioClusterReconciler) reconcileEvictionProtectionOrphanedPDBs(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) error {
	var pdbList policyv1.PodDisruptionBudgetList
	if err := r.List(ctx, &pdbList,
		client.InNamespace(hc.Namespace),
		client.MatchingLabels{evictionProtectionManagedByLabelKey: evictionProtectionManagedByLabelValue}); err != nil {
		return fmt.Errorf("failed to list eviction-protection PDBs in %s: %w", hc.Namespace, err)
	}

	expected := make(map[string]struct{}, len(humioNodePools.Items))
	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		expected[evictionProtectionPDBName(pool)] = struct{}{}
	}

	for i := range pdbList.Items {
		pdb := &pdbList.Items[i]

		if _, current := expected[pdb.Name]; current {
			continue
		}
		if !metav1.IsControlledBy(pdb, hc) {
			continue
		}

		if err := r.Delete(ctx, pdb); err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("failed to delete orphaned eviction-protection PDB %s/%s: %w", hc.Namespace, pdb.Name, err)
		}

		r.Log.Info("deleted orphaned eviction-protection PDB, node pool no longer in HumioCluster spec",
			"pdb", pdb.Name)
		r.Recorder.Eventf(hc, corev1.EventTypeNormal, evictionProtectionReleasedReason,
			"Removed orphaned PodDisruptionBudget %q; node pool no longer present in HumioCluster spec", pdb.Name)
	}

	return nil
}
