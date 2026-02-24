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
	"errors"
	"fmt"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioIPFilterReconciler reconciles a HumioIPFilter object
type HumioIPFilterReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioipfilters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioipfilters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioipfilters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioIPFilterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioIPFilter")

	// reading k8s object
	hi := &humiov1alpha1.HumioIPFilter{}
	err := r.Get(ctx, req.NamespacedName, hi)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// setup humio client configuration
	cluster, err := helpers.NewCluster(ctx, r, hi.Spec.ManagedClusterName, hi.Spec.ExternalClusterName, hi.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setCondErr := r.setCondition(ctx, hi,
			humiov1alpha1.IPFilterConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.IPFilterReasonConfigError,
			fmt.Sprintf("Unable to obtain humio client config: %v", err),
			hi.Status.ID)
		if setCondErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setCondErr, "unable to set cluster state")
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}

	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	// This ensures we handle the delete-recreate before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, hi)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle IP filter rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with creation
		return result, nil
	}

	// handle delete logic
	if hi.GetDeletionTimestamp() != nil {
		return r.handleIPFilterDeletion(ctx, humioHttpClient, hi)
	}

	// Add finalizer for IPFilter so we can run cleanup on delete
	if !helpers.ContainsElement(hi.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to IPFilter")
		if err := r.addFinalizer(ctx, hi); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Get or create IPFilter
	result, err = r.ensureIPFilterExists(ctx, humioHttpClient, hi)
	if err != nil || result.Requeue || result.RequeueAfter > 0 {
		return result, err
	}

	// Update IPFilter if needed
	if err := r.updateIPFilterIfNeeded(ctx, humioHttpClient, hi); err != nil {
		return reconcile.Result{}, err
	}

	// Update final status
	r.updateIPFilterFinalStatus(ctx, humioHttpClient, hi)

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// handleIPFilterDeletion handles the deletion logic for IP filters including force finalize and retry logic
func (r *HumioIPFilterReconciler) handleIPFilterDeletion(ctx context.Context, humioHttpClient *humioapi.Client, hi *humiov1alpha1.HumioIPFilter) (ctrl.Result, error) {
	r.Log.Info("IPFilter marked to be deleted")
	if !helpers.ContainsElement(hi.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil
	}

	// Check for force finalize annotation
	if ShouldForceFinalize(hi) {
		r.Log.Info("Force finalize annotation detected, removing finalizer without cleanup",
			"resource", hi.Name,
			"namespace", hi.Namespace)
		hi.SetFinalizers(helpers.RemoveElement(hi.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hi)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Finalizer removed successfully via force-finalize annotation")
		return reconcile.Result{Requeue: true}, nil
	}

	_, err := r.HumioClient.GetIPFilter(ctx, humioHttpClient, hi)
	// first iteration on delete we don't enter here since IPFilter exists
	if errors.As(err, &humioapi.EntityNotFound{}) {
		hi.SetFinalizers(helpers.RemoveElement(hi.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hi)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil
	}

	// first iteration on delete we run the finalize function which includes delete
	r.Log.Info("IPFilter contains finalizer so run finalizer method")
	if err := r.finalize(ctx, humioHttpClient, hi); err != nil {
		return r.handleFinalizationError(ctx, hi, err)
	}

	// If no error was detected, we need to requeue so that we can remove the finalizer
	return reconcile.Result{Requeue: true}, nil
}

// handleFinalizationError handles errors during finalization with special retry logic
func (r *HumioIPFilterReconciler) handleFinalizationError(ctx context.Context, hi *humiov1alpha1.HumioIPFilter, err error) (ctrl.Result, error) {
	// Error during finalization
	// If the cluster is unavailable or the resource is already deleted, users can manually
	// add the 'humio.com/force-finalize: "true"' annotation to remove the finalizer
	r.Log.Error(err, "Failed to finalize IP filter during deletion. "+
		"If the resource is already deleted or the cluster is unavailable, "+
		"add the annotation 'humio.com/force-finalize: \"true\"' to remove the finalizer")

	// Check if the error is due to IPFilter still being in use
	// This can happen when tokens are still being cleaned up from LogScale's internal state
	errMsg := err.Error()
	if strings.Contains(errMsg, "deleteIPFilter Not allowed while the IP filter is in use") {
		return r.handleInUseError(ctx, hi)
	}

	return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
}

// handleInUseError handles the case where IP filter is still in use with retry logic
func (r *HumioIPFilterReconciler) handleInUseError(ctx context.Context, hi *humiov1alpha1.HumioIPFilter) (ctrl.Result, error) {
	// Track retry count using annotations to avoid infinite retry loops
	const maxRetries = 10 // 10 retries * 15s = 2.5 minutes max wait (well under test timeout of 5min)
	retryCountStr := hi.Annotations["humio.com/ipfilter-deletion-retries"]
	retryCount := 0
	if retryCountStr != "" {
		// Ignore parse errors - if annotation is invalid, retryCount stays 0
		_, _ = fmt.Sscanf(retryCountStr, "%d", &retryCount)
	}
	retryCount++

	if retryCount > maxRetries {
		r.Log.Info("IPFilter still in use after max retries, forcing finalizer removal", "retries", retryCount)
		// Force remove finalizer to unblock K8s deletion
		// Note: IPFilter may remain in LogScale - this should be cleaned up manually
		hi.SetFinalizers(helpers.RemoveElement(hi.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hi)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Finalizer removed after max retries - IPFilter may still exist in LogScale")
		return reconcile.Result{Requeue: true}, nil
	}

	// Update retry count annotation
	if hi.Annotations == nil {
		hi.Annotations = make(map[string]string)
	}
	hi.Annotations["humio.com/ipfilter-deletion-retries"] = fmt.Sprintf("%d", retryCount)
	if err := r.Update(ctx, hi); err != nil {
		return reconcile.Result{}, err
	}

	r.Log.Info("IPFilter still in use, will retry deletion after requeue period", "retryCount", retryCount, "maxRetries", maxRetries)
	// Requeue with a delay to allow tokens to be fully removed from LogScale
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// ensureIPFilterExists gets or creates the IP filter
func (r *HumioIPFilterReconciler) ensureIPFilterExists(ctx context.Context, humioHttpClient *humioapi.Client, hi *humiov1alpha1.HumioIPFilter) (ctrl.Result, error) {
	r.Log.Info("get current IPFilter")
	_, err := r.HumioClient.GetIPFilter(ctx, humioHttpClient, hi)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("IPFilter doesn't exist. Now adding IPFilter")
			ipFilterDetails, addErr := r.HumioClient.AddIPFilter(ctx, humioHttpClient, hi)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create IPFilter")
			}
			r.Log.Info("created IPFilter")
			err = r.setCondition(ctx, hi,
				humiov1alpha1.IPFilterConditionTypeReady,
				metav1.ConditionTrue,
				humiov1alpha1.IPFilterReasonCreated,
				"IPFilter created successfully",
				ipFilterDetails.Id)
			if err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "could not update IPFilter Status")
			}
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if IPFilter exists")
	}
	return reconcile.Result{}, nil
}

// updateIPFilterIfNeeded checks if IP filter differs and updates if needed
func (r *HumioIPFilterReconciler) updateIPFilterIfNeeded(ctx context.Context, humioHttpClient *humioapi.Client, hi *humiov1alpha1.HumioIPFilter) error {
	curIPfilter, err := r.HumioClient.GetIPFilter(ctx, humioHttpClient, hi)
	if err != nil {
		return r.logErrorAndReturn(err, "could not get IPFilter for comparison")
	}

	// check diffs and update
	if asExpected, diffKeysAndValues := ipFilterAlreadyAsExpected(hi, curIPfilter); !asExpected {
		r.Log.Info("information differs, triggering update", "diff", diffKeysAndValues)
		err = r.HumioClient.UpdateIPFilter(ctx, humioHttpClient, hi)
		if err != nil {
			return r.logErrorAndReturn(err, "could not update IPFilter")
		}
	}
	return nil
}

// updateIPFilterFinalStatus updates the final status of the IP filter
func (r *HumioIPFilterReconciler) updateIPFilterFinalStatus(ctx context.Context, humioHttpClient *humioapi.Client, hi *humiov1alpha1.HumioIPFilter) {
	ipFilter, err := r.HumioClient.GetIPFilter(ctx, humioHttpClient, hi)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		_ = r.setCondition(ctx, hi,
			humiov1alpha1.IPFilterConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.IPFilterReasonNotFound,
			"IPFilter not found",
			hi.Status.ID)
	} else if err != nil {
		_ = r.setCondition(ctx, hi,
			humiov1alpha1.IPFilterConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.IPFilterReasonConfigError,
			fmt.Sprintf("Error getting IPFilter: %v", err),
			hi.Status.ID)
	} else {
		_ = r.setCondition(ctx, hi,
			humiov1alpha1.IPFilterConditionTypeReady,
			metav1.ConditionTrue,
			humiov1alpha1.IPFilterReasonReady,
			"IPFilter is ready",
			ipFilter.Id)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioIPFilterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioIPFilter{}).
		Named("humioipfilter").
		Complete(r)
}

