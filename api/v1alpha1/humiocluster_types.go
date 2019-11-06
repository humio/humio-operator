/*
Copyright 2019 Humio.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// HumioNodeType will define which responsibilities nodes will take on.
type HumioNodeType string

const (
	// Ingest nodes are used as endpoints for log shippers and is responsible for storing the logs in Kafka.
	Ingest HumioNodeType = "ingest"
	// Digest nodes are responsible for reading and processing incoming messages from Kafka. It is responsible for building segment files.
	Digest HumioNodeType = "digest"
	// Storage nodes are responsible for storing segment files.
	Storage HumioNodeType = "storage"
)

// HumioNodePool defines a pool of nodes with equal configuration
type HumioNodePool struct {
	// The name of the node pool
	Name string `json:"name,omitempty"`
	// The ID to the first node in the pool
	FirstNodeID int `json:"firstNodeID,omitempty"`
	// The node types for the node pool
	Types []HumioNodeType `json:"types,omitempty"`
	// Desired number of nodes
	NodeCount int `json:"nodeCount,omitempty"`
	// Desired disk size of each node
	NodeDiskSizeGB int `json:"nodeDiskSizeGB,omitempty"`
}

// HumioClusterSpec defines the desired state of HumioCluster
type HumioClusterSpec struct {
	// Desired container image
	Image string `json:"image,omitempty"`
	// Desired version of Humio nodes
	Version string `json:"version,omitempty"`
	// Desired number of nodes
	NodePools []HumioNodePool `json:"nodePools,omitempty"`
	// Desired number of replicas of both storage and ingest partitions
	TargetReplicationFactor int `json:"targetReplicationFactor,omitempty"`
	// Password for the default user 'developer'
	SingleUserPassword string `json:"singleUserPassword,omitempty"`
}

// HumioClusterStatus defines the observed state of HumioCluster
type HumioClusterStatus struct {
	StateLastUpdatedUnix int64 `json:"stateLastUpdated,omitempty"`
	// Current state set by operator.
	AllDataAvailable string `json:"allDataAvailable,omitempty"`
	// JWTToken for interacting with the cluster.
	JWTToken string `json:"jwtToken,omitempty"`
	// BaseURL pointing to an endpoint for the cluster
	BaseURL string `json:"baseURL,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HumioCluster is the Schema for the humioclusters API
type HumioCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioClusterSpec   `json:"spec,omitempty"`
	Status HumioClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioClusterList contains a list of HumioCluster
type HumioClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioCluster{}, &HumioClusterList{})
}
