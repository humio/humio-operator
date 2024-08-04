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

// HumioUserSpec defines the desired state of HumioUser
type HumioUserSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Username of the user in humio
	Username string `json:"username,omitempty"`
	// User ID of the user in humio
	ID string `json:"id,omitempty"`
	// FullName is the full name of the user
	FullName string `json:"fullName,omitempty"`
	// Email is the email of the user
	Email string `json:"email,omitempty"`
	// Company is the compnay of the user
	Company string `json:"company,omitempty"`
	// CountryCode is the compnay of the user
	CountryCode string `json:"countryCode,omitempty"`
	// Picture is the url to the user's profile picture
	Picture string `json:"picture,omitempty"`
	// IsRoot is the root setting for the user
	IsRoot bool `json:"isRoot,omitempty"`
}

// HumioUserStatus defines the observed state of HumioUser
type HumioUserStatus struct {
	// State reflects the current state of the HumioUser
	State string `json:"state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:path=humiousers,scope=Namespaced
//+kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the user"
//+operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio User"

// HumioUser is the Schema for the humiousers API
type HumioUser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioUserSpec   `json:"spec,omitempty"`
	Status HumioUserStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioUserList contains a list of HumioUser
type HumioUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioUser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioUser{}, &HumioUserList{})
}
