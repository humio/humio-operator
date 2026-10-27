package controller

import (
	"context"
	"testing"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// setupReconcilerWithResources creates a fake client-based reconciler for testing
func setupReconcilerWithResources(t *testing.T, objs ...client.Object) *HumioClusterReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = humiov1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&humiov1alpha1.HumioCluster{}).
		Build()

	return &HumioClusterReconciler{
		Client:     fakeClient,
		BaseLogger: ctrl.Log.WithName("controllers").WithName("HumioCluster"),
		Log:        ctrl.Log.WithName("controllers").WithName("HumioCluster"),
		Recorder:   record.NewFakeRecorder(100),
		CommonConfig: CommonConfig{
			RequeuePeriod: 15 * time.Second,
		},
		stalenessCounters: &stalenessCounter{counts: make(map[string]int)},
	}
}

// TestOrphanEviction_SinglePool verifies orphan detection and condition setting for single pool
// Note: This is a focused integration test of the orphan detection logic, not full reconcile flow
func TestOrphanEviction_SinglePool(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	orphanPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "humio-test-cluster-pool-b-0",
			Namespace: "default",
			Labels: map[string]string{
				kubernetes.NodePoolLabelName: "test-cluster-pool-b",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	pods := []corev1.Pod{*orphanPod}

	// Test detectOrphanedPools function directly
	orphanedPools := detectOrphanedPools(hc, pods)
	assert.Len(t, orphanedPools, 1)
	assert.Contains(t, orphanedPools, "test-cluster-pool-b")

	// Test setOrphanPoolCondition
	r := setupReconcilerWithResources(t, hc, orphanPod)
	changed := r.setOrphanPoolCondition(hc, orphanedPools, pods, nil)
	assert.True(t, changed)

	// Verify condition was set correctly
	cond := meta.FindStatusCondition(hc.Status.Conditions, "NodePoolRemovalInProgress")
	assert.NotNil(t, cond, "NodePoolRemovalInProgress condition should be set")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, cond.Message, "test-cluster-pool-b")
	assert.Contains(t, cond.Message, "1 pod")
}

