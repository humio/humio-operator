package controller

import (
	"context"
	"fmt"
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// detectOrphanedPools identifies node pool labels present in pods that are not defined in the HumioCluster spec.
// Returns a deduplicated slice of orphaned pool label values.
// NOT goroutine-safe — caller MUST serialize invocations.
func detectOrphanedPools(hc *humiov1alpha1.HumioCluster, pods []corev1.Pod) []string {
	// Build set of expected pool names
	expectedPools := make(map[string]bool)

	// Add main pool if NodeCount is set or Autoscaling is enabled
	if hc.Spec.NodeCount != nil || hc.Spec.Autoscaling != nil {
		expectedPools[hc.Name] = true
	}

	// Add named pools from spec
	for _, pool := range hc.Spec.NodePools {
		poolName := strings.Join([]string{hc.Name, pool.Name}, "-")
		expectedPools[poolName] = true
	}

	// Collect unique pool names from pods
	foundPools := make(map[string]bool)
	for _, pod := range pods {
		if poolLabel, ok := pod.Labels[kubernetes.NodePoolLabelName]; ok && poolLabel != "" {
			foundPools[poolLabel] = true
		}
	}

	// Find orphans: pools in pods but not in spec
	var orphans []string
	for poolName := range foundPools {
		if !expectedPools[poolName] {
			orphans = append(orphans, poolName)
		}
	}

	return orphans
}

// buildEphemeralDescriptor creates a temporary HumioNodePool descriptor for orphaned node pool operations
func buildEphemeralDescriptor(hc *humiov1alpha1.HumioCluster, orphanedPoolName string) (*HumioNodePool, error) {
	// Extract raw node pool name from full pool label
	// For "test-cluster-pool-b" -> "pool-b"
	// For "test-cluster" -> "" (main pool)
	clusterName := hc.Name
	var rawNodePoolName string
	if orphanedPoolName != clusterName {
		prefix := clusterName + "-"
		if !strings.HasPrefix(orphanedPoolName, prefix) {
			return nil, fmt.Errorf("building ephemeral descriptor for pool %s: pool name does not have expected prefix %q", orphanedPoolName, prefix)
		}
		rawNodePoolName = orphanedPoolName[len(prefix):]
	}

	descriptor := &HumioNodePool{
		namespace:                hc.Namespace,
		clusterName:              clusterName,
		nodePoolName:             rawNodePoolName,
		humioNodeSpec:            hc.Spec.HumioNodeSpec,
		enableDownscalingFeature: true,
	}

	// Override nodeCount to 0 for ephemeral descriptor
	descriptor.humioNodeSpec.NodeCount = new(int32)
	*descriptor.humioNodeSpec.NodeCount = 0

	// Validate all required methods return non-zero values
	if err := validateEphemeralDescriptor(descriptor); err != nil {
		return nil, fmt.Errorf("building ephemeral descriptor for pool %s: %w", orphanedPoolName, err)
	}

	return descriptor, nil
}

// validateEphemeralDescriptor ensures all required HumioNodePoolInterface methods return non-zero values
func validateEphemeralDescriptor(descriptor *HumioNodePool) error {
	if descriptor.GetNamespace() == "" {
		return fmt.Errorf("validation failed: method GetNamespace returned zero value")
	}
	if descriptor.GetClusterName() == "" {
		return fmt.Errorf("validation failed: method GetClusterName returned zero value")
	}
	if descriptor.GetImage() == "" {
		return fmt.Errorf("validation failed: method GetImage returned zero value")
	}
	if descriptor.GetNodePoolLabels() == nil || len(descriptor.GetNodePoolLabels()) == 0 {
		return fmt.Errorf("validation failed: method GetNodePoolLabels returned zero value")
	}
	return nil
}

// isRemainingPod returns true if the pod blocks shadow CR deletion.
func isRemainingPod(pod corev1.Pod) bool {
	if pod.DeletionTimestamp != nil {
		return true
	}
	switch pod.Status.Phase {
	case corev1.PodRunning, corev1.PodPending, corev1.PodUnknown:
		return true
	default:
		return false
	}
}

func (r *HumioClusterReconciler) setOrphanPoolCondition(
	hc *humiov1alpha1.HumioCluster,
	orphanedPools []string,
	pods []corev1.Pod,
	pendingCleanup []string,
) bool {
	newMessage := buildConditionMessage(orphanedPools, pods, pendingCleanup)
	newStatus := metav1.ConditionTrue
	if len(orphanedPools) == 0 && len(pendingCleanup) == 0 {
		newStatus = metav1.ConditionFalse
		newMessage = ""
	}

	existing := meta.FindStatusCondition(hc.Status.Conditions, "NodePoolRemovalInProgress")
	if existing != nil && existing.Status == newStatus && existing.Message == newMessage {
		r.Log.V(1).Info("skipping condition update, no change", "pools", orphanedPools)
		return false
	}

	meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
		Type:    "NodePoolRemovalInProgress",
		Status:  newStatus,
		Reason:  "OrphanEviction",
		Message: newMessage,
	})

	return true
}

