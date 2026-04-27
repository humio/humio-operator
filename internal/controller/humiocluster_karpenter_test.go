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
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsClusterStable(t *testing.T) {
	r := &HumioClusterReconciler{}

	tests := []struct {
		name           string
		clusterState   string
		nodePoolStates []struct {
			name      string
			state     string
			nodeCount int
		}
		want bool
	}{
		{
			name:         "all pools Running",
			clusterState: humiov1alpha1.HumioClusterStateRunning,
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
			},
			want: true,
		},
		{
			name:         "cluster Upgrading",
			clusterState: humiov1alpha1.HumioClusterStateUpgrading,
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateUpgrading, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "cluster Restarting",
			clusterState: humiov1alpha1.HumioClusterStateRestarting,
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRestarting, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "cluster Running but pool Upgrading",
			clusterState: humiov1alpha1.HumioClusterStateRunning,
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateUpgrading, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "cluster Pending",
			clusterState: humiov1alpha1.HumioClusterStatePending,
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStatePending, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "cluster ConfigError",
			clusterState: humiov1alpha1.HumioClusterStateConfigError,
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateConfigError, nodeCount: 3},
			},
			want: false,
		},
		{
			name:         "single pool Running",
			clusterState: humiov1alpha1.HumioClusterStateRunning,
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
			},
			want: true,
		},
		{
			name:         "pool with zero nodes is ignored",
			clusterState: humiov1alpha1.HumioClusterStateRunning,
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
				{name: "pool-a", state: humiov1alpha1.HumioClusterStateRunning, nodeCount: 3},
				{name: "pool-b", state: humiov1alpha1.HumioClusterStateUpgrading, nodeCount: 0},
			},
			want: true,
		},
		{
			name:         "empty state is not stable",
			clusterState: "",
			nodePoolStates: []struct {
				name      string
				state     string
				nodeCount int
			}{
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
				pool := &HumioNodePool{
					clusterName:  "test-cluster",
					nodePoolName: np.name,
					namespace:    "default",
					state:        np.state,
					humioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: np.nodeCount,
					},
				}
				nodePoolList.Add(pool)
			}

			got := r.isClusterStable(hc, nodePoolList)
			if got != tt.want {
				t.Errorf("isClusterStable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestKarpenterPDBName(t *testing.T) {
	pool := &HumioNodePool{
		clusterName:  "mycluster",
		nodePoolName: "ingest",
		namespace:    "default",
	}

	got := karpenterPDBName(pool)
	want := "mycluster-ingest-karpenter-pdb"
	if got != want {
		t.Errorf("karpenterPDBName() = %q, want %q", got, want)
	}

	userPDBName := pool.GetPodDisruptionBudgetName()
	if got == userPDBName {
		t.Errorf("karpenter PDB name %q must not collide with user PDB name %q", got, userPDBName)
	}
}

func TestKarpenterPDBNameDefaultPool(t *testing.T) {
	pool := &HumioNodePool{
		clusterName: "mycluster",
		namespace:   "default",
	}

	got := karpenterPDBName(pool)
	want := "mycluster-karpenter-pdb"
	if got != want {
		t.Errorf("karpenterPDBName() for default pool = %q, want %q", got, want)
	}

	userPDBName := pool.GetPodDisruptionBudgetName()
	if got == userPDBName {
		t.Errorf("karpenter PDB name %q must not collide with user PDB name %q", got, userPDBName)
	}
}
