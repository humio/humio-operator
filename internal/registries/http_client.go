package registries

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	api "github.com/humio/humio-operator/internal/api"
	"golang.org/x/oauth2/google"
)

// HTTPClientInterface allows for dependency injection of HTTP clients
type HTTPClientInterface interface {
	GetGcloudAccessToken(ctx context.Context, serviceAccountKey []byte) (string, error)
	GetWithContext(ctx context.Context, url string) (*http.Response, error)
	GetWithHeadersAndContext(ctx context.Context, url string, headers map[string]string) (*http.Response, error)
	HeadWithHeadersAndContext(ctx context.Context, url string, headers map[string]string) (*http.Response, error)
	PostWithHeadersAndContext(ctx context.Context, url string, headers map[string]string, body []byte) (*http.Response, error)
}

type HTTPClient struct {
	transport *http.Transport
	timeout   time.Duration
}

// TimeoutConfig holds different timeout durations for different operations
type TimeoutConfig struct {
	APICall  time.Duration
	Download time.Duration
	Auth     time.Duration
}

// DefaultTimeouts provides sensible defaults
var DefaultTimeouts = TimeoutConfig{
	APICall:  30 * time.Second,
	Download: 10 * time.Minute,
	Auth:     10 * time.Second,
}

func NewHTTPClient(config api.Config) *HTTPClient {
	return &HTTPClient{
		transport: api.NewHttpTransport(config),
		timeout:   DefaultTimeouts.APICall,
	}
}

func (h *HTTPClient) PostWithHeadersAndContext(ctx context.Context, url string, headers map[string]string, body []byte) (*http.Response, error) {
	client := &http.Client{
		Transport: h.transport,
		Timeout:   h.timeout,
	}
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", url, bodyReader)
	if err != nil {
		return nil, err
	}

	// Apply headers directly to the request
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	return client.Do(req)
}

func (h *HTTPClient) GetGcloudAccessToken(ctx context.Context, serviceAccountKey []byte) (string, error) {
	// Create OAuth2 config using Google's service account credentials
	config, err := google.JWTConfigFromJSON(serviceAccountKey, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", fmt.Errorf("failed to create JWT config from service account: %w", err)
	}

	// Get OAuth2 token source
	tokenSource := config.TokenSource(ctx)

	// Get the access token
	token, err := tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("failed to get OAuth2 token: %w", err)
	}

	return token.AccessToken, nil
}

// GetWithContext makes a GET request with context and timeout control
func (h *HTTPClient) GetWithContext(ctx context.Context, url string) (*http.Response, error) {
	client := &http.Client{
		Transport: h.transport,
		Timeout:   h.timeout,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	return client.Do(req)
}

// HeadWithHeadersAndContext makes a HEAD request with headers, context and timeout control
func (h *HTTPClient) HeadWithHeadersAndContext(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	client := &http.Client{
		Transport: &api.HeaderTransport{
			Base:    h.transport,
			Headers: headers,
		},
		Timeout: h.timeout,
	}

	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return nil, err
	}

	return client.Do(req)
}

// GetWithHeadersAndContext makes a GET request with headers, context and timeout control
func (h *HTTPClient) GetWithHeadersAndContext(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	client := &http.Client{
		Transport: &api.HeaderTransport{
			Base:    h.transport,
			Headers: headers,
		},
		Timeout: h.timeout,
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	return client.Do(req)
}
