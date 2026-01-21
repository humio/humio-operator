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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// Annotation update retry configuration
	annotationUpdateMaxRetries   = 5
	annotationUpdateBaseDelayMs  = 100
	annotationUpdateJitterFactor = 2
)

// HumioEventForwardingRuleReconciler reconciles a HumioEventForwardingRule object
type HumioEventForwardingRuleReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioeventforwardingrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioeventforwardingrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioeventforwardingrules/finalizers,verbs=update
// +kubebuilder:rbac:groups=core.humio.com,resources=humioeventforwarders,verbs=get;list;watch

// retryAnnotationUpdateWithBackoff performs an annotation update operation with exponential backoff and jitter.
// It handles resource version conflicts by fetching the latest version before each attempt.
// The updateFn receives the latest version of the resource and should perform the desired annotation changes.
// Returns an error if all retry attempts are exhausted or context is canceled.
func (r *HumioEventForwardingRuleReconciler) retryAnnotationUpdateWithBackoff(
	ctx context.Context,
	hefr *humiov1alpha1.HumioEventForwardingRule,
	updateFn func(*humiov1alpha1.HumioEventForwardingRule) error,
) error {
	for attempt := 0; attempt < annotationUpdateMaxRetries; attempt++ {
		// Check context cancellation before retry
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Fetch the latest version from the API server
		latest := &humiov1alpha1.HumioEventForwardingRule{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: hefr.Namespace, Name: hefr.Name}, latest); err != nil {
			return fmt.Errorf("failed to fetch latest version: %w", err)
		}

		// Apply the update function to the latest version
		if err := updateFn(latest); err != nil {
			return fmt.Errorf("update function failed: %w", err)
		}

		// Attempt to update the resource
		if err := r.Update(ctx, latest); err != nil {
			if k8serrors.IsConflict(err) && attempt < annotationUpdateMaxRetries-1 {
				// Resource version conflict - retry with backoff and jitter
				r.Log.V(1).Info("conflict updating annotations, retrying", "attempt", attempt+1)
				baseDelay := time.Millisecond * annotationUpdateBaseDelayMs * time.Duration(attempt+1)

				// Add jitter only if baseDelay is positive to avoid divide-by-zero
				var jitter time.Duration
				if baseDelay > 0 {
					maxJitter := int64(baseDelay) / annotationUpdateJitterFactor
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
			return fmt.Errorf("failed to update after %d attempts: %w", attempt+1, err)
		}

		// Success
		return nil
	}

	return fmt.Errorf("exhausted all %d retry attempts", annotationUpdateMaxRetries)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioEventForwardingRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioEventForwardingRule")

	hefr := &humiov1alpha1.HumioEventForwardingRule{}
	err := r.Get(ctx, req.NamespacedName, hefr)
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

	r.Log = r.Log.WithValues("Request.UID", hefr.UID)

	cluster, err := helpers.NewCluster(ctx, r, hefr.Spec.ManagedClusterName, hefr.Spec.ExternalClusterName, hefr.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeReady,
			metav1.ConditionFalse, humiov1alpha1.EventForwardingRuleReasonConfigError,
			fmt.Sprintf("Unable to obtain cluster config: %s", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(parentCtx context.Context) {
		// Skip status update during deletion by checking if resource still exists
		// and hasn't been deleted
		// Use a fresh context derived from parent to avoid cancellation issues
		cleanupCtx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
		defer cancel()

		// Fetch latest version to avoid stale resource issues
		fresh := &humiov1alpha1.HumioEventForwardingRule{}
		if err := r.Get(cleanupCtx, types.NamespacedName{
			Namespace: hefr.Namespace,
			Name:      hefr.Name,
		}, fresh); err != nil {
			if k8serrors.IsNotFound(err) {
				// Resource was deleted, nothing to update
				return
			}
			r.Log.V(1).Info("failed to fetch resource in defer", "error", err)
			return
		}

		// Skip if resource is being deleted
		if fresh.DeletionTimestamp != nil {
			return
		}

		// Skip if Ready condition is already False for permanent failures
		// This prevents defer block from overwriting error conditions set by reconciliation logic
		// Note: EventForwarderNotReady is NOT a permanent failure - it can resolve once forwarder becomes ready
		existingCondition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
		if existingCondition != nil &&
			existingCondition.Status == metav1.ConditionFalse &&
			(existingCondition.Reason == humiov1alpha1.EventForwardingRuleReasonReconcileError ||
				existingCondition.Reason == humiov1alpha1.EventForwardingRuleReasonInvalidForwarder) {
			// Keep the more specific error message, don't overwrite
			r.Log.V(1).Info("skipping defer status update - permanent failure condition present", "reason", existingCondition.Reason)
			return
		}

		// Skip if rule annotation is missing - this means the rule was never successfully created
		// and we should preserve any error condition set during creation attempt
		if fresh.Annotations[humio.EventForwardingRuleAnnotation] == "" {
			r.Log.V(1).Info("skipping defer status update - rule annotation missing (rule never created)")
			return
		}

		_, err := r.HumioClient.GetEventForwardingRule(cleanupCtx, humioHttpClient, fresh)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("defer block: event forwarding rule not found in LogScale",
				"name", fresh.Spec.Name, "repository", fresh.Spec.RepositoryName)
			if err := r.setCondition(cleanupCtx, fresh, humiov1alpha1.EventForwardingRuleConditionTypeReady,
				metav1.ConditionFalse, humiov1alpha1.EventForwardingRuleReasonRuleNotFound,
				"Event Forwarding Rule not found in LogScale"); err != nil {
				r.Log.Error(err, "Failed to update Ready condition in defer block")
			}
			return
		}
		if err != nil {
			// Check if the error is due to repository not found
			errMsg := err.Error()
			errMsgLower := strings.ToLower(errMsg)
			if strings.Contains(errMsgLower, "could not find repository") {
				r.Log.Info("defer block: repository not found", "repository", fresh.Spec.RepositoryName)
				if err := r.setCondition(cleanupCtx, fresh, humiov1alpha1.EventForwardingRuleConditionTypeReady,
					metav1.ConditionFalse, humiov1alpha1.EventForwardingRuleReasonReconcileError,
					fmt.Sprintf("Repository '%s' not found in LogScale", fresh.Spec.RepositoryName)); err != nil {
					r.Log.Error(err, "Failed to update Ready condition in defer block")
				}
				return
			}
			r.Log.Info("defer block: error getting event forwarding rule", "error", err,
				"name", fresh.Spec.Name, "repository", fresh.Spec.RepositoryName)
			if err := r.setCondition(cleanupCtx, fresh, humiov1alpha1.EventForwardingRuleConditionTypeReady,
				metav1.ConditionFalse, humiov1alpha1.EventForwardingRuleReasonConfigError,
				fmt.Sprintf("Failed to get event forwarding rule: %v", err)); err != nil {
				r.Log.Error(err, "Failed to update Ready condition in defer block")
			}
			return
		}
		r.Log.V(1).Info("defer block: verified event forwarding rule exists in LogScale",
			"name", fresh.Spec.Name, "repository", fresh.Spec.RepositoryName)
		if err := r.setCondition(cleanupCtx, fresh, humiov1alpha1.EventForwardingRuleConditionTypeReady,
			metav1.ConditionTrue, humiov1alpha1.EventForwardingRuleReasonReady,
			"Event Forwarding Rule is ready"); err != nil {
			r.Log.Error(err, "Failed to update Ready condition in defer block")
		}
	}(ctx)

	return r.reconcileHumioEventForwardingRule(ctx, humioHttpClient, hefr)
}

// resolveEventForwarderID resolves the event forwarder ID and name from either eventForwarderID or eventForwarderRef.
// If eventForwarderRef is used, it looks up the HumioEventForwarder resource and extracts its ID from status.
// Returns the resolved forwarder ID, forwarder name, and any error encountered.
func (r *HumioEventForwardingRuleReconciler) resolveEventForwarderID(ctx context.Context, hefr *humiov1alpha1.HumioEventForwardingRule) (string, string, error) {
	// If direct ID is specified, use it
	if hefr.Spec.EventForwarderID != "" {
		// For direct ID, we need to fetch the name from LogScale
		// Get cluster config first
		cluster, err := helpers.NewCluster(ctx, r, hefr.Spec.ManagedClusterName, hefr.Spec.ExternalClusterName, hefr.Namespace, helpers.UseCertManager(), true, false)
		if err != nil || cluster == nil || cluster.Config() == nil {
			// If we can't get cluster config, still return the ID but with empty name
			return hefr.Spec.EventForwarderID, "", nil
		}

		humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      hefr.Name,
				Namespace: hefr.Namespace,
			},
		})

		// Create a temporary EventForwarder to fetch the name
		tempForwarder := &humiov1alpha1.HumioEventForwarder{
			Status: humiov1alpha1.HumioEventForwarderStatus{
				EventForwarderID: hefr.Spec.EventForwarderID,
			},
		}

		details, err := r.HumioClient.GetEventForwarder(ctx, humioHttpClient, tempForwarder)
		if err != nil {
			// If we can't fetch the name, still return the ID but with empty name
			// Log at debug level for troubleshooting
			r.Log.V(1).Info("failed to fetch forwarder name for direct ID", "forwarderID", hefr.Spec.EventForwarderID, "error", err)
			return hefr.Spec.EventForwarderID, "", nil
		}

		return hefr.Spec.EventForwarderID, details.Name, nil
	}

	// If reference is specified, resolve it
	if hefr.Spec.EventForwarderRef != nil {
		// Determine the namespace - use ref namespace or default to rule's namespace
		forwarderNamespace := hefr.Spec.EventForwarderRef.Namespace
		if forwarderNamespace == "" {
			forwarderNamespace = hefr.Namespace
		}

		// Look up the HumioEventForwarder resource
		forwarder := &humiov1alpha1.HumioEventForwarder{}
		forwarderKey := types.NamespacedName{
			Name:      hefr.Spec.EventForwarderRef.Name,
			Namespace: forwarderNamespace,
		}

		r.Log.Info("DEBUG: attempting to fetch referenced event forwarder",
			"forwarderKey", forwarderKey.String(),
			"ruleNamespace", hefr.Namespace,
			"ruleName", hefr.Name)

		if err := r.Get(ctx, forwarderKey, forwarder); err != nil {
			r.Log.Info("DEBUG: failed to fetch event forwarder",
				"forwarderKey", forwarderKey.String(),
				"error", err)
			if k8serrors.IsNotFound(err) {
				// Return the K8s NotFound error wrapped so it can be detected in the caller
				return "", "", fmt.Errorf("referenced event forwarder %s/%s not found: %w", forwarderNamespace, hefr.Spec.EventForwarderRef.Name, err)
			}
			return "", "", fmt.Errorf("failed to get event forwarder %s/%s: %w", forwarderNamespace, hefr.Spec.EventForwarderRef.Name, err)
		}

		r.Log.Info("DEBUG: successfully fetched event forwarder",
			"forwarderNamespace", forwarder.Namespace,
			"forwarderName", forwarder.Name,
			"forwarderUID", forwarder.UID,
			"forwarderGeneration", forwarder.Generation,
			"forwarderResourceVersion", forwarder.ResourceVersion,
			"forwarderEventForwarderID", forwarder.Status.EventForwarderID,
			"conditionsCount", len(forwarder.Status.Conditions))

		// Log all conditions for debugging
		for i, condition := range forwarder.Status.Conditions {
			r.Log.Info("DEBUG: forwarder condition",
				"index", i,
				"type", condition.Type,
				"status", string(condition.Status),
				"reason", condition.Reason,
				"message", condition.Message,
				"observedGeneration", condition.ObservedGeneration,
				"lastTransitionTime", condition.LastTransitionTime.String())
		}

		// Check if the forwarder is ready
		readyCondition := meta.FindStatusCondition(forwarder.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)

		if readyCondition == nil || readyCondition.Status != metav1.ConditionTrue {
			return "", "", fmt.Errorf("referenced event forwarder %s/%s is not ready", forwarderNamespace, hefr.Spec.EventForwarderRef.Name)
		}

		r.Log.Info("DEBUG: forwarder readiness check PASSED",
			"forwarder", fmt.Sprintf("%s/%s", forwarderNamespace, hefr.Spec.EventForwarderRef.Name))

		// Get the forwarder ID from status
		if forwarder.Status.EventForwarderID == "" {
			r.Log.Info("DEBUG: forwarder has no EventForwarderID in status",
				"forwarder", fmt.Sprintf("%s/%s", forwarderNamespace, hefr.Spec.EventForwarderRef.Name))
			return "", "", fmt.Errorf("referenced event forwarder %s/%s has no eventForwarderID in status", forwarderNamespace, hefr.Spec.EventForwarderRef.Name)
		}

		r.Log.Info("DEBUG: resolveEventForwarderID SUCCESS",
			"forwarderID", forwarder.Status.EventForwarderID,
			"forwarderName", forwarder.Spec.Name,
			"forwarder", fmt.Sprintf("%s/%s", forwarderNamespace, hefr.Spec.EventForwarderRef.Name))

		return forwarder.Status.EventForwarderID, forwarder.Spec.Name, nil
	}

	// Should not reach here due to XValidation, but handle gracefully
	return "", "", fmt.Errorf("neither eventForwarderID nor eventForwarderRef is specified")
}

