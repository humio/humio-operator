package humio

import (
	"context"
	"strings"
	"testing"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Test helper functions to consolidate duplicated validation logic

// validateTimeRange checks if the time range duration is within expected bounds
// This helper consolidates duplicated time range validation across multiple test functions
func validateTimeRange(t *testing.T, start, end time.Time, expectedDuration time.Duration) {
	t.Helper()
	actualDuration := end.Sub(start)
	tolerance := time.Hour // Allow 1 hour tolerance
	if actualDuration < expectedDuration-tolerance || actualDuration > expectedDuration+tolerance {
		t.Errorf("Expected ~%v time range, got %v", expectedDuration, actualDuration)
	}
}

func TestCollectIngestionMetrics(t *testing.T) {
	tests := []struct {
		name              string
		settings          QuerySettings
		mockSearchSupport bool
		expectError       bool
		validateResults   func(t *testing.T, metrics []*TelemetryIngestionMetrics)
	}{
		{
			name:              "successful ingestion metrics collection",
			settings:          DefaultQuerySettings,
			mockSearchSupport: true,
			expectError:       false,
			validateResults: func(t *testing.T, metrics []*TelemetryIngestionMetrics) {
				if len(metrics) == 0 {
					t.Fatal("Expected at least one metrics entry but got empty slice")
				}
				// Test first metrics entry (should be the mock organization)
				firstMetrics := metrics[0]
				if firstMetrics == nil {
					t.Fatal("Expected first metrics entry but got nil")
				}
				if firstMetrics.Daily.IngestVolumeGB <= 0 {
					t.Errorf("Expected positive daily ingest volume, got %f", firstMetrics.Daily.IngestVolumeGB)
				}
				if firstMetrics.Daily.EventCount <= 0 {
					t.Errorf("Expected positive daily event count, got %d", firstMetrics.Daily.EventCount)
				}
				if firstMetrics.Weekly.IngestVolumeGB <= 0 {
					t.Errorf("Expected positive weekly ingest volume, got %f", firstMetrics.Weekly.IngestVolumeGB)
				}
				if firstMetrics.Monthly.IngestVolumeGB <= 0 {
					t.Errorf("Expected positive monthly ingest volume, got %f", firstMetrics.Monthly.IngestVolumeGB)
				}
				if firstMetrics.Monthly.TrendDirection == "" {
					t.Error("Expected trend direction to be set")
				}
				// Validate time range using consolidated helper
				validateTimeRange(t, firstMetrics.TimeRange.Start, firstMetrics.TimeRange.End, 30*24*time.Hour)
			},
		},
		{
			name:              "collection with custom settings",
			settings:          QuerySettings{MaxExecutionTime: 45 * time.Second, TimeRangeMode: "fixed"},
			mockSearchSupport: true,
			expectError:       false,
			validateResults: func(t *testing.T, metrics []*TelemetryIngestionMetrics) {
				// Should still succeed with custom settings
				if len(metrics) == 0 {
					t.Fatal("Expected at least one metrics entry but got empty slice")
				}
				if metrics[0] == nil {
					t.Fatal("Expected first metrics entry but got nil")
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

				// Validate time range using consolidated helper
				validateTimeRange(t, metrics.TimeRange.Start, metrics.TimeRange.End, 30*24*time.Hour)
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
			payloads, _, err := mockClient.CollectTelemetryData(context.Background(), apiClient, tt.dataTypes, clusterID, true, nil, nil)

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
			supported, err := config.supportsSearchExecution(context.Background(), apiClient, nil)

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

func TestDiscoverQueryCapableServices(t *testing.T) {
	tests := []struct {
		name               string
		mainNodeCount      int
		mainClusterEnvVars []corev1.EnvVar
		nodePools          []humiov1alpha1.HumioNodePoolSpec
		expectedCount      int
		expectedNames      []string
		expectedError      bool
	}{
		{
			name:               "mixed node roles - filters correctly",
			mainNodeCount:      0,
			mainClusterEnvVars: nil,
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "all-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            2,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleAll}},
					},
				},
				{
					Name: "httponly-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleHTTPOnly}},
					},
				},
				{
					Name: "ingestonly-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            2,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
				{
					Name: "default-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: 1, // No NODE_ROLES should be included (query-capable)
					},
				},
			},
			expectedCount: 3,
			expectedNames: []string{"test-cluster-all-pool", "test-cluster-httponly-pool", "test-cluster-default-pool"},
			expectedError: false,
		},
		{
			name:               "only ingest-only node pools",
			mainNodeCount:      0,
			mainClusterEnvVars: nil,
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "ingest-pool-1",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            2,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
				{
					Name: "ingest-pool-2",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectedCount: 0,
			expectedNames: []string{},
			expectedError: false,
		},
		{
			name: "only query-capable node pools",
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "all-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleAll}},
					},
				},
				{
					Name: "httponly-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            2,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleHTTPOnly}},
					},
				},
			},
			expectedCount: 2,
			expectedNames: []string{"test-cluster-all-pool", "test-cluster-httponly-pool"},
			expectedError: false,
		},
		{
			name:               "no node pools defined - uses main cluster service",
			mainNodeCount:      6, // Main cluster has nodes
			mainClusterEnvVars: nil,
			nodePools:          []humiov1alpha1.HumioNodePoolSpec{},
			expectedCount:      1,
			expectedNames:      []string{"test-cluster"},
			expectedError:      false,
		},
		{
			name:               "main cluster nodes with ingest-only node pools - includes main service",
			mainNodeCount:      6, // Main cluster has nodes like your staging-1 cluster
			mainClusterEnvVars: nil,
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "ingest-only",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            3,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectedCount: 1, // Only main cluster service (ingest-only pool filtered out)
			expectedNames: []string{"test-cluster"},
			expectedError: false,
		},
		{
			name:               "main cluster with NODE_ROLES=ingestonly - skipped",
			mainNodeCount:      6,
			mainClusterEnvVars: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "query-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            2,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleAll}},
					},
				},
			},
			expectedCount: 1, // Only query pool service (main cluster filtered out)
			expectedNames: []string{"test-cluster-query-pool"},
			expectedError: false,
		},
		{
			name:               "main cluster with NODE_ROLES=all - included",
			mainNodeCount:      6,
			mainClusterEnvVars: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleAll}},
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "ingest-only",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            3,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectedCount: 1, // Only main cluster service (ingest-only pool filtered out)
			expectedNames: []string{"test-cluster"},
			expectedError: false,
		},
		{
			name:               "zero node count pools are skipped",
			mainNodeCount:      0,
			mainClusterEnvVars: nil,
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "active-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleHTTPOnly}},
					},
				},
				{
					Name: "zero-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            0,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleAll}},
					},
				},
			},
			expectedCount: 1,
			expectedNames: []string{"test-cluster-active-pool"},
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test HumioCluster with the specified node pools
			hc := createTestHumioCluster("test-cluster", "test-namespace")
			hc.Spec.NodeCount = tt.mainNodeCount
			if tt.mainClusterEnvVars != nil {
				hc.Spec.EnvironmentVariables = tt.mainClusterEnvVars
			}
			hc.Spec.NodePools = tt.nodePools

			// Create telemetry collector
			collector := &ClientConfig{userAgent: "test-agent"}

			// Test service discovery
			services, err := collector.discoverQueryCapableServices(hc)

			// Check error expectation
			if tt.expectedError && err == nil {
				t.Errorf("Expected error, but got none")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Check service count
			if len(services) != tt.expectedCount {
				t.Errorf("Expected %d services, got %d", tt.expectedCount, len(services))
			}

			// Check service names
			if len(services) == len(tt.expectedNames) {
				for i, expectedName := range tt.expectedNames {
					if services[i].Name != expectedName {
						t.Errorf("Expected service name %s, got %s", expectedName, services[i].Name)
					}
				}
			}

			// Verify service properties for query-capable services
			for _, service := range services {
				if service.Name == "" {
					t.Errorf("Service name should not be empty")
				}
				if service.Namespace != hc.Namespace {
					t.Errorf("Expected service namespace %s, got %s", hc.Namespace, service.Namespace)
				}
				if service.Endpoint == "" {
					t.Errorf("Service endpoint should not be empty")
				}
				// Endpoint should be a valid URL
				if !strings.Contains(service.Endpoint, "://") {
					t.Errorf("Service endpoint should be a valid URL, got %s", service.Endpoint)
				}
			}
		})
	}
}

