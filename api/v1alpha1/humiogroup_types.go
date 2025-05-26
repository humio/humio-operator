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

// HumioGroupSpec defines the desired state of HumioGroup.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioGroupSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the display name of the HumioGroup
	// +kubebuilder:validation:MinLength=2
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ExternalMappingName is the mapping name from the external provider that will assign the user to this HumioGroup
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ExternalMappingName *string `json:"externalMappingName,omitempty"`
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

	// +kubebuilder:validation:Required
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
