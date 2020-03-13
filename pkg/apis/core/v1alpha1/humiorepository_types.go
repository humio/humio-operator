package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HumioRetention defines the retention for the repository
// TODO: this is not implemented in the humio api yet
type HumioRetention struct {
	IngestSizeInGB  int64 `json:"ingest_size_in_gb,omitempty"`
	StorageSizeInGB int64 `json:"storage_size_in_gb,omitempty"`
	TimeInDays      int64 `json:"time_in_days,omitempty"`
}

// HumioRepositorySpec defines the desired state of HumioRepository
type HumioRepositorySpec struct {
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Retention   HumioRetention `json:"retention,omitempty"`
}

// HumioRepositoryStatus defines the observed state of HumioRepository
type HumioRepositoryStatus struct {
	// TODO?
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// HumioRepository is the Schema for the humiorepositories API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiorepositories,scope=Namespaced
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
