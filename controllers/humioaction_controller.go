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
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioActionReconciler reconciles a HumioAction object
type HumioActionReconciler struct {
	client.Client
	Log         logr.Logger
	HumioClient humio.Client
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions/finalizers,verbs=update

func (r *HumioActionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	zapLog, _ := helpers.NewLogger()
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioAction", logFieldFunctionName, helpers.GetCurrentFuncName())

	ha := &humiov1alpha1.HumioAction{}
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

	cluster, err := helpers.NewCluster(ctx, r, ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName, ha.Namespace, helpers.UseCertManager())
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config", logFieldFunctionName, helpers.GetCurrentFuncName())
		err = r.setState(ctx, humiov1alpha1.HumioActionStateConfigError, ha)
		if err != nil {
			r.Log.Error(err, "unable to set action state", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}
	r.HumioClient.SetHumioClientConfig(cluster.Config(), req)

	if _, err := humio.NotifierFromAction(ha); err != nil {
		r.Log.Error(err, "unable to validate action", logFieldFunctionName, helpers.GetCurrentFuncName())
		err = r.setState(ctx, humiov1alpha1.HumioActionStateConfigError, ha)
		if err != nil {
			r.Log.Error(err, "unable to set action state", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	curNotifier, err := r.HumioClient.GetNotifier(ha)
	if curNotifier != nil && err != nil {
		r.Log.Error(err, "got unexpected error when checking if action exists", logFieldFunctionName, helpers.GetCurrentFuncName())
		stateErr := r.setState(ctx, humiov1alpha1.HumioActionStateUnknown, ha)
		if stateErr != nil {
			r.Log.Error(stateErr, "unable to set action state", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, stateErr
		}
		return reconcile.Result{}, fmt.Errorf("could not check if action exists: %s", err)
	}

	defer func(ctx context.Context, humioClient humio.Client, ha *humiov1alpha1.HumioAction) {
		curNotifier, err := r.HumioClient.GetNotifier(ha)
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioActionStateUnknown, ha)
			return
		}
		if curNotifier == nil {
			_ = r.setState(ctx, humiov1alpha1.HumioActionStateNotFound, ha)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioActionStateExists, ha)
	}(ctx, r.HumioClient, ha)

	return r.reconcileHumioAction(ctx, curNotifier, ha, req)
}

func (r *HumioActionReconciler) reconcileHumioAction(ctx context.Context, curNotifier *humioapi.Notifier, ha *humiov1alpha1.HumioAction, req ctrl.Request) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if Action is marked to be deleted", logFieldFunctionName, helpers.GetCurrentFuncName())
	isMarkedForDeletion := ha.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("Action marked to be deleted", logFieldFunctionName, helpers.GetCurrentFuncName())
		if helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting Action", logFieldFunctionName, helpers.GetCurrentFuncName())
			if err := r.HumioClient.DeleteNotifier(ha); err != nil {
				r.Log.Error(err, "Delete Action returned error", logFieldFunctionName, helpers.GetCurrentFuncName())
				return reconcile.Result{}, err
			}

			r.Log.Info("Action Deleted. Removing finalizer", logFieldFunctionName, helpers.GetCurrentFuncName())
			ha.SetFinalizers(helpers.RemoveElement(ha.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, ha)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully", logFieldFunctionName, helpers.GetCurrentFuncName())
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if Action requires finalizer", logFieldFunctionName, helpers.GetCurrentFuncName())
	// Add finalizer for this CR
	if !helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to Action", logFieldFunctionName, helpers.GetCurrentFuncName())
		ha.SetFinalizers(append(ha.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, ha)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if action needs to be created", logFieldFunctionName, helpers.GetCurrentFuncName())
	// Add Action
	if curNotifier == nil {
		r.Log.Info("Action doesn't exist. Now adding action", logFieldFunctionName, helpers.GetCurrentFuncName())
		addedNotifier, err := r.HumioClient.AddNotifier(ha)
		if err != nil {
			r.Log.Error(err, "could not create action", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, fmt.Errorf("could not create Action: %s", err)
		}
		r.Log.Info("Created action", "Action", ha.Spec.Name, logFieldFunctionName, helpers.GetCurrentFuncName())

		result, err := r.reconcileHumioActionAnnotations(ctx, addedNotifier, ha, req)
		if err != nil {
			return result, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if action needs to be updated", logFieldFunctionName, helpers.GetCurrentFuncName())
	// Update
	expectedNotifier, err := humio.NotifierFromAction(ha)
	if err != nil {
		r.Log.Error(err, "could not parse expected action", logFieldFunctionName, helpers.GetCurrentFuncName())
		return reconcile.Result{}, fmt.Errorf("could not parse expected action: %s", err)
	}
	if !reflect.DeepEqual(*curNotifier, *expectedNotifier) {
		r.Log.Info(fmt.Sprintf("Action differs, triggering update, expected %#v, got: %#v",
			expectedNotifier,
			curNotifier), logFieldFunctionName, helpers.GetCurrentFuncName())
		notifier, err := r.HumioClient.UpdateNotifier(ha)
		if err != nil {
			r.Log.Error(err, "could not update action", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, fmt.Errorf("could not update action: %s", err)
		}
		if notifier != nil {
			r.Log.Info(fmt.Sprintf("Updated notifier \"%s\"", notifier.Name), logFieldFunctionName, helpers.GetCurrentFuncName())
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds", logFieldFunctionName, helpers.GetCurrentFuncName())
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioActionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioAction{}).
		Complete(r)
}

func (r *HumioActionReconciler) setState(ctx context.Context, state string, hr *humiov1alpha1.HumioAction) error {
	if hr.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting action state to %s", state), logFieldFunctionName, helpers.GetCurrentFuncName())
	hr.Status.State = state
	return r.Status().Update(ctx, hr)
}
