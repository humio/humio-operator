package controller

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMergeEnvVars(t *testing.T) {
	testCases := []struct {
		name     string
		from     []corev1.EnvVar
		into     []corev1.EnvVar
		expected []corev1.EnvVar
	}{
		{
			name: "no from",
			from: []corev1.EnvVar{},
			into: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
		},
		{
			name: "no duplicates",
			from: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
			into: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
		},
		{
			name: "duplicates",
			from: []corev1.EnvVar{
				{Name: "DUPLICATE_ENV_VAR", Value: "common_value"},
			},
			into: []corev1.EnvVar{
				{Name: "NODE_ENV_VAR", Value: "nodepool_value"},
				{Name: "DUPLICATE_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODE_ENV_VAR", Value: "nodepool_value"},
				{Name: "DUPLICATE_ENV_VAR", Value: "nodepool_value"},
			},
		},
		{
			name: "no into",
			from: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
			into: []corev1.EnvVar{},
			expected: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := mergeEnvVars(tc.from, tc.into)
			if d := cmp.Diff(tc.expected, actual); d != "" {
				t.Errorf("expected: %v, got: %v", tc.expected, actual)
			}
		})
	}
}

func TestFindDuplicateEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		envVars  []corev1.EnvVar
		expected map[string]int
	}{
		{
			name: "No duplicates",
			envVars: []corev1.EnvVar{
				{Name: "VAR1", Value: "value1"},
				{Name: "VAR2", Value: "value2"},
			},
			expected: map[string]int{},
		},
		{
			name: "With duplicates",
			envVars: []corev1.EnvVar{
				{Name: "VAR1", Value: "value1"},
				{Name: "VAR1", Value: "value1-dup"},
				{Name: "VAR2", Value: "value2"},
				{Name: "VAR3", Value: "value3"},
				{Name: "VAR2", Value: "value2-dup"},
			},
			expected: map[string]int{
				"VAR1": 2,
				"VAR2": 2,
			},
		},
		{
			name: "Triple duplicate",
			envVars: []corev1.EnvVar{
				{Name: "VAR1", Value: "value1"},
				{Name: "VAR1", Value: "value1-dup1"},
				{Name: "VAR1", Value: "value1-dup2"},
			},
			expected: map[string]int{
				"VAR1": 3,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duplicates := findDuplicateEnvVars(tt.envVars)
			assert.Equal(t, tt.expected, duplicates)
		})
	}
}

func TestGetDuplicateEnvVarsErrorMessage(t *testing.T) {
	tests := []struct {
		name       string
		duplicates map[string]int
		expected   string
	}{
		{
			name:       "No duplicates",
			duplicates: map[string]int{},
			expected:   "",
		},
		{
			name:       "One duplicate",
			duplicates: map[string]int{"VAR1": 2},
			expected:   "Duplicate environment variables found in HumioCluster spec: 'VAR1' appears 2 times",
		},
		{
			name:       "Multiple duplicates",
			duplicates: map[string]int{"VAR1": 2, "VAR2": 3},
			expected:   "Duplicate environment variables found in HumioCluster spec: 'VAR1' appears 2 times, 'VAR2' appears 3 times",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := GetDuplicateEnvVarsErrorMessage(tt.duplicates)
			assert.Equal(t, tt.expected, message)
		})
	}
}

func TestGetExpireAfter(t *testing.T) {
	t.Run("nil when not set", func(t *testing.T) {
		hnp := &HumioNodePool{
			humioNodeSpec: humiov1alpha1.HumioNodeSpec{},
		}
		assert.Nil(t, hnp.GetExpireAfter())
	})

	t.Run("returns value when set", func(t *testing.T) {
		d := metav1.Duration{Duration: 24 * time.Hour}
		hnp := &HumioNodePool{
			humioNodeSpec: humiov1alpha1.HumioNodeSpec{
				ExpireAfter: &d,
			},
		}
		result := hnp.GetExpireAfter()
		assert.NotNil(t, result)
		assert.Equal(t, 24*time.Hour, result.Duration)
	})
}

// TestExpireAfterOrClusterDefault: cluster-level spec.expireAfter must propagate to spec.nodePools[] entries.
func TestExpireAfterOrClusterDefault(t *testing.T) {
	cluster := &metav1.Duration{Duration: 168 * time.Hour}
	nodePool := &metav1.Duration{Duration: 24 * time.Hour}

	t.Run("nodePool override wins over cluster default", func(t *testing.T) {
		got := expireAfterOrClusterDefault(nodePool, cluster)
		assert.Equal(t, 24*time.Hour, got.Duration)
	})

	t.Run("cluster default used when nodePool unset", func(t *testing.T) {
		got := expireAfterOrClusterDefault(nil, cluster)
		assert.Equal(t, 168*time.Hour, got.Duration)
	})

	t.Run("nil when neither is set", func(t *testing.T) {
		assert.Nil(t, expireAfterOrClusterDefault(nil, nil))
	})
}

func TestPodExpirationFiltering(t *testing.T) {
	now := time.Now()
	maxAge := 1 * time.Hour

	tests := []struct {
		name            string
		podAges         []time.Duration
		expectedExpired int
	}{
		{
			name:            "no pods expired",
			podAges:         []time.Duration{30 * time.Minute, 45 * time.Minute},
			expectedExpired: 0,
		},
		{
			name:            "all pods expired",
			podAges:         []time.Duration{2 * time.Hour, 3 * time.Hour},
			expectedExpired: 2,
		},
		{
			name:            "some pods expired",
			podAges:         []time.Duration{30 * time.Minute, 90 * time.Minute, 120 * time.Minute},
			expectedExpired: 2,
		},
		{
			name:            "pod exactly at boundary is not expired",
			podAges:         []time.Duration{1 * time.Hour},
			expectedExpired: 0,
		},
		{
			name:            "pod just past boundary is expired",
			podAges:         []time.Duration{2 * time.Hour},
			expectedExpired: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pods []corev1.Pod
			for i, age := range tt.podAges {
				pods = append(pods, corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              fmt.Sprintf("pod-%d", i),
						CreationTimestamp: metav1.NewTime(now.Add(-age)),
					},
				})
			}
			expired, _, _ := classifyExpiredPods(pods, maxAge, now)
			assert.Equal(t, tt.expectedExpired, len(expired))
		})
	}
}

