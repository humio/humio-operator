package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// HumioIngestTokenSpec defines the desired state of HumioIngestToken
type HumioIngestTokenSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// HumioIngestTokenStatus defines the observed state of HumioIngestToken
type HumioIngestTokenStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioIngestToken is the Schema for the humioingesttokens API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioingesttokens,scope=Namespaced
type HumioIngestToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioIngestTokenSpec   `json:"spec,omitempty"`
	Status HumioIngestTokenStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioIngestTokenList contains a list of HumioIngestToken
type HumioIngestTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioIngestToken `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioIngestToken{}, &HumioIngestTokenList{})
}
