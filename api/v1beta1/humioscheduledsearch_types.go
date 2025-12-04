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

package v1beta1

import (
	"github.com/humio/humio-operator/internal/api/humiographql"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/conversion"
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
	// HumioScheduledSearchV1alpha1DeprecatedInVersion tracks the LS release when v1alpha1 was deprecated
	HumioScheduledSearchV1alpha1DeprecatedInVersion = "1.180.0"
)

// HumioScheduledSearchSpec defines the desired state of HumioScheduledSearch.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
// +kubebuilder:validation:XValidation:rule="self.queryTimestampType != 'IngestTimestamp' || (has(self.maxWaitTimeSeconds) && self.maxWaitTimeSeconds >= 0)",message="maxWaitTimeSeconds is required when QueryTimestampType is IngestTimestamp"
// +kubebuilder:validation:XValidation:rule="self.queryTimestampType != 'EventTimestamp' || (has(self.backfillLimit) && self.backfillLimit >= 0)",message="backfillLimit is required when QueryTimestampType is EventTimestamp"
// +kubebuilder:validation:XValidation:rule="self.queryTimestampType != 'IngestTimestamp' || !has(self.backfillLimit)",message="backfillLimit is accepted only when queryTimestampType is set to 'EventTimestamp'"
// +kubebuilder:validation:XValidation:rule="self.queryTimestampType != 'EventTimestamp' || (has(self.searchIntervalOffsetSeconds) && self.searchIntervalOffsetSeconds >= 0)",message="SearchIntervalOffsetSeconds is required when QueryTimestampType is EventTimestamp"
// +kubebuilder:validation:XValidation:rule="self.queryTimestampType != 'IngestTimestamp' || !has(self.searchIntervalOffsetSeconds)",message="searchIntervalOffsetSeconds is accepted only when queryTimestampType is set to 'EventTimestamp'"
type HumioScheduledSearchSpec struct {
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
	// Name is the name of the scheduled search inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the scheduled search will be managed. This can also be a Repository
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Required
	ViewName string `json:"viewName"`
	// QueryString defines the desired Humio query string
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	QueryString string `json:"queryString"`
	// Description is the description of the scheduled search
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
	// MaxWaitTimeSeconds The maximum number of seconds to wait for ingest delay and query warnings. Only allowed when 'queryTimestamp' is IngestTimestamp
	MaxWaitTimeSeconds int64 `json:"maxWaitTimeSeconds,omitempty"`
	// QueryTimestampType Possible values: EventTimestamp or IngestTimestamp, decides what field is used for timestamp for the query
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=EventTimestamp;IngestTimestamp
	QueryTimestampType humiographql.QueryTimestampType `json:"queryTimestampType"`
	// SearchIntervalSeconds is the search interval in seconds.
	// +kubebuilder:validation:Required
	SearchIntervalSeconds int64 `json:"searchIntervalSeconds"`
	// SearchIntervalOffsetSeconds Offset of the search interval in seconds. Only allowed when 'queryTimestampType' is EventTimestamp where it is mandatory.
	SearchIntervalOffsetSeconds *int64 `json:"searchIntervalOffsetSeconds,omitempty"`
	// Schedule is the cron pattern describing the schedule to execute the query on.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.matches(r'^\\s*([0-9,\\-\\*\\/]+)\\s+([0-9,\\-\\*\\/]+)\\s+([0-9,\\-\\*\\/]+)\\s+([0-9,\\-\\*\\/]+)\\s+([0-9,\\-\\*\\/]+)\\s*$')",message="schedule must be a valid cron expression with 5 fields (minute hour day month weekday)"
	Schedule string `json:"schedule"`
	// TimeZone is the time zone of the schedule. Currently, this field only supports UTC offsets like 'UTC', 'UTC-01' or 'UTC+12:45'.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == 'UTC' || self.matches(r'^UTC[+-]([01]?[0-9]|2[0-3])(:[0-5][0-9])?$')",message="timeZone must be 'UTC' or a UTC offset like 'UTC-01', 'UTC+12:45'"
	TimeZone string `json:"timeZone"`
	// BackfillLimit is the user-defined limit, which caps the number of missed searches to backfill, e.g. in the event of a shutdown. Only allowed when queryTimestamp is EventTimestamp
	BackfillLimit *int `json:"backfillLimit,omitempty"`
	// Enabled will set the ScheduledSearch to enabled when set to true
	// +kubebuilder:default=false
	// +kubebuilder:validation:Optional
	Enabled bool `json:"enabled"`
	// Actions is the list of Humio Actions by name that will be triggered by this scheduled search
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:XValidation:rule="self.all(action, size(action) > 0)",message="Actions cannot contain empty strings"
	Actions []string `json:"actions"`
	// Labels are a set of labels on the scheduled search
	// +kubebuilder:validation:Optional
	Labels []string `json:"labels,omitempty"`
}

// HumioScheduledSearchStatus defines the observed state of HumioScheduledSearch.
type HumioScheduledSearchStatus struct {
	// State reflects the current state of the HumioScheduledSearch
	State string `json:"state,omitempty"`
}

// HumioScheduledSearch is the Schema for the humioscheduledsearches API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// +kubebuilder:resource:path=humioscheduledsearches,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the Scheduled Search"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Scheduled Search"
type HumioScheduledSearch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioScheduledSearchSpec   `json:"spec"`
	Status HumioScheduledSearchStatus `json:"status,omitempty"`
}

// Hub marks this version as the conversion hub
func (*HumioScheduledSearch) Hub() {}

// Ensure the type implements the Hub interface
var _ conversion.Hub = &HumioScheduledSearch{}

// +kubebuilder:object:root=true

// HumioScheduledSearchList contains a list of HumioScheduledSearch.
type HumioScheduledSearchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioScheduledSearch `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioScheduledSearch{}, &HumioScheduledSearchList{})
}
