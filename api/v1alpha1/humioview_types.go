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

const (
	// HumioRepositoryStateUnknown is the Unknown state of the repository
	HumioViewStateUnknown = "Unknown"
	// HumioRepositoryStateExists is the Exists state of the repository
	HumioViewStateExists = "Exists"
	// HumioRepositoryStateNotFound is the NotFound state of the repository
	HumioViewStateNotFound = "NotFound"
)

type HumioViewConnection struct {
	RepositoryName string `json:"repositoryName,omitempty"`
	Filter         string `json:"filter,omitEmpty"`
}

// HumioViewSpec defines the desired state of HumioView
type HumioViewSpec struct {
	// Which cluster
	ManagedClusterName  string `json:"managedClusterName,omitempty"`
	ExternalClusterName string `json:"externalClusterName,omitempty"`

	// Input
	Name        string                `json:"name,omitempty"`
	Description string                `json:"description,omitempty"`
	Connections []HumioViewConnection `json:"connections,omitempty"`
}

// HumioViewStatus defines the observed state of HumioView
type HumioViewStatus struct {
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioviews,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the view"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio View"

// HumioView is the Schema for the humioviews API
type HumioView struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioViewSpec   `json:"spec,omitempty"`
	Status HumioViewStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioViewList contains a list of HumioView
type HumioViewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioView `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioView{}, &HumioViewList{})
}
