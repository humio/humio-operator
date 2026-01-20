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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioTelemetryCollectionReconciler reconciles a HumioTelemetryCollection object
type HumioTelemetryCollectionReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
	Recorder    record.EventRecorder
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetrycollections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetrycollections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetrycollections/finalizers,verbs=update
// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetryexports,verbs=get;list;watch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioclusters,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioTelemetryCollectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioTelemetryCollection")

	htc := &humiov1alpha1.HumioTelemetryCollection{}
	err := r.Get(ctx, req.NamespacedName, htc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", htc.UID)

	// Handle deletion
	if htc.DeletionTimestamp != nil {
		r.Log.Info("HumioTelemetryCollection marked for deletion")
		return r.cleanupTelemetryCollection(ctx, htc)
	}

	// Add finalizer if not present
	if !helpers.ContainsElement(htc.Finalizers, HumioFinalizer) {
		r.Log.Info("Adding finalizer to HumioTelemetryCollection")
		htc.Finalizers = append(htc.Finalizers, HumioFinalizer)

		if err := r.Update(ctx, htc); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// Validate configuration
	if err := r.validateConfiguration(htc); err != nil {
		r.Log.Error(err, "Invalid telemetry collection configuration")
		r.Recorder.Eventf(htc, corev1.EventTypeWarning, "HumioTelemetryCollectionConfigurationError",
			"Invalid telemetry collection configuration: %v", err)
		return r.updateStatusWithConfigError(ctx, htc, err)
	}

	// Get the associated HumioCluster
	hc, err := r.getHumioCluster(ctx, htc)
	if err != nil {
		r.Log.Error(err, "Failed to get associated HumioCluster")
		r.Recorder.Eventf(htc, corev1.EventTypeWarning, "HumioClusterNotFound",
			"Failed to get associated HumioCluster '%s': %v", htc.Spec.ManagedClusterName, err)
		return r.updateStatus(ctx, htc, humiov1alpha1.HumioTelemetryCollectionStateConfigError)
	}

	// Check if telemetry collection should run based on collection schedules
	shouldCollect, collectionTypes, nextCollection := r.ShouldRunTelemetryCollection(htc)
	if !shouldCollect {
		r.Log.Info("No telemetry collection needed at this time", "next_collection", nextCollection)
		return reconcile.Result{RequeueAfter: time.Until(nextCollection)}, nil
	}

	r.Log.Info("Running telemetry collection", "types", collectionTypes, "cluster", r.getClusterIdentifier(htc))

	r.Recorder.Eventf(htc, corev1.EventTypeNormal, "TelemetryCollectionStarted",
		"Starting telemetry collection for types: %v (cluster: %s)",
		collectionTypes, r.getClusterIdentifier(htc))

	// Update status to collecting
	if result, err := r.updateStatus(ctx, htc, humiov1alpha1.HumioTelemetryCollectionStateCollecting); err != nil {
		return result, err
	}

	// Perform telemetry collection
	r.Log.Info("Starting telemetry data collection", "types", collectionTypes)
	payloads, collectionErrors, sourceInfo := r.collectTelemetryData(ctx, htc, hc, collectionTypes)

	// Discover registered exporters
	exporters, err := r.discoverRegisteredExporters(ctx, htc)
	if err != nil {
		r.Log.Error(err, "Failed to discover registered exporters")
		// Don't fail the entire reconcile - continue with empty exporter list
		exporters = []ExporterInfo{}
	}

	// Push data to all registered exporters
	exportResults := r.pushToExporters(ctx, htc, payloads, exporters)

	// Update status based on results
	now := metav1.Now()

	// Determine which data types were successfully collected
	successfulCollections := r.determineSuccessfulCollections(collectionTypes, collectionErrors, payloads)

	// Update collection status
	collectionStatus := r.buildCollectionStatus(htc, successfulCollections, collectionErrors, collectionTypes, now)

	// Convert export results to push results
	pushResults := r.convertToPushResults(exportResults)

	if len(collectionErrors) == 0 {
		r.Log.Info("Telemetry collection completed successfully", "types", collectionTypes, "cluster", r.getClusterIdentifier(htc))

		// Count successful collections for the event message
		successfulCollections := r.determineSuccessfulCollections(collectionTypes, collectionErrors, payloads)

		r.Recorder.Eventf(htc, corev1.EventTypeNormal, "TelemetryCollectionSucceeded",
			"Successfully collected %d telemetry types: %v (cluster: %s) %s",
			len(successfulCollections), successfulCollections, r.getClusterIdentifier(htc), sourceInfo)
		return r.updateStatusWithDetails(ctx, htc, humiov1alpha1.HumioTelemetryCollectionStateEnabled, collectionStatus, pushResults)
	} else {
		r.Log.Error(fmt.Errorf("telemetry collection failed"), "Telemetry collection completed with errors", "types", collectionTypes, "error_count", len(collectionErrors))
		failedTypes := r.getFailedTypesFromErrors(collectionErrors, collectionTypes)

		// Create detailed error summary for the event
		errorSummary := r.buildErrorSummaryForEvent(collectionErrors, 200) // Limit message length for events

		r.Recorder.Eventf(htc, corev1.EventTypeWarning, "TelemetryCollectionFailed",
			"Telemetry collection failed with %d errors for types: %v. Errors: %s",
			len(collectionErrors), failedTypes, errorSummary)
		return r.updateStatusWithDetails(ctx, htc, humiov1alpha1.HumioTelemetryCollectionStateEnabled, collectionStatus, pushResults)
	}
}

// getClusterIdentifier returns the cluster identifier, defaulting to managedClusterName if not specified
func (r *HumioTelemetryCollectionReconciler) getClusterIdentifier(htc *humiov1alpha1.HumioTelemetryCollection) string {
	if htc.Spec.ClusterIdentifier != "" {
		return htc.Spec.ClusterIdentifier
	}
	return htc.Spec.ManagedClusterName
}

// validateConfiguration validates the telemetry collection configuration
func (r *HumioTelemetryCollectionReconciler) validateConfiguration(htc *humiov1alpha1.HumioTelemetryCollection) error {
	// Validate managed cluster exists
	if htc.Spec.ManagedClusterName == "" {
		return fmt.Errorf("managedClusterName is required")
	}

	// Validate collections
	if len(htc.Spec.Collections) == 0 {
		return fmt.Errorf("at least one collection must be specified")
	}

	for _, collection := range htc.Spec.Collections {
		if collection.Interval == "" {
			return fmt.Errorf("collection interval is required")
		}
		if len(collection.Include) == 0 {
			return fmt.Errorf("collection must include at least one data type")
		}
		// Validate data types
		for _, dataType := range collection.Include {
			if !isValidDataType(dataType) {
				return fmt.Errorf("invalid data type: %s", dataType)
			}
		}
	}

	return nil
}

// getHumioCluster retrieves the associated HumioCluster resource
func (r *HumioTelemetryCollectionReconciler) getHumioCluster(ctx context.Context, htc *humiov1alpha1.HumioTelemetryCollection) (*humiov1alpha1.HumioCluster, error) {
	hc := &humiov1alpha1.HumioCluster{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      htc.Spec.ManagedClusterName,
		Namespace: htc.Namespace,
	}, hc)
	return hc, err
}