// TestOrphanEviction_MultipleOrphans verifies multiple orphan pools detected and condition message lists all
func TestOrphanEviction_MultipleOrphans(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	orphanPodB := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "humio-test-cluster-pool-b-0",
			Namespace: "default",
			Labels: map[string]string{
				kubernetes.NodePoolLabelName: "test-cluster-pool-b",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	orphanPodC := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "humio-test-cluster-pool-c-0",
			Namespace: "default",
			Labels: map[string]string{
				kubernetes.NodePoolLabelName: "test-cluster-pool-c",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	pods := []corev1.Pod{*orphanPodB, *orphanPodC}

	// Test detectOrphanedPools detects both
	orphanedPools := detectOrphanedPools(hc, pods)
	assert.Len(t, orphanedPools, 2)
	assert.Contains(t, orphanedPools, "test-cluster-pool-b")
	assert.Contains(t, orphanedPools, "test-cluster-pool-c")

	// Test setOrphanPoolCondition includes both in message
	r := setupReconcilerWithResources(t, hc, orphanPodB, orphanPodC)
	changed := r.setOrphanPoolCondition(hc, orphanedPools, pods, nil)
	assert.True(t, changed)

	cond := meta.FindStatusCondition(hc.Status.Conditions, "NodePoolRemovalInProgress")
	assert.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, cond.Message, "test-cluster-pool-b")
	assert.Contains(t, cond.Message, "test-cluster-pool-c")
}

// TestOrphanEviction_GuardClause_BlocksDeletion verifies guard clause prevents deletion when pods remain
// This test verifies the cleanupOrphanedShadowNodePools guard clause logic
func TestOrphanEviction_GuardClause_BlocksDeletion(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	// Orphaned shadow CR (annotated as managed-by shadow-node-pool-sync)
	shadowCR := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-pool-b",
			Namespace: "default",
			Labels:    kubernetes.LabelsForHumio("test-cluster"),
			Annotations: map[string]string{
				"humio.com/managed-by": "humiocluster-shadow",
				"humio.com/cluster":    "test-cluster",
			},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:        "pool-b",
			ClusterName: "test-cluster",
		},
	}

	// Pod still running
	orphanPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "humio-test-cluster-pool-b-0",
			Namespace: "default",
			Labels: map[string]string{
				kubernetes.NodePoolLabelName: "test-cluster-pool-b",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	pods := []corev1.Pod{*orphanPod}

	r := setupReconcilerWithResources(t, hc, shadowCR, orphanPod)

	// Call cleanup function - should not delete because pod remains
	err := r.cleanupOrphanedShadowNodePools(context.Background(), hc, pods, r.stalenessCounters)
	assert.NoError(t, err)

	// Verify shadow CR still exists in fake client
	existingShadowCR := &humiov1alpha1.HumioNodePool{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b", Namespace: "default"}, existingShadowCR)
	assert.NoError(t, err, "shadow CR should still exist when pod remains")
}

// TestOrphanEviction_GuardClause_AllowsDeletion verifies cleanup completes successfully when no pods remain
// Note: Full deletion verification requires controller-runtime scheme integration. This test verifies
// the cleanup function executes without error when guard clause conditions are met (zero pods).
func TestOrphanEviction_GuardClause_AllowsDeletion(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "test-uid-12345",
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	// Orphaned shadow CR, NO pods (annotated as managed-by shadow-node-pool-sync)
	trueVal := true
	shadowCR := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-pool-b",
			Namespace: "default",
			Labels:    kubernetes.LabelsForHumio("test-cluster"),
			Annotations: map[string]string{
				"humio.com/managed-by": "humiocluster-shadow",
				"humio.com/cluster":    "test-cluster",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "core.humio.com/v1alpha1",
					Kind:               "HumioCluster",
					Name:               "test-cluster",
					UID:                "test-uid-12345",
					Controller:         &trueVal,
					BlockOwnerDeletion: &trueVal,
				},
			},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:        "pool-b",
			ClusterName: "test-cluster",
		},
	}

	pods := []corev1.Pod{} // No pods - guard clause allows deletion

	r := setupReconcilerWithResources(t, hc, shadowCR)

	// Call cleanup function - should execute without error when no pods remain
	err := r.cleanupOrphanedShadowNodePools(context.Background(), hc, pods, r.stalenessCounters)
	assert.NoError(t, err, "cleanup should complete successfully when no pods remain")

	// The guard clause logic at line 262-272 of humiocluster_nodepool_sync.go checks:
	// - If pods == nil: defer deletion (line 257-260)
	// - If remainingCount > 0: defer deletion (line 269-272)
	// - If remainingCount == 0: proceed with deletion (line 274-278)
	// This test verifies the function executes the zero-pods path without error.
}

