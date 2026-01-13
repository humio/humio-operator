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
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// Status update retry configuration
	statusUpdateMaxRetries   = 5
	statusUpdateBaseDelayMs  = 100
	statusUpdateJitterFactor = 2
	statusUpdateGetTimeout   = 2 * time.Second

	// DoS protection limits for Kafka properties
	maxPropertiesLength    = 1024 * 1024 // 1MB
	maxPropertiesCount     = 1000
	maxPropertyKeyLength   = 256
	maxPropertyValueLength = 65536 // 64KB for long values like JAAS config

	// Reconciliation intervals
	requeueAfterOnError = 5 * time.Second
)

// HumioEventForwarderReconciler reconciles a HumioEventForwarder object
type HumioEventForwarderReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioeventforwarders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioeventforwarders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioeventforwarders/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// retryStatusUpdateWithBackoff performs a status update operation with exponential backoff and jitter.
// It handles resource version conflicts by fetching the latest version before each attempt.
// The updateFn receives the latest version of the resource and should perform the desired status changes.
// Returns an error if all retry attempts are exhausted or context is canceled.
func (r *HumioEventForwarderReconciler) retryStatusUpdateWithBackoff(
	ctx context.Context,
	hef *humiov1alpha1.HumioEventForwarder,
	updateFn func(*humiov1alpha1.HumioEventForwarder) error,
) error {
	for attempt := 0; attempt < statusUpdateMaxRetries; attempt++ {
		// Check context cancellation before retry
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Fetch the latest version from the API server with timeout
		getCtx, cancel := context.WithTimeout(ctx, statusUpdateGetTimeout)
		latest := &humiov1alpha1.HumioEventForwarder{}
		err := r.Get(getCtx, types.NamespacedName{Namespace: hef.Namespace, Name: hef.Name}, latest)
		cancel()

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ctx.Err()
			}
			return fmt.Errorf("failed to fetch latest version: %w", err)
		}

		// Apply the update function to the latest version
		if err := updateFn(latest); err != nil {
			return fmt.Errorf("update function failed: %w", err)
		}

		// Attempt to update the status
		if err := r.Status().Update(ctx, latest); err != nil {
			if k8serrors.IsConflict(err) && attempt < statusUpdateMaxRetries-1 {
				// Resource version conflict - retry with backoff and jitter
				r.Log.V(1).Info("conflict updating status, retrying", "attempt", attempt+1)
				baseDelay := time.Millisecond * statusUpdateBaseDelayMs * time.Duration(attempt+1)

				// Add jitter only if baseDelay is positive to avoid divide-by-zero
				var jitter time.Duration
				if baseDelay > 0 {
					maxJitter := int64(baseDelay) / statusUpdateJitterFactor
					if maxJitter > 0 {
						n, err := rand.Int(rand.Reader, big.NewInt(maxJitter))
						if err == nil {
							jitter = time.Duration(n.Int64())
						}
					}
				}

				// Sleep with context awareness
				timer := time.NewTimer(baseDelay + jitter)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
					timer.Stop() // Clean up timer after use to prevent leak
				}
				continue
			}
			return fmt.Errorf("failed to update status after %d attempts: %w", attempt+1, err)
		}

		// Success
		return nil
	}

	return fmt.Errorf("exhausted all %d retry attempts", statusUpdateMaxRetries)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioEventForwarderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioEventForwarder")

	hef := &humiov1alpha1.HumioEventForwarder{}
	err := r.Get(ctx, req.NamespacedName, hef)
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

	r.Log = r.Log.WithValues("Request.UID", hef.UID)

	cluster, err := helpers.NewCluster(ctx, r, hef.Spec.ManagedClusterName, hef.Spec.ExternalClusterName, hef.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
			metav1.ConditionFalse, humiov1alpha1.EventForwarderReasonConfigError,
			fmt.Sprintf("Unable to obtain cluster config: %s", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set condition")
		}
		return reconcile.Result{RequeueAfter: requeueAfterOnError}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(parentCtx context.Context) {
		// Skip status update during deletion by checking if resource still exists
		// and hasn't been deleted
		// Use a fresh context derived from parent to avoid cancellation issues
		cleanupCtx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
		defer cancel()

		// Create a reference object with just namespace/name for retry function
		hefRef := &humiov1alpha1.HumioEventForwarder{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: hef.Namespace,
				Name:      hef.Name,
			},
		}

		// Use retryStatusUpdateWithBackoff which handles fetching latest version
		err := r.retryStatusUpdateWithBackoff(cleanupCtx, hefRef, func(fresh *humiov1alpha1.HumioEventForwarder) error {
			// Skip if resource is being deleted
			if fresh.DeletionTimestamp != nil {
				return nil
			}

			// Skip if Ready condition is already set by main reconciliation logic
			// This prevents defer block from racing with lifecycle event status updates
			readyCondition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
			if readyCondition != nil {
				// Skip for permanent failure reasons that should not be overwritten
				if readyCondition.Status == metav1.ConditionFalse && readyCondition.Reason == humiov1alpha1.EventForwarderReasonAdoptionRejected {
					r.Log.V(1).Info("skipping defer status update - permanent failure condition present", "reason", readyCondition.Reason)
					return nil
				}
				// Skip for lifecycle event reasons that were just set by main reconciliation
				// This prevents race conditions between defer block and main reconciliation status updates
				if readyCondition.Status == metav1.ConditionTrue {
					switch readyCondition.Reason {
					case humiov1alpha1.EventForwarderReasonCreated,
						humiov1alpha1.EventForwarderReasonUpdated,
						humiov1alpha1.EventForwarderReasonAdoptionSuccessful:
						r.Log.V(1).Info("skipping defer status update - lifecycle event just occurred", "reason", readyCondition.Reason)
						return nil
					}
				}
			}

			_, err := r.HumioClient.GetEventForwarder(cleanupCtx, humioHttpClient, fresh)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				r.Log.Info("defer block: event forwarder not found in LogScale", "name", fresh.Spec.Name)
				meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{
					Type:               humiov1alpha1.EventForwarderConditionTypeReady,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: fresh.Generation,
					LastTransitionTime: metav1.Now(),
					Reason:             humiov1alpha1.EventForwarderReasonNotFound,
					Message:            "Event forwarder not found in LogScale",
				})
				return r.Status().Update(cleanupCtx, fresh)
			}
			if err != nil {
				r.Log.Info("defer block: error getting event forwarder", "error", err, "name", fresh.Spec.Name)
				meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{
					Type:               humiov1alpha1.EventForwarderConditionTypeReady,
					Status:             metav1.ConditionFalse,
					ObservedGeneration: fresh.Generation,
					LastTransitionTime: metav1.Now(),
					Reason:             humiov1alpha1.EventForwarderReasonConfigError,
					Message:            fmt.Sprintf("Failed to get event forwarder: %v", err),
				})
				return r.Status().Update(cleanupCtx, fresh)
			}
			r.Log.V(1).Info("defer block: verified event forwarder exists in LogScale", "name", fresh.Spec.Name)
			meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{
				Type:               humiov1alpha1.EventForwarderConditionTypeReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: fresh.Generation,
				LastTransitionTime: metav1.Now(),
				Reason:             humiov1alpha1.EventForwarderReasonReady,
				Message:            "Event forwarder is ready",
			})
			return r.Status().Update(cleanupCtx, fresh)
		})
		if err != nil && !k8serrors.IsNotFound(err) {
			r.Log.Error(err, "Failed to update Ready condition in defer block after retries")
		}
	}(ctx)

	return r.reconcileHumioEventForwarder(ctx, humioHttpClient, hef)
}

