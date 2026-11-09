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
	// HumioParserStateUnknown is the Unknown state of the parser
	HumioParserStateUnknown = "Unknown"
	// HumioParserStateExists is the Exists state of the parser
	HumioParserStateExists = "Exists"
	// HumioParserStateNotFound is the NotFound state of the parser
	HumioParserStateNotFound = "NotFound"
	// HumioParserStateConfigError is the state of the parser when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioParserStateConfigError = "ConfigError"
)

const (
	// ParserConditionTypeReady represents whether the parser is ready for use
	ParserConditionTypeReady = "Ready"
	// ParserConditionTypeSynced represents whether the parser is synced with LogScale
	ParserConditionTypeSynced = "Synced"
)

const (
	// ParserReasonReady indicates the parser is ready
	ParserReasonReady = "Ready"
	// ParserReasonCreated indicates the parser was successfully created
	ParserReasonCreated = "Created"
	// ParserReasonUpdated indicates the parser was successfully updated
	ParserReasonUpdated = "Updated"
	// ParserReasonNotFound indicates the parser was not found in LogScale
	ParserReasonNotFound = "NotFound"
	// ParserReasonConfigError indicates a configuration error
	ParserReasonConfigError = "ConfigurationError"
)

// HumioParserSpec defines the desired state of HumioParser.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioParserSpec struct {
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
	// Name is the name of the parser inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ParserScript contains the code for the Humio parser
	ParserScript string `json:"parserScript,omitempty"`
	// RepositoryName defines what repository this parser should be managed in
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	RepositoryName string `json:"repositoryName,omitempty"`
	// TagFields is used to define what fields will be used to define how data will be tagged when being parsed by
	// this parser
	TagFields []string `json:"tagFields,omitempty"`
	// TestData contains example test data to verify the parser behavior
	TestData []string `json:"testData,omitempty"`
	// AllowDataDeletion enables deletion of the LogScale resource when this CR is deleted.
	// If false or unset, the operator will not delete the LogScale resource on CR deletion.
	// +kubebuilder:validation:Optional
	AllowDataDeletion bool `json:"allowDataDeletion,omitempty"`
}

// HumioParserStatus defines the observed state of HumioParser.
type HumioParserStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Reflects the current state of the HumioParser
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
// +kubebuilder:resource:path=humioparsers,scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the parser"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Parser"

// HumioParser is the Schema for the humioparsers API.
type HumioParser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioParserSpec   `json:"spec"`
	Status HumioParserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioParserList contains a list of HumioParser.
type HumioParserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioParser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioParser{}, &HumioParserList{})
}
