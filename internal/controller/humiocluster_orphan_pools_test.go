package controller

import (
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestBuildEphemeralDescriptor_NamedPool(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	descriptor, err := buildEphemeralDescriptor(hc, "test-cluster-pool-b")
	assert.NoError(t, err)
	assert.NotNil(t, descriptor)

	// Verify all required methods return correct values (DESIGN Section 5.2)
	assert.Equal(t, "default", descriptor.GetNamespace())
	assert.Equal(t, "test-cluster", descriptor.GetClusterName())
	assert.Equal(t, "pool-b", descriptor.GetRawNodePoolName())
	assert.Equal(t, 0, descriptor.GetNodeCount())
	assert.True(t, descriptor.IsDownscalingFeatureEnabled())
	assert.Equal(t, "humio/humio:latest", descriptor.GetImage())
	assert.NotEmpty(t, descriptor.GetNodePoolLabels())
}

func TestBuildEphemeralDescriptor_MainPool(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	descriptor, err := buildEphemeralDescriptor(hc, "test-cluster")
	assert.NoError(t, err)
	assert.NotNil(t, descriptor)

	assert.Equal(t, "test-cluster", descriptor.GetClusterName())
	assert.Equal(t, "", descriptor.GetRawNodePoolName()) // main pool has empty nodePoolName
	assert.Equal(t, 0, descriptor.GetNodeCount())
}

// Testability Review P-003, P-004: validation failure test
func TestBuildEphemeralDescriptor_ValidationFailure(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: ""},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	descriptor, err := buildEphemeralDescriptor(hc, "test-cluster-pool-b")
	assert.Nil(t, descriptor)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "building ephemeral descriptor for pool test-cluster-pool-b")
	assert.Contains(t, err.Error(), "validation failed: method GetNamespace returned zero value")
}

func TestBuildEphemeralDescriptor_EmptyImageUsesDefault(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "",
			},
		},
	}

	descriptor, err := buildEphemeralDescriptor(hc, "test-cluster-pool-b")
	assert.NoError(t, err)
	assert.NotNil(t, descriptor)
	assert.NotEmpty(t, descriptor.GetImage(), "should fall back to default image when spec.image is empty")
}

func TestBuildEphemeralDescriptor_MalformedPoolName(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
		},
	}

	descriptor, err := buildEphemeralDescriptor(hc, "unrelated-pool-name")
	assert.Nil(t, descriptor)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not have expected prefix")
}

func TestDetectOrphanedPools_NoOrphans(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
		},
	}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-a"}}},
	}

	orphans := detectOrphanedPools(hc, pods)
	assert.Empty(t, orphans)
}

func TestDetectOrphanedPools_SingleOrphan(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
		},
	}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
	}

	orphans := detectOrphanedPools(hc, pods)
	assert.ElementsMatch(t, []string{"test-cluster-pool-b"}, orphans)
}

func TestDetectOrphanedPools_MultipleOrphans(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
				{Name: "pool-c"},
			},
		},
	}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-a"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-c"}}},
	}

	orphans := detectOrphanedPools(hc, pods)
	assert.ElementsMatch(t, []string{"test-cluster-pool-b"}, orphans)
}

func TestDetectOrphanedPools_MainPoolExpected(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				NodeCount: ptr.To(int32(3)), // main pool
			},
		},
	}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
	}

	orphans := detectOrphanedPools(hc, pods)
	assert.ElementsMatch(t, []string{"test-cluster-pool-b"}, orphans)
}

func TestDetectOrphanedPools_MainPoolWithAutoscaling(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Autoscaling: &humiov1alpha1.AutoscalingSpec{
					MinReplicas: ptr.To(int32(3)),
					MaxReplicas: int32(10),
				},
			},
		},
	}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
	}

	orphans := detectOrphanedPools(hc, pods)
	assert.ElementsMatch(t, []string{"test-cluster-pool-b"}, orphans, "Main pool with autoscaling should not be detected as orphaned")
}

func TestDetectOrphanedPools_EmptyInput(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec:       humiov1alpha1.HumioClusterSpec{},
	}
	pods := []corev1.Pod{}

	orphans := detectOrphanedPools(hc, pods)
	assert.Empty(t, orphans)
}

// Testability Review P-001: verify deduplication logic
func TestDetectOrphanedPools_DuplicatePoolLabels(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
		},
	}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-c"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-c"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-c"}}},
	}

	orphans := detectOrphanedPools(hc, pods)
	assert.Len(t, orphans, 2) // deduplication works
	assert.ElementsMatch(t, []string{"test-cluster-pool-b", "test-cluster-pool-c"}, orphans)
}

func TestDetectOrphanedPools_MissingAndEmptyLabels(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a"},
			},
		},
	}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-a"}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: ""}}},
		{ObjectMeta: metav1.ObjectMeta{}},
	}

	orphans := detectOrphanedPools(hc, pods)
	assert.Empty(t, orphans, "pods with missing or empty node pool labels should be ignored")
}

