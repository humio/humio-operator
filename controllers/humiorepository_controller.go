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

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioRepositoryReconciler reconciles a HumioRepository object
type HumioRepositoryReconciler struct {
	client.Client
	Log         logr.Logger
	HumioClient humio.Client
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humiorepositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humiorepositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humiorepositories/finalizers,verbs=update

func (r *HumioRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	zapLog, _ := helpers.NewLogger()
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioRepository", logFieldFunctionName, helpers.GetCurrentFuncName())

	// Fetch the HumioRepository instance
	hr := &humiov1alpha1.HumioRepository{}
	err := r.Get(ctx, req.NamespacedName, hr)
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

	r.Log.Info("Checking if repository is marked to be deleted", logFieldFunctionName, helpers.GetCurrentFuncName())
	// Check if the HumioRepository instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioRepositoryMarkedToBeDeleted := hr.GetDeletionTimestamp() != nil
	if isHumioRepositoryMarkedToBeDeleted {
		r.Log.Info("Repository marked to be deleted", logFieldFunctionName, helpers.GetCurrentFuncName())
		if helpers.ContainsElement(hr.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Repository contains finalizer so run finalizer method", logFieldFunctionName, helpers.GetCurrentFuncName())
			if err := r.finalize(ctx, hr); err != nil {
				r.Log.Error(err, "Finalizer method returned error", logFieldFunctionName, helpers.GetCurrentFuncName())
				return reconcile.Result{}, err
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.Log.Info("Finalizer done. Removing finalizer", logFieldFunctionName, helpers.GetCurrentFuncName())
			hr.SetFinalizers(helpers.RemoveElement(hr.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, hr)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully", logFieldFunctionName, helpers.GetCurrentFuncName())
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hr.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to repository", logFieldFunctionName, helpers.GetCurrentFuncName())
		if err := r.addFinalizer(ctx, hr); err != nil {
			return reconcile.Result{}, err
		}
	}

	cluster, err := helpers.NewCluster(ctx, r, hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName, hr.Namespace, helpers.UseCertManager())
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config", logFieldFunctionName, helpers.GetCurrentFuncName())
		err = r.setState(ctx, humiov1alpha1.HumioRepositoryStateConfigError, hr)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	defer func(ctx context.Context, humioClient humio.Client, hr *humiov1alpha1.HumioRepository) {
		curRepository, err := humioClient.GetRepository(hr)
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioRepositoryStateUnknown, hr)
			return
		}
		emptyRepository := humioapi.Parser{}
		if reflect.DeepEqual(emptyRepository, *curRepository) {
			_ = r.setState(ctx, humiov1alpha1.HumioRepositoryStateNotFound, hr)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioRepositoryStateExists, hr)
	}(ctx, r.HumioClient, hr)

	r.HumioClient.SetHumioClientConfig(cluster.Config(), req)

	// Get current repository
	r.Log.Info("get current repository", logFieldFunctionName, helpers.GetCurrentFuncName())
	curRepository, err := r.HumioClient.GetRepository(hr)
	if err != nil {
		r.Log.Error(err, "could not check if repository exists", logFieldFunctionName, helpers.GetCurrentFuncName())
		return reconcile.Result{}, fmt.Errorf("could not check if repository exists: %s", err)
	}

	emptyRepository := humioapi.Repository{}
	if reflect.DeepEqual(emptyRepository, *curRepository) {
		r.Log.Info("repository doesn't exist. Now adding repository", logFieldFunctionName, helpers.GetCurrentFuncName())
		// create repository
		_, err := r.HumioClient.AddRepository(hr)
		if err != nil {
			r.Log.Error(err, "could not create repository", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, fmt.Errorf("could not create repository: %s", err)
		}
		r.Log.Info("created repository", "Repository.Name", hr.Spec.Name, logFieldFunctionName, helpers.GetCurrentFuncName())
		return reconcile.Result{Requeue: true}, nil
	}

	if (curRepository.Description != hr.Spec.Description) ||
		(curRepository.RetentionDays != float64(hr.Spec.Retention.TimeInDays)) ||
		(curRepository.IngestRetentionSizeGB != float64(hr.Spec.Retention.IngestSizeInGB)) ||
		(curRepository.StorageRetentionSizeGB != float64(hr.Spec.Retention.StorageSizeInGB)) {
		r.Log.Info(fmt.Sprintf("repository information differs, triggering update, expected %v/%v/%v/%v, got: %v/%v/%v/%v",
			hr.Spec.Description,
			float64(hr.Spec.Retention.TimeInDays),
			float64(hr.Spec.Retention.IngestSizeInGB),
			float64(hr.Spec.Retention.StorageSizeInGB),
			curRepository.Description,
			curRepository.RetentionDays,
			curRepository.IngestRetentionSizeGB,
			curRepository.StorageRetentionSizeGB), logFieldFunctionName, helpers.GetCurrentFuncName())
		_, err = r.HumioClient.UpdateRepository(hr)
		if err != nil {
			r.Log.Error(err, "could not update repository", logFieldFunctionName, helpers.GetCurrentFuncName())
			return reconcile.Result{}, fmt.Errorf("could not update repository: %s", err)
		}
	}

	// TODO: handle updates to repositoryName. Right now we just create the new repository,
	// and "leak/leave behind" the old repository.
	// A solution could be to add an annotation that includes the "old name" so we can see if it was changed.
	// A workaround for now is to delete the repository CR and create it again.

	r.Log.Info("done reconciling, will requeue after 15 seconds", logFieldFunctionName, helpers.GetCurrentFuncName())
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioRepository{}).
		Complete(r)
}

func (r *HumioRepositoryReconciler) finalize(ctx context.Context, hr *humiov1alpha1.HumioRepository) error {
	_, err := helpers.NewCluster(ctx, r, hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName, hr.Namespace, helpers.UseCertManager())
	if errors.IsNotFound(err) {
		return nil
	}

	return r.HumioClient.DeleteRepository(hr)
}

func (r *HumioRepositoryReconciler) addFinalizer(ctx context.Context, hr *humiov1alpha1.HumioRepository) error {
	r.Log.Info("Adding Finalizer for the HumioRepository", logFieldFunctionName, helpers.GetCurrentFuncName())
	hr.SetFinalizers(append(hr.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(ctx, hr)
	if err != nil {
		r.Log.Error(err, "Failed to update HumioRepository with finalizer", logFieldFunctionName, helpers.GetCurrentFuncName())
		return err
	}
	return nil
}

func (r *HumioRepositoryReconciler) setState(ctx context.Context, state string, hr *humiov1alpha1.HumioRepository) error {
	if hr.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting repository state to %s", state), logFieldFunctionName, helpers.GetCurrentFuncName())
	hr.Status.State = state
	return r.Status().Update(ctx, hr)
}
