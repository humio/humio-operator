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
	"strconv"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioTelemetryReconciler reconciles a HumioTelemetry object
type HumioTelemetryReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetries/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioTelemetryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioTelemetry")

	ht := &humiov1alpha1.HumioTelemetry{}
	err := r.Get(ctx, req.NamespacedName, ht)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", ht.UID)

	// Handle deletion
	if ht.DeletionTimestamp != nil {
		r.Log.Info("HumioTelemetry marked for deletion")
		return r.cleanupTelemetry(ctx, ht)
	}

	// Add finalizer if not present
	if !helpers.ContainsElement(ht.Finalizers, HumioFinalizer) {
		r.Log.Info("Adding finalizer to HumioTelemetry")
		ht.Finalizers = append(ht.Finalizers, HumioFinalizer)

		// Increment metrics for new resource creation
		humioTelemetryPrometheusMetrics.Counters.TelemetryResourcesCreated.Inc()
		humioTelemetryPrometheusMetrics.Gauges.ActiveTelemetryResources.Inc()

		if err := r.Update(ctx, ht); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// Validate configuration
	if err := r.validateConfiguration(ht); err != nil {
		r.Log.Error(err, "Invalid telemetry configuration")
		return r.updateStatusWithConfigError(ctx, ht, err)
	}

	// Get the associated HumioCluster
	hc, err := r.getHumioCluster(ctx, ht)
	if err != nil {
		r.Log.Error(err, "Failed to get associated HumioCluster")
		return r.updateStatus(ctx, ht, humiov1alpha1.HumioTelemetryStateConfigError)
	}

	// Check if telemetry should run based on collection schedules
	shouldCollect, collectionTypes, nextCollection := r.ShouldRunTelemetryCollection(ht)
	if !shouldCollect {
		r.Log.V(1).Info("No telemetry collection needed at this time", "next_collection", nextCollection)
		return reconcile.Result{RequeueAfter: time.Until(nextCollection)}, nil
	}

	r.Log.Info("Running telemetry collection", "types", collectionTypes)

	// Update status to collecting
	if result, err := r.updateStatus(ctx, ht, humiov1alpha1.HumioTelemetryStateCollecting); err != nil {
		return result, err
	}

	// Perform telemetry collection and export
	exportErrors := r.collectAndExportTelemetry(ctx, ht, hc, collectionTypes)

	// Update status based on results
	now := metav1.Now()
	ht.Status.LastCollectionTime = &now

	if len(exportErrors) == 0 {
		ht.Status.LastExportTime = &now
		ht.Status.ExportErrors = nil
		return r.updateStatus(ctx, ht, humiov1alpha1.HumioTelemetryStateEnabled)
	} else {
		ht.Status.ExportErrors = exportErrors
		return r.updateStatus(ctx, ht, humiov1alpha1.HumioTelemetryStateEnabled)
	}
}