func TestIsRemainingPod_Phases(t *testing.T) {
	tests := []struct {
		phase    corev1.PodPhase
		expected bool
	}{
		{corev1.PodRunning, true},
		{corev1.PodPending, true},
		{corev1.PodUnknown, true},
		{corev1.PodSucceeded, false},
		{corev1.PodFailed, false},
	}

	for _, tt := range tests {
		pod := corev1.Pod{Status: corev1.PodStatus{Phase: tt.phase}}
		assert.Equal(t, tt.expected, isRemainingPod(pod), "phase: %s", tt.phase)
	}
}

func TestIsRemainingPod_DeletionTimestamp(t *testing.T) {
	now := metav1.Now()
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	assert.True(t, isRemainingPod(pod))
}

func TestIsRemainingPod_DeletionTimestamp_RunningPhase(t *testing.T) {
	now := metav1.Now()
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	assert.True(t, isRemainingPod(pod))
}

// Verifies the scale-to-zero eviction path: when a pool is removed from spec,
// the ephemeral descriptor has nodeCount=0 which causes processDownscaling to
// trigger eviction (podsNotMarkedForEviction > nodeCount).
func TestScaleToZero_EphemeralDescriptorTriggersEviction(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: humiov1alpha1.HumioClusterSpec{
			HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
				Image: "humio/humio:latest",
			},
			NodePools: []humiov1alpha1.HumioNodePoolSpec{
				{Name: "pool-a", HumioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(3))}},
			},
		},
	}

	// pool-b was removed from spec but still has running pods
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-a"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	}

	// detectOrphanedPools identifies pool-b as orphaned
	orphans := detectOrphanedPools(hc, pods)
	assert.Equal(t, []string{"test-cluster-pool-b"}, orphans)

	// buildEphemeralDescriptor creates a descriptor with nodeCount=0 and downscaling enabled
	descriptor, err := buildEphemeralDescriptor(hc, "test-cluster-pool-b")
	assert.NoError(t, err)
	assert.Equal(t, 0, descriptor.GetNodeCount())
	assert.True(t, descriptor.IsDownscalingFeatureEnabled())
	assert.Equal(t, "pool-b", descriptor.GetRawNodePoolName())
	assert.Equal(t, "default", descriptor.GetNamespace())
}

func TestSetOrphanPoolCondition_OrphansExist(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status:     humiov1alpha1.HumioClusterStatus{},
	}
	orphanedPools := []string{"test-cluster-pool-b"}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	}

	r := &HumioClusterReconciler{}
	changed := r.setOrphanPoolCondition(hc, orphanedPools, pods, nil)
	assert.True(t, changed, "Setting condition for first time should return changed=true")

	cond := meta.FindStatusCondition(hc.Status.Conditions, "NodePoolRemovalInProgress")
	assert.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, cond.Message, "test-cluster-pool-b")
	assert.Contains(t, cond.Message, "2 pods")
}

func TestSetOrphanPoolCondition_NoOrphans(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status:     humiov1alpha1.HumioClusterStatus{},
	}

	r := &HumioClusterReconciler{}
	changed := r.setOrphanPoolCondition(hc, []string{}, []corev1.Pod{}, nil)
	assert.True(t, changed, "Clearing condition for first time should return changed=true")

	cond := meta.FindStatusCondition(hc.Status.Conditions, "NodePoolRemovalInProgress")
	assert.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
}

func TestSetOrphanPoolCondition_ChurnPrevention(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status: humiov1alpha1.HumioClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:    "NodePoolRemovalInProgress",
					Status:  metav1.ConditionTrue,
					Message: "Evicting pool: test-cluster-pool-b (2 pods remaining)",
				},
			},
		},
	}
	orphanedPools := []string{"test-cluster-pool-b"}
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{kubernetes.NodePoolLabelName: "test-cluster-pool-b"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	}

	r := &HumioClusterReconciler{}
	changed := r.setOrphanPoolCondition(hc, orphanedPools, pods, nil)
	assert.False(t, changed, "Calling with identical condition should return changed=false (churn prevention)")
}

func TestSetOrphanPoolCondition_PendingCleanup(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status: humiov1alpha1.HumioClusterStatus{
			OrphanedNodePools: []string{"test-cluster-pool-b"},
		},
	}

	r := &HumioClusterReconciler{}
	changed := r.setOrphanPoolCondition(hc, []string{}, []corev1.Pod{}, []string{"test-cluster-pool-b"})
	assert.True(t, changed)

	cond := meta.FindStatusCondition(hc.Status.Conditions, "NodePoolRemovalInProgress")
	assert.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, cond.Message, "Cleaning up pool: test-cluster-pool-b")
	assert.Contains(t, cond.Message, "resources pending")
}

func TestSetOrphanPoolCondition_TransitionTrueToFalse(t *testing.T) {
	hc := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Status: humiov1alpha1.HumioClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:    "NodePoolRemovalInProgress",
					Status:  metav1.ConditionTrue,
					Reason:  "OrphanEviction",
					Message: "Evicting pool: test-cluster-pool-b (2 pods remaining)",
				},
			},
		},
	}

	r := &HumioClusterReconciler{}
	changed := r.setOrphanPoolCondition(hc, []string{}, []corev1.Pod{}, nil)
	assert.True(t, changed, "transitioning from True to False should return changed=true")

	cond := meta.FindStatusCondition(hc.Status.Conditions, "NodePoolRemovalInProgress")
	assert.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Empty(t, cond.Message)
}