// ensureFinalizer checks if the resource has the finalizer and adds it if missing.
// Returns:
//   - (Result, error) for reconciliation
//   - bool indicating if finalizer was added (true = caller should return to requeue)
func (r *HumioEventForwardingRuleReconciler) ensureFinalizer(
	ctx context.Context,
	hefr *humiov1alpha1.HumioEventForwardingRule,
) (reconcile.Result, error, bool) {
	if helpers.ContainsElement(hefr.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil, false // Finalizer already present
	}

	r.Log.Info("Finalizer not present, adding finalizer to Event Forwarding Rule")
	hefr.SetFinalizers(append(hefr.GetFinalizers(), HumioFinalizer))
	err := r.Update(ctx, hefr)
	if err != nil {
		return reconcile.Result{}, err, true
	}

	return reconcile.Result{Requeue: true}, nil, true
}

// transitionLifecycleCondition transitions one-time lifecycle event reasons
// (Created/Updated) to steady-state Ready reason.
// Errors are logged but not returned as they should not block reconciliation.
func (r *HumioEventForwardingRuleReconciler) transitionLifecycleCondition(
	ctx context.Context,
	hefr *humiov1alpha1.HumioEventForwardingRule,
) {
	readyCondition := meta.FindStatusCondition(hefr.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
	if readyCondition == nil {
		return
	}

	if readyCondition.Status != metav1.ConditionTrue {
		return
	}

	// Check if reason is a lifecycle event
	isLifecycleEvent := readyCondition.Reason == humiov1alpha1.EventForwardingRuleReasonCreated ||
		readyCondition.Reason == humiov1alpha1.EventForwardingRuleReasonUpdated

	if !isLifecycleEvent {
		return
	}

	// Transition to steady-state reason
	if err := r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeReady,
		metav1.ConditionTrue, humiov1alpha1.EventForwardingRuleReasonReady,
		"Event forwarding rule is ready"); err != nil {
		r.Log.Error(err, "failed to transition to EventForwardingRuleReady")
	}
}

