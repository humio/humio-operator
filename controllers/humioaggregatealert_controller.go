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

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
)

// HumioAggregateAlertReconciler reconciles a HumioAggregateAlert object
type HumioAggregateAlertReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioAggregateAlerts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioAggregateAlerts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioAggregateAlerts/finalizers,verbs=update

func (r *HumioAggregateAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HummioAggregateAlert")

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

	cluster, err := helpers.NewCluster(ctx, r, haa.Spec.ManagedClusterName, haa.Spec.ExternalClusterName, haa.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioScheduledSearchStateConfigError, haa)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set scheduled search state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}

	defer func(ctx context.Context, HumioClient humio.Client, haa *humiov1alpha1.HumioAggregateAlert) {
		curAggregateAlert, err := r.HumioClient.GetAggregateAlert(cluster.Config(), req, haa)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioAggregateAlertStateNotFound, haa)
			return
		}
		if err != nil || curAggregateAlert == nil {
			_ = r.setState(ctx, humiov1alpha1.HumioAggregateAlertStateConfigError, haa)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioAggregateAlertStateExists, haa)
	}(ctx, r.HumioClient, haa)

	return r.reconcileHumioAggregateAlert(ctx, cluster.Config(), haa, req)
}

func (r *HumioAggregateAlertReconciler) reconcileHumioAggregateAlert(ctx context.Context, config *humioapi.Config, haa *humiov1alpha1.HumioAggregateAlert, req ctrl.Request) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if alert is marked to be deleted")
	isMarkedForDeletion := haa.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("AggregateAlert marked to be deleted")
		if helpers.ContainsElement(haa.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting aggregate alert")
			if err := r.HumioClient.DeleteAggregateAlert(config, req, haa); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete aggregate alert returned error")
			}

			r.Log.Info("AggregateAlert Deleted. Removing finalizer")
			haa.SetFinalizers(helpers.RemoveElement(haa.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, haa)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
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
	curAggregateAlert, err := r.HumioClient.GetAggregateAlert(config, req, haa)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		r.Log.Info("AggregateAlert doesn't exist. Now adding aggregate alert")
		addedAggregateAlert, err := r.HumioClient.AddAggregateAlert(config, req, haa)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create aggregate alert")
		}
		r.Log.Info("Created aggregate alert", "AggregateAlert", haa.Spec.Name)

		result, err := r.reconcileHumioAggregateAlertAnnotations(ctx, addedAggregateAlert, haa, req)
		if err != nil {
			return result, err
		}
		return reconcile.Result{Requeue: true}, nil
	}
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if aggregate alert exists")
	}

	r.Log.Info("Checking if aggregate alert needs to be updated")
	// Update
	if err := r.HumioClient.ValidateActionsForAggregateAlert(config, req, haa); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not validate actions for aggregate alert")
	}
	expectedAggregateAlert, err := humio.AggregateAlertTransform(haa)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not parse expected AggregateAlert")
	}

	sanitizeAggregateAlert(curAggregateAlert)
	if !reflect.DeepEqual(*curAggregateAlert, *expectedAggregateAlert) {
		r.Log.Info(fmt.Sprintf("AggregateAlert differs, triggering update, expected %#v, got: %#v",
			expectedAggregateAlert,
			curAggregateAlert))
		AggregateAlert, err := r.HumioClient.UpdateAggregateAlert(config, req, haa)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update agregate alert")
		}
		if AggregateAlert != nil {
			r.Log.Info(fmt.Sprintf("Updated Aggregate Alert %q", AggregateAlert.Name))
		}
	}

	r.Log.Info("done reconciling, will requeue in 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil

}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioAggregateAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioAggregateAlert{}).
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

func sanitizeAggregateAlert(aggregateAlert *humioapi.AggregateAlert) {
	aggregateAlert.RunAsUserID = ""
}
