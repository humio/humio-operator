/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package humio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Node role constants
const (
	NodeRoleIngestOnly = "ingestonly"
	NodeRoleHTTPOnly   = "httponly"
	NodeRoleAll        = "all"
	EnvNodeRoles       = "NODE_ROLES"
)

// CollectLicenseData implements license data collection for telemetry following existing patterns
func (h *ClientConfig) CollectLicenseData(ctx context.Context, client *api.Client, k8sClient client.Client, hc *humiov1alpha1.HumioCluster) (*TelemetryLicenseData, error) {
	// Use the new GetLicenseForTelemetry GraphQL operation
	resp, err := humiographql.GetLicenseForTelemetry(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get license data: %w", err)
	}

	installedLicense := resp.GetInstalledLicense()
	if installedLicense == nil {
		return nil, fmt.Errorf("no license installed")
	}

	licenseData := &TelemetryLicenseData{
		JWTExtractionSuccess: false, // Default to false, will be set to true if JWT extraction succeeds
	}

	ctrl.Log.Info("Starting license data collection",
		"cluster", hc.Name,
		"namespace", hc.Namespace)

	switch v := (*installedLicense).(type) {
	case *humiographql.GetLicenseForTelemetryInstalledLicenseOnPremLicense:
		licenseData.LicenseUID = v.GetUid()
		licenseData.LicenseType = "onprem"
		licenseData.ExpirationDate = v.GetExpiresAt()
		licenseData.IssuedDate = v.GetIssuedAt()
		licenseData.Owner = v.GetOwner()
		licenseData.MaxUsers = v.GetMaxUsers()
		isSaaS := v.GetIsSaaS()
		licenseData.IsSaaS = &isSaaS
		isOem := v.GetIsOem()
		licenseData.IsOem = &isOem

		ctrl.Log.Info("Collected GraphQL license data for OnPrem license",
			"license_uid", licenseData.LicenseUID,
			"owner", licenseData.Owner,
			"max_users", licenseData.MaxUsers,
			"is_saas", *licenseData.IsSaaS,
			"is_oem", *licenseData.IsOem)

		// Extract JWT-exclusive fields from cluster secret
		h.extractJWTLicenseFields(ctx, k8sClient, hc, licenseData)

	case *humiographql.GetLicenseForTelemetryInstalledLicenseTrialLicense:
		licenseData.LicenseType = "trial"
		licenseData.ExpirationDate = v.GetExpiresAt()
		licenseData.IssuedDate = v.GetIssuedAt()
		// Trial licenses don't have UID, owner, etc from GraphQL

		ctrl.Log.Info("Collected GraphQL license data for Trial license",
			"expiration", licenseData.ExpirationDate,
			"issued", licenseData.IssuedDate)

		// Try to extract JWT fields for trial licenses too (they might have limits)
		h.extractJWTLicenseFields(ctx, k8sClient, hc, licenseData)

	default:
		return nil, fmt.Errorf("unknown license type: %T", v)
	}

	ctrl.Log.Info("License data collection completed",
		"cluster", hc.Name,
		"license_type", licenseData.LicenseType,
		"license_uid", licenseData.LicenseUID,
		"jwt_extraction_success", licenseData.JWTExtractionSuccess,
		"max_ingest_gb_per_day", licenseData.MaxIngestGbPerDay,
		"max_cores", licenseData.MaxCores)

	return licenseData, nil
}

// extractJWTLicenseFields attempts to extract JWT-exclusive license fields
// Logs warnings and continues gracefully if extraction fails
func (h *ClientConfig) extractJWTLicenseFields(ctx context.Context, k8sClient client.Client, hc *humiov1alpha1.HumioCluster, licenseData *TelemetryLicenseData) {
	// Attempt to get license JWT from cluster secret
	licenseJWT, err := h.getLicenseJWTFromClusterSecret(ctx, k8sClient, hc)
	if err != nil {
		ctrl.Log.Info("License JWT extraction failed - unable to retrieve license JWT from cluster secret, continuing with GraphQL data only",
			"cluster", hc.Name,
			"namespace", hc.Namespace,
			"error", err.Error(),
			"license_uid", licenseData.LicenseUID,
			"reason", "secret_access_failed")
		return
	}

	ctrl.Log.V(1).Info("Successfully retrieved license JWT from cluster secret, attempting to parse JWT fields",
		"cluster", hc.Name,
		"jwt_length", len(licenseJWT))

	// Extract JWT-exclusive fields
	jwtFields, err := GetJWTLicenseFields(licenseJWT)
	if err != nil {
		ctrl.Log.Error(err, "License JWT parsing failed - unable to extract JWT license fields, continuing with GraphQL data only",
			"cluster", hc.Name,
			"namespace", hc.Namespace,
			"license_uid", licenseData.LicenseUID,
			"reason", "jwt_parsing_failed")
		return
	}

	// Verify JWT UID matches GraphQL UID for OnPrem licenses
	if licenseData.LicenseType == "onprem" && licenseData.LicenseUID != "" {
		if jwtFields.UID != licenseData.LicenseUID {
			ctrl.Log.Error(nil, "License UID mismatch detected - JWT license UID does not match GraphQL license UID, skipping JWT data extraction for security",
				"cluster", hc.Name,
				"namespace", hc.Namespace,
				"graphql_uid", licenseData.LicenseUID,
				"jwt_uid", jwtFields.UID,
				"reason", "uid_mismatch")
			return
		}
	}

	// Successfully extracted JWT fields - populate license data
	licenseData.JWTExtractionSuccess = true
	licenseData.MaxIngestGbPerDay = jwtFields.MaxIngestGbPerDay
	licenseData.MaxCores = jwtFields.MaxCores
	licenseData.LicenseSubject = jwtFields.Subject

	// Convert Unix timestamp to time.Time for license validity
	if jwtFields.ValidUntil != nil {
		validUntil := time.Unix(*jwtFields.ValidUntil, 0)
		licenseData.LicenseValidUntil = &validUntil
	}

	ctrl.Log.Info("License JWT extraction successful - enhanced license data extracted from JWT token",
		"cluster", hc.Name,
		"namespace", hc.Namespace,
		"license_uid", licenseData.LicenseUID,
		"max_ingest_gb_per_day", licenseData.MaxIngestGbPerDay,
		"max_cores", licenseData.MaxCores,
		"license_subject", licenseData.LicenseSubject,
		"license_valid_until", licenseData.LicenseValidUntil)
}

// CollectClusterInfo implements cluster information collection for telemetry
func (h *ClientConfig) CollectClusterInfo(ctx context.Context, client *api.Client) (*TelemetryClusterInfo, error) {
	clusterInfo := &TelemetryClusterInfo{}

	// Get cluster nodes information using existing GetCluster GraphQL operation
	clusterResp, err := humiographql.GetCluster(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster information: %w", err)
	}

	cluster := clusterResp.GetCluster()
	if nodes := cluster.GetNodes(); len(nodes) > 0 {
		clusterInfo.NodeCount = len(nodes)
	}

	// Get version information using existing Status API
	statusResp, err := client.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get version information: %w", err)
	}
	clusterInfo.Version = statusResp.Version

	// TODO: Implement actual user/repository collection via GraphQL

	return clusterInfo, nil
}

// CollectRepositoryID collects the repository ID from the humio repository
// This replaces the cluster identity with the actual repository ID
func (h *ClientConfig) CollectRepositoryID(ctx context.Context, client *api.Client, settings QuerySettings) (string, error) {
	// Query to get the repository ID from the humio repository
	query := api.Query{
		QueryString: `#repo = humio | #type = humio | dataspace="humio" | viewId = * | select(viewId) | head(1)`,
		Start:       "1h", // Look back 1 hour to find recent data
		End:         "",   // Empty means "now"
		Live:        false,
	}

	// Execute the search with timeout
	result, err := client.ExecuteLogScaleSearchWithTimeout(ctx, "humio", query, settings.MaxExecutionTime)
	if err != nil {
		return "", fmt.Errorf("failed to execute repository ID query: %w", err)
	}

	// Extract repository ID from the query result
	if result != nil && len(result.Events) > 0 {
		// Look for viewId field in the first event
		if viewId, ok := result.Events[0]["viewId"].(string); ok && viewId != "" {
			return viewId, nil
		}
	}

	// If no repository ID found, return an error
	return "", fmt.Errorf("repository ID not found in humio repository query results")
}

