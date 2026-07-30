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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioViewReconciler reconciles a HumioView object
type HumioViewReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioviews,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviews/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviews/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
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
		setConditionErr := r.setCondition(ctx, hv,
			humiov1alpha1.ViewConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.ViewReasonConfigError,
			fmt.Sprintf("Unable to obtain humio client config: %v", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	// This ensures we handle the rename before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, hv)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle view rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with normal reconciliation
		return result, nil
	}

	// Delete
	r.Log.Info("Checking if view is marked to be deleted")
	isMarkedForDeletion := hv.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("View marked to be deleted")
		if helpers.ContainsElement(hv.GetFinalizers(), HumioFinalizer) {
			if ShouldSkipFinalizer(r.CommonConfig, hv) {
				r.Log.Info("Finalizer skip triggered, removing finalizer without cleanup")
				hv.SetFinalizers(helpers.RemoveElement(hv.GetFinalizers(), HumioFinalizer))
				if err := r.Update(ctx, hv); err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{Requeue: true}, nil
			}
			_, err := r.HumioClient.GetView(ctx, humioHttpClient, hv, false)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hv.SetFinalizers(helpers.RemoveElement(hv.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hv)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for HumioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting View")

			// Check if data deletion is allowed
			if !hv.Spec.AllowDataDeletion {
				err := fmt.Errorf("view may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete view blocked")
			}

			// Audit log before deletion
			r.Log.Info("Proceeding with view deletion",
				"allowDataDeletion", hv.Spec.AllowDataDeletion,
				"viewName", hv.Spec.Name,
				"namespace", hv.Namespace,
				"deletionTimestamp", hv.GetDeletionTimestamp(),
			)

			if err := r.HumioClient.DeleteView(ctx, humioHttpClient, hv); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete view returned error")
			}

			r.Log.Info("Successfully deleted view", "viewName", hv.Spec.Name)
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !ShouldSkipFinalizer(r.CommonConfig, hv) && !helpers.ContainsElement(hv.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to view")
		hv.SetFinalizers(append(hv.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hv)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}
	defer func(ctx context.Context, hv *humiov1alpha1.HumioView) {
		_, err := r.HumioClient.GetView(ctx, humioHttpClient, hv, false)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setCondition(ctx, hv,
				humiov1alpha1.ViewConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.ViewReasonNotFound,
				"View not found in LogScale")
			return
		}
		if err != nil {
			_ = r.setCondition(ctx, hv,
				humiov1alpha1.ViewConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.ViewReasonConfigError,
				fmt.Sprintf("Failed to get view: %v", err))
			return
		}
		_ = r.setCondition(ctx, hv,
			humiov1alpha1.ViewConditionTypeReady,
			metav1.ConditionTrue,
			humiov1alpha1.ViewReasonReady,
			"View is ready")

		// Ensure LastSyncedName is always updated when view exists and is ready
		// This is critical for rename detection to work correctly
		_ = r.updateLastSyncedName(ctx, hv)
	}(ctx, hv)

	r.Log.Info("get current view")
	curView, err := r.HumioClient.GetView(ctx, humioHttpClient, hv, false)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("View doesn't exist. Now adding view")
			addErr := r.HumioClient.AddView(ctx, humioHttpClient, hv)
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
		updateErr := r.HumioClient.UpdateView(ctx, humioHttpClient, hv)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update view")
		}
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioViewReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Add field indexes for efficient dependent resource lookups
	if err := r.setupFieldIndexes(mgr); err != nil {
		return fmt.Errorf("failed to setup field indexes: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioView{}).
		Named("humioview").
		Complete(r)
}

// setupFieldIndexes creates field indexes for efficient lookups of resources
// that reference views by name
func (r *HumioViewReconciler) setupFieldIndexes(mgr ctrl.Manager) error {
	// Index resources with simple ViewName field
	viewNameIndexer := func(obj client.Object, getViewName func(client.Object) string) error {
		return mgr.GetFieldIndexer().IndexField(context.Background(), obj, "spec.viewName",
			func(rawObj client.Object) []string {
				viewName := getViewName(rawObj)
				if viewName == "" {
					return nil
				}
				return []string{viewName}
			})
	}

	// Index HumioAlerts by ViewName
	if err := viewNameIndexer(&humiov1alpha1.HumioAlert{},
		func(obj client.Object) string { return obj.(*humiov1alpha1.HumioAlert).Spec.ViewName }); err != nil {
		return fmt.Errorf("failed to create alert index: %w", err)
	}

	// Index HumioActions by ViewName
	if err := viewNameIndexer(&humiov1alpha1.HumioAction{},
		func(obj client.Object) string { return obj.(*humiov1alpha1.HumioAction).Spec.ViewName }); err != nil {
		return fmt.Errorf("failed to create action index: %w", err)
	}

	// Index HumioFilterAlerts by ViewName
	if err := viewNameIndexer(&humiov1alpha1.HumioFilterAlert{},
		func(obj client.Object) string { return obj.(*humiov1alpha1.HumioFilterAlert).Spec.ViewName }); err != nil {
		return fmt.Errorf("failed to create filter alert index: %w", err)
	}

	// Index HumioAggregateAlerts by ViewName
	if err := viewNameIndexer(&humiov1alpha1.HumioAggregateAlert{},
		func(obj client.Object) string { return obj.(*humiov1alpha1.HumioAggregateAlert).Spec.ViewName }); err != nil {
		return fmt.Errorf("failed to create aggregate alert index: %w", err)
	}

	// Index HumioScheduledSearches by ViewName
	if err := viewNameIndexer(&humiov1alpha1.HumioScheduledSearch{},
		func(obj client.Object) string { return obj.(*humiov1alpha1.HumioScheduledSearch).Spec.ViewName }); err != nil {
		return fmt.Errorf("failed to create scheduled search index: %w", err)
	}

	// Index HumioSavedQueries by ViewName
	if err := viewNameIndexer(&humiov1alpha1.HumioSavedQuery{},
		func(obj client.Object) string { return obj.(*humiov1alpha1.HumioSavedQuery).Spec.ViewName }); err != nil {
		return fmt.Errorf("failed to create saved query index: %w", err)
	}

	// Note: HumioPackages reference views through PackageInstallTargets array (ViewNames),
	// HumioViewPermissionRoles and HumioViewTokens reference views through arrays,
	// which can't be efficiently indexed, so we keep the List+filter approach for those

	return nil
}

// setCondition sets a condition on the HumioView resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioViewReconciler) setCondition(ctx context.Context,
	hv *humiov1alpha1.HumioView,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioView{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hv), latest); err != nil {
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
		latest.Status.State = viewStateFromCondition(status, reason)

		return r.Status().Update(ctx, latest)
	})
}