// validateConfiguration validates the telemetry configuration
func (r *HumioTelemetryReconciler) validateConfiguration(ht *humiov1alpha1.HumioTelemetry) error {
	// Validate cluster identifier
	if ht.Spec.ClusterIdentifier == "" {
		return fmt.Errorf("clusterIdentifier is required")
	}

	// Validate managed cluster exists
	if ht.Spec.ManagedClusterName == "" {
		return fmt.Errorf("managedClusterName is required")
	}

	// Validate remote report configuration
	if ht.Spec.RemoteReport.URL == "" {
		return fmt.Errorf("remoteReport.url is required")
	}

	// Validate token secret
	if ht.Spec.RemoteReport.Token.SecretKeyRef == nil {
		return fmt.Errorf("remoteReport.token.secretKeyRef is required")
	}

	// Validate collections
	if len(ht.Spec.Collections) == 0 {
		return fmt.Errorf("at least one collection must be specified")
	}

	for _, collection := range ht.Spec.Collections {
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
func (r *HumioTelemetryReconciler) getHumioCluster(ctx context.Context, ht *humiov1alpha1.HumioTelemetry) (*humiov1alpha1.HumioCluster, error) {
	hc := &humiov1alpha1.HumioCluster{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      ht.Spec.ManagedClusterName,
		Namespace: ht.Namespace,
	}, hc)
	return hc, err
}

// ShouldRunTelemetryCollection determines if telemetry collection should run based on schedules
func (r *HumioTelemetryReconciler) ShouldRunTelemetryCollection(ht *humiov1alpha1.HumioTelemetry) (bool, []string, time.Time) {
	now := time.Now()
	var shouldCollect bool
	var collectTypes []string
	var nextCollection time.Time

	// Initialize next collection to far future
	nextCollection = now.Add(24 * time.Hour)

	for _, collection := range ht.Spec.Collections {
		interval, err := parseDuration(collection.Interval)
		if err != nil {
			r.Log.Error(err, "Failed to parse interval", "interval", collection.Interval)
			continue
		}

		// Determine last collection time for this collection type
		var lastCollection time.Time
		if ht.Status.CollectionStatus != nil {
			for _, dataType := range collection.Include {
				if status, exists := ht.Status.CollectionStatus[dataType]; exists && status.LastCollection != nil {
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

// collectAndExportTelemetry performs the actual telemetry collection and export
func (r *HumioTelemetryReconciler) collectAndExportTelemetry(ctx context.Context, ht *humiov1alpha1.HumioTelemetry, hc *humiov1alpha1.HumioCluster, dataTypes []string) []humiov1alpha1.TelemetryError {
	// Get Humio HTTP client
	cluster, err := helpers.NewCluster(ctx, r.Client, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		return []humiov1alpha1.TelemetryError{{
			Type:      "client",
			Message:   fmt.Sprintf("Failed to get Humio client config for cluster '%s/%s': %v", hc.Namespace, hc.Name, err),
			Timestamp: metav1.Now(),
		}}
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), reconcile.Request{NamespacedName: types.NamespacedName{Name: hc.Name, Namespace: hc.Namespace}})

	// Collect telemetry data
	humioTelemetryPrometheusMetrics.Counters.CollectionsTotal.Inc()
	payloads, err := r.HumioClient.CollectTelemetryData(ctx, humioHttpClient, dataTypes, ht.Spec.ClusterIdentifier)
	if err != nil {
		humioTelemetryPrometheusMetrics.Counters.CollectionsErrorTotal.Inc()
		return []humiov1alpha1.TelemetryError{{
			Type:      "collection",
			Message:   fmt.Sprintf("Failed to collect telemetry data for types [%v] from cluster '%s': %v", dataTypes, ht.Spec.ClusterIdentifier, err),
			Timestamp: metav1.Now(),
		}}
	}
	humioTelemetryPrometheusMetrics.Counters.CollectionsSuccessTotal.Inc()

	// Resolve telemetry token
	token, err := r.resolveTelemetryToken(ctx, ht)
	if err != nil {
		return []humiov1alpha1.TelemetryError{{
			Type:      "configuration",
			Message:   fmt.Sprintf("Failed to resolve telemetry token from secret '%s/%s': %v", ht.Namespace, ht.Spec.RemoteReport.Token.SecretKeyRef.Name, err),
			Timestamp: metav1.Now(),
		}}
	}

	// Extract TLS configuration
	insecureSkipVerify := false
	if ht.Spec.RemoteReport.TLS != nil {
		insecureSkipVerify = ht.Spec.RemoteReport.TLS.InsecureSkipVerify
	}

	// Create exporter and export data
	exporter := humio.NewTelemetryExporter(ht.Spec.RemoteReport.URL, token, insecureSkipVerify, r.Log)
	humioTelemetryPrometheusMetrics.Counters.ExportsTotal.Inc()
	exportErrors := exporter.ExportPayloads(ctx, payloads)

	// Update export metrics based on results
	if len(exportErrors) == 0 {
		humioTelemetryPrometheusMetrics.Counters.ExportsSuccessTotal.Inc()
	} else {
		humioTelemetryPrometheusMetrics.Counters.ExportsErrorTotal.Inc()
	}

	// Convert export errors to telemetry errors
	telemetryErrors := make([]humiov1alpha1.TelemetryError, 0, len(exportErrors))
	for _, exportErr := range exportErrors {
		telemetryErrors = append(telemetryErrors, humiov1alpha1.TelemetryError{
			Type:      exportErr.Type,
			Message:   exportErr.Message,
			Timestamp: metav1.Time{Time: exportErr.Timestamp},
		})
	}

	return telemetryErrors
}

// resolveTelemetryToken resolves the telemetry token from the secret reference
func (r *HumioTelemetryReconciler) resolveTelemetryToken(ctx context.Context, ht *humiov1alpha1.HumioTelemetry) (string, error) {
	secret, err := kubernetes.GetSecret(ctx, r, ht.Spec.RemoteReport.Token.SecretKeyRef.Name, ht.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s: %w", ht.Spec.RemoteReport.Token.SecretKeyRef.Name, err)
	}

	token, exists := secret.Data[ht.Spec.RemoteReport.Token.SecretKeyRef.Key]
	if !exists {
		return "", fmt.Errorf("key %s not found in secret %s", ht.Spec.RemoteReport.Token.SecretKeyRef.Key, ht.Spec.RemoteReport.Token.SecretKeyRef.Name)
	}

	return string(token), nil
}

// cleanupTelemetry handles cleanup when the resource is being deleted
func (r *HumioTelemetryReconciler) cleanupTelemetry(ctx context.Context, ht *humiov1alpha1.HumioTelemetry) (reconcile.Result, error) {
	r.Log.Info("Cleaning up HumioTelemetry")

	// Increment metrics for resource deletion
	humioTelemetryPrometheusMetrics.Counters.TelemetryResourcesDeleted.Inc()
	humioTelemetryPrometheusMetrics.Gauges.ActiveTelemetryResources.Dec()

	// Remove finalizer
	ht.Finalizers = helpers.RemoveElement(ht.Finalizers, HumioFinalizer)
	return reconcile.Result{}, r.Update(ctx, ht)
}

// updateStatus updates the HumioTelemetry status
func (r *HumioTelemetryReconciler) updateStatus(ctx context.Context, ht *humiov1alpha1.HumioTelemetry, state string) (reconcile.Result, error) {
	ht.Status.State = state

	err := r.Status().Update(ctx, ht)
	if err != nil {
		r.Log.Error(err, "Failed to update HumioTelemetry status")
		return reconcile.Result{}, err
	}

	if state == humiov1alpha1.HumioTelemetryStateConfigError {
		return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
	}

	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// updateStatusWithConfigError updates the HumioTelemetry status to ConfigError and stores the error in CollectionErrors
func (r *HumioTelemetryReconciler) updateStatusWithConfigError(ctx context.Context, ht *humiov1alpha1.HumioTelemetry, configErr error) (reconcile.Result, error) {
	ht.Status.State = humiov1alpha1.HumioTelemetryStateConfigError

	// Add the configuration error to CollectionErrors
	now := metav1.Now()
	telemetryError := humiov1alpha1.TelemetryError{
		Type:      "configuration",
		Message:   configErr.Error(),
		Timestamp: now,
	}

	// Clear existing errors and add the new configuration error
	ht.Status.CollectionErrors = []humiov1alpha1.TelemetryError{telemetryError}

	err := r.Status().Update(ctx, ht)
	if err != nil {
		r.Log.Error(err, "Failed to update HumioTelemetry status")
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// isValidDataType checks if a data type is supported
func isValidDataType(dataType string) bool {
	validTypes := map[string]bool{
		"license":         true,
		"cluster_info":    true,
		"user_info":       true,
		"repository_info": true,
	}
	return validTypes[dataType]
}

// parseDuration parses duration strings like "15m", "1h", "1d"
func parseDuration(duration string) (time.Duration, error) {
	if len(duration) < 2 {
		return 0, fmt.Errorf("invalid duration format: %s", duration)
	}

	unit := duration[len(duration)-1:]
	value := duration[:len(duration)-1]

	var d time.Duration
	var err error

	switch unit {
	case "s":
		d, err = time.ParseDuration(duration)
	case "m":
		d, err = time.ParseDuration(duration)
	case "h":
		d, err = time.ParseDuration(duration)
	case "d":
		// Parse days manually since Go's time package doesn't support days
		if dayCount, parseErr := strconv.Atoi(value); parseErr == nil {
			d = time.Duration(dayCount) * 24 * time.Hour
		} else {
			err = fmt.Errorf("invalid day count in duration %s: %w", duration, parseErr)
		}
	default:
		err = fmt.Errorf("unsupported duration unit: %s", unit)
	}

	return d, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioTelemetryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioTelemetry{}).
		Complete(r)
}