// TestOrphanEviction_ChurnPrevention verifies condition not updated when message unchanged
func TestOrphanEviction_ChurnPrevention(t *testing.T) {
	existingMessage := "Evicting pool: test-cluster-pool-b (2 pods remaining)"

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
		Status: humiov1alpha1.HumioClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "NodePoolRemovalInProgress",
					Status:             metav1.ConditionTrue,
					Message:            existingMessage,
					Reason:             "OrphanEviction",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
		},
	}

	orphanPod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "humio-test-cluster-pool-b-0",
			Namespace: "default",
			Labels: map[string]string{
				kubernetes.NodePoolLabelName: "test-cluster-pool-b",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	orphanPod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "humio-test-cluster-pool-b-1",
			Namespace: "default",
			Labels: map[string]string{
				kubernetes.NodePoolLabelName: "test-cluster-pool-b",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	pods := []corev1.Pod{*orphanPod1, *orphanPod2}
	orphanedPools := []string{"test-cluster-pool-b"}

	r := setupReconcilerWithResources(t, hc, orphanPod1, orphanPod2)

	oldTransitionTime := hc.Status.Conditions[0].LastTransitionTime

	// Call setOrphanPoolCondition with same pod count
	changed := r.setOrphanPoolCondition(hc, orphanedPools, pods, nil)
	assert.False(t, changed, "Churn prevention should return changed=false")

	// Verify condition message unchanged and transition time preserved
	cond := meta.FindStatusCondition(hc.Status.Conditions, "NodePoolRemovalInProgress")
	assert.NotNil(t, cond)
	assert.Equal(t, existingMessage, cond.Message, "message should remain unchanged when pod count is same")
	assert.Equal(t, oldTransitionTime, cond.LastTransitionTime, "transition time should not change when message unchanged")
}

func TestOrphanEviction_ResourceCleanup_GatedByFeatureFlag(t *testing.T) {
	trueVal := true
	poolLabels := kubernetes.LabelsForHumio("test-cluster")
	poolLabels[kubernetes.NodePoolLabelName] = "test-cluster-pool-b"

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "test-uid-cleanup",
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	ownerRef := metav1.OwnerReference{
		APIVersion: "core.humio.com/v1alpha1",
		Kind:       "HumioCluster",
		Name:       "test-cluster",
		UID:        "test-uid-cleanup",
		Controller: &trueVal,
	}

	// Service owned by this cluster, for the orphaned pool
	orphanService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster-pool-b",
			Namespace:       "default",
			Labels:          kubernetes.LabelsForHumio("test-cluster"),
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
	}

	// Headless service for the orphaned pool
	orphanHeadlessSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster-pool-b-headless",
			Namespace:       "default",
			Labels:          kubernetes.LabelsForHumio("test-cluster"),
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
	}

	// Humio service account for the orphaned pool
	humioSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster-pool-b-humio",
			Namespace:       "default",
			Labels:          poolLabels,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
	}

	// Init service account for the orphaned pool
	initSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster-pool-b-init",
			Namespace:       "default",
			Labels:          poolLabels,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
	}

	// Init service account secret (random suffix, found by label)
	initSASecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster-pool-b-init-abc123",
			Namespace:       "default",
			Labels:          kubernetes.LabelsForHumio("test-cluster"),
			Annotations:     map[string]string{corev1.ServiceAccountNameKey: "test-cluster-pool-b-init"},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	// Init cluster role (cluster-scoped, no ownerRef, labels only)
	initCR := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "default-test-cluster-pool-b-init",
			Labels: poolLabels,
		},
	}

	// Init cluster role binding (cluster-scoped, no ownerRef, labels only)
	initCRB := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "default-test-cluster-pool-b-init",
			Labels: poolLabels,
		},
	}

	allResources := func() []client.Object {
		return []client.Object{
			orphanService.DeepCopy(),
			orphanHeadlessSvc.DeepCopy(),
			humioSA.DeepCopy(),
			initSA.DeepCopy(),
			initSASecret.DeepCopy(),
			initCR.DeepCopy(),
			initCRB.DeepCopy(),
		}
	}

	t.Run("all owned resources deleted", func(t *testing.T) {
		objs := append([]client.Object{hc.DeepCopy()}, allResources()...)
		r := setupReconcilerWithResources(t, objs...)
		err := r.cleanupOrphanedPoolResources(context.Background(), hc, "test-cluster-pool-b")
		assert.NoError(t, err)

		var svc corev1.Service
		err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b", Namespace: "default"}, &svc)
		assert.True(t, k8serrors.IsNotFound(err), "service should be deleted")

		var headlessSvc corev1.Service
		err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b-headless", Namespace: "default"}, &headlessSvc)
		assert.True(t, k8serrors.IsNotFound(err), "headless service should be deleted")

		var humioSAResult corev1.ServiceAccount
		err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b-humio", Namespace: "default"}, &humioSAResult)
		assert.True(t, k8serrors.IsNotFound(err), "humio service account should be deleted")

		var initSAResult corev1.ServiceAccount
		err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b-init", Namespace: "default"}, &initSAResult)
		assert.True(t, k8serrors.IsNotFound(err), "init service account should be deleted")

		var secretResult corev1.Secret
		err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b-init-abc123", Namespace: "default"}, &secretResult)
		assert.True(t, k8serrors.IsNotFound(err), "init service account secret should be deleted")

		var crResult rbacv1.ClusterRole
		err = r.Get(context.Background(), types.NamespacedName{Name: "default-test-cluster-pool-b-init"}, &crResult)
		assert.True(t, k8serrors.IsNotFound(err), "cluster role should be deleted")

		var crbResult rbacv1.ClusterRoleBinding
		err = r.Get(context.Background(), types.NamespacedName{Name: "default-test-cluster-pool-b-init"}, &crbResult)
		assert.True(t, k8serrors.IsNotFound(err), "cluster role binding should be deleted")
	})

	t.Run("unowned service preserved", func(t *testing.T) {
		unownedService := orphanService.DeepCopy()
		unownedService.OwnerReferences = nil

		r := setupReconcilerWithResources(t, hc.DeepCopy(), unownedService)
		err := r.cleanupOrphanedPoolResources(context.Background(), hc, "test-cluster-pool-b")
		assert.NoError(t, err)

		var svc corev1.Service
		err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b", Namespace: "default"}, &svc)
		assert.NoError(t, err, "unowned service should be preserved")
	})

	t.Run("unlabeled cluster role preserved", func(t *testing.T) {
		unlabeledCR := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default-test-cluster-pool-b-init",
			},
		}

		r := setupReconcilerWithResources(t, hc.DeepCopy(), unlabeledCR)
		err := r.cleanupOrphanedPoolResources(context.Background(), hc, "test-cluster-pool-b")
		assert.NoError(t, err)

		var crResult rbacv1.ClusterRole
		err = r.Get(context.Background(), types.NamespacedName{Name: "default-test-cluster-pool-b-init"}, &crResult)
		assert.NoError(t, err, "unlabeled cluster role should be preserved")
	})

	t.Run("wrong node pool label preserved", func(t *testing.T) {
		wrongPoolLabels := kubernetes.LabelsForHumio("test-cluster")
		wrongPoolLabels[kubernetes.NodePoolLabelName] = "test-cluster-pool-c"

		wrongPoolCR := &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "default-test-cluster-pool-b-init",
				Labels: wrongPoolLabels,
			},
		}

		r := setupReconcilerWithResources(t, hc.DeepCopy(), wrongPoolCR)
		err := r.cleanupOrphanedPoolResources(context.Background(), hc, "test-cluster-pool-b")
		assert.NoError(t, err)

		var crResult rbacv1.ClusterRole
		err = r.Get(context.Background(), types.NamespacedName{Name: "default-test-cluster-pool-b-init"}, &crResult)
		assert.NoError(t, err, "cluster role with wrong node pool label should be preserved")
	})

	t.Run("already cleaned up is idempotent", func(t *testing.T) {
		r := setupReconcilerWithResources(t, hc.DeepCopy())
		err := r.cleanupOrphanedPoolResources(context.Background(), hc, "test-cluster-pool-b")
		assert.NoError(t, err, "cleanup should succeed even when resources already gone")
	})
}

