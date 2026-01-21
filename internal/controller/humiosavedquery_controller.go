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
	"strings"
	"sync"
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
	"k8s.io/client-go/util/retry"
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
	SavedQueryReasonSavedQueryReady     = "SavedQueryReady"
	SavedQueryReasonSavedQueryCreated   = "SavedQueryCreated"
	SavedQueryReasonSavedQueryUpdated   = "SavedQueryUpdated"
	SavedQueryReasonSavedQueryNotFound  = "SavedQueryNotFound"
	SavedQueryReasonConfigurationError  = "ConfigurationError"
	SavedQueryReasonConfigurationSynced = "ConfigurationSynced"
	SavedQueryReasonConfigurationDrift  = "ConfigurationDrift"
	SavedQueryReasonSyncFailed          = "SyncFailed"
	SavedQueryReasonNameCollision       = "NameCollision"
	SavedQueryReasonAdoptionRejected    = "AdoptionRejected"
	SavedQueryReasonAdoptionSuccessful  = "AdoptionSuccessful"
	SavedQueryReasonUnsupportedFields   = "UnsupportedFields"
)

// clusterVersionCacheEntry holds a cached cluster version with timestamp
type clusterVersionCacheEntry struct {
	version   string
	timestamp time.Time
}

// clusterVersionCache provides thread-safe caching of cluster versions
// Cache entries expire after 5 minutes to avoid stale version information
type clusterVersionCache struct {
	mu      sync.RWMutex
	entries map[string]*clusterVersionCacheEntry
	ttl     time.Duration
}

// newClusterVersionCache creates a new version cache with 5 minute TTL
func newClusterVersionCache() *clusterVersionCache {
	return &clusterVersionCache{
		entries: make(map[string]*clusterVersionCacheEntry),
		ttl:     5 * time.Minute,
	}
}

// get retrieves a cached version if it exists and is not expired
func (c *clusterVersionCache) get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[key]
	if !exists {
		return "", false
	}

	// Check if entry has expired
	if time.Since(entry.timestamp) > c.ttl {
		return "", false
	}

	return entry.version, true
}

// set stores a version in the cache
func (c *clusterVersionCache) set(key, version string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &clusterVersionCacheEntry{
		version:   version,
		timestamp: time.Now(),
	}
}

// HumioSavedQueryReconciler reconciles a HumioSavedQuery object
type HumioSavedQueryReconciler struct {
	client.Client
	CommonConfig
	BaseLogger       logr.Logger
	Log              logr.Logger
	HumioClient      humio.Client
	Namespace        string
	versionCache     *clusterVersionCache
	versionCacheOnce sync.Once
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

	return r.reconcileHumioSavedQuery(ctx, humioHttpClient, hsq)
}

// shouldUseV2API determines if we should use the V2 API based on cluster version
// Uses caching to avoid repeated calls to GetClusterImageVersion for the same cluster
func (r *HumioSavedQueryReconciler) shouldUseV2API(ctx context.Context, hsq *humiov1alpha1.HumioSavedQuery) (bool, error) {
	var savedQueryV2MinVersion = "1.200.0"

	// Initialize cache on first use (thread-safe)
	r.versionCacheOnce.Do(func() {
		r.versionCache = newClusterVersionCache()
	})

	// Create cache key from cluster identifier
	clusterKey := hsq.Spec.ManagedClusterName
	if clusterKey == "" {
		clusterKey = hsq.Spec.ExternalClusterName
	}
	cacheKey := fmt.Sprintf("%s/%s", hsq.Namespace, clusterKey)

	// Check cache first
	if cachedVersion, found := r.versionCache.get(cacheKey); found {
		r.Log.V(1).Info("using cached cluster version", "cluster", cacheKey, "version", cachedVersion)
		hasV2, err := helpers.FeatureExists(cachedVersion, savedQueryV2MinVersion)
		if err != nil {
			return false, fmt.Errorf("failed to compare versions: %w", err)
		}
		return hasV2, nil
	}

	// Cache miss - fetch version from API
	r.Log.V(1).Info("fetching cluster version (cache miss)", "cluster", cacheKey)
	clusterVersion, err := helpers.GetClusterImageVersion(ctx, r.Client, hsq.Namespace, hsq.Spec.ManagedClusterName, hsq.Spec.ExternalClusterName)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster version: %w", err)
	}

	// Store in cache for future use
	r.versionCache.set(cacheKey, clusterVersion)

	// Use V2 API if the current version is >= the minimum V2 version
	hasV2, err := helpers.FeatureExists(clusterVersion, savedQueryV2MinVersion)
	if err != nil {
		return false, fmt.Errorf("failed to compare versions: %w", err)
	}
	return hasV2, nil
}

