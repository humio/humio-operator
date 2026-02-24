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

const (
	// UserConditionTypeReady indicates whether the user is ready
	UserConditionTypeReady = "Ready"
	// UserConditionTypeSynced indicates whether the user is synced with LogScale
	UserConditionTypeSynced = "Synced"
)

const (
	// UserReasonReady indicates the user is ready
	UserReasonReady = "Ready"
	// UserReasonCreated indicates the user was created
	UserReasonCreated = "Created"
	// UserReasonUpdated indicates the user was updated
	UserReasonUpdated = "Updated"
	// UserReasonNotFound indicates the user was not found
	UserReasonNotFound = "NotFound"
	// UserReasonConfigError indicates a configuration error
	UserReasonConfigError = "ConfigurationError"
	// UserReasonConfigSynced indicates the configuration is synced
	UserReasonConfigSynced = "ConfigurationSynced"
	// UserReasonConfigDrifted indicates the configuration has drifted
	UserReasonConfigDrifted = "ConfigurationDrifted"
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
	// AllowDataDeletion enables deletion of the LogScale resource when this CR is deleted.
	// If false or unset, the operator will not delete the LogScale resource on CR deletion.
	// +kubebuilder:validation:Optional
	AllowDataDeletion bool `json:"allowDataDeletion,omitempty"`
}

// HumioUserStatus defines the observed state of HumioUser.
type HumioUserStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Reflects the current state of the HumioUser
	// +kubebuilder:validation:Optional
	State string `json:"state,omitempty"`

	// Conditions represent the latest available observations of the resource's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// LastSyncedName is the last name successfully synced with LogScale
	// Used to detect renames
	// +optional
	LastSyncedName string `json:"lastSyncedName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the User"

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
