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
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// HumioPackageStateUnknown is the Unknown state of the package
	HumioPackageStateUnknown = "Unknown"
	// HumioPackageStateExists is the Installed state of the package
	HumioPackageStateExists = "Installed"
	// HumioPackageStateFailed is the Failed installation state of the package
	HumioPackageStateFailed = "FailedInstall"
	// HumioPackageStatePartialFailed is the Partially installed state of the package
	HumioPackageStatePartialFailed = "PartialInstall"
	// HumioPackageStateNotFound is the state of the package when the requested package is not found in the registry
	HumioPackageStateNotFound = "NotFound"
	// HumioPackageStateConfigError is the state of the package when user-provided specification results in configuration error
	HumioPackageStateConfigError = "ConfigError"
)

// HumioPackageSpec defines the desired state of HumioPackage
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
// +kubebuilder:validation:XValidation:rule="[has(self.marketplace) && self.marketplace != null, has(self.gitlab) && self.gitlab != null, has(self.github) && self.github != null, has(self.aws) && self.aws != null, has(self.artifactory) && self.artifactory != null, has(self.gcloud) && self.gcloud != null].filter(x, x).size() == 1",message="Must specify exactly one of marketplace, gitlab, github, aws, artifactory, or gcloud configuration"
type HumioPackageSpec struct {
	// ManagedClusterName specifies the name of a HumioCluster resource managed by this operator.
	// The package will be installed into this cluster. Must specify exactly one of managedClusterName or externalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName specifies the name of a HumioExternalCluster resource representing an external LogScale cluster.
	// The package will be installed into this external cluster. Must specify exactly one of managedClusterName or externalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// PackageName is the name of the LogScale package to install (e.g., "crowdstrike/fdr").
	// This corresponds to the package name field in the package's manifest.yaml file.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	PackageName string `json:"packageName"`
	// PackageVersion is the specific version of the package to install (e.g., "1.2.3").
	// This must match exactly with the version field in the package's manifest.yaml file.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	PackageVersion string `json:"packageVersion"`
	// PackageChecksum is the SHA256 checksum of the package file for integrity verification.
	// Format: "sha256:<64-character-hex-string>". This ensures the downloaded package hasn't been tampered with.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	PackageChecksum string `json:"packageChecksum"`
	// RegistryRef references the HumioPackageRegistry resource that contains authentication and connection details
	// for the package registry where this package should be downloaded from.
	// +kubebuilder:validation:Required
	RegistryRef RegistryReference `json:"registryRef"`
	// PackageInstallTargets specifies the LogScale views/repositories where this package should be installed.
	// Each target can reference views by name directly or reference HumioView Kubernetes resources.
	// At least one target must be specified.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Required
	PackageInstallTargets []PackageInstallTarget `json:"packageInstallTargets"`
	// ConflictPolicy determines how to handle conflicts when package components already exist in the target views.
	// "error": fail installation if conflicts are detected. "overwrite"(default): replace existing components.
	// +kubebuilder:validation:Enum=error;overwrite
	// +kubebuilder:default=overwrite
	ConflictPolicy string `json:"conflictPolicy,omitempty"`
	// Marketplace contains configuration specific to LogScale Marketplace packages.
	// Required when using the marketplace registry. Must specify exactly one registry type configuration.
	// +optional
	Marketplace *MarketplacePackageInfo `json:"marketplace,omitempty"`
	// Gitlab contains configuration specific to GitLab Package Registry packages.
	// Required when using a GitLab registry. Must specify exactly one registry type configuration.
	// +optional
	Gitlab *GitlabPackageInfo `json:"gitlab,omitempty"`
	// Github contains configuration specific to GitHub Releases packages.
	// Required when using a GitHub registry. Must specify exactly one registry type configuration.
	// +optional
	Github *GithubPackageInfo `json:"github,omitempty"`
	// Aws contains configuration specific to AWS CodeArtifact generic packages.
	// Required when using an AWS registry. Must specify exactly one registry type configuration.
	// +optional
	Aws *AwsPackageInfo `json:"aws,omitempty"`
	// Artifactory contains configuration specific to JFrog Artifactory generic repository packages.
	// Required when using an Artifactory registry. Must specify exactly one registry type configuration.
	// +optional
	Artifactory *ArtifactoryPackageInfo `json:"artifactory,omitempty"`
	// Gcloud contains configuration specific to Google Cloud Artifact Registry generic packages.
	// Required when using a Google Cloud registry. Must specify exactly one registry type configuration.
	// +optional
	Gcloud *GcloudPackageInfo `json:"gcloud,omitempty"`
}

