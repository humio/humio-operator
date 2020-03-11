package humio

import (
	"reflect"
	"testing"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
)

func TestClusterController_AreAllRegisteredNodesAvailable(t *testing.T) {
	type fields struct {
		client Client
	}
	tests := []struct {
		name    string
		fields  fields
		want    bool
		wantErr bool
	}{
		{
			"test available nodes",
			fields{NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{humioapi.ClusterNode{
						IsAvailable: true,
					}}}, nil, nil, nil),
			},
			true,
			false,
		},
		{
			"test no available nodes",
			fields{NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{humioapi.ClusterNode{
						IsAvailable: false,
					}}}, nil, nil, nil),
			},
			false,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterController{
				client: tt.fields.client,
			}
			got, err := c.AreAllRegisteredNodesAvailable()
			if (err != nil) != tt.wantErr {
				t.Errorf("ClusterController.AreAllRegisteredNodesAvailable() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ClusterController.AreAllRegisteredNodesAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterController_NoDataMissing(t *testing.T) {
	type fields struct {
		client Client
	}
	tests := []struct {
		name    string
		fields  fields
		want    bool
		wantErr bool
	}{
		{
			"test no missing segments",
			fields{NewMocklient(
				humioapi.Cluster{
					MissingSegmentSize: 0,
				}, nil, nil, nil),
			},
			true,
			false,
		},
		{
			"test missing segments",
			fields{NewMocklient(
				humioapi.Cluster{
					MissingSegmentSize: 1,
				}, nil, nil, nil),
			},
			false,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterController{
				client: tt.fields.client,
			}
			got, err := c.NoDataMissing()
			if (err != nil) != tt.wantErr {
				t.Errorf("ClusterController.NoDataMissing() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ClusterController.NoDataMissing() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterController_IsNodeRegistered(t *testing.T) {
	type fields struct {
		client Client
	}
	type args struct {
		nodeID int
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    bool
		wantErr bool
	}{
		{
			"test node is registered",
			fields{NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{humioapi.ClusterNode{
						Id: 1,
					}}}, nil, nil, nil),
			},
			args{
				nodeID: 1,
			},
			true,
			false,
		},
		{
			"test node is not registered",
			fields{NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{humioapi.ClusterNode{
						Id: 2,
					}}}, nil, nil, nil),
			},
			args{
				nodeID: 1,
			},
			false,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterController{
				client: tt.fields.client,
			}
			got, err := c.IsNodeRegistered(tt.args.nodeID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ClusterController.IsNodeRegistered() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ClusterController.IsNodeRegistered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterController_CountNodesRegistered(t *testing.T) {
	type fields struct {
		client Client
	}
	tests := []struct {
		name    string
		fields  fields
		want    int
		wantErr bool
	}{
		{
			"test count registered nodes",
			fields{NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{humioapi.ClusterNode{}}}, nil, nil, nil),
			},
			1,
			false,
		},
		{
			"test count no registered nodes",
			fields{NewMocklient(
				humioapi.Cluster{}, nil, nil, nil),
			},
			0,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterController{
				client: tt.fields.client,
			}
			got, err := c.CountNodesRegistered()
			if (err != nil) != tt.wantErr {
				t.Errorf("ClusterController.CountNodesRegistered() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ClusterController.CountNodesRegistered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterController_CanBeSafelyUnregistered(t *testing.T) {
	type fields struct {
		client Client
	}
	type args struct {
		podID int
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    bool
		wantErr bool
	}{
		{
			"test node is can be safely unregistered",
			fields{NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{humioapi.ClusterNode{
						Id:                      1,
						CanBeSafelyUnregistered: true,
					}}}, nil, nil, nil),
			},
			args{
				podID: 1,
			},
			true,
			false,
		},
		{
			"test node is cannot be safely unregistered",
			fields{NewMocklient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{humioapi.ClusterNode{
						Id:                      1,
						CanBeSafelyUnregistered: false,
					}}}, nil, nil, nil),
			},
			args{
				podID: 1,
			},
			false,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterController{
				client: tt.fields.client,
			}
			got, err := c.CanBeSafelyUnregistered(tt.args.podID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ClusterController.CanBeSafelyUnregistered() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ClusterController.CanBeSafelyUnregistered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterController_IsStoragePartitionsBalanced(t *testing.T) {
	type fields struct {
		client Client
	}
	type args struct {
		hc *corev1alpha1.HumioCluster
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    bool
		wantErr bool
	}{
		{
			"test storage partitions are balanced",
			fields{NewMocklient(
				humioapi.Cluster{
					StoragePartitions: []humioapi.StoragePartition{
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{0},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{1},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{2},
						},
					},
					Nodes: []humioapi.ClusterNode{
						humioapi.ClusterNode{
							Id: 0,
						},
						humioapi.ClusterNode{
							Id: 1,
						},
						humioapi.ClusterNode{
							Id: 2,
						},
					}}, nil, nil, nil),
			},
			args{
				&corev1alpha1.HumioCluster{
					Spec: corev1alpha1.HumioClusterSpec{
						TargetReplicationFactor: 1,
					},
				},
			},
			true,
			false,
		},
		{
			"test storage partitions do no equal the target replication factor",
			fields{NewMocklient(
				humioapi.Cluster{
					StoragePartitions: []humioapi.StoragePartition{
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{0, 1},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{1, 2},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{2, 0},
						},
					},
					Nodes: []humioapi.ClusterNode{
						humioapi.ClusterNode{
							Id: 0,
						},
						humioapi.ClusterNode{
							Id: 1,
						},
						humioapi.ClusterNode{
							Id: 2,
						},
					}}, nil, nil, nil),
			},
			args{
				&corev1alpha1.HumioCluster{
					Spec: corev1alpha1.HumioClusterSpec{
						TargetReplicationFactor: 1,
					},
				},
			},
			false,
			false,
		},
		{
			"test storage partitions are unbalanced by more than a factor of 1",
			fields{NewMocklient(
				humioapi.Cluster{
					StoragePartitions: []humioapi.StoragePartition{
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{0, 0, 0},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{1, 1, 1},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{2, 1, 1},
						},
					},
					Nodes: []humioapi.ClusterNode{
						humioapi.ClusterNode{
							Id: 0,
						},
						humioapi.ClusterNode{
							Id: 1,
						},
						humioapi.ClusterNode{
							Id: 2,
						},
					}}, nil, nil, nil),
			},
			args{
				&corev1alpha1.HumioCluster{
					Spec: corev1alpha1.HumioClusterSpec{
						TargetReplicationFactor: 3,
					},
				},
			},
			false,
			false,
		},
		{
			"test storage partitions are not balanced",
			fields{NewMocklient(
				humioapi.Cluster{
					StoragePartitions: []humioapi.StoragePartition{
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{0, 1},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{1, 0},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{0, 1},
						},
					},
					Nodes: []humioapi.ClusterNode{
						humioapi.ClusterNode{
							Id: 0,
						},
						humioapi.ClusterNode{
							Id: 1,
						},
						humioapi.ClusterNode{
							Id: 2,
						},
					}}, nil, nil, nil),
			},
			args{
				&corev1alpha1.HumioCluster{
					Spec: corev1alpha1.HumioClusterSpec{
						TargetReplicationFactor: 1,
					},
				},
			},
			false,
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterController{
				client: tt.fields.client,
			}
			got, err := c.AreStoragePartitionsBalanced(tt.args.hc)
			if (err != nil) != tt.wantErr {
				t.Errorf("ClusterController.AreStoragePartitionsBalanced() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ClusterController.AreStoragePartitionsBalanced() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClusterController_RebalanceStoragePartitions(t *testing.T) {
	type fields struct {
		client             Client
		expectedPartitions *[]humioapi.StoragePartition
	}
	type args struct {
		hc *corev1alpha1.HumioCluster
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			"test rebalancing storage partitions",
			fields{NewMocklient(
				humioapi.Cluster{
					StoragePartitions: []humioapi.StoragePartition{
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{0},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{0},
						},
						humioapi.StoragePartition{
							Id:      1,
							NodeIds: []int{0},
						},
					},
					Nodes: []humioapi.ClusterNode{
						humioapi.ClusterNode{
							Id: 0,
						},
						humioapi.ClusterNode{
							Id: 1,
						},
						humioapi.ClusterNode{
							Id: 2,
						},
					}}, nil, nil, nil),
				&[]humioapi.StoragePartition{
					humioapi.StoragePartition{
						Id:      0,
						NodeIds: []int{0, 1},
					},
					humioapi.StoragePartition{
						Id:      1,
						NodeIds: []int{1, 2},
					},
					humioapi.StoragePartition{
						Id:      2,
						NodeIds: []int{2, 0},
					},
				},
			},
			args{
				&corev1alpha1.HumioCluster{
					Spec: corev1alpha1.HumioClusterSpec{
						TargetReplicationFactor: 2,
						StoragePartitionsCount:  3,
					},
				},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClusterController{
				client: tt.fields.client,
			}
			if err := c.RebalanceStoragePartitions(tt.args.hc); (err != nil) != tt.wantErr {
				t.Errorf("ClusterController.RebalanceStoragePartitions() error = %v, wantErr %v", err, tt.wantErr)
			}
			if sps, _ := c.client.GetStoragePartitions(); !reflect.DeepEqual(*sps, *tt.fields.expectedPartitions) {
				t.Errorf("ClusterController.RebalanceStoragePartitions() expected = %v, want %v", *tt.fields.expectedPartitions, *sps)
			}
		})
	}
}