// ensureFinalizer checks if the resource has the finalizer and adds it if missing.
// Returns:
//   - (Result, error) for reconciliation
//   - bool indicating if finalizer was added (true = caller should return to requeue)
func (r *HumioEventForwarderReconciler) ensureFinalizer(
	ctx context.Context,
	hef *humiov1alpha1.HumioEventForwarder,
) (reconcile.Result, error, bool) {
	if helpers.ContainsElement(hef.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil, false // Finalizer already present
	}

	r.Log.Info("Finalizer not present, adding finalizer to event forwarder")
	hef.SetFinalizers(append(hef.GetFinalizers(), HumioFinalizer))
	err := r.Update(ctx, hef)
	if err != nil {
		return reconcile.Result{}, err, true
	}

	return reconcile.Result{Requeue: true}, nil, true
}

// transitionLifecycleCondition transitions one-time lifecycle event reasons
// (Created/Updated/AdoptionSuccessful) to steady-state Ready reason.
// Errors are logged but not returned as they should not block reconciliation.
func (r *HumioEventForwarderReconciler) transitionLifecycleCondition(
	ctx context.Context,
	hef *humiov1alpha1.HumioEventForwarder,
) {
	readyCondition := meta.FindStatusCondition(hef.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
	if readyCondition == nil {
		return
	}

	if readyCondition.Status != metav1.ConditionTrue {
		return
	}

	// Check if reason is a lifecycle event
	isLifecycleEvent := readyCondition.Reason == humiov1alpha1.EventForwarderReasonCreated ||
		readyCondition.Reason == humiov1alpha1.EventForwarderReasonUpdated ||
		readyCondition.Reason == humiov1alpha1.EventForwarderReasonAdoptionSuccessful

	if !isLifecycleEvent {
		return
	}

	// Transition to steady-state reason
	if err := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
		metav1.ConditionTrue, humiov1alpha1.EventForwarderReasonReady,
		"Event forwarder is ready"); err != nil {
		r.Log.Error(err, "failed to transition to EventForwarderReady")
	}
}