func buildConditionMessage(orphanedPools []string, pods []corev1.Pod, pendingCleanup []string) string {
	if len(orphanedPools) == 0 && len(pendingCleanup) == 0 {
		return ""
	}

	messages := make([]string, 0, len(orphanedPools)+len(pendingCleanup))

	for _, poolName := range orphanedPools {
		count := 0
		for _, pod := range pods {
			if pod.Labels[kubernetes.NodePoolLabelName] == poolName && isRemainingPod(pod) {
				count++
			}
		}
		messages = append(messages, fmt.Sprintf("Evicting pool: %s (%d pods remaining)", poolName, count))
	}

	for _, poolName := range pendingCleanup {
		messages = append(messages, fmt.Sprintf("Cleaning up pool: %s (0 pods, resources pending)", poolName))
	}

	return strings.Join(messages, "; ")
}

func mergeOrphanedPoolsIntoStatus(hc *humiov1alpha1.HumioCluster, detected []string) bool {
	existing := make(map[string]bool, len(hc.Status.OrphanedNodePools))
	for _, name := range hc.Status.OrphanedNodePools {
		existing[name] = true
	}
	changed := false
	for _, name := range detected {
		if !existing[name] {
			hc.Status.OrphanedNodePools = append(hc.Status.OrphanedNodePools, name)
			changed = true
		}
	}
	return changed
}

func poolsReadyForCleanup(statusPools []string, currentlyOrphaned []string) []string {
	stillOrphaned := make(map[string]bool, len(currentlyOrphaned))
	for _, name := range currentlyOrphaned {
		stillOrphaned[name] = true
	}
	var ready []string
	for _, name := range statusPools {
		if !stillOrphaned[name] {
			ready = append(ready, name)
		}
	}
	return ready
}

func removePoolFromStatus(hc *humiov1alpha1.HumioCluster, poolName string) {
	filtered := hc.Status.OrphanedNodePools[:0]
	for _, name := range hc.Status.OrphanedNodePools {
		if name != poolName {
			filtered = append(filtered, name)
		}
	}
	hc.Status.OrphanedNodePools = filtered
}

// cleanupOrphanedPoolResources deletes resources associated with an orphaned pool.
// Only deletes resources that are both named for this pool AND owned by this HumioCluster
// (or label-matched for cluster-scoped resources that cannot have ownerReferences).
// PVCs are intentionally left intact — the original pool's reclaim policy is unknown.
func (r *HumioClusterReconciler) cleanupOrphanedPoolResources(ctx context.Context, hc *humiov1alpha1.HumioCluster, orphanedPoolName string) error {
	// Delete per-pool service (e.g., "test-cluster-pool-b")
	if err := r.deleteOwnedService(ctx, hc, orphanedPoolName); err != nil {
		return err
	}
	// Delete per-pool headless service (e.g., "test-cluster-pool-b-headless")
	if err := r.deleteOwnedService(ctx, hc, poolHeadlessServiceName(orphanedPoolName)); err != nil {
		return err
	}
	// Delete per-pool humio service account (e.g., "test-cluster-pool-b-humio")
	humioSAName := fmt.Sprintf("%s-%s", orphanedPoolName, HumioServiceAccountNameSuffix)
	if err := r.deleteOwnedServiceAccount(ctx, hc, humioSAName, orphanedPoolName); err != nil {
		return err
	}
	// Delete per-pool init service account (e.g., "test-cluster-pool-b-init")
	initSAName := fmt.Sprintf("%s-%s", orphanedPoolName, initServiceAccountNameSuffix)
	if err := r.deleteOwnedServiceAccount(ctx, hc, initSAName, orphanedPoolName); err != nil {
		return err
	}
	// Delete per-pool init service account secrets (found by label, random name suffix)
	if err := r.deleteOwnedServiceAccountSecrets(ctx, hc, initSAName); err != nil {
		return err
	}
	// Delete per-pool init cluster role (e.g., "default-test-cluster-pool-b-init")
	initCRName := fmt.Sprintf("%s-%s-%s", hc.Namespace, orphanedPoolName, initClusterRoleSuffix)
	if err := r.deleteClusterRoleByName(ctx, hc, initCRName, orphanedPoolName); err != nil {
		return err
	}
	// Delete per-pool init cluster role binding (e.g., "default-test-cluster-pool-b-init")
	initCRBName := fmt.Sprintf("%s-%s-%s", hc.Namespace, orphanedPoolName, initClusterRoleBindingSuffix)
	if err := r.deleteClusterRoleBindingByName(ctx, hc, initCRBName, orphanedPoolName); err != nil {
		return err
	}
	return nil
}

