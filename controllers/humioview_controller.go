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
	"sort"
	"time"

	"github.com/go-logr/logr"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioViewReconciler reconciles a HumioView object
type HumioViewReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioviews,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioviews/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioviews/finalizers,verbs=update

func (r *HumioViewReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioView")

	// Fetch the HumioView instance
	hv := &humiov1alpha1.HumioView{}
	err := r.Get(ctx, req.NamespacedName, hv)
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

	cluster, err := helpers.NewCluster(ctx, r, hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName, hv.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		err = r.setState(ctx, humiov1alpha1.HumioParserStateConfigError, hv)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: time.Second * 15}, nil
	}

	defer func(ctx context.Context, humioClient humio.Client, hv *humiov1alpha1.HumioView) {
		curView, err := r.HumioClient.GetView(cluster.Config(), req, hv)
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

	r.Log.Info("get current view")
	curView, err := r.HumioClient.GetView(cluster.Config(), req, hv)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if view exists")
	}

	return r.reconcileHumioView(ctx, cluster.Config(), curView, hv, req)
}

func (r *HumioViewReconciler) reconcileHumioView(ctx context.Context, config *humioapi.Config, curView *humioapi.View, hv *humiov1alpha1.HumioView, req reconcile.Request) (reconcile.Result, error) {
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
			if err := r.HumioClient.DeleteView(config, req, hv); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete view returned error")
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
		_, err := r.HumioClient.AddView(config, req, hv)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create view")
		}
		r.Log.Info("created view", "ViewName", hv.Spec.Name)
		return reconcile.Result{Requeue: true}, nil
	}

	// Update View connections
	if viewConnectionsDiffer(curView.Connections, hv.GetViewConnections()) {
		r.Log.Info(fmt.Sprintf("view information differs, triggering update, expected %v, got: %v",
			hv.Spec.Connections,
			curView.Connections))
		_, err := r.HumioClient.UpdateView(config, req, hv)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update view")
		}
	}

	// Update View description
	if viewDescriptionDiffer(curView.Description, hv.Description) {
		r.Log.Info(fmt.Stringf("View description differs, triggering update."))
		_, err := r.HumioClient.UpdateView(config, req, hv)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update view")
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

// viewDescriptionDiffer returns whether view's description differ.
func viewDescriptionDiffer(curDescription, newDescription string) bool {
	if curDescription != newDescription {
		return true
	}

	return false
}

// viewConnectionsDiffer returns whether two slices of connections differ.
// Connections are compared by repo name and filter so the ordering is not taken
// into account.
func viewConnectionsDiffer(curConnections, newConnections []humioapi.ViewConnection) bool {
	if len(curConnections) != len(newConnections) {
		return true
	}
	// sort the slices to avoid changes to the order of items in the slice to
	// trigger an update. Kubernetes does not guarantee that slice items are
	// deterministic ordered, so without this we could trigger updates to views
	// without any functional changes. As the result of a view update in Humio is
	// live queries against it are refreshed it can lead to dashboards and queries
	// refreshing all the time.
	sortConnections(curConnections)
	sortConnections(newConnections)

	for i := range curConnections {
		if curConnections[i] != newConnections[i] {
			return true
		}
	}

	return false
}

func sortConnections(connections []humioapi.ViewConnection) {
	sort.SliceStable(connections, func(i, j int) bool {
		return connections[i].RepoName > connections[j].RepoName || connections[i].Filter > connections[j].Filter
	})
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

func (r *HumioViewReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
