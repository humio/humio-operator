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
	"time"
)

// Telemetry constants to eliminate string repetition and improve maintainability
const (
	// Error Types for TelemetryError.Type
	TelemetryErrorTypeCollection       = "collection"
	TelemetryErrorTypeExport           = "export"
	TelemetryErrorTypeConfiguration    = "configuration"
	TelemetryErrorTypeClient           = "client"
	TelemetryErrorTypeCollectionAccess = "collection_access"
	TelemetryErrorTypeValidation       = "validation"

	// Collection Types for TelemetryPayload.CollectionType
	TelemetryCollectionTypeLicense           = "license"
	TelemetryCollectionTypeClusterInfo       = "cluster_info"
	TelemetryCollectionTypeIngestionMetrics  = "ingestion_metrics"
	TelemetryCollectionTypeRepositoryUsage   = "repository_usage"
	TelemetryCollectionTypeUserActivity      = "user_activity"
	TelemetryCollectionTypeDetailedAnalytics = "detailed_analytics"
	TelemetryCollectionTypeCollectionErrors  = "collection_errors"

	// Source Types for TelemetryPayload.SourceType
	TelemetrySourceTypeJSON = "json"
)

// TelemetryLicenseData represents license information for telemetry
type TelemetryLicenseData struct {
	// GraphQL-sourced fields
	LicenseUID     string    `json:"license_uid,omitempty"`
	LicenseType    string    `json:"license_type"` // "onprem", "trial"
	ExpirationDate time.Time `json:"expiration_date"`
	IssuedDate     time.Time `json:"issued_date"`
	Owner          string    `json:"owner,omitempty"`
	MaxUsers       *int      `json:"max_users,omitempty"`
	IsSaaS         *bool     `json:"is_saas,omitempty"`
	IsOem          *bool     `json:"is_oem,omitempty"`

	// JWT-exclusive fields (not available via GraphQL)
	MaxIngestGbPerDay    *float64   `json:"max_ingest_gb_per_day,omitempty"` // Daily ingestion limit from JWT
	MaxCores             *int       `json:"max_cores,omitempty"`             // CPU core limit from JWT
	LicenseValidUntil    *time.Time `json:"license_valid_until,omitempty"`   // License validity from JWT
	LicenseSubject       string     `json:"license_subject,omitempty"`       // License holder from JWT
	JWTExtractionSuccess bool       `json:"jwt_extraction_success"`          // Whether JWT parsing succeeded

	// Raw license data for future implementation
	RawLicenseData map[string]any `json:"raw_license_data,omitempty"`
}

// TelemetryClusterInfo represents cluster information for telemetry
type TelemetryClusterInfo struct {
	Version   string `json:"version"`
	NodeCount int    `json:"node_count"`

	// TODO: Implement user/repository collection via GraphQL
	UserCount       int `json:"user_count,omitempty"`
	RepositoryCount int `json:"repository_count,omitempty"`
}

// TelemetryPayload is the complete payload sent to the telemetry cluster
type TelemetryPayload struct {
	Timestamp        time.Time        `json:"@timestamp"`
	ClusterID        string           `json:"cluster_id"`      // User-specified cluster identifier
	ClusterGUID      string           `json:"cluster_guid"`    // LogScale repository ID
	CollectionType   string           `json:"collection_type"` // See TelemetryCollectionType* constants
	SourceType       string           `json:"source_type"`     // Parser name for LogScale - typically "json" for structured data
	Data             any              `json:"data"`
	CollectionErrors []TelemetryError `json:"collection_errors,omitempty"`
}

// TelemetryEvent represents a single telemetry event with flattened data
// Used for collections that need to be split into multiple events (like repository_usage)
type TelemetryEvent struct {
	Timestamp      time.Time        `json:"@timestamp"`
	ClusterID      string           `json:"cluster_id"`   // User-specified cluster identifier
	ClusterGUID    string           `json:"cluster_guid"` // LogScale repository ID
	CollectionType string           `json:"collection_type"`
	SourceType     string           `json:"source_type"`
	Data           map[string]any   `json:"data"` // Flattened key-value pairs
	Errors         []TelemetryError `json:"collection_errors,omitempty"`
}