// handleResourceDeletion handles the deletion flow for an event forwarder.
// Returns:
//   - (Result, error) for reconciliation
//   - bool indicating if deletion was handled (true = caller should return immediately)
func (r *HumioEventForwarderReconciler) handleResourceDeletion(
	ctx context.Context,
	client *humioapi.Client,
	hef *humiov1alpha1.HumioEventForwarder,
) (reconcile.Result, error, bool) {
	r.Log.Info("Checking if event forwarder is marked to be deleted")
	if hef.GetDeletionTimestamp() == nil {
		return reconcile.Result{}, nil, false
	}

	r.Log.Info("Event forwarder marked to be deleted")
	if !helpers.ContainsElement(hef.GetFinalizers(), HumioFinalizer) {
		// No finalizer, nothing to do
		return reconcile.Result{}, nil, true
	}

	// Check if we ever adopted/created this forwarder
	// We use ManagedByOperator as the primary indicator (more explicit)
	// Fall back to EventForwarderID check for backwards compatibility
	if hef.Status.ManagedByOperator != nil && !*hef.Status.ManagedByOperator {
		r.Log.Info("Resource not managed by operator (adoption rejected), removing finalizer without deleting from LogScale")
		hef.SetFinalizers(helpers.RemoveElement(hef.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hef)
		if err != nil {
			return reconcile.Result{}, err, true
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil, true
	}

	// If no ID and ManagedByOperator is not explicitly set, we never took ownership - just remove the finalizer
	if hef.Status.EventForwarderID == "" {
		r.Log.Info("No forwarder ID in status - never adopted or created, removing finalizer")
		hef.SetFinalizers(helpers.RemoveElement(hef.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hef)
		if err != nil {
			return reconcile.Result{}, err, true
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil, true
	}

	// We have an ID - verify the forwarder exists before trying to delete
	_, err := r.HumioClient.GetEventForwarder(ctx, client, hef)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		// Forwarder already deleted, just remove finalizer
		hef.SetFinalizers(helpers.RemoveElement(hef.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hef)
		if err != nil {
			return reconcile.Result{}, err, true
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil, true
	}
	if err != nil {
		// Classify error to decide whether to retry or remove finalizer
		if r.isPermanentError(err) {
			// Permanent error (e.g., cluster unreachable, authentication failure)
			// Remove finalizer to unblock deletion
			r.Log.Info("Permanent error checking if forwarder exists, removing finalizer anyway", "error", err)
			hef.SetFinalizers(helpers.RemoveElement(hef.GetFinalizers(), HumioFinalizer))
			updateErr := r.Update(ctx, hef)
			if updateErr != nil {
				return reconcile.Result{}, updateErr, true
			}
			r.Log.Info("Finalizer removed successfully despite permanent error")
			return reconcile.Result{Requeue: true}, nil, true
		}

		// Transient error - retry
		r.Log.Info("Transient error checking if forwarder exists, will retry", "error", err)
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to check if forwarder exists during deletion"), true
	}

	// Check if any rules are using this forwarder before attempting deletion
	r.Log.Info("Checking for dependent event forwarding rules before deletion")
	rules, err := r.checkForDependentRules(ctx, client, hef)
	if err != nil {
		// In test/dummy mode, the GraphQL call will fail with "connection refused"
		// Treat this as "no dependent rules found" and proceed with deletion
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "dial tcp") {
			r.Log.Info("Unable to check for dependent rules (likely test/dummy mode), assuming no dependencies", "error", err)
			rules = nil
			err = nil
		} else {
			return reconcile.Result{}, r.logErrorAndReturn(err, "failed to check for dependent rules"), true
		}
	}
	if len(rules) > 0 {
		errMsg := fmt.Sprintf("Cannot delete event forwarder - it is used by %d event forwarding rule(s). Delete the rules first: %s",
			len(rules), strings.Join(rules, ", "))
		r.Log.Info("deletion blocked by dependent rules", "ruleCount", len(rules), "rules", rules)
		// Don't change Ready status - just block deletion and requeue
		return reconcile.Result{RequeueAfter: 15 * time.Second}, r.logErrorAndReturn(errors.New(errMsg), "event forwarder has dependent rules"), true
	}

	// Run finalization logic for humioFinalizer. If the
	// finalization logic fails, don't remove the finalizer so
	// that we can retry during the next reconciliation.
	r.Log.Info("Deleting event forwarder")
	if err := r.HumioClient.DeleteEventForwarder(ctx, client, hef); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "Delete event forwarder returned error"), true
	}

	// Remove finalizer after successful deletion
	r.Log.Info("Event forwarder deleted successfully, removing finalizer")
	hef.SetFinalizers(helpers.RemoveElement(hef.GetFinalizers(), HumioFinalizer))
	err = r.Update(ctx, hef)
	if err != nil {
		return reconcile.Result{}, err, true
	}
	r.Log.Info("Finalizer removed successfully")
	return reconcile.Result{}, nil, true
}

func (r *HumioEventForwarderReconciler) reconcileHumioEventForwarder(ctx context.Context, client *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) (reconcile.Result, error) {
	// Handle deletion
	result, err, handled := r.handleResourceDeletion(ctx, client, hef)
	if handled {
		return result, err
	}

	// Ensure finalizer is present
	result, err, added := r.ensureFinalizer(ctx, hef)
	if added {
		return result, err
	}

	// Validate that no other HumioEventForwarder in the cluster has the same spec.name + managedClusterName
	// Only check on spec changes (Ready condition's observed generation doesn't match current generation) to avoid expensive O(n) checks on every reconciliation
	readyCondition := meta.FindStatusCondition(hef.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
	needsDuplicateCheck := readyCondition == nil || readyCondition.ObservedGeneration != hef.Generation
	if needsDuplicateCheck {
		r.Log.V(1).Info("Checking for duplicate forwarder names in cluster (spec changed or first reconciliation)")
		duplicateErr := r.checkForDuplicateForwarderName(ctx, hef)
		if duplicateErr != nil {
			_ = r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
				metav1.ConditionFalse, humiov1alpha1.EventForwarderReasonConfigError,
				duplicateErr.Error())
			return reconcile.Result{}, r.logErrorAndReturn(duplicateErr, "duplicate forwarder name detected")
		}
	} else {
		r.Log.V(1).Info("Skipping duplicate check - no spec changes detected")
	}

	r.Log.V(1).Info("Checking if event forwarder needs to be created")

	// Fetch and merge Kafka properties early for accurate comparison
	mergedProperties, propErr := r.fetchKafkaProperties(ctx, hef)
	if propErr != nil {
		sanitizedErr := r.sanitizeSecretError(propErr)
		// Set status condition before returning error so defer block doesn't overwrite it
		_ = r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
			metav1.ConditionFalse, humiov1alpha1.EventForwarderReasonReconcileError,
			fmt.Sprintf("Failed to fetch Kafka properties: %s", sanitizedErr.Error()))
		return reconcile.Result{}, r.logErrorAndReturn(sanitizedErr, "failed to fetch Kafka properties")
	}

	// Create a copy with merged properties for GetEventForwarder lookup
	// This avoids mutating the original hef.Spec.KafkaConfig.Properties
	hefWithMergedProps := hef.DeepCopy()
	hefWithMergedProps.Spec.KafkaConfig.Properties = mergedProperties

	// Add EventForwarder or adopt existing one by name
	curForwarder, err := r.HumioClient.GetEventForwarder(ctx, client, hefWithMergedProps)

	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("event forwarder doesn't exist. Now adding event forwarder")

			// Use the copy with merged properties for creation
			addErr := r.HumioClient.AddEventForwarder(ctx, client, hefWithMergedProps)

			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, fmt.Sprintf("could not create event forwarder '%s'", hef.Spec.Name))
			}

			// Store the forwarder ID returned from AddEventForwarder
			// The AddEventForwarder method populates the status on the copy
			forwarderID := hefWithMergedProps.Status.EventForwarderID

			// Update status with retry logic to handle conflicts
			if err := r.retryStatusUpdateWithBackoff(ctx, hef, func(latest *humiov1alpha1.HumioEventForwarder) error {
				latest.Status.EventForwarderID = forwarderID
				return r.Status().Update(ctx, latest)
			}); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("could not update event forwarder '%s' status", hef.Spec.Name))
			}

			r.Log.Info("created event forwarder",
				"Forwarder", hef.Spec.Name,
				"Type", hef.Spec.ForwarderType,
			)
			// Set Ready condition with lifecycle event reason so defer block skips verification
			_ = r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
				metav1.ConditionTrue, humiov1alpha1.EventForwarderReasonCreated,
				"Event forwarder created in LogScale")
			_ = r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeSynced,
				metav1.ConditionTrue, humiov1alpha1.EventForwarderReasonConfigurationSynced,
				"Event forwarder created")
			_ = r.setManagedByOperator(ctx, hef, true)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if event forwarder exists")
	}

	// GetEventForwarder found a forwarder (either by ID or by name adoption)
	// Check if this was an adoption scenario and verify properties match
	// Adoption occurs when we found a forwarder but don't have an ID in status yet
	wasAdoption := hef.Status.EventForwarderID == ""
	forwarderID := curForwarder.GetId()

	r.Log.V(1).Info("Checking if event forwarder needs to be updated",
		"wasAdoption", wasAdoption,
		"forwarderID", forwarderID,
		"statusID", hef.Status.EventForwarderID,
	)

	// Compare using copy with merged properties
	asExpected, diffKeysAndValues, compareErr := eventForwarderAlreadyAsExpected(hefWithMergedProps, curForwarder)
	if compareErr != nil {
		r.Log.Error(compareErr, "failed to compare event forwarder configuration")
		// Set error condition before returning
		_ = r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
			metav1.ConditionFalse, humiov1alpha1.EventForwarderReasonReconcileError,
			fmt.Sprintf("Failed to compare configuration: %v", compareErr))
		return reconcile.Result{}, r.logErrorAndReturn(compareErr, "failed to compare event forwarder configuration")
	}

	// If this was an adoption attempt, handle it with helper method
	if wasAdoption {
		return r.handleEventForwarderAdoption(ctx, hef, forwarderID, asExpected, diffKeysAndValues)
	}

	// Normal case: forwarder exists and is already managed by this CRD
	// Verify the ID we have matches what we got from LogScale (if we have one)
	// Handle edge case where forwarder was recreated externally with different ID
	// This should not happen during normal adoption flow (wasAdoption flag prevents it)
	if hef.Status.EventForwarderID != "" && hef.Status.EventForwarderID != forwarderID {
		r.Log.Info("forwarder ID mismatch - updating status with correct ID",
			"statusID", hef.Status.EventForwarderID,
			"logScaleID", forwarderID,
		)
		// This could happen if the forwarder was recreated in LogScale with same name
		// Update our status to reflect the new ID
		if err := r.retryStatusUpdateWithBackoff(ctx, hef, func(latest *humiov1alpha1.HumioEventForwarder) error {
			latest.Status.EventForwarderID = forwarderID
			return r.Status().Update(ctx, latest)
		}); err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update status with correct forwarder ID")
		}
		// Requeue to pick up the new ID in next reconciliation
		return reconcile.Result{Requeue: true}, nil
	}

	// Check if configuration needs updating
	if !asExpected {
		r.Log.Info("event forwarder configuration differs, triggering update",
			"diff", diffKeysAndValues,
		)

		// Set synced condition to false before attempting update
		if err := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeSynced,
			metav1.ConditionFalse, humiov1alpha1.EventForwarderReasonConfigurationDrifted,
			fmt.Sprintf("Configuration has drifted from desired state: %v", diffKeysAndValues)); err != nil {
			r.Log.Error(err, "failed to update synced condition before update")
		}

		// Check if we have an ID in status - if not, this is an adoption rejection
		// We can't update a forwarder we don't own
		if hef.Status.EventForwarderID == "" {
			errorMsg := fmt.Sprintf("Cannot adopt existing event forwarder - configuration mismatch: %v. "+
				"Either delete the existing forwarder in LogScale, or update the CRD spec to match the existing configuration.",
				diffKeysAndValues)

			// Set Ready: False since we can't manage this forwarder
			if err := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
				metav1.ConditionFalse, humiov1alpha1.EventForwarderReasonConfigError,
				errorMsg); err != nil {
				r.Log.Error(err, "failed to set ready condition for adoption rejection")
			}

			return reconcile.Result{}, r.logErrorAndReturn(
				fmt.Errorf("event forwarder '%s' already exists in LogScale with different configuration", hef.Spec.Name),
				errorMsg,
			)
		}

		// Use copy with merged properties for update
		err = r.HumioClient.UpdateEventForwarder(ctx, client, hefWithMergedProps)

		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update event forwarder")
		}
		r.Log.Info("updated event forwarder",
			"Forwarder", hef.Spec.Name,
		)

		// Set Ready condition with lifecycle event reason
		if err := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
			metav1.ConditionTrue, humiov1alpha1.EventForwarderReasonUpdated,
			"Event forwarder updated in LogScale"); err != nil {
			r.Log.V(1).Info("failed to update ready condition after update", "error", err)
		}

		// Set synced condition to true after successful update
		if err := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeSynced,
			metav1.ConditionTrue, humiov1alpha1.EventForwarderReasonConfigurationSynced,
			"Configuration has been synchronized with LogScale"); err != nil {
			r.Log.V(1).Info("failed to update synced condition after update", "error", err)
		}
		return reconcile.Result{Requeue: true}, nil
	} else {
		// Set synced condition if already as expected
		if err := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeSynced,
			metav1.ConditionTrue, humiov1alpha1.EventForwarderReasonConfigurationSynced,
			"Configuration matches desired state"); err != nil {
			r.Log.V(1).Info("failed to update synced condition", "error", err)
		}
	}

	// Transition lifecycle events to steady state
	r.transitionLifecycleCondition(ctx, hef)

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// fetchKafkaProperties fetches and merges Kafka properties from both plaintext and secret sources.
// Properties from the secret take precedence over plaintext properties if there are conflicts.
// Returns a normalized, sorted string of properties suitable for comparison and API calls.
//
// Properties merging behavior:
//   - Properties are parsed as key=value pairs, one per line
//   - Comments starting with # are ignored
//   - Blank lines are ignored
//   - Leading and trailing whitespace is trimmed from keys and values
//   - If a key appears multiple times, the last value wins
//   - Properties from propertiesSecretRef override properties from the properties field
//   - Result is normalized and sorted for consistent comparison
func (r *HumioEventForwarderReconciler) fetchKafkaProperties(ctx context.Context, hef *humiov1alpha1.HumioEventForwarder) (string, error) {
	if hef.Spec.KafkaConfig == nil {
		return "", nil
	}

	// Start with plaintext properties from spec
	mergedProperties := hef.Spec.KafkaConfig.Properties

	// If PropertiesSecretRef is specified, fetch secret and merge
	if hef.Spec.KafkaConfig.PropertiesSecretRef != nil {
		secretNamespace := hef.Spec.KafkaConfig.PropertiesSecretRef.Namespace
		if secretNamespace == "" {
			secretNamespace = hef.Namespace
		}

		// Enforce same-namespace restriction for security
		if secretNamespace != hef.Namespace {
			return "", fmt.Errorf("cross-namespace secret references are not allowed: secret must be in the same namespace as the HumioEventForwarder (%s)", hef.Namespace)
		}

		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      hef.Spec.KafkaConfig.PropertiesSecretRef.Name,
			Namespace: secretNamespace,
		}, secret); err != nil {
			return "", fmt.Errorf("failed to fetch secret %s/%s: %w",
				secretNamespace, hef.Spec.KafkaConfig.PropertiesSecretRef.Name, err)
		}

		secretData, ok := secret.Data[hef.Spec.KafkaConfig.PropertiesSecretRef.Key]
		if !ok {
			return "", fmt.Errorf("key %s not found in secret %s/%s",
				hef.Spec.KafkaConfig.PropertiesSecretRef.Key, secretNamespace,
				hef.Spec.KafkaConfig.PropertiesSecretRef.Name)
		}

		// Merge secret properties (secret takes precedence for duplicate keys)
		if mergedProperties != "" {
			mergedProperties = mergedProperties + "\n" + string(secretData)
		} else {
			mergedProperties = string(secretData)
		}
	}

	// Normalize the merged properties for consistent comparison
	return normalizeKafkaProperties(mergedProperties)
}

