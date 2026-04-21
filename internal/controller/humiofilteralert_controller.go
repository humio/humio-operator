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
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioFilterAlertReconciler reconciles a HumioFilterAlert object
type HumioFilterAlertReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiofilteralerts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiofilteralerts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiofilteralerts/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioFilterAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioFilterAlert")

	hfa := &humiov1alpha1.HumioFilterAlert{}
	err := r.Get(ctx, req.NamespacedName, hfa)
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

	r.Log = r.Log.WithValues("Request.UID", hfa.UID)

	cluster, err := helpers.NewCluster(ctx, r, hfa.Spec.ManagedClusterName, hfa.Spec.ExternalClusterName, hfa.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, hfa, humiov1alpha1.FilterAlertConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.FilterAlertReasonConfigError, fmt.Sprintf("unable to obtain humio client config: %s", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set filter alert condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	// This ensures we handle the delete-recreate before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, hfa)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle filter alert rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with creation
		return result, nil
	}

	defer func(ctx context.Context, hfa *humiov1alpha1.HumioFilterAlert) {
		_, err := r.HumioClient.GetFilterAlert(ctx, humioHttpClient, hfa)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setCondition(ctx, hfa, humiov1alpha1.FilterAlertConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.FilterAlertReasonNotFound, "Filter alert not found")
			return
		}
		if err != nil {
			_ = r.setCondition(ctx, hfa, humiov1alpha1.FilterAlertConditionTypeReady, metav1.ConditionUnknown, humiov1alpha1.FilterAlertReasonConfigError, fmt.Sprintf("unable to get filter alert: %s", err))
			return
		}
		_ = r.setCondition(ctx, hfa, humiov1alpha1.FilterAlertConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.FilterAlertReasonReady, "Filter alert is ready")
	}(ctx, hfa)

	return r.reconcileHumioFilterAlert(ctx, humioHttpClient, hfa)
}

func (r *HumioFilterAlertReconciler) reconcileHumioFilterAlert(ctx context.Context, client *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) (reconcile.Result, error) {
	r.Log.Info("Checking if filter alert is marked to be deleted")
	isMarkedForDeletion := hfa.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("FilterAlert marked to be deleted")
		if helpers.ContainsElement(hfa.GetFinalizers(), HumioFinalizer) {
			_, err := r.HumioClient.GetFilterAlert(ctx, client, hfa)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hfa.SetFinalizers(helpers.RemoveElement(hfa.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hfa)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting filter alert")

			// Check if data deletion is allowed
			if !hfa.Spec.AllowDataDeletion {
				err := fmt.Errorf("filter alert may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete filter alert blocked")
			}

			// Audit log before deletion
			r.Log.Info("Proceeding with filter alert deletion",
				"allowDataDeletion", hfa.Spec.AllowDataDeletion,
				"alertName", hfa.Spec.Name,
				"viewName", hfa.Spec.ViewName,
				"namespace", hfa.Namespace,
				"deletionTimestamp", hfa.GetDeletionTimestamp(),
			)

			if err := r.HumioClient.DeleteFilterAlert(ctx, client, hfa); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete filter alert returned error")
			}

			r.Log.Info("Successfully deleted filter alert", "alertName", hfa.Spec.Name)
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if filter alert requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(hfa.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to filter alert")
		hfa.SetFinalizers(append(hfa.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hfa)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	if hfa.Spec.ThrottleTimeSeconds > 0 && hfa.Spec.ThrottleTimeSeconds < 60 {
		r.Log.Error(fmt.Errorf("ThrottleTimeSeconds must be at least 60 seconds"), "error managing filter alert")
		err := r.setCondition(ctx, hfa, humiov1alpha1.FilterAlertConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.FilterAlertReasonConfigError, "ThrottleTimeSeconds must be at least 60 seconds")
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set filter alert condition")
		}
		return reconcile.Result{}, err
	}

	r.Log.Info("Checking if filter alert needs to be created")
	curFilterAlert, err := r.HumioClient.GetFilterAlert(ctx, client, hfa)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("FilterAlert doesn't exist. Now adding filter alert")
			addErr := r.HumioClient.AddFilterAlert(ctx, client, hfa)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create filter alert")
			}
			r.Log.Info("Created filter alert",
				"FilterAlert", hfa.Spec.Name,
			)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if alert exists")
	}

	r.Log.Info("Checking if filter alert needs to be updated")
	if err := r.HumioClient.ValidateActionsForFilterAlert(ctx, client, hfa); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not get action id mapping")
	}

	if asExpected, diffKeysAndValues := filterAlertAlreadyAsExpected(hfa, curFilterAlert); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		updateErr := r.HumioClient.UpdateFilterAlert(ctx, client, hfa)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update filter alert")
		}
		r.Log.Info("Updated filter alert",
			"FilterAlert", hfa.Spec.Name,
		)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioFilterAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioFilterAlert{}).
		Named("humiofilteralert").
		Complete(r)
}