// updateLastSyncedName ensures LastSyncedName is set to the current Spec.Name
// This is called separately from setCondition to ensure it's always persisted
func (r *HumioViewReconciler) updateLastSyncedName(ctx context.Context, hv *humiov1alpha1.HumioView) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioView{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hv), latest); err != nil {
			return err
		}

		// Only update if LastSyncedName is empty or matches current spec (not during rename)
		// This prevents overwriting the old name before rename detection can run
		if latest.Status.LastSyncedName == "" || latest.Status.LastSyncedName == latest.Spec.Name {
			latest.Status.LastSyncedName = latest.Spec.Name
			return r.Status().Update(ctx, latest)
		}

		return nil
	})
}

func viewStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioViewStateExists
	}
	switch reason {
	case humiov1alpha1.ViewReasonNotFound:
		return humiov1alpha1.HumioViewStateNotFound
	case humiov1alpha1.ViewReasonConfigError:
		return humiov1alpha1.HumioViewStateConfigError
	default:
		return humiov1alpha1.HumioViewStateUnknown
	}
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

// addCascadeAnnotations adds annotations to track cascade updates
func addCascadeAnnotations(annotations map[string]string, oldName, newName string) map[string]string {
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations["humio.com/last-cascade-update"] = time.Now().Format(time.RFC3339)
	annotations["humio.com/cascade-reason"] = fmt.Sprintf("View renamed from %s to %s", oldName, newName)
	return annotations
}