// determineAPIVersion determines which API to use
// Returns (use_v1_api, error)
// Note: Unlike ScheduledSearch, SavedQuery only has v1alpha1 CRD, so no type conversion is needed
func (r *HumioSavedQueryReconciler) determineAPIVersion(ctx context.Context, hsq *humiov1alpha1.HumioSavedQuery) (bool, error) {
	// First check if cluster supports V2 API
	useV2, err := r.shouldUseV2API(ctx, hsq)
	if err != nil {
		return false, err
	}

	if useV2 {
		// Cluster supports V2 API
		return false, nil
	}

	// Cluster supports V1 API only
	return true, nil
}

// getSavedQueryVersionAware wraps the HumioClient.GetSavedQuery call with version detection
func (r *HumioSavedQueryReconciler) getSavedQueryVersionAware(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) (*humiographql.SavedQueryDetailsV2, error) {
	useV1, err := r.determineAPIVersion(ctx, hsq)
	if err != nil {
		return nil, err
	}

	if useV1 {
		// Use V1 API and convert result to V2 format
		resultV1, err := r.HumioClient.GetSavedQuery(ctx, client, hsq)
		if err != nil {
			return nil, err
		}
		// Convert V1 to V2 format
		return &humiographql.SavedQueryDetailsV2{
			Id:          resultV1.Id,
			Name:        resultV1.Name,
			DisplayName: resultV1.DisplayName,
			Description: nil, // V1 doesn't have description
			Labels:      nil, // V1 doesn't have labels
			Query: humiographql.SavedQueryDetailsV2QueryHumioQuery{
				QueryString: resultV1.Query.QueryString,
			},
		}, nil
	} else {
		// Use V2 API directly
		return r.HumioClient.GetSavedQueryV2(ctx, client, hsq)
	}
}

// addSavedQueryVersionAware wraps the HumioClient.AddSavedQuery call with version detection
func (r *HumioSavedQueryReconciler) addSavedQueryVersionAware(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) error {
	useV1, err := r.determineAPIVersion(ctx, hsq)
	if err != nil {
		return err
	}

	if useV1 {
		// Use V1 API (ignores description and labels)
		return r.HumioClient.AddSavedQuery(ctx, client, hsq, false)
	} else {
		return r.HumioClient.AddSavedQueryV2(ctx, client, hsq)
	}
}

// updateSavedQueryVersionAware wraps the HumioClient.UpdateSavedQuery call with version detection
func (r *HumioSavedQueryReconciler) updateSavedQueryVersionAware(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) error {
	useV1, err := r.determineAPIVersion(ctx, hsq)
	if err != nil {
		return err
	}

	if useV1 {
		// Use V1 API (ignores description and labels)
		return r.HumioClient.UpdateSavedQuery(ctx, client, hsq, false)
	} else {
		return r.HumioClient.UpdateSavedQueryV2(ctx, client, hsq)
	}
}

// ensureFinalizer checks if the resource has the finalizer and adds it if missing.
// Returns:
//   - (Result, error) for reconciliation
//   - bool indicating if finalizer was added (true = caller should return to requeue)
func (r *HumioSavedQueryReconciler) ensureFinalizer(
	ctx context.Context,
	hsq *humiov1alpha1.HumioSavedQuery,
) (reconcile.Result, error, bool) {
	if helpers.ContainsElement(hsq.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil, false // Finalizer already present
	}

	r.Log.Info("Finalizer not present, adding finalizer to saved query")
	hsq.SetFinalizers(append(hsq.GetFinalizers(), HumioFinalizer))
	err := r.Update(ctx, hsq)
	if err != nil {
		return reconcile.Result{}, err, true
	}

	return reconcile.Result{Requeue: true}, nil, true
}