// CollectTelemetryData collects telemetry data based on the specified data types and returns source information
func (h *ClientConfig) CollectTelemetryData(ctx context.Context, client *api.Client, dataTypes []string, clusterID string, sendCollectionErrors bool, k8sClient client.Client, hc *humiov1alpha1.HumioCluster) ([]TelemetryPayload, string, error) {
	var payloads []TelemetryPayload
	var allErrors []TelemetryError
	timestamp := time.Now()

	// Setup source tracking and client selection
	actualClient, sourceInfo := h.setupClientAndSourceInfo(client, hc)

	// Validate search capabilities
	if err := h.validateSearchCapabilities(ctx, actualClient, hc); err != nil {
		return nil, sourceInfo, err
	}

	// Collect the repository ID using the service-specific client (if available)
	repositoryID, err := h.CollectRepositoryID(ctx, actualClient, QuerySettings{MaxExecutionTime: 30 * time.Second})
	if err != nil {
		return nil, sourceInfo, fmt.Errorf("failed to collect repository ID for telemetry: %w", err)
	}

	ctrl.Log.Info("Successfully collected repository ID for telemetry",
		"repository_id", repositoryID,
		"user_cluster_id", clusterID)

	// Collect data for each requested data type
	for _, dataType := range dataTypes {
		if dataType == "repository_usage" {
			// Handle repository usage specially since it returns multiple flattened payloads
			ctrl.Log.Info("Starting repository_usage collection",
				"cluster_id", clusterID,
				"collection_type", "repository_usage")

			repositoryUsage, err := h.CollectRepositoryUsage(ctx, actualClient, DefaultQuerySettings)
			if err != nil {
				ctrl.Log.Error(err, "Failed to collect repository usage",
					"cluster_id", clusterID)
				allErrors = append(allErrors, TelemetryError{
					Type:      TelemetryErrorTypeCollection,
					Message:   fmt.Sprintf("Failed to collect repository usage: %v", err),
					Timestamp: timestamp,
				})
			} else {
				ctrl.Log.Info("Successfully collected repository usage",
					"cluster_id", clusterID,
					"total_repositories", repositoryUsage.TotalRepositories)

				// Flatten repository usage into multiple separate events
				flattenedPayloads := h.FlattenRepositoryUsageData(timestamp, clusterID, repositoryID, repositoryUsage, []TelemetryError{})
				payloads = append(payloads, flattenedPayloads...)
			}
		} else {
			payload, errors := h.collectSingleDataType(ctx, dataType, actualClient, k8sClient, hc, timestamp, clusterID, repositoryID)
			if payload != nil {
				payloads = append(payloads, *payload)
			}
			allErrors = append(allErrors, errors...)
		}
	}

	return h.finalizePayloads(payloads, allErrors, timestamp, clusterID, repositoryID, sendCollectionErrors, sourceInfo)
}

// setupClientAndSourceInfo sets up the client and source tracking information
func (h *ClientConfig) setupClientAndSourceInfo(client *api.Client, hc *humiov1alpha1.HumioCluster) (*api.Client, string) {
	// Determine source information for collection tracking
	sourceURL := "cluster-wide-service"
	sourceService := ""

	// Try to get the actual endpoint from the API client
	if client != nil {
		config := client.Config()
		if config.Address != nil {
			sourceURL = config.Address.String()
		}
	}

	// Try to discover query-capable services to get more specific source info
	queryCapableServices, err := h.discoverQueryCapableServices(hc)
	if err == nil && len(queryCapableServices) > 0 {
		// We're using service-aware collection, update source information
		firstService := queryCapableServices[0]
		sourceURL = firstService.Endpoint
		sourceService = firstService.Name

		ctrl.Log.Info("Using service-aware telemetry collection",
			"service", sourceService,
			"endpoint", sourceURL,
			"total_query_capable_services", len(queryCapableServices))
	} else {
		ctrl.Log.Info("Using cluster-wide telemetry collection",
			"endpoint", sourceURL,
			"service_discovery_error", err)
	}

	// Format source info for return
	sourceInfo := fmt.Sprintf("from %s", sourceURL)
	if sourceService != "" {
		sourceInfo = fmt.Sprintf("from service %s (%s)", sourceService, sourceURL)
	}

	// Create service-specific client if we discovered query-capable services
	actualClient := client
	if len(queryCapableServices) > 0 {
		serviceClient, err := h.createServiceSpecificClient(queryCapableServices[0], hc, client)
		if err != nil {
			ctrl.Log.Error(err, "Failed to create service-specific client, using cluster-wide client as fallback",
				"service", queryCapableServices[0].Name)
			// Continue with original client as fallback
		} else {
			actualClient = serviceClient
			ctrl.Log.Info("Using service-specific client for telemetry collection",
				"service", queryCapableServices[0].Name)
		}
	}

	return actualClient, sourceInfo
}

// validateSearchCapabilities validates that search execution is supported
func (h *ClientConfig) validateSearchCapabilities(ctx context.Context, actualClient *api.Client, hc *humiov1alpha1.HumioCluster) error {
	searchSupported, err := h.supportsSearchExecution(ctx, actualClient, hc)
	if err != nil {
		return fmt.Errorf("LogScale cluster does not support search execution required for telemetry collection: %w", err)
	}
	if !searchSupported {
		return fmt.Errorf("LogScale cluster search capability check failed - search execution is required for advanced telemetry data types (ingestion_metrics, repository_usage, user_activity, detailed_analytics)")
	}
	return nil
}

// collectSingleDataType collects data for a single telemetry data type
func (h *ClientConfig) collectSingleDataType(ctx context.Context, dataType string, actualClient *api.Client, k8sClient client.Client, hc *humiov1alpha1.HumioCluster, timestamp time.Time, clusterID string, repositoryID string) (*TelemetryPayload, []TelemetryError) {
	var payload *TelemetryPayload
	var collectionErrors []TelemetryError

	switch dataType {
	case "license":
		payload, collectionErrors = h.collectLicenseData(ctx, actualClient, k8sClient, hc, timestamp, clusterID, repositoryID)
	case "cluster_info":
		payload, collectionErrors = h.collectClusterInfoData(ctx, actualClient, timestamp, clusterID, repositoryID)
	case "ingestion_metrics":
		payload, collectionErrors = h.collectIngestionMetricsData(ctx, actualClient, timestamp, clusterID, repositoryID)
	case "user_activity":
		payload, collectionErrors = h.collectUserActivityData(ctx, actualClient, timestamp, clusterID, repositoryID)
	case "detailed_analytics":
		payload, collectionErrors = h.collectDetailedAnalyticsData(ctx, actualClient, timestamp, clusterID, repositoryID)
	default:
		collectionErrors = append(collectionErrors, TelemetryError{
			Type:      TelemetryErrorTypeConfiguration,
			Message:   fmt.Sprintf("Unknown data type: %s", dataType),
			Timestamp: timestamp,
		})
	}

	return payload, collectionErrors
}

// collectLicenseData collects license telemetry data
func (h *ClientConfig) collectLicenseData(ctx context.Context, actualClient *api.Client, k8sClient client.Client, hc *humiov1alpha1.HumioCluster, timestamp time.Time, clusterID string, repositoryID string) (*TelemetryPayload, []TelemetryError) {
	var collectionErrors []TelemetryError

	licenseData, err := h.CollectLicenseData(ctx, actualClient, k8sClient, hc)
	if err != nil {
		collectionErrors = append(collectionErrors, TelemetryError{
			Type:      TelemetryErrorTypeCollection,
			Message:   fmt.Sprintf("Failed to collect license data: %v", err),
			Timestamp: timestamp,
		})
		return nil, collectionErrors
	}

	payload := &TelemetryPayload{
		Timestamp:        timestamp,
		ClusterID:        clusterID,
		ClusterGUID:      repositoryID,
		CollectionType:   TelemetryCollectionTypeLicense,
		SourceType:       TelemetrySourceTypeJSON,
		Data:             licenseData,
		CollectionErrors: collectionErrors,
	}
	return payload, collectionErrors
}

