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
	// HumioViewTokenUnknown is the Unknown state of the View token
	HumioViewTokenUnknown = "Unknown"
	// HumioViewTokenExists is the Exists state of the View token
	HumioViewTokenExists = "Exists"
	// HumioViewTokenNotFound is the NotFound state of the View token
	HumioViewTokenNotFound = "NotFound"
	// HumioViewTokenConfigError is the state of the View token when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioViewTokenConfigError = "ConfigError"
)

// HumioViewTokenSpec defines the desired state of HumioViewToken
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioViewTokenSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio resources should be created.
	// This conflicts with ExternalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the view token inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ViewNames is the Humio list of View names for the token.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:XValidation:rule="self.all(item, size(item) >= 1 && size(item) <= 253)",message="viewNames: each item must be 1-253 characters long"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	ViewNames []string `json:"viewNames"`
	// IPFilterName is the Humio IP Filter to be attached to the View Token
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Optional
	IPFilterName string `json:"ipFilterName,omitempty"`
	// Permissions is the list of Humio permissions attached to the view token
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:XValidation:rule="self.all(item, size(item) >= 1 && size(item) <= 253)",message="permissions: each item must be 1-253 characters long"
	// +kubebuilder:validation:Required
	Permissions []string `json:"permissions"`
	// ExpiresAt is the time when the View token is set to expire.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// TokenSecretName specifies the name of the Kubernetes secret that will be created and contain the view token.
	// The key in the secret storing the View token is "token".
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([-a-zA-Z0-9]*[a-zA-Z0-9])?$`
	// +kubebuilder:validation:Required
	TokenSecretName string `json:"tokenSecretName"`
	// TokenSecretLabels specifies additional key,value pairs to add as labels on the Kubernetes Secret containing the View token.
	// +kubebuilder:validation:MaxProperties=63
	// +kubebuilder:validation:XValidation:rule="self.all(key, size(key) <= 63 && size(key) > 0)",message="tokenSecretLabels keys must be 1-63 characters"
	// +kubebuilder:validation:XValidation:rule="self.all(key, size(self[key]) <= 63 && size(self[key]) > 0)",message="tokenSecretLabels values must be 1-63 characters"
	// +kubebuilder:validation:Optional
	TokenSecretLabels map[string]string `json:"tokenSecretLabels"`
	// TokenSecretAnnotations specifies additional key,value pairs to add as annotations on the Kubernetes Secret containing the View token.
	// +kubebuilder:validation:MaxProperties=63
	// +kubebuilder:validation:XValidation:rule="self.all(key, size(key) > 0 && size(key) <= 63)",message="tokenSecretAnnotations keys must be 1-63 characters"
	// +kubebuilder:validation:Optional
	TokenSecretAnnotations map[string]string `json:"tokenSecretAnnotations,omitempty"`
}

// HumioViewTokenStatus defines the observed state of HumioViewToken.
type HumioViewTokenStatus struct {
	// State reflects the current state of the HumioViewToken
	State string `json:"state,omitempty"`
	// ID stores the Humio generated ID for the View token
	ID string `json:"id,omitempty"`
	// Token stores the encrypted Humio generated secret for the View token
	Token string `json:"token,omitempty"`
}

// HumioViewToken is the Schema for the humioviewtokens API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioviewtokens,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the View Token"
// +kubebuilder:printcolumn:name="HumioID",type="string",JSONPath=".status.id",description="Humio generated ID"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio View Token"
type HumioViewToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioViewTokenSpec   `json:"spec"`
	Status HumioViewTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioViewTokenList contains a list of HumioViewToken
type HumioViewTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioViewToken `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioViewToken{}, &HumioViewTokenList{})
}
