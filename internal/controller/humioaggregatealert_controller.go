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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioAggregateAlertReconciler reconciles a HumioAggregateAlert object
type HumioAggregateAlertReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioaggregatealerts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioaggregatealerts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioaggregatealerts/finalizers,verbs=update

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
		setStateErr := r.setState(ctx, humiov1alpha1.HumioAggregateAlertStateConfigError, haa)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set scheduled search state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(ctx context.Context, haa *humiov1alpha1.HumioAggregateAlert) {
		curAggregateAlert, err := r.HumioClient.GetAggregateAlert(ctx, humioHttpClient, req, haa)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioAggregateAlertStateNotFound, haa)
			return
		}
		if err != nil || curAggregateAlert == nil {
			_ = r.setState(ctx, humiov1alpha1.HumioAggregateAlertStateConfigError, haa)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioAggregateAlertStateExists, haa)
	}(ctx, haa)

	return r.reconcileHumioAggregateAlert(ctx, humioHttpClient, haa, req)
}

func (r *HumioAggregateAlertReconciler) reconcileHumioAggregateAlert(ctx context.Context, client *humioapi.Client, haa *humiov1alpha1.HumioAggregateAlert, req ctrl.Request) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if alert is marked to be deleted")
	isMarkedForDeletion := haa.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("AggregateAlert marked to be deleted")
		if helpers.ContainsElement(haa.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetAggregateAlert(ctx, client, req, haa)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				haa.SetFinalizers(helpers.RemoveElement(haa.GetFinalizers(), humioFinalizer))
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
			if err := r.HumioClient.DeleteAggregateAlert(ctx, client, req, haa); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete aggregate alert returned error")
			}
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if aggregate alert requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(haa.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to alert")
		haa.SetFinalizers(append(haa.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, haa)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	if haa.Spec.ThrottleTimeSeconds > 0 && haa.Spec.ThrottleTimeSeconds < 60 {
		r.Log.Error(fmt.Errorf("ThrottleTimeSeconds must be greater than or equal to 60"), "ThrottleTimeSeconds must be greater than or equal to 60")
		err := r.setState(ctx, humiov1alpha1.HumioAggregateAlertStateConfigError, haa)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set alert state")
		}
		return reconcile.Result{}, err
	}

	r.Log.Info("Checking if aggregate alert needs to be created")
	// Add Alert
	curAggregateAlert, err := r.HumioClient.GetAggregateAlert(ctx, client, req, haa)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("AggregateAlert doesn't exist. Now adding aggregate alert")
			addErr := r.HumioClient.AddAggregateAlert(ctx, client, req, haa)
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
	if err := r.HumioClient.ValidateActionsForAggregateAlert(ctx, client, req, haa); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not validate actions for aggregate alert")
	}

	if asExpected, diffKeysAndValues := aggregateAlertAlreadyAsExpected(haa, curAggregateAlert); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		updateErr := r.HumioClient.UpdateAggregateAlert(ctx, client, req, haa)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update aggregate alert")
		}
		r.Log.Info("Updated Aggregate Alert",
			"AggregateAlert", haa.Spec.Name,
		)
	}

	r.Log.Info("done reconciling, will requeue in 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil

}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioAggregateAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioAggregateAlert{}).
		Named("humioaggregatealert").
		Complete(r)
}

func (r *HumioAggregateAlertReconciler) setState(ctx context.Context, state string, haa *humiov1alpha1.HumioAggregateAlert) error {
	if haa.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting alert state to %s", state))
	haa.Status.State = state
	return r.Status().Update(ctx, haa)
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
