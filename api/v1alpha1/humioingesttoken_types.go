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
	// HumioIngestTokenStateUnknown is the Unknown state of the ingest token
	HumioIngestTokenStateUnknown = "Unknown"
	// HumioIngestTokenStateExists is the Exists state of the ingest token
	HumioIngestTokenStateExists = "Exists"
	// HumioIngestTokenStateNotFound is the NotFound state of the ingest token
	HumioIngestTokenStateNotFound = "NotFound"
	// HumioIngestTokenStateConfigError is the state of the ingest token when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioIngestTokenStateConfigError = "ConfigError"
)

// HumioIngestTokenSpec defines the desired state of HumioIngestToken
type HumioIngestTokenSpec struct {
	// Which cluster
	ManagedClusterName  string `json:"managedClusterName,omitempty"`
	ExternalClusterName string `json:"externalClusterName,omitempty"`

	// Input
	Name           string `json:"name,omitempty"`
	ParserName     string `json:"parserName,omitempty"`
	RepositoryName string `json:"repositoryName,omitempty"`

	// Output
	TokenSecretName string `json:"tokenSecretName,omitempty"`
}

// HumioIngestTokenStatus defines the observed state of HumioIngestToken
type HumioIngestTokenStatus struct {
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioingesttokens,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the ingest token"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Ingest Token"

// HumioIngestToken is the Schema for the humioingesttokens API
type HumioIngestToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioIngestTokenSpec   `json:"spec,omitempty"`
	Status HumioIngestTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioIngestTokenList contains a list of HumioIngestToken
type HumioIngestTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioIngestToken `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioIngestToken{}, &HumioIngestTokenList{})
}
