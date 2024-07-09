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

// HumioFilterAlertSpec defines the desired state of HumioFilterAlert
type HumioFilterAlertSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the filter alert inside Humio
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the filter alert will be managed. This can also be a Repository
	ViewName string `json:"viewName"`
	// QueryString defines the desired Humio query string
	QueryString string `json:"queryString"`
	// Description is the description of the filter alert
	Description string `json:"description,omitempty"`
	// ThrottleTimeSeconds is the throttle time in seconds. A filter alert is triggered at most once per the throttle time
	ThrottleTimeSeconds *int `json:"throttleTimeSeconds,omitempty"`
	// ThrottleField is the field on which to throttle
	ThrottleField string `json:"throttleField,omitempty"`
	// Enabled will set the FilterAlert to enabled when set to true
	Enabled bool `json:"enabled,omitempty"`
	// Actions is the list of Humio Actions by name that will be triggered by this filter alert
	Actions []string `json:"actions"`
	// Labels are a set of labels on the filter alert
	Labels []string `json:"labels,omitempty"`
}

// HumioFilterAlertStatus defines the observed state of HumioFilterAlert
type HumioFilterAlertStatus struct {
	// State reflects the current state of the HumioFilterAlert
	State string `json:"state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HumioFilterAlert is the Schema for the HumioFilterAlerts API
type HumioFilterAlert struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioFilterAlertSpec   `json:"spec,omitempty"`
	Status HumioFilterAlertStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioFilterAlertList contains a list of HumioFilterAlert
type HumioFilterAlertList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioFilterAlert `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioFilterAlert{}, &HumioFilterAlertList{})
}
