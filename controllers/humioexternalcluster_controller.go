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
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioExternalClusterReconciler reconciles a HumioExternalCluster object
type HumioExternalClusterReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioexternalclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioexternalclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioexternalclusters/finalizers,verbs=update

func (r *HumioExternalClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioExternalCluster")

	// Fetch the HumioExternalCluster instance
	hec := &humiov1alpha1.HumioExternalCluster{}
	err := r.Get(ctx, req.NamespacedName, hec)
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

	if hec.Status.State == "" {
		err := r.setState(ctx, humiov1alpha1.HumioExternalClusterStateUnknown, hec)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
	}

	cluster, err := helpers.NewCluster(ctx, r, "", hec.Name, hec.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		return reconcile.Result{}, err
	}

	err = r.HumioClient.TestAPIToken(cluster.Config(), req)
	if err != nil {
		r.Log.Error(err, "unable to test if the API token is works")
		err = r.Client.Get(ctx, req.NamespacedName, hec)
		if err != nil {
			r.Log.Error(err, "unable to get cluster state")
			return reconcile.Result{}, err
		}
		err = r.setState(ctx, humiov1alpha1.HumioExternalClusterStateUnknown, hec)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: time.Second * 15}, nil
	}

	err = r.Client.Get(ctx, req.NamespacedName, hec)
	if err != nil {
		r.Log.Error(err, "unable to get cluster state")
		return reconcile.Result{}, err
	}
	if hec.Status.State != humiov1alpha1.HumioExternalClusterStateReady {
		err = r.setState(ctx, humiov1alpha1.HumioExternalClusterStateReady, hec)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioExternalClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioExternalCluster{}).
		Complete(r)
}