// handleResourceDeletion handles the deletion flow for an event forwarding rule.
// Returns:
//   - (Result, error) for reconciliation
//   - bool indicating if deletion was handled (true = caller should return immediately)
func (r *HumioEventForwardingRuleReconciler) handleResourceDeletion(
	ctx context.Context,
	client *humioapi.Client,
	hefr *humiov1alpha1.HumioEventForwardingRule,
) (reconcile.Result, error, bool) {
	r.Log.Info("Checking if Event Forwarding Rule is marked to be deleted")
	if hefr.GetDeletionTimestamp() == nil {
		return reconcile.Result{}, nil, false
	}

	r.Log.Info("Event Forwarding Rule marked to be deleted")
	if !helpers.ContainsElement(hefr.GetFinalizers(), HumioFinalizer) {
		// No finalizer, nothing to do
		return reconcile.Result{}, nil, true
	}

	_, err := r.HumioClient.GetEventForwardingRule(ctx, client, hefr)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		hefr.SetFinalizers(helpers.RemoveElement(hefr.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hefr)
		if err != nil {
			return reconcile.Result{}, err, true
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil, true
	}
	if err != nil {
		// Classify error to decide whether to retry or remove finalizer
		if r.isPermanentError(err) {
			// Permanent error (e.g., repository doesn't exist, authentication failure)
			// Remove finalizer to unblock deletion
			r.Log.Info("Permanent error checking if rule exists, removing finalizer anyway", "error", err)
			hefr.SetFinalizers(helpers.RemoveElement(hefr.GetFinalizers(), HumioFinalizer))
			updateErr := r.Update(ctx, hefr)
			if updateErr != nil {
				return reconcile.Result{}, updateErr, true
			}
			r.Log.Info("Finalizer removed successfully despite permanent error")
			return reconcile.Result{Requeue: true}, nil, true
		}

		// Transient error - retry
		r.Log.Info("Transient error checking if rule exists, will retry", "error", err)
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to check if rule exists during deletion"), true
	}

	// Run finalization logic for humioFinalizer. If the
	// finalization logic fails, don't remove the finalizer so
	// that we can retry during the next reconciliation.
	r.Log.Info("Deleting Event Forwarding Rule")
	if err := r.HumioClient.DeleteEventForwardingRule(ctx, client, hefr); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "Delete Event Forwarding Rule returned error"), true
	}
	// If no error was detected, we need to requeue so that we can remove the finalizer
	return reconcile.Result{Requeue: true}, nil, true
}

