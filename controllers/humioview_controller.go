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
	"github.com/go-logr/zapr"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	uberzap "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioViewReconciler reconciles a HumioView object
type HumioViewReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	HumioClient humio.Client
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviews/status,verbs=get;update;patch

func (r *HumioViewReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	zapLog, _ := uberzap.NewProduction(uberzap.AddCaller(), uberzap.AddCallerSkip(1))
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioView")

	// Fetch the HumioView instance
	humioViewSpec, err := r.getViewSpec(req)
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

	defer r.setLatestState(humioViewSpec)

	result, err := r.authenticate(humioViewSpec)
	if err != nil {
		return result, err
	}

	curView, result, err := r.getView(humioViewSpec)
	if err != nil {
		return result, err
	}

	reconcileHumioViewResult, err := r.reconcileHumioView(curView, humioViewSpec)
	if err != nil {
		return reconcileHumioViewResult, err
	}

	return reconcileHumioViewResult, nil
}

func (r *HumioViewReconciler) reconcileHumioView(curView *humioapi.View, hv *humiov1alpha1.HumioView) (reconcile.Result, error) {
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
			err := r.Update(context.TODO(), hv)
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
		err := r.Update(context.TODO(), hv)
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

	return reconcile.Result{}, nil
}

func (r *HumioViewReconciler) getView(hv *humiov1alpha1.HumioView) (*humioapi.View, reconcile.Result, error) {
	r.Log.Info("get current view")
	curView, err := r.HumioClient.GetView(hv)
	if err != nil {
		r.Log.Error(err, "could not check if view exists")
		return nil, reconcile.Result{}, fmt.Errorf("could not check if view exists: %s", err)
	}
	return curView, reconcile.Result{}, nil
}

func (r *HumioViewReconciler) authenticate(hv *humiov1alpha1.HumioView) (reconcile.Result, error) {
	cluster, err := helpers.NewCluster(context.TODO(), r, hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName, hv.Namespace, helpers.UseCertManager())
	if err != nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		return reconcile.Result{}, err
	}

	err = r.HumioClient.Authenticate(cluster.Config())
	if err != nil {
		r.Log.Error(err, "unable to authenticate humio client")
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}
	return reconcile.Result{}, nil
}

func (r *HumioViewReconciler) getViewSpec(req ctrl.Request) (*humiov1alpha1.HumioView, error) {
	hv := &humiov1alpha1.HumioView{}
	err := r.Get(context.TODO(), req.NamespacedName, hv)

	return hv, err
}

func (r *HumioViewReconciler) setLatestState(hv *humiov1alpha1.HumioView) {
	ctx := context.TODO()
	curView, err := r.HumioClient.GetView(hv)
	if err != nil {
		r.setState(ctx, humiov1alpha1.HumioViewStateUnknown, hv)
		return
	}
	emptyView := humioapi.View{}
	if reflect.DeepEqual(emptyView, *curView) {
		r.setState(ctx, humiov1alpha1.HumioViewStateNotFound, hv)
		return
	}
	r.setState(ctx, humiov1alpha1.HumioViewStateExists, hv)
}

func (r *HumioViewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioView{}).
		Complete(r)
}

func (r *HumioViewReconciler) setState(ctx context.Context, state string, hr *humiov1alpha1.HumioView) error {
	hr.Status.State = state
	return r.Status().Update(ctx, hr)
}
