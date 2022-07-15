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
	humioapi "github.com/humio/cli/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioViewStateUnknown is the Unknown state of the view
	HumioViewStateUnknown = "Unknown"
	// HumioViewStateExists is the Exists state of the view
	HumioViewStateExists = "Exists"
	// HumioViewStateNotFound is the NotFound state of the view
	HumioViewStateNotFound = "NotFound"
	// HumioViewStateConfigError is the state of the view when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioViewStateConfigError = "ConfigError"
)

type HumioViewConnection struct {
	// RepositoryName contains the name of the target repository
	RepositoryName string `json:"repositoryName,omitempty"`
	// Filter contains the prefix filter that will be applied for the given RepositoryName
	Filter string `json:"filter,omitempty"`
}

// HumioViewSpec defines the desired state of HumioView
type HumioViewSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the view inside Humio
	Name string `json:"name,omitempty"`
	// Connections contains the connections to the Humio repositories which is accessible in this view
	// Description contains the description that will be set on this view
	Description string `json:"description,omitempty"`
	Connections []HumioViewConnection `json:"connections,omitempty"`
}

// HumioViewStatus defines the observed state of HumioView
type HumioViewStatus struct {
	// State reflects the current state of the HumioView
	State string `json:"state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:path=humioviews,scope=Namespaced
//+kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the view"
//+operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio View"

// HumioView is the Schema for the humioviews API
type HumioView struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioViewSpec   `json:"spec,omitempty"`
	Status HumioViewStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioViewList contains a list of HumioView
type HumioViewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioView `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioView{}, &HumioViewList{})
}

func (hv *HumioView) GetViewConnections() []humioapi.ViewConnection {
	viewConnections := make([]humioapi.ViewConnection, 0)

	for _, connection := range hv.Spec.Connections {
		viewConnections = append(viewConnections, humioapi.ViewConnection{
			RepoName: connection.RepositoryName,
			Filter:   connection.Filter,
		})
	}
	return viewConnections
}
