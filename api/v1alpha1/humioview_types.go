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
	"github.com/humio/humio-operator/internal/api/humiographql"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioViewStateUnknown is the Unknown state of the view
	HumioViewStateUnknown = "Unknown"
	// HumioViewStateExists is the Exists state of the view
	HumioViewStateExists = "Exists"
	// HumioViewStateNotFound is the NotFound state of the view
	HumioViewStateNotFound = "NotFound"
	// HumioViewStateConfigError is the state of the view when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioViewStateConfigError = "ConfigError"
)

const (
	// ViewConditionTypeReady represents whether the view is ready for use
	ViewConditionTypeReady = "Ready"
	// ViewConditionTypeSynced represents whether the view is synced with LogScale
	ViewConditionTypeSynced = "Synced"
)

const (
	// ViewReasonReady indicates the view is ready
	ViewReasonReady = "Ready"
	// ViewReasonCreated indicates the view was successfully created
	ViewReasonCreated = "Created"
	// ViewReasonUpdated indicates the view was successfully updated
	ViewReasonUpdated = "Updated"
	// ViewReasonNotFound indicates the view was not found in LogScale
	ViewReasonNotFound = "NotFound"
	// ViewReasonConfigError indicates a configuration error
	ViewReasonConfigError = "ConfigurationError"
)

// HumioViewConnection represents a connection to a specific repository with an optional filter
type HumioViewConnection struct {
	// RepositoryName contains the name of the target repository
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	RepositoryName string `json:"repositoryName,omitempty"`
	// Filter contains the prefix filter that will be applied for the given RepositoryName
	Filter string `json:"filter,omitempty"`
}

// HumioViewSpec defines the desired state of HumioView.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioViewSpec struct {
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
	// Name is the name of the view inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Description contains the description that will be set on the view
	// +kubebuilder:validation:Optional
	Description string `json:"description,omitempty"`
	// Connections contains the connections to the Humio repositories which is accessible in this view
	Connections []HumioViewConnection `json:"connections,omitempty"`
	// AutomaticSearch is used to specify the start search automatically on loading the search page option.
	AutomaticSearch *bool `json:"automaticSearch,omitempty"`
	// AllowDataDeletion enables deletion of the LogScale resource when this CR is deleted.
	// If false or unset, the operator will not delete the LogScale resource on CR deletion.
	// +kubebuilder:validation:Optional
	AllowDataDeletion bool `json:"allowDataDeletion,omitempty"`
	// CascadeRenames enables automatic cascading of view name changes to dependent resources
	// (HumioAlert, HumioAggregateAlert, HumioFilterAlert, HumioScheduledSearch, HumioAction,
	// HumioSavedQuery, HumioMultiClusterSearchView, HumioEventForwardingRule). When true,
	// renaming this view will automatically update references in dependent resources. When false
	// (default), rename operations proceed without updating dependent resources, which is safer
	// for GitOps workflows where dependent resource specs are managed in version control.
	// +kubebuilder:default=false
	// +kubebuilder:validation:Optional
	CascadeRenames bool `json:"cascadeRenames,omitempty"`
}

// HumioViewStatus defines the observed state of HumioView.
type HumioViewStatus struct {
	// State is deprecated (use Conditions instead). Will be removed in a future release. Reflects the current state of the HumioView
	// +kubebuilder:validation:Optional
	State string `json:"state,omitempty"`
	// Conditions represent the latest available observations of the resource's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// LastSyncedName is the last name successfully synced with LogScale
	// Used to detect renames
	// +optional
	LastSyncedName string `json:"lastSyncedName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioviews,scope=Namespaced
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the view"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio View"

// HumioView is the Schema for the humioviews API.
type HumioView struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioViewSpec   `json:"spec"`
	Status HumioViewStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioViewList contains a list of HumioView.
type HumioViewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioView `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioView{}, &HumioViewList{})
}

// GetViewConnections returns the HumioView in the same format as we can fetch from GraphQL so that we can compare
// the custom resource HumioView with humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection.
func (hv *HumioView) GetViewConnections() []humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection {
	viewConnections := make([]humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection, 0)
	for _, connection := range hv.Spec.Connections {
		viewConnections = append(viewConnections, humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection{
			Repository: humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnectionRepository{
				Name: connection.RepositoryName,
			},
			Filter: connection.Filter,
		})
	}
	return viewConnections
}
