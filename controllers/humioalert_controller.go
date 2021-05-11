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

	humioapi "github.com/humio/cli/api"

	"github.com/humio/humio-operator/pkg/helpers"
	uberzap "go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
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
	zapLog, _ := uberzap.NewProduction(uberzap.AddCaller(), uberzap.AddCallerSkip(1))
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioAlert")

	ha := &humiov1alpha1.HumioAlert{}
	err := r.Get(context.TODO(), req.NamespacedName, ha)
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

	cluster, err := helpers.NewCluster(context.TODO(), r, ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName, ha.Namespace, helpers.UseCertManager())
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		err = r.setState(context.TODO(), humiov1alpha1.HumioAlertStateConfigError, ha)
		if err != nil {
			r.Log.Error(err, "unable to set Alert state")
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}
	r.HumioClient.SetHumioClientConfig(cluster.Config(), false)

	curAlert, err := r.HumioClient.GetAlert(ha)
	if curAlert != nil && err != nil {
		r.Log.Error(err, "got unexpected error when checking if Alert exists")
		err = r.setState(context.TODO(), humiov1alpha1.HumioAlertStateUnknown, ha)
		if err != nil {
			r.Log.Error(err, "unable to set Alert state")
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
	}(context.TODO(), r.HumioClient, ha)

	return r.reconcileHumioAlert(curAlert, ha, req)
}

func (r *HumioAlertReconciler) reconcileHumioAlert(curAlert *humioapi.Alert, ha *humiov1alpha1.HumioAlert, req ctrl.Request) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if alert is marked to be deleted")
	isMarkedForDeletion := ha.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("Alert marked to be deleted")
		if helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting alert")
			if err := r.HumioClient.DeleteAlert(ha); err != nil {
				r.Log.Error(err, "Delete alert returned error")
				return reconcile.Result{}, err
			}

			r.Log.Info("Alert Deleted. Removing finalizer")
			ha.SetFinalizers(helpers.RemoveElement(ha.GetFinalizers(), humioFinalizer))
			err := r.Update(context.TODO(), ha)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if alert requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to alert")
		ha.SetFinalizers(append(ha.GetFinalizers(), humioFinalizer))
		err := r.Update(context.TODO(), ha)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if alert needs to be created")
	// Add Alert
	if curAlert == nil {
		r.Log.Info("Alert doesn't exist. Now adding alert")
		addedAlert, err := r.HumioClient.AddAlert(ha)
		if err != nil {
			r.Log.Error(err, "could not create alert")
			return reconcile.Result{}, fmt.Errorf("could not create alert: %s", err)
		}
		r.Log.Info("Created alert", "Alert", ha.Spec.Name)

		result, err := r.reconcileHumioAlertAnnotations(addedAlert, ha, req)
		if err != nil {
			return result, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if alert needs to be updated")
	// Update
	actionIdMap, err := r.HumioClient.GetActionIDsMapForAlerts(ha)
	if err != nil {
		r.Log.Error(err, "could not get action id mapping")
		return reconcile.Result{}, fmt.Errorf("could not get action id mapping: %s", err)
	}
	expectedAlert, err := humio.AlertTransform(ha, actionIdMap)
	if err != nil {
		r.Log.Error(err, "could not parse expected alert")
		return reconcile.Result{}, fmt.Errorf("could not parse expected Alert: %s", err)
	}
	if !reflect.DeepEqual(*curAlert, *expectedAlert) {
		r.Log.Info(fmt.Sprintf("Alert differs, triggering update, expected %#v, got: %#v",
			expectedAlert,
			curAlert))
		alert, err := r.HumioClient.UpdateAlert(ha)
		if err != nil {
			r.Log.Error(err, "could not update alert")
			return reconcile.Result{}, fmt.Errorf("could not update alert: %s", err)
		}
		if alert != nil {
			r.Log.Info(fmt.Sprintf("Updated alert \"%s\"", alert.Name))
		}
	}

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
	r.Log.Info(fmt.Sprintf("setting alert state to %s", state))
	ha.Status.State = state
	return r.Status().Update(ctx, ha)
}