// collectClusterInfoData collects cluster info telemetry data
func (h *ClientConfig) collectClusterInfoData(ctx context.Context, actualClient *api.Client, timestamp time.Time, clusterID string, repositoryID string) (*TelemetryPayload, []TelemetryError) {
	var collectionErrors []TelemetryError

	clusterInfo, err := h.CollectClusterInfo(ctx, actualClient)
	if err != nil {
		collectionErrors = append(collectionErrors, TelemetryError{
			Type:      TelemetryErrorTypeCollection,
			Message:   fmt.Sprintf("Failed to collect cluster info: %v", err),
			Timestamp: timestamp,
		})
		return nil, collectionErrors
	}

	payload := &TelemetryPayload{
		Timestamp:        timestamp,
		ClusterID:        clusterID,
		ClusterGUID:      repositoryID,
		CollectionType:   TelemetryCollectionTypeClusterInfo,
		SourceType:       TelemetrySourceTypeJSON,
		Data:             clusterInfo,
		CollectionErrors: collectionErrors,
	}
	return payload, collectionErrors
}

// collectIngestionMetricsData collects ingestion metrics telemetry data
func (h *ClientConfig) collectIngestionMetricsData(ctx context.Context, actualClient *api.Client, timestamp time.Time, clusterID string, repositoryID string) (*TelemetryPayload, []TelemetryError) {
	var collectionErrors []TelemetryError

	ctrl.Log.Info("Starting ingestion_metrics collection",
		"cluster_id", clusterID,
		"collection_type", "ingestion_metrics")

	// Check if organizational usage data is available (required for ingestion metrics)
	usageAvailable, err := h.checkUsageDataAvailability(ctx, actualClient)
	if err != nil {
		ctrl.Log.Error(err, "Failed to check organizational usage data availability for ingestion metrics",
			"cluster_id", clusterID)
		collectionErrors = append(collectionErrors, TelemetryError{
			Type:      TelemetryErrorTypeCollection,
			Message:   fmt.Sprintf("Cannot check organizational usage data availability for ingestion metrics: %v", err),
			Timestamp: timestamp,
		})
		return nil, collectionErrors
	} else if !usageAvailable {
		ctrl.Log.Info("Organizational usage data not available - skipping ingestion metrics collection",
			"cluster_id", clusterID,
			"message", "LogScale organizational usage job is required for ingestion metrics but no data found in last 24h")
		collectionErrors = append(collectionErrors, TelemetryError{
			Type:      TelemetryErrorTypeCollection,
			Message:   "Ingestion metrics require LogScale organizational usage data but none found in last 24 hours. Please ensure the organizational usage job is configured and running.",
			Timestamp: timestamp,
		})
		return nil, collectionErrors
	}

	// Usage data is available, proceed with ingestion metrics collection
	ingestionMetrics, err := h.CollectIngestionMetrics(ctx, actualClient, DefaultQuerySettings)
	if err != nil {
		ctrl.Log.Error(err, "Failed to collect ingestion metrics",
			"cluster_id", clusterID)
		collectionErrors = append(collectionErrors, TelemetryError{
			Type:      TelemetryErrorTypeCollection,
			Message:   fmt.Sprintf("Failed to collect ingestion metrics: %v", err),
			Timestamp: timestamp,
		})
		return nil, collectionErrors
	}

	ctrl.Log.Info("Successfully collected ingestion metrics",
		"cluster_id", clusterID,
		"daily_volume_gb", ingestionMetrics.Daily.IngestVolumeGB,
		"daily_events", ingestionMetrics.Daily.EventCount)

	payload := &TelemetryPayload{
		Timestamp:        timestamp,
		ClusterID:        clusterID,
		ClusterGUID:      repositoryID,
		CollectionType:   TelemetryCollectionTypeIngestionMetrics,
		SourceType:       TelemetrySourceTypeJSON,
		Data:             ingestionMetrics,
		CollectionErrors: collectionErrors,
	}
	return payload, collectionErrors
}

// collectUserActivityData collects user activity telemetry data
func (h *ClientConfig) collectUserActivityData(ctx context.Context, actualClient *api.Client, timestamp time.Time, clusterID string, repositoryID string) (*TelemetryPayload, []TelemetryError) {
	var collectionErrors []TelemetryError

	ctrl.Log.Info("Starting user_activity collection",
		"cluster_id", clusterID,
		"collection_type", "user_activity")

	userActivity, err := h.CollectUserActivity(ctx, actualClient, DefaultQuerySettings)
	if err != nil {
		ctrl.Log.Error(err, "Failed to collect user activity",
			"cluster_id", clusterID)
		collectionErrors = append(collectionErrors, TelemetryError{
			Type:      TelemetryErrorTypeCollection,
			Message:   fmt.Sprintf("Failed to collect user activity: %v", err),
			Timestamp: timestamp,
		})
		return nil, collectionErrors
	}

	ctrl.Log.Info("Successfully collected user activity",
		"cluster_id", clusterID,
		"active_users_24h", userActivity.ActiveUsers.Last24h,
		"total_queries", userActivity.QueryActivity.TotalQueries)

	payload := &TelemetryPayload{
		Timestamp:        timestamp,
		ClusterID:        clusterID,
		ClusterGUID:      repositoryID,
		CollectionType:   TelemetryCollectionTypeUserActivity,
		SourceType:       TelemetrySourceTypeJSON,
		Data:             userActivity,
		CollectionErrors: collectionErrors,
	}
	return payload, collectionErrors
}

// collectDetailedAnalyticsData collects detailed analytics telemetry data
func (h *ClientConfig) collectDetailedAnalyticsData(ctx context.Context, actualClient *api.Client, timestamp time.Time, clusterID string, repositoryID string) (*TelemetryPayload, []TelemetryError) {
	var collectionErrors []TelemetryError

	ctrl.Log.Info("Starting detailed_analytics collection",
		"cluster_id", clusterID,
		"collection_type", "detailed_analytics")

	detailedAnalytics, err := h.CollectDetailedAnalytics(ctx, actualClient, DefaultQuerySettings)
	if err != nil {
		ctrl.Log.Error(err, "Failed to collect detailed analytics",
			"cluster_id", clusterID)
		collectionErrors = append(collectionErrors, TelemetryError{
			Type:      TelemetryErrorTypeCollection,
			Message:   fmt.Sprintf("Failed to collect detailed analytics: %v", err),
			Timestamp: timestamp,
		})
		return nil, collectionErrors
	}

	ctrl.Log.Info("Successfully collected detailed analytics",
		"cluster_id", clusterID,
		"performance_metrics_count", len(detailedAnalytics.PerformanceMetrics),
		"usage_patterns_count", len(detailedAnalytics.UsagePatterns))

	payload := &TelemetryPayload{
		Timestamp:        timestamp,
		ClusterID:        clusterID,
		ClusterGUID:      repositoryID,
		CollectionType:   TelemetryCollectionTypeDetailedAnalytics,
		SourceType:       TelemetrySourceTypeJSON,
		Data:             detailedAnalytics,
		CollectionErrors: collectionErrors,
	}
	return payload, collectionErrors
}

// finalizePayloads handles error payloads and final validation
func (h *ClientConfig) finalizePayloads(payloads []TelemetryPayload, allErrors []TelemetryError, timestamp time.Time, clusterID string, repositoryID string, sendCollectionErrors bool, sourceInfo string) ([]TelemetryPayload, string, error) {
	// If we have collection errors and sendCollectionErrors is enabled, create a payload to carry them
	if len(allErrors) > 0 && sendCollectionErrors {
		errorPayload := TelemetryPayload{
			Timestamp:        timestamp,
			ClusterID:        clusterID,
			ClusterGUID:      repositoryID,
			CollectionType:   TelemetryCollectionTypeCollectionErrors,
			SourceType:       TelemetrySourceTypeJSON,
			Data:             map[string]interface{}{"collection_error_count": len(allErrors)},
			CollectionErrors: allErrors,
		}
		payloads = append(payloads, errorPayload)
	}

	// If we have errors but no successful payloads, return the errors
	if len(allErrors) > 0 && (len(payloads) == 0 || (len(payloads) == 1 && payloads[0].CollectionType == TelemetryCollectionTypeCollectionErrors)) {
		return nil, sourceInfo, fmt.Errorf("failed to collect any telemetry data: %d errors occurred", len(allErrors))
	}

	return payloads, sourceInfo, nil
}