// transitionLifecycleCondition transitions one-time lifecycle event reasons
// (Created/Updated/AdoptionSuccessful) to steady-state Ready reason.
// Errors are logged but not returned as they should not block reconciliation.
func (r *HumioSavedQueryReconciler) transitionLifecycleCondition(
	ctx context.Context,
	hsq *humiov1alpha1.HumioSavedQuery,
) {
	readyCondition := meta.FindStatusCondition(hsq.Status.Conditions, SavedQueryConditionTypeReady)
	if readyCondition == nil {
		return
	}

	if readyCondition.Status != metav1.ConditionTrue {
		return
	}

	// Check if reason is a lifecycle event
	isLifecycleEvent := readyCondition.Reason == SavedQueryReasonSavedQueryCreated ||
		readyCondition.Reason == SavedQueryReasonSavedQueryUpdated ||
		readyCondition.Reason == SavedQueryReasonAdoptionSuccessful

	if !isLifecycleEvent {
		return
	}

	// Transition to steady-state reason
	if err := r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionTrue,
		SavedQueryReasonSavedQueryReady, "Saved query is ready"); err != nil {
		r.Log.Error(err, "failed to transition to SavedQueryReady")
	}
}

// handleResourceDeletion handles the deletion flow for a saved query.
// Returns:
//   - (Result, error) for reconciliation
//   - bool indicating if deletion was handled (true = caller should return immediately)
func (r *HumioSavedQueryReconciler) handleResourceDeletion(
	ctx context.Context,
	client *humioapi.Client,
	hsq *humiov1alpha1.HumioSavedQuery,
) (reconcile.Result, error, bool) {
	r.Log.Info("Checking if saved query is marked to be deleted")
	if hsq.GetDeletionTimestamp() == nil {
		return reconcile.Result{}, nil, false
	}

	r.Log.Info("Saved query marked to be deleted")
	if !helpers.ContainsElement(hsq.GetFinalizers(), HumioFinalizer) {
		// No finalizer, nothing to do
		return reconcile.Result{}, nil, true
	}

	// Only delete from LogScale if we actually manage this query
	// Check ManagedByOperator status field
	if hsq.Status.ManagedByOperator == nil || !*hsq.Status.ManagedByOperator {
		// Query is not managed by operator (adoption rejected or never created)
		// Just remove finalizer without deleting from LogScale
		r.Log.Info("Resource not managed by operator, removing finalizer without deleting from LogScale")
		hsq.SetFinalizers(helpers.RemoveElement(hsq.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hsq)
		if err != nil {
			return reconcile.Result{}, err, true
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil, true
	}

	r.Log.Info("Resource is managed by operator, checking if query exists in LogScale")
	_, err := r.HumioClient.GetSavedQuery(ctx, client, hsq)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		// Query doesn't exist in LogScale, just remove finalizer
		r.Log.Info("Saved query not found in LogScale, removing finalizer")
		hsq.SetFinalizers(helpers.RemoveElement(hsq.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hsq)
		if err != nil {
			return reconcile.Result{}, err, true
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil, true
	}
	if err != nil {
		// Classify error to decide whether to retry or remove finalizer
		if r.isPermanentError(err) {
			// Permanent error (e.g., view doesn't exist, already deleted)
			// Remove finalizer to unblock deletion
			r.Log.Info("Permanent error checking if saved query exists, removing finalizer anyway", "error", err)
			hsq.SetFinalizers(helpers.RemoveElement(hsq.GetFinalizers(), HumioFinalizer))
			updateErr := r.Update(ctx, hsq)
			if updateErr != nil {
				return reconcile.Result{}, updateErr, true
			}
			r.Log.Info("Finalizer removed successfully despite permanent error")
			return reconcile.Result{Requeue: true}, nil, true
		}

		// Transient error - retry
		r.Log.Info("Transient error checking if saved query exists, will retry", "error", err)
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to check if saved query exists during deletion"), true
	}

	// Query exists in LogScale and we manage it, delete it
	r.Log.Info("Deleting saved query from LogScale")
	if err := r.HumioClient.DeleteSavedQuery(ctx, client, hsq); err != nil {
		// If deletion fails with EntityNotFound, that's fine - already gone
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("Saved query already deleted from LogScale")
		} else {
			return reconcile.Result{}, r.logErrorAndReturn(err, "Delete saved query returned error"), true
		}
	}
	r.Log.Info("Successfully deleted saved query from LogScale")
	// If no error was detected, we need to requeue so that we can remove the finalizer
	return reconcile.Result{Requeue: true}, nil, true
}

func (r *HumioSavedQueryReconciler) reconcileHumioSavedQuery(ctx context.Context, client *humioapi.Client, hsq *humiov1alpha1.HumioSavedQuery) (reconcile.Result, error) {
	// Handle deletion
	result, err, handled := r.handleResourceDeletion(ctx, client, hsq)
	if handled {
		return result, err
	}

	// Ensure finalizer is present
	result, err, added := r.ensureFinalizer(ctx, hsq)
	if added {
		return result, err
	}

	// Check for name collision - ensure no other HumioSavedQuery is managing the same LogScale saved query
	// Note: This check happens before creation/adoption to prevent TOCTOU issues
	// Only check on spec changes (Ready condition's observed generation doesn't match current generation) to avoid expensive O(n) checks on every reconciliation
	readyCondition := meta.FindStatusCondition(hsq.Status.Conditions, SavedQueryConditionTypeReady)
	needsDuplicateCheck := readyCondition == nil || readyCondition.ObservedGeneration != hsq.Generation
	if needsDuplicateCheck {
		r.Log.V(1).Info("Checking for duplicate saved query names (spec changed or first reconciliation)")
		duplicateErr := r.checkForDuplicateSavedQueryName(ctx, hsq)
		if duplicateErr != nil {
			r.Log.Error(duplicateErr, "Duplicate saved query name detected")
			_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeReady,
				metav1.ConditionFalse, SavedQueryReasonNameCollision,
				duplicateErr.Error())
			return reconcile.Result{}, r.logErrorAndReturn(duplicateErr, "duplicate saved query name detected")
		}
	} else {
		r.Log.V(1).Info("Skipping duplicate check - no spec changes detected")
	}

	// Check for view migration (ViewName changed)
	r.Log.Info("Checking if view has changed")
	if hsq.Status.LastSyncedViewName != "" && hsq.Status.LastSyncedViewName != hsq.Spec.ViewName {
		r.Log.Info("View name changed, initiating migration", "oldView", hsq.Status.LastSyncedViewName, "newView", hsq.Spec.ViewName)

		// Create a temporary object with the old view name to delete from the old view
		oldHsq := hsq.DeepCopy()
		oldHsq.Spec.ViewName = hsq.Status.LastSyncedViewName

		// Delete from old view (ignore not found errors)
		err := r.HumioClient.DeleteSavedQuery(ctx, client, oldHsq)
		if err != nil && !errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Error(err, "Failed to delete saved query from old view during migration", "oldView", hsq.Status.LastSyncedViewName)
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not delete saved query from old view")
		}
		r.Log.Info("Successfully deleted saved query from old view", "oldView", hsq.Status.LastSyncedViewName)

		// Now proceed to create in new view - fall through to create logic below
	}

	r.Log.Info("Checking if saved query needs to be created or adopted")
	// Get current state from LogScale - use version-aware method
	curSavedQuery, err := r.getSavedQueryVersionAware(ctx, client, hsq)

	// Check if the view/repository doesn't exist
	var searchDomainNotFound humioapi.EntityNotFound
	if errors.As(err, &searchDomainNotFound) && searchDomainNotFound.EntityType() == "search-domain" {
		errorMsg := fmt.Sprintf("View/repository '%s' does not exist in LogScale. Create the view/repository first.", hsq.Spec.ViewName)
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionFalse, SavedQueryReasonConfigurationError, errorMsg)
		return reconcile.Result{}, r.logErrorAndReturn(err, errorMsg)
	}

	// Check if the saved query doesn't exist (but the view does)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		// Saved query doesn't exist in LogScale - create it using version-aware method
		r.Log.Info("Saved query doesn't exist. Creating saved query")
		if err := r.addSavedQueryVersionAware(ctx, client, hsq); err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create saved query")
		}
		r.Log.Info("Created saved query")
		// Set both Ready and Synced conditions so defer block skips verification
		// This prevents race conditions where defer block might not find the newly created query
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionTrue, SavedQueryReasonSavedQueryCreated, "Saved query created in LogScale")
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionTrue, SavedQueryReasonConfigurationSynced, "Saved query created")
		_ = r.updateLastSyncedViewName(ctx, hsq)
		_ = r.setManagedByOperator(ctx, hsq, true)
		return reconcile.Result{Requeue: true}, nil
	}
	if err != nil {
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionFalse, SavedQueryReasonConfigurationError, fmt.Sprintf("Could not check if saved query exists: %v", err))
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if saved query exists")
	}

	// Saved query exists in LogScale - check if this is an adoption scenario
	// Adoption occurs when we found a query but haven't synced to this view yet
	wasAdoption := hsq.Status.LastSyncedViewName == ""

	r.Log.V(1).Info("Checking if saved query needs to be updated",
		"wasAdoption", wasAdoption,
		"lastSyncedView", hsq.Status.LastSyncedViewName,
		"currentView", hsq.Spec.ViewName,
	)

	// Compare existing configuration with desired state
	// Determine if we should compare description/labels based on version
	useV1, err := r.determineAPIVersion(ctx, hsq)
	if err != nil {
		r.Log.V(1).Info("Could not determine API version, skipping description/labels comparison", "error", err)
		useV1 = true // Default to V1 (safer - doesn't compare description/labels)
	}
	compareDescriptionAndLabels := !useV1 // V2 API includes description/labels
	expectedAsActual, diff := savedQueryAlreadyAsExpected(hsq, curSavedQuery, compareDescriptionAndLabels)

	// If this was an adoption attempt, handle it specially
	if wasAdoption {
		return r.handleSavedQueryAdoption(ctx, hsq, expectedAsActual, diff)
	}

	// Normal case: saved query exists and is already managed by this CRD
	if !expectedAsActual {
		r.Log.Info("Information differs, triggering update", "diff", diff)
		err = r.updateSavedQueryVersionAware(ctx, client, hsq)
		if err != nil {
			_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionFalse, SavedQueryReasonSyncFailed, fmt.Sprintf("Failed to update saved query: %v", err))
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update saved query")
		}
		r.Log.Info("Updated saved query")
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionTrue, SavedQueryReasonSavedQueryUpdated, "Saved query updated in LogScale")
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionTrue, SavedQueryReasonConfigurationSynced, "Saved query updated")
		_ = r.updateLastSyncedViewName(ctx, hsq)
		// Ensure ManagedByOperator is set
		if hsq.Status.ManagedByOperator == nil || !*hsq.Status.ManagedByOperator {
			_ = r.setManagedByOperator(ctx, hsq, true)
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// Transition lifecycle events to steady state
	r.transitionLifecycleCondition(ctx, hsq)

	// Configuration is synced
	_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionTrue, SavedQueryReasonConfigurationSynced, "Configuration matches desired state")
	_ = r.updateLastSyncedViewName(ctx, hsq)

	// Ensure ManagedByOperator is set (in case it wasn't persisted during adoption/creation)
	if hsq.Status.ManagedByOperator == nil || !*hsq.Status.ManagedByOperator {
		_ = r.setManagedByOperator(ctx, hsq, true)
	}

	// Everything is good, requeue after standard interval
	r.Log.Info("Done reconciling, will requeue after standard interval")
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// handleSavedQueryAdoption handles the case where we found a saved query by name
// and need to decide whether to adopt it or reject adoption based on configuration match.
func (r *HumioSavedQueryReconciler) handleSavedQueryAdoption(
	ctx context.Context,
	hsq *humiov1alpha1.HumioSavedQuery,
	configMatches bool,
	diff map[string]string,
) (reconcile.Result, error) {
	if configMatches {
		// Adoption success - configuration matches
		r.Log.Info("adopted existing saved query from LogScale",
			"name", hsq.Spec.Name,
			"view", hsq.Spec.ViewName,
		)
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionTrue, SavedQueryReasonAdoptionSuccessful,
			fmt.Sprintf("Successfully adopted existing saved query '%s' from LogScale", hsq.Spec.Name))
		_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionTrue, SavedQueryReasonConfigurationSynced,
			"Configuration matches existing saved query")
		_ = r.updateLastSyncedViewName(ctx, hsq)
		_ = r.setManagedByOperator(ctx, hsq, true)
		return reconcile.Result{Requeue: true}, nil
	}

	// Adoption rejected - configuration doesn't match
	errorMsg := fmt.Sprintf("Cannot adopt existing saved query '%s' - configuration mismatch: %v. "+
		"Either delete the existing saved query in LogScale, or update the CRD spec to match the existing configuration.",
		hsq.Spec.Name, diff)

	r.Log.Info("rejecting adoption due to configuration mismatch",
		"name", hsq.Spec.Name,
		"view", hsq.Spec.ViewName,
		"diff", diff,
	)

	_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeReady, metav1.ConditionFalse, SavedQueryReasonAdoptionRejected, errorMsg)
	_ = r.setCondition(ctx, hsq, SavedQueryConditionTypeSynced, metav1.ConditionFalse, SavedQueryReasonConfigurationDrift,
		fmt.Sprintf("Configuration differs from existing saved query: %v", diff))
	_ = r.setManagedByOperator(ctx, hsq, false)

	return reconcile.Result{}, r.logErrorAndReturn(
		fmt.Errorf("saved query '%s' already exists in view '%s' with different configuration", hsq.Spec.Name, hsq.Spec.ViewName),
		errorMsg,
	)
}

