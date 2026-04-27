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

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	policyv1 "k8s.io/api/policy/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	karpenterPDBSuffix = "-karpenter-pdb"
)

func karpenterPDBName(hnp *HumioNodePool) string {
	return fmt.Sprintf("%s%s", hnp.GetNodePoolName(), karpenterPDBSuffix)
}

// isClusterStable returns true when the cluster is fully stable: the cluster state is Running
// and all node pools with nodes are Running.
func (r *HumioClusterReconciler) isClusterStable(hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) bool {
	if hc.Status.State != humiov1alpha1.HumioClusterStateRunning {
		return false
	}

	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if pool.GetState() != humiov1alpha1.HumioClusterStateRunning {
			return false
		}
	}

	return true
}

// ensureKarpenterPDBExists creates a restrictive PDB (maxUnavailable: 0) for the given node pool
// if it does not already exist. This prevents Karpenter from evicting any pod in the pool.
func (r *HumioClusterReconciler) ensureKarpenterPDBExists(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	pdbName := karpenterPDBName(hnp)

	existingPDB := &policyv1.PodDisruptionBudget{}
	err := r.Get(ctx, client.ObjectKey{Name: pdbName, Namespace: hc.Namespace}, existingPDB)
	// if the PDB exists, do nothing
	if err == nil {
		return nil
	}
	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to get karpenter PDB %s/%s: %w", hc.Namespace, pdbName, err)
	}

	maxUnavailable := intstr.FromInt32(0)
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pdbName,
			Namespace: hc.Namespace,
			Labels:    hnp.GetNodePoolLabels(),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: kubernetes.MatchingLabelsForHumioNodePool(hc.Name, hnp.GetNodePoolName()),
			},
		},
	}

	if err := controllerutil.SetControllerReference(hc, pdb, r.Scheme()); err != nil {
		return fmt.Errorf("failed to set controller reference on karpenter PDB %s: %w", pdbName, err)
	}

	if err := r.Create(ctx, pdb); err != nil {
		return fmt.Errorf("failed to create karpenter PDB %s/%s: %w", hc.Namespace, pdbName, err)
	}

	r.Log.Info(fmt.Sprintf("created karpenter PDB %s to prevent disruptions", pdbName))
	return nil
}

// deleteKarpenterPDB deletes the restrictive Karpenter PDB for the given node pool if it exists.
func (r *HumioClusterReconciler) deleteKarpenterPDB(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	pdbName := karpenterPDBName(hnp)

	existingPDB := &policyv1.PodDisruptionBudget{}
	err := r.Get(ctx, client.ObjectKey{Name: pdbName, Namespace: hc.Namespace}, existingPDB)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get karpenter PDB %s/%s: %w", hc.Namespace, pdbName, err)
	}

	if err := r.Delete(ctx, existingPDB); err != nil {
		return fmt.Errorf("failed to delete karpenter PDB %s/%s: %w", hc.Namespace, pdbName, err)
	}

	r.Log.Info(fmt.Sprintf("deleted karpenter PDB %s, allowing Karpenter consolidation", pdbName))
	return nil
}

// reconcileKarpenterPDBsAddOnly is Phase 1: creates restrictive PDBs for all node pools
// when the cluster is not stable. Never deletes PDBs.
// If the feature is disabled, this is a no-op.
func (r *HumioClusterReconciler) reconcileKarpenterPDBsAddOnly(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) error {
	if !hc.Spec.OperatorFeatureFlags.EnableKarpenterIntegration {
		return nil
	}

	if r.isClusterStable(hc, humioNodePools) {
		return nil
	}

	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if err := r.ensureKarpenterPDBExists(ctx, hc, pool); err != nil {
			return err
		}
	}

	return nil
}

// reconcileKarpenterPDBsRemoveIfStable is Phase 2: deletes restrictive PDBs for all node pools
// when the cluster is stable. Also handles cleanup when the feature flag is disabled.
func (r *HumioClusterReconciler) reconcileKarpenterPDBsRemoveIfStable(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) error {
	featureEnabled := hc.Spec.OperatorFeatureFlags.EnableKarpenterIntegration

	if featureEnabled && !r.isClusterStable(hc, humioNodePools) {
		return nil
	}

	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if err := r.deleteKarpenterPDB(ctx, hc, pool); err != nil {
			return err
		}
	}

	return nil
}