// FlattenRepositoryUsageData converts repository usage data into multiple separate events
// instead of sending arrays. Each repository becomes its own event with flattened fields.
func (h *ClientConfig) FlattenRepositoryUsageData(timestamp time.Time, clusterID string, repositoryID string, repositoryUsage *TelemetryRepositoryUsageMetrics, collectionErrors []TelemetryError) []TelemetryPayload {
	// Pre-allocate slice with estimated capacity (1 summary + repos + top repos)
	estimatedCapacity := 1 + len(repositoryUsage.Repositories) + len(repositoryUsage.TopRepositories)
	payloads := make([]TelemetryPayload, 0, estimatedCapacity)

	// Create a summary event with aggregate information
	summaryData := map[string]interface{}{
		"total_repositories": repositoryUsage.TotalRepositories,
		"event_type":         "repository_usage_summary",
	}

	// Add error tracking to summary data
	if len(collectionErrors) > 0 {
		summaryData["collector_errors"] = true
		errorMessages := make([]string, len(collectionErrors))
		for i, err := range collectionErrors {
			errorMessages[i] = err.Message
		}
		summaryData["collector_error_messages"] = errorMessages
	} else {
		summaryData["collector_errors"] = false
	}

	summaryPayload := TelemetryPayload{
		Timestamp:        timestamp,
		ClusterID:        clusterID,    // This is the user-provided clusterIdentifier
		ClusterGUID:      repositoryID, // This is the LogScale repository ID
		CollectionType:   TelemetryCollectionTypeRepositoryUsage,
		SourceType:       TelemetrySourceTypeJSON,
		Data:             summaryData,
		CollectionErrors: collectionErrors,
	}
	payloads = append(payloads, summaryPayload)

	// Create individual events for each repository
	for _, repo := range repositoryUsage.Repositories {
		repoData := map[string]interface{}{
			"name":                 repo.Name,
			"ingest_volume_gb_24h": repo.IngestVolumeGB24h,
			"event_count_24h":      repo.EventCount24h,
			"retention_days":       repo.RetentionDays,
			"storage_usage_gb":     repo.StorageUsageGB,
			"last_activity_time":   repo.LastActivityTime,
			"event_type":           "repository_usage_detail",
		}

		// Add error tracking to individual repository data
		if len(collectionErrors) > 0 {
			repoData["collector_errors"] = true
			errorMessages := make([]string, len(collectionErrors))
			for i, err := range collectionErrors {
				errorMessages[i] = err.Message
			}
			repoData["collector_error_messages"] = errorMessages
		} else {
			repoData["collector_errors"] = false
		}

		// Add dataspace as a single string value
		if repo.Dataspace != "" {
			repoData["dataspace"] = repo.Dataspace
		}

		repoPayload := TelemetryPayload{
			Timestamp:        timestamp,
			ClusterID:        clusterID,    // This is the user-provided clusterIdentifier
			ClusterGUID:      repositoryID, // This is the LogScale repository ID
			CollectionType:   TelemetryCollectionTypeRepositoryUsage,
			SourceType:       TelemetrySourceTypeJSON,
			Data:             repoData,
			CollectionErrors: collectionErrors,
		}
		payloads = append(payloads, repoPayload)
	}

	// Create individual events for top repositories (if different from all repositories)
	// Mark them with a different event_type to distinguish them
	for _, topRepo := range repositoryUsage.TopRepositories {
		topRepoData := map[string]interface{}{
			"name":                 topRepo.Name,
			"ingest_volume_gb_24h": topRepo.IngestVolumeGB24h,
			"event_count_24h":      topRepo.EventCount24h,
			"retention_days":       topRepo.RetentionDays,
			"storage_usage_gb":     topRepo.StorageUsageGB,
			"last_activity_time":   topRepo.LastActivityTime,
			"event_type":           "top_repository_usage",
		}

		// Add error tracking to top repository data
		if len(collectionErrors) > 0 {
			topRepoData["collector_errors"] = true
			errorMessages := make([]string, len(collectionErrors))
			for i, err := range collectionErrors {
				errorMessages[i] = err.Message
			}
			topRepoData["collector_error_messages"] = errorMessages
		} else {
			topRepoData["collector_errors"] = false
		}

		if topRepo.Dataspace != "" {
			topRepoData["dataspace"] = topRepo.Dataspace
		}

		topRepoPayload := TelemetryPayload{
			Timestamp:        timestamp,
			ClusterID:        clusterID,    // This is the user-provided clusterIdentifier
			ClusterGUID:      repositoryID, // This is the LogScale repository ID
			CollectionType:   TelemetryCollectionTypeRepositoryUsage,
			SourceType:       TelemetrySourceTypeJSON,
			Data:             topRepoData,
			CollectionErrors: collectionErrors,
		}
		payloads = append(payloads, topRepoPayload)
	}

	return payloads
}

// Search-based telemetry collection methods

// Predefined LogScale queries for telemetry collection
const (
	// IngestionMetricsQuery collects ingestion metrics for the past 30 days
	IngestionMetricsQuery = `#sampleRate = hour #sampleType = organization |
groupBy([orgId, orgName], function=[
{ingestLast30Days := sum(segmentWriteBytes)},
{processedEventsSize := sum(processedEventsSize)},
{removedFieldsSize := sum(removedFieldsSize)},
{ingestAfterFieldRemovalSize := sum(ingestAfterFieldRemovalSize)},
{falconSegmentWriteBytes := sum(falconSegmentWriteBytes)},
{falconIngestAfterFieldRemovalSize := sum(falconIngestAfterFieldRemovalSize)},
{selectLast([subscription, @timestamp, contractedDailyIngestBase10, contractedDailyIngest, storageSize, falconStorageSize, contractedRetention,measurementPoint,cid])}]) |
averageDailyIngestLast30Days := (ingestLast30Days / 30) |
processedEventsSize := (processedEventsSize / 30) |
removedFieldsSize := (removedFieldsSize / 30) |
ingestAfterFieldRemovalSize := (ingestAfterFieldRemovalSize / 30) |
falconSegmentWriteBytes := (falconSegmentWriteBytes / 30) |
falconIngestAfterFieldRemovalSize := (falconIngestAfterFieldRemovalSize / 30) |
currentTime :=  now() |
age := (currentTime - @timestamp) |
writeJson([orgId, orgName, averageDailyIngestLast30Days, contractedDailyIngestBase10, contractedDailyIngest, subscription, storageSize, falconStorageSize, contractedRetention,cloud,processedEventsSize,removedFieldsSize,ingestAfterFieldRemovalSize,falconSegmentWriteBytes,falconIngestAfterFieldRemovalSize,measurementPoint,cid], as=rawstring) |
timestamp := formatTime("%Y-%m-%dT%H:%M:%SZ",field=currentTime, timezone=Z) |
table([timestamp, rawstring], limit=7500)`

	// RepositoryUsageQuery is a placeholder for repository usage metrics
	RepositoryUsageQuery = `| head(10) | table([@timestamp, @rawstring])`

	// UserActivityQuery is a placeholder for user activity metrics
	UserActivityQuery = `| head(10) | table([@timestamp, @rawstring])`

	// DetailedAnalyticsQuery is a placeholder for detailed analytics
	DetailedAnalyticsQuery = `| head(10) | table([@timestamp, @rawstring])`
)

// QueryCapableService represents a service for a node pool that can handle search queries
type QueryCapableService struct {
	Name         string // Service name
	Namespace    string // Service namespace
	NodePoolName string // Node pool name
	Endpoint     string // Full endpoint URL for the service
}

// isQueryCapable checks if a set of environment variables indicates query capability
// Returns true if NODE_ROLES is unset (default), "httponly", or "all"
// Returns false if NODE_ROLES is "ingestonly"
// entityName is used for logging (e.g., cluster name or node pool name)
// entityType is used for logging (e.g., "cluster" or "nodePool")
func isQueryCapable(envVars []corev1.EnvVar, entityName, entityType string) bool {
	// Default to query-capable if no NODE_ROLES is specified
	queryCapable := true

	for _, envVar := range envVars {
		if envVar.Name == EnvNodeRoles {
			switch envVar.Value {
			case NodeRoleIngestOnly:
				queryCapable = false
				ctrl.Log.V(1).Info("Entity is ingest-only, skipping",
					entityType, entityName,
					"nodeRoles", envVar.Value)
			case NodeRoleHTTPOnly, NodeRoleAll:
				queryCapable = true
				ctrl.Log.V(1).Info("Entity is query-capable",
					entityType, entityName,
					"nodeRoles", envVar.Value)
			default:
				ctrl.Log.V(1).Info("Unknown NODE_ROLES value, assuming query-capable",
					entityType, entityName,
					"nodeRoles", envVar.Value)
			}
			break
		}
	}

	return queryCapable
}