// checkSimpleViewNameMatch checks if a resource's ViewName matches the target
func checkSimpleViewNameMatch(list client.ObjectList, viewName string, kind string, dependents *[]DependentResource) {
	switch v := list.(type) {
	case *humiov1alpha1.HumioAlertList:
		for _, item := range v.Items {
			if item.Spec.ViewName == viewName {
				*dependents = append(*dependents, DependentResource{Kind: kind, Name: item.Name, Namespace: item.Namespace})
			}
		}
	case *humiov1alpha1.HumioActionList:
		for _, item := range v.Items {
			if item.Spec.ViewName == viewName {
				*dependents = append(*dependents, DependentResource{Kind: kind, Name: item.Name, Namespace: item.Namespace})
			}
		}
	case *humiov1alpha1.HumioFilterAlertList:
		for _, item := range v.Items {
			if item.Spec.ViewName == viewName {
				*dependents = append(*dependents, DependentResource{Kind: kind, Name: item.Name, Namespace: item.Namespace})
			}
		}
	case *humiov1alpha1.HumioAggregateAlertList:
		for _, item := range v.Items {
			if item.Spec.ViewName == viewName {
				*dependents = append(*dependents, DependentResource{Kind: kind, Name: item.Name, Namespace: item.Namespace})
			}
		}
	case *humiov1alpha1.HumioScheduledSearchList:
		for _, item := range v.Items {
			if item.Spec.ViewName == viewName {
				*dependents = append(*dependents, DependentResource{Kind: kind, Name: item.Name, Namespace: item.Namespace})
			}
		}
	case *humiov1alpha1.HumioSavedQueryList:
		for _, item := range v.Items {
			if item.Spec.ViewName == viewName {
				*dependents = append(*dependents, DependentResource{Kind: kind, Name: item.Name, Namespace: item.Namespace})
			}
		}
	}
}