// ShouldRunTelemetryCollection determines if telemetry collection should run based on schedules
func (r *HumioTelemetryCollectionReconciler) ShouldRunTelemetryCollection(htc *humiov1alpha1.HumioTelemetryCollection) (bool, []string, time.Time) {
	now := time.Now()
	var shouldCollect bool
	var collectTypes []string
	var nextCollection time.Time

	// Initialize next collection to far future
	nextCollection = now.Add(24 * time.Hour)

	for _, collection := range htc.Spec.Collections {
		interval, err := parseDuration(collection.Interval)
		if err != nil {
			r.Log.Error(err, "Failed to parse interval", "interval", collection.Interval)
			continue
		}

		// Determine last collection time for this collection type
		var lastCollection time.Time
		if htc.Status.CollectionStatus != nil {
			for _, dataType := range collection.Include {
				if status, exists := htc.Status.CollectionStatus[dataType]; exists && status.LastCollection != nil {
					if status.LastCollection.After(lastCollection) {
						lastCollection = status.LastCollection.Time
					}
				}
			}
		}

		// Check if it's time to collect
		nextTime := lastCollection.Add(interval)
		if now.After(nextTime) || now.Equal(nextTime) {
			shouldCollect = true
			collectTypes = append(collectTypes, collection.Include...)
		}

		// Track earliest next collection time
		if nextTime.Before(nextCollection) {
			nextCollection = nextTime
		}
	}

	return shouldCollect, collectTypes, nextCollection
}

