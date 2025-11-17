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
	// HumioPackageRegistryStateUnknown is the Unknown state of the registry
	HumioPackageRegistryStateUnknown = "Unknown"
	// HumioPackageRegistryStateExists is the Active state of the registry
	HumioPackageRegistryStateExists = "Active"
	// HumioPackageRegistryStateDisabled is the Disabled state of the registry
	HumioPackageRegistryStateDisabled = "Disabled"
	// HumioPackageRegistryStateConfigError is the state of the package registry when user-provided specification results in configuration error
	HumioPackageRegistryStateConfigError = "ConfigError"
)

// HumioPackageRegistrySpec defines the desired state of HumioPackageRegistry
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
// +kubebuilder:validation:XValidation:rule="(self.registryType == 'marketplace') ? has(self.marketplace) : true",message="marketplace is required when registryType is 'marketplace'"
// +kubebuilder:validation:XValidation:rule="(self.registryType == 'gitlab') ? has(self.gitlab) : true",message="gitlab is required when registryType is 'gitlab'"
// +kubebuilder:validation:XValidation:rule="(self.registryType == 'github') ? has(self.github) : true",message="github is required when registryType is 'github'"
// +kubebuilder:validation:XValidation:rule="(self.registryType == 'artifactory') ? has(self.artifactory) : true",message="artifactory is required when registryType is 'artifactory'"
// +kubebuilder:validation:XValidation:rule="(self.registryType == 'aws') ? has(self.aws) : true",message="aws is required when registryType is 'aws'"
// +kubebuilder:validation:XValidation:rule="(self.registryType == 'gcloud') ? has(self.gcloud) : true",message="gcloud is required when registryType is 'gcloud'"
// +kubebuilder:validation:XValidation:rule="(self.registryType != 'marketplace') ? !has(self.marketplace) : true",message="marketplace should only be set when registryType is 'marketplace'"
// +kubebuilder:validation:XValidation:rule="(self.registryType != 'gitlab') ? !has(self.gitlab) : true",message="gitlab should only be set when registryType is 'gitlab'"
// +kubebuilder:validation:XValidation:rule="(self.registryType != 'github') ? !has(self.github) : true",message="github should only be set when registryType is 'github'"
// +kubebuilder:validation:XValidation:rule="(self.registryType != 'artifactory') ? !has(self.artifactory) : true",message="artifactory should only be set when registryType is 'artifactory'"
// +kubebuilder:validation:XValidation:rule="(self.registryType != 'aws') ? !has(self.aws) : true",message="aws should only be set when registryType is 'aws'"
// +kubebuilder:validation:XValidation:rule="(self.registryType != 'gcloud') ? !has(self.gcloud) : true",message="gcloud should only be set when registryType is 'gcloud'"
type HumioPackageRegistrySpec struct {
	// ManagedClusterName specifies the name of a HumioCluster resource managed by this operator.
	// Package registries configured with this field will be available to HumioPackage resources targeting this cluster.
	// Must specify exactly one of managedClusterName or externalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName specifies the name of a HumioExternalCluster resource representing an external LogScale cluster.
	// Package registries configured with this field will be available to HumioPackage resources targeting this external cluster.
	// Must specify exactly one of managedClusterName or externalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// DisplayName is a human-readable name for the registry shown in logs and status messages.
	// This is optional and can be used to distinguish between multiple registries of the same type.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Optional
	DisplayName string `json:"displayName"`
	// RegistryType specifies the type of package registry to configure.
	// Each type requires its corresponding configuration block (e.g., registryType: "gitlab" requires the gitlab field).
	// Supported types: marketplace, gitlab, github, artifactory, aws, gcloud.
	// +kubebuilder:validation:Enum=marketplace;gitlab;github;artifactory;aws;gcloud
	// +kubebuilder:validation:Required
	RegistryType string `json:"registryType"`
	// Enabled determines whether this registry is active and available for package installations.
	// When false, the registry will be ignored by HumioPackage resources. Defaults to true.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`
	// Marketplace contains connection configuration for the LogScale Marketplace (packages.humio.com).
	// Required when registryType is "marketplace". Must specify exactly one registry type configuration.
	// +optional
	Marketplace *RegistryConnectionMarketplace `json:"marketplace,omitempty"`
	// Gitlab contains connection configuration for GitLab Package Registry.
	// Required when registryType is "gitlab". Must specify exactly one registry type configuration.
	// +optional
	Gitlab *RegistryConnectionGitlab `json:"gitlab,omitempty"`
	// Github contains connection configuration for GitHub Releases.
	// Required when registryType is "github". Must specify exactly one registry type configuration.
	// +optional
	Github *RegistryConnectionGithub `json:"github,omitempty"`
	// Artifactory contains connection configuration for JFrog Artifactory generic repositories.
	// Required when registryType is "artifactory". Must specify exactly one registry type configuration.
	// +optional
	Artifactory *RegistryConnectionArtifactory `json:"artifactory,omitempty"`
	// Aws contains connection configuration for AWS CodeArtifact.
	// Required when registryType is "aws". Must specify exactly one registry type configuration.
	// +optional
	Aws *RegistryConnectionAws `json:"aws,omitempty"`
	// Gcloud contains connection configuration for Google Cloud Artifact Registry.
	// Required when registryType is "gcloud". Must specify exactly one registry type configuration.
	// +optional
	Gcloud *RegistryConnectionGcloud `json:"gcloud,omitempty"`
}