// discoverQueryCapableServices finds all services for node pools that can handle search queries
// Filters out node pools with NODE_ROLES=ingestonly and includes httponly, all, or unspecified roles
func (h *ClientConfig) discoverQueryCapableServices(hc *humiov1alpha1.HumioCluster) ([]QueryCapableService, error) {
	if hc == nil {
		return nil, fmt.Errorf("HumioCluster cannot be nil")
	}

	ctrl.Log.Info("Discovering query-capable node pool services",
		"cluster", hc.Name,
		"namespace", hc.Namespace,
		"total_nodepools", len(hc.Spec.NodePools))

	var queryCapableServices []QueryCapableService

	// Always check if main cluster has nodes and add main service if it does
	if hc.Spec.NodeCount > 0 {
		if isQueryCapable(hc.Spec.EnvironmentVariables, hc.Name, "cluster") {
			protocol := "https"
			if !h.getTLSEnabledForCluster(hc) {
				protocol = "http"
			}
			mainService := QueryCapableService{
				Name:         hc.Name, // Main cluster service typically uses cluster name
				Namespace:    hc.Namespace,
				NodePoolName: hc.Name, // Use cluster name as "node pool name" for main service
				Endpoint:     fmt.Sprintf("%s://%s.%s.svc.cluster.local:8080", protocol, hc.Name, hc.Namespace),
			}
			queryCapableServices = append(queryCapableServices, mainService)
			ctrl.Log.Info("Added main cluster service for main cluster nodes",
				"service", mainService.Name,
				"nodeCount", hc.Spec.NodeCount,
				"endpoint", mainService.Endpoint)
		}
	}

	// If no node pools are defined, we're done (main service already added above if needed)
	if len(hc.Spec.NodePools) == 0 {
		return queryCapableServices, nil
	}

	// Check each node pool to see if it's query-capable
	for _, nodePool := range hc.Spec.NodePools {
		// Skip node pools with no nodes
		if nodePool.NodeCount == 0 {
			ctrl.Log.V(1).Info("Skipping node pool with zero nodes",
				"nodePool", nodePool.Name)
			continue
		}

		// Check NODE_ROLES environment variable
		if isQueryCapable(nodePool.EnvironmentVariables, nodePool.Name, "nodePool") {
			serviceName := fmt.Sprintf("%s-%s", hc.Name, nodePool.Name)
			protocol := "https"
			if !h.getTLSEnabledForCluster(hc) {
				protocol = "http"
			}
			service := QueryCapableService{
				Name:         serviceName,
				Namespace:    hc.Namespace,
				NodePoolName: nodePool.Name,
				Endpoint:     fmt.Sprintf("%s://%s.%s.svc.cluster.local:8080", protocol, serviceName, hc.Namespace),
			}
			queryCapableServices = append(queryCapableServices, service)
			ctrl.Log.V(1).Info("Added query-capable service",
				"service", service.Name,
				"nodePool", nodePool.Name,
				"endpoint", service.Endpoint)
		}
	}

	ctrl.Log.Info("Discovered query-capable services",
		"cluster", hc.Name,
		"total_services", len(queryCapableServices),
		"services", func() []string {
			var names []string
			for _, svc := range queryCapableServices {
				names = append(names, svc.Name)
			}
			return names
		}())

	return queryCapableServices, nil
}

// getTLSEnabledForCluster determines if TLS is enabled for the cluster
// This is a helper function to avoid circular dependencies with the helpers package
func (h *ClientConfig) getTLSEnabledForCluster(hc *humiov1alpha1.HumioCluster) bool {
	// Simple check - if TLS is not explicitly disabled, assume it's enabled
	// This matches the behavior in helpers/clusterinterface.go
	return hc.Spec.TLS == nil || hc.Spec.TLS.Enabled == nil || *hc.Spec.TLS.Enabled
}

// createServiceSpecificClient creates an API client for a specific service endpoint
// This enables direct communication with query-capable services rather than cluster-wide services
func (h *ClientConfig) createServiceSpecificClient(service QueryCapableService, hc *humiov1alpha1.HumioCluster, existingClient *api.Client) (*api.Client, error) {
	// Parse the service endpoint URL
	serviceURL, err := url.Parse(service.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse service endpoint '%s': %w", service.Endpoint, err)
	}

	// Create API configuration for the specific service endpoint
	config := &api.Config{
		Address:   serviceURL,
		UserAgent: h.userAgent,
		Insecure:  !h.getTLSEnabledForCluster(hc),
	}

	// Copy authentication token from the existing client
	if existingClient != nil {
		config.Token = existingClient.Token()
	}

	// Create the API client with the service-specific configuration
	return api.NewClient(*config), nil
}

// supportsSearchExecutionClusterWide checks if the LogScale cluster supports search execution
// This is the fallback version that uses cluster-wide endpoint
func (h *ClientConfig) supportsSearchExecutionClusterWide(ctx context.Context, client *api.Client) (bool, error) {
	// Test search capability by attempting a simple query on the "humio" repository
	// Use string time formats like the CLI does
	testQuery := api.Query{
		QueryString: "| head(1)",
		Start:       "1m", // Use relative time string like CLI
		End:         "",   // Empty end means "now"
		Live:        false,
	}

	// Log the capability check attempt
	ctrl.Log.Info("Testing LogScale search execution capability (legacy cluster-wide)",
		"repository", "humio",
		"query", testQuery.QueryString,
		"start", testQuery.Start,
		"end", testQuery.End)

	result, err := client.ExecuteLogScaleSearch(ctx, "humio", testQuery)
	if err != nil {
		ctrl.Log.Error(err, "LogScale search execution capability check failed (legacy)",
			"repository", "humio",
			"query", testQuery.QueryString,
			"start", testQuery.Start,
			"end", testQuery.End,
			"error_type", fmt.Sprintf("%T", err))
		return false, fmt.Errorf("search capability check failed on repository 'humio': %w", err)
	}

	// Additional validation on the result
	if result == nil {
		ctrl.Log.Error(nil, "LogScale search returned nil result (legacy)",
			"repository", "humio",
			"query", testQuery.QueryString)
		return false, fmt.Errorf("search capability check returned nil result")
	}

	ctrl.Log.Info("LogScale search execution capability confirmed (legacy)",
		"repository", "humio",
		"result_events", len(result.Events),
		"result_done", result.Done)

	return true, nil
}

// supportsSearchExecution checks if the LogScale cluster supports search execution
// Uses service-level validation to target query-capable services
func (h *ClientConfig) supportsSearchExecution(ctx context.Context, client *api.Client, hc *humiov1alpha1.HumioCluster) (bool, error) {
	// Try service-aware approach first
	queryCapableServices, err := h.discoverQueryCapableServices(hc)
	if err != nil {
		ctrl.Log.Error(err, "Failed to discover query-capable services, falling back to cluster-wide approach")
		return h.supportsSearchExecutionClusterWide(ctx, client)
	}

	if len(queryCapableServices) == 0 {
		ctrl.Log.Info("No query-capable services found, falling back to cluster-wide approach")
		return h.supportsSearchExecutionClusterWide(ctx, client)
	}

	ctrl.Log.Info("Testing search execution capability using query-capable services",
		"service_count", len(queryCapableServices))

	// Test search capability on the first available query-capable service
	testService := queryCapableServices[0]

	// Create a service-specific client for testing
	serviceClient, err := h.createServiceSpecificClient(testService, hc, client)
	if err != nil {
		ctrl.Log.Error(err, "Failed to create service-specific client, falling back to cluster-wide approach",
			"service", testService.Name)
		return h.supportsSearchExecutionClusterWide(ctx, client)
	}

	// Test search capability by attempting a simple query on the "humio" repository
	testQuery := api.Query{
		QueryString: "| head(1)",
		Start:       "1m", // Use relative time string like CLI
		End:         "",   // Empty end means "now"
		Live:        false,
	}

	ctrl.Log.Info("Testing LogScale search execution capability on specific service",
		"service", testService.Name,
		"endpoint", testService.Endpoint,
		"repository", "humio",
		"query", testQuery.QueryString,
		"start", testQuery.Start,
		"end", testQuery.End)

	result, err := serviceClient.ExecuteLogScaleSearch(ctx, "humio", testQuery)
	if err != nil {
		ctrl.Log.Error(err, "LogScale search execution capability check failed on service",
			"service", testService.Name,
			"endpoint", testService.Endpoint,
			"repository", "humio",
			"query", testQuery.QueryString,
			"error_type", fmt.Sprintf("%T", err))
		return false, fmt.Errorf("search capability check failed on repository 'humio' using service '%s': %w", testService.Name, err)
	}

	// Additional validation on the result
	if result == nil {
		ctrl.Log.Error(nil, "LogScale search returned nil result from service",
			"service", testService.Name,
			"repository", "humio",
			"query", testQuery.QueryString)
		return false, fmt.Errorf("search capability check returned nil result from service '%s'", testService.Name)
	}

	ctrl.Log.Info("LogScale search execution capability confirmed using service",
		"service", testService.Name,
		"endpoint", testService.Endpoint,
		"repository", "humio",
		"result_events", len(result.Events),
		"result_done", result.Done)

	return true, nil
}

