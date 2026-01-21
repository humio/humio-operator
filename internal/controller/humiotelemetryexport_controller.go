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

// HumioTelemetryExportReconciler reconciles a HumioTelemetryExport object
type HumioTelemetryExportReconciler struct {
	client.Client
	CommonConfig
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
	Recorder   record.EventRecorder
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetryexports,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetryexports/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetryexports/finalizers,verbs=update
// +kubebuilder:rbac:groups=core.humio.com,resources=humiotelemetrycollections,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioTelemetryExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioTelemetryExport")

	hte := &humiov1alpha1.HumioTelemetryExport{}
	err := r.Get(ctx, req.NamespacedName, hte)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", hte.UID)

	// Handle deletion
	if hte.DeletionTimestamp != nil {
		r.Log.Info("HumioTelemetryExport marked for deletion")
		return r.cleanupTelemetryExport(ctx, hte)
	}

	// Add finalizer if not present
	if !helpers.ContainsElement(hte.Finalizers, HumioFinalizer) {
		r.Log.Info("Adding finalizer to HumioTelemetryExport")
		hte.Finalizers = append(hte.Finalizers, HumioFinalizer)

		if err := r.Update(ctx, hte); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// Validate configuration
	if err := r.validateConfiguration(hte); err != nil {
		r.Log.Error(err, "Invalid telemetry export configuration")
		r.Recorder.Eventf(hte, corev1.EventTypeWarning, "TelemetryExportConfigurationError",
			"Invalid telemetry export configuration: %v", err)
		return r.updateStatus(ctx, hte, humiov1alpha1.HumioTelemetryExportStateConfigError, err)
	}

	// Verify all registered collections exist and are accessible
	collectionStatuses := r.verifyRegisteredCollections(ctx, hte)

	// Check if any collections are missing
	hasMissingCollections := false
	for _, status := range collectionStatuses {
		if !status.Found {
			hasMissingCollections = true
			break
		}
	}

	if hasMissingCollections {
		r.Log.Info("Some registered collections are missing", "export", hte.Name)
		r.Recorder.Eventf(hte, corev1.EventTypeWarning, "MissingCollections",
			"Some registered collections are not found or inaccessible")
		return r.updateStatusWithCollections(ctx, hte, humiov1alpha1.HumioTelemetryExportStateConfigError, collectionStatuses, nil)
	}

	r.Log.Info("All registered collections verified successfully", "export", hte.Name, "collection_count", len(collectionStatuses))
	r.Recorder.Eventf(hte, corev1.EventTypeNormal, "CollectionsVerified",
		"All %d registered collections verified successfully", len(collectionStatuses))

	// Update export status with collection verification results
	return r.updateStatusWithCollections(ctx, hte, humiov1alpha1.HumioTelemetryExportStateEnabled, collectionStatuses, nil)
}

// validateConfiguration validates the telemetry export configuration
func (r *HumioTelemetryExportReconciler) validateConfiguration(hte *humiov1alpha1.HumioTelemetryExport) error {
	// Validate remote report configuration
	if hte.Spec.RemoteReport.URL == "" {
		return fmt.Errorf("remoteReport.url is required")
	}

	// Validate token secret
	if hte.Spec.RemoteReport.Token.SecretKeyRef == nil {
		return fmt.Errorf("remoteReport.token.secretKeyRef is required")
	}

	// Validate registered collections
	if len(hte.Spec.RegisteredCollections) == 0 {
		return fmt.Errorf("at least one registered collection must be specified")
	}

	for _, regCol := range hte.Spec.RegisteredCollections {
		if regCol.Name == "" {
			return fmt.Errorf("registered collection name is required")
		}
	}

	// Validate token secret exists and is accessible
	_, err := r.resolveSecretToken(context.Background(), hte)
	if err != nil {
		return fmt.Errorf("failed to resolve telemetry token: %w", err)
	}

	return nil
}

// verifyRegisteredCollections ensures all referenced collections exist and are accessible
func (r *HumioTelemetryExportReconciler) verifyRegisteredCollections(ctx context.Context, hte *humiov1alpha1.HumioTelemetryExport) map[string]humiov1alpha1.HumioTelemetryCollectionRegistrationStatus {
	collectionStatuses := make(map[string]humiov1alpha1.HumioTelemetryCollectionRegistrationStatus)

	for _, regCol := range hte.Spec.RegisteredCollections {
		// Resolve namespace - defaults to export's namespace if not specified
		regNamespace := regCol.Namespace
		if regNamespace == "" {
			regNamespace = hte.Namespace
		}

		collectionKey := fmt.Sprintf("%s/%s", regNamespace, regCol.Name)

		// Try to get the collection resource
		collection := &humiov1alpha1.HumioTelemetryCollection{}
		err := r.Get(ctx, types.NamespacedName{Name: regCol.Name, Namespace: regNamespace}, collection)

		status := humiov1alpha1.HumioTelemetryCollectionRegistrationStatus{}
		if err != nil {
			if k8serrors.IsNotFound(err) {
				r.Log.Info("Registered collection not found", "collection", collectionKey, "export", hte.Name)
				status.Found = false
			} else {
				r.Log.Error(err, "Failed to get registered collection", "collection", collectionKey, "export", hte.Name)
				status.Found = false
				status.LastExportError = &humiov1alpha1.HumioTelemetryExportError{
					Type:      humio.TelemetryErrorTypeCollectionAccess,
					Message:   fmt.Sprintf("Failed to access collection %s: %v", collectionKey, err),
					Timestamp: metav1.Now(),
				}
			}
		} else {
			r.Log.Info("Registered collection found and accessible", "collection", collectionKey, "export", hte.Name)
			status.Found = true
		}

		collectionStatuses[collectionKey] = status
	}

	return collectionStatuses
}

// resolveSecretToken resolves the telemetry token from the secret reference
func (r *HumioTelemetryExportReconciler) resolveSecretToken(ctx context.Context, hte *humiov1alpha1.HumioTelemetryExport) (string, error) {
	secret, err := kubernetes.GetSecret(ctx, r, hte.Spec.RemoteReport.Token.SecretKeyRef.Name, hte.Namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s: %w", hte.Spec.RemoteReport.Token.SecretKeyRef.Name, err)
	}

	token, exists := secret.Data[hte.Spec.RemoteReport.Token.SecretKeyRef.Key]
	if !exists {
		return "", fmt.Errorf("key %s not found in secret %s", hte.Spec.RemoteReport.Token.SecretKeyRef.Key, hte.Spec.RemoteReport.Token.SecretKeyRef.Name)
	}

	return string(token), nil
}

// Status update methods
func (r *HumioTelemetryExportReconciler) updateStatus(ctx context.Context, hte *humiov1alpha1.HumioTelemetryExport, state string, err error) (reconcile.Result, error) {
	var exportErrors []humiov1alpha1.HumioTelemetryExportError
	if err != nil {
		exportErrors = []humiov1alpha1.HumioTelemetryExportError{{
			Type:      humio.TelemetryErrorTypeConfiguration,
			Message:   err.Error(),
			Timestamp: metav1.Now(),
		}}
	}
	return r.updateStatusWithCollections(ctx, hte, state, nil, exportErrors)
}

func (r *HumioTelemetryExportReconciler) updateStatusWithCollections(ctx context.Context, hte *humiov1alpha1.HumioTelemetryExport, state string, collectionStatuses map[string]humiov1alpha1.HumioTelemetryCollectionRegistrationStatus, exportErrors []humiov1alpha1.HumioTelemetryExportError) (reconcile.Result, error) {
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &humiov1alpha1.HumioTelemetryExport{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hte), current); err != nil {
			return err
		}

		// Update status fields
		current.Status.State = state

		if collectionStatuses != nil {
			if current.Status.RegisteredCollectionStatus == nil {
				current.Status.RegisteredCollectionStatus = make(map[string]humiov1alpha1.HumioTelemetryCollectionRegistrationStatus)
			}
			for collectionKey, status := range collectionStatuses {
				current.Status.RegisteredCollectionStatus[collectionKey] = status
			}
		}

		if exportErrors != nil {
			current.Status.ExportErrors = exportErrors
		}

		// Update timestamps based on state
		switch state {
		case humiov1alpha1.HumioTelemetryExportStateEnabled:
			// For successful verification/configuration, clear errors
			if len(exportErrors) == 0 {
				current.Status.ExportErrors = nil
			}
		}

		return r.Status().Update(ctx, current)
	}); err != nil {
		r.Log.Error(err, "Failed to update HumioTelemetryExport status", "state", state)
		if k8serrors.IsConflict(err) {
			r.Log.Info("Status update conflict - will retry on next reconcile", "state", state)
			return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}

	if state == humiov1alpha1.HumioTelemetryExportStateConfigError {
		return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
	}

	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// cleanupTelemetryExport handles cleanup when the resource is being deleted
func (r *HumioTelemetryExportReconciler) cleanupTelemetryExport(ctx context.Context, hte *humiov1alpha1.HumioTelemetryExport) (reconcile.Result, error) {
	r.Log.Info("Cleaning up HumioTelemetryExport")

	// Remove finalizer
	hte.Finalizers = helpers.RemoveElement(hte.Finalizers, HumioFinalizer)
	return reconcile.Result{}, r.Update(ctx, hte)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioTelemetryExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("humiotelemetryexport-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioTelemetryExport{}).
		Complete(r)
}
