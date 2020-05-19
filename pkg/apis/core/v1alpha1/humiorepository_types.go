package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioRepositoryStateUnknown is the Unknown state of the repository
	HumioRepositoryStateUnknown = "Unknown"
	// HumioRepositoryStateExists is the Exists state of the repository
	HumioRepositoryStateExists = "Exists"
	// HumioRepositoryStateNotFound is the NotFound state of the repository
	HumioRepositoryStateNotFound = "NotFound"
)

// HumioRetention defines the retention for the repository
type HumioRetention struct {
	// perhaps we should migrate to resource.Quantity? the Humio API needs float64, but that is not supported here, see more here:
	// https://github.com/kubernetes-sigs/controller-tools/issues/245
	IngestSizeInGB  int32 `json:"ingestSizeInGB,omitempty"`
	StorageSizeInGB int32 `json:"storageSizeInGB,omitempty"`
	TimeInDays      int32 `json:"timeInDays,omitempty"`
}

// HumioRepositorySpec defines the desired state of HumioRepository
type HumioRepositorySpec struct {
	// Which cluster
	ManagedClusterName  string `json:"managedClusterName,omitempty"`
	ExternalClusterName string `json:"externalClusterName,omitempty"`

	// Input
	Name              string         `json:"name,omitempty"`
	Description       string         `json:"description,omitempty"`
	Retention         HumioRetention `json:"retention,omitempty"`
	AllowDataDeletion bool           `json:"allowDataDeletion,omitempty"`
}

// HumioRepositoryStatus defines the observed state of HumioRepository
type HumioRepositoryStatus struct {
	State string `json:"state,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioRepository is the Schema for the humiorepositories API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiorepositories,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the parser"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Repository"
type HumioRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioRepositorySpec   `json:"spec,omitempty"`
	Status HumioRepositoryStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioRepositoryList contains a list of HumioRepository
type HumioRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioRepository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioRepository{}, &HumioRepositoryList{})
}