// normalizeKafkaProperties takes a properties string and returns a normalized, sorted version
// that can be used for comparison. It handles:
// - Property parsing (key=value format)
// - Whitespace trimming (around keys, values, and =)
// - Comment stripping (lines starting with #)
// - Empty line removal
// - Alphabetical sorting by key
// - Consistent newline format (Unix \n)
// - Windows line ending normalization (\r\n -> \n)
// - Trailing newline removal
// - DoS protection via size and count limits
func normalizeKafkaProperties(properties string) (string, error) {
	if properties == "" {
		return "", nil
	}

	// DoS protection: Check total length
	if len(properties) > maxPropertiesLength {
		return "", fmt.Errorf("properties exceed maximum length of %d bytes", maxPropertiesLength)
	}

	// Parse properties into a map
	propMap := make(map[string]string)

	for _, line := range strings.Split(properties, "\n") {
		// Handle different line ending formats (Windows \r\n -> Unix \n)
		line = strings.TrimRight(line, "\r")
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid property format (expected key=value): %s", line)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			return "", fmt.Errorf("empty property key in line: %s", line)
		}

		// DoS protection: Check key and value lengths
		if len(key) > maxPropertyKeyLength {
			return "", fmt.Errorf("property key too long: %d bytes (max %d)", len(key), maxPropertyKeyLength)
		}
		if len(value) > maxPropertyValueLength {
			return "", fmt.Errorf("property value for key %s too long: %d bytes (max %d)",
				key, len(value), maxPropertyValueLength)
		}

		// Note: If duplicate keys exist, last value wins
		// This is documented behavior and matches standard properties file semantics
		// TODO: Consider logging a warning when duplicates are detected - would require
		// refactoring this function to accept a logger or return warnings alongside result
		propMap[key] = value
	}

	// DoS protection: Check total property count
	if len(propMap) > maxPropertiesCount {
		return "", fmt.Errorf("too many properties: %d (max %d)", len(propMap), maxPropertiesCount)
	}

	// Sort keys and build normalized string
	keys := make([]string, 0, len(propMap))
	for k := range propMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, fmt.Sprintf("%s=%s", k, propMap[k]))
	}

	// Join with Unix newlines, no trailing newline
	return strings.Join(lines, "\n"), nil
}

