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
	// HumioScheduledSearchStateUnknown is the Unknown state of the scheduled search
	HumioScheduledSearchStateUnknown = "Unknown"
	// HumioScheduledSearchStateExists is the Exists state of the scheduled search
	HumioScheduledSearchStateExists = "Exists"
	// HumioScheduledSearchStateNotFound is the NotFound state of the scheduled search
	HumioScheduledSearchStateNotFound = "NotFound"
	// HumioScheduledSearchStateConfigError is the state of the scheduled search when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioScheduledSearchStateConfigError = "ConfigError"
)

// HumioScheduledSearchSpec defines the desired state of HumioScheduledSearch
type HumioScheduledSearchSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the scheduled search inside Humio
	//+kubebuilder:validation:MinLength=1
	//+required
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the scheduled search will be managed. This can also be a Repository
	//+kubebuilder:validation:MinLength=1
	//+required
	ViewName string `json:"viewName"`
	// QueryString defines the desired Humio query string
	QueryString string `json:"queryString"`
	// Description is the description of the scheduled search
	//+optional
	Description string `json:"description,omitempty"`
	// QueryStart is the start of the relative time interval for the query.
	QueryStart string `json:"queryStart"`
	// QueryEnd is the end of the relative time interval for the query.
	QueryEnd string `json:"queryEnd"`
	// Schedule is the cron pattern describing the schedule to execute the query on.
	Schedule string `json:"schedule"`
	// TimeZone is the time zone of the schedule. Currently, this field only supports UTC offsets like 'UTC', 'UTC-01' or 'UTC+12:45'.
	TimeZone string `json:"timeZone"`
	// BackfillLimit is the user-defined limit, which caps the number of missed searches to backfill, e.g. in the event of a shutdown.
	BackfillLimit int `json:"backfillLimit"`
	// Enabled will set the ScheduledSearch to enabled when set to true
	Enabled bool `json:"enabled,omitempty"`
	// Actions is the list of Humio Actions by name that will be triggered by this scheduled search
	Actions []string `json:"actions"`
	// Labels are a set of labels on the scheduled search
	Labels []string `json:"labels,omitempty"`
}

// HumioScheduledSearchStatus defines the observed state of HumioScheduledSearch
type HumioScheduledSearchStatus struct {
	// State reflects the current state of the HumioScheduledSearch
	State string `json:"state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HumioScheduledSearch is the Schema for the HumioScheduledSearches API
type HumioScheduledSearch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioScheduledSearchSpec   `json:"spec,omitempty"`
	Status HumioScheduledSearchStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioScheduledSearchList contains a list of HumioScheduledSearch
type HumioScheduledSearchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioScheduledSearch `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioScheduledSearch{}, &HumioScheduledSearchList{})
}
