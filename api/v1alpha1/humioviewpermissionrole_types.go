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
	// HumioViewPermissionRoleStateUnknown is the Unknown state of the view permission role
	HumioViewPermissionRoleStateUnknown = "Unknown"
	// HumioViewPermissionRoleStateExists is the Exists state of the view permission role
	HumioViewPermissionRoleStateExists = "Exists"
	// HumioViewPermissionRoleStateNotFound is the NotFound state of the view permission role
	HumioViewPermissionRoleStateNotFound = "NotFound"
	// HumioViewPermissionRoleStateConfigError is the state of the view permission role when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioViewPermissionRoleStateConfigError = "ConfigError"
)

// HumioViewPermissionRoleSpec defines the desired state of HumioViewPermissionRole.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioViewPermissionRoleSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the role inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Permissions is the list of view permissions that this role grants.
	// For more details, see https://library.humio.com/logscale-graphql-reference-datatypes/graphql-enum-permission.html
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:items:MinLength=1
	// +listType=set
	Permissions []string `json:"permissions"`
	// TODO: Add support for assigning the role to groups. These assignments do not just take a group name, but also a view for where this is assigned, so will need to adjust the field below to reflect that.
	// Groups *string `json:"groups,omitempty"`
}

// HumioViewPermissionRoleStatus defines the observed state of HumioViewPermissionRole.
type HumioViewPermissionRoleStatus struct {
	// State reflects the current state of the HumioViewPermissionRole
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HumioViewPermissionRole is the Schema for the humioviewpermissionroles API.
type HumioViewPermissionRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioViewPermissionRoleSpec   `json:"spec,omitempty"`
	Status HumioViewPermissionRoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioViewPermissionRoleList contains a list of HumioViewPermissionRole.
type HumioViewPermissionRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioViewPermissionRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioViewPermissionRole{}, &HumioViewPermissionRoleList{})
}
