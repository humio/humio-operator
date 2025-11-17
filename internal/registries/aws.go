package registries

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CodeArtifactAssetResponse represents the response from AWS CodeArtifact assets API
type CodeArtifactAssetResponse struct {
	Assets []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
	Package string `json:"package"`
	Format  string `json:"format"`
	Version string `json:"version"`
}

type AwsRegistryClient struct {
	RegistryType string
	Enabled      bool
	Name         string
	Config       *humiov1alpha1.RegistryConnectionAws
	HTTPClient   HTTPClientInterface
	K8SClient    client.Client
	Namespace    string
	Logger       logr.Logger
}

func (a *AwsRegistryClient) GetRegistryType() string {
	return a.RegistryType
}

func (a *AwsRegistryClient) IsEnabled() bool {
	return a.Enabled
}

func (a *AwsRegistryClient) GetName() string {
	return a.Name
}

func (a *AwsRegistryClient) CheckConnection(ctx context.Context) error {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.APICall)
	defer cancel()

	// Get AWS credentials
	accessKey, secretKey, err := a.getAwsCredentials(ctx)
	if err != nil {
		return fmt.Errorf("error retrieving AWS credentials: %w", err)
	}
	// Test connection by making a simple CodeArtifact API call
	baseURL := fmt.Sprintf("https://codeartifact.%s.amazonaws.com/v1/domain", a.Config.Region)

	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("error parsing base URL: %w", err)
	}
	// Use url.Values to properly encode query parameters
	params := url.Values{}
	params.Set("domain", a.Config.Domain)
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	// Sign the request using AWS Signature Version 4
	a.signRequest(req, accessKey, secretKey, "codeartifact", a.Config.Region, time.Now().UTC())

	// Convert to headers for HTTPClient
	headers := make(map[string]string)
	for key, values := range req.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	// Make the actual HTTP request with context
	resp, err := a.HTTPClient.GetWithHeadersAndContext(ctx, u.String(), headers)
	if err != nil {
		return fmt.Errorf("failed to connect to AWS CodeArtifact: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			a.Logger.Error(err, "could not close response body")
		}
	}()

	// Accept both 200 (success) and 404 (domain doesn't exist but auth worked)
	if resp.StatusCode != http.StatusOK {
		// Read response body for more detailed error information
		body, bodyErr := io.ReadAll(resp.Body)
		if bodyErr == nil && len(body) > 0 {
			return fmt.Errorf("AWS CodeArtifact connection test failed with status: %d, response: %s", resp.StatusCode, string(body))
		}
		return fmt.Errorf("AWS CodeArtifact connection test failed with status: %d", resp.StatusCode)
	}

	return nil
}