// RegistryConnectionMarketplace contains connection configuration for the LogScale Marketplace.
// The marketplace is the official package repository hosted by LogScale at packages.humio.com.
type RegistryConnectionMarketplace struct {
	// URL is the LogScale Marketplace REST API endpoint.
	// Defaults to "https://packages.humio.com" (the official marketplace). Custom marketplace instances can override this.
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:default="https://packages.humio.com"
	// +kubebuilder:validation:Required
	URL string `json:"url"`
}

// RegistryConnectionGitlab contains connection configuration for GitLab Package Registry.
// GitLab's Package Registry supports generic packages with project-scoped access control.
type RegistryConnectionGitlab struct {
	// URL is the GitLab API base URL including the API version (e.g., "https://gitlab.example.com/api/v4").
	// For GitLab.com, use "https://gitlab.com/api/v4". For self-hosted instances, adjust the domain accordingly.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:validation:Required
	URL string `json:"url"`
	// TokenRef contains a reference to a Kubernetes secret with GitLab authentication credentials.
	// The token should have at least read access to the specified project's package registry.
	// +kubebuilder:validation:Required
	TokenRef SecretKeyRef `json:"tokenRef"`
	// Project is the GitLab project identifier where packages are stored.
	// This can be either the project path (e.g., "group/project") or the numeric project ID.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Project string `json:"project"`
}

// RegistryConnectionGithub contains connection configuration for GitHub Releases.
// GitHub Releases allows packages to be distributed either as release assets or source code archives.
type RegistryConnectionGithub struct {
	// URL is the GitHub API base URL (e.g., "https://api.github.com").
	// For GitHub Enterprise Server, use your instance's API URL (e.g., "https://github.enterprise.com/api/v3").
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:validation:Required
	URL string `json:"url"`
	// TokenRef contains a reference to a Kubernetes secret with GitHub authentication credentials.
	// For public repositories, a classic personal access token with no scopes is sufficient.
	// For private repositories, the token needs the "repo" scope.
	// +kubebuilder:validation:Required
	TokenRef SecretKeyRef `json:"tokenRef"`
	// Owner is the GitHub username or organization name that owns the repositories.
	// This will be combined with repository names from HumioPackage specs to form the full repository path.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
}

// RegistryConnectionArtifactory contains connection configuration for JFrog Artifactory generic repositories.
// Artifactory organizes packages in repositories with flexible path structures.
type RegistryConnectionArtifactory struct {
	// URL is the Artifactory base URL (e.g., "https://mycompany.jfrog.io/artifactory").
	// This should be the base Artifactory URL without the repository path.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:validation:Required
	URL string `json:"url"`
	// Repository is the name of the generic repository in Artifactory where packages are stored.
	// This repository must be configured to allow generic package types.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Repository string `json:"repository"`
	// TokenRef contains a reference to a Kubernetes secret with Artifactory authentication credentials.
	// The token should have read access to the specified repository.
	// +kubebuilder:validation:Required
	TokenRef SecretKeyRef `json:"tokenRef"`
}

