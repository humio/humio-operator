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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	// HumioTokenUnknown is the Unknown state of the token
	HumioTokenUnknown = "Unknown"
	// HumioTokenExists is the Exists state of the token
	HumioTokenExists = "Exists"
	// HumioTokenNotFound is the NotFound state of the token
	HumioTokenNotFound = "NotFound"
	// HumioTokenConfigError is the state of the token when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioTokenConfigError = "ConfigError"
)

// HumioTokenSpec defines the shared spec of Humio Tokens
type HumioTokenSpec struct {
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
	// Name is the name of the token inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// IPFilterName is the Humio IP Filter to be attached to the Token
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Optional
	IPFilterName string `json:"ipFilterName,omitempty"`
	// Permissions is the list of Humio permissions attached to the token
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:XValidation:rule="self.all(item, size(item) >= 1 && size(item) <= 253)",message="permissions: each item must be 1-253 characters long"
	// +kubebuilder:validation:Required
	Permissions []string `json:"permissions"`
	// ExpiresAt is the time when the token is set to expire.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// TokenSecretName specifies the name of the Kubernetes secret that will be created and contain the token.
	// The key in the secret storing the token is "token".
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([-a-zA-Z0-9]*[a-zA-Z0-9])?$`
	// +kubebuilder:validation:Required
	TokenSecretName string `json:"tokenSecretName"`
	// TokenSecretLabels specifies additional key,value pairs to add as labels on the Kubernetes Secret containing the token.
	// +kubebuilder:validation:MaxProperties=63
	// +kubebuilder:validation:XValidation:rule="self.all(key, size(key) <= 63 && size(key) > 0)",message="tokenSecretLabels keys must be 1-63 characters"
	// +kubebuilder:validation:XValidation:rule="self.all(key, size(self[key]) <= 63 && size(self[key]) > 0)",message="tokenSecretLabels values must be 1-63 characters"
	// +kubebuilder:validation:Optional
	TokenSecretLabels map[string]string `json:"tokenSecretLabels"`
	// TokenSecretAnnotations specifies additional key,value pairs to add as annotations on the Kubernetes Secret containing the token.
	// +kubebuilder:validation:MaxProperties=63
	// +kubebuilder:validation:XValidation:rule="self.all(key, size(key) > 0 && size(key) <= 63)",message="tokenSecretAnnotations keys must be 1-63 characters"
	// +kubebuilder:validation:Optional
	TokenSecretAnnotations map[string]string `json:"tokenSecretAnnotations,omitempty"`
}

// HumioTokenStatus defines the observed state of HumioToken.
type HumioTokenStatus struct {
	// State reflects the current state of the HumioToken
	State string `json:"state,omitempty"`
	// HumioID stores the Humio generated ID for the token
	HumioID string `json:"humioId,omitempty"`
}
