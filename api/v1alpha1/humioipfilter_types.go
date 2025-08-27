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
	// HumioIPFilterStateUnknown is the Unknown state of the IPFilter
	HumioIPFilterStateUnknown = "Unknown"
	// HumioIPFilterStateExists is the Exists state of the IPFilter
	HumioIPFilterStateExists = "Exists"
	// HumioIPFilterStateNotFound is the NotFound state of the IPFilter
	HumioIPFilterStateNotFound = "NotFound"
	// HumioIPFilterStateConfigError is the state of the IPFilter when user-provided specification results in configuration error
	HumioIPFilterStateConfigError = "ConfigError"
)

// HumioIPFilterSpec defines the desired state of HumioIPFilter
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioIPFilterSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the IP filter inside Humio
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// IPFilter defines the IP filter to use
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Items:Pattern=`^(allow|deny) ((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)(/(3[0-2]|[12]?[0-9]))?|([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|::1|::|(([0-9a-fA-F]{1,4}:){1,7}:|:((:[0-9a-fA-F]{1,4}){1,7}|:))(/(12[0-8]|1[01][0-9]|[1-9]?[0-9]))?$`
	IPFilter []string `json:"ipFilter"`
}

// HumioIPFilterStatus defines the observed state of HumioIPFilter.
type HumioIPFilterStatus struct {
	// State reflects the current state of the HumioIPFilter
	State string `json:"state,omitempty"`
	// ID stores the Humio generated ID for the filter
	ID string `json:"id,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioipfilters,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the IPFilter"
// +kubebuilder:printcolumn:name="HumioID",type="string",JSONPath=".status.id",description="Humio generated ID"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio IPFilter"

// HumioIPFilter is the Schema for the humioipfilters API
type HumioIPFilter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// +kubebuilder:validation:Required
	Spec   HumioIPFilterSpec   `json:"spec"`
	Status HumioIPFilterStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// HumioIPFilterList contains a list of HumioIPFilter
type HumioIPFilterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioIPFilter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioIPFilter{}, &HumioIPFilterList{})
}