// findDependentResources finds all CRD resources that reference the given view name
// within the same namespace for RBAC compliance. Uses field indexes for efficient lookups.
func (r *HumioViewReconciler) findDependentResources(ctx context.Context,
	viewName string, namespace string) ([]DependentResource, error) {

	dependents := make([]DependentResource, 0, 15) // Pre-allocate for typical case
	listOpts := []client.ListOption{
		client.InNamespace(namespace), // Only same namespace for RBAC compliance
	}

	// Helper to add indexed resources with simple ViewName field
	addIndexedDeps := func(kind string, list client.ObjectList) error {
		opts := append(listOpts, client.MatchingFields{"spec.viewName": viewName})
		if err := r.List(ctx, list, opts...); err != nil {
			return fmt.Errorf("failed to list %s: %w", kind, err)
		}
		checkSimpleViewNameMatch(list, viewName, kind, &dependents)
		return nil
	}

	// Find resources with simple ViewName field (using indexes)
	if err := addIndexedDeps("HumioAlert", &humiov1alpha1.HumioAlertList{}); err != nil {
		return nil, err
	}
	if err := addIndexedDeps("HumioAction", &humiov1alpha1.HumioActionList{}); err != nil {
		return nil, err
	}
	if err := addIndexedDeps("HumioFilterAlert", &humiov1alpha1.HumioFilterAlertList{}); err != nil {
		return nil, err
	}
	if err := addIndexedDeps("HumioAggregateAlert", &humiov1alpha1.HumioAggregateAlertList{}); err != nil {
		return nil, err
	}
	if err := addIndexedDeps("HumioScheduledSearch", &humiov1alpha1.HumioScheduledSearchList{}); err != nil {
		return nil, err
	}
	if err := addIndexedDeps("HumioSavedQuery", &humiov1alpha1.HumioSavedQueryList{}); err != nil {
		return nil, err
	}

	// Find HumioViewPermissionRoles referencing this view
	// Note: Can't use index because view is in RoleAssignments array
	viewPermissionRoleList := &humiov1alpha1.HumioViewPermissionRoleList{}
	if err := r.List(ctx, viewPermissionRoleList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list view permission roles: %w", err)
	}
	for _, viewPermissionRole := range viewPermissionRoleList.Items {
		// Check if any role assignment references this view
		for _, assignment := range viewPermissionRole.Spec.RoleAssignments {
			if assignment.RepoOrViewName == viewName {
				dependents = append(dependents, DependentResource{
					Kind:      "HumioViewPermissionRole",
					Name:      viewPermissionRole.Name,
					Namespace: viewPermissionRole.Namespace,
				})
				break // Only count each role once
			}
		}
	}

	// Find HumioViewTokens referencing this view
	// Note: Can't use index because view is in ViewNames array
	viewTokenList := &humiov1alpha1.HumioViewTokenList{}
	if err := r.List(ctx, viewTokenList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list view tokens: %w", err)
	}
	for _, viewToken := range viewTokenList.Items {
		// Check if any view name in the list matches
		for _, vName := range viewToken.Spec.ViewNames {
			if vName == viewName {
				dependents = append(dependents, DependentResource{
					Kind:      "HumioViewToken",
					Name:      viewToken.Name,
					Namespace: viewToken.Namespace,
				})
				break // Only count each token once
			}
		}
	}

	// Find HumioPackages referencing this view
	// Note: Can't use index because view is in PackageInstallTargets array
	packageList := &humiov1alpha1.HumioPackageList{}
	if err := r.List(ctx, packageList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list packages: %w", err)
	}
	for _, pkg := range packageList.Items {
		// Check if any install target references this view
		for _, target := range pkg.Spec.PackageInstallTargets {
			// Check direct view name references
			for _, vName := range target.ViewNames {
				if vName == viewName {
					dependents = append(dependents, DependentResource{
						Kind:      "HumioPackage",
						Name:      pkg.Name,
						Namespace: pkg.Namespace,
					})
					goto nextPackage // Only count each package once
				}
			}
		}
	nextPackage:
	}

	return dependents, nil
}

