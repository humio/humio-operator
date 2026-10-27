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
	"sort"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/humio/humio-operator/internal/kubernetes"
)

// ensureEmptyNodePoolHasNoPods removes the pods of a node pool explicitly configured with zero nodes.
//
// This is the fallback for scale-to-zero when the downscaling feature is disabled. When
// downscaling IS enabled, processDownscaling handles eviction with proper data safety — this
// function must not compete with it.
//
// Only acts on explicit nodeCount=0 intent (spec field set to 0). Autoscaling pools that
// transiently resolve to zero (e.g. during HPA startup or status loss) are never touched.
//
// WARNING: takes no *HumioCluster — compiler-enforced guarantee it writes no cluster status.
func (r *HumioClusterReconciler) ensureEmptyNodePoolHasNoPods(ctx context.Context, hnp *HumioNodePool) (reconcile.Result, error) {
	// Only act on explicit nodeCount=0 in the spec. We must NOT use GetNodeCount() here because
	// it resolves through a precedence chain (spec → status → autoscaling.minReplicas → default)
	// and an autoscaling pool can transiently resolve to 0 during HPA startup or status loss.
	if hnp.humioNodeSpec.NodeCount == nil || *hnp.humioNodeSpec.NodeCount != 0 {
		return reconcile.Result{}, nil
	}
	// When the downscaling feature is enabled, processDownscaling handles scale-to-zero with
	// proper data eviction via the LogScale API. This fallback must not compete — it deletes
	// pods without eviction, so running both would remove 2 pods per reconcile instead of 1.
	if hnp.IsDownscalingFeatureEnabled() {
		return reconcile.Result{}, nil
	}

	pods, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to list pods")
	}

	deletable := make([]corev1.Pod, 0, len(pods))
	for idx := range pods {
		if pods[idx].DeletionTimestamp != nil {
			continue
		}
		deletable = append(deletable, pods[idx])
	}

	if len(deletable) == 0 {
		return reconcile.Result{}, nil
	}

	// newest first: least likely to hold data segments, minimizes re-replication cost
	sort.Slice(deletable, func(i, j int) bool {
		return deletable[i].CreationTimestamp.After(deletable[j].CreationTimestamp.Time)
	})

	target := deletable[0]
	r.Log.Info(fmt.Sprintf("node pool %s is configured with zero nodes; deleting pod %s",
		hnp.GetNodePoolName(), target.Name), "remainingPods", len(deletable))

	if err := r.Delete(ctx, &target); err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: Requeue is deprecated; migration to RequeueAfter tracked as follow-up
		}
		return reconcile.Result{}, r.logErrorAndReturn(err,
			fmt.Sprintf("failed to delete pod %s from empty node pool", target.Name))
	}
	humioClusterPrometheusMetrics.Counters.PodsDeleted.Inc()

	return reconcile.Result{Requeue: true}, nil //nolint:staticcheck // SA1019: Requeue is deprecated; migration to RequeueAfter tracked as follow-up
}
