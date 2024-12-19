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
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
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

	r.Log = r.Log.WithValues("Request.UID", hv.UID)

	cluster, err := helpers.NewCluster(ctx, r, hv.Spec.ManagedClusterName, hv.Spec.ExternalClusterName, hv.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioParserStateConfigError, hv)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Delete
	r.Log.Info("Checking if view is marked to be deleted")
	isMarkedForDeletion := hv.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("View marked to be deleted")
		if helpers.ContainsElement(hv.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetView(ctx, humioHttpClient, req, hv)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hv.SetFinalizers(helpers.RemoveElement(hv.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hv)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting View")
			if err := r.HumioClient.DeleteView(ctx, humioHttpClient, req, hv); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete view returned error")
			}
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
	defer func(ctx context.Context, humioClient humio.Client, hv *humiov1alpha1.HumioView) {
		_, err := r.HumioClient.GetView(ctx, humioHttpClient, req, hv)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioViewStateNotFound, hv)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioViewStateUnknown, hv)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioViewStateExists, hv)
	}(ctx, r.HumioClient, hv)

	r.Log.Info("get current view")
	curView, err := r.HumioClient.GetView(ctx, humioHttpClient, req, hv)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("View doesn't exist. Now adding view")
			addErr := r.HumioClient.AddView(ctx, humioHttpClient, req, hv)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create view")
			}
			r.Log.Info("created view", "ViewName", hv.Spec.Name)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if view exists")
	}

	if asExpected, diffKeysAndValues := viewAlreadyAsExpected(hv, curView); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		updateErr := r.HumioClient.UpdateView(ctx, humioHttpClient, req, hv)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update view")
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
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

// viewAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func viewAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioView, fromGraphQL *humiographql.GetSearchDomainSearchDomainView) (bool, map[string]string) {
	keyValues := map[string]string{}

	currentConnections := fromGraphQL.GetConnections()
	expectedConnections := fromKubernetesCustomResource.GetViewConnections()
	sortConnections(currentConnections)
	sortConnections(expectedConnections)
	if diff := cmp.Diff(currentConnections, expectedConnections); diff != "" {
		keyValues["viewConnections"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetDescription(), &fromKubernetesCustomResource.Spec.Description); diff != "" {
		keyValues["description"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetAutomaticSearch(), helpers.BoolTrue(fromKubernetesCustomResource.Spec.AutomaticSearch)); diff != "" {
		keyValues["automaticSearch"] = diff
	}

	return len(keyValues) == 0, keyValues
}

func sortConnections(connections []humiographql.GetSearchDomainSearchDomainViewConnectionsViewConnection) {
	sort.SliceStable(connections, func(i, j int) bool {
		return connections[i].Repository.Name > connections[j].Repository.Name || connections[i].Filter > connections[j].Filter
	})
}
