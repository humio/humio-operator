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
	"testing"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsClusterStable(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	r := &HumioClusterReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Log:    logr.Discard(),
	}

	type poolFixture struct {
		name      string
		state     string
		nodeCount int32
	}

	tests := []struct {
		name           string
		clusterState   string
		nodePoolStates []poolFixture
		want           bool
	}{
		{
			name:         "all pools Running",
			clusterState: humiov1alpha1.HumioClusterStateRunning,
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
			},
			want: true,
		},
		{
			name:         "cluster Upgrading",
			clusterState: humiov1alpha1.HumioClusterStateUpgrading,
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateUpgrading, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "cluster Restarting",
			clusterState: humiov1alpha1.HumioClusterStateRestarting,
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRestarting, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "cluster Running but pool Upgrading",
			clusterState: humiov1alpha1.HumioClusterStateRunning,
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateUpgrading, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "cluster Pending",
			clusterState: humiov1alpha1.HumioClusterStatePending,
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStatePending, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "cluster ConfigError",
			clusterState: humiov1alpha1.HumioClusterStateConfigError,
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateConfigError, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "single pool Running",
			clusterState: humiov1alpha1.HumioClusterStateRunning,
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
			},
			want: true,
		},
		{
			name:         "pool with zero nodes is ignored",
			clusterState: humiov1alpha1.HumioClusterStateRunning,
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateUpgrading, nodeCount: 0},
			},
			want: true,
		},
		{
			name:         "empty state is not stable",
			clusterState: "",
			nodePoolStates: []poolFixture{
				{name: "pool-a", state: "", nodeCount: 3},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Status: humiov1alpha1.HumioClusterStatus{
					State: tt.clusterState,
				},
			}

			var nodePoolList HumioNodePoolList
			for _, np := range tt.nodePoolStates {
				nc := np.nodeCount
				pool := &HumioNodePool{
					clusterName:  "test-cluster",
					nodePoolName: np.name,
					namespace:    "default",
					state:        np.state,
					humioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: &nc,
					},
				}
				nodePoolList.Add(pool)
			}

			got := r.isClusterStable(context.Background(), hc, nodePoolList)
			if got != tt.want {
				t.Errorf("isClusterStable() = %v, want %v", got, tt.want)
			}
		})
	}

	// DeletionTimestamp case: cluster Running, all pools Running, but a pod is terminating.
	t.Run("cluster Running but pod has DeletionTimestamp set", func(t *testing.T) {
		now := metav1.Now()
		nc := int32(3)
		pool := &HumioNodePool{
			clusterName:   "test-cluster",
			nodePoolName:  "test-cluster",
			namespace:     "default",
			state:         humiov1alpha1.HumioClusterStateRunning,
			humioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: &nc},
		}
		terminatingPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "test-cluster-core-abc",
				Namespace:         "default",
				DeletionTimestamp: &now,
				Finalizers:        []string{"test"},
				Labels:            pool.GetNodePoolLabels(),
			},
		}
		scheme2 := runtime.NewScheme()
		require.NoError(t, corev1.AddToScheme(scheme2))
		require.NoError(t, humiov1alpha1.AddToScheme(scheme2))
		r2 := &HumioClusterReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme2).WithObjects(terminatingPod).Build(),
			Log:    logr.Discard(),
		}
		hc := &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
			Status:     humiov1alpha1.HumioClusterStatus{State: humiov1alpha1.HumioClusterStateRunning},
		}
		var nodePoolList HumioNodePoolList
		nodePoolList.Add(pool)
		got := r2.isClusterStable(context.Background(), hc, nodePoolList)
		require.False(t, got, "cluster should be unstable when a pod has DeletionTimestamp set")
	})
}

func TestEvictionProtectionPDBName(t *testing.T) {
	pool := &HumioNodePool{
		clusterName:  "mycluster",
		nodePoolName: "ingest",
		namespace:    "default",
	}

	got := evictionProtectionPDBName(pool)
	want := "mycluster-ingest-eviction-protection"
	if got != want {
		t.Errorf("evictionProtectionPDBName() = %q, want %q", got, want)
	}

	userPDBName := pool.GetPodDisruptionBudgetName()
	if got == userPDBName {
		t.Errorf("eviction-protection PDB name %q must not collide with user PDB name %q", got, userPDBName)
	}
}

func TestEvictionProtectionPDBNameDefaultPool(t *testing.T) {
	pool := &HumioNodePool{
		clusterName: "mycluster",
		namespace:   "default",
	}

	got := evictionProtectionPDBName(pool)
	want := "mycluster-eviction-protection"
	if got != want {
		t.Errorf("evictionProtectionPDBName() for default pool = %q, want %q", got, want)
	}

	userPDBName := pool.GetPodDisruptionBudgetName()
	if got == userPDBName {
		t.Errorf("eviction-protection PDB name %q must not collide with user PDB name %q", got, userPDBName)
	}
}

func TestEvictionProtectionPDBExpired(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	ttl := 2 * time.Hour

	pdbWithCreatedAt := func(v string) *policyv1.PodDisruptionBudget {
		return &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					evictionProtectionCreatedAtAnnKey: v,
				},
			},
		}
	}

	tests := []struct {
		name string
		pdb  *policyv1.PodDisruptionBudget
		want bool
	}{
		{
			name: "nil pdb is not expired",
			pdb:  nil,
			want: false,
		},
		{
			name: "no annotations map is treated as expired",
			pdb:  &policyv1.PodDisruptionBudget{},
			want: true,
		},
		{
			name: "missing annotation is treated as expired",
			pdb: &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{"other": "value"},
				},
			},
			want: true,
		},
		{
			name: "empty annotation is treated as expired",
			pdb:  pdbWithCreatedAt(""),
			want: true,
		},
		{
			name: "malformed annotation is treated as expired",
			pdb:  pdbWithCreatedAt("not-a-timestamp"),
			want: true,
		},
		{
			name: "created just now is not expired",
			pdb:  pdbWithCreatedAt(now.Format(time.RFC3339)),
			want: false,
		},
		{
			name: "created within TTL is not expired",
			pdb:  pdbWithCreatedAt(now.Add(-30 * time.Minute).Format(time.RFC3339)),
			want: false,
		},
		{
			name: "created exactly TTL ago is expired",
			pdb:  pdbWithCreatedAt(now.Add(-ttl).Format(time.RFC3339)),
			want: true,
		},
		{
			name: "created well beyond TTL is expired",
			pdb:  pdbWithCreatedAt(now.Add(-24 * time.Hour).Format(time.RFC3339)),
			want: true,
		},
		{
			name: "future createdAt (clock skew) is not expired",
			pdb:  pdbWithCreatedAt(now.Add(1 * time.Minute).Format(time.RFC3339)),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := evictionProtectionPDBExpired(tt.pdb, ttl, now)
			if got != tt.want {
				t.Errorf("evictionProtectionPDBExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}