func (r *HumioClusterReconciler) deleteOwnedService(ctx context.Context, hc *humiov1alpha1.HumioCluster, serviceName string) error {
	var svc corev1.Service
	err := r.Get(ctx, types.NamespacedName{Name: serviceName, Namespace: hc.Namespace}, &svc)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get service %s: %w", serviceName, err)
	}
	if !isOwnedBy(&svc, hc) || !hasExpectedLabels(&svc, kubernetes.LabelsForHumio(hc.Name)) {
		return nil
	}

	r.Log.Info("deleting orphaned pool service", "service", serviceName, "cluster", hc.Name)
	if err := r.Delete(ctx, &svc); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete orphaned service %s: %w", serviceName, err)
	}
	return nil
}

func (r *HumioClusterReconciler) deleteOwnedServiceAccount(ctx context.Context, hc *humiov1alpha1.HumioCluster, saName, orphanedPoolName string) error {
	var sa corev1.ServiceAccount
	err := r.Get(ctx, types.NamespacedName{Name: saName, Namespace: hc.Namespace}, &sa)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get service account %s: %w", saName, err)
	}
	expectedLabels := kubernetes.MatchingLabelsForHumioNodePool(hc.Name, orphanedPoolName)
	if !isOwnedBy(&sa, hc) || !hasExpectedLabels(&sa, expectedLabels) {
		return nil
	}

	r.Log.Info("deleting orphaned pool service account", "serviceAccount", saName, "cluster", hc.Name)
	if err := r.Delete(ctx, &sa); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete orphaned service account %s: %w", saName, err)
	}
	return nil
}

func (r *HumioClusterReconciler) deleteOwnedServiceAccountSecrets(ctx context.Context, hc *humiov1alpha1.HumioCluster, saName string) error {
	var secretList corev1.SecretList
	if err := r.List(ctx, &secretList,
		client.InNamespace(hc.Namespace),
		client.MatchingLabels(kubernetes.LabelsForHumio(hc.Name)),
	); err != nil {
		return fmt.Errorf("failed to list secrets for orphaned pool cleanup: %w", err)
	}

	for idx := range secretList.Items {
		secret := &secretList.Items[idx]
		if secret.Annotations[corev1.ServiceAccountNameKey] != saName {
			continue
		}
		if !isOwnedBy(secret, hc) || !hasExpectedLabels(secret, kubernetes.LabelsForHumio(hc.Name)) {
			continue
		}
		r.Log.Info("deleting orphaned pool service account secret", "secret", secret.Name, "cluster", hc.Name)
		if err := r.Delete(ctx, secret); err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete orphaned service account secret %s: %w", secret.Name, err)
		}
	}
	return nil
}

func (r *HumioClusterReconciler) deleteClusterRoleByName(ctx context.Context, hc *humiov1alpha1.HumioCluster, crName, orphanedPoolName string) error {
	var cr rbacv1.ClusterRole
	err := r.Get(ctx, types.NamespacedName{Name: crName}, &cr)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get cluster role %s: %w", crName, err)
	}
	expectedLabels := kubernetes.MatchingLabelsForHumioNodePool(hc.Name, orphanedPoolName)
	if !hasExpectedLabels(&cr, expectedLabels) {
		return nil
	}

	r.Log.Info("deleting orphaned pool cluster role", "clusterRole", crName, "cluster", hc.Name)
	if err := r.Delete(ctx, &cr); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete orphaned cluster role %s: %w", crName, err)
	}
	return nil
}

func (r *HumioClusterReconciler) deleteClusterRoleBindingByName(ctx context.Context, hc *humiov1alpha1.HumioCluster, crbName, orphanedPoolName string) error {
	var crb rbacv1.ClusterRoleBinding
	err := r.Get(ctx, types.NamespacedName{Name: crbName}, &crb)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get cluster role binding %s: %w", crbName, err)
	}
	expectedLabels := kubernetes.MatchingLabelsForHumioNodePool(hc.Name, orphanedPoolName)
	if !hasExpectedLabels(&crb, expectedLabels) {
		return nil
	}

	r.Log.Info("deleting orphaned pool cluster role binding", "clusterRoleBinding", crbName, "cluster", hc.Name)
	if err := r.Delete(ctx, &crb); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete orphaned cluster role binding %s: %w", crbName, err)
	}
	return nil
}

func hasExpectedLabels(obj client.Object, expected map[string]string) bool {
	objLabels := obj.GetLabels()
	if objLabels == nil {
		return false
	}
	for k, v := range expected {
		if objLabels[k] != v {
			return false
		}
	}
	return true
}
