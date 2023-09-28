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
	// HumioBootstrapTokenStateMissing is the Missing state of the bootstrap token
	HumioBootstrapTokenStateMissing = "Missing"
	// HumioBootstrapTokenStateReady is the Ready state of the bootstrap token
	HumioBootstrapTokenStateReady = "Ready"
)

// HumioBootstrapTokenSpec defines the bootstrap token that Humio will use to bootstrap authentication
type HumioBootstrapTokenSpec struct {
	// TODO: determine if we even want to reference the cluster here
	// ManagedClusterName
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`

	Image             string                     `json:"image,omitempty"`
	TokenSecret       HumioTokenSecretSpec       `json:"tokenSecret,omitempty"`
	HashedTokenSecret HumioHashedTokenSecretSpec `json:"hashedTokenSecret,omitempty"`
}

type HumioTokenSecretSpec struct {
	// TODO: we could clean this up by removing the "AutoCreate" and in docs explain if you want to use your own secret
	// then create the secret before the bootstraptoken resource
	AutoCreate   *bool                     `json:"autoCreate,omitempty"`
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

type HumioHashedTokenSecretSpec struct {
	// TODO: maybe remove AutoCreate
	AutoCreate   *bool                     `json:"autoCreate,omitempty"`
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

type HumioBootstrapTokenStatus struct {
	// TODO set the status. This is used by the HumioCluster resource to get the secret reference and load the secret. We don't want to rely on the spec
	// here as the spec could be empty. Or do we want to
	//Created                 bool                         `json:"created,omitempty"`
	TokenSecretKeyRef       HumioTokenSecretStatus       `json:"tokenSecretStatus,omitempty"`
	HashedTokenSecretKeyRef HumioHashedTokenSecretStatus `json:"hashedTokenSecretStatus,omitempty"`
}

type HumioTokenSecretStatus struct {
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

type HumioHashedTokenSecretStatus struct {
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:path=humiobootstraptokens,scope=Namespaced
//+kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the bootstrap token"
//+operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Bootstrap Token"

// HumioBootstrapToken defines the bootstrap token that Humio will use to bootstrap authentication
type HumioBootstrapToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioBootstrapTokenSpec   `json:"spec,omitempty"`
	Status HumioBootstrapTokenStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioBootstrapTokenList contains a list of HumioBootstrapTokens
type HumioBootstrapTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioBootstrapToken `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioBootstrapToken{}, &HumioBootstrapTokenList{})
}