func (r *HumioSavedQueryReconciler) setCondition(ctx context.Context, hsq *humiov1alpha1.HumioSavedQuery, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	// Use retry logic to handle conflicts during status updates
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch latest version to avoid conflicts
		latest := &humiov1alpha1.HumioSavedQuery{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hsq), latest); err != nil {
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
		return r.Status().Update(ctx, latest)
	})
}

func (r *HumioSavedQueryReconciler) updateLastSyncedViewName(ctx context.Context, hsq *humiov1alpha1.HumioSavedQuery) error {
	// Use retry logic to handle conflicts during status updates
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch latest version to avoid conflicts
		latest := &humiov1alpha1.HumioSavedQuery{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hsq), latest); err != nil {
			return err
		}

		// Only update if changed
		if latest.Status.LastSyncedViewName == hsq.Spec.ViewName {
			return nil
		}

		latest.Status.LastSyncedViewName = hsq.Spec.ViewName
		return r.Status().Update(ctx, latest)
	})
}

func (r *HumioSavedQueryReconciler) setManagedByOperator(ctx context.Context, hsq *humiov1alpha1.HumioSavedQuery, managed bool) error {
	// Use retry logic to handle conflicts during status updates
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch latest version to avoid conflicts
		latest := &humiov1alpha1.HumioSavedQuery{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hsq), latest); err != nil {
			return err
		}

		// Only update if changed
		if latest.Status.ManagedByOperator != nil && *latest.Status.ManagedByOperator == managed {
			return nil
		}

		latest.Status.ManagedByOperator = &managed
		return r.Status().Update(ctx, latest)
	})
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

