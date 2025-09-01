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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioMultiClusterSearchViewConnectionTypeLocal indicates the HumioMultiClusterSearchViewConnection instance is a connection to a local repository or view.
	HumioMultiClusterSearchViewConnectionTypeLocal = "Local"
	// HumioMultiClusterSearchViewConnectionTypeRemote indicates the HumioMultiClusterSearchViewConnection instance is a connection to a repository or view on a remote cluster.
	HumioMultiClusterSearchViewConnectionTypeRemote = "Remote"
)

const (
	// HumioMultiClusterSearchViewStateUnknown is the Unknown state of the view
	HumioMultiClusterSearchViewStateUnknown = "Unknown"
	// HumioMultiClusterSearchViewStateExists is the Exists state of the view
	HumioMultiClusterSearchViewStateExists = "Exists"
	// HumioMultiClusterSearchViewStateNotFound is the NotFound state of the view
	HumioMultiClusterSearchViewStateNotFound = "NotFound"
	// HumioMultiClusterSearchViewStateConfigError is the state of the view when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioMultiClusterSearchViewStateConfigError = "ConfigError"
)

// HumioMultiClusterSearchViewConnectionTag represents a tag that will be applied to a connection.
type HumioMultiClusterSearchViewConnectionTag struct {
	// Key specifies the key of the tag
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=50
	// +kubebuilder:validation:XValidation:rule="self != 'clusteridentity'",message="The key 'clusteridentity' is reserved and cannot be used"
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// Value specifies the value of the tag
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=50
	// +kubebuilder:validation:Required
	Value string `json:"value"`
}

// HumioMultiClusterSearchViewConnectionAPITokenSpec points to the location of the LogScale API token to use for a remote connection
type HumioMultiClusterSearchViewConnectionAPITokenSpec struct {
	// SecretKeyRef specifies which key of a secret in the namespace of the HumioMultiClusterSearchView that holds the LogScale API token
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self != null && has(self.name) && self.name != \"\" && has(self.key) && self.key != \"\"",message="SecretKeyRef must have both name and key fields set"
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef"`
}

// HumioMultiClusterSearchViewConnection represents a connection to a specific repository with an optional filter
// +kubebuilder:validation:XValidation:rule="self.type == 'Local' ? has(self.viewOrRepoName) && !has(self.url) && !has(self.apiTokenSource) : true",message="When type is Local, viewOrRepoName must be set and url/apiTokenSource must not be set"
// +kubebuilder:validation:XValidation:rule="self.type == 'Remote' ? has(self.url) && has(self.apiTokenSource) && !has(self.viewOrRepoName) : true",message="When type is Remote, url/apiTokenSource must be set and viewOrRepoName must not be set"
type HumioMultiClusterSearchViewConnection struct {
	// ClusterIdentity is a required field that gets used as an identifier for the connection.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=50
	// +kubebuilder:validation:Required
	ClusterIdentity string `json:"clusterIdentity"`

	// Filter contains the prefix filter that will be applied to the connection.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=200
	Filter string `json:"filter,omitempty"`

	// Tags contains the key-value pair tags that will be applied to the connection.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=24
	// +kubebuilder:validation:XValidation:rule="size(self.map(c, c.key)) == size(self)",message="All tags must have unique keys"
	// +listType=map
	// +listMapKey=key
	Tags []HumioMultiClusterSearchViewConnectionTag `json:"tags,omitempty"`

	// Type specifies the type of connection.
	// If Type=Local, the connection will be to a local repository or view and requires the viewOrRepoName field to be set.
	// If Type=Remote, the connection will be to a remote repository or view and requires the fields remoteUrl and remoteSecretName to be set.
	// +kubebuilder:validation:Enum=Local;Remote
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	Type string `json:"type"`

	// ViewOrRepoName contains the name of the repository or view for the local connection.
	// Only used when Type=Local.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Optional
	ViewOrRepoName string `json:"viewOrRepoName,omitempty"`

	// Url contains the URL to use for the remote connection.
	// Only used when Type=Remote.
	// +kubebuilder:validation:MinLength=8
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Optional
	Url string `json:"url,omitempty"`

	// APITokenSource specifies where to fetch the LogScale API token to use for the remote connection.
	// Only used when Type=Remote.
	// +kubebuilder:validation:Optional
	APITokenSource *HumioMultiClusterSearchViewConnectionAPITokenSpec `json:"apiTokenSource,omitempty"`
}

// HumioMultiClusterSearchViewSpec defines the desired state of HumioMultiClusterSearchView.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
type HumioMultiClusterSearchViewSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Optional
	ManagedClusterName string `json:"managedClusterName,omitempty"`

	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Optional
	ExternalClusterName string `json:"externalClusterName,omitempty"`

	// Name is the name of the view inside Humio
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Description contains the description that will be set on the view
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=100
	Description string `json:"description,omitempty"`

	// Connections contains the connections to the Humio repositories which is accessible in this view
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self.filter(c, c.type == 'Local').size() <= 1",message="Only one connection can have type 'Local'"
	// +kubebuilder:validation:XValidation:rule="size(self.map(c, c.clusterIdentity)) == size(self)",message="All connections must have unique clusterIdentity values"
	// +listType=map
	// +listMapKey=clusterIdentity
	Connections []HumioMultiClusterSearchViewConnection `json:"connections,omitempty"`

	// AutomaticSearch is used to specify the start search automatically on loading the search page option.
	// +kubebuilder:validation:Optional
	AutomaticSearch *bool `json:"automaticSearch,omitempty"`
}

// HumioMultiClusterSearchViewStatus defines the observed state of HumioMultiClusterSearchView.
type HumioMultiClusterSearchViewStatus struct {
	// State reflects the current state of the HumioMultiClusterSearchView
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HumioMultiClusterSearchView is the Schema for the humiomulticlustersearchviews API.
type HumioMultiClusterSearchView struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioMultiClusterSearchViewSpec   `json:"spec,omitempty"`
	Status HumioMultiClusterSearchViewStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioMultiClusterSearchViewList contains a list of HumioMultiClusterSearchView.
type HumioMultiClusterSearchViewList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioMultiClusterSearchView `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioMultiClusterSearchView{}, &HumioMultiClusterSearchViewList{})
}
