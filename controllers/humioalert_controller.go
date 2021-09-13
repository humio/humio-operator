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
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioAlertReconciler reconciles a HumioAlert object
type HumioAlertReconciler struct {
	client.Client
	Log         logr.Logger
	HumioClient humio.Client
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioalerts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioalerts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioalerts/finalizers,verbs=update

func (r *HumioAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	zapLog, _ := helpers.NewLogger()
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioAlert", logFieldFunctionName, helpers.GetCurrentFuncName())

	ha := &humiov1alpha1.HumioAlert{}
	err := r.Get(ctx, req.NamespacedName, ha)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	setAlertDefaults(ha)

	cluster, err := helpers.NewCluster(ctx, r, ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName, ha.Namespace, helpers.UseCertManager())
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config", logFieldFunctionName, helpers.GetCurrentFuncName())
		err = r.setState(ctx, humiov1alpha1.HumioAlertStateConfigError, ha)
		if err != nil {
			r.Log.Error(err, "unable to set Alert state", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}
	r.HumioClient.SetHumioClientConfig(cluster.Config(), req)

	curAlert, err := r.HumioClient.GetAlert(ha)
	if curAlert != nil && err != nil {
		r.Log.Error(err, "got unexpected error when checking if Alert exists", logFieldFunctionName, helpers.GetCurrentFuncName())
		err = r.setState(ctx, humiov1alpha1.HumioAlertStateUnknown, ha)
		if err != nil {
			r.Log.Error(err, "unable to set Alert state", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, fmt.Errorf("could not check if Alert exists: %s", err)
	}

	defer func(ctx context.Context, humioClient humio.Client, ha *humiov1alpha1.HumioAlert) {
		curAlert, err := r.HumioClient.GetAlert(ha)
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioAlertStateConfigError, ha)
			return
		}
		if curAlert == nil {
			_ = r.setState(ctx, humiov1alpha1.HumioAlertStateNotFound, ha)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioAlertStateExists, ha)
	}(ctx, r.HumioClient, ha)

	return r.reconcileHumioAlert(ctx, curAlert, ha, req)
}

func (r *HumioAlertReconciler) reconcileHumioAlert(ctx context.Context, curAlert *humioapi.Alert, ha *humiov1alpha1.HumioAlert, req ctrl.Request) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if alert is marked to be deleted", logFieldFunctionName, helpers.GetCurrentFuncName())
	isMarkedForDeletion := ha.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("Alert marked to be deleted", logFieldFunctionName, helpers.GetCurrentFuncName())
		if helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting alert", logFieldFunctionName, helpers.GetCurrentFuncName())
			if err := r.HumioClient.DeleteAlert(ha); err != nil {
				r.Log.Error(err, "Delete alert returned error", logFieldFunctionName, helpers.GetCurrentFuncName())
				return reconcile.Result{}, err
			}

			r.Log.Info("Alert Deleted. Removing finalizer", logFieldFunctionName, helpers.GetCurrentFuncName())
			ha.SetFinalizers(helpers.RemoveElement(ha.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, ha)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully", logFieldFunctionName, helpers.GetCurrentFuncName())
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if alert requires finalizer", logFieldFunctionName, helpers.GetCurrentFuncName())
	// Add finalizer for this CR
	if !helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to alert", logFieldFunctionName, helpers.GetCurrentFuncName())
		ha.SetFinalizers(append(ha.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, ha)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if alert needs to be created", logFieldFunctionName, helpers.GetCurrentFuncName())
	// Add Alert
	if curAlert == nil {
		r.Log.Info("Alert doesn't exist. Now adding alert", logFieldFunctionName, helpers.GetCurrentFuncName())
		addedAlert, err := r.HumioClient.AddAlert(ha)
		if err != nil {
			r.Log.Error(err, "could not create alert", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, fmt.Errorf("could not create alert: %s", err)
		}
		r.Log.Info("Created alert", "Alert.Name", ha.Spec.Name, logFieldFunctionName, helpers.GetCurrentFuncName())

		result, err := r.reconcileHumioAlertAnnotations(ctx, addedAlert, ha, req)
		if err != nil {
			return result, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if alert needs to be updated", logFieldFunctionName, helpers.GetCurrentFuncName())
	// Update
	actionIdMap, err := r.HumioClient.GetActionIDsMapForAlerts(ha)
	if err != nil {
		r.Log.Error(err, "could not get action id mapping", logFieldFunctionName, helpers.GetCurrentFuncName())
		return reconcile.Result{}, fmt.Errorf("could not get action id mapping: %s", err)
	}
	expectedAlert, err := humio.AlertTransform(ha, actionIdMap)
	if err != nil {
		r.Log.Error(err, "could not parse expected alert", logFieldFunctionName, helpers.GetCurrentFuncName())
		return reconcile.Result{}, fmt.Errorf("could not parse expected Alert: %s", err)
	}
	if !reflect.DeepEqual(*curAlert, *expectedAlert) {
		r.Log.Info(fmt.Sprintf("Alert differs, triggering update, expected %#v, got: %#v",
			expectedAlert,
			curAlert), logFieldFunctionName, helpers.GetCurrentFuncName())
		alert, err := r.HumioClient.UpdateAlert(ha)
		if err != nil {
			r.Log.Error(err, "could not update alert", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, fmt.Errorf("could not update alert: %s", err)
		}
		if alert != nil {
			r.Log.Info(fmt.Sprintf("Updated alert \"%s\"", alert.Name), logFieldFunctionName, helpers.GetCurrentFuncName())
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds", logFieldFunctionName, helpers.GetCurrentFuncName())
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.HumioAlert{}).
		Complete(r)
}

func (r *HumioAlertReconciler) setState(ctx context.Context, state string, ha *humiov1alpha1.HumioAlert) error {
	if ha.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting alert state to %s", state), logFieldFunctionName, helpers.GetCurrentFuncName())
	ha.Status.State = state
	return r.Status().Update(ctx, ha)
}
