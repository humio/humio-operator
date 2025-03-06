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

// HumioParserSpec defines the desired state of HumioParser.
type HumioParserSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the parser inside Humio
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
	// ParserScript contains the code for the Humio parser
	ParserScript string `json:"parserScript,omitempty"`
	// RepositoryName defines what repository this parser should be managed in
	// +kubebuilder:validation:MinLength=1
	// +required
	RepositoryName string `json:"repositoryName,omitempty"`
	// TagFields is used to define what fields will be used to define how data will be tagged when being parsed by
	// this parser
	TagFields []string `json:"tagFields,omitempty"`
	// TestData contains example test data to verify the parser behavior
	TestData []string `json:"testData,omitempty"`
}

// HumioParserStatus defines the observed state of HumioParser.
type HumioParserStatus struct {
	// State reflects the current state of the HumioParser
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioparsers,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the parser"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Parser"

// HumioParser is the Schema for the humioparsers API.
type HumioParser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioParserSpec   `json:"spec,omitempty"`
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
