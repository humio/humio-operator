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

// HumioAlertReconciler reconciles a HumioAlert object
type HumioAlertReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioalerts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioalerts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioalerts/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioAlert")

	ha := &humiov1alpha1.HumioAlert{}
	err := r.Get(ctx, req.NamespacedName, ha)
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

	r.Log = r.Log.WithValues("Request.UID", ha.UID)

	cluster, err := helpers.NewCluster(ctx, r, ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName, ha.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, ha, humiov1alpha1.AlertConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.AlertReasonConfigError, fmt.Sprintf("unable to obtain humio client config: %s", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set alert condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	// This ensures we handle the delete-recreate before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, ha)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle alert rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with creation
		return result, nil
	}

	defer func(ctx context.Context, ha *humiov1alpha1.HumioAlert) {
		_, err := r.HumioClient.GetAlert(ctx, humioHttpClient, ha)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setCondition(ctx, ha, humiov1alpha1.AlertConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.AlertReasonNotFound, "Alert not found")
			return
		}
		if err != nil {
			_ = r.setCondition(ctx, ha, humiov1alpha1.AlertConditionTypeReady, metav1.ConditionUnknown, humiov1alpha1.AlertReasonConfigError, fmt.Sprintf("unable to get alert: %s", err))
			return
		}
		_ = r.setCondition(ctx, ha, humiov1alpha1.AlertConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.AlertReasonReady, "Alert is ready")
	}(ctx, ha)

	return r.reconcileHumioAlert(ctx, humioHttpClient, ha)
}

func (r *HumioAlertReconciler) reconcileHumioAlert(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAlert) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if alert is marked to be deleted")
	if ha.GetDeletionTimestamp() != nil {
		r.Log.Info("Alert marked to be deleted")
		if helpers.ContainsElement(ha.GetFinalizers(), HumioFinalizer) {
			if ShouldSkipFinalizer(r.CommonConfig, ha) {
				r.Log.Info("Finalizer skip triggered, removing finalizer without cleanup")
				ha.SetFinalizers(helpers.RemoveElement(ha.GetFinalizers(), HumioFinalizer))
				if err := r.Update(ctx, ha); err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{Requeue: true}, nil
			}
			_, err := r.HumioClient.GetAlert(ctx, client, ha)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				ha.SetFinalizers(helpers.RemoveElement(ha.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, ha)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting alert")

			// Check if data deletion is allowed
			if !ha.Spec.AllowDataDeletion {
				err := fmt.Errorf("alert may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete alert blocked")
			}

			// Audit log before deletion
			r.Log.Info("Proceeding with alert deletion",
				"allowDataDeletion", ha.Spec.AllowDataDeletion,
				"alertName", ha.Spec.Name,
				"viewName", ha.Spec.ViewName,
				"namespace", ha.Namespace,
				"deletionTimestamp", ha.GetDeletionTimestamp(),
			)

			if err := r.HumioClient.DeleteAlert(ctx, client, ha); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete alert returned error")
			}

			r.Log.Info("Successfully deleted alert", "alertName", ha.Spec.Name)
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if alert requires finalizer")
	// Add finalizer for this CR
	if !ShouldSkipFinalizer(r.CommonConfig, ha) && !helpers.ContainsElement(ha.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to alert")
		ha.SetFinalizers(append(ha.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, ha)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if alert needs to be created")
	// Add Alert
	curAlert, err := r.HumioClient.GetAlert(ctx, client, ha)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("Alert doesn't exist. Now adding alert")
			addErr := r.HumioClient.AddAlert(ctx, client, ha)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create alert")
			}
			r.Log.Info("Created alert",
				"Alert", ha.Spec.Name,
			)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if alert exists")
	}

	r.Log.Info("Checking if alert needs to be updated")

	if asExpected, diffKeysAndValues := alertAlreadyAsExpected(ha, curAlert); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		err = r.HumioClient.UpdateAlert(ctx, client, ha)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update alert")
		}
		r.Log.Info("Updated Alert",
			"Alert", ha.Spec.Name,
		)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioAlert{}).
		Named("humioalert").
		Complete(r)
}

// setCondition sets a condition on the HumioAlert resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioAlertReconciler) setCondition(ctx context.Context,
	ha *humiov1alpha1.HumioAlert,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioAlert{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(ha), latest); err != nil {
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
		latest.Status.State = alertStateFromCondition(status, reason)

		// Track the synced name when alert is ready
		if conditionType == humiov1alpha1.AlertConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.Name
		}

		return r.Status().Update(ctx, latest)
	})
}

// alertStateFromCondition converts a condition status and reason to a legacy state string for backward compatibility
func alertStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioAlertStateExists
	}
	switch reason {
	case humiov1alpha1.AlertReasonNotFound:
		return humiov1alpha1.HumioAlertStateNotFound
	case humiov1alpha1.AlertReasonConfigError:
		return humiov1alpha1.HumioAlertStateConfigError
	default:
		return humiov1alpha1.HumioAlertStateUnknown
	}
}

func (r *HumioAlertReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// alertAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func alertAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioAlert, fromGraphQL *humiographql.AlertDetails) (bool, map[string]string) {
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
	if diff := cmp.Diff(fromGraphQL.GetThrottleTimeMillis(), int64(fromKubernetesCustomResource.Spec.ThrottleTimeMillis)); diff != "" {
		keyValues["throttleTimeMillis"] = diff
	}
	actionsFromGraphQL := humioapi.GetActionNames(fromGraphQL.GetActionsV2())
	sort.Strings(actionsFromGraphQL)
	sort.Strings(fromKubernetesCustomResource.Spec.Actions)
	if diff := cmp.Diff(actionsFromGraphQL, fromKubernetesCustomResource.Spec.Actions); diff != "" {
		keyValues["actions"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryString(), fromKubernetesCustomResource.Spec.Query.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryStart(), fromKubernetesCustomResource.Spec.Query.Start); diff != "" {
		keyValues["start"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetEnabled(), !fromKubernetesCustomResource.Spec.Silenced); diff != "" {
		keyValues["enabled"] = diff
	}
	if !humioapi.QueryOwnershipIsOrganizationOwnership(fromGraphQL.GetQueryOwnership()) {
		keyValues["queryOwnership"] = fmt.Sprintf("%+v", fromGraphQL.GetQueryOwnership())
	}

	return len(keyValues) == 0, keyValues
}

// detectAndHandleRename checks if the alert name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
func (r *HumioAlertReconciler) detectAndHandleRename(ctx context.Context,
	httpClient *humioapi.Client, ha *humiov1alpha1.HumioAlert) (bool, reconcile.Result, error) {

	config := DeleteRecreateRenameConfig{
		ResourceType: "alert",
		GetSpecName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioAlert).Spec.Name
		},
		SetSpecName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioAlert).Spec.Name = name
		},
		GetLastSyncedName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioAlert).Status.LastSyncedName
		},
		SetLastSyncedName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioAlert).Status.LastSyncedName = name
		},
		DeleteResource: func(ctx context.Context, apiClient *humioapi.Client, obj client.Object) error {
			return r.HumioClient.DeleteAlert(ctx, apiClient, obj.(*humiov1alpha1.HumioAlert))
		},
		SetErrorState: func(ctx context.Context, obj client.Object) error {
			return r.setCondition(ctx, obj.(*humiov1alpha1.HumioAlert),
				humiov1alpha1.AlertConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.AlertReasonConfigError,
				"Configuration error during rename")
		},
		Client:        r.Client,
		StatusUpdater: r.Status(),
	}

	return HandleDeleteRecreateRename(ctx, httpClient, ha, config, r.Log)
}