// checkUsageDataAvailability verifies that organizational usage data is available in humio-usage repository
// This checks if the LogScale organizational usage job is running and populating data
func (h *ClientConfig) checkUsageDataAvailability(ctx context.Context, client *api.Client) (bool, error) {
	// Check for organizational usage data over the last 24 hours
	testQuery := api.Query{
		QueryString: "#sampleRate = hour #sampleType = organization | count()",
		Start:       "24h", // Check last 24 hours
		End:         "",    // Empty end means "now"
		Live:        false,
	}

	// Log the usage data availability check attempt
	ctrl.Log.Info("Checking organizational usage data availability",
		"repository", "humio-usage",
		"query", testQuery.QueryString,
		"start", testQuery.Start,
		"timeframe", "24 hours")

	result, err := client.ExecuteLogScaleSearchWithTimeout(ctx, "humio-usage", testQuery, 30*time.Second)
	if err != nil {
		ctrl.Log.Error(err, "Failed to check organizational usage data availability",
			"repository", "humio-usage",
			"query", testQuery.QueryString,
			"start", testQuery.Start,
			"error_type", fmt.Sprintf("%T", err))
		return false, fmt.Errorf("failed to execute usage data availability check: %w", err)
	}

	if result == nil {
		ctrl.Log.Error(fmt.Errorf("nil result"), "Usage data availability check returned nil result",
			"repository", "humio-usage",
			"query", testQuery.QueryString)
		return false, fmt.Errorf("usage data availability check returned nil result")
	}

	// Check if we have any organizational usage data
	if len(result.Events) == 0 {
		ctrl.Log.Info("No organizational usage data found in last 24 hours",
			"repository", "humio-usage",
			"result_events", len(result.Events),
			"message", "LogScale organizational usage job may not be running or configured")
		return false, nil
	}

	// Check the count result - if it's 0, no organizational data is available
	if len(result.Events) > 0 {
		if countValue, exists := result.Events[0]["_count"]; exists {
			if count, ok := countValue.(float64); ok && count == 0 {
				ctrl.Log.Info("Organizational usage count is zero in last 24 hours",
					"repository", "humio-usage",
					"count", count,
					"message", "LogScale organizational usage job is not generating data")
				return false, nil
			}
			if count, ok := countValue.(float64); ok && count > 0 {
				ctrl.Log.Info("Organizational usage data confirmed available",
					"repository", "humio-usage",
					"count", count,
					"timeframe", "last 24 hours")
				return true, nil
			}
		}
	}

	// If we can't determine the count, assume data is not available
	ctrl.Log.Info("Could not determine organizational usage data count",
		"repository", "humio-usage",
		"result_events", len(result.Events),
		"message", "Assuming organizational usage job is not active")
	return false, nil
}

// CollectIngestionMetrics collects ingestion volume and event metrics via search queries
func (h *ClientConfig) CollectIngestionMetrics(ctx context.Context, client *api.Client, settings QuerySettings) (*TelemetryIngestionMetrics, error) {
	// Calculate time ranges based on settings (for result structure, not query)
	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour) // 30 days back

	query := api.Query{
		QueryString: IngestionMetricsQuery,
		Start:       "30d", // Use relative time string like CLI
		End:         "",    // Empty means "now"
		Live:        false,
	}

	// Execute the search with timeout
	result, err := client.ExecuteLogScaleSearchWithTimeout(ctx, "humio", query, settings.MaxExecutionTime)
	if err != nil {
		return nil, fmt.Errorf("failed to execute ingestion metrics query: %w", err)
	}

	// Parse the result and create metrics
	metrics := &TelemetryIngestionMetrics{
		TimeRange: struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		}{Start: startTime, End: endTime},
		RawQueryResult: SanitizeTimestamps(result),
	}

	// Extract metrics from query results
	if len(result.Events) > 0 {
		// Parse the JSON result from the LogScale query
		if rawString, ok := result.Events[0]["rawstring"].(string); ok {
			h.parseIngestionMetricsResult(rawString, metrics)
		}
	}

	return metrics, nil
}

// parseIngestionMetricsResult parses the JSON result from LogScale query into metrics
func (h *ClientConfig) parseIngestionMetricsResult(rawString string, metrics *TelemetryIngestionMetrics) {
	// Parse the JSON result from the query
	// The query returns JSON like:
	// {"orgId":"SINGLE_ORGANIZATION_ID","orgName":"Organization Name","averageDailyIngestLast30Days":5.226325372986667E11,...}

	// Try to parse the JSON result
	var queryResult map[string]interface{}
	if err := json.Unmarshal([]byte(rawString), &queryResult); err == nil {
		// Successfully parsed JSON - extract all metrics fields

		// Organization identification
		if orgID, ok := queryResult["orgId"].(string); ok {
			metrics.OrgID = orgID
		}
		if orgName, ok := queryResult["orgName"].(string); ok {
			metrics.OrgName = orgName
		}

		// Contract and subscription information
		if subscription, ok := queryResult["subscription"].(string); ok {
			metrics.Subscription = subscription
		}
		if contractedBase10, ok := queryResult["contractedDailyIngestBase10"].(float64); ok {
			metrics.ContractedDailyIngestBase10 = contractedBase10 / (1024 * 1024 * 1024) // Convert to GB
		}
		if contracted, ok := queryResult["contractedDailyIngest"].(float64); ok {
			metrics.ContractedDailyIngest = contracted / (1024 * 1024 * 1024) // Convert to GB
		}
		if retention, ok := queryResult["contractedRetention"].(float64); ok {
			metrics.ContractedRetention = retention
		}
		if cloud, ok := queryResult["cloud"].(string); ok {
			metrics.Cloud = cloud
		}

		// Storage metrics
		if storageSize, ok := queryResult["storageSize"].(float64); ok {
			metrics.StorageSize = storageSize / (1024 * 1024 * 1024) // Convert to GB
		}
		if falconStorage, ok := queryResult["falconStorageSize"].(float64); ok {
			metrics.FalconStorageSize = falconStorage / (1024 * 1024 * 1024) // Convert to GB
		}

		// Ingestion metrics (already in daily averages from query)
		if avgDaily, ok := queryResult["averageDailyIngestLast30Days"].(float64); ok {
			dailyIngestGB := avgDaily / (1024 * 1024 * 1024) // Convert to GB
			metrics.AverageDailyIngestLast30Days = dailyIngestGB

			// Update legacy daily/weekly/monthly fields for backward compatibility
			metrics.Daily.IngestVolumeGB = dailyIngestGB
			metrics.Weekly.IngestVolumeGB = dailyIngestGB * 7
			metrics.Monthly.IngestVolumeGB = dailyIngestGB * 30
		}

		if processedEvents, ok := queryResult["processedEventsSize"].(float64); ok {
			metrics.ProcessedEventsSize = processedEvents / (1024 * 1024 * 1024) // Convert to GB
			// Update legacy fields
			metrics.Daily.EventCount = int64(processedEvents)
			metrics.Weekly.EventCount = int64(processedEvents * 7)
			metrics.Monthly.EventCount = int64(processedEvents * 30)
		}

		if removedFields, ok := queryResult["removedFieldsSize"].(float64); ok {
			metrics.RemovedFieldsSize = removedFields / (1024 * 1024 * 1024) // Convert to GB
		}

		if ingestAfterRemoval, ok := queryResult["ingestAfterFieldRemovalSize"].(float64); ok {
			metrics.IngestAfterFieldRemovalSize = ingestAfterRemoval / (1024 * 1024 * 1024) // Convert to GB
		}

		if falconSegment, ok := queryResult["falconSegmentWriteBytes"].(float64); ok {
			metrics.FalconSegmentWriteBytes = falconSegment / (1024 * 1024 * 1024) // Convert to GB
		}

		if falconIngest, ok := queryResult["falconIngestAfterFieldRemovalSize"].(float64); ok {
			metrics.FalconIngestAfterFieldRemovalSize = falconIngest / (1024 * 1024 * 1024) // Convert to GB
		}

		// Measurement metadata
		if measurementPoint, ok := queryResult["measurementPoint"].(string); ok {
			metrics.MeasurementPoint = measurementPoint
		}
		if cid, ok := queryResult["cid"].(string); ok {
			metrics.CID = cid
		}

		// Calculate average event size if we have both metrics (legacy compatibility)
		if metrics.Daily.IngestVolumeGB > 0 && metrics.Daily.EventCount > 0 {
			totalBytes := metrics.Daily.IngestVolumeGB * 1024 * 1024 * 1024
			metrics.Daily.AverageEventSizeB = int64(totalBytes / float64(metrics.Daily.EventCount))
		}

		// Leave other trend/growth fields as zero values when unknown
	}
	// If failed to parse JSON, all metrics are already zero values by default
}