// MarketplacePackageInfo contains configuration for LogScale Marketplace packages.
// The marketplace uses a two-part naming scheme: scope/package (e.g., "crowdstrike/fdr").
type MarketplacePackageInfo struct {
	// Scope is the marketplace namespace or organization name (e.g., "crowdstrike").
	// This corresponds to the first part of the package identifier in the marketplace.
	// +kubebuilder:validation:Required
	Scope string `json:"scope"`
	// Package is the package name within the scope (e.g., "fdr").
	// This corresponds to the second part of the package identifier in the marketplace.
	// +kubebuilder:validation:Required
	Package string `json:"package"`
}

// GitlabPackageInfo contains configuration for GitLab Package Registry generic packages.
// GitLab uses a project-based package registry with version-specific asset names.
type GitlabPackageInfo struct {
	// Package is the GitLab generic package name as stored in the Package Registry.
	// This should match the package name used when publishing to GitLab Package Registry.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Package string `json:"package"`
	// AssetName is the specific file name within the package version to download and install.
	// This corresponds to the actual file uploaded to the GitLab Package Registry (e.g., "mypackage-1.0.0.zip").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	AssetName string `json:"assetName"`
}

// GithubPackageInfo contains configuration for GitHub Releases packages.
// GitHub packages can be downloaded either from specific release assets or as source code archives.
type GithubPackageInfo struct {
	// Repository is the GitHub repository name within the configured owner/organization.
	// This should be just the repository name, not the full path (e.g., "my-package", not "owner/my-package").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Repository string `json:"repository"`
	// Tag is the GitHub release tag to download from (e.g., "v1.2.3" or "1.2.3").
	// This must match an existing release tag in the repository.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Tag string `json:"tag"`
	// AssetName is the specific release asset filename to download (optional).
	// If not specified, the release's source code archive (zipball) will be downloaded instead.
	// Use this when the package is distributed as a specific file attachment to the release.
	// +kubebuilder:validation:Optional
	AssetName string `json:"assetName,omitempty"`
}

// AwsPackageInfo contains configuration for AWS CodeArtifact generic packages.
// AWS CodeArtifact organizes packages using domain/repository/namespace/package structure.
type AwsPackageInfo struct {
	// Namespace is the AWS CodeArtifact namespace for generic packages.
	// This provides an additional level of organization within the repository (e.g., "com.example").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`
	// Package is the generic package name within the namespace.
	// This should match the package name as stored in CodeArtifact (e.g., "my-logscale-package").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Package string `json:"package"`
	// Filename is the specific asset filename within the package version to download.
	// This corresponds to the actual file uploaded to CodeArtifact (e.g., "package-1.0.0.zip").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Filename string `json:"filename"`
}

// ArtifactoryPackageInfo contains configuration for JFrog Artifactory generic repository packages.
// Artifactory uses a simple file path structure within repositories.
type ArtifactoryPackageInfo struct {
	// FilePath is the complete path to the artifact file within the Artifactory repository.
	// This should include any folder structure and the filename (e.g., "packages/mypackage/1.0.0/mypackage-1.0.0.zip").
	// The path is relative to the repository root configured in the registry.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	FilePath string `json:"filePath"`
}

// GcloudPackageInfo contains configuration for Google Cloud Artifact Registry generic packages.
// Google Cloud Artifact Registry organizes generic packages with package names and version-specific files.
type GcloudPackageInfo struct {
	// Package is the generic package name as stored in Google Cloud Artifact Registry.
	// This should match the package name used when uploading to Artifact Registry (e.g., "my-logscale-package").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Package string `json:"package"`
	// Filename is the specific file within the package version to download.
	// This corresponds to the actual file uploaded to Artifact Registry (e.g., "package-1.0.0.zip").
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Filename string `json:"filename"`
}

