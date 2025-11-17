package registries

import (
	"context"
	"encoding/json"
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

type GithubRegistryClient struct {
	RegistryType string
	Enabled      bool
	Name         string
	Config       *humiov1alpha1.RegistryConnectionGithub
	HTTPClient   HTTPClientInterface
	K8SClient    client.Client
	Namespace    string
	Logger       logr.Logger
}

// GitHub API response structures
type GithubReleaseAssets struct {
	TagName     string        `json:"tag_name"`
	Assets      []GithubAsset `json:"assets"`
	DownloadUrl string        `json:"zipball_url"`
}

type GithubAsset struct {
	Name               string `json:"name,omitempty"`
	BrowserDownloadURL string `json:"browser_download_url,omitempty"`
}

func (g *GithubRegistryClient) GetRegistryType() string {
	return g.RegistryType
}

func (g *GithubRegistryClient) IsEnabled() bool {
	return g.Enabled
}

func (g *GithubRegistryClient) GetName() string {
	return g.Name
}

func (g *GithubRegistryClient) CheckConnection(ctx context.Context) error {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.APICall)
	defer cancel()

	// Check connection by making a request to GitHub API
	endpoint, err := helpers.SafeURLJoin(g.Config.URL, "/")
	if err != nil {
		return fmt.Errorf("error building API endpoint: %w", err)
	}

	headers := map[string]string{
		"Accept": "application/vnd.github.v3+json",
	}

	// Add authentication
	token, err := g.getSecretValue(ctx)
	if err != nil {
		return fmt.Errorf("error retrieving auth credentials: %w", err)
	}
	if token != "" {
		headers["Authorization"] = fmt.Sprintf("token %s", token)
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
		return fmt.Errorf("authentication failed: invalid or expired GitHub token")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, resp.Status)
	}

	return nil
}

func (g *GithubRegistryClient) CheckPackageExists(humioPackage *humiov1alpha1.HumioPackage) bool {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeouts.APICall)
	defer cancel()

	path := fmt.Sprintf("/repos/%s/%s/releases/tags/%s",
		g.Config.Owner,
		humioPackage.Spec.Github.Repository,
		humioPackage.Spec.Github.Tag)

	endpoint, err := helpers.SafeURLJoin(g.Config.URL, path)
	if err != nil {
		g.Logger.Error(err, "error building API endpoint")
		return false
	}

	g.Logger.Info("CheckPackageExists: Making GitHub API request", "endpoint", endpoint, "path", path, "assetName", humioPackage.Spec.Github.AssetName)

	headers := map[string]string{
		"Accept": "application/vnd.github.v3+json",
	}

	token, err := g.getSecretValue(ctx)
	if err != nil || token == "" {
		g.Logger.Error(err, "failed to get GitHub token")
		return false
	}
	headers["Authorization"] = fmt.Sprintf("token %s", token)

	g.Logger.Info("CheckPackageExists: Making GitHub API request", "endpoint", endpoint, "assetName", humioPackage.Spec.Github.AssetName)

	resp, err := g.HTTPClient.GetWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		g.Logger.Error(err, "could not issue request", "endpoint", endpoint)
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			g.Logger.Error(err, "could not close response body")
		}
	}()

	g.Logger.Info("CheckPackageExists: Received GitHub API response", "statusCode", resp.StatusCode, "status", resp.Status, "assetName", humioPackage.Spec.Github.AssetName)

	if resp.StatusCode != http.StatusOK {
		g.Logger.Error(fmt.Errorf("status code: %v", resp.StatusCode), "CheckPackageExists: unexpected status code")
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		g.Logger.Error(err, "could not read response body")
		return false
	}

	var release GithubReleaseAssets
	if err := json.Unmarshal(body, &release); err != nil {
		g.Logger.Error(err, "CheckPackageExists: could not unmarshal response body", "body", string(body), "bodyLength", len(body), "assetName", humioPackage.Spec.Github.AssetName)
		return false
	}
	downloadUrl := release.DownloadUrl
	g.Logger.Info("CheckPackageExists: Processing release response", "downloadUrl", downloadUrl, "assetsCount", len(release.Assets), "assetName", humioPackage.Spec.Github.AssetName)
	// Check if the specified release asset exists
	if humioPackage.Spec.Github.AssetName != "" {
		// reset downloadUrl since we received an assetName
		downloadUrl = ""
		for _, asset := range release.Assets {
			if asset.Name == humioPackage.Spec.Github.AssetName {
				downloadUrl = asset.BrowserDownloadURL
				break
			}
		}
	}
	g.Logger.Info("CheckPackageExists: Final result", "downloadUrl", downloadUrl, "packageExists", downloadUrl != "", "assetName", humioPackage.Spec.Github.AssetName)
	return downloadUrl != ""
}