func TestMergeOrphanedPoolsIntoStatus(t *testing.T) {
	t.Run("adds new pools", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{}
		changed := mergeOrphanedPoolsIntoStatus(hc, []string{"pool-a", "pool-b"})
		assert.True(t, changed)
		assert.ElementsMatch(t, []string{"pool-a", "pool-b"}, hc.Status.OrphanedNodePools)
	})

	t.Run("idempotent on duplicates", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			Status: humiov1alpha1.HumioClusterStatus{
				OrphanedNodePools: []string{"pool-a"},
			},
		}
		changed := mergeOrphanedPoolsIntoStatus(hc, []string{"pool-a"})
		assert.False(t, changed)
		assert.Equal(t, []string{"pool-a"}, hc.Status.OrphanedNodePools)
	})

	t.Run("adds only new pools", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			Status: humiov1alpha1.HumioClusterStatus{
				OrphanedNodePools: []string{"pool-a"},
			},
		}
		changed := mergeOrphanedPoolsIntoStatus(hc, []string{"pool-a", "pool-b"})
		assert.True(t, changed)
		assert.ElementsMatch(t, []string{"pool-a", "pool-b"}, hc.Status.OrphanedNodePools)
	})
}

func TestPoolsReadyForCleanup(t *testing.T) {
	t.Run("pool in status but no longer orphaned", func(t *testing.T) {
		ready := poolsReadyForCleanup([]string{"pool-a", "pool-b"}, []string{"pool-a"})
		assert.Equal(t, []string{"pool-b"}, ready)
	})

	t.Run("all still orphaned", func(t *testing.T) {
		ready := poolsReadyForCleanup([]string{"pool-a"}, []string{"pool-a"})
		assert.Empty(t, ready)
	})

	t.Run("empty status", func(t *testing.T) {
		ready := poolsReadyForCleanup([]string{}, []string{"pool-a"})
		assert.Empty(t, ready)
	})

	t.Run("all ready", func(t *testing.T) {
		ready := poolsReadyForCleanup([]string{"pool-a", "pool-b"}, []string{})
		assert.ElementsMatch(t, []string{"pool-a", "pool-b"}, ready)
	})
}

func TestRemovePoolFromStatus(t *testing.T) {
	t.Run("removes single pool", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			Status: humiov1alpha1.HumioClusterStatus{
				OrphanedNodePools: []string{"pool-a", "pool-b", "pool-c"},
			},
		}
		removePoolFromStatus(hc, "pool-b")
		assert.Equal(t, []string{"pool-a", "pool-c"}, hc.Status.OrphanedNodePools)
	})

	t.Run("removes last element", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			Status: humiov1alpha1.HumioClusterStatus{
				OrphanedNodePools: []string{"pool-a"},
			},
		}
		removePoolFromStatus(hc, "pool-a")
		assert.Empty(t, hc.Status.OrphanedNodePools)
	})

	t.Run("no-op for missing pool", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			Status: humiov1alpha1.HumioClusterStatus{
				OrphanedNodePools: []string{"pool-a"},
			},
		}
		removePoolFromStatus(hc, "pool-z")
		assert.Equal(t, []string{"pool-a"}, hc.Status.OrphanedNodePools)
	})
}

func TestHasExpectedLabels(t *testing.T) {
	obj := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app.kubernetes.io/instance":   "test-cluster",
				"app.kubernetes.io/managed-by": "humio-operator",
				"app.kubernetes.io/name":       "humio",
				"extra":                        "label",
			},
		},
	}

	t.Run("all expected labels present", func(t *testing.T) {
		assert.True(t, hasExpectedLabels(obj, kubernetes.LabelsForHumio("test-cluster")))
	})

	t.Run("wrong cluster name", func(t *testing.T) {
		assert.False(t, hasExpectedLabels(obj, kubernetes.LabelsForHumio("other-cluster")))
	})

	t.Run("nil labels", func(t *testing.T) {
		nilObj := &corev1.Service{ObjectMeta: metav1.ObjectMeta{}}
		assert.False(t, hasExpectedLabels(nilObj, kubernetes.LabelsForHumio("test-cluster")))
	})

	t.Run("superset match with node pool label", func(t *testing.T) {
		objWithPool := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Labels: kubernetes.MatchingLabelsForHumioNodePool("test-cluster", "test-cluster-pool-b"),
			},
		}
		assert.True(t, hasExpectedLabels(objWithPool, kubernetes.MatchingLabelsForHumioNodePool("test-cluster", "test-cluster-pool-b")))
		assert.False(t, hasExpectedLabels(objWithPool, kubernetes.MatchingLabelsForHumioNodePool("test-cluster", "test-cluster-pool-c")))
	})
}