func TestPodExpireJitter(t *testing.T) {
	t.Run("deterministic for same pod name", func(t *testing.T) {
		j1 := podExpireJitter("my-pod-name", time.Hour)
		j2 := podExpireJitter("my-pod-name", time.Hour)
		assert.Equal(t, j1, j2)
	})

	t.Run("different pods get different jitter", func(t *testing.T) {
		jitters := map[time.Duration]bool{}
		for i := 0; i < 10; i++ {
			j := podExpireJitter(fmt.Sprintf("pod-%d", i), time.Hour)
			jitters[j] = true
		}
		assert.Greater(t, len(jitters), 1, "at least some pods should have different jitter values")
	})

	t.Run("jitter within 0 to 1h cap", func(t *testing.T) {
		maxAge := 168 * time.Hour
		for i := 0; i < 100; i++ {
			j := podExpireJitter(fmt.Sprintf("pod-%d", i), maxAge)
			assert.GreaterOrEqual(t, j, time.Duration(0))
			assert.Less(t, j, time.Hour)
		}
	})

	t.Run("zero maxAge returns zero jitter", func(t *testing.T) {
		assert.Equal(t, time.Duration(0), podExpireJitter("any-pod", 0))
	})

	// Regression: uint32 modulo collapsed jitter to ~4.29s; verify spread exceeds 10m for 24h maxAge.
	t.Run("jitter window spans a meaningful range for large maxAge", func(t *testing.T) {
		maxAge := 24 * time.Hour
		var maxSeen time.Duration
		for i := 0; i < 200; i++ {
			j := podExpireJitter(fmt.Sprintf("pod-%d", i), maxAge)
			if j > maxSeen {
				maxSeen = j
			}
		}
		assert.Greater(t, maxSeen, 10*time.Minute, "jitter must span more than a handful of seconds for a 24h maxAge (uint32 overflow regression)")
	})
}

func TestExpiredPodsOldestFirst(t *testing.T) {
	now := time.Now()
	maxAge := 24 * time.Hour
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "pod-c", CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Hour))}},
		{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Hour))}},
		{ObjectMeta: metav1.ObjectMeta{Name: "pod-b", CreationTimestamp: metav1.NewTime(now.Add(-2 * time.Hour))}},
	}

	sort.Slice(pods, func(i, j int) bool {
		return podExpiresAt(pods[i], maxAge).Before(podExpiresAt(pods[j], maxAge))
	})

	assert.Equal(t, "pod-c", pods[0].Name)
	assert.Equal(t, "pod-b", pods[1].Name)
	assert.Equal(t, "pod-a", pods[2].Name)
}

// TestExpireAfterEvictionProtectionInteraction verifies that when both expireAfter
// and enableEvictionProtectionDuringMaintenance are configured:
// 1. isClusterStable returns false when a pod is terminating, triggering PDB creation
// 2. The PDB is created BEFORE the eviction subresource is called (no Karpenter window)
// 3. Jitter is deterministic so the expiry schedule is stable across restarts
func TestExpireAfterEvictionProtectionInteraction(t *testing.T) {
	t.Run("eviction protection not needed when cluster is Running", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			Spec: humiov1alpha1.HumioClusterSpec{
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableEvictionProtectionDuringMaintenance: true,
				},
			},
			Status: humiov1alpha1.HumioClusterStatus{
				State: humiov1alpha1.HumioClusterStateRunning,
			},
		}
		r := &HumioClusterReconciler{}
		assert.True(t, r.isClusterStable(context.Background(), hc, HumioNodePoolList{}), "Running cluster should be stable — no PDB needed")
	})

	t.Run("eviction protection needed when cluster is Restarting due to expireAfter cycle", func(t *testing.T) {
		hc := &humiov1alpha1.HumioCluster{
			Spec: humiov1alpha1.HumioClusterSpec{
				OperatorFeatureFlags: humiov1alpha1.HumioOperatorFeatureFlags{
					EnableEvictionProtectionDuringMaintenance: true,
				},
			},
			Status: humiov1alpha1.HumioClusterStatus{
				State: humiov1alpha1.HumioClusterStateRestarting,
			},
		}
		r := &HumioClusterReconciler{}
		assert.False(t, r.isClusterStable(context.Background(), hc, HumioNodePoolList{}), "Restarting cluster (e.g. expireAfter cycling) should not be stable — PDB should block Karpenter")
	})

	t.Run("expireAfter jitter is deterministic across restarts", func(t *testing.T) {
		// If the pod is deleted and re-created with the same name, it must get the same jitter
		// so the expiry schedule is predictable and doesn't drift.
		podName := "logsc-cicd-1-gke-core-abcdef"
		maxAge := 168 * time.Hour
		j1 := podExpireJitter(podName, maxAge)
		j2 := podExpireJitter(podName, maxAge)
		assert.Equal(t, j1, j2, "jitter must be deterministic for same pod name")
		assert.Less(t, j1, time.Hour, "jitter must not exceed 1h cap")
	})
}
