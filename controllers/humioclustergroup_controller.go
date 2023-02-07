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
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioClusterGroupReconciler reconciles a HumioClusterGroup object
type HumioClusterGroupReconciler struct {
	client.Client
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioclustergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioclustergroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioclustergroups/finalizers,verbs=update

// Reconcile runs the reconciler for a HumioClusterGroup object
func (r *HumioClusterGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioClusterGroup")

	// Fetch the HumioClusterGroup
	hcg := &humiov1alpha1.HumioClusterGroup{}
	if err := r.Get(ctx, req.NamespacedName, hcg); err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// GC HumioCluster resources that may have been removed
	for _, humioClusterGroupStatus := range hcg.Status {
		for _, clusterStatus := range humioClusterGroupStatus.ClusterList {
			hc := humiov1alpha1.HumioCluster{}
			clusterKey := types.NamespacedName{Name: clusterStatus.ClusterName, Namespace: hcg.Namespace}
			if err := r.Get(ctx, clusterKey, &hc); err != nil {
				if k8serrors.IsNotFound(err) {
					r.Log.Info(fmt.Sprintf("cluster %s no longer exists. removing it from the cluster group lock...", clusterStatus.ClusterName))
					hc.Name = clusterStatus.ClusterName
					hc.Namespace = hcg.Namespace
					hc.Spec.ClusterGroup = humiov1alpha1.HumioClusterGroupConfiguration{
						Enabled: true,
						Name:    hcg.Name,
					}
					return newClusterGroupLock(r.Client, r.Client.Status(), &hc).tryClusterGroupLock(clusterStatus.ClusterState, HumioClusterGroupLockRelease)
				}
			}
			timeSinceLastUpdate := time.Now().Sub(clusterStatus.LastUpdateTime.Time)
			r.Log.Info(fmt.Sprintf("cluster %s has been in state %s for %d ms", clusterStatus.ClusterName, clusterStatus.ClusterState, timeSinceLastUpdate.Milliseconds()))
		}
	}
	return reconcile.Result{RequeueAfter: time.Second * 60}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioClusterGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioClusterGroup{}).
		Owns(&humiov1alpha1.HumioCluster{}).
		Complete(r)
}

func (r *HumioClusterGroupReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
