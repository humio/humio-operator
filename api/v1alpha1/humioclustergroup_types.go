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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:path=humioclustergroups,scope=Namespaced
//+kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the cluster groups"
//+operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Cluster Group"

// HumioClusterGroup is the Schema for the humioclustergroupss API
type HumioClusterGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioClusterGroupSpec                                `json:"spec,omitempty"`
	Status []HumioClusterGroupStatusHumioClusterStatusAggregate `json:"status,omitempty"`
}

// HumioClusterGroupSepc defines the spec for the HumioClusterGroup
type HumioClusterGroupSpec struct {
	MaxConcurrentUpgrades int `json:"maxConcurrentUpgrades,omitempty"`
	MaxConcurrentRestarts int `json:"maxConcurrentRestarts,omitempty"`
}

// HumioClusterGroupStatusHumioClusterStatusAggregate provides an aggregate status of HumioCluster's who are members of
// this HumioClusterGroup when they are in a state that is managed by a HumioClusterGroup
type HumioClusterGroupStatusHumioClusterStatusAggregate struct {
	Type            string                                      `json:"type"`
	InProgressCount int                                         `json:"inProgress"`
	ClusterList     []HumioClusterGroupStatusHumioClusterStatus `json:"clusterList"`
}

// HumioClusterGroupStatusHumioClusterStatus tracks each HumioCluster's status who are members of this HumioClusterGroup
// when they are in a state that is managed by a HumioClusterGroup
type HumioClusterGroupStatusHumioClusterStatus struct {
	ClusterName    string       `json:"clusterName"`
	ClusterState   string       `json:"state"`
	LastUpdateTime *metav1.Time `json:"lastUpdateTime"`
}

//+kubebuilder:object:root=true

// HumioClusterGroupList contains a list of HumioClusterGroup
type HumioClusterGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioClusterGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioClusterGroup{}, &HumioClusterGroupList{})
}