// CollectRepositoryUsage collects repository-specific usage metrics
func (h *ClientConfig) CollectRepositoryUsage(ctx context.Context, client *api.Client, settings QuerySettings) (*TelemetryRepositoryUsageMetrics, error) {
	// First, get list of repositories via the enhanced telemetry GraphQL query
	repoResp, err := humiographql.ListSearchDomainsForTelemetry(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get search domains for telemetry: %w", err)
	}

	repos := repoResp.GetSearchDomains()
	metrics := &TelemetryRepositoryUsageMetrics{
		TotalRepositories: len(repos),
		Repositories:      make([]RepositoryUsage, 0, len(repos)),
		TopRepositories:   make([]RepositoryUsage, 0),
	}

	// For each repository, collect usage metrics
	for _, repo := range repos {
		if repo.GetName() == "" {
			continue
		}

		repoUsage := h.collectSingleRepositoryUsageWithGraphQLData(ctx, client, repo, settings)

		metrics.Repositories = append(metrics.Repositories, *repoUsage)
	}

	// Sort and get top repositories by storage usage
	if len(metrics.Repositories) > 0 {
		// Sort repositories by storage size (descending)
		sort.Slice(metrics.Repositories, func(i, j int) bool {
			return metrics.Repositories[i].StorageUsageGB > metrics.Repositories[j].StorageUsageGB
		})

		// Take up to 10 repositories as "top" repositories
		topCount := 10
		if len(metrics.Repositories) < topCount {
			topCount = len(metrics.Repositories)
		}
		metrics.TopRepositories = metrics.Repositories[:topCount]
	}

	return metrics, nil
}

// collectSingleRepositoryUsageWithGraphQLData collects usage metrics for a single repository using GraphQL data
func (h *ClientConfig) collectSingleRepositoryUsageWithGraphQLData(ctx context.Context, client *api.Client, searchDomain humiographql.ListSearchDomainsForTelemetrySearchDomainsSearchDomain, settings QuerySettings) *RepositoryUsage {
	repoName := searchDomain.GetName()

	// Create usage metrics with GraphQL data
	usage := &RepositoryUsage{
		Name:             repoName,
		LastActivityTime: time.Now(),
	}

	// Extract repository-specific data if this is a Repository type
	if repo, ok := searchDomain.(*humiographql.ListSearchDomainsForTelemetrySearchDomainsRepository); ok {
		// Get actual storage size from GraphQL
		compressedSize := repo.GetCompressedByteSize()
		if compressedSize > 0 {
			// Convert bytes to GB
			usage.StorageUsageGB = float64(compressedSize) / (1024 * 1024 * 1024)
		}

		// Get retention settings
		retention := repo.GetTimeBasedRetention()
		if retention != nil && *retention > 0 {
			usage.RetentionDays = int(*retention)
		}
		// No else clause - leave RetentionDays as zero if not available

		// For now, still execute a simple query to get recent activity metrics
		query := api.Query{
			QueryString: RepositoryUsageQuery,
			Start:       "1d", // Last 24 hours
			End:         "",   // Empty means "now"
			Live:        false,
		}

		result, err := client.ExecuteLogScaleSearchWithTimeout(ctx, repoName, query, settings.MaxExecutionTime)
		if err == nil && result != nil && len(result.Events) > 0 {
			// Use query metadata to derive activity metrics
			// Safely convert with overflow protection
			eventCount := result.Metadata.EventCount
			if eventCount > math.MaxInt64 {
				usage.EventCount24h = math.MaxInt64 // max int64 if overflow would occur
			} else {
				usage.EventCount24h = int64(eventCount) // #nosec G115 - overflow check performed above
			}

			// Estimate daily ingest volume based on processed bytes (convert to GB)
			if result.Metadata.ProcessedBytes > 0 {
				usage.IngestVolumeGB24h = float64(result.Metadata.ProcessedBytes) / (1024 * 1024 * 1024)
			}

			// Set repository name as data source since we're collecting per-repository metrics
			usage.Dataspace = repoName
		} else {
			// No recent activity - use minimal defaults
			usage.EventCount24h = 0
			usage.IngestVolumeGB24h = 0
			usage.Dataspace = repoName
		}
	} else if view, ok := searchDomain.(*humiographql.ListSearchDomainsForTelemetrySearchDomainsView); ok {
		// For views, aggregate data from connected repositories
		connections := view.GetConnections()
		totalStorageGB := 0.0
		totalRetentionDays := 0
		repoCount := 0

		// For views, aggregate data from connected repositories
		for _, conn := range connections {
			repo := conn.GetRepository()
			// Repository data from connections
			compressedSize := repo.GetCompressedByteSize()
			if compressedSize > 0 {
				totalStorageGB += float64(compressedSize) / (1024 * 1024 * 1024)
			}
			retention := repo.GetTimeBasedRetention()
			if retention != nil && *retention > 0 {
				totalRetentionDays += int(*retention)
				repoCount++
			}
		}

		// For views, use the view name as the data source
		usage.Dataspace = repoName

		usage.StorageUsageGB = totalStorageGB
		if repoCount > 0 {
			usage.RetentionDays = totalRetentionDays / repoCount // Average retention
		}
		// Leave RetentionDays as zero if no repositories with retention data

		// For views, we don't have direct activity metrics, so set defaults
		usage.EventCount24h = 0
		usage.IngestVolumeGB24h = 0
	} else {
		// Unknown type - leave all metrics as zero values
		usage.Dataspace = repoName
	}

	return usage
}

// CollectUserActivity collects user activity and query pattern metrics
func (h *ClientConfig) CollectUserActivity(ctx context.Context, client *api.Client, settings QuerySettings) (*TelemetryUserActivityMetrics, error) {
	endTime := time.Now()
	startTime := endTime.Add(-30 * 24 * time.Hour) // Last 30 days

	query := api.Query{
		QueryString: UserActivityQuery,
		Start:       "30d", // Last 30 days
		End:         "",    // Empty means "now"
		Live:        false,
	}

	result, err := client.ExecuteLogScaleSearchWithTimeout(ctx, "humio", query, settings.MaxExecutionTime)
	if err != nil {
		return nil, fmt.Errorf("failed to execute user activity query: %w", err)
	}

	metrics := &TelemetryUserActivityMetrics{
		TimeRange: struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		}{Start: startTime, End: endTime},
	}

	// Extract metrics from query results - only report actual measured data
	if result != nil && len(result.Events) > 0 {
		// Track actual users and login events from audit logs
		uniqueUsers := make(map[string]bool)
		uniqueUsers24h := make(map[string]bool)
		uniqueUsers7d := make(map[string]bool)
		queryCount := int64(0)
		loginCount := int64(0)
		failedLoginCount := int64(0)

		// Calculate time boundaries for different windows
		now := endTime
		day24hAgo := now.Add(-24 * time.Hour)
		days7Ago := now.Add(-7 * 24 * time.Hour)

		for _, event := range result.Events {
			// Extract timestamp for time-based filtering
			var eventTime time.Time
			if eventTimeStr, ok := event["@timestamp"].(string); ok {
				if parsedTime, err := time.Parse(time.RFC3339, eventTimeStr); err == nil {
					eventTime = parsedTime
				}
			}

			// Look for user identifiers
			userID := ""
			if uid, ok := event["@userid"].(string); ok && uid != "" {
				userID = uid
			} else if userName, ok := event["@username"].(string); ok && userName != "" {
				userID = userName
			}

			if userID != "" {
				// Track users in different time windows
				uniqueUsers[userID] = true
				if eventTime.After(day24hAgo) {
					uniqueUsers24h[userID] = true
				}
				if eventTime.After(days7Ago) {
					uniqueUsers7d[userID] = true
				}
			}

			// Count actual login events
			if eventType, ok := event["@type"].(string); ok {
				switch eventType {
				case "login", "authentication", "session_start":
					loginCount++
				case "login_failed", "authentication_failed":
					failedLoginCount++
				case "query", "search":
					queryCount++
				}
			}

			// Also check for action field which might contain login information
			if action, ok := event["@action"].(string); ok {
				switch action {
				case "login", "authenticate":
					loginCount++
				case "login_failed", "authentication_failed":
					failedLoginCount++
				}
			}
		}

		// Set metrics based on actual measured data only
		metrics.ActiveUsers.Last30d = len(uniqueUsers)
		metrics.ActiveUsers.Last7d = len(uniqueUsers7d)
		metrics.ActiveUsers.Last24h = len(uniqueUsers24h)

		metrics.QueryActivity.TotalQueries = queryCount
		if queryCount > 0 {
			// Calculate average query time based on query processing
			metrics.QueryActivity.AvgQueryTime = float64(result.Metadata.TimeMillis) / float64(queryCount) / 1000.0
			metrics.QueryActivity.TopQueryTypes = []QueryTypeUsage{
				{Type: "search", Count: queryCount, AvgDuration: metrics.QueryActivity.AvgQueryTime},
			}
		}

		// Report actual login metrics only
		metrics.LoginActivity.TotalLogins = loginCount
		metrics.LoginActivity.UniqueUsers = len(uniqueUsers)
		metrics.LoginActivity.FailedAttempts = failedLoginCount
	}
	// No events found - all metrics already zero-initialized

	return metrics, nil
}