func (r *HumioEventForwardingRuleReconciler) reconcileHumioEventForwardingRule(ctx context.Context, client *humioapi.Client, hefr *humiov1alpha1.HumioEventForwardingRule) (reconcile.Result, error) {
	// Handle deletion
	result, err, handled := r.handleResourceDeletion(ctx, client, hefr)
	if handled {
		return result, err
	}

	// Ensure finalizer is present
	result, err, added := r.ensureFinalizer(ctx, hefr)
	if added {
		return result, err
	}

	// Resolve the event forwarder ID from either eventForwarderID or eventForwarderRef
	r.Log.Info("Resolving event forwarder ID")
	resolvedForwarderID, forwarderName, err := r.resolveEventForwarderID(ctx, hefr)
	if err != nil {
		// Set condition to indicate forwarder resolution failure
		var reason string
		if hefr.Spec.EventForwarderRef != nil {
			// Check if error is NotFound - unwrap to find the root cause
			if errors.Is(err, &k8serrors.StatusError{}) || k8serrors.IsNotFound(errors.Unwrap(err)) {
				reason = humiov1alpha1.EventForwardingRuleReasonInvalidForwarder
			} else {
				reason = humiov1alpha1.EventForwardingRuleReasonForwarderNotReady
			}
		} else {
			reason = humiov1alpha1.EventForwardingRuleReasonInvalidForwarder
		}

		setConditionErr := r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeReady,
			metav1.ConditionFalse, reason,
			fmt.Sprintf("Failed to resolve event forwarder: %s", err))
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set condition")
		}

		// Calculate exponential backoff based on failure duration
		backoffDuration := r.calculateBackoffDuration(hefr)
		r.Log.Info("failed to resolve event forwarder ID, will requeue with backoff",
			"error", err,
			"backoffDuration", backoffDuration.String())
		return reconcile.Result{RequeueAfter: backoffDuration}, r.logErrorAndReturn(err, "failed to resolve event forwarder ID")
	}

	// Update status with resolved forwarder ID and name if changed
	if hefr.Status.ResolvedEventForwarderID != resolvedForwarderID || hefr.Status.EventForwarderName != forwarderName {
		r.Log.Info("Updating resolved event forwarder details in status",
			"forwarderID", resolvedForwarderID,
			"forwarderName", forwarderName)
		hefr.Status.ResolvedEventForwarderID = resolvedForwarderID
		hefr.Status.EventForwarderName = forwarderName
		if err := r.Status().Update(ctx, hefr); err != nil {
			// Handle conflict errors by requeuing immediately
			if k8serrors.IsConflict(err) {
				r.Log.V(1).Info("conflict updating status with resolved forwarder details, requeuing", "error", err)
				return reconcile.Result{Requeue: true}, nil
			}
			return reconcile.Result{}, r.logErrorAndReturn(err, "failed to update status with resolved forwarder details")
		}
		// Requeue to continue with updated status
		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if Event Forwarding Rule needs to be created")
	// Add EventForwardingRule
	curRule, err := r.HumioClient.GetEventForwardingRule(ctx, client, hefr)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("Event Forwarding Rule doesn't exist. Now adding Event Forwarding Rule")
			addErr := r.HumioClient.AddEventForwardingRule(ctx, client, hefr)
			if addErr != nil {
				// Check if the error is due to repository not found (case-insensitive)
				errMsgLower := strings.ToLower(addErr.Error())
				if strings.Contains(errMsgLower, "could not find repository") {
					// Set status condition before returning error so defer block doesn't overwrite it
					_ = r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeReady,
						metav1.ConditionFalse, humiov1alpha1.EventForwardingRuleReasonReconcileError,
						fmt.Sprintf("Repository '%s' not found in LogScale", hefr.Spec.RepositoryName))
					return reconcile.Result{}, r.logErrorAndReturn(addErr, fmt.Sprintf("Repository '%s' not found", hefr.Spec.RepositoryName))
				}

				// Check if the error is due to applying rule to a view instead of repository
				if strings.Contains(addErr.Error(), "view") || strings.Contains(addErr.Error(), "View") {
					// Set status condition before returning error so defer block doesn't overwrite it
					_ = r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeReady,
						metav1.ConditionFalse, humiov1alpha1.EventForwardingRuleReasonReconcileError,
						fmt.Sprintf("Event Forwarding Rules can only be applied to repositories, not views. '%s' is a view.", hefr.Spec.RepositoryName))
					return reconcile.Result{}, r.logErrorAndReturn(addErr, "Event Forwarding Rules cannot be applied to views")
				}

				// Check if the error is due to query validation failure
				if strings.Contains(addErr.Error(), "query:") || strings.Contains(addErr.Error(), "Unknown function") {
					// Extract a clean error message from LogScale's formatted error
					errorMsg := extractQueryValidationError(addErr.Error())
					// Set status condition before returning error so defer block doesn't overwrite it
					_ = r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeReady,
						metav1.ConditionFalse, humiov1alpha1.EventForwardingRuleReasonReconcileError,
						errorMsg)
					return reconcile.Result{}, r.logErrorAndReturn(addErr, "query validation failed")
				}

				return reconcile.Result{}, r.logErrorAndReturn(addErr, fmt.Sprintf("could not create Event Forwarding Rule '%s' in repository %s", hefr.Spec.Name, hefr.Spec.RepositoryName))
			}

			// Store the rule ID returned from AddEventForwardingRule
			// The AddEventForwardingRule method populates hefr.Annotations with the rule ID
			ruleID := hefr.Annotations[humio.EventForwardingRuleAnnotation]

			// Update annotations with retry logic to handle conflicts
			if err := r.retryAnnotationUpdateWithBackoff(ctx, hefr, func(latest *humiov1alpha1.HumioEventForwardingRule) error {
				if latest.Annotations == nil {
					latest.Annotations = make(map[string]string)
				}
				latest.Annotations[humio.EventForwardingRuleAnnotation] = ruleID
				return nil
			}); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("could not update Event Forwarding Rule '%s' with annotation", hefr.Spec.Name))
			}

			r.Log.Info("created Event Forwarding Rule",
				"Rule", hefr.Spec.Name,
				"Repository", hefr.Spec.RepositoryName,
			)
			// Set Ready condition with lifecycle event reason so defer block skips verification
			_ = r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeReady,
				metav1.ConditionTrue, humiov1alpha1.EventForwardingRuleReasonCreated,
				"Event Forwarding Rule created in LogScale")
			_ = r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeSynced,
				metav1.ConditionTrue, humiov1alpha1.EventForwardingRuleReasonConfigurationSynced,
				"Event Forwarding Rule created")
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("could not check if Event Forwarding Rule exists for repository %s", hefr.Spec.RepositoryName))
	}

	r.Log.Info("Checking if Event Forwarding Rule needs to be updated")

	if asExpected, diffKeysAndValues := eventForwardingRuleAlreadyAsExpected(hefr, curRule); !asExpected {
		r.Log.Info("Event Forwarding Rule configuration differs, triggering update",
			"diff", diffKeysAndValues,
		)

		// Set synced condition to false before attempting update
		if err := r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeSynced,
			metav1.ConditionFalse, humiov1alpha1.EventForwardingRuleReasonConfigurationDrifted,
			fmt.Sprintf("Configuration has drifted from desired state: %v", diffKeysAndValues)); err != nil {
			r.Log.V(1).Info("failed to update synced condition before update", "error", err)
		}

		err = r.HumioClient.UpdateEventForwardingRule(ctx, client, hefr)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("could not update Event Forwarding Rule for repository %s", hefr.Spec.RepositoryName))
		}
		r.Log.Info("updated Event Forwarding Rule",
			"Rule", hefr.Spec.Name,
			"Repository", hefr.Spec.RepositoryName,
		)

		// Set Ready condition with lifecycle event reason
		if err := r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeReady,
			metav1.ConditionTrue, humiov1alpha1.EventForwardingRuleReasonUpdated,
			"Event Forwarding Rule updated in LogScale"); err != nil {
			r.Log.V(1).Info("failed to update ready condition after update", "error", err)
		}

		// Set synced condition to true after successful update
		if err := r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeSynced,
			metav1.ConditionTrue, humiov1alpha1.EventForwardingRuleReasonConfigurationSynced,
			"Configuration has been synchronized with LogScale"); err != nil {
			r.Log.V(1).Info("failed to update synced condition after update", "error", err)
		}
		return reconcile.Result{Requeue: true}, nil
	} else {
		// Set synced condition if already as expected
		if err := r.setCondition(ctx, hefr, humiov1alpha1.EventForwardingRuleConditionTypeSynced,
			metav1.ConditionTrue, humiov1alpha1.EventForwardingRuleReasonConfigurationSynced,
			"Configuration matches desired state"); err != nil {
			r.Log.V(1).Info("failed to update synced condition", "error", err)
		}
	}

	// Transition lifecycle events to steady state
	r.transitionLifecycleCondition(ctx, hefr)

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioEventForwardingRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioEventForwardingRule{}).
		Watches(
			&humiov1alpha1.HumioEventForwarder{},
			handler.EnqueueRequestsFromMapFunc(r.findRulesForForwarder),
		).
		Named("humioeventforwardingrule").
		Complete(r)
}