// handleEventForwarderAdoption handles the case where we found a forwarder by name
// and need to decide whether to adopt it or reject adoption based on configuration match.
// Returns a reconciliation result and error.
func (r *HumioEventForwarderReconciler) handleEventForwarderAdoption(
	ctx context.Context,
	hef *humiov1alpha1.HumioEventForwarder,
	forwarderID string,
	asExpected bool,
	diffKeysAndValues map[string]string,
) (reconcile.Result, error) {
	if asExpected {
		// Adoption success - properties match
		r.Log.Info("adopted existing event forwarder from LogScale",
			"Forwarder", hef.Spec.Name,
			"ID", forwarderID,
		)
		return r.persistAdoptedID(ctx, hef, forwarderID)
	}

	// Adoption rejected - properties don't match
	return r.rejectAdoption(ctx, hef, diffKeysAndValues)
}

// persistAdoptedID updates the status with the adopted forwarder ID
func (r *HumioEventForwarderReconciler) persistAdoptedID(
	ctx context.Context,
	hef *humiov1alpha1.HumioEventForwarder,
	forwarderID string,
) (reconcile.Result, error) {
	// Persist the adopted ID to status
	if err := r.retryStatusUpdateWithBackoff(ctx, hef, func(latest *humiov1alpha1.HumioEventForwarder) error {
		latest.Status.EventForwarderID = forwarderID
		return nil
	}); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("could not update event forwarder '%s' status after adoption", hef.Spec.Name))
	}

	// Set Ready condition with lifecycle event reason after successful adoption
	if err := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeReady,
		metav1.ConditionTrue, humiov1alpha1.EventForwarderReasonAdoptionSuccessful,
		fmt.Sprintf("Successfully adopted existing event forwarder '%s' from LogScale", hef.Spec.Name)); err != nil {
		r.Log.Error(err, "failed to set ready condition after adoption")
	}

	// Set synced condition
	if err := r.setCondition(ctx, hef, humiov1alpha1.EventForwarderConditionTypeSynced,
		metav1.ConditionTrue, humiov1alpha1.EventForwarderReasonConfigurationSynced,
		"Configuration matches existing event forwarder"); err != nil {
		r.Log.Error(err, "failed to set synced condition after adoption")
	}

	// Mark as managed by operator after successful adoption
	if err := r.setManagedByOperator(ctx, hef, true); err != nil {
		r.Log.Error(err, "failed to set ManagedByOperator after adoption")
	}

	return reconcile.Result{Requeue: true}, nil
}

