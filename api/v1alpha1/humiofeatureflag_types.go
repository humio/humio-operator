/*
Copyright 2025 Humio https://humio.com

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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// HumioFeatureFlagStateUnknown is the Unknown state of the ingest token
	HumioFeatureFlagStateUnknown = "Unknown"
	// HumioFeatureFlagStateExists is the Exists state of the ingest token
	HumioFeatureFlagStateExists = "Exists"
	// HumioFeatureFlagStateNotFound is the NotFound state of the ingest token
	HumioFeatureFlagStateNotFound = "NotFound"
	// HumioFeatureFlagStateConfigError is the state of the ingest token when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioFeatureFlagStateConfigError = "ConfigError"
)

const (
	// FeatureFlagConditionTypeReady indicates whether the FeatureFlag is ready
	FeatureFlagConditionTypeReady = "Ready"
	// FeatureFlagConditionTypeSynced indicates whether the FeatureFlag is synchronized with Humio
	FeatureFlagConditionTypeSynced = "Synced"
)

const (
	// FeatureFlagReasonReady indicates the FeatureFlag is ready
	FeatureFlagReasonReady = "Ready"
	// FeatureFlagReasonCreated indicates the FeatureFlag was created
	FeatureFlagReasonCreated = "Created"
	// FeatureFlagReasonNotFound indicates the FeatureFlag was not found
	FeatureFlagReasonNotFound = "NotFound"
	// FeatureFlagReasonConfigError indicates a configuration error
	FeatureFlagReasonConfigError = "ConfigurationError"
	// FeatureFlagReasonUnknown indicates the FeatureFlag state is unknown
	FeatureFlagReasonUnknown = "Unknown"
)

// HumioFeatureFlagSpec defines the desired state of HumioFeatureFlag.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioFeatureFlagSpec struct {
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
	// Name is the name of the feature flag inside Humio
	// This field is immutable after creation because feature flags reference predefined LogScale features.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Name is immutable"
	Name string `json:"name"`
}

// HumioFeatureFlagStatus defines the observed state of HumioFeatureFlag.
type HumioFeatureFlagStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Reflects the current state of the HumioFeatureFlag
	// +kubebuilder:validation:Optional
	State string `json:"state,omitempty"`
	// Conditions represent the latest available observations of the resource's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state"

// HumioFeatureFlag is the Schema for the humioFeatureFlags API.
type HumioFeatureFlag struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioFeatureFlagSpec   `json:"spec,omitempty"`
	Status HumioFeatureFlagStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioFeatureFlagList contains a list of HumioFeatureFlag.
type HumioFeatureFlagList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioFeatureFlag `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioFeatureFlag{}, &HumioFeatureFlagList{})
}