// findRulesForForwarder finds all HumioEventForwardingRules that reference a given HumioEventForwarder.
// This enables automatic reconciliation when a forwarder becomes ready.
// Optimization: Lists rules in the forwarder's namespace first (most common case),
// then checks cluster-wide only if necessary.
func (r *HumioEventForwardingRuleReconciler) findRulesForForwarder(ctx context.Context, obj client.Object) []reconcile.Request {
	forwarder, ok := obj.(*humiov1alpha1.HumioEventForwarder)
	if !ok {
		return nil
	}

	var requests []reconcile.Request

	// Optimization 1: List rules in forwarder's namespace first (most common case)
	sameNsRuleList := &humiov1alpha1.HumioEventForwardingRuleList{}
	if err := r.List(ctx, sameNsRuleList, client.InNamespace(forwarder.Namespace)); err != nil {
		r.Log.Error(err, "failed to list HumioEventForwardingRules in namespace",
			"namespace", forwarder.Namespace)
		return nil
	}

	requests = append(requests, r.findMatchingRules(forwarder, sameNsRuleList.Items)...)

	// Optimization 2: If r.Namespace is set (namespace-scoped operator), skip cluster-wide list
	if r.Namespace != "" {
		return requests
	}

	// For cluster-wide operators, check other namespaces for cross-namespace references
	// This is less common but still supported
	allRuleList := &humiov1alpha1.HumioEventForwardingRuleList{}
	if err := r.List(ctx, allRuleList); err != nil {
		r.Log.Error(err, "failed to list HumioEventForwardingRules cluster-wide")
		return requests // Return what we found in same namespace
	}

	// Only add rules from other namespaces
	for _, rule := range allRuleList.Items {
		if rule.Namespace != forwarder.Namespace {
			if rule.Spec.EventForwarderRef != nil {
				refNamespace := rule.Spec.EventForwarderRef.Namespace
				if refNamespace == "" {
					refNamespace = rule.Namespace
				}

				if rule.Spec.EventForwarderRef.Name == forwarder.Name &&
					refNamespace == forwarder.Namespace {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: rule.Namespace,
							Name:      rule.Name,
						},
					})
				}
			}
		}
	}

	return requests
}