// rejectAdoption handles the case where we cannot adopt due to config mismatch
func (r *HumioEventForwarderReconciler) rejectAdoption(
	ctx context.Context,
	hef *humiov1alpha1.HumioEventForwarder,
	diffKeysAndValues map[string]string,
) (reconcile.Result, error) {
	errorMsg := fmt.Sprintf("Cannot adopt existing event forwarder - configuration mismatch: %v. "+
		"Either delete the existing forwarder in LogScale, or update the CRD spec to match the existing configuration.",
		diffKeysAndValues)

	r.Log.Info("rejecting adoption due to configuration mismatch",
		"name", hef.Spec.Name,
		"diff", diffKeysAndValues,
	)

	// Use retryStatusUpdateWithBackoff to ensure conditions are persisted despite conflicts
	// This is critical because the defer block checks for these conditions
	if err := r.retryStatusUpdateWithBackoff(ctx, hef, func(latest *humiov1alpha1.HumioEventForwarder) error {
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               humiov1alpha1.EventForwarderConditionTypeReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: latest.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             humiov1alpha1.EventForwarderReasonAdoptionRejected,
			Message:            errorMsg,
		})
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               humiov1alpha1.EventForwarderConditionTypeSynced,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: latest.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             humiov1alpha1.EventForwarderReasonConfigurationDrifted,
			Message:            fmt.Sprintf("Configuration differs from existing event forwarder: %v", diffKeysAndValues),
		})
		// Mark as not managed since adoption was rejected
		managed := false
		latest.Status.ManagedByOperator = &managed
		return r.Status().Update(ctx, latest)
	}); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to update status for adoption rejection")
	}

	return reconcile.Result{}, r.logErrorAndReturn(
		fmt.Errorf("event forwarder '%s' already exists in LogScale with different configuration", hef.Spec.Name),
		errorMsg,
	)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioEventForwarderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioEventForwarder{}).
		Named("humioeventforwarder").
		Complete(r)
}

