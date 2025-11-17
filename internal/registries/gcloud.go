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
	"github.com/humio/humio-operator/internal/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GcloudRegistryClient struct {
	RegistryType string
	Enabled      bool
	Name         string
	Config       *humiov1alpha1.RegistryConnectionGcloud
	HTTPClient   HTTPClientInterface
	K8SClient    client.Client
	Namespace    string
	Logger       logr.Logger
}

func (g *GcloudRegistryClient) GetRegistryType() string {
	return g.RegistryType
}

func (g *GcloudRegistryClient) IsEnabled() bool {
	return g.Enabled
}

func (g *GcloudRegistryClient) GetName() string {
	return g.Name
}

func (g *GcloudRegistryClient) CheckConnection(ctx context.Context) error {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.APICall)
	defer cancel()

	// Get access token
	token, err := g.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("error getting access token: %w", err)
	}

	// Test connection by listing repositories
	projectLocationsPath := fmt.Sprintf("/v1/projects/%s/locations/%s", g.Config.ProjectID, g.Config.Location)
	endpoint, err := helpers.SafeURLJoin(g.Config.URL, projectLocationsPath)
	if err != nil {
		return fmt.Errorf("error building API endpoint: %w", err)
	}

	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
		"Content-Type":  "application/json",
	}

	resp, err := g.HTTPClient.GetWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		return fmt.Errorf("error during CheckConnection: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			g.Logger.Error(err, "could not close response body")
		}
	}()

	// Check for authentication errors
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("authentication failed: invalid or expired GCP service account")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GCP Artifact Registry API returned status %d: %s", resp.StatusCode, resp.Status)
	}

	return nil
}

func (g *GcloudRegistryClient) CheckPackageExists(humioPackage *humiov1alpha1.HumioPackage) bool {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeouts.APICall)
	defer cancel()

	// Get access token
	token, err := g.getAccessToken(ctx)
	if err != nil {
		return false
	}

	// Build Artifact Registry file URL for existence check
	// Format: /download/v1/projects/{project}/locations/{location}/repositories/{repository}/files/{packageName}:{version}:{filename}:download?alt=media
	artifactPath := fmt.Sprintf("/download/v1/projects/%s/locations/%s/repositories/%s/files/%s:%s:%s",
		g.Config.ProjectID,
		g.Config.Location,
		g.Config.Repository,
		humioPackage.Spec.Gcloud.Package,
		humioPackage.Spec.PackageVersion,
		humioPackage.Spec.Gcloud.Filename)

	endpoint, err := helpers.SafeURLJoin(g.Config.URL, artifactPath)
	endpoint = endpoint + ":download?alt=media"
	if err != nil {
		return false
	}

	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
		"Range":         "bytes=0-0",
	}

	// Make request with context to check if artifact exists
	resp, err := g.HTTPClient.GetWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			g.Logger.Error(err, "could not close response body")
		}
	}()

	// Return true only if we get a 206 status
	return resp.StatusCode == http.StatusPartialContent
}

func (g *GcloudRegistryClient) DownloadPackage(ctx context.Context, humioPackage *humiov1alpha1.HumioPackage, downloadPath string) error {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download cancelled: %w", err)
	}

	// Create timeout context for download - use longer timeout for downloads
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.Download)
	defer cancel()

	// Get access token
	token, err := g.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("error getting access token: %w", err)
	}

	// Build Artifact Registry download URL
	// Format: /download/v1/projects/{project}/locations/{location}/repositories/{repository}/files/{packageName}:{version}:{filename}:download?alt=media
	artifactPath := fmt.Sprintf("/download/v1/projects/%s/locations/%s/repositories/%s/files/%s:%s:%s",
		g.Config.ProjectID,
		g.Config.Location,
		g.Config.Repository,
		humioPackage.Spec.Gcloud.Package,
		humioPackage.Spec.PackageVersion,
		humioPackage.Spec.Gcloud.Filename)

	endpoint, err := helpers.SafeURLJoin(g.Config.URL, artifactPath)
	endpoint = endpoint + ":download?alt=media"
	if err != nil {
		return fmt.Errorf("error building download URL: %w", err)
	}

	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token),
	}

	resp, err := g.HTTPClient.GetWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		return fmt.Errorf("failed to download package: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			g.Logger.Error(err, "could not close response body")
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
			g.Logger.Error(err, "could not close output file")
		}
	}()

	// Stream the response body to the file
	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write package to file: %w", err)
	}

	return nil
}

func (g *GcloudRegistryClient) ValidateConfig(ctx context.Context) error {
	if g.Config == nil {
		return fmt.Errorf("gcloud registry config is nil")
	}
	if g.Config.URL == "" {
		return fmt.Errorf("URL field is required")
	}
	if g.Config.ProjectID == "" {
		return fmt.Errorf("projectId field is required")
	}
	if g.Config.Repository == "" {
		return fmt.Errorf("repository field is required")
	}
	if g.Config.Location == "" {
		return fmt.Errorf("location field is required")
	}
	if g.Config.ServiceAccountKeyRef.Name == "" {
		return fmt.Errorf("serviceAccountKey secret name field is required")
	}
	if g.Config.ServiceAccountKeyRef.Key == "" {
		return fmt.Errorf("serviceAccountKey secret key field is required")
	}

	// Validate that we can retrieve and parse the service account
	_, err := g.getServiceAccountKey(ctx)
	return err
}

func (g *GcloudRegistryClient) getServiceAccountKey(ctx context.Context) ([]byte, error) {
	// Determine which namespace to use for the secret
	secretNamespace := g.Namespace
	if g.Config.ServiceAccountKeyRef.Namespace != "" {
		secretNamespace = g.Config.ServiceAccountKeyRef.Namespace
	}

	// Check if the secret exists
	secret, err := kubernetes.GetSecret(ctx, g.K8SClient, g.Config.ServiceAccountKeyRef.Name, secretNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret %s in namespace %s: %w", g.Config.ServiceAccountKeyRef.Name, secretNamespace, err)
	}

	// Check if the secret has the required key
	keyData, exists := secret.Data[g.Config.ServiceAccountKeyRef.Key]
	if !exists {
		return nil, fmt.Errorf("secret %s does not contain key %s", g.Config.ServiceAccountKeyRef.Name, g.Config.ServiceAccountKeyRef.Key)
	}

	// Validate that it's valid JSON (service account key format)
	var temp any
	if err := json.Unmarshal(keyData, &temp); err != nil {
		return nil, fmt.Errorf("service account key is not valid JSON: %w", err)
	}

	return keyData, nil
}

func (g *GcloudRegistryClient) getAccessToken(ctx context.Context) (string, error) {
	// Get service account credentials from Kubernetes secret
	serviceAccountKey, err := g.getServiceAccountKey(ctx)
	if err != nil {
		return "", err
	}
	// Use HTTPClient to get access token (allows for mocking in tests)
	return g.HTTPClient.GetGcloudAccessToken(ctx, serviceAccountKey)
}
