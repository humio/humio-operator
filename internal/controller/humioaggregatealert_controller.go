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

// HumioAggregateAlertReconciler reconciles a HumioAggregateAlert object
type HumioAggregateAlertReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioaggregatealerts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioaggregatealerts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioaggregatealerts/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioAggregateAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioAggregateAlert")

	haa := &humiov1alpha1.HumioAggregateAlert{}
	err := r.Get(ctx, req.NamespacedName, haa)
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

	r.Log = r.Log.WithValues("Request.UID", haa.UID)

	cluster, err := helpers.NewCluster(ctx, r, haa.Spec.ManagedClusterName, haa.Spec.ExternalClusterName, haa.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, haa, humiov1alpha1.AggregateAlertConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.AggregateAlertReasonConfigError, fmt.Sprintf("unable to obtain humio client config: %s", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set aggregate alert condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	// This ensures we handle the delete-recreate before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, haa)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle aggregate alert rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with creation
		return result, nil
	}

	defer func(ctx context.Context, haa *humiov1alpha1.HumioAggregateAlert) {
		curAggregateAlert, err := r.HumioClient.GetAggregateAlert(ctx, humioHttpClient, haa)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setCondition(ctx, haa, humiov1alpha1.AggregateAlertConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.AggregateAlertReasonNotFound, "Aggregate alert not found")
			return
		}
		if err != nil || curAggregateAlert == nil {
			_ = r.setCondition(ctx, haa, humiov1alpha1.AggregateAlertConditionTypeReady, metav1.ConditionUnknown, humiov1alpha1.AggregateAlertReasonConfigError, fmt.Sprintf("unable to get aggregate alert: %s", err))
			return
		}
		_ = r.setCondition(ctx, haa, humiov1alpha1.AggregateAlertConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.AggregateAlertReasonReady, "Aggregate alert is ready")
	}(ctx, haa)

	return r.reconcileHumioAggregateAlert(ctx, humioHttpClient, haa)
}

func (r *HumioAggregateAlertReconciler) reconcileHumioAggregateAlert(ctx context.Context, client *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if alert is marked to be deleted")
	isMarkedForDeletion := haa.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("AggregateAlert marked to be deleted")
		if helpers.ContainsElement(haa.GetFinalizers(), HumioFinalizer) {
			_, err := r.HumioClient.GetAggregateAlert(ctx, client, haa)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				haa.SetFinalizers(helpers.RemoveElement(haa.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, haa)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting aggregate alert")

			// Check if data deletion is allowed
			if !haa.Spec.AllowDataDeletion {
				err := fmt.Errorf("aggregate alert may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete aggregate alert blocked")
			}

			// Audit log before deletion
			r.Log.Info("Proceeding with aggregate alert deletion",
				"allowDataDeletion", haa.Spec.AllowDataDeletion,
				"alertName", haa.Spec.Name,
				"viewName", haa.Spec.ViewName,
				"namespace", haa.Namespace,
				"deletionTimestamp", haa.GetDeletionTimestamp(),
			)

			if err := r.HumioClient.DeleteAggregateAlert(ctx, client, haa); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete aggregate alert returned error")
			}

			r.Log.Info("Successfully deleted aggregate alert", "alertName", haa.Spec.Name)
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if aggregate alert requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(haa.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to alert")
		haa.SetFinalizers(append(haa.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, haa)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	if haa.Spec.ThrottleTimeSeconds > 0 && haa.Spec.ThrottleTimeSeconds < 60 {
		r.Log.Error(fmt.Errorf("ThrottleTimeSeconds must be greater than or equal to 60"), "ThrottleTimeSeconds must be greater than or equal to 60")
		err := r.setCondition(ctx, haa, humiov1alpha1.AggregateAlertConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.AggregateAlertReasonConfigError, "ThrottleTimeSeconds must be greater than or equal to 60")
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set aggregate alert condition")
		}
		return reconcile.Result{}, err
	}

	r.Log.Info("Checking if aggregate alert needs to be created")
	// Add Alert
	curAggregateAlert, err := r.HumioClient.GetAggregateAlert(ctx, client, haa)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("AggregateAlert doesn't exist. Now adding aggregate alert")
			addErr := r.HumioClient.AddAggregateAlert(ctx, client, haa)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create aggregate alert")
			}
			r.Log.Info("Created aggregate alert",
				"AggregateAlert", haa.Spec.Name,
			)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if aggregate alert exists")
	}

	r.Log.Info("Checking if aggregate alert needs to be updated")
	// Update
	if err := r.HumioClient.ValidateActionsForAggregateAlert(ctx, client, haa); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not validate actions for aggregate alert")
	}

	if asExpected, diffKeysAndValues := aggregateAlertAlreadyAsExpected(haa, curAggregateAlert); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		updateErr := r.HumioClient.UpdateAggregateAlert(ctx, client, haa)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update aggregate alert")
		}
		r.Log.Info("Updated Aggregate Alert",
			"AggregateAlert", haa.Spec.Name,
		)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioAggregateAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioAggregateAlert{}).
		Named("humioaggregatealert").
		Complete(r)
}

