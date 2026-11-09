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

const (
	// GroupConditionTypeReady indicates whether the group is ready
	GroupConditionTypeReady = "Ready"
	// GroupConditionTypeSynced indicates whether the group is synced with LogScale
	GroupConditionTypeSynced = "Synced"
)

const (
	// GroupReasonReady indicates the group is ready
	GroupReasonReady = "Ready"
	// GroupReasonCreated indicates the group was created
	GroupReasonCreated = "Created"
	// GroupReasonUpdated indicates the group was updated
	GroupReasonUpdated = "Updated"
	// GroupReasonNotFound indicates the group was not found
	GroupReasonNotFound = "NotFound"
	// GroupReasonConfigError indicates a configuration error
	GroupReasonConfigError = "ConfigurationError"
	// GroupReasonConfigSynced indicates the configuration is synced
	GroupReasonConfigSynced = "ConfigurationSynced"
	// GroupReasonConfigDrifted indicates the configuration has drifted
	GroupReasonConfigDrifted = "ConfigurationDrifted"
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
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ExternalMappingName is the mapping name from the external provider that will assign the user to this HumioGroup
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ExternalMappingName *string `json:"externalMappingName,omitempty"`
	// AllowDataDeletion enables deletion of the LogScale resource when this CR is deleted.
	// If false or unset, the operator will not delete the LogScale resource on CR deletion.
	// +kubebuilder:validation:Optional
	AllowDataDeletion bool `json:"allowDataDeletion,omitempty"`
}

// HumioGroupStatus defines the observed state of HumioGroup.
type HumioGroupStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Reflects the current state of the HumioGroup
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
// +kubebuilder:resource:path=humiogroups,scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
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
