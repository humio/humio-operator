package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GitlabRegistryClient struct {
	RegistryType string
	Enabled      bool
	Name         string
	Config       *humiov1alpha1.RegistryConnectionGitlab
	HTTPClient   HTTPClientInterface
	K8SClient    client.Client
	Namespace    string
	Logger       logr.Logger
}

func (g *GitlabRegistryClient) GetRegistryType() string {
	return g.RegistryType
}

func (g *GitlabRegistryClient) IsEnabled() bool {
	return g.Enabled
}

func (g *GitlabRegistryClient) GetName() string {
	return g.Name
}

func (g *GitlabRegistryClient) GetProject() string {
	project := g.Config.Project

	// Check if the project is already URL encoded by trying to decode it
	// If decoding succeeds and produces a different result, it was already encoded
	if decoded, err := url.PathUnescape(project); err == nil && decoded != project {
		return project
	}
	return url.PathEscape(project)
}

func (g *GitlabRegistryClient) CheckConnection(ctx context.Context) error {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.APICall)
	defer cancel()

	var err error
	//retrieve token to be used for auth
	token, err := g.getSecretValue(ctx)
	if err != nil {
		return fmt.Errorf("error retrieving auth credentials: %v", err)
	}

	// build auth headers
	headers := map[string]string{
		"PRIVATE-TOKEN": token,
		"Content-Type":  "application/json",
	}
	endpoint, err := helpers.SafeURLJoin(g.Config.URL, "/version")
	if err != nil {
		return fmt.Errorf("error encountered while building API endpoint: %v", err)
	}
	// make http request with context
	resp, err := g.HTTPClient.GetWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		return fmt.Errorf("error encountered during CheckConnection: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			g.Logger.Error(err, "could not close response body")
		}
	}()

	// Check for authentication errors
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("authentication failed: invalid or expired GitLab token")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitLab API returned status %d: %s", resp.StatusCode, resp.Status)
	}

	// read expected field from body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %v", err)
	}

	var versionResponse map[string]any
	if err := json.Unmarshal(body, &versionResponse); err != nil {
		return fmt.Errorf("error parsing JSON response: %v", err)
	}

	// Check if version field exists
	if _, exists := versionResponse["version"]; !exists {
		return fmt.Errorf("response does not contain version field")
	}

	return nil
}

func (g *GitlabRegistryClient) CheckPackageExists(humioPackage *humiov1alpha1.HumioPackage) bool {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeouts.APICall)
	defer cancel()

	// Get token for authentication
	token, err := g.getSecretValue(ctx)
	if err != nil {
		return false
	}

	// Build GitLab Package Registry API URL
	// Format: /projects/:id/packages/generic/:package_name/:package_version/:file_name
	path := fmt.Sprintf("/%s/packages/generic/%s/%s/%s",
		g.GetProject(), humioPackage.Spec.Gitlab.Package, humioPackage.Spec.PackageVersion,
		humioPackage.Spec.Gitlab.AssetName)
	endpoint, err := helpers.SafeURLJoin(g.Config.URL, "/projects")
	// url.String() double encodes so we exclude path from helpers.SafeURLJoin
	if err != nil {
		return false
	}

	endpoint = endpoint + path
	headers := map[string]string{
		"PRIVATE-TOKEN": token,
	}

	// Make HEAD request to check if package exists
	resp, err := g.HTTPClient.HeadWithHeadersAndContext(ctx, endpoint, headers)
	if err != nil {
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			g.Logger.Error(err, "could not close response body")
		}
	}()

	// Return true only if we get a 200 OK status
	return resp.StatusCode == http.StatusOK
}

func (g *GitlabRegistryClient) DownloadPackage(ctx context.Context, humioPackage *humiov1alpha1.HumioPackage, downloadPath string) error {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download cancelled: %w", err)
	}

	// Create timeout context for download - use longer timeout for downloads
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.Download)
	defer cancel()

	// Get token for authentication
	token, err := g.getSecretValue(ctx)
	if err != nil {
		return fmt.Errorf("error retrieving auth credentials: %w", err)
	}

	// GitLab Package Registry API URL
	// Format: /projects/:id/packages/generic/:package_name/:package_version/:file_name
	path := fmt.Sprintf("/%s/packages/generic/%s/%s/%s",
		g.GetProject(), humioPackage.Spec.Gitlab.Package, humioPackage.Spec.PackageVersion,
		humioPackage.Spec.Gitlab.AssetName)
	endpoint, err := helpers.SafeURLJoin(g.Config.URL, "/projects")
	if err != nil {
		return fmt.Errorf("error building download URL: %w", err)
	}

	endpoint = endpoint + path
	headers := map[string]string{
		"PRIVATE-TOKEN": token,
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

	// Stream the response body to the file with context awareness
	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write package to file: %w", err)
	}

	return nil
}

func (g *GitlabRegistryClient) ValidateConfig(ctx context.Context) error {
	if g.Config == nil {
		return fmt.Errorf("gitlab registry config is nil")
	}
	if g.Config.TokenRef.Name == "" {
		return fmt.Errorf("token secret name field is required")
	}
	if g.Config.TokenRef.Key == "" {
		return fmt.Errorf("token secret key field is required")
	}
	if g.Config.Project == "" {
		return fmt.Errorf("project field is required")
	}
	_, err := g.getSecretValue(ctx)
	return err
}

func (g *GitlabRegistryClient) getSecretValue(ctx context.Context) (string, error) {
	// Determine which namespace to use for the secret
	secretNamespace := g.Namespace
	if g.Config.TokenRef.Namespace != "" {
		secretNamespace = g.Config.TokenRef.Namespace
	}

	// Check if the secret exists
	secret, err := kubernetes.GetSecret(ctx, g.K8SClient, g.Config.TokenRef.Name, secretNamespace)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s in namespace %s: %v", g.Config.TokenRef.Name, secretNamespace, err)
	}

	// Check if the secret has the required key
	token, exists := secret.Data[g.Config.TokenRef.Key]
	if !exists {
		return "", fmt.Errorf("secret %s does not contain key %s", g.Config.TokenRef.Name, g.Config.TokenRef.Key)
	}

	return strings.TrimSpace(string(token)), nil
}
