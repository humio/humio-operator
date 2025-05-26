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
	// HumioSystemPermissionRoleStateUnknown is the Unknown state of the system permission role
	HumioSystemPermissionRoleStateUnknown = "Unknown"
	// HumioSystemPermissionRoleStateExists is the Exists state of the system permission role
	HumioSystemPermissionRoleStateExists = "Exists"
	// HumioSystemPermissionRoleStateNotFound is the NotFound state of the system permission role
	HumioSystemPermissionRoleStateNotFound = "NotFound"
	// HumioSystemPermissionRoleStateConfigError is the state of the system permission role when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioSystemPermissionRoleStateConfigError = "ConfigError"
)

// HumioSystemPermissionRoleSpec defines the desired state of HumioSystemPermissionRole.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioSystemPermissionRoleSpec struct {
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
	// Permissions is the list of system permissions that this role grants.
	// For more details, see https://library.humio.com/logscale-graphql-reference-datatypes/graphql-enum-systempermission.html
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:items:MinLength=1
	// +listType=set
	Permissions []string `json:"permissions"`
	// RoleAssignmentGroupNames lists the names of LogScale groups that this role is assigned to.
	// It is optional to specify the list of role assignments. If not specified, the role will not be assigned to any groups.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:items:MinLength=1
	// +listType=set
	RoleAssignmentGroupNames []string `json:"roleAssignmentGroupNames,omitempty"`
}

// HumioSystemPermissionRoleStatus defines the observed state of HumioSystemPermissionRole.
type HumioSystemPermissionRoleStatus struct {
	// State reflects the current state of the HumioSystemPermissionRole
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HumioSystemPermissionRole is the Schema for the humiosystempermissionroles API.
type HumioSystemPermissionRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioSystemPermissionRoleSpec   `json:"spec"`
	Status HumioSystemPermissionRoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioSystemPermissionRoleList contains a list of HumioSystemPermissionRole.
type HumioSystemPermissionRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioSystemPermissionRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioSystemPermissionRole{}, &HumioSystemPermissionRoleList{})
}
