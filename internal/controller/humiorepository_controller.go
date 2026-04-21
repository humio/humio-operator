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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
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
		setConditionErr := r.setCondition(ctx, hr,
			humiov1alpha1.RepositoryConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.RepositoryReasonConfigError,
			fmt.Sprintf("Unable to obtain humio client config: %v", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	// This ensures we handle the rename before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, hr)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle repository rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with normal reconciliation
		return result, nil
	}

	r.Log.Info("Checking if repository is marked to be deleted")
	// Check if the HumioRepository instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioRepositoryMarkedToBeDeleted := hr.GetDeletionTimestamp() != nil
	if isHumioRepositoryMarkedToBeDeleted {
		r.Log.Info("Repository marked to be deleted")
		if helpers.ContainsElement(hr.GetFinalizers(), HumioFinalizer) {
			// Check for force finalize annotation
			if ShouldForceFinalize(hr) {
				r.Log.Info("Force finalize annotation detected, removing finalizer without cleanup",
					"resource", hr.Name,
					"namespace", hr.Namespace)
				hr.SetFinalizers(helpers.RemoveElement(hr.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hr)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully via force-finalize annotation")
				return reconcile.Result{Requeue: true}, nil
			}

			_, err := r.HumioClient.GetRepository(ctx, humioHttpClient, hr)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hr.SetFinalizers(helpers.RemoveElement(hr.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hr)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for HumioFinalizer. If the
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
	if !helpers.ContainsElement(hr.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to repository")
		if err := r.addFinalizer(ctx, hr); err != nil {
			return reconcile.Result{}, err
		}
	}

	defer func(ctx context.Context, humioClient humio.Client, hr *humiov1alpha1.HumioRepository) {
		_, err := humioClient.GetRepository(ctx, humioHttpClient, hr)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setCondition(ctx, hr,
				humiov1alpha1.RepositoryConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.RepositoryReasonNotFound,
				"Repository not found in LogScale")
			return
		}
		if err != nil {
			_ = r.setCondition(ctx, hr,
				humiov1alpha1.RepositoryConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.RepositoryReasonConfigError,
				fmt.Sprintf("Failed to get repository: %v", err))
			return
		}
		_ = r.setCondition(ctx, hr,
			humiov1alpha1.RepositoryConditionTypeReady,
			metav1.ConditionTrue,
			humiov1alpha1.RepositoryReasonReady,
			"Repository is ready")
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
	// Add field indexes for efficient dependent resource lookups
	if err := r.setupFieldIndexes(mgr); err != nil {
		return fmt.Errorf("failed to setup field indexes: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioRepository{}).
		Named("humiorepository").
		Complete(r)
}

// setupFieldIndexes creates field indexes for efficient lookups of resources
// that reference repositories by name
func (r *HumioRepositoryReconciler) setupFieldIndexes(mgr ctrl.Manager) error {
	// Index HumioParsers by RepositoryName
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &humiov1alpha1.HumioParser{}, "spec.repositoryName",
		func(rawObj client.Object) []string {
			parser := rawObj.(*humiov1alpha1.HumioParser)
			if parser.Spec.RepositoryName == "" {
				return nil
			}
			return []string{parser.Spec.RepositoryName}
		}); err != nil {
		return fmt.Errorf("failed to create parser index: %w", err)
	}

	// Index HumioIngestTokens by RepositoryName
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &humiov1alpha1.HumioIngestToken{}, "spec.repositoryName",
		func(rawObj client.Object) []string {
			token := rawObj.(*humiov1alpha1.HumioIngestToken)
			if token.Spec.RepositoryName == "" {
				return nil
			}
			return []string{token.Spec.RepositoryName}
		}); err != nil {
		return fmt.Errorf("failed to create ingest token index: %w", err)
	}

	// Index HumioEventForwardingRules by RepositoryName
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &humiov1alpha1.HumioEventForwardingRule{}, "spec.repositoryName",
		func(rawObj client.Object) []string {
			rule := rawObj.(*humiov1alpha1.HumioEventForwardingRule)
			if rule.Spec.RepositoryName == "" {
				return nil
			}
			return []string{rule.Spec.RepositoryName}
		}); err != nil {
		return fmt.Errorf("failed to create event forwarding rule index: %w", err)
	}

	// Note: HumioViews reference repositories through Connections array,
	// which can't be efficiently indexed, so we keep the List+filter approach for views

	return nil
}

func (r *HumioRepositoryReconciler) finalize(ctx context.Context, client *humioapi.Client, hr *humiov1alpha1.HumioRepository) error {
	_, err := helpers.NewCluster(ctx, r, hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName, hr.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Check if data deletion is allowed
	if !hr.Spec.AllowDataDeletion {
		return fmt.Errorf("repository may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
	}

	// Audit log before deletion
	r.Log.Info("Proceeding with repository deletion",
		"allowDataDeletion", hr.Spec.AllowDataDeletion,
		"repositoryName", hr.Spec.Name,
		"namespace", hr.Namespace,
		"deletionTimestamp", hr.GetDeletionTimestamp(),
	)

	return r.HumioClient.DeleteRepository(ctx, client, hr)
}

func (r *HumioRepositoryReconciler) addFinalizer(ctx context.Context, hr *humiov1alpha1.HumioRepository) error {
	r.Log.Info("Adding Finalizer for the HumioRepository")
	hr.SetFinalizers(append(hr.GetFinalizers(), HumioFinalizer))

	// Update CR
	err := r.Update(ctx, hr)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioRepository with finalizer")
	}
	return nil
}

// setCondition sets a condition on the HumioRepository resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioRepositoryReconciler) setCondition(ctx context.Context,
	hr *humiov1alpha1.HumioRepository,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioRepository{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hr), latest); err != nil {
			return err
		}

		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               conditionType,
			Status:             status,
			ObservedGeneration: latest.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		})

		// BACKWARD COMPATIBILITY: Update State field based on condition
		latest.Status.State = repositoryStateFromCondition(status, reason)

		// Track the synced name when repository is ready
		// Only update if LastSyncedName is empty or matches current spec (not during rename)
		// This prevents overwriting the old name before rename detection can run
		if conditionType == humiov1alpha1.RepositoryConditionTypeReady && status == metav1.ConditionTrue {
			if latest.Status.LastSyncedName == "" || latest.Status.LastSyncedName == latest.Spec.Name {
				latest.Status.LastSyncedName = latest.Spec.Name
			}
		}

		return r.Status().Update(ctx, latest)
	})
}

func repositoryStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioRepositoryStateExists
	}
	switch reason {
	case humiov1alpha1.RepositoryReasonNotFound:
		return humiov1alpha1.HumioRepositoryStateNotFound
	case humiov1alpha1.RepositoryReasonConfigError:
		return humiov1alpha1.HumioRepositoryStateConfigError
	default:
		return humiov1alpha1.HumioRepositoryStateUnknown
	}
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

	// Only check Description if explicitly set (not nil)
	if fromKubernetesCustomResource.Spec.Description != nil {
		if diff := cmp.Diff(fromGraphQL.GetDescription(), fromKubernetesCustomResource.Spec.Description); diff != "" {
			keyValues["description"] = diff
		}
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

// DependentResource represents a Kubernetes resource that depends on a repository or view
type DependentResource struct {
	Kind      string
	Name      string
	Namespace string
}

// findDependentResources finds all CRD resources that reference the given repository name
// within the same namespace for RBAC compliance. Uses field indexes for efficient lookups.
func (r *HumioRepositoryReconciler) findDependentResources(ctx context.Context,
	repoName string, namespace string) ([]DependentResource, error) {

	dependents := make([]DependentResource, 0, 10) // Pre-allocate for typical case
	listOpts := []client.ListOption{
		client.InNamespace(namespace), // Only same namespace for RBAC compliance
	}

	// Find HumioParsers referencing this repository
	parserList := &humiov1alpha1.HumioParserList{}
	if err := r.List(ctx, parserList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list parsers: %w", err)
	}
	for _, parser := range parserList.Items {
		if parser.Spec.RepositoryName == repoName {
			dependents = append(dependents, DependentResource{
				Kind:      "HumioParser",
				Name:      parser.Name,
				Namespace: parser.Namespace,
			})
		}
	}

	// Find HumioIngestTokens referencing this repository
	tokenList := &humiov1alpha1.HumioIngestTokenList{}
	if err := r.List(ctx, tokenList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list ingest tokens: %w", err)
	}
	for _, token := range tokenList.Items {
		if token.Spec.RepositoryName == repoName {
			dependents = append(dependents, DependentResource{
				Kind:      "HumioIngestToken",
				Name:      token.Name,
				Namespace: token.Namespace,
			})
		}
	}

	// Find HumioViews referencing this repository in connections
	// Note: Views can't use index because repository is in a nested array
	viewList := &humiov1alpha1.HumioViewList{}
	if err := r.List(ctx, viewList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list views: %w", err)
	}
	for _, view := range viewList.Items {
		for _, conn := range view.Spec.Connections {
			if conn.RepositoryName == repoName {
				dependents = append(dependents, DependentResource{
					Kind:      "HumioView",
					Name:      view.Name,
					Namespace: view.Namespace,
				})
				break // Only count each view once
			}
		}
	}

	// Find HumioEventForwardingRules referencing this repository
	ruleList := &humiov1alpha1.HumioEventForwardingRuleList{}
	if err := r.List(ctx, ruleList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list event forwarding rules: %w", err)
	}
	for _, rule := range ruleList.Items {
		if rule.Spec.RepositoryName == repoName {
			dependents = append(dependents, DependentResource{
				Kind:      "HumioEventForwardingRule",
				Name:      rule.Name,
				Namespace: rule.Namespace,
			})
		}
	}

	return dependents, nil
}

// formatDependents formats dependent resources for logging
func formatDependents(deps []DependentResource) []string {
	result := make([]string, 0, len(deps))
	for _, dep := range deps {
		result = append(result, fmt.Sprintf("%s/%s/%s", dep.Kind, dep.Namespace, dep.Name))
	}
	return result
}

// updateDependentResources updates all dependent resources to use the new repository name
// within the same namespace for RBAC compliance
func (r *HumioRepositoryReconciler) updateDependentResources(ctx context.Context,
	oldName, newName, namespace string) error {

	r.Log.Info("Starting cascade update of dependent resources",
		"oldRepositoryName", oldName,
		"newRepositoryName", newName,
		"namespace", namespace)

	dependents, err := r.findDependentResources(ctx, oldName, namespace)
	if err != nil {
		return fmt.Errorf("failed to find dependent resources: %w", err)
	}

	r.Log.Info("Found dependent resources for cascade update",
		"count", len(dependents),
		"dependents", formatDependents(dependents))

	var errs []error
	updatedCount := 0

	for _, dep := range dependents {
		if err := r.updateSingleDependent(ctx, dep, oldName, newName); err != nil {
			errs = append(errs, fmt.Errorf("failed to update %s/%s/%s: %w",
				dep.Kind, dep.Namespace, dep.Name, err))
			r.Log.Error(err, "Failed to update dependent resource",
				"kind", dep.Kind,
				"namespace", dep.Namespace,
				"name", dep.Name)
		} else {
			updatedCount++
			r.Log.Info("Successfully updated dependent resource",
				"kind", dep.Kind,
				"namespace", dep.Namespace,
				"name", dep.Name,
				"oldRepositoryName", oldName,
				"newRepositoryName", newName)
		}
	}

	r.Log.Info("Cascade update completed",
		"total", len(dependents),
		"successful", updatedCount,
		"failed", len(errs))

	if len(errs) > 0 {
		return fmt.Errorf("cascade update had %d failures out of %d dependents: %v",
			len(errs), len(dependents), errs)
	}

	return nil
}

// updateSingleDependent updates a single dependent resource to use the new repository name
func (r *HumioRepositoryReconciler) updateSingleDependent(ctx context.Context,
	dep DependentResource, oldName, newName string) error {

	key := types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}

	switch dep.Kind {
	case "HumioParser":
		parser := &humiov1alpha1.HumioParser{}
		if err := r.Get(ctx, key, parser); err != nil {
			return err
		}

		// Update repository reference
		parser.Spec.RepositoryName = newName

		// Add annotation to track cascade update
		if parser.Annotations == nil {
			parser.Annotations = make(map[string]string)
		}
		parser.Annotations["humio.com/last-cascade-update"] = time.Now().Format(time.RFC3339)
		parser.Annotations["humio.com/cascade-reason"] = fmt.Sprintf("Repository renamed from %s to %s", oldName, newName)

		if err := r.Update(ctx, parser); err != nil {
			return err
		}

	case "HumioIngestToken":
		token := &humiov1alpha1.HumioIngestToken{}
		if err := r.Get(ctx, key, token); err != nil {
			return err
		}

		token.Spec.RepositoryName = newName

		if token.Annotations == nil {
			token.Annotations = make(map[string]string)
		}
		token.Annotations["humio.com/last-cascade-update"] = time.Now().Format(time.RFC3339)
		token.Annotations["humio.com/cascade-reason"] = fmt.Sprintf("Repository renamed from %s to %s", oldName, newName)

		if err := r.Update(ctx, token); err != nil {
			return err
		}

	case "HumioView":
		view := &humiov1alpha1.HumioView{}
		if err := r.Get(ctx, key, view); err != nil {
			return err
		}

		// Update all connections referencing the old repository
		updated := false
		for i := range view.Spec.Connections {
			if view.Spec.Connections[i].RepositoryName == oldName {
				view.Spec.Connections[i].RepositoryName = newName
				updated = true
			}
		}

		if !updated {
			// Shouldn't happen but guard against it
			return fmt.Errorf("view connections did not reference old repository %s", oldName)
		}

		if view.Annotations == nil {
			view.Annotations = make(map[string]string)
		}
		view.Annotations["humio.com/last-cascade-update"] = time.Now().Format(time.RFC3339)
		view.Annotations["humio.com/cascade-reason"] = fmt.Sprintf("Repository renamed from %s to %s", oldName, newName)

		if err := r.Update(ctx, view); err != nil {
			return err
		}

	case "HumioEventForwardingRule":
		rule := &humiov1alpha1.HumioEventForwardingRule{}
		if err := r.Get(ctx, key, rule); err != nil {
			return err
		}

		rule.Spec.RepositoryName = newName

		if rule.Annotations == nil {
			rule.Annotations = make(map[string]string)
		}
		rule.Annotations["humio.com/last-cascade-update"] = time.Now().Format(time.RFC3339)
		rule.Annotations["humio.com/cascade-reason"] = fmt.Sprintf("Repository renamed from %s to %s", oldName, newName)

		if err := r.Update(ctx, rule); err != nil {
			return err
		}

	default:
		return fmt.Errorf("unknown dependent kind: %s", dep.Kind)
	}

	return nil
}

