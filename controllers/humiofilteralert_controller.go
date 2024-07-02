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

// HumioFilterAlertReconciler reconciles a HumioFilterAlert object
type HumioFilterAlertReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humiofilteralerts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humiofilteralerts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humiofilteralerts/finalizers,verbs=update

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

	cluster, err := helpers.NewCluster(ctx, r, hfa.Spec.ManagedClusterName, hfa.Spec.ExternalClusterName, hfa.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		err = r.setState(ctx, humiov1alpha1.HumioFilterAlertStateConfigError, hfa)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set filter alert state")
		}
		return reconcile.Result{}, err
	}

	defer func(ctx context.Context, humioClient humio.Client, hfa *humiov1alpha1.HumioFilterAlert) {
		curFilterAlert, err := r.HumioClient.GetFilterAlert(cluster.Config(), req, hfa)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioFilterAlertStateNotFound, hfa)
			return
		}
		if err != nil || curFilterAlert == nil {
			_ = r.setState(ctx, humiov1alpha1.HumioFilterAlertStateConfigError, hfa)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioFilterAlertStateExists, hfa)
	}(ctx, r.HumioClient, hfa)

	return r.reconcileHumioFilterAlert(ctx, cluster.Config(), hfa, req)
}

func (r *HumioFilterAlertReconciler) reconcileHumioFilterAlert(ctx context.Context, config *humioapi.Config, hfa *humiov1alpha1.HumioFilterAlert, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info("Checking if filter alert is marked to be deleted")
	isMarkedForDeletion := hfa.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("FilterAlert marked to be deleted")
		if helpers.ContainsElement(hfa.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting filter alert")
			if err := r.HumioClient.DeleteFilterAlert(config, req, hfa); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete filter alert returned error")
			}

			r.Log.Info("FilterAlert Deleted. Removing finalizer")
			hfa.SetFinalizers(helpers.RemoveElement(hfa.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, hfa)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if filter alert requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(hfa.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to filter alert")
		hfa.SetFinalizers(append(hfa.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, hfa)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if filter alert needs to be created")
	curFilterAlert, err := r.HumioClient.GetFilterAlert(config, req, hfa)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		r.Log.Info("FilterAlert doesn't exist. Now adding filter alert")
		addedFilterAlert, err := r.HumioClient.AddFilterAlert(config, req, hfa)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create filter alert")
		}
		r.Log.Info("Created filter alert", "FilterAlert", hfa.Spec.Name)

		result, err := r.reconcileHumioFilterAlertAnnotations(ctx, addedFilterAlert, hfa, req)
		if err != nil {
			return result, err
		}
		return reconcile.Result{Requeue: true}, nil
	}
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if alert exists")
	}

	r.Log.Info("Checking if filter alert needs to be updated")
	if err := r.HumioClient.ValidateActionIDsForFilterAlert(config, req, hfa); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not get action id mapping")
	}
	expectedFilterAlert, err := humio.FilterAlertTransform(hfa)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not parse expected FilterAlert")
	}

	sanitizeFilterAlert(curFilterAlert)
	if !reflect.DeepEqual(*curFilterAlert, *expectedFilterAlert) {
		r.Log.Info(fmt.Sprintf("FilterAlert differs, triggering update, expected %#v, got: %#v",
			expectedFilterAlert,
			curFilterAlert))
		filterAlert, err := r.HumioClient.UpdateFilterAlert(config, req, hfa)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update filter alert")
		}
		if filterAlert != nil {
			r.Log.Info(fmt.Sprintf("Updated filter lert %q", filterAlert.Name))
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioFilterAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioFilterAlert{}).
		Complete(r)
}

func (r *HumioFilterAlertReconciler) setState(ctx context.Context, state string, hfa *humiov1alpha1.HumioFilterAlert) error {
	if hfa.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting filter alert state to %s", state))
	hfa.Status.State = state
	return r.Status().Update(ctx, hfa)
}

func (r *HumioFilterAlertReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

func sanitizeFilterAlert(filterAlert *humioapi.FilterAlert) {
	filterAlert.ID = ""
	filterAlert.RunAsUserID = ""
}
