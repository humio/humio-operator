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
	// HumioAggregateAlertStateUnknown is the Unknown state of the aggregated alert
	HumioAggregateAlertStateUnknown = "Unknown"
	// HumioAggregateAlertStateExists is the Exists state of the aggregated alert
	HumioAggregateAlertStateExists = "Exists"
	// HumioAggregateAlertStateNotFound is the NotFound state of the aggregated alert
	HumioAggregateAlertStateNotFound = "NotFound"
	// HumioAggregateAlertStateConfigError is the state of the aggregated alert when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioAggregateAlertStateConfigError = "ConfigError"
)

// HumioAggregateAlertSpec defines the desired state of HumioAggregateAlert
type HumioAggregateAlertSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the aggregated alert inside Humio
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the filter alert will be managed. This can also be a Repository
	ViewName string `json:"viewName"`
	// QueryString defines the desired Humio query string
	QueryString string `json:"queryString"`
	// Description is the description of the Aggregated alert
	Description string `json:"description,omitempty"`
	// Search Interval time in seconds
	SearchIntervalSeconds int `json:"searchIntervalSeconds,omitempty"`
	// ThrottleTimeSeconds is the throttle time in seconds. An aggregated alert is triggered at most once per the throttle time
	ThrottleTimeSeconds int `json:"throttleTimeSeconds,omitempty"`
	// ThrottleField is the field on which to throttle
	ThrottleField string `json:"throttleField,omitempty"`
	// Aggregated Alert trigger mode
	TriggerMode string `json:"triggerMode,omitempty"`
	// Enabled will set the AggregateAlert to enabled when set to true
	Enabled bool `json:"enabled,omitempty"`
	// Actions is the list of Humio Actions by name that will be triggered by this Aggregated alert
	Actions []string `json:"actions"`
	// Labels are a set of labels on the filter alert
	Labels []string `json:"labels,omitempty"`
}

// HumioAggregateAlertStatus defines the observed state of HumioAggregateAlert
type HumioAggregateAlertStatus struct {
	// State reflects the current state of HumioAggregateAlert
	State string `json:"state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HumioAggregateAlert is the Schema for the humioAggregateAlerts API
type HumioAggregateAlert struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioAggregateAlertSpec   `json:"spec,omitempty"`
	Status HumioAggregateAlertStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioAggregateAlertList contains a list of HumioAggregateAlert
type HumioAggregateAlertList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioAggregateAlert `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioAggregateAlert{}, &HumioAggregateAlertList{})
}
