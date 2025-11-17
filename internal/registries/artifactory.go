package registries

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ArtifactoryRegistryClient struct {
	RegistryType string
	Enabled      bool
	Name         string
	Config       *humiov1alpha1.RegistryConnectionArtifactory
	HTTPClient   HTTPClientInterface
	K8SClient    client.Client
	Namespace    string
	Logger       logr.Logger
}

func (a *ArtifactoryRegistryClient) GetRegistryType() string {
	return a.RegistryType
}

func (a *ArtifactoryRegistryClient) IsEnabled() bool {
	return a.Enabled
}

func (a *ArtifactoryRegistryClient) GetName() string {
	return a.Name
}

func (a *ArtifactoryRegistryClient) CheckConnection(ctx context.Context) error {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.APICall)
	defer cancel()

	token, err := a.getSecretValue(ctx)
	if err != nil {
		return fmt.Errorf("error retrieving auth credentials: %w", err)
	}

	// Build Artifactory API endpoint for system ping
	endpoint, err := helpers.SafeURLJoin(a.Config.URL, "/api/system/ping")
	if err != nil {
		return fmt.Errorf("error building API endpoint: %w", err)
	}

	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
		"Content-Type":  "application/json",
	}
	resp, err := a.HTTPClient.GetWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		return fmt.Errorf("error during CheckConnection: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			a.Logger.Error(err, "could not close response body")
		}
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("authentication failed: invalid or expired Artifactory token")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("artifactory API returned status %d: %s", resp.StatusCode, resp.Status)
	}

	return nil
}

func (a *ArtifactoryRegistryClient) CheckPackageExists(humioPackage *humiov1alpha1.HumioPackage) bool {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeouts.APICall)
	defer cancel()

	token, err := a.getSecretValue(ctx)
	if err != nil {
		return false
	}

	// Build Artifactory artifact URL Format: /{repository}/{filePath}
	artifactPath := fmt.Sprintf("/%s/%s", a.Config.Repository, humioPackage.Spec.Artifactory.FilePath)
	endpoint, err := helpers.SafeURLJoin(a.Config.URL, artifactPath)
	if err != nil {
		return false
	}

	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
	}
	// Use GET with context instead of HEAD to ensure context-aware timeout handling
	resp, err := a.HTTPClient.GetWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			a.Logger.Error(err, "could not close response body")
		}
	}()

	return resp.StatusCode == http.StatusOK
}

func (a *ArtifactoryRegistryClient) DownloadPackage(ctx context.Context, humioPackage *humiov1alpha1.HumioPackage, downloadPath string) error {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download cancelled: %w", err)
	}

	// Create timeout context for download - use longer timeout for downloads
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.Download)
	defer cancel()

	// Get token for authentication
	token, err := a.getSecretValue(ctx)
	if err != nil {
		return fmt.Errorf("error retrieving auth credentials: %w", err)
	}

	// Build Artifactory artifact download URL Format: /{repository}/{filePath}
	artifactPath := fmt.Sprintf("/%s/%s", a.Config.Repository, humioPackage.Spec.Artifactory.FilePath)

	endpoint, err := helpers.SafeURLJoin(a.Config.URL, artifactPath)
	if err != nil {
		return fmt.Errorf("error building download URL: %w", err)
	}

	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
	}

	resp, err := a.HTTPClient.GetWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		return fmt.Errorf("failed to download package: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			a.Logger.Error(err, "could not close response body")
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
			a.Logger.Error(err, "could not close output file")
		}
	}()

	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write package to file: %w", err)
	}

	return nil
}

func (a *ArtifactoryRegistryClient) ValidateConfig(ctx context.Context) error {
	if a.Config == nil {
		return fmt.Errorf("artifactory registry config is nil")
	}
	if a.Config.URL == "" {
		return fmt.Errorf("URL field is required")
	}
	if a.Config.Repository == "" {
		return fmt.Errorf("repository field is required")
	}
	if a.Config.TokenRef.Name == "" {
		return fmt.Errorf("token secret name field is required")
	}
	if a.Config.TokenRef.Key == "" {
		return fmt.Errorf("token secret key field is required")
	}

	// Validate that we can retrieve the token
	_, err := a.getSecretValue(ctx)
	return err
}

func (a *ArtifactoryRegistryClient) getSecretValue(ctx context.Context) (string, error) {
	secretNamespace := a.Namespace
	if a.Config.TokenRef.Namespace != "" {
		secretNamespace = a.Config.TokenRef.Namespace
	}

	// Check if the secret exists
	secret, err := kubernetes.GetSecret(ctx, a.K8SClient, a.Config.TokenRef.Name, secretNamespace)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s in namespace %s: %w", a.Config.TokenRef.Name, secretNamespace, err)
	}

	// Check if the secret has the required key
	token, exists := secret.Data[a.Config.TokenRef.Key]
	if !exists {
		return "", fmt.Errorf("secret %s does not contain key %s", a.Config.TokenRef.Name, a.Config.TokenRef.Key)
	}

	return strings.TrimSpace(string(token)), nil
}