// collectTelemetryData performs the actual telemetry collection
func (r *HumioTelemetryCollectionReconciler) collectTelemetryData(ctx context.Context, htc *humiov1alpha1.HumioTelemetryCollection, hc *humiov1alpha1.HumioCluster, dataTypes []string) ([]humio.TelemetryPayload, []humiov1alpha1.HumioTelemetryCollectionError, string) {
	// Get Humio HTTP client
	cluster, err := helpers.NewCluster(ctx, r.Client, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		return nil, []humiov1alpha1.HumioTelemetryCollectionError{{
			Type:      humio.TelemetryErrorTypeClient,
			Message:   fmt.Sprintf("Failed to get Humio client config for cluster '%s/%s': %v", hc.Namespace, hc.Name, err),
			Timestamp: metav1.Now(),
		}}, ""
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), reconcile.Request{NamespacedName: types.NamespacedName{Name: hc.Name, Namespace: hc.Namespace}})

	// Collect telemetry data with source tracking
	r.Log.Info("Collecting telemetry data from Humio cluster", "types", dataTypes, "cluster", r.getClusterIdentifier(htc))
	// Always collect collection errors during the collection phase - individual exporters
	// can choose whether to include them based on their SendCollectionErrors setting
	sendCollectionErrors := true
	payloads, sourceInfo, err := r.HumioClient.CollectTelemetryData(ctx, humioHttpClient, dataTypes, r.getClusterIdentifier(htc), sendCollectionErrors, r.Client, hc)
	if err != nil {
		r.Log.Error(err, "Failed to collect telemetry data", "types", dataTypes, "cluster", r.getClusterIdentifier(htc))
		return nil, []humiov1alpha1.HumioTelemetryCollectionError{{
			Type:      humio.TelemetryErrorTypeCollection,
			Message:   fmt.Sprintf("Failed to collect telemetry data for types %v from cluster '%s': %v", dataTypes, r.getClusterIdentifier(htc), err),
			Timestamp: metav1.Now(),
		}}, ""
	}

	r.Log.Info("Successfully collected telemetry data", "payload_count", len(payloads), "types", dataTypes, "source", sourceInfo)

	// Extract collection errors from payloads
	var collectionErrors []humiov1alpha1.HumioTelemetryCollectionError
	for _, payload := range payloads {
		for _, collErr := range payload.CollectionErrors {
			collectionErrors = append(collectionErrors, humiov1alpha1.HumioTelemetryCollectionError{
				Type:      collErr.Type,
				Message:   collErr.Message,
				Timestamp: metav1.Time{Time: collErr.Timestamp},
			})
		}
	}

	return payloads, collectionErrors, sourceInfo
}

// discoverRegisteredExporters finds all HumioTelemetryExport resources that reference this collection
func (r *HumioTelemetryCollectionReconciler) discoverRegisteredExporters(ctx context.Context, htc *humiov1alpha1.HumioTelemetryCollection) ([]ExporterInfo, error) {
	exportList := &humiov1alpha1.HumioTelemetryExportList{}
	if err := r.List(ctx, exportList); err != nil {
		return nil, fmt.Errorf("failed to list telemetry exports: %w", err)
	}

	var exporters []ExporterInfo
	for _, export := range exportList.Items {
		if r.exportRegistersCollection(&export, htc) {
			exporters = append(exporters, ExporterInfo{
				Name:      export.Name,
				Namespace: export.Namespace,
				Config:    &export,
			})
		}
	}

	r.Log.Info("Discovered registered exporters", "collection", htc.Name, "exporter_count", len(exporters))
	return exporters, nil
}

// exportRegistersCollection checks if an export resource registers a specific collection
func (r *HumioTelemetryCollectionReconciler) exportRegistersCollection(export *humiov1alpha1.HumioTelemetryExport, htc *humiov1alpha1.HumioTelemetryCollection) bool {
	for _, regCol := range export.Spec.RegisteredCollections {
		// Resolve namespace - defaults to export's namespace if not specified
		regNamespace := regCol.Namespace
		if regNamespace == "" {
			regNamespace = export.Namespace
		}

		if regCol.Name == htc.Name && regNamespace == htc.Namespace {
			return true
		}
	}
	return false
}