// findMatchingRules is a helper function that finds rules matching a forwarder
func (r *HumioEventForwardingRuleReconciler) findMatchingRules(
	forwarder *humiov1alpha1.HumioEventForwarder,
	rules []humiov1alpha1.HumioEventForwardingRule,
) []reconcile.Request {
	var requests []reconcile.Request

	for _, rule := range rules {
		if rule.Spec.EventForwarderRef != nil {
			refNamespace := rule.Spec.EventForwarderRef.Namespace
			if refNamespace == "" {
				refNamespace = rule.Namespace
			}

			if rule.Spec.EventForwarderRef.Name == forwarder.Name &&
				refNamespace == forwarder.Namespace {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Namespace: rule.Namespace,
						Name:      rule.Name,
					},
				})
			}
		}
	}

	return requests
}

func (r *HumioEventForwardingRuleReconciler) setCondition(ctx context.Context, hefr *humiov1alpha1.HumioEventForwardingRule, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	// Use retry logic to handle conflicts during status updates
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch latest version to avoid conflicts
		latest := &humiov1alpha1.HumioEventForwardingRule{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hefr), latest); err != nil {
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

func (r *HumioEventForwardingRuleReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// extractQueryValidationError extracts a clean error message from LogScale's query validation error.
// LogScale returns errors in the format:
//
//	input:3: createEventForwardingRule (query: Unknown function: `INVALID_FUNCTION`.
//	```
//	 1: #type=accesslog | INVALID_FUNCTION()
//	                      ^^^^^^^^^^^^^^^^
//	```) There were errors in the input.
//
// This function extracts just the meaningful part: "Query validation failed: Unknown function: INVALID_FUNCTION"
// It's designed to be robust against variations in LogScale's error format.
func extractQueryValidationError(fullError string) string {
	const maxErrorLength = 500 // Truncate very long errors

	// Look for "query: " marker
	if idx := strings.Index(fullError, "query: "); idx != -1 {
		// Extract from "query: " to the end of the line or backticks
		start := idx + len("query: ")
		rest := fullError[start:]

		// Find the end - either newline or backticks
		end := len(rest)
		if newlineIdx := strings.Index(rest, "\n"); newlineIdx != -1 && newlineIdx < end {
			end = newlineIdx
		}
		if backtickIdx := strings.Index(rest, "```"); backtickIdx != -1 && backtickIdx < end {
			end = backtickIdx
		}

		// Clean up the extracted message
		errorDetail := strings.TrimSpace(rest[:end])
		// Remove trailing period if present
		errorDetail = strings.TrimSuffix(errorDetail, ".")

		// Truncate if too long
		if len(errorDetail) > maxErrorLength {
			errorDetail = errorDetail[:maxErrorLength] + "..."
		}

		return fmt.Sprintf("Query validation failed: %s", errorDetail)
	}

	// Fallback for other query-related errors
	if strings.Contains(fullError, "Unknown function") {
		// Try to extract the function name
		if idx := strings.Index(fullError, "Unknown function"); idx != -1 {
			snippet := fullError[idx:]
			if len(snippet) > 100 {
				snippet = snippet[:100] + "..."
			}
			return fmt.Sprintf("Query validation failed: %s", snippet)
		}
		return "Query validation failed: Unknown function in query"
	}

	// Generic fallback - return a truncated version of the full error
	if len(fullError) > maxErrorLength {
		return fmt.Sprintf("Query validation failed: %s...", fullError[:maxErrorLength])
	}

	return fmt.Sprintf("Query validation failed: %s", fullError)
}

// isPermanentError classifies whether an error during finalizer execution is permanent or transient.
// Permanent errors indicate that we should give up and remove the finalizer (e.g., resource already deleted, repository doesn't exist).
// Transient errors indicate we should retry (e.g., network issues, temporary API unavailability).
func (r *HumioEventForwardingRuleReconciler) isPermanentError(err error) bool {
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

// calculateBackoffDuration calculates the requeue delay based on how long the Ready condition has been False.
// This implements exponential backoff to avoid infinite reconciliation loops when a referenced forwarder
// is not available.
//
// Backoff schedule:
//   - < 10s since failure: 5s
//   - < 30s since failure: 10s
//   - < 60s since failure: 20s
//   - >= 60s since failure: 60s (capped at 1 minute)
func (r *HumioEventForwardingRuleReconciler) calculateBackoffDuration(hefr *humiov1alpha1.HumioEventForwardingRule) time.Duration {
	readyCondition := meta.FindStatusCondition(hefr.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)

	// If no Ready condition exists, or it's not False, use default backoff
	if readyCondition == nil || readyCondition.Status != metav1.ConditionFalse {
		return 5 * time.Second
	}

	// If the generation changed (spec was modified), reset to shortest backoff
	if readyCondition.ObservedGeneration != hefr.Generation {
		return 5 * time.Second
	}

	// Calculate time since the condition transitioned to False
	timeSinceFailure := time.Since(readyCondition.LastTransitionTime.Time)

	// Exponential backoff based on time in failed state
	switch {
	case timeSinceFailure < 10*time.Second:
		return 5 * time.Second
	case timeSinceFailure < 30*time.Second:
		return 10 * time.Second
	case timeSinceFailure < 60*time.Second:
		return 20 * time.Second
	default:
		return 60 * time.Second
	}
}

// eventForwardingRuleAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already match what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func eventForwardingRuleAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioEventForwardingRule, fromGraphQL *humiographql.EventForwardingRuleDetails) (bool, map[string]string) {
	keyValues := make(map[string]string)

	// Compare queryString
	if diff := cmp.Diff(fromGraphQL.GetQueryString(), fromKubernetesCustomResource.Spec.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}

	// Compare eventForwarderId using the resolved forwarder ID from status
	// Only compare if we have a resolved ID (non-empty string)
	if fromKubernetesCustomResource.Status.ResolvedEventForwarderID != "" {
		if diff := cmp.Diff(fromGraphQL.GetEventForwarderId(), fromKubernetesCustomResource.Status.ResolvedEventForwarderID); diff != "" {
			keyValues["eventForwarderId"] = diff
		}
	}

	// Compare language version (nil-safe)
	// When spec.languageVersion is nil, we accept any value from LogScale (cluster default)
	// Only compare if explicitly set in spec
	specLangVer := fromKubernetesCustomResource.Spec.LanguageVersion
	if specLangVer != nil {
		graphQLLangVer := fromGraphQL.GetLanguageVersion()

		var graphQLLangName *string
		if graphQLLangVer.Name != nil {
			name := string(*graphQLLangVer.Name)
			graphQLLangName = &name
		}

		if diff := cmp.Diff(graphQLLangName, specLangVer); diff != "" {
			keyValues["languageVersion"] = diff
		}
	}

	return len(keyValues) == 0, keyValues
}
