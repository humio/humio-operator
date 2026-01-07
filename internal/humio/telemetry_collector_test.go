package humio

import (
	"context"
	"testing"
	"time"

	"github.com/humio/humio-operator/internal/api"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestCollectIngestionMetrics(t *testing.T) {
	tests := []struct {
		name              string
		settings          QuerySettings
		mockSearchSupport bool
		expectError       bool
		validateResults   func(t *testing.T, metrics *TelemetryIngestionMetrics)
	}{
		{
			name:              "successful ingestion metrics collection",
			settings:          DefaultQuerySettings,
			mockSearchSupport: true,
			expectError:       false,
			validateResults: func(t *testing.T, metrics *TelemetryIngestionMetrics) {
				if metrics == nil {
					t.Fatal("Expected metrics but got nil")
				}
				if metrics.Daily.IngestVolumeGB <= 0 {
					t.Errorf("Expected positive daily ingest volume, got %f", metrics.Daily.IngestVolumeGB)
				}
				if metrics.Daily.EventCount <= 0 {
					t.Errorf("Expected positive daily event count, got %d", metrics.Daily.EventCount)
				}
				if metrics.Weekly.IngestVolumeGB <= 0 {
					t.Errorf("Expected positive weekly ingest volume, got %f", metrics.Weekly.IngestVolumeGB)
				}
				if metrics.Monthly.IngestVolumeGB <= 0 {
					t.Errorf("Expected positive monthly ingest volume, got %f", metrics.Monthly.IngestVolumeGB)
				}
				if metrics.Monthly.TrendDirection == "" {
					t.Error("Expected trend direction to be set")
				}
				// Validate time range is reasonable (30 days)
				expectedDuration := 30 * 24 * time.Hour
				actualDuration := metrics.TimeRange.End.Sub(metrics.TimeRange.Start)
				if actualDuration < expectedDuration-time.Hour || actualDuration > expectedDuration+time.Hour {
					t.Errorf("Expected ~30 day time range, got %v", actualDuration)
				}
			},
		},
		{
			name:              "collection with custom settings",
			settings:          QuerySettings{MaxExecutionTime: 45 * time.Second, TimeRangeMode: "fixed"},
			mockSearchSupport: true,
			expectError:       false,
			validateResults: func(t *testing.T, metrics *TelemetryIngestionMetrics) {
				// Should still succeed with custom settings
				if metrics == nil {
					t.Fatal("Expected metrics but got nil")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := NewMockClient()
			apiClient := &api.Client{}

			// Test the collection using mock client's method
			metrics, err := mockClient.CollectIngestionMetrics(context.Background(), apiClient, tt.settings)

			// Validate error expectation
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Run custom validation if provided and no error expected
			if !tt.expectError && tt.validateResults != nil {
				tt.validateResults(t, metrics)
			}

			// Cleanup
			mockClient.ClearHumioClientConnections("")
		})
	}
}

func TestCollectRepositoryUsage(t *testing.T) {
	tests := []struct {
		name            string
		settings        QuerySettings
		setupRepos      func(*MockClientConfig)
		expectError     bool
		validateResults func(t *testing.T, metrics *TelemetryRepositoryUsageMetrics)
	}{
		{
			name:     "successful repository usage collection",
			settings: DefaultQuerySettings,
			setupRepos: func(mock *MockClientConfig) {
				// No additional setup needed - mock will return default repositories
			},
			expectError: false,
			validateResults: func(t *testing.T, metrics *TelemetryRepositoryUsageMetrics) {
				if metrics == nil {
					t.Fatal("Expected metrics but got nil")
				}
				if metrics.TotalRepositories <= 0 {
					t.Errorf("Expected positive total repositories, got %d", metrics.TotalRepositories)
				}
				if len(metrics.Repositories) != metrics.TotalRepositories {
					t.Errorf("Repository count mismatch: total=%d, actual=%d", metrics.TotalRepositories, len(metrics.Repositories))
				}
				// Validate repository data structure
				for i, repo := range metrics.Repositories {
					if repo.Name == "" {
						t.Errorf("Repository %d has empty name", i)
					}
					if repo.IngestVolumeGB24h < 0 {
						t.Errorf("Repository %s has negative ingest volume: %f", repo.Name, repo.IngestVolumeGB24h)
					}
					if repo.EventCount24h < 0 {
						t.Errorf("Repository %s has negative event count: %d", repo.Name, repo.EventCount24h)
					}
					if repo.RetentionDays <= 0 {
						t.Errorf("Repository %s has invalid retention: %d", repo.Name, repo.RetentionDays)
					}
					if repo.LastActivityTime.IsZero() {
						t.Errorf("Repository %s has zero last activity time", repo.Name)
					}
				}
				if len(metrics.TopRepositories) > len(metrics.Repositories) {
					t.Errorf("More top repositories (%d) than total repositories (%d)", len(metrics.TopRepositories), len(metrics.Repositories))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := NewMockClient()
			if tt.setupRepos != nil {
				tt.setupRepos(mockClient)
			}
			apiClient := &api.Client{}

			// Test the collection using mock client's method
			metrics, err := mockClient.CollectRepositoryUsage(context.Background(), apiClient, tt.settings)

			// Validate error expectation
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Run custom validation if provided and no error expected
			if !tt.expectError && tt.validateResults != nil {
				tt.validateResults(t, metrics)
			}

			// Cleanup
			mockClient.ClearHumioClientConnections("")
		})
	}
}

func TestCollectUserActivity(t *testing.T) {
	tests := []struct {
		name            string
		settings        QuerySettings
		expectError     bool
		validateResults func(t *testing.T, metrics *TelemetryUserActivityMetrics)
	}{
		{
			name:        "successful user activity collection",
			settings:    DefaultQuerySettings,
			expectError: false,
			validateResults: func(t *testing.T, metrics *TelemetryUserActivityMetrics) {
				if metrics == nil {
					t.Fatal("Expected metrics but got nil")
				}
				// Validate active users make sense
				if metrics.ActiveUsers.Last24h < 0 {
					t.Errorf("Expected non-negative 24h users, got %d", metrics.ActiveUsers.Last24h)
				}
				if metrics.ActiveUsers.Last7d < metrics.ActiveUsers.Last24h {
					t.Errorf("Expected 7d users >= 24h users, got 7d=%d, 24h=%d", metrics.ActiveUsers.Last7d, metrics.ActiveUsers.Last24h)
				}
				if metrics.ActiveUsers.Last30d < metrics.ActiveUsers.Last7d {
					t.Errorf("Expected 30d users >= 7d users, got 30d=%d, 7d=%d", metrics.ActiveUsers.Last30d, metrics.ActiveUsers.Last7d)
				}

				// Validate query activity
				if metrics.QueryActivity.TotalQueries < 0 {
					t.Errorf("Expected non-negative total queries, got %d", metrics.QueryActivity.TotalQueries)
				}
				if metrics.QueryActivity.AvgQueryTime < 0 {
					t.Errorf("Expected non-negative avg query time, got %f", metrics.QueryActivity.AvgQueryTime)
				}
				if len(metrics.QueryActivity.TopQueryTypes) == 0 {
					t.Error("Expected at least one query type in top query types")
				}

				// Validate login activity
				if metrics.LoginActivity.TotalLogins < 0 {
					t.Errorf("Expected non-negative total logins, got %d", metrics.LoginActivity.TotalLogins)
				}
				if metrics.LoginActivity.UniqueUsers < 0 {
					t.Errorf("Expected non-negative unique users, got %d", metrics.LoginActivity.UniqueUsers)
				}
				if metrics.LoginActivity.FailedAttempts < 0 {
					t.Errorf("Expected non-negative failed attempts, got %d", metrics.LoginActivity.FailedAttempts)
				}

				// Validate time range is reasonable (30 days)
				expectedDuration := 30 * 24 * time.Hour
				actualDuration := metrics.TimeRange.End.Sub(metrics.TimeRange.Start)
				if actualDuration < expectedDuration-time.Hour || actualDuration > expectedDuration+time.Hour {
					t.Errorf("Expected ~30 day time range, got %v", actualDuration)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := NewMockClient()
			apiClient := &api.Client{}

			// Test the collection using mock client's method
			metrics, err := mockClient.CollectUserActivity(context.Background(), apiClient, tt.settings)

			// Validate error expectation
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Run custom validation if provided and no error expected
			if !tt.expectError && tt.validateResults != nil {
				tt.validateResults(t, metrics)
			}

			// Cleanup
			mockClient.ClearHumioClientConnections("")
		})
	}
}

func TestCollectDetailedAnalytics(t *testing.T) {
	tests := []struct {
		name            string
		settings        QuerySettings
		expectError     bool
		validateResults func(t *testing.T, metrics *TelemetryDetailedAnalytics)
	}{
		{
			name:        "successful detailed analytics collection",
			settings:    DefaultQuerySettings,
			expectError: false,
			validateResults: func(t *testing.T, metrics *TelemetryDetailedAnalytics) {
				if metrics == nil {
					t.Fatal("Expected metrics but got nil")
				}

				// Validate performance metrics
				if metrics.PerformanceMetrics == nil {
					t.Fatal("Expected performance metrics but got nil")
				}
				if len(metrics.PerformanceMetrics) == 0 {
					t.Error("Expected at least one performance metric")
				}

				// Validate usage patterns
				if metrics.UsagePatterns == nil {
					t.Fatal("Expected usage patterns but got nil")
				}
				if len(metrics.UsagePatterns) == 0 {
					t.Error("Expected at least one usage pattern")
				}

				// Validate time range is reasonable (4 hours)
				expectedDuration := 4 * time.Hour
				actualDuration := metrics.TimeRange.End.Sub(metrics.TimeRange.Start)
				if actualDuration < expectedDuration-time.Minute || actualDuration > expectedDuration+time.Minute {
					t.Errorf("Expected ~4 hour time range, got %v", actualDuration)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := NewMockClient()
			apiClient := &api.Client{}

			// Test the collection using mock client's method
			metrics, err := mockClient.CollectDetailedAnalytics(context.Background(), apiClient, tt.settings)

			// Validate error expectation
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Run custom validation if provided and no error expected
			if !tt.expectError && tt.validateResults != nil {
				tt.validateResults(t, metrics)
			}

			// Cleanup
			mockClient.ClearHumioClientConnections("")
		})
	}
}

func TestCollectTelemetryDataAdvanced(t *testing.T) {
	tests := []struct {
		name            string
		dataTypes       []string
		expectError     bool
		validateResults func(t *testing.T, payloads []TelemetryPayload)
	}{
		{
			name:        "collect mixed GraphQL and search-based data types",
			dataTypes:   []string{"license", "cluster_info"},
			expectError: false,
			validateResults: func(t *testing.T, payloads []TelemetryPayload) {
				if len(payloads) != 2 {
					t.Errorf("Expected 2 payloads, got %d", len(payloads))
				}

				expectedTypes := map[string]bool{
					"license":      false,
					"cluster_info": false,
				}

				for _, payload := range payloads {
					if payload.ClusterID == "" {
						t.Error("Expected cluster ID to be set")
					}
					if payload.SourceType != "json" {
						t.Errorf("Expected source type 'json', got '%s'", payload.SourceType)
					}
					if payload.Data == nil {
						t.Errorf("Expected data for collection type %s", payload.CollectionType)
					}
					if payload.Timestamp.IsZero() {
						t.Error("Expected timestamp to be set")
					}

					if _, exists := expectedTypes[payload.CollectionType]; exists {
						expectedTypes[payload.CollectionType] = true
					} else {
						t.Errorf("Unexpected collection type: %s", payload.CollectionType)
					}
				}

				for collectionType, found := range expectedTypes {
					if !found {
						t.Errorf("Missing payload for collection type: %s", collectionType)
					}
				}
			},
		},
		{
			name:        "unknown data type should cause error",
			dataTypes:   []string{"license", "unknown_type"},
			expectError: true,
			validateResults: func(t *testing.T, payloads []TelemetryPayload) {
				// Should have no payloads when there's an unknown data type error
				if len(payloads) != 0 {
					t.Errorf("Expected 0 payloads due to error, got %d", len(payloads))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := NewMockClient()
			apiClient := &api.Client{}
			clusterID := "test-cluster-123"

			// Test the collection using mock client's method
			payloads, err := mockClient.CollectTelemetryData(context.Background(), apiClient, tt.dataTypes, clusterID, true, nil, nil)

			// Validate error expectation
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Run custom validation if provided
			if tt.validateResults != nil {
				tt.validateResults(t, payloads)
			}

			// Cleanup
			mockClient.ClearHumioClientConnections("")
		})
	}
}

func TestSupportsSearchExecution(t *testing.T) {
	tests := []struct {
		name            string
		expectSupported bool
		expectError     bool
	}{
		{
			name:            "search supported with mock client",
			expectSupported: false, // Mock client returns localhost which will fail, so we expect false
			expectError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock client
			mockClient := NewMockClient()

			// Create a proper API client with URL (required for HTTP requests)
			apiClient := mockClient.GetHumioHttpClient(nil, ctrl.Request{})

			// Test search support detection
			config := &ClientConfig{}
			supported, err := config.supportsSearchExecution(context.Background(), apiClient)

			if supported != tt.expectSupported {
				t.Errorf("Expected search support %v, got %v", tt.expectSupported, supported)
			}

			// Check error expectation
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Cleanup
			mockClient.ClearHumioClientConnections("")
		})
	}
}

func TestQuerySettings(t *testing.T) {
	// Test default query settings
	if DefaultQuerySettings.MaxExecutionTime != 30*time.Second {
		t.Errorf("Expected default max execution time 30s, got %v", DefaultQuerySettings.MaxExecutionTime)
	}
	if DefaultQuerySettings.MaxResultSize != 1024*1024 {
		t.Errorf("Expected default max result size 1MB, got %d", DefaultQuerySettings.MaxResultSize)
	}
	if DefaultQuerySettings.TimeoutRetries != 2 {
		t.Errorf("Expected default timeout retries 2, got %d", DefaultQuerySettings.TimeoutRetries)
	}
	if DefaultQuerySettings.TimeRangeMode != "relative" {
		t.Errorf("Expected default time range mode 'relative', got %s", DefaultQuerySettings.TimeRangeMode)
	}
}