// isPermanentError classifies whether an error during finalizer execution is permanent or transient.
// Permanent errors indicate that we should give up and remove the finalizer (e.g., resource already deleted, view doesn't exist).
// Transient errors indicate we should retry (e.g., network issues, temporary API unavailability).
func (r *HumioSavedQueryReconciler) isPermanentError(err error) bool {
	if err == nil {
		return false
	}

	// EntityNotFound errors are permanent - resource is already gone
	var entityNotFound humioapi.EntityNotFound
	if errors.As(err, &entityNotFound) {
		return true
	}

	// Check for specific error messages that indicate permanent conditions
	errMsg := err.Error()

	// View/repository doesn't exist - permanent
	if strings.Contains(errMsg, "Could not find") ||
		strings.Contains(errMsg, "does not exist") ||
		strings.Contains(errMsg, "not found") {
		return true
	}

	// Permission denied - usually permanent
	if strings.Contains(errMsg, "permission denied") ||
		strings.Contains(errMsg, "unauthorized") ||
		strings.Contains(errMsg, "forbidden") ||
		strings.Contains(errMsg, "authentication") {
		return true
	}

	// Invalid configuration - permanent
	if strings.Contains(errMsg, "invalid") ||
		strings.Contains(errMsg, "malformed") {
		return true
	}

	// Context errors are transient (timeout, cancellation)
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Network errors are typically transient
	if strings.Contains(errMsg, "connection") ||
		strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "dial") {
		return false
	}

	// Default to transient (safer - we'll retry)
	return false
}

