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

// HumioScheduledSearchReconciler reconciles a HumioScheduledSearch object
type HumioScheduledSearchReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches/finalizers,verbs=update

func (r *HumioScheduledSearchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioScheduledSearch")

	hss := &humiov1alpha1.HumioScheduledSearch{}
	err := r.Get(ctx, req.NamespacedName, hss)
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

	r.Log = r.Log.WithValues("Request.UID", hss.UID)

	cluster, err := helpers.NewCluster(ctx, r, hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName, hss.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateConfigError, hss)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set scheduled search state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(ctx context.Context, hss *humiov1alpha1.HumioScheduledSearch) {
		_, err := r.HumioClient.GetScheduledSearch(ctx, humioHttpClient, hss)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateNotFound, hss)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateUnknown, hss)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateExists, hss)
	}(ctx, hss)

	return r.reconcileHumioScheduledSearch(ctx, humioHttpClient, hss)
}

func (r *HumioScheduledSearchReconciler) reconcileHumioScheduledSearch(ctx context.Context, client *humioapi.Client, hss *humiov1alpha1.HumioScheduledSearch) (reconcile.Result, error) {
	r.Log.Info("Checking if scheduled search is marked to be deleted")
	isMarkedForDeletion := hss.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("ScheduledSearch marked to be deleted")
		if helpers.ContainsElement(hss.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetScheduledSearch(ctx, client, hss)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hss.SetFinalizers(helpers.RemoveElement(hss.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hss)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting scheduled search")
			if err := r.HumioClient.DeleteScheduledSearch(ctx, client, hss); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete scheduled search returned error")
			}
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if scheduled search requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(hss.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to scheduled search")
		hss.SetFinalizers(append(hss.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, hss)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if scheduled search needs to be created")
	curScheduledSearch, err := r.HumioClient.GetScheduledSearch(ctx, client, hss)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("ScheduledSearch doesn't exist. Now adding scheduled search")
			addErr := r.HumioClient.AddScheduledSearch(ctx, client, hss)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create scheduled search")
			}
			r.Log.Info("Created scheduled search", "ScheduledSearch", hss.Spec.Name)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if scheduled search")
	}

	r.Log.Info("Checking if scheduled search needs to be updated")
	if err := r.HumioClient.ValidateActionsForScheduledSearch(ctx, client, hss); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not get action id mapping")
	}

	if asExpected, diffKeysAndValues := scheduledSearchAlreadyAsExpected(hss, curScheduledSearch); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		updateErr := r.HumioClient.UpdateScheduledSearch(ctx, client, hss)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update scheduled search")
		}
		r.Log.Info("Updated scheduled search",
			"ScheduledSearch", hss.Spec.Name,
		)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioScheduledSearchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioScheduledSearch{}).
		Named("humioscheduledsearch").
		Complete(r)
}

func (r *HumioScheduledSearchReconciler) setState(ctx context.Context, state string, hss *humiov1alpha1.HumioScheduledSearch) error {
	if hss.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting scheduled search to %s", state))
	hss.Status.State = state
	return r.Status().Update(ctx, hss)
}

func (r *HumioScheduledSearchReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// scheduledSearchAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func scheduledSearchAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioScheduledSearch, fromGraphQL *humiographql.ScheduledSearchDetails) (bool, map[string]string) {
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
	if diff := cmp.Diff(fromGraphQL.GetStart(), fromKubernetesCustomResource.Spec.QueryStart); diff != "" {
		keyValues["throttleField"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetEnd(), fromKubernetesCustomResource.Spec.QueryEnd); diff != "" {
		keyValues["throttleTimeSeconds"] = diff
	}
	actionsFromGraphQL := humioapi.GetActionNames(fromGraphQL.GetActionsV2())
	sort.Strings(actionsFromGraphQL)
	sort.Strings(fromKubernetesCustomResource.Spec.Actions)
	if diff := cmp.Diff(actionsFromGraphQL, fromKubernetesCustomResource.Spec.Actions); diff != "" {
		keyValues["actions"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetTimeZone(), fromKubernetesCustomResource.Spec.TimeZone); diff != "" {
		keyValues["queryTimestampType"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryString(), fromKubernetesCustomResource.Spec.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetSchedule(), fromKubernetesCustomResource.Spec.Schedule); diff != "" {
		keyValues["triggerMode"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetBackfillLimit(), fromKubernetesCustomResource.Spec.BackfillLimit); diff != "" {
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
