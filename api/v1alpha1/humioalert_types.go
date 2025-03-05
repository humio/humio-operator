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
	// HumioAlertStateUnknown is the Unknown state of the alert
	HumioAlertStateUnknown = "Unknown"
	// HumioAlertStateExists is the Exists state of the alert
	HumioAlertStateExists = "Exists"
	// HumioAlertStateNotFound is the NotFound state of the alert
	HumioAlertStateNotFound = "NotFound"
	// HumioAlertStateConfigError is the state of the alert when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioAlertStateConfigError = "ConfigError"
)

// HumioQuery defines the desired state of the Humio query
type HumioQuery struct {
	// QueryString is the Humio query that will trigger the alert
	QueryString string `json:"queryString"`
	// Start is the start time for the query. Defaults to "24h"
	Start string `json:"start,omitempty"`
	// End is the end time for the query. Defaults to "now"
	// Deprecated: Will be ignored. All alerts end at "now".
	DeprecatedEnd string `json:"end,omitempty"`
	// IsLive sets whether the query is a live query. Defaults to "true"
	// Deprecated: Will be ignored. All alerts are live.
	DeprecatedIsLive *bool `json:"isLive,omitempty"`
}

// HumioAlertSpec defines the desired state of HumioAlert.
type HumioAlertSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the alert inside Humio
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the Alert will be managed. This can also be a Repository
	// +kubebuilder:validation:MinLength=1
	// +required
	ViewName string `json:"viewName"`
	// Query defines the desired state of the Humio query
	// +required
	Query HumioQuery `json:"query"`
	// Description is the description of the Alert
	// +optional
	Description string `json:"description,omitempty"`
	// ThrottleTimeMillis is the throttle time in milliseconds. An Alert is triggered at most once per the throttle time
	ThrottleTimeMillis int `json:"throttleTimeMillis,omitempty"`
	// ThrottleField is the field on which to throttle
	ThrottleField *string `json:"throttleField,omitempty"`
	// Silenced will set the Alert to enabled when set to false
	Silenced bool `json:"silenced,omitempty"`
	// Actions is the list of Humio Actions by name that will be triggered by this Alert
	Actions []string `json:"actions"`
	// Labels are a set of labels on the Alert
	Labels []string `json:"labels,omitempty"`
}

// HumioAlertStatus defines the observed state of HumioAlert.
type HumioAlertStatus struct {
	// State reflects the current state of the HumioAlert
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HumioAlert is the Schema for the humioalerts API.
type HumioAlert struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioAlertSpec   `json:"spec,omitempty"`
	Status HumioAlertStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioAlertList contains a list of HumioAlert.
type HumioAlertList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioAlert `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioAlert{}, &HumioAlertList{})
}