func (r *HumioEventForwarderReconciler) setCondition(ctx context.Context, hef *humiov1alpha1.HumioEventForwarder, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	// Use retry logic to handle conflicts during status updates
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch latest version to avoid conflicts
		latest := &humiov1alpha1.HumioEventForwarder{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hef), latest); err != nil {
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

func (r *HumioEventForwarderReconciler) setManagedByOperator(ctx context.Context, hef *humiov1alpha1.HumioEventForwarder, managed bool) error {
	// Use retry logic to handle conflicts during status updates
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch latest version to avoid conflicts
		latest := &humiov1alpha1.HumioEventForwarder{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hef), latest); err != nil {
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

func (r *HumioEventForwarderReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// checkForDuplicateForwarderName validates that no other HumioEventForwarder resource
// in the cluster is managing the same LogScale forwarder name (spec.name + cluster).
// This prevents multiple Kubernetes resources from fighting over the same LogScale forwarder,
// which can lead to unexpected deletion and recreation.
// Checks both managedClusterName and externalClusterName.
func (r *HumioEventForwarderReconciler) checkForDuplicateForwarderName(ctx context.Context, hef *humiov1alpha1.HumioEventForwarder) error {
	// List all HumioEventForwarder resources in all namespaces
	var forwarderList humiov1alpha1.HumioEventForwarderList
	if err := r.List(ctx, &forwarderList); err != nil {
		return fmt.Errorf("failed to list event forwarders: %w", err)
	}

	// Determine which cluster field to compare for current resource
	currentCluster := hef.Spec.ManagedClusterName
	if currentCluster == "" {
		currentCluster = hef.Spec.ExternalClusterName
	}

	// Check for duplicates with same spec.name + cluster
	for _, other := range forwarderList.Items {
		// Skip self (same UID)
		if other.UID == hef.UID {
			continue
		}

		// Determine which cluster field to compare for other resource
		otherCluster := other.Spec.ManagedClusterName
		if otherCluster == "" {
			otherCluster = other.Spec.ExternalClusterName
		}

		// Skip if different cluster
		if otherCluster != currentCluster {
			continue
		}

		// Check for duplicate LogScale name
		if other.Spec.Name == hef.Spec.Name {
			return fmt.Errorf("cannot manage forwarder '%s' - already managed by HumioEventForwarder '%s/%s'. Each LogScale forwarder must be managed by exactly one Kubernetes resource",
				hef.Spec.Name, other.Namespace, other.Name)
		}
	}

	return nil
}

// sanitizeSecretError prevents secret data from leaking in error messages.
// This function redacts potentially sensitive information while preserving diagnostic value.
// It logs the full error at debug level for troubleshooting while returning a sanitized version.
func (r *HumioEventForwarderReconciler) sanitizeSecretError(err error) error {
	if err == nil {
		return nil
	}

	msg := err.Error()

	// Use regex to redact sensitive patterns more precisely
	// Redact key=value patterns (common in Kafka properties errors)
	keyValuePattern := regexp.MustCompile(`(\w+)=([^\s,]+)`)
	msg = keyValuePattern.ReplaceAllString(msg, "$1=[redacted]")

	// Redact content inside quotes that might contain secrets
	quotedPattern := regexp.MustCompile(`["']([^"']+)["']`)
	msg = quotedPattern.ReplaceAllString(msg, "[redacted]")

	// Redact specific secret-related words followed by values
	secretWordsPattern := regexp.MustCompile(`(?i)(password|secret|token|key|credential|auth)[\s:=]+[^\s,]+`)
	msg = secretWordsPattern.ReplaceAllString(msg, "$1=[redacted]")

	return fmt.Errorf("%s", msg)
}

// isPermanentError classifies whether an error during finalizer execution is permanent or transient.
// Permanent errors indicate that we should give up and remove the finalizer (e.g., resource already deleted, not found).
// Transient errors indicate we should retry (e.g., network issues, temporary API unavailability).
func (r *HumioEventForwarderReconciler) isPermanentError(err error) bool {
	if err == nil {
		return false
	}

	// EntityNotFound errors are permanent - resource is already gone
	var entityNotFound humioapi.EntityNotFound
	if errors.As(err, &entityNotFound) {
		return true
	}

	// Kubernetes NotFound errors are permanent
	if k8serrors.IsNotFound(err) {
		return true
	}

	// Check for specific error messages that indicate permanent conditions
	errMsg := err.Error()

	// Resource doesn't exist - permanent
	if strings.Contains(errMsg, "Could not find") ||
		strings.Contains(errMsg, "does not exist") ||
		strings.Contains(errMsg, "not found") {
		return true
	}

	// Authentication/authorization failures - usually permanent
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

	// Kubernetes conflict errors are transient
	if k8serrors.IsConflict(err) {
		return false
	}

	// Network errors are typically transient
	if strings.Contains(errMsg, "connection") ||
		strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "dial") ||
		strings.Contains(errMsg, "EOF") {
		return false
	}

	// Default to transient (safer - we'll retry)
	return false
}

// checkForDependentRules checks if any EventForwardingRules (managed or unmanaged) are using this forwarder.
// It queries all repositories in LogScale and returns a list of rule identifiers (repository/rule-id) that
// depend on this forwarder. This prevents deletion of forwarders that are still in use.
func (r *HumioEventForwarderReconciler) checkForDependentRules(ctx context.Context, client *humioapi.Client, hef *humiov1alpha1.HumioEventForwarder) ([]string, error) {
	// Get the forwarder ID from status
	forwarderID := hef.Status.EventForwarderID
	if forwarderID == "" {
		// No ID means we never created/adopted this forwarder, so no rules can be using it
		r.Log.V(1).Info("no forwarder ID in status, skipping rule check")
		return nil, nil
	}

	// OPTIMIZATION: Check Kubernetes resources first (local, fast)
	// This is much more efficient than querying all repositories and rules in LogScale via GraphQL
	var k8sRuleList humiov1alpha1.HumioEventForwardingRuleList
	if err := r.List(ctx, &k8sRuleList); err != nil {
		r.Log.V(1).Info("failed to list K8s rules, falling back to LogScale query", "error", err)
		// Fall through to LogScale query below
	} else {
		// Check if any K8s-managed rules reference this forwarder
		var dependentRules []string
		for _, rule := range k8sRuleList.Items {
			// Check if the rule references this forwarder by ID
			if rule.Status.ResolvedEventForwarderID == forwarderID {
				dependentRules = append(dependentRules, fmt.Sprintf("k8s:%s/%s", rule.Namespace, rule.Name))
			}
		}

		if len(dependentRules) > 0 {
			r.Log.Info("found dependent K8s-managed rules", "count", len(dependentRules), "rules", dependentRules)
			return dependentRules, nil
		}

		r.Log.V(1).Info("no K8s-managed dependent rules found, checking LogScale for non-K8s-managed rules")
	}

	// Query all repositories and their event forwarding rules in LogScale
	// This is only reached if:
	// 1. K8s list failed, OR
	// 2. No K8s-managed rules were found (need to check for manually created rules in LogScale)
	resp, err := humiographql.ListAllEventForwardingRules(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to list event forwarding rules: %w", err)
	}

	// Collect rules that use this forwarder
	var dependentRules []string
	for _, searchDomain := range resp.GetSearchDomains() {
		// Extract repository name
		repoName := searchDomain.GetName()

		// Type assert to Repository to access eventForwardingRules
		if repo, ok := searchDomain.(*humiographql.ListAllEventForwardingRulesSearchDomainsRepository); ok {
			rules := repo.GetEventForwardingRules()
			for _, rule := range rules {
				if rule.GetEventForwarderId() == forwarderID {
					// Format as "logscale:repository/rule-id" to distinguish from K8s-managed rules
					dependentRules = append(dependentRules, fmt.Sprintf("logscale:%s/%s", repoName, rule.GetId()))
				}
			}
		}
	}

	if len(dependentRules) > 0 {
		r.Log.Info("found dependent LogScale rules", "count", len(dependentRules), "rules", dependentRules)
	} else {
		r.Log.V(1).Info("no dependent rules found for forwarder", "forwarderID", forwarderID)
	}

	return dependentRules, nil
}

// eventForwarderAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already match what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
// Returns (matches, diffMap, error). Error is returned if normalization fails.
func eventForwarderAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioEventForwarder, fromGraphQL *humiographql.KafkaEventForwarderDetails) (bool, map[string]string, error) {
	keyValues := make(map[string]string)

	// Compare name
	if diff := cmp.Diff(fromGraphQL.GetName(), fromKubernetesCustomResource.Spec.Name); diff != "" {
		keyValues["name"] = diff
	}

	// Compare description
	if diff := cmp.Diff(fromGraphQL.GetDescription(), fromKubernetesCustomResource.Spec.Description); diff != "" {
		keyValues["description"] = diff
	}

	// Compare enabled
	if diff := cmp.Diff(fromGraphQL.GetEnabled(), fromKubernetesCustomResource.Spec.Enabled); diff != "" {
		keyValues["enabled"] = diff
	}

	// Compare Kafka-specific fields
	if fromKubernetesCustomResource.Spec.KafkaConfig != nil {
		// Compare topic
		if diff := cmp.Diff(fromGraphQL.GetTopic(), fromKubernetesCustomResource.Spec.KafkaConfig.Topic); diff != "" {
			keyValues["topic"] = diff
		}

		// Compare properties - normalize BOTH sides before comparing
		// CRD spec properties should already be normalized from fetchKafkaProperties()
		normalizedCRD := fromKubernetesCustomResource.Spec.KafkaConfig.Properties

		// Normalize LogScale properties to match CRD format
		normalizedLogScale, err := normalizeKafkaProperties(fromGraphQL.GetProperties())
		if err != nil {
			// Normalization failed - return error separately instead of putting it in diff map
			return false, keyValues, fmt.Errorf("failed to normalize LogScale properties: %w", err)
		}

		// Compare normalized properties
		if diff := cmp.Diff(normalizedLogScale, normalizedCRD); diff != "" {
			keyValues["properties"] = diff
		}
	}

	return len(keyValues) == 0, keyValues, nil
}
