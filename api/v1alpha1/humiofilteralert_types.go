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
	// HumioFilterAlertStateUnknown is the Unknown state of the filter alert
	HumioFilterAlertStateUnknown = "Unknown"
	// HumioFilterAlertStateExists is the Exists state of the filter alert
	HumioFilterAlertStateExists = "Exists"
	// HumioFilterAlertStateNotFound is the NotFound state of the filter alert
	HumioFilterAlertStateNotFound = "NotFound"
	// HumioFilterAlertStateConfigError is the state of the filter alert when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioFilterAlertStateConfigError = "ConfigError"
)

const (
	// FilterAlertConditionTypeReady indicates whether the filter alert is ready
	FilterAlertConditionTypeReady = "Ready"
	// FilterAlertConditionTypeSynced indicates whether the filter alert is synced with LogScale
	FilterAlertConditionTypeSynced = "Synced"
)

const (
	// FilterAlertReasonReady indicates the filter alert is ready
	FilterAlertReasonReady = "Ready"
	// FilterAlertReasonCreated indicates the filter alert was created
	FilterAlertReasonCreated = "Created"
	// FilterAlertReasonUpdated indicates the filter alert was updated
	FilterAlertReasonUpdated = "Updated"
	// FilterAlertReasonNotFound indicates the filter alert was not found
	FilterAlertReasonNotFound = "NotFound"
	// FilterAlertReasonConfigError indicates a configuration error
	FilterAlertReasonConfigError = "ConfigurationError"
	// FilterAlertReasonConfigSynced indicates the configuration is synced
	FilterAlertReasonConfigSynced = "ConfigurationSynced"
	// FilterAlertReasonConfigDrifted indicates the configuration has drifted
	FilterAlertReasonConfigDrifted = "ConfigurationDrifted"
)

// HumioFilterAlertSpec defines the desired state of HumioFilterAlert.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioFilterAlertSpec struct {
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
	// Name is the name of the filter alert inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the filter alert will be managed. This can also be a Repository
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	ViewName string `json:"viewName"`
	// QueryString defines the desired Humio query string
	QueryString string `json:"queryString"`
	// Description is the description of the filter alert
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
	// ThrottleTimeSeconds is the throttle time in seconds. A filter alert is triggered at most once per the throttle time
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Required
	ThrottleTimeSeconds int `json:"throttleTimeSeconds,omitempty"`
	// ThrottleField is the field on which to throttle
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	ThrottleField *string `json:"throttleField,omitempty"`
	// Enabled will set the FilterAlert to enabled when set to true
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
	// Actions is the list of Humio Actions by name that will be triggered by this filter alert
	Actions []string `json:"actions"`
	// Labels are a set of labels on the filter alert
	// +kubebuilder:validation:Optional
	Labels []string `json:"labels,omitempty"`
	// AllowDataDeletion enables deletion of the LogScale resource when this CR is deleted.
	// If false or unset, the operator will not delete the LogScale resource on CR deletion.
	// +kubebuilder:validation:Optional
	AllowDataDeletion bool `json:"allowDataDeletion,omitempty"`
}

// HumioFilterAlertStatus defines the observed state of HumioFilterAlert.
type HumioFilterAlertStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Reflects the current state of the HumioFilterAlert
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

// HumioFilterAlert is the Schema for the humiofilteralerts API.
type HumioFilterAlert struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioFilterAlertSpec   `json:"spec"`
	Status HumioFilterAlertStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioFilterAlertList contains a list of HumioFilterAlert.
type HumioFilterAlertList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioFilterAlert `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioFilterAlert{}, &HumioFilterAlertList{})
}
