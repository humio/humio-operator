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
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioRepositoryReconciler reconciles a HumioRepository object
type HumioRepositoryReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiorepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiorepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiorepositories/finalizers,verbs=update

func (r *HumioRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioRepository")

	// Fetch the HumioRepository instance
	hr := &humiov1alpha1.HumioRepository{}
	err := r.Get(ctx, req.NamespacedName, hr)
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

	r.Log = r.Log.WithValues("Request.UID", hr.UID)

	cluster, err := helpers.NewCluster(ctx, r, hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName, hr.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioRepositoryStateConfigError, hr)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	r.Log.Info("Checking if repository is marked to be deleted")
	// Check if the HumioRepository instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioRepositoryMarkedToBeDeleted := hr.GetDeletionTimestamp() != nil
	if isHumioRepositoryMarkedToBeDeleted {
		r.Log.Info("Repository marked to be deleted")
		if helpers.ContainsElement(hr.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetRepository(ctx, humioHttpClient, hr)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hr.SetFinalizers(helpers.RemoveElement(hr.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hr)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Repository contains finalizer so run finalizer method")
			if err := r.finalize(ctx, humioHttpClient, hr); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hr.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to repository")
		if err := r.addFinalizer(ctx, hr); err != nil {
			return reconcile.Result{}, err
		}
	}

	defer func(ctx context.Context, humioClient humio.Client, hr *humiov1alpha1.HumioRepository) {
		_, err := humioClient.GetRepository(ctx, humioHttpClient, hr)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioRepositoryStateNotFound, hr)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioRepositoryStateUnknown, hr)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioRepositoryStateExists, hr)
	}(ctx, r.HumioClient, hr)

	// Get current repository
	r.Log.Info("get current repository")
	curRepository, err := r.HumioClient.GetRepository(ctx, humioHttpClient, hr)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("repository doesn't exist. Now adding repository")
			// create repository
			addErr := r.HumioClient.AddRepository(ctx, humioHttpClient, hr)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create repository")
			}
			r.Log.Info("created repository", "RepositoryName", hr.Spec.Name)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if repository exists")
	}

	if asExpected, diffKeysAndValues := repositoryAlreadyAsExpected(hr, curRepository); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		err = r.HumioClient.UpdateRepository(ctx, humioHttpClient, hr)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update repository")
		}
	}

	// TODO: handle updates to repositoryName. Right now we just create the new repository,
	// and "leak/leave behind" the old repository.
	// A solution could be to add an annotation that includes the "old name" so we can see if it was changed.
	// A workaround for now is to delete the repository CR and create it again.

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioRepository{}).
		Named("humiorepository").
		Complete(r)
}

func (r *HumioRepositoryReconciler) finalize(ctx context.Context, client *humioapi.Client, hr *humiov1alpha1.HumioRepository) error {
	_, err := helpers.NewCluster(ctx, r, hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName, hr.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return r.HumioClient.DeleteRepository(ctx, client, hr)
}

func (r *HumioRepositoryReconciler) addFinalizer(ctx context.Context, hr *humiov1alpha1.HumioRepository) error {
	r.Log.Info("Adding Finalizer for the HumioRepository")
	hr.SetFinalizers(append(hr.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(ctx, hr)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioRepository with finalizer")
	}
	return nil
}

func (r *HumioRepositoryReconciler) setState(ctx context.Context, state string, hr *humiov1alpha1.HumioRepository) error {
	if hr.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting repository state to %s", state))
	hr.Status.State = state
	return r.Status().Update(ctx, hr)
}

func (r *HumioRepositoryReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// repositoryAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func repositoryAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioRepository, fromGraphQL *humiographql.RepositoryDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetDescription(), &fromKubernetesCustomResource.Spec.Description); diff != "" {
		keyValues["description"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetTimeBasedRetention(), helpers.Int32PtrToFloat64Ptr(fromKubernetesCustomResource.Spec.Retention.TimeInDays)); diff != "" {
		keyValues["timeInDays"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetIngestSizeBasedRetention(), helpers.Int32PtrToFloat64Ptr(fromKubernetesCustomResource.Spec.Retention.IngestSizeInGB)); diff != "" {
		keyValues["ingestSizeInGB"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetStorageSizeBasedRetention(), helpers.Int32PtrToFloat64Ptr(fromKubernetesCustomResource.Spec.Retention.StorageSizeInGB)); diff != "" {
		keyValues["storageSizeInGB"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetAutomaticSearch(), helpers.BoolTrue(fromKubernetesCustomResource.Spec.AutomaticSearch)); diff != "" {
		keyValues["automaticSearch"] = diff
	}

	return len(keyValues) == 0, keyValues
}
