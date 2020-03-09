package humio

import (
	"testing"

	humioapi "github.com/humio/cli/api"
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
