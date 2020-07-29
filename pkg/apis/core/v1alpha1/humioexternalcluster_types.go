package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioExternalClusterStateUnknown is the Unknown state of the external cluster
	HumioExternalClusterStateUnknown = "Unknown"
	// HumioExternalClusterStateRunning is the Ready state of the external cluster
	HumioExternalClusterStateReady = "Ready"
)

// HumioExternalClusterSpec defines the desired state of HumioExternalCluster
type HumioExternalClusterSpec struct {
	// Url is used to connect to the Humio cluster we want to use.
	Url string `json:"url,omitempty"`
	// APITokenSecretName is used to obtain the API token we need to use when communicating with the external Humio cluster.
	// The secret must contain a key "token" which holds the Humio API token.
	APITokenSecretName string `json:"apiTokenSecretName,omitempty"`
	// TLSDisabled is used to disable intra-cluster TLS when cert-manager is being used.
	Insecure bool `json:"insecure,omitempty"`
	// CASecretName is used to point to a Kubernetes secret that holds the CA that will be used to issue intra-cluster TLS certificates.
	// The secret must contain a key "ca.crt" which holds the CA certificate in PEM format.
	CASecretName string `json:"caSecretName,omitempty"`
}

// HumioExternalClusterStatus defines the observed state of HumioExternalCluster
type HumioExternalClusterStatus struct {
	State   string `json:"state,omitempty"`
	Version string `json:"version,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioExternalCluster is the Schema for the humioexternalclusters API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioexternalclusters,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the external Humio cluster"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio External Cluster"
type HumioExternalCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioExternalClusterSpec   `json:"spec,omitempty"`
	Status HumioExternalClusterStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioExternalClusterList contains a list of HumioExternalCluster
type HumioExternalClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioExternalCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioExternalCluster{}, &HumioExternalClusterList{})
}
