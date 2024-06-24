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
	// HumioRepositoryStateUnknown is the Unknown state of the repository
	HumioRepositoryStateUnknown = "Unknown"
	// HumioRepositoryStateExists is the Exists state of the repository
	HumioRepositoryStateExists = "Exists"
	// HumioRepositoryStateNotFound is the NotFound state of the repository
	HumioRepositoryStateNotFound = "NotFound"
	// HumioRepositoryStateConfigError is the state of the repository when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioRepositoryStateConfigError = "ConfigError"
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
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the repository inside Humio
	Name string `json:"name,omitempty"`
	// Description contains the description that will be set on the repository
	Description string `json:"description,omitempty"`
	// Retention defines the retention settings for the repository
	Retention HumioRetention `json:"retention,omitempty"`
	// AllowDataDeletion is used as a blocker in case an operation of the operator would delete data within the
	// repository. This must be set to true before the operator will apply retention settings that will (or might)
	// cause data to be deleted within the repository.
	AllowDataDeletion bool `json:"allowDataDeletion,omitempty"`
	// DisableAutomaticSearch is used to disable the start search automatically on loading the search page option.
	DisableAutomaticSearch bool `json:"disableAutomaticSearch,omitempty"`
}

// HumioRepositoryStatus defines the observed state of HumioRepository
type HumioRepositoryStatus struct {
	// State reflects the current state of the HumioRepository
	State string `json:"state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:path=humiorepositories,scope=Namespaced
//+kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the repository"
//+operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Repository"

// HumioRepository is the Schema for the humiorepositories API
type HumioRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioRepositorySpec   `json:"spec,omitempty"`
	Status HumioRepositoryStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioRepositoryList contains a list of HumioRepository
type HumioRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioRepository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioRepository{}, &HumioRepositoryList{})
}
