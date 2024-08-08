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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/humio/humio-operator/pkg/kubernetes"

	humioapi "github.com/humio/cli/api"

	"github.com/humio/humio-operator/pkg/helpers"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioScheduledSearchReconciler reconciles a HumioScheduledSearch object
type HumioScheduledSearchReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches/finalizers,verbs=update

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

	cluster, err := helpers.NewCluster(ctx, r, hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName, hss.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateConfigError, hss)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set scheduled search state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}

	defer func(ctx context.Context, humioClient humio.Client, hss *humiov1alpha1.HumioScheduledSearch) {
		curScheduledSearch, err := r.HumioClient.GetScheduledSearch(cluster.Config(), req, hss)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateNotFound, hss)
			return
		}
		if err != nil || curScheduledSearch == nil {
			_ = r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateConfigError, hss)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateExists, hss)
	}(ctx, r.HumioClient, hss)

	return r.reconcileHumioScheduledSearch(ctx, cluster.Config(), hss, req)
}

func (r *HumioScheduledSearchReconciler) reconcileHumioScheduledSearch(ctx context.Context, config *humioapi.Config, hss *humiov1alpha1.HumioScheduledSearch, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info("Checking if scheduled search is marked to be deleted")
	isMarkedForDeletion := hss.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("ScheduledSearch marked to be deleted")
		if helpers.ContainsElement(hss.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting scheduled search")
			if err := r.HumioClient.DeleteScheduledSearch(config, req, hss); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete scheduled search returned error")
			}

			r.Log.Info("ScheduledSearch Deleted. Removing finalizer")
			hss.SetFinalizers(helpers.RemoveElement(hss.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, hss)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
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
	curScheduledSearch, err := r.HumioClient.GetScheduledSearch(config, req, hss)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		r.Log.Info("ScheduledSearch doesn't exist. Now adding scheduled search")
		addedScheduledSearch, err := r.HumioClient.AddScheduledSearch(config, req, hss)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create scheduled search")
		}
		r.Log.Info("Created scheduled search", "ScheduledSearch", hss.Spec.Name)

		result, err := r.reconcileHumioScheduledSearchAnnotations(ctx, addedScheduledSearch, hss, req)
		if err != nil {
			return result, err
		}
		return reconcile.Result{Requeue: true}, nil
	}
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if scheduled search")
	}

	r.Log.Info("Checking if scheduled search needs to be updated")
	if err := r.HumioClient.ValidateActionsForScheduledSearch(config, req, hss); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not get action id mapping")
	}
	expectedScheduledSearch, err := humio.ScheduledSearchTransform(hss)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not parse expected ScheduledSearch")
	}

	sanitizeScheduledSearch(curScheduledSearch)
	if !reflect.DeepEqual(*curScheduledSearch, *expectedScheduledSearch) {
		r.Log.Info(fmt.Sprintf("ScheduledSearch differs, triggering update, expected %#v, got: %#v",
			expectedScheduledSearch,
			curScheduledSearch))
		scheduledSearch, err := r.HumioClient.UpdateScheduledSearch(config, req, hss)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update scheduled search")
		}
		if scheduledSearch != nil {
			r.Log.Info(fmt.Sprintf("Updated scheduled search %q", scheduledSearch.Name))
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioScheduledSearchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioScheduledSearch{}).
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

func sanitizeScheduledSearch(scheduledSearch *humioapi.ScheduledSearch) {
	scheduledSearch.RunAsUserID = ""
}
