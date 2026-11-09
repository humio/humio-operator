package controller

import (
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildErrorSummaryForEvent(t *testing.T) {
	r := &HumioTelemetryCollectionReconciler{}

	t.Run("empty errors", func(t *testing.T) {
		summary := r.buildErrorSummaryForEvent([]humiov1alpha1.HumioTelemetryCollectionError{}, 100)
		require.Equal(t, "No specific errors available", summary)
	})

	t.Run("single error with DataType", func(t *testing.T) {
		errors := []humiov1alpha1.HumioTelemetryCollectionError{
			{
				Type:      "collection",
				Message:   "Failed to collect license data",
				DataType:  "license",
				Timestamp: metav1.Now(),
			},
		}
		summary := r.buildErrorSummaryForEvent(errors, 100)
		require.Equal(t, "(collection) license: Failed to collect license data", summary)
	})

	t.Run("single error without DataType", func(t *testing.T) {
		errors := []humiov1alpha1.HumioTelemetryCollectionError{
			{
				Type:      "collection",
				Message:   "Failed to get client config",
				Timestamp: metav1.Now(),
			},
		}
		summary := r.buildErrorSummaryForEvent(errors, 100)
		require.Equal(t, "(collection) Failed to get client config", summary)
	})

	t.Run("multiple errors", func(t *testing.T) {
		errors := []humiov1alpha1.HumioTelemetryCollectionError{
			{
				Type:      "collection",
				Message:   "Failed to collect license data",
				DataType:  "license",
				Timestamp: metav1.Now(),
			},
			{
				Type:      "collection",
				Message:   "Failed to collect user activity",
				DataType:  "user_activity",
				Timestamp: metav1.Now(),
			},
		}
		summary := r.buildErrorSummaryForEvent(errors, 200)
		require.Equal(t, "(collection) license: Failed to collect license data; (collection) user_activity: Failed to collect user activity", summary)
	})

	t.Run("truncation with length limit", func(t *testing.T) {
		errors := []humiov1alpha1.HumioTelemetryCollectionError{
			{
				Type:      "collection",
				Message:   "This is a very long error message that should be truncated",
				DataType:  "license",
				Timestamp: metav1.Now(),
			},
		}
		summary := r.buildErrorSummaryForEvent(errors, 50)
		require.Contains(t, summary, "...")
		require.True(t, len(summary) <= 53) // 50 + "..."
	})

	t.Run("remaining count when too many errors", func(t *testing.T) {
		errors := []humiov1alpha1.HumioTelemetryCollectionError{
			{Type: "collection", Message: "Error 1", DataType: "type1", Timestamp: metav1.Now()},
			{Type: "collection", Message: "Error 2", DataType: "type2", Timestamp: metav1.Now()},
			{Type: "collection", Message: "Error 3", DataType: "type3", Timestamp: metav1.Now()},
		}
		summary := r.buildErrorSummaryForEvent(errors, 60)
		require.Contains(t, summary, "... and")
		require.Contains(t, summary, "more")
	})
}

func TestErrorBelongsToDataType(t *testing.T) {
	r := &HumioTelemetryCollectionReconciler{}

	t.Run("exact DataType field match", func(t *testing.T) {
		err := humiov1alpha1.HumioTelemetryCollectionError{
			Type:     "collection",
			Message:  "Some error occurred",
			DataType: "license",
		}
		require.True(t, r.errorBelongsToDataType(err, "license"))
		require.False(t, r.errorBelongsToDataType(err, "user_activity"))
	})

	t.Run("pattern matching for license", func(t *testing.T) {
		err := humiov1alpha1.HumioTelemetryCollectionError{
			Type:    "collection",
			Message: "Failed to collect license data: connection timeout",
		}
		require.True(t, r.errorBelongsToDataType(err, "license"))
	})

	t.Run("pattern matching for ingestion_metrics", func(t *testing.T) {
		err := humiov1alpha1.HumioTelemetryCollectionError{
			Type:    "collection",
			Message: "Failed to collect ingestion metrics: query failed",
		}
		require.True(t, r.errorBelongsToDataType(err, "ingestion_metrics"))
	})

	t.Run("pattern matching for organizational usage (ingestion_metrics)", func(t *testing.T) {
		err := humiov1alpha1.HumioTelemetryCollectionError{
			Type:    "collection",
			Message: "Cannot check organizational usage data availability for ingestion metrics",
		}
		require.True(t, r.errorBelongsToDataType(err, "ingestion_metrics"))
	})

	t.Run("pattern matching for user_activity", func(t *testing.T) {
		err := humiov1alpha1.HumioTelemetryCollectionError{
			Type:    "collection",
			Message: "Failed to collect user activity: timeout occurred",
		}
		require.True(t, r.errorBelongsToDataType(err, "user_activity"))
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		err := humiov1alpha1.HumioTelemetryCollectionError{
			Type:    "collection",
			Message: "Failed to collect LICENSE data: error",
		}
		require.True(t, r.errorBelongsToDataType(err, "license"))
	})

	t.Run("no match", func(t *testing.T) {
		err := humiov1alpha1.HumioTelemetryCollectionError{
			Type:    "collection",
			Message: "Failed to connect to server: network timeout",
		}
		require.False(t, r.errorBelongsToDataType(err, "license"))
		require.False(t, r.errorBelongsToDataType(err, "user_activity"))
		require.False(t, r.errorBelongsToDataType(err, "ingestion_metrics"))
	})

	t.Run("fallback string matching", func(t *testing.T) {
		err := humiov1alpha1.HumioTelemetryCollectionError{
			Type:    "collection",
			Message: "Some error with cluster_info in the message",
		}
		require.True(t, r.errorBelongsToDataType(err, "cluster_info"))
	})
}

func TestDetermineSuccessfulCollections(t *testing.T) {
	r := &HumioTelemetryCollectionReconciler{}

	t.Run("all successful with payloads", func(t *testing.T) {
		requestedTypes := []string{"license", "cluster_info"}
		payloads := []humio.TelemetryPayload{
			{CollectionType: "license"},
			{CollectionType: "cluster_info"},
		}
		collectionErrors := []humiov1alpha1.HumioTelemetryCollectionError{}

		successful := r.determineSuccessfulCollections(requestedTypes, collectionErrors, payloads)
		require.ElementsMatch(t, []string{"license", "cluster_info"}, successful)
	})

	t.Run("ingestion_metrics fails with organizational usage error", func(t *testing.T) {
		requestedTypes := []string{"license", "ingestion_metrics", "cluster_info"}
		payloads := []humio.TelemetryPayload{
			{CollectionType: "license"},
			{CollectionType: "cluster_info"},
			// No payload for ingestion_metrics due to organizational usage error
		}
		collectionErrors := []humiov1alpha1.HumioTelemetryCollectionError{
			{
				Type:    "collection",
				Message: "Cannot check organizational usage data availability for ingestion metrics: 403 Forbidden",
			},
		}

		successful := r.determineSuccessfulCollections(requestedTypes, collectionErrors, payloads)
		require.ElementsMatch(t, []string{"license", "cluster_info"}, successful)
		require.NotContains(t, successful, "ingestion_metrics")
	})

	t.Run("user_activity fails with timeout", func(t *testing.T) {
		requestedTypes := []string{"license", "user_activity"}
		payloads := []humio.TelemetryPayload{
			{CollectionType: "license"},
			// No payload for user_activity due to timeout
		}
		collectionErrors := []humiov1alpha1.HumioTelemetryCollectionError{
			{
				Type:    "collection",
				Message: "Failed to collect user activity: context deadline exceeded",
			},
		}

		successful := r.determineSuccessfulCollections(requestedTypes, collectionErrors, payloads)
		require.ElementsMatch(t, []string{"license"}, successful)
		require.NotContains(t, successful, "user_activity")
	})

	t.Run("no payloads and no specific errors", func(t *testing.T) {
		requestedTypes := []string{"license", "cluster_info"}
		payloads := []humio.TelemetryPayload{}
		collectionErrors := []humiov1alpha1.HumioTelemetryCollectionError{
			{
				Type:    "client",
				Message: "Failed to connect to server: network error",
			},
		}

		successful := r.determineSuccessfulCollections(requestedTypes, collectionErrors, payloads)
		require.Empty(t, successful) // No payloads means no success, even without specific errors
	})

	t.Run("mixed success and failure", func(t *testing.T) {
		requestedTypes := []string{"license", "ingestion_metrics", "user_activity", "cluster_info"}
		payloads := []humio.TelemetryPayload{
			{CollectionType: "license"},
			{CollectionType: "cluster_info"},
			// No payloads for ingestion_metrics and user_activity
		}
		collectionErrors := []humiov1alpha1.HumioTelemetryCollectionError{
			{
				Type:    "collection",
				Message: "Ingestion metrics require LogScale organizational usage data but none found in last 24 hours",
			},
			{
				Type:    "collection",
				Message: "Failed to collect user activity: query timeout",
			},
		}

		successful := r.determineSuccessfulCollections(requestedTypes, collectionErrors, payloads)
		require.ElementsMatch(t, []string{"license", "cluster_info"}, successful)
		require.NotContains(t, successful, "ingestion_metrics")
		require.NotContains(t, successful, "user_activity")
	})
}