func (a *AwsRegistryClient) CheckPackageExists(humioPackage *humiov1alpha1.HumioPackage) bool {
	// Create timeout context for API call
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeouts.APICall)
	defer cancel()

	accessKey, secretKey, err := a.getAwsCredentials(ctx)
	if err != nil {
		return false
	}

	// Build CodeArtifact assets API URL
	baseURL := fmt.Sprintf("https://codeartifact.%s.amazonaws.com/v1/package/version/assets", a.Config.Region)

	// Use url.Values to properly encode query parameters
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}

	params := url.Values{}
	params.Set("domain", a.Config.Domain)
	params.Set("format", "generic")
	params.Set("namespace", humioPackage.Spec.Aws.Namespace)
	params.Set("package", humioPackage.Spec.Aws.Package)
	params.Set("repository", a.Config.Repository)
	params.Set("version", humioPackage.Spec.PackageVersion)

	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, "POST", u.String(), nil)
	if err != nil {
		return false
	}

	// Sign the request
	a.signRequest(req, accessKey, secretKey, "codeartifact", a.Config.Region, time.Now().UTC())

	// Convert to headers for HTTPClient
	headers := make(map[string]string)
	for key, values := range req.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	// Make the actual HTTP request using HTTPClient with context
	resp, err := a.HTTPClient.PostWithHeadersAndContext(ctx, u.String(), headers, nil)
	if err != nil {
		return false
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			a.Logger.Error(err, "could not close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	var assetResponse CodeArtifactAssetResponse
	if err := json.NewDecoder(resp.Body).Decode(&assetResponse); err != nil {
		return false
	}

	// Check if the filename exists in the assets array
	for _, asset := range assetResponse.Assets {
		if asset.Name == humioPackage.Spec.Aws.Filename {
			return true
		}
	}

	return false
}

func (a *AwsRegistryClient) DownloadPackage(ctx context.Context, humioPackage *humiov1alpha1.HumioPackage, downloadPath string) error {
	// Check if context is already cancelled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("download cancelled: %w", err)
	}

	// Create timeout context for download - use longer timeout for downloads
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeouts.Download)
	defer cancel()

	accessKey, secretKey, err := a.getAwsCredentials(ctx)
	if err != nil {
		return fmt.Errorf("error retrieving AWS credentials: %w", err)
	}

	// Build CodeArtifact download URL
	baseURL := fmt.Sprintf("https://codeartifact.%s.amazonaws.com/v1/package/version/asset", a.Config.Region)
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("error parsing base URL: %w", err)
	}
	params := url.Values{}
	params.Set("asset", humioPackage.Spec.Aws.Filename)
	params.Set("domain", a.Config.Domain)
	params.Set("format", "generic")
	params.Set("namespace", humioPackage.Spec.Aws.Namespace)
	params.Set("package", humioPackage.Spec.Aws.Package)
	params.Set("repository", a.Config.Repository)
	params.Set("version", humioPackage.Spec.PackageVersion)
	u.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return fmt.Errorf("error creating download request: %w", err)
	}

	// Sign the request
	a.signRequest(req, accessKey, secretKey, "codeartifact", a.Config.Region, time.Now().UTC())

	// Convert to headers for HTTPClient
	headers := make(map[string]string)
	for key, values := range req.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}

	resp, err := a.HTTPClient.GetWithHeadersAndContext(ctx, u.String(), headers)
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

	// Stream the response body to the file
	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write package to file: %w", err)
	}

	return nil
}

func (a *AwsRegistryClient) ValidateConfig(ctx context.Context) error {
	if a.Config == nil {
		return fmt.Errorf("aws registry config is nil")
	}
	if a.Config.Region == "" {
		return fmt.Errorf("region field is required")
	}
	if a.Config.Domain == "" {
		return fmt.Errorf("domain field is required")
	}
	if a.Config.Repository == "" {
		return fmt.Errorf("repository field is required")
	}
	if a.Config.AccessKeyRef.Name == "" {
		return fmt.Errorf("access key secret name field is required")
	}
	if a.Config.AccessKeyRef.Key == "" {
		return fmt.Errorf("access key secret key field is required")
	}
	if a.Config.AccessSecretRef.Name == "" {
		return fmt.Errorf("access secret secret name field is required")
	}
	if a.Config.AccessSecretRef.Key == "" {
		return fmt.Errorf("access secret secret key field is required")
	}

	// Validate that we can retrieve the credentials
	_, _, err := a.getAwsCredentials(ctx)
	return err
}

func (a *AwsRegistryClient) getAwsCredentials(ctx context.Context) (accessKey, secretKey string, err error) {
	// Get access key
	accessKeyNamespace := a.Namespace
	if a.Config.AccessKeyRef.Namespace != "" {
		accessKeyNamespace = a.Config.AccessKeyRef.Namespace
	}

	secret, err := kubernetes.GetSecret(ctx, a.K8SClient, a.Config.AccessKeyRef.Name, accessKeyNamespace)
	if err != nil {
		return "", "", fmt.Errorf("failed to get access key secret %s in namespace %s: %w", a.Config.AccessKeyRef.Name, accessKeyNamespace, err)
	}

	accessKeyBytes, exists := secret.Data[a.Config.AccessKeyRef.Key]
	if !exists {
		return "", "", fmt.Errorf("secret %s does not contain key %s", a.Config.AccessKeyRef.Name, a.Config.AccessKeyRef.Key)
	}

	// Get secret key
	secretKeyNamespace := a.Namespace
	if a.Config.AccessSecretRef.Namespace != "" {
		secretKeyNamespace = a.Config.AccessSecretRef.Namespace
	}

	secretSecret, err := kubernetes.GetSecret(ctx, a.K8SClient, a.Config.AccessSecretRef.Name, secretKeyNamespace)
	if err != nil {
		return "", "", fmt.Errorf("failed to get access secret secret %s in namespace %s: %w", a.Config.AccessSecretRef.Name, secretKeyNamespace, err)
	}

	secretKeyBytes, exists := secretSecret.Data[a.Config.AccessSecretRef.Key]
	if !exists {
		return "", "", fmt.Errorf("secret %s does not contain key %s", a.Config.AccessSecretRef.Name, a.Config.AccessSecretRef.Key)
	}

	return strings.TrimSpace(string(accessKeyBytes)), strings.TrimSpace(string(secretKeyBytes)), nil
}

