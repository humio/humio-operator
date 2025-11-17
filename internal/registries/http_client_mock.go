package registries

import (
	"context"
	"net/http"

	"github.com/stretchr/testify/mock"
)

type MockHTTPClient struct {
	mock.Mock
}

// PostWithHeadersAndContext is the context-aware version
func (m *MockHTTPClient) PostWithHeadersAndContext(ctx context.Context, url string, headers map[string]string, body []byte) (*http.Response, error) {
	args := m.Called(ctx, url, headers, body)
	return args.Get(0).(*http.Response), args.Error(1)
}

func (m *MockHTTPClient) GetGcloudAccessToken(ctx context.Context, serviceAccountKey []byte) (string, error) {
	args := m.Called(ctx, serviceAccountKey)
	return args.String(0), args.Error(1)
}

func (m *MockHTTPClient) HeadWithHeadersAndContext(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	args := m.Called(ctx, url, headers)
	return args.Get(0).(*http.Response), args.Error(1)
}

// Context-aware methods
func (m *MockHTTPClient) GetWithContext(ctx context.Context, url string) (*http.Response, error) {
	args := m.Called(ctx, url)
	return args.Get(0).(*http.Response), args.Error(1)
}

func (m *MockHTTPClient) GetWithHeadersAndContext(ctx context.Context, url string, headers map[string]string) (*http.Response, error) {
	args := m.Called(ctx, url, headers)
	return args.Get(0).(*http.Response), args.Error(1)
}
