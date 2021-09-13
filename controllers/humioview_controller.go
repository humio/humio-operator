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
	"github.com/go-logr/logr"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"k8s.io/apimachinery/pkg/api/errors"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioViewReconciler reconciles a HumioView object
type HumioViewReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioviews,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioviews/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioviews/finalizers,verbs=update

func (r *HumioViewReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioView")

	// Fetch the HumioView instance
	hv := &humiov1alpha1.HumioView{}
	err := r.Get(ctx, req.NamespacedName, hv)
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

	cluster, err := helpers.NewCluster(ctx, r, hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName, hv.Namespace, helpers.UseCertManager())
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		err = r.setState(ctx, humiov1alpha1.HumioParserStateConfigError, hv)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	defer func(ctx context.Context, humioClient humio.Client, hv *humiov1alpha1.HumioView) {
		curView, err := r.HumioClient.GetView(hv)
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioViewStateUnknown, hv)
			return
		}
		emptyView := humioapi.View{}
		if reflect.DeepEqual(emptyView, *curView) {
			_ = r.setState(ctx, humiov1alpha1.HumioViewStateNotFound, hv)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioViewStateExists, hv)
	}(ctx, r.HumioClient, hv)

	r.HumioClient.SetHumioClientConfig(cluster.Config(), req)

	r.Log.Info("get current view")
	curView, err := r.HumioClient.GetView(hv)
	if err != nil {
		r.Log.Error(err, "could not check if view exists")
		return reconcile.Result{}, fmt.Errorf("could not check if view exists: %s", err)
	}

	return r.reconcileHumioView(ctx, curView, hv)
}

func (r *HumioViewReconciler) reconcileHumioView(ctx context.Context, curView *humioapi.View, hv *humiov1alpha1.HumioView) (reconcile.Result, error) {
	emptyView := humioapi.View{}

	// Delete
	r.Log.Info("Checking if view is marked to be deleted")
	isMarkedForDeletion := hv.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("View marked to be deleted")
		if helpers.ContainsElement(hv.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting View")
			if err := r.HumioClient.DeleteView(hv); err != nil {
				r.Log.Error(err, "Delete view returned error")
				return reconcile.Result{}, err
			}

			r.Log.Info("View Deleted. Removing finalizer")
			hv.SetFinalizers(helpers.RemoveElement(hv.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, hv)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hv.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to view")
		hv.SetFinalizers(append(hv.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, hv)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	// Add View
	if reflect.DeepEqual(emptyView, *curView) {
		r.Log.Info("View doesn't exist. Now adding view")
		_, err := r.HumioClient.AddView(hv)
		if err != nil {
			r.Log.Error(err, "could not create view")
			return reconcile.Result{}, fmt.Errorf("could not create view: %s", err)
		}
		r.Log.Info("created view", "ViewName", hv.Spec.Name)
		return reconcile.Result{Requeue: true}, nil
	}

	// Update
	if reflect.DeepEqual(curView.Connections, hv.GetViewConnections()) == false {
		r.Log.Info(fmt.Sprintf("view information differs, triggering update, expected %v, got: %v",
			hv.Spec.Connections,
			curView.Connections))
		_, err := r.HumioClient.UpdateView(hv)
		if err != nil {
			r.Log.Error(err, "could not update view")
			return reconcile.Result{}, fmt.Errorf("could not update view: %s", err)
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioViewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioView{}).
		Complete(r)
}

func (r *HumioViewReconciler) setState(ctx context.Context, state string, hr *humiov1alpha1.HumioView) error {
	if hr.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting view state to %s", state))
	hr.Status.State = state
	return r.Status().Update(ctx, hr)
}