func savedQueryAlreadyAsExpected(fromKubernetes *humiov1alpha1.HumioSavedQuery, fromGraphQL *humiographql.SavedQueryDetailsV2, compareDescriptionAndLabels bool) (bool, map[string]string) {
	keyValues := map[string]string{}

	// Compare query string (always)
	if diff := cmp.Diff(fromGraphQL.Query.QueryString, fromKubernetes.Spec.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}

	// Compare description and labels if version supports them
	if compareDescriptionAndLabels {
		// Compare description
		expectedDesc := fromKubernetes.Spec.Description
		actualDesc := ""
		if fromGraphQL.Description != nil {
			actualDesc = *fromGraphQL.Description
		}
		if diff := cmp.Diff(actualDesc, expectedDesc); diff != "" {
			keyValues["description"] = diff
		}

		// Compare labels (use sorted comparison for order independence)
		if diff := cmp.Diff(fromGraphQL.Labels, fromKubernetes.Spec.Labels); diff != "" {
			keyValues["labels"] = diff
		}
	}

	return len(keyValues) == 0, keyValues
}

// checkForDuplicateSavedQueryName validates that no other HumioSavedQuery resource
// is managing the same LogScale saved query name (spec.name + spec.viewName + cluster).
// This prevents multiple Kubernetes resources from fighting over the same LogScale saved query.
func (r *HumioSavedQueryReconciler) checkForDuplicateSavedQueryName(ctx context.Context, hsq *humiov1alpha1.HumioSavedQuery) error {
	// List all HumioSavedQuery resources in the same namespace
	var savedQueryList humiov1alpha1.HumioSavedQueryList
	if err := r.List(ctx, &savedQueryList, client.InNamespace(hsq.Namespace)); err != nil {
		return fmt.Errorf("failed to list saved queries: %w", err)
	}

	// Determine which cluster field to compare
	clusterName := hsq.Spec.ManagedClusterName
	if clusterName == "" {
		clusterName = hsq.Spec.ExternalClusterName
	}

	// Check for duplicates with same spec.name + spec.viewName + cluster
	for _, other := range savedQueryList.Items {
		// Skip self (same UID)
		if other.UID == hsq.UID {
			continue
		}

		// Determine other's cluster name
		otherClusterName := other.Spec.ManagedClusterName
		if otherClusterName == "" {
			otherClusterName = other.Spec.ExternalClusterName
		}

		// Skip if different cluster
		if otherClusterName != clusterName {
			continue
		}

		// Skip if different view
		if other.Spec.ViewName != hsq.Spec.ViewName {
			continue
		}

		// Check for duplicate LogScale name
		if other.Spec.Name == hsq.Spec.Name {
			return fmt.Errorf("cannot manage saved query '%s' in view '%s' - already managed by HumioSavedQuery '%s/%s'. Each LogScale saved query must be managed by exactly one Kubernetes resource",
				hsq.Spec.Name, hsq.Spec.ViewName, other.Namespace, other.Name)
		}
	}

	return nil
}