// signRequest signs an HTTP request using AWS Signature Version 4
// Based on AWS SDK implementation but without external dependencies
func (a *AwsRegistryClient) signRequest(req *http.Request, accessKey, secretKey, service, region string, signingTime time.Time) {
	// Step 1: Set required headers
	req.Header.Set("Host", req.URL.Host)
	req.Header.Set("X-Amz-Date", signingTime.Format("20060102T150405Z"))

	// Step 2: Create canonical request
	canonicalRequest, signedHeaders := a.buildCanonicalRequest(req)

	// Step 3: Create string to sign
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request",
		signingTime.Format("20060102"), region, service)

	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		signingTime.Format("20060102T150405Z"),
		credentialScope,
		sha256Hash(canonicalRequest))

	// Step 4: Calculate signature
	signature := a.calculateSignature(secretKey, signingTime, region, service, stringToSign)

	// Step 5: Add authorization header
	authorization := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature)

	req.Header.Set("Authorization", authorization)
}

// buildCanonicalRequest creates the canonical request string for AWS v4 signing
// Implementation based on AWS SDK v4 signer logic
func (a *AwsRegistryClient) buildCanonicalRequest(req *http.Request) (string, string) {
	// Canonical method
	method := req.Method

	// Canonical URI - use URL path, default to "/"
	uri := req.URL.Path
	if uri == "" {
		uri = "/"
	}

	// Canonical query string - sort parameters alphabetically
	query := ""
	if req.URL.RawQuery != "" {
		values, _ := url.ParseQuery(req.URL.RawQuery)
		var keys []string
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var parts []string
		for _, key := range keys {
			for _, value := range values[key] {
				parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(value))
			}
		}
		query = strings.Join(parts, "&")
	}

	// Canonical headers - normalize and sort
	headerKeys := make([]string, 0, len(req.Header))
	headers := make(map[string]string)

	for name, values := range req.Header {
		lowerName := strings.ToLower(name)
		headerKeys = append(headerKeys, lowerName)

		// Trim whitespace and join multiple values
		var trimmedValues []string
		for _, value := range values {
			trimmedValues = append(trimmedValues, strings.TrimSpace(value))
		}
		headers[lowerName] = strings.Join(trimmedValues, ",")
	}

	sort.Strings(headerKeys)

	var canonicalHeaders strings.Builder
	for _, key := range headerKeys {
		canonicalHeaders.WriteString(key)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(headers[key])
		canonicalHeaders.WriteString("\n")
	}

	signedHeaders := strings.Join(headerKeys, ";")

	// Payload hash - SHA256 of empty string for empty requests
	payloadHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	// Build canonical request
	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		method,
		uri,
		query,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash)

	return canonicalRequest, signedHeaders
}

// calculateSignature derives the signing key and calculates the final signature
// Implementation follows AWS Signature Version 4 specification
func (a *AwsRegistryClient) calculateSignature(secretKey string, signingTime time.Time, region, service, stringToSign string) string {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), signingTime.Format("20060102"))
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hmacSHA256(kSigning, stringToSign)

	return hex.EncodeToString(signature)
}

// hmacSHA256 computes HMAC-SHA256
func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// sha256Hash computes SHA256 hash and returns hex string
func sha256Hash(data string) string {
	h := sha256.New()
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}
