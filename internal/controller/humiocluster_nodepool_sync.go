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
	"reflect"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
)

const (
	shadowNodePoolManagedBy = "humiocluster-shadow"
	mainNodePoolName        = "main"

	annotationManagedBy = "humio.com/managed-by"
	annotationCluster   = "humio.com/cluster"
)

func nodeCountOrDefault(nc *int32, autoscaling *humiov1alpha1.AutoscalingSpec) int32 {
	if nc == nil {
		return effectiveMinReplicas(autoscaling)
	}
	return *nc
}

func (r *HumioClusterReconciler) ensureNodePoolResources(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if hc.DeletionTimestamp != nil {
		return nil
	}

	if !hc.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools {
		return r.cleanupAllShadowNodePools(ctx, hc)
	}

	if (hc.Spec.NodeCount != nil && *hc.Spec.NodeCount > 0) || (hc.Spec.NodeCount == nil && hc.Spec.Autoscaling != nil) {
		if err := r.ensureShadowNodePool(ctx, hc, mainNodePoolName, hc.Spec.HumioNodeSpec); err != nil {
			return fmt.Errorf("failed to ensure main node pool shadow resource: %w", err)
		}
		// Forward-sync main pool
		shadow := &humiov1alpha1.HumioNodePool{}
		shadowKey := types.NamespacedName{Name: shadowNodePoolResourceName(hc.Name, mainNodePoolName), Namespace: hc.Namespace}
		if err := r.Get(ctx, shadowKey, shadow); err != nil {
			return err
		}
		if err := forwardSyncNodeCount(ctx, r.Client, shadow, hc.Spec.NodeCount); err != nil {
			return err
		}
		if err := forwardSyncAutoscaling(ctx, r.Client, shadow, hc.Spec.Autoscaling); err != nil {
			return err
		}

		// Reverse-sync main pool
		if err := reverseSyncWithStalenessTracking(ctx, r.Client, hc, mainNodePoolName, r.stalenessCounters, r.Recorder); err != nil {
			r.Log.Error(err, "reverse-sync failed for main pool, continuing reconcile", "pool", mainNodePoolName)
		}
	}

	for _, nodePoolSpec := range hc.Spec.NodePools {
		if err := r.ensureShadowNodePool(ctx, hc, nodePoolSpec.Name, nodePoolSpec.HumioNodeSpec); err != nil {
			return fmt.Errorf("failed to ensure shadow resource for node pool %s: %w", nodePoolSpec.Name, err)
		}
		// Forward-sync node pool
		shadow := &humiov1alpha1.HumioNodePool{}
		shadowKey := types.NamespacedName{Name: shadowNodePoolResourceName(hc.Name, nodePoolSpec.Name), Namespace: hc.Namespace}
		if err := r.Get(ctx, shadowKey, shadow); err != nil {
			return err
		}
		if err := forwardSyncNodeCount(ctx, r.Client, shadow, nodePoolSpec.HumioNodeSpec.NodeCount); err != nil {
			return err
		}
		if err := forwardSyncAutoscaling(ctx, r.Client, shadow, nodePoolSpec.Autoscaling); err != nil {
			return err
		}

		// Reverse-sync node pool
		if err := reverseSyncWithStalenessTracking(ctx, r.Client, hc, nodePoolSpec.Name, r.stalenessCounters, r.Recorder); err != nil {
			r.Log.Error(err, "reverse-sync failed, continuing reconcile", "pool", nodePoolSpec.Name)
		}
	}

	return r.cleanupOrphanedShadowNodePools(ctx, hc, r.stalenessCounters)
}

