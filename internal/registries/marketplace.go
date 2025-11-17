package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type MarketplaceRegistryClient struct {
	RegistryType string
	Enabled      bool
	Name         string
	Config       *humiov1alpha1.RegistryConnectionMarketplace
	HTTPClient   HTTPClientInterface
	K8SClient    client.Client
	Namespace    string
	Logger       logr.Logger
}

type MarketplacePackageVersionList struct {
	Version         string `json:"version"`
	MinHumioVersion string `json:"minHumioVersion"`
}

func (m *MarketplaceRegistryClient) GetRegistryType() string {
	return m.RegistryType
}

func (m *MarketplaceRegistryClient) IsEnabled() bool {
	return m.Enabled
}

func (m *MarketplaceRegistryClient) GetName() string {
	return m.Name
}

func (m *MarketplaceRegistryClient) CheckConnection(ctx context.Context) error {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.APICall)
	defer cancel()

	url := m.Config.URL

	resp, err := m.HTTPClient.GetWithContext(ctx, url)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			m.Logger.Error(err, "could not close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func (m *MarketplaceRegistryClient) CheckPackageExists(humioPackage *humiov1alpha1.HumioPackage) bool {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeouts.APICall)
	defer cancel()

	baseUrl := m.Config.URL
	checkPath, err := helpers.SafeURLJoin(baseUrl, fmt.Sprintf("/packages/%s/versions", humioPackage.Spec.GetPackageName()))
	if err != nil {
		m.Logger.Error(err, "could not build check path", "baseUrl", baseUrl, "packageName", humioPackage.Spec.GetPackageName())
		return false
	}
	// store of HTTP response
	versionResponse := []MarketplacePackageVersionList{}
	resp, err := m.HTTPClient.GetWithContext(ctx, checkPath)
	if err != nil {
		m.Logger.Error(err, "error during HTTP request", "checkPath", checkPath)
		return false
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			m.Logger.Error(err, "could not close response body")
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		m.Logger.Error(err, "error reading response body", "checkPath", checkPath)
		return false
	}

	if err := json.Unmarshal(body, &versionResponse); err != nil {
		m.Logger.Error(err, "error parsing JSON response", "checkPath", checkPath, "responseBody", string(body))
		return false
	}

	for _, version := range versionResponse {
		if version.Version == humioPackage.Spec.PackageVersion {
			return true
		}
	}
	return false
}

func (m *MarketplaceRegistryClient) DownloadPackage(ctx context.Context, humioPackage *humiov1alpha1.HumioPackage, downloadPath string) error {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download cancelled: %w", err)
	}

	// Create timeout context for download - use longer timeout for downloads
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.Download)
	defer cancel()

	baseUrl := m.Config.URL
	packageName := humioPackage.Spec.GetPackageName()
	downloadURL, err := helpers.SafeURLJoin(baseUrl, fmt.Sprintf("/packages/%s/%s/download", packageName, humioPackage.Spec.PackageVersion))
	if err != nil {
		return fmt.Errorf("failed to build download URL: %w", err)
	}

	// Make HTTP request with context
	resp, err := m.HTTPClient.GetWithContext(ctx, downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download package: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			m.Logger.Error(err, "could not close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	cleanPath, err := validateAndCleanPath(downloadPath)
	if err != nil {
		return err
	}

	// Create the output file
	outFile, err := os.Create(cleanPath) // #nosec G304 - path is validated and constructed internally
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() {
		if err := outFile.Close(); err != nil {
			m.Logger.Error(err, "could not close output file")
		}
	}()

	// Stream the response body to the file with context awareness
	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write package to file: %w", err)
	}

	return nil
}

func (m *MarketplaceRegistryClient) ValidateConfig(ctx context.Context) error {
	return nil
}
