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
	"github.com/go-logr/zapr"
	"github.com/humio/humio-operator/pkg/helpers"
	uberzap "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioExternalClusterReconciler reconciles a HumioExternalCluster object
type HumioExternalClusterReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	HumioClient humio.Client
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioexternalclusters;humioexternalclusters/finalizers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioexternalclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *HumioExternalClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	zapLog, _ := uberzap.NewProduction(uberzap.AddCaller(), uberzap.AddCallerSkip(1))
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioExternalCluster")

	// Fetch the HumioExternalCluster instance
	hec := &humiov1alpha1.HumioExternalCluster{}
	err := r.Get(context.TODO(), req.NamespacedName, hec)
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

	if hec.Status.State == "" {
		err := r.setState(context.TODO(), humiov1alpha1.HumioExternalClusterStateUnknown, hec)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
	}

	cluster, err := helpers.NewCluster(context.TODO(), r, "", hec.Name, hec.Namespace, helpers.UseCertManager())
	if err != nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		return reconcile.Result{}, err
	}

	r.HumioClient.SetHumioClientConfig(cluster.Config())

	err = r.HumioClient.TestAPIToken()
	if err != nil {
		err = r.Client.Get(context.TODO(), req.NamespacedName, hec)
		if err != nil {
			r.Log.Error(err, "unable to get cluster state")
			return reconcile.Result{}, err
		}
		err = r.setState(context.TODO(), humiov1alpha1.HumioExternalClusterStateUnknown, hec)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
	}

	err = r.Client.Get(context.TODO(), req.NamespacedName, hec)
	if err != nil {
		r.Log.Error(err, "unable to get cluster state")
		return reconcile.Result{}, err
	}
	if hec.Status.State != humiov1alpha1.HumioExternalClusterStateReady {
		err = r.setState(context.TODO(), humiov1alpha1.HumioExternalClusterStateReady, hec)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
}

func (r *HumioExternalClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioExternalCluster{}).
		Complete(r)
}
