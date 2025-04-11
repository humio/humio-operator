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

// HumioFeatureFlagSpec defines the desired state of HumioFeatureFlag.
type HumioFeatureFlagSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the feature flag inside Humio
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
}

// HumioFeatureFlagStatus defines the observed state of HumioFeatureFlag.
type HumioFeatureFlagStatus struct {
	// State reflects the current state of the HumioFeatureFlag
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HumioFeatureFlag is the Schema for the humioFeatureFlags API.
type HumioFeatureFlag struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

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
