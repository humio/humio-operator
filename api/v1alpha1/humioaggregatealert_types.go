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
	// HumioAggregateAlertStateUnknown is the Unknown state of the aggregate alert
	HumioAggregateAlertStateUnknown = "Unknown"
	// HumioAggregateAlertStateExists is the Exists state of the aggregate alert
	HumioAggregateAlertStateExists = "Exists"
	// HumioAggregateAlertStateNotFound is the NotFound state of the aggregate alert
	HumioAggregateAlertStateNotFound = "NotFound"
	// HumioAggregateAlertStateConfigError is the state of the aggregate alert when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioAggregateAlertStateConfigError = "ConfigError"
)

const (
	// AggregateAlertConditionTypeReady indicates whether the aggregate alert is ready
	AggregateAlertConditionTypeReady = "Ready"
	// AggregateAlertConditionTypeSynced indicates whether the aggregate alert is synced with LogScale
	AggregateAlertConditionTypeSynced = "Synced"
)

const (
	// AggregateAlertReasonReady indicates the aggregate alert is ready
	AggregateAlertReasonReady = "Ready"
	// AggregateAlertReasonCreated indicates the aggregate alert was created
	AggregateAlertReasonCreated = "Created"
	// AggregateAlertReasonUpdated indicates the aggregate alert was updated
	AggregateAlertReasonUpdated = "Updated"
	// AggregateAlertReasonNotFound indicates the aggregate alert was not found
	AggregateAlertReasonNotFound = "NotFound"
	// AggregateAlertReasonConfigError indicates a configuration error
	AggregateAlertReasonConfigError = "ConfigurationError"
	// AggregateAlertReasonConfigSynced indicates the configuration is synced
	AggregateAlertReasonConfigSynced = "ConfigurationSynced"
	// AggregateAlertReasonConfigDrifted indicates the configuration has drifted
	AggregateAlertReasonConfigDrifted = "ConfigurationDrifted"
)

// HumioAggregateAlertSpec defines the desired state of HumioAggregateAlert.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioAggregateAlertSpec struct {
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
	// Name is the name of the aggregate alert inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the aggregate alert will be managed. This can also be a Repository
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	ViewName string `json:"viewName"`
	// QueryString defines the desired Humio query string
	QueryString string `json:"queryString"`
	// QueryTimestampType defines the timestamp type to use for a query
	QueryTimestampType string `json:"queryTimestampType,omitempty"`
	// Description is the description of the Aggregate alert
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
	// SearchIntervalSeconds specifies the search interval (in seconds) to use when running the query
	SearchIntervalSeconds int `json:"searchIntervalSeconds,omitempty"`
	// ThrottleTimeSeconds is the throttle time in seconds. An aggregate alert is triggered at most once per the throttle time
	ThrottleTimeSeconds int `json:"throttleTimeSeconds,omitempty"`
	// ThrottleField is the field on which to throttle
	ThrottleField *string `json:"throttleField,omitempty"`
	// TriggerMode specifies which trigger mode to use when configuring the aggregate alert
	TriggerMode string `json:"triggerMode,omitempty"`
	// Enabled will set the AggregateAlert to enabled when set to true
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
	// Actions is the list of Humio Actions by name that will be triggered by this Aggregate alert
	Actions []string `json:"actions"`
	// Labels are a set of labels on the aggregate alert
	// +kubebuilder:validation:Optional
	Labels []string `json:"labels,omitempty"`
	// AllowDataDeletion enables deletion of the LogScale resource when this CR is deleted.
	// If false or unset, the operator will not delete the LogScale resource on CR deletion.
	// +kubebuilder:validation:Optional
	AllowDataDeletion bool `json:"allowDataDeletion,omitempty"`
}

// HumioAggregateAlertStatus defines the observed state of HumioAggregateAlert.
type HumioAggregateAlertStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Reflects the current state of HumioAggregateAlert
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

// HumioAggregateAlert is the Schema for the humioaggregatealerts API.
type HumioAggregateAlert struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioAggregateAlertSpec   `json:"spec"`
	Status HumioAggregateAlertStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioAggregateAlertList contains a list of HumioAggregateAlert.
type HumioAggregateAlertList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioAggregateAlert `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioAggregateAlert{}, &HumioAggregateAlertList{})
}