func (r *HumioClusterReconciler) ensureShadowNodePool(ctx context.Context, hc *humiov1alpha1.HumioCluster, poolName string, nodeSpec humiov1alpha1.HumioNodeSpec) error {
	resourceName := shadowNodePoolResourceName(hc.Name, poolName)
	nodePoolLabels := kubernetes.MatchingLabelsForHumioNodePool(hc.Name, resourceName)

	selector := &metav1.LabelSelector{
		MatchLabels: nodePoolLabels,
	}

	desired := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: hc.Namespace,
			Labels:    kubernetes.LabelsForHumio(hc.Name),
			Annotations: map[string]string{
				annotationManagedBy: shadowNodePoolManagedBy,
				annotationCluster:   hc.Name,
			},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:          poolName,
			ClusterName:   hc.Name,
			Selector:      selector,
			NodeCount:     nodeCountOrDefault(nodeSpec.NodeCount, nodeSpec.Autoscaling),
			HumioNodeSpec: nodeSpec,
		},
	}

	if err := controllerutil.SetControllerReference(hc, desired, r.Scheme()); err != nil {
		return fmt.Errorf("failed to set owner reference on shadow HumioNodePool %s: %w", resourceName, err)
	}

	existing := &humiov1alpha1.HumioNodePool{}
	err := r.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: hc.Namespace}, existing)

	if k8serrors.IsNotFound(err) {
		r.Log.Info("creating shadow HumioNodePool resource", "name", resourceName, "clusterName", hc.Name, "component", "shadow-node-pool-sync")
		if err := r.Create(ctx, desired); err != nil {
			return err
		}
		return r.patchShadowNodePoolStatus(ctx, hc.Name, resourceName, hc.Namespace, selector)
	}
	if err != nil {
		return fmt.Errorf("failed to get shadow HumioNodePool %s: %w", resourceName, err)
	}

	// Check for foreign resource collision
	if !isOwnedBy(existing, hc) {
		r.Log.Info("WARN: foreign HumioNodePool resource exists with same name, skipping shadow creation",
			"name", resourceName, "component", "shadow-node-pool-sync")
		return nil
	}

	// Check for unexpected finalizers on shadow CR
	if len(existing.Finalizers) > 0 {
		r.Log.Info("WARN: shadow HumioNodePool has unexpected finalizers",
			"name", resourceName,
			"component", "shadow-node-pool-sync",
			"finalizers", existing.Finalizers)
	}

	// Preserve HPA-managed nodeCount when parent has nil
	if nodeSpec.NodeCount == nil {
		// HPA-managed mode: preserve existing shadow nodeCount
		desired.Spec.NodeCount = existing.Spec.NodeCount
	}

	if !reflect.DeepEqual(existing.Spec, desired.Spec) {
		r.Log.Info("updating shadow HumioNodePool resource", "name", resourceName, "clusterName", hc.Name, "component", "shadow-node-pool-sync")
		existing.Spec = desired.Spec
		existing.Labels = desired.Labels
		existing.Annotations = desired.Annotations
		if err := r.Update(ctx, existing); err != nil {
			return err
		}
	}

	return r.patchShadowNodePoolStatus(ctx, hc.Name, resourceName, hc.Namespace, selector)
}

func (r *HumioClusterReconciler) cleanupAllShadowNodePools(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	nodePoolList := &humiov1alpha1.HumioNodePoolList{}
	err := r.List(ctx, nodePoolList,
		client.InNamespace(hc.Namespace),
		client.MatchingLabels(kubernetes.LabelsForHumio(hc.Name)),
	)
	if err != nil {
		return fmt.Errorf("failed to list shadow HumioNodePool resources: %w", err)
	}

	for idx := range nodePoolList.Items {
		np := &nodePoolList.Items[idx]
		if np.Annotations[annotationManagedBy] == shadowNodePoolManagedBy {
			r.Log.Info("deleting shadow HumioNodePool resource (feature disabled)", "name", np.Name, "clusterName", hc.Name, "component", "shadow-node-pool-sync")
			if err := r.Delete(ctx, np); err != nil && !k8serrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete shadow HumioNodePool %s: %w", np.Name, err)
			}
		}
	}

	return nil
}

