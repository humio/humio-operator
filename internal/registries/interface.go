package registries

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RegistryClientInterface common interface for all PackageRegistryClients
type RegistryClientInterface interface {
	GetRegistryType() string
	IsEnabled() bool
	GetName() string
	CheckConnection(context.Context) error
	CheckPackageExists(*humiov1alpha1.HumioPackage) bool
	DownloadPackage(ctx context.Context, humioPackage *humiov1alpha1.HumioPackage, downloadPath string) error
	ValidateConfig(context.Context) error
}

// NewPackageRegistryClientWithHTTPClient allows injection of various dependencies
func NewPackageRegistryClient(hpr *humiov1alpha1.HumioPackageRegistry, httpClient HTTPClientInterface, k8sClient client.Client, namespace string, logger logr.Logger) (RegistryClientInterface, error) {
	var client RegistryClientInterface
	var err error

	switch hpr.Spec.RegistryType {
	case "marketplace":
		client = &MarketplaceRegistryClient{
			RegistryType: hpr.Spec.RegistryType,
			Enabled:      hpr.Spec.Enabled,
			Name:         hpr.Spec.DisplayName,
			Config:       hpr.Spec.Marketplace,
			HTTPClient:   httpClient,
			K8SClient:    nil,
			Namespace:    namespace,
			Logger:       logger,
		}
	case "gitlab":
		client = &GitlabRegistryClient{
			RegistryType: hpr.Spec.RegistryType,
			Enabled:      hpr.Spec.Enabled,
			Name:         hpr.Spec.DisplayName,
			Config:       hpr.Spec.Gitlab,
			HTTPClient:   httpClient,
			K8SClient:    k8sClient,
			Namespace:    namespace,
			Logger:       logger,
		}
	case "github":
		client = &GithubRegistryClient{
			RegistryType: hpr.Spec.RegistryType,
			Enabled:      hpr.Spec.Enabled,
			Name:         hpr.Spec.DisplayName,
			Config:       hpr.Spec.Github,
			HTTPClient:   httpClient,
			K8SClient:    k8sClient,
			Namespace:    namespace,
			Logger:       logger,
		}
	case "aws":
		client = &AwsRegistryClient{
			RegistryType: hpr.Spec.RegistryType,
			Enabled:      hpr.Spec.Enabled,
			Name:         hpr.Spec.DisplayName,
			Config:       hpr.Spec.Aws,
			HTTPClient:   httpClient,
			K8SClient:    k8sClient,
			Namespace:    namespace,
			Logger:       logger,
		}
	case "artifactory":
		client = &ArtifactoryRegistryClient{
			RegistryType: hpr.Spec.RegistryType,
			Enabled:      hpr.Spec.Enabled,
			Name:         hpr.Spec.DisplayName,
			Config:       hpr.Spec.Artifactory,
			HTTPClient:   httpClient,
			K8SClient:    k8sClient,
			Namespace:    namespace,
			Logger:       logger,
		}
	case "gcloud":
		client = &GcloudRegistryClient{
			RegistryType: hpr.Spec.RegistryType,
			Enabled:      hpr.Spec.Enabled,
			Name:         hpr.Spec.DisplayName,
			Config:       hpr.Spec.Gcloud,
			HTTPClient:   httpClient,
			K8SClient:    k8sClient,
			Namespace:    namespace,
			Logger:       logger,
		}
	default:
		return nil, fmt.Errorf("unsupported registry type: %s", hpr.Spec.RegistryType)
	}

	if !client.IsEnabled() {
		return nil, fmt.Errorf("PackageRegistryClient is not enabled '%v'", client.GetName())
	}

	err = client.ValidateConfig(context.TODO())
	if err != nil {
		return nil, err
	}

	return client, err
}
