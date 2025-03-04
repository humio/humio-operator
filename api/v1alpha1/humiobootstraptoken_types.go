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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioBootstrapTokenStateNotReady is the NotReady state of the bootstrap token
	HumioBootstrapTokenStateNotReady = "NotReady"
	// HumioBootstrapTokenStateReady is the Ready state of the bootstrap token
	HumioBootstrapTokenStateReady = "Ready"
)

// HumioBootstrapTokenSpec defines the desired state of HumioBootstrapToken.
type HumioBootstrapTokenSpec struct {
	// ManagedClusterName refers to the name of the HumioCluster which will use this bootstrap token
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to the name of the HumioExternalCluster which will use this bootstrap token for authentication
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Image can be set to override the image used to run when generating a bootstrap token. This will default to the image
	// that is used by either the HumioCluster resource or the first NodePool resource if ManagedClusterName is set on the HumioBootstrapTokenSpec
	Image string `json:"bootstrapImage,omitempty"`
	// ImagePullSecrets defines the imagepullsecrets for the bootstrap image onetime pod. These secrets are not created by the operator. This will default to the imagePullSecrets
	// that are used by either the HumioCluster resource or the first NodePool resource if ManagedClusterName is set on the HumioBootstrapTokenSpec
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
	// Affinity defines the affinity for the bootstrap onetime pod. This will default to the affinity of the first
	// non-empty node pool if ManagedClusterName is set on the HumioBootstrapTokenSpec
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	// Resources is the kubernetes resource limits for the bootstrap onetime pod
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`
	// TokenSecret is the secret reference that contains the token to use for this HumioBootstrapToken. This is used if one wants to use an existing
	// token for the BootstrapToken rather than letting the operator create one by running a bootstrap token onetime pod
	TokenSecret HumioTokenSecretSpec `json:"tokenSecret,omitempty"`
	// HashedTokenSecret is the secret reference that contains the hashed token to use for this HumioBootstrapToken. This is used if one wants to use an existing
	// hashed token for the BootstrapToken rather than letting the operator create one by running a bootstrap token onetime pod
	HashedTokenSecret HumioHashedTokenSecretSpec `json:"hashedTokenSecret,omitempty"`
}

type HumioTokenSecretSpec struct {
	// SecretKeyRef is the secret key reference to a kubernetes secret containing the bootstrap token secret
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

type HumioHashedTokenSecretSpec struct {
	// SecretKeyRef is the secret key reference to a kubernetes secret containing the bootstrap hashed token secret
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// HumioBootstrapTokenStatus defines the observed state of HumioBootstrapToken.
type HumioBootstrapTokenStatus struct {
	// State can be "NotReady" or "Ready"
	State string `json:"state,omitempty"`
	// TokenSecretKeyRef contains the secret key reference to a kubernetes secret containing the bootstrap token secret. This is set regardless of whether it's defined
	// in the spec or automatically created
	TokenSecretKeyRef HumioTokenSecretStatus `json:"tokenSecretStatus,omitempty"`
	// HashedTokenSecret is the secret reference that contains the hashed token to use for this HumioBootstrapToken. This is set regardless of whether it's defined
	// in the spec or automatically created
	HashedTokenSecretKeyRef HumioHashedTokenSecretStatus `json:"hashedTokenSecretStatus,omitempty"`
}

// HumioTokenSecretStatus contains the secret key reference to a kubernetes secret containing the bootstrap token secret. This is set regardless of whether it's defined
// in the spec or automatically created
type HumioTokenSecretStatus struct {
	// SecretKeyRef contains the secret key reference to a kubernetes secret containing the bootstrap token secret. This is set regardless of whether it's defined
	// in the spec or automatically created
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// HumioTokenSecretStatus contains the secret key reference to a kubernetes secret containing the bootstrap token secret. This is set regardless of whether it's defined
// in the spec or automatically created
type HumioHashedTokenSecretStatus struct {
	// SecretKeyRef is the secret reference that contains the hashed token to use for this HumioBootstrapToken. This is set regardless of whether it's defined
	// in the spec or automatically created
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiobootstraptokens,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the bootstrap token"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Bootstrap Token"

// HumioBootstrapToken is the Schema for the humiobootstraptokens API.
type HumioBootstrapToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioBootstrapTokenSpec   `json:"spec,omitempty"`
	Status HumioBootstrapTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioBootstrapTokenList contains a list of HumioBootstrapToken.
type HumioBootstrapTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioBootstrapToken `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioBootstrapToken{}, &HumioBootstrapTokenList{})
}