func (g *GithubRegistryClient) DownloadPackage(ctx context.Context, humioPackage *humiov1alpha1.HumioPackage, downloadPath string) error {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download cancelled: %w", err)
	}

	// Create timeout context for API call first
	apiCtx, apiCancel := context.WithTimeout(ctx, DefaultTimeouts.APICall)
	defer apiCancel()

	g.Logger.Info("DownloadPackage: Starting download", "downloadPath", downloadPath, "assetName", humioPackage.Spec.Github.AssetName)

	// First, get the release information to find the asset download URL
	path := fmt.Sprintf("/repos/%s/%s/releases/tags/%s",
		g.Config.Owner,
		humioPackage.Spec.Github.Repository,
		humioPackage.Spec.Github.Tag)

	endpoint, err := helpers.SafeURLJoin(g.Config.URL, path)
	if err != nil {
		return fmt.Errorf("error building API URL: %w", err)
	}

	g.Logger.Info("DownloadPackage: Making release info API request", "endpoint", endpoint, "assetName", humioPackage.Spec.Github.AssetName)

	headers := map[string]string{
		"Accept": "application/vnd.github.v3+json",
	}

	// Add authentication
	token, err := g.getSecretValue(apiCtx)
	if err != nil {
		return fmt.Errorf("error retrieving auth credentials: %w", err)
	}
	if token != "" {
		headers["Authorization"] = fmt.Sprintf("token %s", token)
	}

	resp, err := g.HTTPClient.GetWithHeadersAndContext(apiCtx, endpoint, headers)
	if err != nil {
		return fmt.Errorf("failed to get release information: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			g.Logger.Error(err, "could not close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get release with status: %d", resp.StatusCode)
	}

	// Parse the response to find the asset download URL
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read release response: %w", err)
	}

	var release GithubReleaseAssets
	if err := json.Unmarshal(body, &release); err != nil {
		g.Logger.Error(err, "DownloadPackage: failed to parse release response", "body", string(body), "bodyLength", len(body), "assetName", humioPackage.Spec.Github.AssetName)
		return fmt.Errorf("failed to parse release response: %w", err)
	}

	// Find the specific asset
	downloadUrl := release.DownloadUrl
	g.Logger.Info("DownloadPackage: Processing release response", "downloadUrl", downloadUrl, "assetsCount", len(release.Assets), "assetName", humioPackage.Spec.Github.AssetName)
	if humioPackage.Spec.Github.AssetName != "" {
		for _, asset := range release.Assets {
			if asset.Name == humioPackage.Spec.Github.AssetName {
				downloadUrl = asset.BrowserDownloadURL
				break
			}
		}
	}
	if downloadUrl == "" {
		return fmt.Errorf("asset %s not found in release %s", humioPackage.Spec.Github.AssetName, humioPackage.Spec.PackageVersion)
	}

	g.Logger.Info("DownloadPackage: Starting asset download", "downloadUrl", downloadUrl, "assetName", humioPackage.Spec.Github.AssetName)

	// Create timeout context for download - use longer timeout for downloads
	downloadCtx, downloadCancel := context.WithTimeout(ctx, DefaultTimeouts.Download)
	defer downloadCancel()

	// Download the asset
	downloadHeaders := map[string]string{}
	downloadHeaders["Accept"] = "application/vnd.github+json"

	token, tokenErr := g.getSecretValue(downloadCtx)
	if tokenErr != nil || token == "" {
		return fmt.Errorf("error retrieving auth credentials: %w", tokenErr)
	}
	downloadHeaders["Authorization"] = fmt.Sprintf("token %s", token)
	downloadResp, err := g.HTTPClient.GetWithHeadersAndContext(downloadCtx, downloadUrl, downloadHeaders)
	if err != nil {
		return fmt.Errorf("failed to download asset: %w", err)
	}
	defer func() {
		if err := downloadResp.Body.Close(); err != nil {
			g.Logger.Error(err, "could not close download response body")
		}
	}()

	if downloadResp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", downloadResp.StatusCode)
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

	_, err = io.Copy(outFile, downloadResp.Body)
	if err != nil {
		return fmt.Errorf("failed to write package to file: %w", err)
	}

	return nil
}

func (g *GithubRegistryClient) ValidateConfig(ctx context.Context) error {
	if g.Config == nil {
		return fmt.Errorf("github registry config is nil")
	}
	if g.Config.URL == "" {
		return fmt.Errorf("URL field is required")
	}
	if g.Config.Owner == "" {
		return fmt.Errorf("owner field is required")
	}
	if g.Config.TokenRef.Name == "" {
		return fmt.Errorf("token secret name field is required")
	}
	if g.Config.TokenRef.Key == "" {
		return fmt.Errorf("token secret key field is required")
	}

	// Validate that we can retrieve the token
	_, err := g.getSecretValue(ctx)
	return err
}

func (g *GithubRegistryClient) getSecretValue(ctx context.Context) (string, error) {
	// Determine which namespace to use for the secret
	secretNamespace := g.Namespace
	if g.Config.TokenRef.Namespace != "" {
		secretNamespace = g.Config.TokenRef.Namespace
	}

	// Check if the secret exists
	secret, err := kubernetes.GetSecret(ctx, g.K8SClient, g.Config.TokenRef.Name, secretNamespace)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s in namespace %s: %w", g.Config.TokenRef.Name, secretNamespace, err)
	}

	// Check if the secret has the required key
	token, exists := secret.Data[g.Config.TokenRef.Key]
	if !exists {
		return "", fmt.Errorf("secret %s does not contain key %s", g.Config.TokenRef.Name, g.Config.TokenRef.Key)
	}

	return strings.TrimSpace(string(token)), nil
}