// pushToExporters immediately sends collected data to all registered exporters
func (r *HumioTelemetryCollectionReconciler) pushToExporters(ctx context.Context, htc *humiov1alpha1.HumioTelemetryCollection, payloads []humio.TelemetryPayload, exporters []ExporterInfo) []ExportResult {
	if len(exporters) == 0 {
		r.Log.Info("No exporters registered for collection", "collection", htc.Name)
		return nil
	}

	r.Log.Info("Pushing telemetry data to exporters", "collection", htc.Name, "exporter_count", len(exporters), "payload_count", len(payloads))

	results := make([]ExportResult, 0, len(exporters))

	// Push to all exporters in parallel
	resultChan := make(chan ExportResult, len(exporters))

	for _, exporter := range exporters {
		go r.pushToSingleExporter(ctx, htc, payloads, exporter, resultChan)
	}

	// Collect results
	for i := 0; i < len(exporters); i++ {
		result := <-resultChan
		results = append(results, result)

		// Find the corresponding exporter config to emit events on the correct resource
		var exporterConfig *humiov1alpha1.HumioTelemetryExport
		for _, exp := range exporters {
			if exp.Name == result.ExporterName && exp.Namespace == result.ExporterNamespace {
				exporterConfig = exp.Config
				break
			}
		}

		if result.Success {
			r.Log.Info("Successfully pushed to exporter",
				"collection", htc.Name,
				"exporter", fmt.Sprintf("%s/%s", result.ExporterNamespace, result.ExporterName),
				"payload_count", result.PayloadCount)

			// Emit success event on the HumioTelemetryExport resource
			if exporterConfig != nil {
				r.Recorder.Eventf(exporterConfig, corev1.EventTypeNormal, "TelemetryDataExported",
					"Successfully exported %d telemetry payloads from collection %s/%s",
					result.PayloadCount, htc.Namespace, htc.Name)
			}
		} else {
			r.Log.Error(result.Error, "Failed to push to exporter",
				"collection", htc.Name,
				"exporter", fmt.Sprintf("%s/%s", result.ExporterNamespace, result.ExporterName))

			// Emit failure event on the HumioTelemetryExport resource
			if exporterConfig != nil {
				r.Recorder.Eventf(exporterConfig, corev1.EventTypeWarning, "TelemetryDataExportFailed",
					"Failed to export telemetry data from collection %s/%s: %v",
					htc.Namespace, htc.Name, result.Error)
			}
		}
	}

	return results
}

// pushToSingleExporter handles pushing to a single exporter
func (r *HumioTelemetryCollectionReconciler) pushToSingleExporter(ctx context.Context, _ *humiov1alpha1.HumioTelemetryCollection, payloads []humio.TelemetryPayload, exporterInfo ExporterInfo, resultChan chan<- ExportResult) {
	result := ExportResult{
		ExporterName:      exporterInfo.Name,
		ExporterNamespace: exporterInfo.Namespace,
		ExportedAt:        time.Now(),
		PayloadCount:      len(payloads),
	}

	// Resolve telemetry token for this exporter
	token, err := r.resolveTelemetryToken(ctx, exporterInfo.Config)
	if err != nil {
		result.Error = fmt.Errorf("failed to resolve token for exporter %s/%s: %w", exporterInfo.Namespace, exporterInfo.Name, err)
		resultChan <- result
		return
	}

	// Extract TLS configuration
	insecureSkipVerify := false
	if exporterInfo.Config.Spec.RemoteReport.TLS != nil {
		insecureSkipVerify = exporterInfo.Config.Spec.RemoteReport.TLS.InsecureSkipVerify
	}

	// Get the exporter's SendCollectionErrors setting (defaults to true)
	sendCollectionErrors := true
	if exporterInfo.Config.Spec.SendCollectionErrors != nil {
		sendCollectionErrors = *exporterInfo.Config.Spec.SendCollectionErrors
	}

	// Filter payloads based on exporter configuration
	filteredPayloads := make([]humio.TelemetryPayload, 0, len(payloads))
	for _, payload := range payloads {
		// Skip collection error payloads if this exporter doesn't want them
		if payload.CollectionType == humio.TelemetryCollectionTypeCollectionErrors && !sendCollectionErrors {
			r.Log.V(1).Info("Skipping collection error payload for exporter",
				"exporter", fmt.Sprintf("%s/%s", exporterInfo.Namespace, exporterInfo.Name),
				"sendCollectionErrors", sendCollectionErrors)
			continue
		}
		filteredPayloads = append(filteredPayloads, payload)
	}

	// Update result with filtered payload count
	result.PayloadCount = len(filteredPayloads)

	r.Log.V(1).Info("Filtering payloads for exporter",
		"exporter", fmt.Sprintf("%s/%s", exporterInfo.Namespace, exporterInfo.Name),
		"original_count", len(payloads),
		"filtered_count", len(filteredPayloads),
		"sendCollectionErrors", sendCollectionErrors)

	// Create exporter and push data
	exporter := humio.NewTelemetryExporter(
		exporterInfo.Config.Spec.RemoteReport.URL,
		token,
		insecureSkipVerify,
		r.Log.WithValues("exporter", fmt.Sprintf("%s/%s", exporterInfo.Namespace, exporterInfo.Name)))

	exportErrors := exporter.ExportPayloads(ctx, filteredPayloads)
	if len(exportErrors) == 0 {
		result.Success = true
	} else {
		result.Success = false
		// Combine export errors into a single error
		errorMessages := make([]string, len(exportErrors))
		for i, exportErr := range exportErrors {
			errorMessages[i] = exportErr.Message
		}
		result.Error = fmt.Errorf("export errors: %s", strings.Join(errorMessages, "; "))
	}

	resultChan <- result
}