// setCondition sets a condition on the HumioAggregateAlert resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioAggregateAlertReconciler) setCondition(ctx context.Context,
	haa *humiov1alpha1.HumioAggregateAlert,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioAggregateAlert{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(haa), latest); err != nil {
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
		latest.Status.State = aggregateAlertStateFromCondition(status, reason)

		// Track the synced name when aggregate alert is ready
		if conditionType == humiov1alpha1.AggregateAlertConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.Name
		}

		return r.Status().Update(ctx, latest)
	})
}

// aggregateAlertStateFromCondition converts a condition status and reason to a legacy state string for backward compatibility
func aggregateAlertStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioAggregateAlertStateExists
	}
	switch reason {
	case humiov1alpha1.AggregateAlertReasonNotFound:
		return humiov1alpha1.HumioAggregateAlertStateNotFound
	case humiov1alpha1.AggregateAlertReasonConfigError:
		return humiov1alpha1.HumioAggregateAlertStateConfigError
	default:
		return humiov1alpha1.HumioAggregateAlertStateUnknown
	}
}

func (r *HumioAggregateAlertReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// aggregateAlertAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func aggregateAlertAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioAggregateAlert, fromGraphQL *humiographql.AggregateAlertDetails) (bool, map[string]string) {
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
	if diff := cmp.Diff(fromGraphQL.GetThrottleTimeSeconds(), int64(fromKubernetesCustomResource.Spec.ThrottleTimeSeconds)); diff != "" {
		keyValues["throttleTimeSeconds"] = diff
	}
	actionsFromGraphQL := humioapi.GetActionNames(fromGraphQL.GetActions())
	sort.Strings(actionsFromGraphQL)
	sort.Strings(fromKubernetesCustomResource.Spec.Actions)
	if diff := cmp.Diff(actionsFromGraphQL, fromKubernetesCustomResource.Spec.Actions); diff != "" {
		keyValues["actions"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryTimestampType(), humiographql.QueryTimestampType(fromKubernetesCustomResource.Spec.QueryTimestampType)); diff != "" {
		keyValues["queryTimestampType"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryString(), fromKubernetesCustomResource.Spec.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetTriggerMode(), humiographql.TriggerMode(fromKubernetesCustomResource.Spec.TriggerMode)); diff != "" {
		keyValues["triggerMode"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetSearchIntervalSeconds(), int64(fromKubernetesCustomResource.Spec.SearchIntervalSeconds)); diff != "" {
		keyValues["searchIntervalSeconds"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetEnabled(), fromKubernetesCustomResource.Spec.Enabled); diff != "" {
		keyValues["enabled"] = diff
	}
	if !humioapi.QueryOwnershipIsOrganizationOwnership(fromGraphQL.GetQueryOwnership()) {
		keyValues["queryOwnership"] = fmt.Sprintf("%+v", fromGraphQL.GetQueryOwnership())
	}

	return len(keyValues) == 0, keyValues
}

// detectAndHandleRename checks if the aggregate alert name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
func (r *HumioAggregateAlertReconciler) detectAndHandleRename(ctx context.Context,
	httpClient *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert) (bool, reconcile.Result, error) {

	config := DeleteRecreateRenameConfig{
		ResourceType: "aggregate alert",
		GetSpecName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioAggregateAlert).Spec.Name
		},
		SetSpecName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioAggregateAlert).Spec.Name = name
		},
		GetLastSyncedName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioAggregateAlert).Status.LastSyncedName
		},
		SetLastSyncedName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioAggregateAlert).Status.LastSyncedName = name
		},
		DeleteResource: func(ctx context.Context, apiClient *humioapi.Client, obj client.Object) error {
			return r.HumioClient.DeleteAggregateAlert(ctx, apiClient, obj.(*humiov1alpha1.HumioAggregateAlert))
		},
		SetErrorState: func(ctx context.Context, obj client.Object) error {
			return r.setCondition(ctx, obj.(*humiov1alpha1.HumioAggregateAlert),
				humiov1alpha1.AggregateAlertConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.AggregateAlertReasonConfigError,
				"Configuration error during rename")
		},
		Client:        r.Client,
		StatusUpdater: r.Status(),
	}

	return HandleDeleteRecreateRename(ctx, httpClient, haa, config, r.Log)
}
