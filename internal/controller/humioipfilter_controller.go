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

package controller

import (
	"context"
	"errors"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioIPFilterReconciler reconciles a HumioIPFilter object
type HumioIPFilterReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioipfilters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioipfilters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioipfilters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioIPFilterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioIPFilter")

	// reading k8s object
	hi := &humiov1alpha1.HumioIPFilter{}
	err := r.Get(ctx, req.NamespacedName, hi)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// setup humio client configuration
	cluster, err := helpers.NewCluster(ctx, r, hi.Spec.ManagedClusterName, hi.Spec.ExternalClusterName, hi.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioIPFilterStateConfigError, hi.Status.ID, hi)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}

	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// handle delete logic
	isHumioIPFilterMarkedToBeDeleted := hi.GetDeletionTimestamp() != nil
	if isHumioIPFilterMarkedToBeDeleted {
		r.Log.Info("IPFilter marked to be deleted")
		if helpers.ContainsElement(hi.GetFinalizers(), HumioFinalizer) {
			_, err := r.HumioClient.GetIPFilter(ctx, humioHttpClient, hi)
			// first iteration on delete we don't enter here since IPFilter exists
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hi.SetFinalizers(helpers.RemoveElement(hi.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hi)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}
			// first iteration on delete we run the finalize function which includes delete
			r.Log.Info("IPFilter contains finalizer so run finalizer method")
			if err := r.finalize(ctx, humioHttpClient, hi); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for IPFilter so we can run cleanup on delete
	if !helpers.ContainsElement(hi.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to IPFilter")
		if err := r.addFinalizer(ctx, hi); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Get or create IPFilter
	r.Log.Info("get current IPFilter")
	curIPfilter, err := r.HumioClient.GetIPFilter(ctx, humioHttpClient, hi)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("IPFilter doesn't exist. Now adding IPFilter")
			ipFilterDetails, addErr := r.HumioClient.AddIPFilter(ctx, humioHttpClient, hi)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create IPFilter")
			}
			r.Log.Info("created IPFilter")
			err = r.setState(ctx, humiov1alpha1.HumioIPFilterStateExists, ipFilterDetails.Id, hi)
			if err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "could not update IPFilter Status")
			}
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if IPFilter exists")
	}

	// check diffs and update
	if asExpected, diffKeysAndValues := ipFilterAlreadyAsExpected(hi, curIPfilter); !asExpected {
		r.Log.Info("information differs, triggering update", "diff", diffKeysAndValues)
		err = r.HumioClient.UpdateIPFilter(ctx, humioHttpClient, hi)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update IPFilter")
		}
	}

	// final state update
	ipFilter, err := r.HumioClient.GetIPFilter(ctx, humioHttpClient, hi)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		_ = r.setState(ctx, humiov1alpha1.HumioIPFilterStateNotFound, hi.Status.ID, hi)
	} else if err != nil {
		_ = r.setState(ctx, humiov1alpha1.HumioIPFilterStateUnknown, hi.Status.ID, hi)
	} else {
		_ = r.setState(ctx, humiov1alpha1.HumioIPFilterStateExists, ipFilter.Id, hi)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioIPFilterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioIPFilter{}).
		Named("humioipfilter").
		Complete(r)
}

func (r *HumioIPFilterReconciler) finalize(ctx context.Context, client *humioapi.Client, hi *humiov1alpha1.HumioIPFilter) error {
	if hi.Status.ID == "" {
		// ipFIlter ID not set, unexpected but we should not err
		return nil
	}
	err := r.HumioClient.DeleteIPFilter(ctx, client, hi)
	if err != nil {
		return r.logErrorAndReturn(err, "error in finalize function call")
	}
	return nil
}

func (r *HumioIPFilterReconciler) addFinalizer(ctx context.Context, hi *humiov1alpha1.HumioIPFilter) error {
	r.Log.Info("Adding Finalizer for the HumioIPFilter")
	hi.SetFinalizers(append(hi.GetFinalizers(), HumioFinalizer))

	err := r.Update(ctx, hi)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioIPFilter with finalizer")
	}
	return nil
}

func (r *HumioIPFilterReconciler) setState(ctx context.Context, state string, id string, hi *humiov1alpha1.HumioIPFilter) error {
	if hi.Status.State == state && hi.Status.ID == id {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting IPFilter state to %s", state))
	hi.Status.State = state
	hi.Status.ID = id
	return r.Status().Update(ctx, hi)
}

func (r *HumioIPFilterReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// ipFilterAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL.
func ipFilterAlreadyAsExpected(fromK8sCR *humiov1alpha1.HumioIPFilter, fromGraphQL *humiographql.IPFilterDetails) (bool, map[string]string) {
	keyValues := map[string]string{}
	// we only care about ipFilter field
	fromGql := fromGraphQL.GetIpFilter()
	fromK8s := helpers.FirewallRulesToString(fromK8sCR.Spec.IPFilter, "\n")
	if diff := cmp.Diff(fromGql, fromK8s); diff != "" {
		keyValues["ipFilter"] = diff
	}
	return len(keyValues) == 0, keyValues
}
