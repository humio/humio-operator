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
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Condition types for HumioSavedQuery
const (
	SavedQueryConditionTypeReady  = "Ready"
	SavedQueryConditionTypeSynced = "Synced"
)

// Condition reasons for HumioSavedQuery
const (
	SavedQueryReasonSavedQueryExists    = "SavedQueryExists"
	SavedQueryReasonSavedQueryNotFound  = "SavedQueryNotFound"
	SavedQueryReasonConfigurationError  = "ConfigurationError"
	SavedQueryReasonConfigurationSynced = "ConfigurationSynced"
	SavedQueryReasonConfigurationDrift  = "ConfigurationDrift"
	SavedQueryReasonSyncFailed          = "SyncFailed"
)

// HumioSavedQueryReconciler reconciles a HumioSavedQuery object
type HumioSavedQueryReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiosavedqueries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiosavedqueries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiosavedqueries/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioSavedQueryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioSavedQuery")

	hsq := &humiov1alpha1.HumioSavedQuery{}
	err := r.Get(ctx, req.NamespacedName, hsq)
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

	r.Log = r.Log.WithValues("Request.UID", hsq.UID)

	cluster, err := helpers.NewCluster(ctx, r, hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName, hsq.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionFalse, SavedQueryReasonConfigurationError, fmt.Sprintf("Unable to obtain Humio client config: %v", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(ctx context.Context, hsq *humiov1alpha1.HumioSavedQuery) {
		// Don't update status if resource is being deleted
		if hsq.GetDeletionTimestamp() != nil {
			return
		}

		_, err := r.HumioClient.GetSavedQuery(ctx, humioHttpClient, hsq)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			if setErr := r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionFalse, SavedQueryReasonSavedQueryNotFound, "Saved query not found in LogScale"); setErr != nil {
				r.Log.Error(setErr, "Failed to update Ready condition in defer block")
			}
			return
		}
		if err != nil {
			if setErr := r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionUnknown, SavedQueryReasonConfigurationError, fmt.Sprintf("Failed to get saved query: %v", err)); setErr != nil {
				r.Log.Error(setErr, "Failed to update Ready condition in defer block")
			}
			return
		}
		if setErr := r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionTrue, SavedQueryReasonSavedQueryExists, "Saved query exists in LogScale"); setErr != nil {
			r.Log.Error(setErr, "Failed to update Ready condition in defer block")
		}
	}(ctx, hsq)

	return r.reconcileHumioSavedQuery(ctx, humioHttpClient, hsq)
}

func (r *HumioSavedQueryReconciler) reconcileHumioSavedQuery(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if saved query is marked to be deleted")
	if hsq.GetDeletionTimestamp() != nil {
		r.Log.Info("Saved query marked to be deleted")
		if helpers.ContainsElement(hsq.GetFinalizers(), HumioFinalizer) {
			_, err := r.HumioClient.GetSavedQuery(ctx, client, hsq)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hsq.SetFinalizers(helpers.RemoveElement(hsq.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hsq)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting saved query")
			if err := r.HumioClient.DeleteSavedQuery(ctx, client, hsq); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete saved query returned error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if saved query requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(hsq.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to saved query")
		hsq.SetFinalizers(append(hsq.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hsq)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if saved query needs to be created")
	// Get current state
	curSavedQuery, err := r.HumioClient.GetSavedQuery(ctx, client, hsq)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		r.Log.Info("Saved query doesn't exist. Creating saved query")
		if err := r.HumioClient.AddSavedQuery(ctx, client, hsq); err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create saved query")
		}
		r.Log.Info("Created saved query")
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionTrue, SavedQueryReasonConfigurationSynced, "Saved query created")
		return reconcile.Result{Requeue: true}, nil
	}
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if saved query exists")
	}

	r.Log.Info("Checking if saved query needs to be updated")
	expectedAsActual, diff := savedQueryAlreadyAsExpected(hsq, curSavedQuery)
	if !expectedAsActual {
		r.Log.Info("Information differs, triggering update", "diff", diff)
		err = r.HumioClient.UpdateSavedQuery(ctx, client, hsq)
		if err != nil {
			_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionFalse, SavedQueryReasonSyncFailed, fmt.Sprintf("Failed to update saved query: %v", err))
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update saved query")
		}
		r.Log.Info("Updated saved query")
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionTrue, SavedQueryReasonConfigurationSynced, "Saved query updated")
		return reconcile.Result{Requeue: true}, nil
	}

	// Configuration is synced
	_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionTrue, SavedQueryReasonConfigurationSynced, "Configuration matches desired state")

	// Everything is good, requeue after standard interval
	r.Log.Info("Done reconciling, will requeue after standard interval")
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

func (r *HumioSavedQueryReconciler) setCondition(ctx context.Context, hsq *humiov1alpha1.HumioSavedQuery, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	// Fetch latest version to avoid conflicts
	latest := &humiov1alpha1.HumioSavedQuery{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(hsq), latest); err != nil {
		return err
	}

	// Only update if the condition has changed (including generation)
	existingCondition := meta.FindStatusCondition(latest.Status.Conditions, conditionType)
	if existingCondition != nil &&
		existingCondition.Status == status &&
		existingCondition.Reason == reason &&
		existingCondition.Message == message &&
		existingCondition.ObservedGeneration == latest.Generation {
		return nil
	}

	meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: latest.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})
	return r.Status().Update(ctx, latest)
}

func (r *HumioSavedQueryReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioSavedQueryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioSavedQuery{}).
		Complete(r)
}

func savedQueryAlreadyAsExpected(fromKubernetes *humiov1alpha1.HumioSavedQuery, fromGraphQL *humiographql.SavedQueryDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	// Compare description (normalize nil vs empty string)
	graphQLDescription := ""
	if fromGraphQL.Description != nil {
		graphQLDescription = *fromGraphQL.Description
	}
	if diff := cmp.Diff(graphQLDescription, fromKubernetes.Spec.Description); diff != "" {
		keyValues["description"] = diff
	}

	// Compare query string
	if diff := cmp.Diff(fromGraphQL.Query.QueryString, fromKubernetes.Spec.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}

	// Compare labels (create defensive copies before sorting to avoid mutation)
	labelsFromGraphQL := make([]string, len(fromGraphQL.Labels))
	copy(labelsFromGraphQL, fromGraphQL.Labels)
	sort.Strings(labelsFromGraphQL)

	labelsFromKubernetes := make([]string, len(fromKubernetes.Spec.Labels))
	copy(labelsFromKubernetes, fromKubernetes.Spec.Labels)
	sort.Strings(labelsFromKubernetes)

	if diff := cmp.Diff(labelsFromGraphQL, labelsFromKubernetes); diff != "" {
		keyValues["labels"] = diff
	}

	return len(keyValues) == 0, keyValues
}