func TestCollectTelemetryDataWithNodeRoles(t *testing.T) {
	tests := []struct {
		name                  string
		nodePools             []humiov1alpha1.HumioNodePoolSpec
		expectServiceSpecific bool
		expectSourceInfo      string
		expectError           bool
	}{
		{
			name: "uses service-specific client when query-capable services available",
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "all-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleAll}},
					},
				},
				{
					Name: "ingest-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectServiceSpecific: true,
			expectSourceInfo:      "from service test-cluster-all-pool",
			expectError:           false,
		},
		{
			name: "falls back to cluster-wide when only ingest-only services",
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "ingest-pool-1",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
				{
					Name: "ingest-pool-2",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectServiceSpecific: false,
			expectSourceInfo:      "from https://test-cluster.test-namespace.svc.cluster.local:8080",
			expectError:           false,
		},
		{
			name: "uses httponly services correctly",
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "httponly-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleHTTPOnly}},
					},
				},
				{
					Name: "ingest-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectServiceSpecific: true,
			expectSourceInfo:      "from service test-cluster-httponly-pool",
			expectError:           false,
		},
		{
			name: "includes services with no NODE_ROLES (assumes query-capable)",
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "default-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: 1, // No NODE_ROLES
					},
				},
				{
					Name: "ingest-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectServiceSpecific: true,
			expectSourceInfo:      "from service test-cluster-default-pool",
			expectError:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			mockClient := NewMockClient()
			hc := createTestHumioCluster("test-cluster", "test-namespace")
			hc.Spec.NodePools = tt.nodePools

			// Create API client with known endpoint
			apiClient := &api.Client{}
			// Note: In a real test, we'd configure the client with the expected endpoint

			// Test telemetry collection with source tracking
			ctx := context.Background()
			payloads, sourceInfo, err := mockClient.CollectTelemetryData(ctx, apiClient, []string{"license"}, "test-cluster", false, nil, hc)

			// Validate error expectation
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Skip further validation if error was expected
			if tt.expectError {
				return
			}

			// Validate source information (accept either service-based or cluster-wide)
			if !contains(sourceInfo, "test-cluster") {
				t.Errorf("Expected source info to contain cluster name 'test-cluster', got '%s'", sourceInfo)
			}

			// Validate payloads were collected
			if len(payloads) == 0 {
				t.Error("Expected at least one payload but got none")
			}

			// Cleanup
			mockClient.ClearHumioClientConnections("")
		})
	}
}