func (r *HumioIPFilterReconciler) finalize(ctx context.Context, client *humioapi.Client, hi *humiov1alpha1.HumioIPFilter) error {
	if hi.Status.ID == "" {
		// ipFIlter ID not set, unexpected but we should not err
		return nil
	}

	// Check if data deletion is allowed
	if !hi.Spec.AllowDataDeletion {
		return fmt.Errorf("IP filter may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
	}

	// Audit log before deletion
	r.Log.Info("Proceeding with IP filter deletion",
		"allowDataDeletion", hi.Spec.AllowDataDeletion,
		"ipFilterName", hi.Spec.Name,
		"namespace", hi.Namespace,
		"deletionTimestamp", hi.GetDeletionTimestamp(),
	)

	err := r.HumioClient.DeleteIPFilter(ctx, client, hi)
	if err != nil {
		return r.logErrorAndReturn(err, "error in finalize function call")
	}

	r.Log.Info("Successfully deleted IP filter", "ipFilterName", hi.Spec.Name)
	return nil
}

func (r *HumioIPFilterReconciler) addFinalizer(ctx context.Context, hi *humiov1alpha1.HumioIPFilter) error {
	r.Log.Info("Adding Finalizer for the HumioIPFilter")
	hi.SetFinalizers(append(hi.GetFinalizers(), HumioFinalizer))

	err := r.Update(ctx, hi)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioIPFilter with finalizer")
	}
	return nil
}

