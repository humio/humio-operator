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

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	"github.com/humio/humio-operator/internal/registries"
)

// HumioPackageRegistryReconciler reconciles a HumioPackageRegistry object
type HumioPackageRegistryReconciler struct {
	client.Client
	CommonConfig
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
	Recorder   record.EventRecorder
	HTTPClient registries.HTTPClientInterface
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackageregistries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackageregistries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackageregistries/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to move the current state of the cluster closer to the desired state.
func (r *HumioPackageRegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" && r.Namespace != req.Namespace {
		return reconcile.Result{}, nil
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("reconciling HumioPackageRegistry")

	// reading k8s object
	hpr, err := r.getK8sHumioPackageRegistry(ctx, req)
	if hpr == nil {
		// its unexpected so we requeue
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	// Check for rename BEFORE processing the resource
	// This ensures we handle the delete-recreate before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, hpr)
	if err != nil {
		return result, logErrorAndReturn(r.Log, err, "failed to handle package registry rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with creation
		return result, nil
	}

	// handle delete logic
	isMarkedToBeDeleted := hpr.GetDeletionTimestamp() != nil
	if isMarkedToBeDeleted {
		r.Log.Info("HumioPackageRegistry marked to be deleted")
		if helpers.ContainsElement(hpr.GetFinalizers(), HumioFinalizer) {
			// Check for force finalize annotation
			if ShouldForceFinalize(hpr) {
				r.Log.Info("Force finalize annotation detected, removing finalizer without cleanup",
					"resource", hpr.Name,
					"namespace", hpr.Namespace)
				hpr.SetFinalizers(helpers.RemoveElement(hpr.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hpr)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully via force-finalize annotation")
				return reconcile.Result{Requeue: true}, nil
			}

			r.Log.Info("HumioPackageRegistry contains finalizer so run finalize method")
			if err := r.finalize(hpr); err != nil {
				_ = r.setCondition(ctx, hpr,
					humiov1alpha1.PackageRegistryConditionTypeReady,
					metav1.ConditionFalse,
					humiov1alpha1.PackageRegistryReasonUnknown,
					err.Error())
				return reconcile.Result{}, logErrorAndReturn(r.Log, err, "finalize method returned an error")
			}
			// remove finalizer
			hpr.SetFinalizers(helpers.RemoveElement(hpr.GetFinalizers(), HumioFinalizer))
			err := r.Update(ctx, hpr)
			if err != nil {
				return reconcile.Result{}, logErrorAndReturn(r.Log, err, "update to remove finalizer failed")
			}
			// work completed, return
			return reconcile.Result{}, nil
		}
		// finalizer not present, return
		return reconcile.Result{}, nil
	}

	// Add finalizer so we can run cleanup on delete
	if !helpers.ContainsElement(hpr.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to HumioPackageRegistry")
		if err := r.addFinalizer(ctx, hpr); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	// if disabled, set state and return
	if !hpr.Spec.Enabled {
		_ = r.setCondition(ctx, hpr,
			humiov1alpha1.PackageRegistryConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageRegistryReasonDisabled,
			"Registry is disabled")
		return reconcile.Result{}, nil
	}

	// get registry client
	rClient, err := r.getPackageRegistryClient(hpr)
	if err != nil || rClient == nil {
		r.Log.Error(err, "Failed to initialize registry client")
		_ = r.setCondition(ctx, hpr,
			humiov1alpha1.PackageRegistryConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageRegistryReasonConfigError,
			err.Error())
		return reconcile.Result{}, err
	}

	// test client connection
	err = rClient.CheckConnection(ctx)
	if err != nil {
		r.Log.Error(err, "Failed to check registry connection")
		_ = r.setCondition(ctx, hpr,
			humiov1alpha1.PackageRegistryConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageRegistryReasonConfigError,
			err.Error())
		return reconcile.Result{}, err
	}

	err = r.setCondition(ctx, hpr,
		humiov1alpha1.PackageRegistryConditionTypeReady,
		metav1.ConditionTrue,
		humiov1alpha1.PackageRegistryReasonActive,
		"Connection tested successfully")
	if err != nil {
		return ctrl.Result{}, err
	}

	r.Log.Info("registry is healthy and active")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioPackageRegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPackageRegistry{}).
		Named("humiopackageregistry").
		Complete(r)
}

func (r *HumioPackageRegistryReconciler) addFinalizer(ctx context.Context, hpr *humiov1alpha1.HumioPackageRegistry) error {
	r.Log.Info("Adding Finalizer for HumioPackageRegistry")
	hpr.SetFinalizers(append(hpr.GetFinalizers(), HumioFinalizer))

	// Update CR
	err := r.Update(ctx, hpr)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioPackageRegistry with finalizer")
	}
	return nil
}

func (r *HumioPackageRegistryReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

func (r *HumioPackageRegistryReconciler) finalize(hpr *humiov1alpha1.HumioPackageRegistry) error {
	// Check if data deletion is allowed
	if !hpr.Spec.AllowDataDeletion {
		return fmt.Errorf("package registry may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
	}

	// Audit log before deletion
	r.Log.Info("Proceeding with package registry deletion",
		"allowDataDeletion", hpr.Spec.AllowDataDeletion,
		"registryName", hpr.Spec.RegistryType,
		"namespace", hpr.Namespace,
		"deletionTimestamp", hpr.GetDeletionTimestamp(),
	)

	// No actual deletion needed - PackageRegistry is a configuration resource
	return nil
}

// setCondition sets a condition on the HumioPackageRegistry resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioPackageRegistryReconciler) setCondition(ctx context.Context,
	hpr *humiov1alpha1.HumioPackageRegistry,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioPackageRegistry{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hpr), latest); err != nil {
			return err
		}

		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               conditionType,
			Status:             status,
			ObservedGeneration: latest.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		})

		// BACKWARD COMPATIBILITY: Update State and Message fields based on condition
		latest.Status.State = r.stateFromCondition(status, reason)
		latest.Status.Message = message

		// Track the synced name when package registry is ready
		if conditionType == humiov1alpha1.PackageRegistryConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.DisplayName
		}

		return r.Status().Update(ctx, latest)
	})
}