// CollectDetailedAnalytics collects comprehensive analytics data
func (h *ClientConfig) CollectDetailedAnalytics(ctx context.Context, client *api.Client, settings QuerySettings) (*TelemetryDetailedAnalytics, error) {
	endTime := time.Now()
	startTime := endTime.Add(-4 * time.Hour) // Last 4 hours for detailed analytics

	query := api.Query{
		QueryString: DetailedAnalyticsQuery,
		Start:       "4h", // Last 4 hours
		End:         "",   // Empty means "now"
		Live:        false,
	}

	result, err := client.ExecuteLogScaleSearchWithTimeout(ctx, "humio", query, settings.MaxExecutionTime)
	if err != nil {
		return nil, fmt.Errorf("failed to execute detailed analytics query: %w", err)
	}

	metrics := &TelemetryDetailedAnalytics{
		TimeRange: struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		}{Start: startTime, End: endTime},
		PerformanceMetrics: make(map[string]interface{}),
		UsagePatterns:      make(map[string]interface{}),
	}

	// Extract performance metrics from query results or use defaults
	if result != nil && len(result.Events) > 0 {
		// Calculate performance metrics from query metadata
		if result.Metadata.TimeMillis > 0 {
			// Average response time in seconds
			avgResponseTime := float64(result.Metadata.TimeMillis) / 1000.0
			metrics.PerformanceMetrics["avg_query_response_time"] = avgResponseTime
		}

		// Estimate concurrent queries based on events processed
		if result.Metadata.ProcessedEvents > 0 {
			concurrentQueries := result.Metadata.ProcessedEvents / result.Metadata.TimeMillis * 1000
			if concurrentQueries > 100 {
				concurrentQueries = 100 // Cap at reasonable maximum
			}
			metrics.PerformanceMetrics["peak_concurrent_queries"] = concurrentQueries
		}

		// Analyze usage patterns from timestamp distribution
		if len(result.Events) > 0 {
			// Find most common hour from timestamps
			hourCounts := make(map[int]int)
			for _, event := range result.Events {
				if timestamp, ok := event["@timestamp"].(float64); ok {
					timestampInt := int64(timestamp)

					// Use our standard timestamp conversion function
					converted := ConvertUnixTimestampToISO(timestampInt)

					// If it was converted to ISO string, parse it back to time.Time
					// If it wasn't converted (returned as int64), treat as seconds
					var t time.Time
					if isoStr, ok := converted.(string); ok {
						// Was converted to ISO - parse it back
						var err error
						t, err = time.Parse(time.RFC3339Nano, isoStr)
						if err != nil {
							// If parsing fails, fall back to treating as seconds
							t = time.Unix(timestampInt, 0)
						}
					} else {
						// Wasn't converted, treat as seconds
						t = time.Unix(timestampInt, 0)
					}

					hour := t.Hour()
					hourCounts[hour]++
				}
			}

			// Find peak hour only if we have data
			maxCount := 0
			peakHour := -1 // No default hour
			for hour, count := range hourCounts {
				if count > maxCount {
					maxCount = count
					peakHour = hour
				}
			}

			// Only set most_active_hour if we found actual data
			if peakHour >= 0 {
				metrics.UsagePatterns["most_active_hour"] = fmt.Sprintf("%02d:00", peakHour)
			}
		}

		// Determine query complexity trend based on processing metrics
		if result.Metadata.TotalWork > 0 && result.Metadata.WorkDone > 0 {
			workRatio := float64(result.Metadata.WorkDone) / float64(result.Metadata.TotalWork)
			if workRatio > 0.9 {
				metrics.UsagePatterns["query_complexity_trend"] = "stable"
			} else if workRatio > 0.7 {
				metrics.UsagePatterns["query_complexity_trend"] = "moderate"
			} else {
				metrics.UsagePatterns["query_complexity_trend"] = "complex"
			}
		}
		// Don't set any complexity trend if no work metadata available
	}
	// No query results - all metrics already zero-initialized

	return metrics, nil
}

// getLicenseJWTFromClusterSecret retrieves the license JWT from the HumioCluster's license secret
// Returns empty string and logs warning if secret is not accessible - allows graceful fallback
func (h *ClientConfig) getLicenseJWTFromClusterSecret(ctx context.Context, k8sClient client.Client, hc *humiov1alpha1.HumioCluster) (string, error) {
	// Check if the cluster has a license secret reference
	if hc.Spec.License.SecretKeyRef == nil {
		return "", fmt.Errorf("no license secret reference found in HumioCluster %s/%s", hc.Namespace, hc.Name)
	}

	secretRef := hc.Spec.License.SecretKeyRef
	ctrl.Log.V(1).Info("Attempting to retrieve license JWT from cluster secret",
		"cluster", hc.Name,
		"namespace", hc.Namespace,
		"secret", secretRef.Name,
		"key", secretRef.Key)

	// Get the secret using the existing kubernetes helper
	secret, err := kubernetes.GetSecret(ctx, k8sClient, secretRef.Name, hc.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get license secret %s/%s: %w", hc.Namespace, secretRef.Name, err)
	}

	// Get the license data from the secret
	licenseData, exists := secret.Data[secretRef.Key]
	if !exists {
		return "", fmt.Errorf("key %s not found in license secret %s/%s", secretRef.Key, hc.Namespace, secretRef.Name)
	}

	// The license might be base64 encoded in the secret - try to decode
	licenseString := string(licenseData)

	// Check if it's base64 encoded (license secrets often are)
	if decoded, err := base64.StdEncoding.DecodeString(licenseString); err == nil {
		// If decode succeeded and result looks like JWT, use decoded version
		decodedStr := string(decoded)
		if len(decodedStr) > 10 && (decodedStr[:3] == "eyJ" || decodedStr[0] == 'e') { // JWT typically starts with eyJ when base64 encoded
			licenseString = decodedStr
			ctrl.Log.V(1).Info("Successfully decoded base64-encoded license JWT from secret",
				"cluster", hc.Name,
				"secret", secretRef.Name)
		}
	}

	// Basic validation - check minimum length and JWT structure
	if len(licenseString) < 10 {
		return "", fmt.Errorf("license data from secret %s/%s appears to be too short to be a valid JWT", hc.Namespace, secretRef.Name)
	}

	// Validate JWT structure (should have exactly 3 parts: header.payload.signature)
	if len(strings.Split(licenseString, ".")) != 3 {
		parts := len(strings.Split(licenseString, "."))
		return "", fmt.Errorf("license data from secret %s/%s does not appear to be a valid JWT (expected 3 parts, found %d)", hc.Namespace, secretRef.Name, parts)
	}

	ctrl.Log.V(1).Info("Successfully retrieved license JWT from cluster secret",
		"cluster", hc.Name,
		"secret", secretRef.Name,
		"jwt_length", len(licenseString))

	return licenseString, nil
}