// resolveTelemetryToken resolves the telemetry token from the secret reference
func (r *HumioTelemetryCollectionReconciler) resolveTelemetryToken(ctx context.Context, export *humiov1alpha1.HumioTelemetryExport) (string, error) {
	secret, err := kubernetes.GetSecret(ctx, r, export.Spec.RemoteReport.Token.SecretKeyRef.Name, export.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s: %w", export.Spec.RemoteReport.Token.SecretKeyRef.Name, err)
	}

	token, exists := secret.Data[export.Spec.RemoteReport.Token.SecretKeyRef.Key]
	if !exists {
		return "", fmt.Errorf("key %s not found in secret %s", export.Spec.RemoteReport.Token.SecretKeyRef.Key, export.Spec.RemoteReport.Token.SecretKeyRef.Name)
	}

	return string(token), nil
}

// Helper methods for processing results and status updates
func (r *HumioTelemetryCollectionReconciler) determineSuccessfulCollections(requestedTypes []string, collectionErrors []humiov1alpha1.HumioTelemetryCollectionError, payloads []humio.TelemetryPayload) []string {
	// Determine which data types are present in the actual payloads
	successfulTypes := make(map[string]bool)
	for _, payload := range payloads {
		if payload.CollectionType != "" {
			successfulTypes[payload.CollectionType] = true
		}
	}

	// Determine successful collections and failed types more efficiently
	var successful []string
	failedTypes := make(map[string]bool)

	// First pass: identify successful collections (those with payloads)
	for _, dataType := range requestedTypes {
		if successfulTypes[dataType] {
			successful = append(successful, dataType)
		} else {
			// Data type not in payloads, check if there were specific errors for this data type
			for _, err := range collectionErrors {
				if r.errorBelongsToDataType(err, dataType) {
					failedTypes[dataType] = true
					break
				}
			}
		}
	}

	return successful
}

func (r *HumioTelemetryCollectionReconciler) buildCollectionStatus(htc *humiov1alpha1.HumioTelemetryCollection, successfulCollections []string, collectionErrors []humiov1alpha1.HumioTelemetryCollectionError, requestedTypes []string, now metav1.Time) map[string]humiov1alpha1.CollectionTypeStatus {
	collectionStatus := make(map[string]humiov1alpha1.CollectionTypeStatus)

	// Copy existing status
	if htc.Status.CollectionStatus != nil {
		for dataType, status := range htc.Status.CollectionStatus {
			collectionStatus[dataType] = status
		}
	}

	// Map collection errors to specific data types
	errorsByType := r.mapErrorsToDataTypes(collectionErrors, requestedTypes)

	// Update status for all requested types
	for _, dataType := range requestedTypes {
		currentStatus, exists := collectionStatus[dataType]
		if !exists {
			currentStatus = humiov1alpha1.CollectionTypeStatus{}
		}

		// Check if this type was successful in this run
		isSuccessful := r.contains(successfulCollections, dataType)

		if isSuccessful {
			// Data type succeeded - update success timestamp and clear error
			currentStatus.LastCollection = &now
			currentStatus.LastError = nil
		} else {
			// Data type failed or wasn't attempted
			currentStatus.LastCollection = &now

			// Add error if there's one for this data type
			if err, hasError := errorsByType[dataType]; hasError {
				currentStatus.LastError = &err
			}
		}

		collectionStatus[dataType] = currentStatus
	}

	return collectionStatus
}