// updateDependentResources updates all dependent resources to use the new view name
// within the same namespace for RBAC compliance
func (r *HumioViewReconciler) updateDependentResources(ctx context.Context,
	oldName, newName, namespace string) error {

	r.Log.Info("Starting cascade update of dependent resources",
		"oldViewName", oldName,
		"newViewName", newName,
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
				"oldViewName", oldName,
				"newViewName", newName)
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

// updateSingleDependent updates a single dependent resource to use the new view name
func (r *HumioViewReconciler) updateSingleDependent(ctx context.Context,
	dep DependentResource, oldName, newName string) error {

	key := types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}

	// Helper to update simple ViewName field resources
	updateSimpleViewName := func(obj client.Object) error {
		if err := r.Get(ctx, key, obj); err != nil {
			return err
		}

		// Set ViewName using reflection-like approach via type switch
		switch v := obj.(type) {
		case *humiov1alpha1.HumioAlert:
			v.Spec.ViewName = newName
			v.Annotations = addCascadeAnnotations(v.Annotations, oldName, newName)
		case *humiov1alpha1.HumioAction:
			v.Spec.ViewName = newName
			v.Annotations = addCascadeAnnotations(v.Annotations, oldName, newName)
		case *humiov1alpha1.HumioFilterAlert:
			v.Spec.ViewName = newName
			v.Annotations = addCascadeAnnotations(v.Annotations, oldName, newName)
		case *humiov1alpha1.HumioAggregateAlert:
			v.Spec.ViewName = newName
			v.Annotations = addCascadeAnnotations(v.Annotations, oldName, newName)
		case *humiov1alpha1.HumioScheduledSearch:
			v.Spec.ViewName = newName
			v.Annotations = addCascadeAnnotations(v.Annotations, oldName, newName)
		case *humiov1alpha1.HumioSavedQuery:
			v.Spec.ViewName = newName
			v.Annotations = addCascadeAnnotations(v.Annotations, oldName, newName)
		}

		return r.Update(ctx, obj)
	}

	switch dep.Kind {
	case "HumioAlert":
		return updateSimpleViewName(&humiov1alpha1.HumioAlert{})
	case "HumioAction":
		return updateSimpleViewName(&humiov1alpha1.HumioAction{})
	case "HumioFilterAlert":
		return updateSimpleViewName(&humiov1alpha1.HumioFilterAlert{})
	case "HumioAggregateAlert":
		return updateSimpleViewName(&humiov1alpha1.HumioAggregateAlert{})
	case "HumioScheduledSearch":
		return updateSimpleViewName(&humiov1alpha1.HumioScheduledSearch{})
	case "HumioSavedQuery":
		return updateSimpleViewName(&humiov1alpha1.HumioSavedQuery{})

	case "HumioViewPermissionRole":
		viewPermissionRole := &humiov1alpha1.HumioViewPermissionRole{}
		if err := r.Get(ctx, key, viewPermissionRole); err != nil {
			return err
		}

		// Update all role assignments referencing the old view
		updated := false
		for i := range viewPermissionRole.Spec.RoleAssignments {
			if viewPermissionRole.Spec.RoleAssignments[i].RepoOrViewName == oldName {
				viewPermissionRole.Spec.RoleAssignments[i].RepoOrViewName = newName
				updated = true
			}
		}

		if !updated {
			return fmt.Errorf("role assignments did not reference old view %s", oldName)
		}

		viewPermissionRole.Annotations = addCascadeAnnotations(viewPermissionRole.Annotations, oldName, newName)
		return r.Update(ctx, viewPermissionRole)

	case "HumioViewToken":
		viewToken := &humiov1alpha1.HumioViewToken{}
		if err := r.Get(ctx, key, viewToken); err != nil {
			return err
		}

		// Update all view names in the list
		updated := false
		for i := range viewToken.Spec.ViewNames {
			if viewToken.Spec.ViewNames[i] == oldName {
				viewToken.Spec.ViewNames[i] = newName
				updated = true
			}
		}

		if !updated {
			return fmt.Errorf("view names did not reference old view %s", oldName)
		}

		viewToken.Annotations = addCascadeAnnotations(viewToken.Annotations, oldName, newName)
		return r.Update(ctx, viewToken)

	case "HumioPackage":
		pkg := &humiov1alpha1.HumioPackage{}
		if err := r.Get(ctx, key, pkg); err != nil {
			return err
		}

		// Update all install targets referencing the old view
		updated := false
		for i := range pkg.Spec.PackageInstallTargets {
			for j := range pkg.Spec.PackageInstallTargets[i].ViewNames {
				if pkg.Spec.PackageInstallTargets[i].ViewNames[j] == oldName {
					pkg.Spec.PackageInstallTargets[i].ViewNames[j] = newName
					updated = true
				}
			}
		}

		if !updated {
			return fmt.Errorf("package install targets did not reference old view %s", oldName)
		}

		pkg.Annotations = addCascadeAnnotations(pkg.Annotations, oldName, newName)
		return r.Update(ctx, pkg)

	default:
		return fmt.Errorf("unknown dependent kind: %s", dep.Kind)
	}
}

