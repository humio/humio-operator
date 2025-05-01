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
	// HumioExternalClusterStateUnknown is the Unknown state of the external cluster
	HumioExternalClusterStateUnknown = "Unknown"
	// HumioExternalClusterStateReady is the Ready state of the external cluster
	HumioExternalClusterStateReady = "Ready"
)

// HumioExternalClusterSpec defines the desired state of HumioExternalCluster.
type HumioExternalClusterSpec struct {
	// Url is used to connect to the Humio cluster we want to use.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Url string `json:"url"`
	// APITokenSecretName is used to obtain the API token we need to use when communicating with the external Humio cluster.
	// It refers to a Kubernetes secret that must be located in the same namespace as the HumioExternalCluster.
	// The humio-operator instance must be able to read the content of the Kubernetes secret.
	// The Kubernetes secret must be of type opaque, and contain the key "token" which holds the Humio API token.
	// Depending on the use-case it is possible to use different token types, depending on what resources it will be
	// used to manage, e.g. HumioParser.
	// In most cases, it is recommended to create a dedicated user within the LogScale cluster and grant the
	// appropriate permissions to it, then use the personal API token for that user.
	APITokenSecretName string `json:"apiTokenSecretName,omitempty"`
	// Insecure is used to disable TLS certificate verification when communicating with Humio clusters over TLS.
	Insecure bool `json:"insecure,omitempty"`
	// CASecretName is used to point to a Kubernetes secret that holds the CA that will be used to issue intra-cluster TLS certificates.
	// The secret must contain a key "ca.crt" which holds the CA certificate in PEM format.
	CASecretName string `json:"caSecretName,omitempty"`
}

// HumioExternalClusterStatus defines the observed state of HumioExternalCluster.
type HumioExternalClusterStatus struct {
	// State reflects the current state of the HumioExternalCluster
	State string `json:"state,omitempty"`
	// Version shows the Humio cluster version of the HumioExternalCluster
	Version string `json:"version,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioexternalclusters,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the external Humio cluster"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio External Cluster"

// HumioExternalCluster is the Schema for the humioexternalclusters API.
type HumioExternalCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioExternalClusterSpec   `json:"spec"`
	Status HumioExternalClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioExternalClusterList contains a list of HumioExternalCluster.
type HumioExternalClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioExternalCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioExternalCluster{}, &HumioExternalClusterList{})
}