func (r *HumioTelemetryCollectionReconciler) mapErrorsToDataTypes(errors []humiov1alpha1.HumioTelemetryCollectionError, dataTypes []string) map[string]humiov1alpha1.HumioTelemetryCollectionError {
	errorsByType := make(map[string]humiov1alpha1.HumioTelemetryCollectionError)

	for _, err := range errors {
		for _, dataType := range dataTypes {
			if r.errorBelongsToDataType(err, dataType) {
				errorsByType[dataType] = err
				break
			}
		}
	}

	return errorsByType
}

func (r *HumioTelemetryCollectionReconciler) getFailedTypesFromErrors(errors []humiov1alpha1.HumioTelemetryCollectionError, requestedTypes []string) []string {
	failedTypes := make(map[string]bool)

	for _, err := range errors {
		for _, dataType := range requestedTypes {
			if r.errorBelongsToDataType(err, dataType) {
				failedTypes[dataType] = true
				break
			}
		}
	}

	failed := make([]string, 0, len(failedTypes))
	for dataType := range failedTypes {
		failed = append(failed, dataType)
	}

	return failed
}

func (r *HumioTelemetryCollectionReconciler) errorBelongsToDataType(err humiov1alpha1.HumioTelemetryCollectionError, dataType string) bool {
	// If the error has a specific DataType field set, use that for exact matching
	if err.DataType != "" {
		return err.DataType == dataType
	}

	// Check for common patterns: "Failed to collect [datatype]" or "[datatype]" in message
	lowerMessage := strings.ToLower(err.Message)
	lowerDataType := strings.ToLower(dataType)

	// Handle underscore vs space variations (e.g., "user_activity" vs "user activity")
	dataTypeWithSpaces := strings.ReplaceAll(lowerDataType, "_", " ")

	// Special case for ingestion_metrics: it also fails when organizational usage data is unavailable
	if dataType == "ingestion_metrics" && strings.Contains(lowerMessage, "organizational usage") {
		return true
	}

	return strings.Contains(lowerMessage, lowerDataType) ||
		strings.Contains(lowerMessage, dataTypeWithSpaces) ||
		strings.Contains(lowerMessage, "failed to collect "+lowerDataType) ||
		strings.Contains(lowerMessage, "failed to collect "+dataTypeWithSpaces)
}

// buildErrorSummaryForEvent creates a concise error summary for Kubernetes events
func (r *HumioTelemetryCollectionReconciler) buildErrorSummaryForEvent(errors []humiov1alpha1.HumioTelemetryCollectionError, maxLength int) string {
	if len(errors) == 0 {
		return "No specific errors available"
	}

	summaries := make([]string, 0, len(errors))
	remainingLength := maxLength

	for i, err := range errors {
		// Create a short summary for this error
		var summary string
		if err.DataType != "" {
			summary = fmt.Sprintf("(%s) %s: %s", err.Type, err.DataType, err.Message)
		} else {
			summary = fmt.Sprintf("(%s) %s", err.Type, err.Message)
		}

		// If this summary would exceed our length limit, truncate it
		if len(summary) > remainingLength {
			if remainingLength > 20 {
				summary = summary[:remainingLength-3] + "..."
			} else {
				// If we can't fit even a truncated version, show count of remaining errors
				if i < len(errors) {
					summaries = append(summaries, fmt.Sprintf("... and %d more", len(errors)-i))
				}
				break
			}
		}

		summaries = append(summaries, summary)
		remainingLength -= len(summary) + 2 // +2 for "; " separator

		// If we're running low on space, add remaining count and stop
		if remainingLength < 20 && i < len(errors)-1 {
			summaries = append(summaries, fmt.Sprintf("... and %d more", len(errors)-i-1))
			break
		}
	}

	return strings.Join(summaries, "; ")
}

