package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HumioParserSpec defines the desired state of HumioParser
type HumioParserSpec struct {
	// Which cluster
	ManagedClusterName  string `json:"managedClusterName,omitempty"`
	ExternalClusterName string `json:"externalClusterName,omitempty"`

	// Input
	Name           string   `json:"name,omitempty"`
	ParserScript   string   `json:"parserScript,omitempty"`
	RepositoryName string   `json:"repositoryName,omitempty"`
	TagFields      []string `json:"tagFields,omitempty"`
	TestData       []string `json:"testData,omitempty"`
}

// HumioParserStatus defines the observed state of HumioParser
type HumioParserStatus struct {
	Created bool `json:"created,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioParser is the Schema for the humioparsers API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioparsers,scope=Namespaced
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Parser"
type HumioParser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioParserSpec   `json:"spec,omitempty"`
	Status HumioParserStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioParserList contains a list of HumioParser
type HumioParserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioParser `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioParser{}, &HumioParserList{})
}
