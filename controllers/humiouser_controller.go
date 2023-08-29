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
	"time"

	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioUserReconciler reconciles a HumioUser object
type HumioUserReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humiousers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humiousers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humiousers/finalizers,verbs=update

func (r *HumioUserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioUser")

	// Fetch the HumioUser instance
	hu := &humiov1alpha1.HumioUser{}
	err := r.Get(ctx, req.NamespacedName, hu)
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

	cluster, err := helpers.NewCluster(ctx, r, hu.Spec.ManagedClusterName, hu.Spec.ExternalClusterName, hu.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		err = r.setState(ctx, humiov1alpha1.HumioUserStateConfigError, hu)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: time.Second * 15}, nil
	}

	r.Log.Info("Checking if user is marked to be deleted")
	// Check if the HumioUser instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioUserMarkedToBeDeleted := hu.GetDeletionTimestamp() != nil
	if isHumioUserMarkedToBeDeleted {
		r.Log.Info("User marked to be deleted")
		if helpers.ContainsElement(hu.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("User contains finalizer so run finalizer method")
			if err := r.finalize(ctx, cluster.Config(), req, hu); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.Log.Info("Finalizer done. Removing finalizer")
			hu.SetFinalizers(helpers.RemoveElement(hu.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, hu)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hu.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to user")
		if err := r.addFinalizer(ctx, hu); err != nil {
			return reconcile.Result{}, err
		}
	}

	defer func(ctx context.Context, humioClient humio.Client, hu *humiov1alpha1.HumioUser) {
		if hu.Status.State == humiov1alpha1.HumioAlertStateConfigError {
			return
		}
		curUser, err := humioClient.GetUser(cluster.Config(), req, hu)
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioUserStateUnknown, hu)
			return
		}
		emptyUser := humioapi.User{}
		if reflect.DeepEqual(emptyUser, *curUser) {
			_ = r.setState(ctx, humiov1alpha1.HumioUserStateNotFound, hu)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioUserStateExists, hu)
	}(ctx, r.HumioClient, hu)

	// Get current user
	r.Log.Info("get current user")
	curUser, err := r.HumioClient.GetUser(cluster.Config(), req, hu)
	emptyUser := humioapi.User{}

	// if this is a new user check that the username doesn't exist in humio first
	if (len(hu.Status.State) == 0) && (*curUser != emptyUser) {
		r.Log.Info("No state for user but user exists in Humio")
		err = r.setState(ctx, humiov1alpha1.HumioUserStateConfigError, hu)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set user state")
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not create user resource, username exists")

	}
	if emptyUser == *curUser {
		r.Log.Info("user doesn't exist. Now adding user")
		// create user
		_, err := r.HumioClient.AddUser(cluster.Config(), req, hu)
		if err != nil {
			err = r.setState(ctx, humiov1alpha1.HumioUserStateConfigError, hu)
			if err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set user state")
			}
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create user")
		}
		r.Log.Info("created user", "Username", hu.Spec.Username)
		return reconcile.Result{Requeue: true}, nil

	}

	if (curUser.FullName != hu.Spec.FullName) ||
		(curUser.Email != hu.Spec.Email) ||
		(curUser.Company != hu.Spec.Company) ||
		(curUser.CountryCode != hu.Spec.CountryCode) ||
		(curUser.Picture != hu.Spec.Picture) ||
		(curUser.IsRoot != bool(hu.Spec.IsRoot)) {
		r.Log.Info(fmt.Sprintf("user information differs, triggering update, expected %v/%v/%v/%v/%v/%v/%v, got: %v/%v/%v/%v/%v/%v/%v",
			hu.Spec.Username,
			hu.Spec.FullName,
			hu.Spec.Email,
			hu.Spec.Company,
			hu.Spec.CountryCode,
			hu.Spec.Picture,
			hu.Spec.IsRoot,
			curUser.Username,
			curUser.FullName,
			curUser.Email,
			curUser.Company,
			curUser.CountryCode,
			curUser.Picture,
			curUser.IsRoot))
		_, err = r.HumioClient.UpdateUser(cluster.Config(), req, hu)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update user")
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioUser{}).
		Complete(r)
}

func (r *HumioUserReconciler) finalize(ctx context.Context, config *humioapi.Config, req reconcile.Request, hu *humiov1alpha1.HumioUser) error {
	_, err := helpers.NewCluster(ctx, r, hu.Spec.ManagedClusterName, hu.Spec.ExternalClusterName, hu.Namespace, helpers.UseCertManager(), true)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return r.HumioClient.DeleteUser(config, req, hu)
}

func (r *HumioUserReconciler) addFinalizer(ctx context.Context, hu *humiov1alpha1.HumioUser) error {
	r.Log.Info("Adding Finalizer for the HumioUser")
	hu.SetFinalizers(append(hu.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(ctx, hu)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioUser with finalizer")
	}
	return nil
}

func (r *HumioUserReconciler) setState(ctx context.Context, state string, hu *humiov1alpha1.HumioUser) error {
	if hu.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting user state to %s", state))
	hu.Status.State = state
	return r.Status().Update(ctx, hu)
}

func (r *HumioUserReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
