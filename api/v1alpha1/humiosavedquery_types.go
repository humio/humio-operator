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

// HumioSavedQuerySpec defines the desired state of HumioSavedQuery.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioSavedQuerySpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the saved query inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the SavedQuery will be managed. This can also be a Repository
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	ViewName string `json:"viewName"`
	// QueryString is the actual query that will be saved
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1048576
	// +kubebuilder:validation:Required
	QueryString string `json:"queryString"`
	// Description is the description of the SavedQuery.
	// This field is only supported in LogScale 1.200.0 and later.
	// For earlier versions, a warning condition is set but reconciliation continues.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=4096
	Description string `json:"description,omitempty"`
	// Labels are a set of labels on the SavedQuery (maximum 10 labels allowed by LogScale).
	// This field is only supported in LogScale 1.200.0 and later.
	// For earlier versions, a warning condition is set but reconciliation continues.
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:Optional
	Labels []string `json:"labels,omitempty"`
}

// HumioSavedQueryStatus defines the observed state of HumioSavedQuery.
type HumioSavedQueryStatus struct {
	// Conditions represent the latest available observations of the saved query's state
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// LastSyncedViewName tracks the view name where the saved query was last successfully synced.
	// This is used to detect and handle view migrations.
	// +optional
	LastSyncedViewName string `json:"lastSyncedViewName,omitempty"`
	// ManagedByOperator indicates whether the operator successfully owns this LogScale saved query.
	// Set to true when the operator creates a new query or successfully adopts an existing one.
	// Set to false when adoption is rejected.
	// When false, the finalizer will not delete the query from LogScale.
	// +optional
	ManagedByOperator *bool `json:"managedByOperator,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiosavedqueries,scope=Namespaced,shortName=hsq,categories={humio,all}
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status",description="Status of the saved query"
// +kubebuilder:printcolumn:name="View",type="string",JSONPath=".spec.viewName",description="The view containing the saved query"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Saved Query"

// HumioSavedQuery is the Schema for the humiosavedqueries API.
type HumioSavedQuery struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioSavedQuerySpec   `json:"spec"`
	Status HumioSavedQueryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioSavedQueryList contains a list of HumioSavedQuery.
type HumioSavedQueryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioSavedQuery `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioSavedQuery{}, &HumioSavedQueryList{})
}
