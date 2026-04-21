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
	// HumioOrganizationPermissionRoleStateUnknown is the Unknown state of the organization permission role
	HumioOrganizationPermissionRoleStateUnknown = "Unknown"
	// HumioOrganizationPermissionRoleStateExists is the Exists state of the organization permission role
	HumioOrganizationPermissionRoleStateExists = "Exists"
	// HumioOrganizationPermissionRoleStateNotFound is the NotFound state of the organization permission role
	HumioOrganizationPermissionRoleStateNotFound = "NotFound"
	// HumioOrganizationPermissionRoleStateConfigError is the state of the organization permission role when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioOrganizationPermissionRoleStateConfigError = "ConfigError"
)

const (
	// OrganizationPermissionRoleConditionTypeReady indicates whether the organization permission role is ready
	OrganizationPermissionRoleConditionTypeReady = "Ready"
	// OrganizationPermissionRoleConditionTypeSynced indicates whether the organization permission role is synced with LogScale
	OrganizationPermissionRoleConditionTypeSynced = "Synced"
)

const (
	// OrganizationPermissionRoleReasonReady indicates the organization permission role is ready
	OrganizationPermissionRoleReasonReady = "Ready"
	// OrganizationPermissionRoleReasonCreated indicates the organization permission role was created
	OrganizationPermissionRoleReasonCreated = "Created"
	// OrganizationPermissionRoleReasonUpdated indicates the organization permission role was updated
	OrganizationPermissionRoleReasonUpdated = "Updated"
	// OrganizationPermissionRoleReasonNotFound indicates the organization permission role was not found
	OrganizationPermissionRoleReasonNotFound = "NotFound"
	// OrganizationPermissionRoleReasonConfigError indicates a configuration error
	OrganizationPermissionRoleReasonConfigError = "ConfigurationError"
	// OrganizationPermissionRoleReasonConfigSynced indicates the configuration is synced
	OrganizationPermissionRoleReasonConfigSynced = "ConfigurationSynced"
	// OrganizationPermissionRoleReasonConfigDrifted indicates the configuration has drifted
	OrganizationPermissionRoleReasonConfigDrifted = "ConfigurationDrifted"
)

// HumioOrganizationPermissionRoleSpec defines the desired state of HumioOrganizationPermissionRole.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioOrganizationPermissionRoleSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the role inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Permissions is the list of organization permissions that this role grants.
	// For more details, see https://library.humio.com/logscale-graphql-reference-datatypes/graphql-enum-organizationpermission.html
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
	// AllowDataDeletion enables deletion of the LogScale resource when this CR is deleted.
	// If false or unset, the operator will not delete the LogScale resource on CR deletion.
	// +kubebuilder:validation:Optional
	AllowDataDeletion bool `json:"allowDataDeletion,omitempty"`
}

// HumioOrganizationPermissionRoleStatus defines the observed state of HumioOrganizationPermissionRole.
type HumioOrganizationPermissionRoleStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Reflects the current state of the HumioOrganizationPermissionRole
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
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"

// HumioOrganizationPermissionRole is the Schema for the humioorganizationpermissionroles API.
type HumioOrganizationPermissionRole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioOrganizationPermissionRoleSpec   `json:"spec,omitempty"`
	Status HumioOrganizationPermissionRoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioOrganizationPermissionRoleList contains a list of HumioOrganizationPermissionRole.
type HumioOrganizationPermissionRoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioOrganizationPermissionRole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioOrganizationPermissionRole{}, &HumioOrganizationPermissionRoleList{})
}