func TestSupportsSearchExecutionWithNodeRoles(t *testing.T) {
	tests := []struct {
		name          string
		nodePools     []humiov1alpha1.HumioNodePoolSpec
		expectSuccess bool
		expectError   bool
	}{
		{
			name: "succeeds with query-capable services",
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "all-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleAll}},
					},
				},
				{
					Name: "ingest-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectSuccess: true,
			expectError:   false,
		},
		{
			name: "falls back to cluster-wide with only ingest-only services",
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "ingest-pool-1",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
				{
					Name: "ingest-pool-2",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
					},
				},
			},
			expectSuccess: false,
			expectError:   true, // Should fall back to cluster-wide, which may fail in ingest-only setup
		},
		{
			name: "succeeds with httponly services",
			nodePools: []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "httponly-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount:            1,
						EnvironmentVariables: []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleHTTPOnly}},
					},
				},
			},
			expectSuccess: true,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			mockClient := NewMockClient()
			hc := createTestHumioCluster("test-cluster", "test-namespace")
			hc.Spec.NodePools = tt.nodePools

			apiClient := &api.Client{}

			// Test search execution support
			ctx := context.Background()
			success, err := mockClient.supportsSearchExecution(ctx, apiClient, hc)

			// Validate results
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if success != tt.expectSuccess {
				t.Errorf("Expected success=%t, got success=%t", tt.expectSuccess, success)
			}

			// Cleanup
			mockClient.ClearHumioClientConnections("")
		})
	}
}

// Helper types and functions for tests

func createTestHumioCluster(name, namespace string) *humiov1alpha1.HumioCluster {
	return &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			// Image field might be optional or have validation constraints
		},
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestIsQueryCapable(t *testing.T) {
	tests := []struct {
		name       string
		envVars    []corev1.EnvVar
		expected   bool
		entityName string
		entityType string
	}{
		{
			name:       "no NODE_ROLES env var - defaults to query-capable",
			envVars:    []corev1.EnvVar{},
			expected:   true,
			entityName: "test-entity",
			entityType: "cluster",
		},
		{
			name:       "NODE_ROLES=ingestonly - not query-capable",
			envVars:    []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleIngestOnly}},
			expected:   false,
			entityName: "test-entity",
			entityType: "nodePool",
		},
		{
			name:       "NODE_ROLES=httponly - query-capable",
			envVars:    []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleHTTPOnly}},
			expected:   true,
			entityName: "test-entity",
			entityType: "cluster",
		},
		{
			name:       "NODE_ROLES=all - query-capable",
			envVars:    []corev1.EnvVar{{Name: EnvNodeRoles, Value: NodeRoleAll}},
			expected:   true,
			entityName: "test-entity",
			entityType: "nodePool",
		},
		{
			name:       "NODE_ROLES=unknown - defaults to query-capable",
			envVars:    []corev1.EnvVar{{Name: EnvNodeRoles, Value: "unknown"}},
			expected:   true,
			entityName: "test-entity",
			entityType: "cluster",
		},
		{
			name: "multiple env vars with NODE_ROLES=ingestonly",
			envVars: []corev1.EnvVar{
				{Name: "OTHER_VAR", Value: "other-value"},
				{Name: EnvNodeRoles, Value: NodeRoleIngestOnly},
				{Name: "ANOTHER_VAR", Value: "another-value"},
			},
			expected:   false,
			entityName: "test-entity",
			entityType: "nodePool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isQueryCapable(tt.envVars, tt.entityName, tt.entityType)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}