// detectAndHandleRename checks if the repository name has changed and performs the rename
// Returns true if a rename was performed, false otherwise
func (r *HumioRepositoryReconciler) detectAndHandleRename(ctx context.Context,
	client *humioapi.Client, hr *humiov1alpha1.HumioRepository) (bool, reconcile.Result, error) {

	// Skip rename check if resource is being deleted
	if hr.GetDeletionTimestamp() != nil {
		return false, reconcile.Result{}, nil
	}

	// Only check if we have a previously synced name
	if hr.Status.LastSyncedName == "" {
		return false, reconcile.Result{}, nil
	}

	// No rename needed
	if hr.Status.LastSyncedName == hr.Spec.Name {
		return false, reconcile.Result{}, nil
	}

	// Save the old name before any updates
	oldName := hr.Status.LastSyncedName
	newName := hr.Spec.Name

	r.Log.Info("Repository name change detected",
		"namespace", hr.Namespace,
		"name", hr.Name,
		"oldName", oldName,
		"newName", newName)

	// Check for dependent resources and prepare for cascade update
	dependents, err := r.findDependentResources(ctx, oldName, hr.Namespace)
	if err != nil {
		return false, reconcile.Result{}, fmt.Errorf("failed to find dependent resources: %w", err)
	}

	if len(dependents) > 0 {
		r.Log.Info("Repository has dependent resources - will update them after rename",
			"oldName", oldName,
			"newName", newName,
			"dependentCount", len(dependents),
			"dependents", formatDependents(dependents))
	}

	// Idempotency check: verify if the new name already exists in LogScale
	// This handles cases where rename succeeded but status update failed
	r.Log.Info("Checking if repository with new name already exists in LogScale",
		"newName", newName)

	// Create a temporary repository object with the new name to check if it exists
	tempRepo := &humiov1alpha1.HumioRepository{
		Spec: humiov1alpha1.HumioRepositorySpec{
			Name: newName,
		},
	}
	_, err = r.HumioClient.GetRepository(ctx, client, tempRepo)
	if err == nil {
		// Repository with new name already exists in LogScale
		// Check if another K8s resource is already managing this repository
		allRepos := &humiov1alpha1.HumioRepositoryList{}
		if err := r.List(ctx, allRepos); err != nil {
			return false, reconcile.Result{}, fmt.Errorf("failed to list repositories: %w", err)
		}

		// Look for other K8s resources managing the same LogScale repository
		for _, repo := range allRepos.Items {
			// Skip the current resource
			if repo.UID == hr.UID {
				continue
			}

			// Only check resources targeting the same cluster
			if repo.Spec.ManagedClusterName != hr.Spec.ManagedClusterName {
				continue
			}

			// Check if this other K8s resource is managing the target repository
			if repo.Status.LastSyncedName == newName || repo.Spec.Name == newName {
				// Another K8s resource is already managing this repository - reject the rename
				err := fmt.Errorf("cannot rename repository to %q: another HumioRepository resource %q in namespace %q is already managing a LogScale repository with this name in cluster %q",
					newName, repo.Name, repo.Namespace, hr.Spec.ManagedClusterName)
				r.Log.Error(err, "Repository name collision detected",
					"targetName", newName,
					"conflictingResource", repo.Name,
					"conflictingNamespace", repo.Namespace,
					"clusterName", hr.Spec.ManagedClusterName)

				setStateErr := r.setCondition(ctx, hr,
					humiov1alpha1.RepositoryConditionTypeReady,
					metav1.ConditionFalse,
					humiov1alpha1.RepositoryReasonConfigError,
					err.Error())
				if setStateErr != nil {
					return false, reconcile.Result{}, setStateErr
				}
				return false, reconcile.Result{}, err
			}
		}

		// Repository with new name exists but no other K8s resource is managing it
		// This could be either:
		// 1. Idempotent retry: rename succeeded previously but status update failed
		// 2. Name collision: another system/user created a repository with this name
		//
		// To distinguish: check if the OLD repository still exists
		tempOldRepo := &humiov1alpha1.HumioRepository{
			Spec: humiov1alpha1.HumioRepositorySpec{
				Name: oldName,
			},
		}
		_, oldErr := r.HumioClient.GetRepository(ctx, client, tempOldRepo)
		if oldErr == nil {
			// Both old and new repositories exist - this is a name collision, not idempotent retry
			err := fmt.Errorf("cannot rename repository from %q to %q: a repository with name %q already exists in LogScale cluster %q",
				oldName, newName, newName, hr.Spec.ManagedClusterName)
			r.Log.Error(err, "Repository name collision - target name already exists",
				"oldName", oldName,
				"newName", newName,
				"clusterName", hr.Spec.ManagedClusterName)

			setStateErr := r.setCondition(ctx, hr,
				humiov1alpha1.RepositoryConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.RepositoryReasonConfigError,
				err.Error())
			if setStateErr != nil {
				return false, reconcile.Result{}, setStateErr
			}
			return false, reconcile.Result{}, err
		}

		// Old repository doesn't exist, new one does - this is idempotent retry
		// The rename succeeded previously but status update failed
		r.Log.Info("Repository with new name already exists and old name doesn't exist - idempotent retry after successful rename",
			"oldName", oldName,
			"newName", newName)

		// Just update status to reflect reality and cascade update dependents if needed
		hr.Status.LastSyncedName = newName
		if err := r.Status().Update(ctx, hr); err != nil {
			return false, reconcile.Result{}, fmt.Errorf("failed to update status: %w", err)
		}

		// Update dependents to ensure consistency using saved old/new names
		if len(dependents) > 0 && hr.Spec.CascadeRenames {
			if err := r.updateDependentResources(ctx, oldName, newName, hr.Namespace); err != nil {
				r.Log.Error(err, "Failed to update some dependent resources",
					"newName", newName)
			}
		} else if len(dependents) > 0 {
			r.Log.Info("Cascade renames disabled - dependent resources not updated automatically",
				"oldName", oldName,
				"newName", newName,
				"dependentCount", len(dependents),
				"dependents", formatDependents(dependents))
		}

		return true, reconcile.Result{Requeue: true}, nil
	}

	// Perform the rename in LogScale
	r.Log.Info("Renaming repository in LogScale",
		"oldName", oldName,
		"newName", newName)

	// Validate the new name before attempting rename
	if err := ValidateLogScaleName(newName); err != nil {
		setStateErr := r.setCondition(ctx, hr,
			humiov1alpha1.RepositoryConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.RepositoryReasonConfigError,
			fmt.Sprintf("Invalid repository name %q: %v", newName, err))
		if setStateErr != nil {
			return false, reconcile.Result{}, setStateErr
		}
		return false, reconcile.Result{}, fmt.Errorf("invalid repository name %q: %w", newName, err)
	}

	if err := r.HumioClient.RenameRepository(ctx, client, oldName, newName); err != nil {
		setStateErr := r.setCondition(ctx, hr,
			humiov1alpha1.RepositoryConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.RepositoryReasonConfigError,
			fmt.Sprintf("Failed to rename repository from %q to %q: %v", oldName, newName, err))
		if setStateErr != nil {
			return false, reconcile.Result{}, setStateErr
		}
		return false, reconcile.Result{}, fmt.Errorf("failed to rename repository from %q to %q: %w",
			oldName, newName, err)
	}

	// Update dependent resources (best-effort, don't fail if some fail) using saved old/new names
	if len(dependents) > 0 && hr.Spec.CascadeRenames {
		if err := r.updateDependentResources(ctx, oldName, newName, hr.Namespace); err != nil {
			r.Log.Error(err, "Failed to update some dependent resources - they may need manual correction",
				"oldName", oldName,
				"newName", newName)
			// Don't return error - repository was renamed successfully
			// Users can fix inconsistent dependent resources manually or they'll reconcile eventually
		}
	} else if len(dependents) > 0 {
		r.Log.Info("Cascade renames disabled - dependent resources not updated automatically",
			"oldName", oldName,
			"newName", newName,
			"dependentCount", len(dependents),
			"dependents", formatDependents(dependents))
	}

	// Update the tracked name
	hr.Status.LastSyncedName = newName
	if err := r.Status().Update(ctx, hr); err != nil {
		return false, reconcile.Result{}, fmt.Errorf("failed to update status after rename: %w", err)
	}

	r.Log.Info("Repository renamed successfully with cascade updates",
		"newName", newName,
		"updatedDependents", len(dependents))

	// Return true to indicate rename was performed and trigger requeue
	return true, reconcile.Result{Requeue: true}, nil
}