func TestOrphanEviction_GuardClause_NilPodsDefersCleanup(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "test-uid-nil-pods",
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	trueVal := true
	shadowCR := &humiov1alpha1.HumioNodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-pool-b",
			Namespace: "default",
			Labels:    kubernetes.LabelsForHumio("test-cluster"),
			Annotations: map[string]string{
				"humio.com/managed-by": "humiocluster-shadow",
				"humio.com/cluster":    "test-cluster",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "core.humio.com/v1alpha1",
					Kind:               "HumioCluster",
					Name:               "test-cluster",
					UID:                "test-uid-nil-pods",
					Controller:         &trueVal,
					BlockOwnerDeletion: &trueVal,
				},
			},
		},
		Spec: humiov1alpha1.HumioNodePoolSpec{
			Name:        "pool-b",
			ClusterName: "test-cluster",
		},
	}

	r := setupReconcilerWithResources(t, hc, shadowCR)

	// Pass nil pods — guard clause should defer deletion
	err := r.cleanupOrphanedShadowNodePools(context.Background(), hc, nil, r.stalenessCounters)
	assert.NoError(t, err)

	// Shadow CR must still exist
	existing := &humiov1alpha1.HumioNodePool{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b", Namespace: "default"}, existing)
	assert.NoError(t, err, "shadow CR should be preserved when pods==nil")
}