func (r *HumioTelemetryCollectionReconciler) contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func (r *HumioTelemetryCollectionReconciler) convertToPushResults(exportResults []ExportResult) []humiov1alpha1.ExportPushResult {
	pushResults := make([]humiov1alpha1.ExportPushResult, len(exportResults))
	for i, result := range exportResults {
		pushResult := humiov1alpha1.ExportPushResult{
			ExporterName:      result.ExporterName,
			ExporterNamespace: result.ExporterNamespace,
			LastPushTime:      &metav1.Time{Time: result.ExportedAt},
			LastPushSuccess:   result.Success,
			TotalPushes:       1,
		}

		if result.Success {
			pushResult.SuccessfulPushes = 1
		} else if result.Error != nil {
			pushResult.LastPushError = &humiov1alpha1.HumioTelemetryCollectionError{
				Type:      humio.TelemetryErrorTypeExport,
				Message:   result.Error.Error(),
				Timestamp: metav1.Time{Time: result.ExportedAt},
			}
		}

		pushResults[i] = pushResult
	}
	return pushResults
}

// Status update methods
func (r *HumioTelemetryCollectionReconciler) updateStatus(ctx context.Context, htc *humiov1alpha1.HumioTelemetryCollection, state string) (reconcile.Result, error) {
	return r.updateStatusWithDetails(ctx, htc, state, nil, nil)
}

func (r *HumioTelemetryCollectionReconciler) updateStatusWithDetails(ctx context.Context, htc *humiov1alpha1.HumioTelemetryCollection, state string, collectionStatus map[string]humiov1alpha1.CollectionTypeStatus, pushResults []humiov1alpha1.ExportPushResult) (reconcile.Result, error) {
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &humiov1alpha1.HumioTelemetryCollection{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(htc), current); err != nil {
			return err
		}

		current.Status.State = state

		if collectionStatus != nil {
			if current.Status.CollectionStatus == nil {
				current.Status.CollectionStatus = make(map[string]humiov1alpha1.CollectionTypeStatus)
			}
			for dataType, status := range collectionStatus {
				current.Status.CollectionStatus[dataType] = status
			}
		}

		if pushResults != nil {
			current.Status.ExportPushResults = pushResults
		}

		// Update timestamps based on state
		now := metav1.Now()
		switch state {
		case humiov1alpha1.HumioTelemetryCollectionStateCollecting:
			// Don't update timestamps when starting collection
		case humiov1alpha1.HumioTelemetryCollectionStateEnabled:
			current.Status.LastCollectionTime = &now
		}

		return r.Status().Update(ctx, current)
	}); err != nil {
		r.Log.Error(err, "Failed to update HumioTelemetryCollection status", "state", state)
		if k8serrors.IsConflict(err) {
			r.Log.Info("Status update conflict - will retry on next reconcile", "state", state)
			return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}

	if state == humiov1alpha1.HumioTelemetryCollectionStateConfigError {
		return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
	}

	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

func (r *HumioTelemetryCollectionReconciler) updateStatusWithConfigError(ctx context.Context, htc *humiov1alpha1.HumioTelemetryCollection, err error) (reconcile.Result, error) {
	// Check if this is a data type validation error
	if strings.Contains(err.Error(), "invalid data type:") {
		parts := strings.Split(err.Error(), "invalid data type: ")
		if len(parts) > 1 {
			invalidDataType := parts[1]

			now := metav1.Now()
			collectionStatus := make(map[string]humiov1alpha1.CollectionTypeStatus)

			collectionStatus[invalidDataType] = humiov1alpha1.CollectionTypeStatus{
				LastError: &humiov1alpha1.HumioTelemetryCollectionError{
					Type:      humio.TelemetryErrorTypeValidation,
					Message:   fmt.Sprintf("invalid data type: %s", invalidDataType),
					Timestamp: now,
					DataType:  invalidDataType,
				},
			}

			return r.updateStatusWithDetails(ctx, htc, humiov1alpha1.HumioTelemetryCollectionStateConfigError, collectionStatus, nil)
		}
	}

	return r.updateStatus(ctx, htc, humiov1alpha1.HumioTelemetryCollectionStateConfigError)
}

// cleanupTelemetryCollection handles cleanup when the resource is being deleted
func (r *HumioTelemetryCollectionReconciler) cleanupTelemetryCollection(ctx context.Context, htc *humiov1alpha1.HumioTelemetryCollection) (reconcile.Result, error) {
	r.Log.Info("Cleaning up HumioTelemetryCollection")

	// Remove finalizer
	htc.Finalizers = helpers.RemoveElement(htc.Finalizers, HumioFinalizer)
	return reconcile.Result{}, r.Update(ctx, htc)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioTelemetryCollectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("humiotelemetrycollection-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioTelemetryCollection{}).
		Complete(r)
}