// RegistryConnectionAws contains connection configuration for AWS CodeArtifact.
// AWS CodeArtifact is a managed package repository service that supports generic packages.
type RegistryConnectionAws struct {
	// Region is the AWS region where the CodeArtifact domain and repository are located (e.g., "us-east-1").
	// This must match the region where your CodeArtifact resources were created.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Required
	Region string `json:"region"`
	// Domain is the name of the CodeArtifact domain that contains the repository.
	// Domains provide a way to organize repositories and control access across them.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Domain string `json:"domain"`
	// Repository is the name of the CodeArtifact repository within the domain where packages are stored.
	// The repository must be configured to support generic package format.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Repository string `json:"repository"`
	// AccessKeyRef contains a reference to a Kubernetes secret with the AWS access key ID.
	// This access key must have permissions to read from the specified CodeArtifact repository.
	// +kubebuilder:validation:Required
	AccessKeyRef SecretKeyRef `json:"accessKeyRef"`
	// AccessSecretRef contains a reference to a Kubernetes secret with the AWS secret access key.
	// This secret key corresponds to the access key and is used for AWS Signature V4 authentication.
	// +kubebuilder:validation:Required
	AccessSecretRef SecretKeyRef `json:"accessSecretRef"`
}

// RegistryConnectionGcloud contains connection configuration for Google Cloud Artifact Registry.
// Google Cloud Artifact Registry is a managed package repository service that supports generic packages.
type RegistryConnectionGcloud struct {
	// URL is the Google Cloud Artifact Registry API base URL.
	// Defaults to "https://artifactregistry.googleapis.com" (the standard API endpoint).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:default="https://artifactregistry.googleapis.com"
	URL string `json:"url"`
	// ProjectID is the Google Cloud project ID where the Artifact Registry repository is located.
	// This is the unique identifier for your GCP project (e.g., "my-project-123").
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	ProjectID string `json:"projectId"`
	// Repository is the name of the Artifact Registry repository where packages are stored.
	// The repository must be configured to support generic package format.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Repository string `json:"repository"`
	// Location is the Google Cloud region or multi-region where the repository is located.
	// Examples: "us-central1" (region), "us" (multi-region). This must match the repository's actual location.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Location string `json:"location"`
	// ServiceAccountKeyRef contains a reference to a Kubernetes secret with the GCP service account JSON key.
	// The service account must have the "Artifact Registry Reader" role or equivalent permissions.
	// +kubebuilder:validation:Required
	ServiceAccountKeyRef SecretKeyRef `json:"serviceAccountKeyRef"`
}

// SecretKeyRef references a specific key within a Kubernetes secret.
// This allows secure storage of sensitive configuration like API tokens, passwords, or service account keys.
type SecretKeyRef struct {
	// Name is the name of the Kubernetes secret containing the required data.
	// The secret must exist in the same namespace as the HumioPackageRegistry (or in the namespace specified below).
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Key is the specific key within the secret data that contains the required value.
	// For example, "token" for API tokens, "password" for passwords, or "service-account.json" for GCP service accounts.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Key string `json:"key"`
	// Namespace is the namespace where the secret is located (optional).
	// If not specified, defaults to the same namespace as the HumioPackageRegistry resource.
	// This allows referencing secrets from other namespaces when appropriate RBAC permissions exist.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// HumioPackageRegistryStatus defines the observed state of HumioPackageRegistry.
// This reflects the current operational status as determined by the controller.
type HumioPackageRegistryStatus struct {
	// State reflects the current operational state of the package registry.
	// Possible values: "Unknown", "Active", "Disabled", "ConfigError".
	// "Active" means the registry is successfully configured and available for use.
	State string `json:"state,omitempty"`
	// Message provides additional details about the current state.
	// This may contain error messages, connection status, or other diagnostic information from the controller.
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiopackageregistries,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the package registry"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message",description="The last state message detected by the reconciler"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Package Registry"

// HumioPackageRegistry is the Schema for the humiopackageregistries API
type HumioPackageRegistry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"` //nolint:modernize // Required for Kubernetes API

	// +kubebuilder:validation:Required
	Spec   HumioPackageRegistrySpec   `json:"spec"`
	Status HumioPackageRegistryStatus `json:"status,omitempty"` //nolint:modernize // Required for Kubernetes API
}

// +kubebuilder:object:root=true

// HumioPackageRegistryList contains a list of HumioPackageRegistry
type HumioPackageRegistryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"` //nolint:modernize // Required for Kubernetes API
	Items           []HumioPackageRegistry      `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioPackageRegistry{}, &HumioPackageRegistryList{})
}
