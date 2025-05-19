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

// HumioViewPermissionRoleReconciler reconciles a HumioViewPermissionRole object
type HumioViewPermissionRoleReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewpermissionroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewpermissionroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewpermissionroles/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioViewPermissionRoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioViewPermissionRole")

	// Fetch the HumioViewPermissionRole instance
	hp := &humiov1alpha1.HumioViewPermissionRole{}
	err := r.Get(ctx, req.NamespacedName, hp)
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

	r.Log = r.Log.WithValues("Request.UID", hp.UID)

	cluster, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioViewPermissionRoleStateConfigError, hp)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	r.Log.Info("Checking if viewPermissionRole is marked to be deleted")
	// Check if the HumioViewPermissionRole instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioViewPermissionRoleMarkedToBeDeleted := hp.GetDeletionTimestamp() != nil
	if isHumioViewPermissionRoleMarkedToBeDeleted {
		r.Log.Info("ViewPermissionRole marked to be deleted")
		if helpers.ContainsElement(hp.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetViewPermissionRole(ctx, humioHttpClient, hp)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hp)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("ViewPermissionRole contains finalizer so run finalizer method")
			if err := r.finalize(ctx, humioHttpClient, hp); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
			}
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hp.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to viewPermissionRole")
		if err := r.addFinalizer(ctx, hp); err != nil {
			return reconcile.Result{}, err
		}
	}

	defer func(ctx context.Context, humioClient humio.Client, hp *humiov1alpha1.HumioViewPermissionRole) {
		_, err := humioClient.GetViewPermissionRole(ctx, humioHttpClient, hp)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioViewPermissionRoleStateNotFound, hp)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioViewPermissionRoleStateUnknown, hp)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioViewPermissionRoleStateExists, hp)
	}(ctx, r.HumioClient, hp)

	// Get current viewPermissionRole
	r.Log.Info("get current viewPermissionRole")
	curViewPermissionRole, err := r.HumioClient.GetViewPermissionRole(ctx, humioHttpClient, hp)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("viewPermissionRole doesn't exist. Now adding viewPermissionRole")
			// create viewPermissionRole
			addErr := r.HumioClient.AddViewPermissionRole(ctx, humioHttpClient, hp)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create viewPermissionRole")
			}
			r.Log.Info("created viewPermissionRole")
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if viewPermissionRole exists")
	}

	if asExpected, diffKeysAndValues := viewPermissionRoleAlreadyAsExpected(hp, curViewPermissionRole); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		err = r.HumioClient.UpdateViewPermissionRole(ctx, humioHttpClient, hp)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update viewPermissionRole")
		}
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioViewPermissionRoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioViewPermissionRole{}).
		Named("humioviewpermissionrole").
		Complete(r)
}

func (r *HumioViewPermissionRoleReconciler) finalize(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioViewPermissionRole) error {
	_, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return r.HumioClient.DeleteViewPermissionRole(ctx, client, hp)
}

func (r *HumioViewPermissionRoleReconciler) addFinalizer(ctx context.Context, hp *humiov1alpha1.HumioViewPermissionRole) error {
	r.Log.Info("Adding Finalizer for the HumioViewPermissionRole")
	hp.SetFinalizers(append(hp.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(ctx, hp)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioViewPermissionRole with finalizer")
	}
	return nil
}

func (r *HumioViewPermissionRoleReconciler) setState(ctx context.Context, state string, hp *humiov1alpha1.HumioViewPermissionRole) error {
	if hp.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting viewPermissionRole state to %s", state))
	hp.Status.State = state
	return r.Status().Update(ctx, hp)
}

func (r *HumioViewPermissionRoleReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// viewPermissionRoleAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func viewPermissionRoleAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioViewPermissionRole, fromGraphQL *humiographql.RoleDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetDisplayName(), fromKubernetesCustomResource.Spec.Name); diff != "" {
		keyValues["name"] = diff
	}
	permissionsFromGraphQL := fromGraphQL.GetViewPermissions()
	viewPermissionsToStrings := make([]string, len(permissionsFromGraphQL))
	for idx := range permissionsFromGraphQL {
		viewPermissionsToStrings[idx] = string(permissionsFromGraphQL[idx])
	}
	sort.Strings(viewPermissionsToStrings)
	sort.Strings(fromKubernetesCustomResource.Spec.Permissions)
	if diff := cmp.Diff(viewPermissionsToStrings, fromKubernetesCustomResource.Spec.Permissions); diff != "" {
		keyValues["permissions"] = diff
	}

	return len(keyValues) == 0, keyValues
}