func (r *HumioClusterReconciler) cleanupOrphanedShadowNodePools(ctx context.Context, hc *humiov1alpha1.HumioCluster, counter *stalenessCounter) error {
	nodePoolList := &humiov1alpha1.HumioNodePoolList{}
	err := r.List(ctx, nodePoolList,
		client.InNamespace(hc.Namespace),
		client.MatchingLabels(kubernetes.LabelsForHumio(hc.Name)),
	)
	if err != nil {
		return fmt.Errorf("failed to list shadow HumioNodePool resources: %w", err)
	}

	expectedNames := make(map[string]bool)
	if (hc.Spec.NodeCount != nil && *hc.Spec.NodeCount > 0) || (hc.Spec.NodeCount == nil && hc.Spec.Autoscaling != nil) {
		expectedNames[shadowNodePoolResourceName(hc.Name, mainNodePoolName)] = true
	}
	for _, np := range hc.Spec.NodePools {
		expectedNames[shadowNodePoolResourceName(hc.Name, np.Name)] = true
	}

	for idx := range nodePoolList.Items {
		np := &nodePoolList.Items[idx]
		if np.Annotations[annotationManagedBy] != shadowNodePoolManagedBy {
			continue
		}
		if !expectedNames[np.Name] {
			r.Log.Info("deleting orphaned shadow HumioNodePool resource", "name", np.Name, "clusterName", hc.Name, "component", "shadow-node-pool-sync")
			if err := r.Delete(ctx, np); err != nil && !k8serrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete orphaned shadow HumioNodePool %s: %w", np.Name, err)
			}
			// Cleanup staleness counter and metric
			poolName := np.Spec.Name
			if counter != nil {
				counter.mu.Lock()
				delete(counter.counts, poolName)
				counter.mu.Unlock()
			}
			ShadowStaleness.DeleteLabelValues(poolName)
		}
	}

	return nil
}

func shadowNodePoolResourceName(clusterName, poolName string) string {
	if poolName == mainNodePoolName {
		return clusterName
	}
	return fmt.Sprintf("%s-%s", clusterName, poolName)
}

func isOwnedBy(resource *humiov1alpha1.HumioNodePool, owner *humiov1alpha1.HumioCluster) bool {
	for _, ownerRef := range resource.OwnerReferences {
		if ownerRef.UID == owner.UID {
			return true
		}
	}
	return false
}

func (r *HumioClusterReconciler) patchShadowNodePoolStatus(ctx context.Context, clusterName, resourceName, namespace string, selector *metav1.LabelSelector) error {
	// Retrieve the shadow node pool
	nodePool := &humiov1alpha1.HumioNodePool{}
	if err := r.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace}, nodePool); err != nil {
		return fmt.Errorf("failed to get shadow HumioNodePool %s: %w", resourceName, err)
	}

	// List pods matching the selector
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels(selector.MatchLabels)); err != nil {
		r.Log.Error(err, "failed to list pods for node pool", "nodePool", resourceName, "clusterName", clusterName, "component", "shadow-node-pool-sync")
		return nil
	}

	// Count ready pods
	readyCount := int32(0)
	for _, pod := range podList.Items {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				readyCount++
				break
			}
		}
	}

	// Convert selector to string format
	selectorString := labels.Set(selector.MatchLabels).String()

	// Look up state from parent cluster status
	hc := &humiov1alpha1.HumioCluster{}
	if err := r.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, hc); err == nil {
		for _, poolStatus := range hc.Status.NodePoolStatus {
			if poolStatus.Name == resourceName {
				nodePool.Status.State = poolStatus.State
				break
			}
		}
	}

	// Patch status
	nodePool.Status.Name = nodePool.Spec.Name
	nodePool.Status.CurrentReplicas = readyCount
	nodePool.Status.Selector = selectorString

	if err := r.Status().Update(ctx, nodePool); err != nil {
		return fmt.Errorf("failed to update status for shadow HumioNodePool %s: %w", resourceName, err)
	}

	return nil
}

// forwardSyncNodeCount conditionally overwrites shadow spec.nodeCount.
// Only updates when parent nodeCount is non-nil (explicit override).
// When parent nodeCount is nil, preserves the shadow's current value (HPA-managed).
func forwardSyncNodeCount(ctx context.Context, c client.Client, shadow *humiov1alpha1.HumioNodePool, parentNodeCount *int32) error {
	start := time.Now()
	defer func() {
		ReconcileDurationSeconds.WithLabelValues("HumioCluster", shadow.Name).Observe(time.Since(start).Seconds())
	}()

	if !isExplicitOverride(parentNodeCount) {
		return nil
	}
	shadow.Spec.NodeCount = *parentNodeCount
	if err := c.Update(ctx, shadow); err != nil {
		return fmt.Errorf("forward-sync nodeCount failed for pool %s: %w", shadow.Name, err)
	}
	return nil
}

