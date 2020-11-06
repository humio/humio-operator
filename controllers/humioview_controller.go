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
	hv := &humiov1alpha1.HumioView{}
	err := r.Get(context.TODO(), req.NamespacedName, hv)
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

	defer r.SetLatestState(hv)

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

	// Get current repository
	r.Log.Info("get current view")
	curView, err := r.HumioClient.GetView(hv)
	if err != nil {
		r.Log.Error(err, "could not check if view exists")
		return reconcile.Result{}, fmt.Errorf("could not check if view exists: %s", err)
	}

	emptyView := humioapi.View{}
	if reflect.DeepEqual(emptyView, *curView) {
		r.Log.Info("View doesn't exist. Now adding view")
		// create View
		_, err := r.HumioClient.AddView(hv)
		if err != nil {
			r.Log.Error(err, "could not create view")
			return reconcile.Result{}, fmt.Errorf("could not create view: %s", err)
		}
		r.Log.Info("created view", "ViewName", hv.Spec.Name)
		return reconcile.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

func (r *HumioViewReconciler) SetLatestState(hv *humiov1alpha1.HumioView) {
	func(ctx context.Context, humioClient humio.Client, hv *humiov1alpha1.HumioView) {
		curView, err := humioClient.GetView(hv)
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
	}(context.TODO(), r.HumioClient, hv)
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
