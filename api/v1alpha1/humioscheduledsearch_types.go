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
	"encoding/json"
	"fmt"
	"time"

	"github.com/humio/humio-operator/api/v1beta1"
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
	// HumioScheduledSearchTimeNow represents the "now" time value used in time parsing
	HumioScheduledSearchTimeNow = "now"
)

// HumioScheduledSearchSpec defines the desired state of HumioScheduledSearch.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
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
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the scheduled search will be managed. This can also be a Repository
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	ViewName string `json:"viewName"`
	// QueryString defines the desired Humio query string
	QueryString string `json:"queryString"`
	// Description is the description of the scheduled search
	// +kubebuilder:validation:Optional
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
	// +kubebuilder:default=0
	BackfillLimit int `json:"backfillLimit"`
	// Enabled will set the ScheduledSearch to enabled when set to true
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`
	// Actions is the list of Humio Actions by name that will be triggered by this scheduled search
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

// ConvertTo converts this v1alpha1 to the Hub version (v1beta1)
func (src *HumioScheduledSearch) ConvertTo(dstRaw conversion.Hub) error {
	dst := dstRaw.(*v1beta1.HumioScheduledSearch)

	// Normal conversion: v1alpha1 -> v1beta1
	dst.ObjectMeta = src.ObjectMeta
	dst.Status = v1beta1.HumioScheduledSearchStatus(src.Status)

	// Re-initialize maps after ObjectMeta copy in case they were nil
	if dst.Labels == nil {
		dst.Labels = make(map[string]string)
	}
	if dst.Annotations == nil {
		dst.Annotations = make(map[string]string)
	}

	// Preserve original v1alpha1 spec
	specJson, err := json.Marshal(src.Spec)
	if err != nil {
		return fmt.Errorf("failed to marshal original v1alpha1 spec for preservation: %v", err)
	} else {
		dst.Annotations["humio.com/original-v1alpha1-spec"] = string(specJson)
		dst.Labels["humio.com/conversion-time"] = fmt.Sprintf("%d", time.Now().Unix())
	}

	// Convert spec fields from v1alpha1 to v1beta1
	dst.Spec.ManagedClusterName = src.Spec.ManagedClusterName
	dst.Spec.ExternalClusterName = src.Spec.ExternalClusterName
	dst.Spec.Name = src.Spec.Name
	dst.Spec.ViewName = src.Spec.ViewName
	dst.Spec.QueryString = src.Spec.QueryString
	dst.Spec.Description = src.Spec.Description
	dst.Spec.BackfillLimit = &src.Spec.BackfillLimit
	dst.Spec.QueryTimestampType = humiographql.QueryTimestampTypeEventtimestamp
	dst.Spec.Schedule = src.Spec.Schedule
	dst.Spec.TimeZone = src.Spec.TimeZone
	dst.Spec.Enabled = src.Spec.Enabled
	dst.Spec.Actions = src.Spec.Actions
	dst.Spec.Labels = src.Spec.Labels

	// Convert time fields
	start, err := ParseTimeStringToSeconds(src.Spec.QueryStart)
	if err != nil {
		return fmt.Errorf("could not convert src.Spec.QueryStart to seconds, value received '%v': %w", src.Spec.QueryStart, err)
	}

	end, err := ParseTimeStringToSeconds(src.Spec.QueryEnd)
	if err != nil {
		return fmt.Errorf("could not convert src.Spec.QueryEnd to seconds, value received '%v': %w", src.Spec.QueryEnd, err)
	}
	dst.Spec.SearchIntervalOffsetSeconds = &end
	dst.Spec.SearchIntervalSeconds = start
	return nil
}

// ConvertFrom converts from the Hub version (v1beta1) to v1alpha1
func (dst *HumioScheduledSearch) ConvertFrom(srcRaw conversion.Hub) error {
	src := srcRaw.(*v1beta1.HumioScheduledSearch)
	// Convert metadata first
	dst.ObjectMeta = src.ObjectMeta

	// Re-initialize maps after ObjectMeta copy in case they were nil in src
	if dst.Labels == nil {
		dst.Labels = make(map[string]string)
	}
	if dst.Annotations == nil {
		dst.Annotations = make(map[string]string)
	}

	// Convert status
	dst.Status = HumioScheduledSearchStatus(src.Status)

	// Convert spec fields from v1beta1 to v1alpha1
	dst.Spec.ManagedClusterName = src.Spec.ManagedClusterName
	dst.Spec.ExternalClusterName = src.Spec.ExternalClusterName
	dst.Spec.Name = src.Spec.Name
	dst.Spec.ViewName = src.Spec.ViewName
	dst.Spec.QueryString = src.Spec.QueryString
	dst.Spec.Description = src.Spec.Description
	dst.Spec.Schedule = src.Spec.Schedule
	dst.Spec.TimeZone = src.Spec.TimeZone
	// Backfill needs to default to 0
	backfill := 0
	if src.Spec.BackfillLimit != nil {
		backfill = *src.Spec.BackfillLimit
	}
	dst.Spec.BackfillLimit = backfill
	dst.Spec.Enabled = src.Spec.Enabled
	dst.Spec.Actions = src.Spec.Actions
	dst.Spec.Labels = src.Spec.Labels

	// Convert time fields with error handling
	var err error
	dst.Spec.QueryStart, err = ParseSecondsToString(src.Spec.SearchIntervalSeconds)
	if err != nil {
		return fmt.Errorf("failed to convert SearchIntervalSeconds: %w", err)
	}

	dst.Spec.QueryEnd = HumioScheduledSearchTimeNow // default
	if src.Spec.SearchIntervalOffsetSeconds != nil {
		if *src.Spec.SearchIntervalOffsetSeconds > int64(0) {
			dst.Spec.QueryEnd, err = ParseSecondsToString(*src.Spec.SearchIntervalOffsetSeconds)
			if err != nil {
				return fmt.Errorf("failed to convert SearchIntervalOffsetSeconds: %w", err)
			}
		}
	}
	return nil
}

// Ensure the type implements the Convertible interface
var _ conversion.Convertible = &HumioScheduledSearch{}

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

// ParseTimeStringToSeconds converts time strings like "now", "1m", "1h", "1day", "1year" to seconds
func ParseTimeStringToSeconds(timeStr string) (int64, error) {
	if timeStr == HumioScheduledSearchTimeNow {
		return 0, nil
	}

	if len(timeStr) < 2 {
		return 0, fmt.Errorf("invalid time string: %s", timeStr)
	}

	var value int64
	var unit string

	// Find where the number ends and unit begins
	i := 0
	for i < len(timeStr) && (timeStr[i] >= '0' && timeStr[i] <= '9') {
		i++
	}

	if i == 0 {
		return 0, fmt.Errorf("invalid time string: %s", timeStr)
	}

	_, err := fmt.Sscanf(timeStr[:i], "%d", &value)
	if err != nil {
		return 0, fmt.Errorf("invalid number in time string: %s", timeStr)
	}

	unit = timeStr[i:]

	switch unit {
	case "s", "sec", "second", "seconds":
		return value, nil
	case "m", "min", "minute", "minutes":
		return value * 60, nil
	case "h", "hour", "hours":
		return value * 3600, nil
	case "d", "day", "days":
		return value * 86400, nil
	case "w", "week", "weeks":
		return value * 604800, nil
	case "y", "year", "years":
		return value * 31536000, nil
	default:
		return 0, fmt.Errorf("unknown time unit: %s", unit)
	}
}

// ParseSecondsToString converts seconds to human-readable time strings like "1m", "1h", "1d", etc.
func ParseSecondsToString(timeSeconds int64) (string, error) {
	if timeSeconds <= 0 {
		return HumioScheduledSearchTimeNow, nil
	}

	units := []struct {
		name     string
		duration int64
	}{
		{"d", 86400}, // 24 * 60 * 60
		{"h", 3600},  // 60 * 60
		{"m", 60},
		{"s", 1},
	}

	for _, unit := range units {
		if timeSeconds >= unit.duration && timeSeconds%unit.duration == 0 {
			return fmt.Sprintf("%d%s", timeSeconds/unit.duration, unit.name), nil
		}
	}

	return fmt.Sprintf("%ds", timeSeconds), nil
}