// forwardSyncAutoscaling mirrors parent autoscaling spec to shadow.
// Clears stale autoscaling when parent has none.
func forwardSyncAutoscaling(ctx context.Context, c client.Client, shadow *humiov1alpha1.HumioNodePool, parentAutoscaling *humiov1alpha1.AutoscalingSpec) error {
	if reflect.DeepEqual(shadow.Spec.Autoscaling, parentAutoscaling) {
		return nil
	}
	shadow.Spec.Autoscaling = parentAutoscaling
	if err := c.Update(ctx, shadow); err != nil {
		return fmt.Errorf("forward-sync autoscaling failed for pool %s: %w", shadow.Name, err)
	}
	return nil
}

// reverseSyncNodeCount reads shadow nodeCount, clamps to bounds, updates cluster status.
// If clamping occurred, corrects the shadow spec.nodeCount.
func reverseSyncNodeCount(ctx context.Context, c client.Client, cluster *humiov1alpha1.HumioCluster, shadow *humiov1alpha1.HumioNodePool, poolName string) error {
	desired := shadow.Spec.NodeCount

	// Skip clamping entirely when autoscaling is not configured
	clamped := desired
	if shadow.Spec.Autoscaling != nil {
		min := effectiveMinReplicas(shadow.Spec.Autoscaling)
		max := effectiveMaxReplicas(shadow.Spec.Autoscaling, min)
		clamped = clampReplicas(desired, min, max)
	}

	clampedStr := strconv.FormatBool(clamped != desired)

	NodeCountUpdates.WithLabelValues(poolName, "hpa", clampedStr).Inc()

	statusUpdated := false
	for i := range cluster.Status.NodePoolStatus {
		if cluster.Status.NodePoolStatus[i].Name == shadow.Name {
			cluster.Status.NodePoolStatus[i].DesiredReplicas = clamped
			statusUpdated = true
			break
		}
	}

	if !statusUpdated {
		return fmt.Errorf("reverse-sync update cluster status failed: pool %s not found in status", shadow.Name)
	}

	if err := c.Status().Update(ctx, cluster); err != nil {
		return fmt.Errorf("reverse-sync update cluster status failed for pool %s: %w", poolName, err)
	}

	if clamped != desired {
		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			// Re-fetch shadow to get latest resourceVersion
			freshShadow := &humiov1alpha1.HumioNodePool{}
			shadowKey := types.NamespacedName{Name: shadow.Name, Namespace: shadow.Namespace}
			if err := c.Get(ctx, shadowKey, freshShadow); err != nil {
				return err
			}
			freshShadow.Spec.NodeCount = clamped
			return c.Update(ctx, freshShadow)
		})
		if err != nil {
			return fmt.Errorf("reverse-sync clamp-and-correct failed for pool %s: %w", poolName, err)
		}
	}

	return nil
}

type stalenessCounter struct {
	mu     sync.RWMutex
	counts map[string]int
}

const stalenessThreshold = 5

// reverseSyncWithStalenessTracking wraps reverse-sync with staleness tracking.
// On shadow read failure, increments counter. On success, resets counter and
// delegates to reverseSyncNodeCount.
func reverseSyncWithStalenessTracking(ctx context.Context, c client.Client, cluster *humiov1alpha1.HumioCluster, poolName string, counter *stalenessCounter, recorder record.EventRecorder) error {
	shadow := &humiov1alpha1.HumioNodePool{}
	shadowKey := types.NamespacedName{Name: shadowNodePoolResourceName(cluster.Name, poolName), Namespace: cluster.Namespace}

	if err := c.Get(ctx, shadowKey, shadow); err != nil {
		if k8serrors.IsNotFound(err) && counter != nil {
			counter.mu.Lock()
			counter.counts[poolName]++
			count := counter.counts[poolName]
			ShadowReadFailuresTotal.WithLabelValues(poolName, "not_found").Inc()
			ShadowStaleness.WithLabelValues(poolName).Set(float64(count))
			if count == stalenessThreshold && recorder != nil {
				recorder.Event(cluster, "Warning", "ShadowReadStaleness", fmt.Sprintf("Shadow pool %s read failed %d consecutive times", poolName, count))
			}
			counter.mu.Unlock()
		}
		return err
	}

	if counter != nil {
		counter.mu.Lock()
		counter.counts[poolName] = 0
		ShadowStaleness.WithLabelValues(poolName).Set(0)
		counter.mu.Unlock()
	}

	return reverseSyncNodeCount(ctx, c, cluster, shadow, poolName)
}