// stateFromCondition converts condition status and reason to legacy State field value
//
//nolint:unparam // reason parameter kept for consistency with other controllers
func (r *HumioPackageRegistryReconciler) stateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioPackageRegistryStateExists
	}
	switch reason {
	case humiov1alpha1.PackageRegistryReasonDisabled:
		return humiov1alpha1.HumioPackageRegistryStateDisabled
	case humiov1alpha1.PackageRegistryReasonConfigError:
		return humiov1alpha1.HumioPackageRegistryStateConfigError
	default:
		return humiov1alpha1.HumioPackageRegistryStateUnknown
	}
}

func (r *HumioPackageRegistryReconciler) getK8sHumioPackageRegistry(ctx context.Context, req ctrl.Request) (*humiov1alpha1.HumioPackageRegistry, error) {
	hpr := &humiov1alpha1.HumioPackageRegistry{}
	err := r.Get(ctx, req.NamespacedName, hpr)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return hpr, nil
}

// getRegistryClient return a RegistryClientInterface from the provided HumioPackageRegistry
func (r *HumioPackageRegistryReconciler) getPackageRegistryClient(hpr *humiov1alpha1.HumioPackageRegistry) (registries.RegistryClientInterface, error) {
	client, err := registries.NewPackageRegistryClient(hpr, r.HTTPClient, r.Client, hpr.Namespace, r.Log)
	if err != nil {
		fmt.Printf("Could not initiate PackageRegistryClient for type: %s", hpr.Spec.RegistryType)
	}
	return client, err
}

// detectAndHandleRename checks if the package registry name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
// Note: HumioPackageRegistry uses Spec.DisplayName instead of Spec.Name
// Note: PackageRegistry is Kubernetes-only and doesn't require LogScale API calls
func (r *HumioPackageRegistryReconciler) detectAndHandleRename(ctx context.Context,
	hpr *humiov1alpha1.HumioPackageRegistry) (bool, reconcile.Result, error) {

	// Skip rename check if resource is being deleted
	if hpr.GetDeletionTimestamp() != nil {
		return false, reconcile.Result{}, nil
	}

	// Only check if we have a previously synced name
	if hpr.Status.LastSyncedName == "" {
		return false, reconcile.Result{}, nil
	}

	// No rename needed (note: comparing DisplayName, not Name)
	if hpr.Status.LastSyncedName == hpr.Spec.DisplayName {
		return false, reconcile.Result{}, nil
	}

	r.Log.Info("Package registry display name change detected",
		"namespace", hpr.Namespace,
		"name", hpr.Name,
		"oldName", hpr.Status.LastSyncedName,
		"newName", hpr.Spec.DisplayName)

	// Require explicit annotation for safety
	if hpr.Annotations["humio.com/allow-rename"] != AllowRenameAnnotationValue {
		err := fmt.Errorf("package registry display name change detected (from %q to %q), but the required annotation is not set. "+
			"To proceed, add the annotation 'humio.com/allow-rename: \"true\"' to this resource",
			hpr.Status.LastSyncedName, hpr.Spec.DisplayName)

		setStateErr := r.setCondition(ctx, hpr,
			humiov1alpha1.PackageRegistryConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageRegistryReasonConfigError,
			err.Error())
		if setStateErr != nil {
			return false, reconcile.Result{}, setStateErr
		}

		r.Log.Error(err, "blocking package registry rename - annotation required")
		return true, reconcile.Result{}, nil
	}

	r.Log.Info("HumioPackageRegistry rename does not require LogScale API calls",
		"oldName", hpr.Status.LastSyncedName,
		"newName", hpr.Spec.DisplayName,
		"reason", "Package registries are Kubernetes-only resources")

	// Clear the lastSyncedName so the normal reconcile validates with the new name
	hpr.Status.LastSyncedName = ""
	if err := r.Status().Update(ctx, hpr); err != nil {
		return false, reconcile.Result{}, fmt.Errorf("failed to clear lastSyncedName: %w", err)
	}

	r.Log.Info("Package registry rename complete, requeueing",
		"newName", hpr.Spec.DisplayName)

	return true, reconcile.Result{Requeue: true}, nil
}