// detectAndHandleRename checks if the view name has changed and performs the rename
// Returns true if a rename was performed, false otherwise
func (r *HumioViewReconciler) detectAndHandleRename(ctx context.Context,
	client *humioapi.Client, hv *humiov1alpha1.HumioView) (bool, reconcile.Result, error) {

	// Skip rename check if resource is being deleted
	if hv.GetDeletionTimestamp() != nil {
		return false, reconcile.Result{}, nil
	}

	// Only check if we have a previously synced name
	if hv.Status.LastSyncedName == "" {
		return false, reconcile.Result{}, nil
	}

	// No rename needed
	if hv.Status.LastSyncedName == hv.Spec.Name {
		return false, reconcile.Result{}, nil
	}

	r.Log.Info("View name change detected",
		"namespace", hv.Namespace,
		"name", hv.Name,
		"oldName", hv.Status.LastSyncedName,
		"newName", hv.Spec.Name)

	// Check for dependent resources and prepare for cascade update
	dependents, err := r.findDependentResources(ctx, hv.Status.LastSyncedName, hv.Namespace)
	if err != nil {
		return false, reconcile.Result{}, fmt.Errorf("failed to find dependent resources: %w", err)
	}

	if len(dependents) > 0 {
		r.Log.Info("View has dependent resources - will update them after rename",
			"oldName", hv.Status.LastSyncedName,
			"newName", hv.Spec.Name,
			"dependentCount", len(dependents),
			"dependents", formatDependents(dependents))
	}

	// Idempotency check: verify if the new name already exists in LogScale
	// This handles cases where rename succeeded but status update failed
	r.Log.Info("Checking if view with new name already exists in LogScale",
		"newName", hv.Spec.Name)

	// Create a temporary view object with the new name to check if it exists
	tempView := &humiov1alpha1.HumioView{
		Spec: humiov1alpha1.HumioViewSpec{
			Name: hv.Spec.Name,
		},
	}
	_, err = r.HumioClient.GetView(ctx, client, tempView, false)
	if err == nil {
		// View with new name already exists in LogScale
		// Check if another K8s resource is already managing this view
		allViews := &humiov1alpha1.HumioViewList{}
		if err := r.List(ctx, allViews); err != nil {
			return false, reconcile.Result{}, fmt.Errorf("failed to list views: %w", err)
		}

		// Look for other K8s resources managing the same LogScale view
		for _, view := range allViews.Items {
			// Skip the current resource
			if view.UID == hv.UID {
				continue
			}

			// Only check resources targeting the same cluster
			if view.Spec.ManagedClusterName != hv.Spec.ManagedClusterName {
				continue
			}

			// Check if this other K8s resource is managing the target view
			if view.Status.LastSyncedName == hv.Spec.Name || view.Spec.Name == hv.Spec.Name {
				// Another K8s resource is already managing this view - reject the rename
				err := fmt.Errorf("cannot rename view to %q: another HumioView resource %q in namespace %q is already managing a LogScale view with this name in cluster %q",
					hv.Spec.Name, view.Name, view.Namespace, hv.Spec.ManagedClusterName)
				r.Log.Error(err, "View name collision detected",
					"targetName", hv.Spec.Name,
					"conflictingResource", view.Name,
					"conflictingNamespace", view.Namespace,
					"clusterName", hv.Spec.ManagedClusterName)

				setStateErr := r.setCondition(ctx, hv, humiov1alpha1.ViewConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.ViewReasonConfigError, err.Error())
				if setStateErr != nil {
					return false, reconcile.Result{}, setStateErr
				}
				return false, reconcile.Result{}, err
			}
		}

		// View with new name exists but no other K8s resource is managing it
		// This could be either:
		// 1. Idempotent retry: rename succeeded previously but status update failed
		// 2. Name collision: another system/user created a view with this name
		//
		// To distinguish: check if the OLD view still exists
		tempOldView := &humiov1alpha1.HumioView{
			Spec: humiov1alpha1.HumioViewSpec{
				Name: hv.Status.LastSyncedName,
			},
		}
		_, oldErr := r.HumioClient.GetView(ctx, client, tempOldView, false)
		if oldErr == nil {
			// Both old and new views exist - this is a name collision, not idempotent retry
			err := fmt.Errorf("cannot rename view from %q to %q: a view with name %q already exists in LogScale cluster %q",
				hv.Status.LastSyncedName, hv.Spec.Name, hv.Spec.Name, hv.Spec.ManagedClusterName)
			r.Log.Error(err, "View name collision - target name already exists",
				"oldName", hv.Status.LastSyncedName,
				"newName", hv.Spec.Name,
				"clusterName", hv.Spec.ManagedClusterName)

			setStateErr := r.setCondition(ctx, hv,
				humiov1alpha1.ViewConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.ViewReasonConfigError,
				err.Error())
			if setStateErr != nil {
				return false, reconcile.Result{}, setStateErr
			}
			return false, reconcile.Result{}, err
		}

		// Old view doesn't exist, new one does - this is idempotent retry
		// The rename succeeded previously but status update failed
		r.Log.Info("View with new name already exists and old name doesn't exist - idempotent retry after successful rename",
			"oldName", hv.Status.LastSyncedName,
			"newName", hv.Spec.Name)

		// Save the old name before updating status
		oldName := hv.Status.LastSyncedName

		// Just update status to reflect reality and cascade update dependents if needed
		hv.Status.LastSyncedName = hv.Spec.Name
		if err := r.Status().Update(ctx, hv); err != nil {
			return false, reconcile.Result{}, fmt.Errorf("failed to update status: %w", err)
		}

		// Update dependents to ensure consistency - use the saved oldName
		if len(dependents) > 0 && hv.Spec.CascadeRenames {
			if err := r.updateDependentResources(ctx, oldName, hv.Spec.Name, hv.Namespace); err != nil {
				r.Log.Error(err, "Failed to update some dependent resources",
					"newName", hv.Spec.Name)
			}
		} else if len(dependents) > 0 {
			r.Log.Info("Cascade renames disabled - dependent resources not updated automatically",
				"oldName", oldName,
				"newName", hv.Spec.Name,
				"dependentCount", len(dependents))
		}

		return true, reconcile.Result{Requeue: true}, nil
	}

	// Perform the rename in LogScale
	r.Log.Info("Renaming view in LogScale",
		"oldName", hv.Status.LastSyncedName,
		"newName", hv.Spec.Name)

	// Validate the new name before attempting rename
	if err := ValidateLogScaleName(hv.Spec.Name); err != nil {
		setStateErr := r.setCondition(ctx, hv, humiov1alpha1.ViewConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.ViewReasonConfigError, err.Error())
		if setStateErr != nil {
			return false, reconcile.Result{}, setStateErr
		}
		return false, reconcile.Result{}, fmt.Errorf("invalid view name %q: %w", hv.Spec.Name, err)
	}

	if err := r.HumioClient.RenameView(ctx, client, hv.Status.LastSyncedName, hv.Spec.Name); err != nil {
		setStateErr := r.setCondition(ctx, hv, humiov1alpha1.ViewConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.ViewReasonConfigError, err.Error())
		if setStateErr != nil {
			return false, reconcile.Result{}, setStateErr
		}
		return false, reconcile.Result{}, fmt.Errorf("failed to rename view from %q to %q: %w",
			hv.Status.LastSyncedName, hv.Spec.Name, err)
	}

	// Update dependent resources (best-effort, don't fail if some fail)
	if len(dependents) > 0 && hv.Spec.CascadeRenames {
		if err := r.updateDependentResources(ctx, hv.Status.LastSyncedName, hv.Spec.Name, hv.Namespace); err != nil {
			r.Log.Error(err, "Failed to update some dependent resources - they may need manual correction",
				"oldName", hv.Status.LastSyncedName,
				"newName", hv.Spec.Name)
			// Don't return error - view was renamed successfully
			// Users can fix inconsistent dependent resources manually or they'll reconcile eventually
		}
	} else if len(dependents) > 0 {
		r.Log.Info("Cascade renames disabled - dependent resources not updated automatically",
			"oldName", hv.Status.LastSyncedName,
			"newName", hv.Spec.Name,
			"dependentCount", len(dependents))
	}

	// Update the tracked name
	hv.Status.LastSyncedName = hv.Spec.Name
	if err := r.Status().Update(ctx, hv); err != nil {
		return false, reconcile.Result{}, fmt.Errorf("failed to update status after rename: %w", err)
	}

	r.Log.Info("View renamed successfully with cascade updates",
		"newName", hv.Spec.Name,
		"updatedDependents", len(dependents))

	// Return true to indicate rename was performed and trigger requeue
	return true, reconcile.Result{Requeue: true}, nil
}
