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
	// HumioUserStateUnknown is the Unknown state of the user
	HumioUserStateUnknown = "Unknown"
	// HumioUserStateExists is the Exists state of the user
	HumioUserStateExists = "Exists"
	// HumioUserStateNotFound is the NotFound state of the user
	HumioUserStateNotFound = "NotFound"
	// HumioUserStateConfigError is the state of the user when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioUserStateConfigError = "ConfigError"
)

// HumioUserSpec defines the desired state of HumioUser.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioUserSpec struct {
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
	// UserName defines the username for the LogScale user.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	UserName string `json:"userName"`
	// IsRoot toggles whether the user should be marked as a root user or not.
	// If explicitly set by the user, the value will be enforced, otherwise the root state of a user will be ignored.
	// Updating the root status of a user requires elevated privileges. When using ExternalClusterName it is important
	// to ensure the API token for the ExternalClusterName is one such privileged API token.
	// When using ManagedClusterName the API token should already be one such privileged API token that allows managing
	// the root status of users.
	// +kubebuilder:validation:Optional
	IsRoot *bool `json:"isRoot,omitempty"`
}

// HumioUserStatus defines the observed state of HumioUser.
type HumioUserStatus struct {
	// State reflects the current state of the HumioParser
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HumioUser is the Schema for the humiousers API.
type HumioUser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioUserSpec   `json:"spec"`
	Status HumioUserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioUserList contains a list of HumioUser.
type HumioUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioUser{}, &HumioUserList{})
}