// TelemetryError represents an error that occurred during telemetry collection
type TelemetryError struct {
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"@timestamp"`
}

// TelemetryCollectionResult contains the result of a telemetry collection operation
type TelemetryCollectionResult struct {
	Data   any              `json:"data,omitempty"`
	Errors []TelemetryError `json:"errors,omitempty"`
}

// Search-based telemetry data types

// TelemetryIngestionMetrics represents ingestion volume and event count metrics
type TelemetryIngestionMetrics struct {
	TimeRange struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"time_range"`

	// Organization identification fields
	OrgID   string `json:"org_id,omitempty"`
	OrgName string `json:"org_name,omitempty"`

	// Contract and subscription information (bytes as strings for exact precision)
	Subscription                string  `json:"subscription,omitempty"`
	ContractedDailyIngestBase10 string  `json:"contracted_daily_ingest_base10,omitempty"`
	ContractedDailyIngest       string  `json:"contracted_daily_ingest,omitempty"`
	ContractedRetention         float64 `json:"contracted_retention,omitempty"`
	Cloud                       string  `json:"cloud,omitempty"`

	// Storage metrics (in bytes as strings for exact precision)
	StorageSize       string `json:"storage_size,omitempty"`
	FalconStorageSize string `json:"falcon_storage_size,omitempty"`

	// Ingestion metrics (in bytes as strings for exact precision)
	AverageDailyIngestLast30Days      string `json:"average_daily_ingest_last_30_days,omitempty"`
	ProcessedEventsSize               string `json:"processed_events_size,omitempty"`
	RemovedFieldsSize                 string `json:"removed_fields_size,omitempty"`
	IngestAfterFieldRemovalSize       string `json:"ingest_after_field_removal_size,omitempty"`
	FalconSegmentWriteBytes           string `json:"falcon_segment_write_bytes,omitempty"`
	FalconIngestAfterFieldRemovalSize string `json:"falcon_ingest_after_field_removal_size,omitempty"`

	// Measurement metadata
	MeasurementPoint string `json:"measurement_point,omitempty"`
	CID              string `json:"cid,omitempty"`

	Daily struct {
		IngestVolumeGB    float64 `json:"ingest_volume_gb"`
		EventCount        int64   `json:"event_count"`
		AverageEventSizeB int64   `json:"average_event_size_bytes"`
	} `json:"daily"`

	Weekly struct {
		IngestVolumeGB    float64 `json:"ingest_volume_gb"`
		EventCount        int64   `json:"event_count"`
		GrowthRatePercent float64 `json:"growth_rate_percent"`
	} `json:"weekly"`

	Monthly struct {
		IngestVolumeGB float64 `json:"ingest_volume_gb"`
		EventCount     int64   `json:"event_count"`
		TrendDirection string  `json:"trend_direction"` // "increasing", "decreasing", "stable"
	} `json:"monthly"`

	// Raw query result for debugging/validation
	RawQueryResult any `json:"raw_query_result,omitempty"`
}

// TelemetryRepositoryUsageMetrics represents repository-specific usage metrics
type TelemetryRepositoryUsageMetrics struct {
	TotalRepositories int               `json:"total_repositories"`
	Repositories      []RepositoryUsage `json:"repositories"`
	TopRepositories   []RepositoryUsage `json:"top_repositories_by_volume"`
}

// RepositoryUsage represents usage metrics for a single repository
type RepositoryUsage struct {
	Name              string    `json:"name"`
	IngestVolumeGB24h float64   `json:"ingest_volume_gb_24h"`
	EventCount24h     int64     `json:"event_count_24h"`
	RetentionDays     int       `json:"retention_days"`
	StorageUsageGB    float64   `json:"storage_usage_gb"`
	LastActivityTime  time.Time `json:"last_activity_time"`
	Dataspace         string    `json:"dataspace,omitempty"`
}

// TelemetryUserActivityMetrics represents user activity and query patterns
type TelemetryUserActivityMetrics struct {
	TimeRange struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"time_range"`

	ActiveUsers struct {
		Last24h int `json:"last_24h"`
		Last7d  int `json:"last_7d"`
		Last30d int `json:"last_30d"`
	} `json:"active_users"`

	QueryActivity struct {
		TotalQueries  int64            `json:"total_queries"`
		AvgQueryTime  float64          `json:"avg_query_time_seconds"`
		TopQueryTypes []QueryTypeUsage `json:"top_query_types"`
	} `json:"query_activity"`

	LoginActivity struct {
		TotalLogins    int64 `json:"total_logins"`
		UniqueUsers    int   `json:"unique_users"`
		FailedAttempts int64 `json:"failed_attempts"`
	} `json:"login_activity"`
}

// QueryTypeUsage represents usage statistics for a specific query type
type QueryTypeUsage struct {
	Type        string  `json:"type"` // "search", "dashboard", "alert", etc.
	Count       int64   `json:"count"`
	AvgDuration float64 `json:"avg_duration_seconds"`
}

// TelemetryDetailedAnalytics represents comprehensive analytics data
type TelemetryDetailedAnalytics struct {
	TimeRange struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"time_range"`

	// Additional detailed metrics can be added here as needed
	PerformanceMetrics map[string]any `json:"performance_metrics,omitempty"`
	UsagePatterns      map[string]any `json:"usage_patterns,omitempty"`
}

// QuerySettings represents configuration for search query execution
type QuerySettings struct {
	MaxExecutionTime time.Duration `json:"max_execution_time"`
	MaxResultSize    int64         `json:"max_result_size"`
	TimeoutRetries   int           `json:"timeout_retries"`
	TimeRangeMode    string        `json:"time_range_mode"` // "relative", "fixed"
}

// WithTimeout creates a copy of QuerySettings with modified execution timeout
// This avoids redundant copying of individual fields
func (qs QuerySettings) WithTimeout(timeout time.Duration) QuerySettings {
	qs.MaxExecutionTime = timeout
	return qs
}

// WithMaxResultSize creates a copy of QuerySettings with modified max result size
func (qs QuerySettings) WithMaxResultSize(size int64) QuerySettings {
	qs.MaxResultSize = size
	return qs
}

// WithRetries creates a copy of QuerySettings with modified retry count
func (qs QuerySettings) WithRetries(retries int) QuerySettings {
	qs.TimeoutRetries = retries
	return qs
}

// WithTimeRangeMode creates a copy of QuerySettings with modified time range mode
func (qs QuerySettings) WithTimeRangeMode(mode string) QuerySettings {
	qs.TimeRangeMode = mode
	return qs
}

// DefaultQuerySettings provides sensible defaults for query execution
var DefaultQuerySettings = QuerySettings{
	MaxExecutionTime: 30 * time.Second,
	MaxResultSize:    1024 * 1024, // 1MB
	TimeoutRetries:   2,
	TimeRangeMode:    "relative",
}