// setCondition sets a condition on the HumioFilterAlert resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioFilterAlertReconciler) setCondition(ctx context.Context,
	hfa *humiov1alpha1.HumioFilterAlert,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioFilterAlert{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hfa), latest); err != nil {
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
		latest.Status.State = filterAlertStateFromCondition(status, reason)

		// Track the synced name when filter alert is ready
		if conditionType == humiov1alpha1.FilterAlertConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.Name
		}

		return r.Status().Update(ctx, latest)
	})
}

// filterAlertStateFromCondition converts a condition status and reason to a legacy state string for backward compatibility
func filterAlertStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioFilterAlertStateExists
	}
	switch reason {
	case humiov1alpha1.FilterAlertReasonNotFound:
		return humiov1alpha1.HumioFilterAlertStateNotFound
	case humiov1alpha1.FilterAlertReasonConfigError:
		return humiov1alpha1.HumioFilterAlertStateConfigError
	default:
		return humiov1alpha1.HumioFilterAlertStateUnknown
	}
}

func (r *HumioFilterAlertReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// filterAlertAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func filterAlertAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioFilterAlert, fromGraphQL *humiographql.FilterAlertDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetDescription(), &fromKubernetesCustomResource.Spec.Description); diff != "" {
		keyValues["description"] = diff
	}
	labelsFromGraphQL := fromGraphQL.GetLabels()
	sort.Strings(labelsFromGraphQL)
	sort.Strings(fromKubernetesCustomResource.Spec.Labels)
	if diff := cmp.Diff(labelsFromGraphQL, fromKubernetesCustomResource.Spec.Labels); diff != "" {
		keyValues["labels"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetThrottleField(), fromKubernetesCustomResource.Spec.ThrottleField); diff != "" {
		keyValues["throttleField"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetThrottleTimeSeconds(), helpers.Int64Ptr(int64(fromKubernetesCustomResource.Spec.ThrottleTimeSeconds))); diff != "" {
		keyValues["throttleTimeSeconds"] = diff
	}
	actionsFromGraphQL := humioapi.GetActionNames(fromGraphQL.GetActions())
	sort.Strings(actionsFromGraphQL)
	sort.Strings(fromKubernetesCustomResource.Spec.Actions)
	if diff := cmp.Diff(actionsFromGraphQL, fromKubernetesCustomResource.Spec.Actions); diff != "" {
		keyValues["actions"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryString(), fromKubernetesCustomResource.Spec.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetEnabled(), fromKubernetesCustomResource.Spec.Enabled); diff != "" {
		keyValues["enabled"] = diff
	}
	if !humioapi.QueryOwnershipIsOrganizationOwnership(fromGraphQL.GetQueryOwnership()) {
		keyValues["queryOwnership"] = fmt.Sprintf("%+v", fromGraphQL.GetQueryOwnership())
	}

	return len(keyValues) == 0, keyValues
}

// detectAndHandleRename checks if the filter alert name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
func (r *HumioFilterAlertReconciler) detectAndHandleRename(ctx context.Context,
	httpClient *humioapi.Client, hfa *humiov1alpha1.HumioFilterAlert) (bool, reconcile.Result, error) {

	config := DeleteRecreateRenameConfig{
		ResourceType: "filter alert",
		GetSpecName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioFilterAlert).Spec.Name
		},
		SetSpecName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioFilterAlert).Spec.Name = name
		},
		GetLastSyncedName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioFilterAlert).Status.LastSyncedName
		},
		SetLastSyncedName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioFilterAlert).Status.LastSyncedName = name
		},
		DeleteResource: func(ctx context.Context, apiClient *humioapi.Client, obj client.Object) error {
			return r.HumioClient.DeleteFilterAlert(ctx, apiClient, obj.(*humiov1alpha1.HumioFilterAlert))
		},
		SetErrorState: func(ctx context.Context, obj client.Object) error {
			return r.setCondition(ctx, obj.(*humiov1alpha1.HumioFilterAlert),
				humiov1alpha1.FilterAlertConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.FilterAlertReasonConfigError,
				"Configuration error during rename")
		},
		Client:        r.Client,
		StatusUpdater: r.Status(),
	}

	return HandleDeleteRecreateRename(ctx, httpClient, hfa, config, r.Log)
}
