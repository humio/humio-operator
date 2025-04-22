package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioGroupStateUnknown is the Unknown state of the group
	HumioGroupStateUnknown = "Unknown"
	// HumioGroupStateExists is the Exists state of the group
	HumioGroupStateExists = "Exists"
	// HumioGroupStateNotFound is the NotFound state of the group
	HumioGroupStateNotFound = "NotFound"
	// HumioGroupStateConfigError is the state of the group when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioGroupStateConfigError = "ConfigError"
)

// HumioGroupRoleAssignment represents a role assignment for a group
type HumioGroupRoleAssignment struct {
	// RoleName contains the name of the role to assign
	// +kubebuilder:validation:MinLength=1
	// +required
	RoleName string `json:"roleName"`
	// ViewName contains the name of the view to associate the group with
	// +kubebuilder:validation:MinLength=1
	// +required
	ViewName string `json:"viewName"`
}

// HumioGroupSpec defines the desired state of HumioGroup.
type HumioGroupSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// DisplayName is the display name of the HumioGroup
	// +kubebuilder:validation:MinLength=1
	// +required
	DisplayName string `json:"displayName"`
	// LookupName is the lookup name of the HumioGroup
	// +optional
	LookupName *string `json:"lookupName,omitempty"`
	// Assignments contains the list of role assignments for the group
	// +optional
	Assignments []HumioGroupRoleAssignment `json:"assignments,omitempty"`
}

// HumioGroupStatus defines the observed state of HumioGroup.
type HumioGroupStatus struct {
	// State reflects the current state of the HumioGroup
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiogroups,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the group"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Group"

// HumioGroup is the Schema for the humiogroups API
type HumioGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioGroupSpec   `json:"spec,omitempty"`
	Status HumioGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioGroupList contains a list of HumioGroup
type HumioGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioGroup{}, &HumioGroupList{})
}
