package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HumioExternalClusterSpec defines the desired state of HumioExternalCluster
type HumioExternalClusterSpec struct {
	Url string `json:"url,omitempty"`
}

// HumioExternalClusterStatus defines the observed state of HumioExternalCluster
type HumioExternalClusterStatus struct {
	Version string `json:"version,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioExternalCluster is the Schema for the humioexternalclusters API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioexternalclusters,scope=Namespaced
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
