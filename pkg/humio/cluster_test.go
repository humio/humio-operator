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
			fields{NewMockClient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{{
						IsAvailable: true,
					}}}, nil, nil, nil, ""),
			},
			true,
			false,
		},
		{
			"test no available nodes",
			fields{NewMockClient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{{
						IsAvailable: false,
					}}}, nil, nil, nil, ""),
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
			fields{NewMockClient(
				humioapi.Cluster{
					MissingSegmentSize: 0,
				}, nil, nil, nil, ""),
			},
			true,
			false,
		},
		{
			"test missing segments",
			fields{NewMockClient(
				humioapi.Cluster{
					MissingSegmentSize: 1,
				}, nil, nil, nil, ""),
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
			fields{NewMockClient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{{
						Id: 1,
					}}}, nil, nil, nil, ""),
			},
			args{
				nodeID: 1,
			},
			true,
			false,
		},
		{
			"test node is not registered",
			fields{NewMockClient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{{
						Id: 2,
					}}}, nil, nil, nil, ""),
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
			fields{NewMockClient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{{}}}, nil, nil, nil, ""),
			},
			1,
			false,
		},
		{
			"test count no registered nodes",
			fields{NewMockClient(
				humioapi.Cluster{}, nil, nil, nil, ""),
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
			fields{NewMockClient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{{
						Id:                      1,
						CanBeSafelyUnregistered: true,
					}}}, nil, nil, nil, ""),
			},
			args{
				podID: 1,
			},
			true,
			false,
		},
		{
			"test node is cannot be safely unregistered",
			fields{NewMockClient(
				humioapi.Cluster{
					Nodes: []humioapi.ClusterNode{{
						Id:                      1,
						CanBeSafelyUnregistered: false,
					}}}, nil, nil, nil, ""),
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
			}
			if got != tt.want {
				t.Errorf("ClusterController.CanBeSafelyUnregistered() = %v, want %v", got, tt.want)
			}
		})
	}
}
