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

// HumioIngestTokenSpec defines the desired state of HumioIngestToken.
type HumioIngestTokenSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the ingest token inside Humio
	// +kubebuilder:validation:MinLength=1
	// +required
	Name string `json:"name"`
	// ParserName is the name of the parser which will be assigned to the ingest token.
	// +kubebuilder:validation:MinLength=1
	// +required
	ParserName *string `json:"parserName,omitempty"`
	// RepositoryName is the name of the Humio repository under which the ingest token will be created
	// +kubebuilder:validation:MinLength=1
	// +required
	RepositoryName string `json:"repositoryName,omitempty"`
	// TokenSecretName specifies the name of the Kubernetes secret that will be created
	// and contain the ingest token. The key in the secret storing the ingest token is "token".
	// This field is optional.
	TokenSecretName string `json:"tokenSecretName,omitempty"`
	// TokenSecretLabels specifies additional key,value pairs to add as labels on the Kubernetes Secret containing
	// the ingest token.
	// This field is optional.
	TokenSecretLabels map[string]string `json:"tokenSecretLabels,omitempty"`
	// TokenSecretAnnotations specifies additional key,value pairs to add as annotations on the Kubernetes Secret containing
	// the ingest token.
	// This field is optional.
	TokenSecretAnnotations map[string]string `json:"tokenSecretAnnotations,omitempty"`
}

// HumioIngestTokenStatus defines the observed state of HumioIngestToken.
type HumioIngestTokenStatus struct {
	// State reflects the current state of the HumioIngestToken
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioingesttokens,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the ingest token"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Ingest Token"

// HumioIngestToken is the Schema for the humioingesttokens API.
type HumioIngestToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioIngestTokenSpec   `json:"spec,omitempty"`
	Status HumioIngestTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioIngestTokenList contains a list of HumioIngestToken.
type HumioIngestTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioIngestToken `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioIngestToken{}, &HumioIngestTokenList{})
}