// PackageInstallTarget represents a target LogScale view or repository where a package should be installed.
// It supports two methods of specifying targets: direct view names or references to HumioView CRDs.
// Must specify exactly one of viewNames or viewRef.
// +kubebuilder:validation:XValidation:rule="(has(self.viewNames) && size(self.viewNames) > 0) != (has(self.viewRef) && self.viewRef != null)",message="Must specify exactly one of viewNames or viewRef"
type PackageInstallTarget struct {
	// ViewNames is a list of LogScale view names where the package should be installed.
	// These are direct string references to existing views in the LogScale cluster.
	// Use this when you manage views directly in LogScale rather than through Kubernetes CRDs.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	ViewNames []string `json:"viewNames,omitempty"`
	// ViewRef is a reference to a HumioView Kubernetes resource.
	// The package will be installed into the LogScale view managed by this HumioView CRD.
	// Use this when you manage views through Kubernetes HumioView resources.
	// +kubebuilder:validation:Optional
	ViewRef *HumioViewReference `json:"viewRef,omitempty"`
}

// HumioViewReference represents a reference to a HumioView Kubernetes resource.
// This allows packages to target views that are managed as Kubernetes custom resources.
type HumioViewReference struct {
	// Name is the name of the HumioView Kubernetes resource.
	// The referenced HumioView must exist and be successfully reconciled.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Namespace is the namespace where the HumioView resource is located (optional).
	// If not specified, defaults to the same namespace as the HumioPackage resource.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// RegistryReference references a HumioPackageRegistry resource for package installation.
// This connects HumioPackage resources to their configured package registries.
type RegistryReference struct {
	// Name is the name of the HumioPackageRegistry Kubernetes resource.
	// The referenced registry must exist, be enabled, and have valid configuration.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Namespace is the namespace where the HumioPackageRegistry resource is located (optional).
	// If not specified, defaults to the same namespace as the HumioPackage resource.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// GetPackageName returns the package name
func (spec *HumioPackageSpec) GetPackageName() string {
	return spec.PackageName
}

// ResolveInstallTargets resolves PackageInstallTargets to actual view names
// For ViewNames targets, returns the names directly
// For ViewRef targets, looks up the HumioView CRD and returns its view name
func (spec *HumioPackageSpec) ResolveInstallTargets(ctx context.Context, client client.Client, namespace string) ([]string, error) {
	var viewNames []string

	for _, target := range spec.PackageInstallTargets {
		if len(target.ViewNames) > 0 {
			// Direct view names reference
			viewNames = append(viewNames, target.ViewNames...)
		} else if target.ViewRef != nil {
			// HumioView CRD reference - need to look it up
			viewNamespace := namespace
			if target.ViewRef.Namespace != "" {
				viewNamespace = target.ViewRef.Namespace
			}
			humioView := &HumioView{}
			err := client.Get(ctx, types.NamespacedName{
				Name:      target.ViewRef.Name,
				Namespace: viewNamespace,
			}, humioView)
			if err != nil {
				return nil, fmt.Errorf("failed to get HumioView %s/%s: %w", viewNamespace, target.ViewRef.Name, err)
			}

			// Extract the view name from the HumioView spec
			viewNames = append(viewNames, humioView.Spec.Name)
		}
	}

	return viewNames, nil
}

// HumioPackageStatus defines the observed state of HumioPackage.
type HumioPackageStatus struct {
	// State reflects the current state of the HumioPackage
	State string `json:"state,omitempty"`
	// Message displays the last detected state message set via the reconciler
	Message string `json:"message,omitempty"`
	// HumioPackageName displays the Humio package name installed extracted from manifest.yaml
	HumioPackageName string `json:"humioPackageName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiopackages,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="The state of the package"
// +kubebuilder:printcolumn:name="HumioPackageName",type="string",JSONPath=".status.humioPackageName",description="The package name as installed in Humio"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message",description="The last state message detected by the reconciler"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Package"

// HumioPackage is the Schema for the humiopackages API
type HumioPackage struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"` //nolint:modernize // Required for Kubernetes API

	// +kubebuilder:validation:Required
	Spec   HumioPackageSpec   `json:"spec"`
	Status HumioPackageStatus `json:"status,omitempty"` //nolint:modernize // Required for Kubernetes API
}

// +kubebuilder:object:root=true

// HumioPackageList contains a list of HumioPackage
type HumioPackageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioPackage `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioPackage{}, &HumioPackageList{})
}
