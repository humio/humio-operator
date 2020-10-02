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
	"go.uber.org/zap"
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
	Log         logr.Logger // TODO: Migrate to *zap.SugaredLogger
	logger      *zap.SugaredLogger
	Scheme      *runtime.Scheme
	HumioClient humio.Client
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioexternalclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioexternalclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *HumioExternalClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	r.logger = logger.Sugar().With("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.logger.Info("Reconciling HumioExternalCluster")

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
			r.logger.Infof("unable to set cluster state: %s", err)
			return reconcile.Result{}, err
		}
	}

	cluster, err := helpers.NewCluster(context.TODO(), r, "", hec.Name, hec.Namespace, helpers.UseCertManager())
	if err != nil || cluster.Config() == nil {
		r.logger.Error("unable to obtain humio client config: %s", err)
		return reconcile.Result{}, err
	}

	err = r.HumioClient.Authenticate(cluster.Config())
	if err != nil {
		r.logger.Warnf("unable to authenticate humio client: %s", err)
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}

	err = r.HumioClient.TestAPIToken()
	if err != nil {
		err = r.Client.Get(context.TODO(), req.NamespacedName, hec)
		if err != nil {
			r.logger.Infof("unable to get cluster state: %s", err)
			return reconcile.Result{}, err
		}
		err = r.setState(context.TODO(), humiov1alpha1.HumioExternalClusterStateUnknown, hec)
		if err != nil {
			r.logger.Infof("unable to set cluster state: %s", err)
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
	}

	err = r.Client.Get(context.TODO(), req.NamespacedName, hec)
	if err != nil {
		r.logger.Infof("unable to get cluster state: %s", err)
		return reconcile.Result{}, err
	}
	if hec.Status.State != humiov1alpha1.HumioExternalClusterStateReady {
		err = r.setState(context.TODO(), humiov1alpha1.HumioExternalClusterStateReady, hec)
		if err != nil {
			r.logger.Infof("unable to set cluster state: %s", err)
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