// setCondition sets a condition on the HumioIPFilter resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioIPFilterReconciler) setCondition(ctx context.Context,
	hi *humiov1alpha1.HumioIPFilter,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
	id string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioIPFilter{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hi), latest); err != nil {
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

		// BACKWARD COMPATIBILITY: Update State field based on condition
		latest.Status.State = r.stateFromCondition(status, reason)
		latest.Status.ID = id

		// Track the synced name when IP filter is ready
		if conditionType == humiov1alpha1.IPFilterConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.Name
		}

		return r.Status().Update(ctx, latest)
	})
}

// stateFromCondition converts condition status and reason to legacy State field value
func (r *HumioIPFilterReconciler) stateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioIPFilterStateExists
	}
	switch reason {
	case humiov1alpha1.IPFilterReasonNotFound:
		return humiov1alpha1.HumioIPFilterStateNotFound
	case humiov1alpha1.IPFilterReasonConfigError:
		return humiov1alpha1.HumioIPFilterStateConfigError
	default:
		return humiov1alpha1.HumioIPFilterStateUnknown
	}
}

func (r *HumioIPFilterReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// ipFilterAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL.
func ipFilterAlreadyAsExpected(fromK8sCR *humiov1alpha1.HumioIPFilter, fromGraphQL *humiographql.IPFilterDetails) (bool, map[string]string) {
	keyValues := map[string]string{}
	// we only care about ipFilter field
	fromGql := fromGraphQL.GetIpFilter()
	fromK8s := helpers.FirewallRulesToString(fromK8sCR.Spec.IPFilter, "\n")
	if diff := cmp.Diff(fromGql, fromK8s); diff != "" {
		keyValues["ipFilter"] = diff
	}
	return len(keyValues) == 0, keyValues
}

// detectAndHandleRename checks if the IP filter name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
func (r *HumioIPFilterReconciler) detectAndHandleRename(ctx context.Context,
	httpClient *humioapi.Client, hif *humiov1alpha1.HumioIPFilter) (bool, reconcile.Result, error) {

	config := DeleteRecreateRenameConfig{
		ResourceType: "IP filter",
		GetSpecName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioIPFilter).Spec.Name
		},
		SetSpecName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioIPFilter).Spec.Name = name
		},
		GetLastSyncedName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioIPFilter).Status.LastSyncedName
		},
		SetLastSyncedName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioIPFilter).Status.LastSyncedName = name
		},
		DeleteResource: func(ctx context.Context, apiClient *humioapi.Client, obj client.Object) error {
			return r.HumioClient.DeleteIPFilter(ctx, apiClient, obj.(*humiov1alpha1.HumioIPFilter))
		},
		SetErrorState: func(ctx context.Context, obj client.Object) error {
			return r.setCondition(ctx, obj.(*humiov1alpha1.HumioIPFilter),
				humiov1alpha1.IPFilterConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.IPFilterReasonConfigError,
				"Configuration error during rename", "")
		},
		Client:        r.Client,
		StatusUpdater: r.Status(),
	}

	return HandleDeleteRecreateRename(ctx, httpClient, hif, config, r.Log)
}