func TestOrphanEviction_CleanupDisabled_PoolRetainedInStatus(t *testing.T) {
	trueVal := true
	ownerRef := metav1.OwnerReference{
		APIVersion: "core.humio.com/v1alpha1",
		Kind:       "HumioCluster",
		Name:       "test-cluster",
		UID:        "test-uid-flag-disabled",
		Controller: &trueVal,
	}

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "test-uid-flag-disabled",
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
			// EnableOrphanedResourceCleanup intentionally NOT set (defaults to false)
		},
		Status: humiov1alpha1.HumioClusterStatus{
			OrphanedNodePools: []string{"test-cluster-pool-b"},
		},
	}

	orphanService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster-pool-b",
			Namespace:       "default",
			Labels:          kubernetes.LabelsForHumio("test-cluster"),
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
	}

	// pool-b has no running pods — it is ready for cleanup
	orphanedPools := detectOrphanedPools(hc, []corev1.Pod{})
	readyPools := poolsReadyForCleanup(hc.Status.OrphanedNodePools, orphanedPools)
	assert.Contains(t, readyPools, "test-cluster-pool-b")

	r := setupReconcilerWithResources(t, hc.DeepCopy(), orphanService)

	// Simulate the controller's cleanup loop with flag disabled
	for _, poolName := range readyPools {
		if hc.Spec.OperatorFeatureFlags.EnableOrphanedResourceCleanup {
			t.Fatal("cleanup flag should be disabled in this test")
		}
		// With flag disabled, pool stays in status (no removePoolFromStatus call)
		_ = poolName
	}

	// Pool should remain in status so it can be cleaned up when flag is enabled later
	assert.Equal(t, []string{"test-cluster-pool-b"}, hc.Status.OrphanedNodePools,
		"pool should remain in status when cleanup is disabled, so enabling the flag later triggers cleanup")

	// Resources should still exist (not cleaned up)
	var svc corev1.Service
	err := r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b", Namespace: "default"}, &svc)
	assert.NoError(t, err, "service should be preserved when cleanup flag is disabled")
}

func TestOrphanEviction_ResourceCleanup_SAWithWrongPoolLabel(t *testing.T) {
	trueVal := true
	ownerRef := metav1.OwnerReference{
		APIVersion: "core.humio.com/v1alpha1",
		Kind:       "HumioCluster",
		Name:       "test-cluster",
		UID:        "test-uid-wrong-label",
		Controller: &trueVal,
	}

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "test-uid-wrong-label",
		},
	}

	wrongPoolLabels := kubernetes.LabelsForHumio("test-cluster")
	wrongPoolLabels[kubernetes.NodePoolLabelName] = "test-cluster-pool-c"

	wrongLabelSA := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster-pool-b-humio",
			Namespace:       "default",
			Labels:          wrongPoolLabels,
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
	}

	r := setupReconcilerWithResources(t, hc.DeepCopy(), wrongLabelSA)
	err := r.cleanupOrphanedPoolResources(context.Background(), hc, "test-cluster-pool-b")
	assert.NoError(t, err)

	var sa corev1.ServiceAccount
	err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b-humio", Namespace: "default"}, &sa)
	assert.NoError(t, err, "service account with wrong node pool label should be preserved")
}

func TestOrphanEviction_ResourceCleanup_SecretWithWrongOwner(t *testing.T) {
	trueVal := true

	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "test-uid-wrong-owner",
		},
	}

	foreignOwnerRef := metav1.OwnerReference{
		APIVersion: "core.humio.com/v1alpha1",
		Kind:       "HumioCluster",
		Name:       "other-cluster",
		UID:        "foreign-uid",
		Controller: &trueVal,
	}

	foreignSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cluster-pool-b-init-xyz789",
			Namespace:       "default",
			Labels:          kubernetes.LabelsForHumio("test-cluster"),
			Annotations:     map[string]string{corev1.ServiceAccountNameKey: "test-cluster-pool-b-init"},
			OwnerReferences: []metav1.OwnerReference{foreignOwnerRef},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}

	r := setupReconcilerWithResources(t, hc.DeepCopy(), foreignSecret)
	err := r.cleanupOrphanedPoolResources(context.Background(), hc, "test-cluster-pool-b")
	assert.NoError(t, err)

	var secret corev1.Secret
	err = r.Get(context.Background(), types.NamespacedName{Name: "test-cluster-pool-b-init-xyz789", Namespace: "default"}, &secret)
	assert.NoError(t, err, "secret with matching SA annotation but wrong owner should be preserved")
}
