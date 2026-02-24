package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	humioapi "github.com/humio/humio-operator/internal/api"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioFinalizer generic finalizer to add to resources
const HumioFinalizer = "core.humio.com/finalizer"

// AllowRenameAnnotationValue is the value expected for the allow-rename annotation
const AllowRenameAnnotationValue = "true"

// ForceFinalizerAnnotation is the annotation key that triggers force finalization
const ForceFinalizerAnnotation = "humio.com/force-finalize"

// ForceFinalizerAnnotationValue is the value expected for the force-finalize annotation
const ForceFinalizerAnnotationValue = "true"

// CommonConfig has common configuration parameters for all controllers.
type CommonConfig struct {
	RequeuePeriod              time.Duration // How frequently to requeue a resource for reconcile.
	CriticalErrorRequeuePeriod time.Duration // How frequently to requeue a resource for reconcile after a critical error.
}

// ShouldForceFinalize safely checks if the force-finalize annotation is set on a resource.
// This annotation (humio.com/force-finalize: "true") allows administrators to remove
// a finalizer without performing cleanup operations in LogScale.
//
// This is the PRIMARY method for handling stuck finalizers when:
// - The LogScale cluster is unavailable or deleted
// - The resource was already manually deleted from LogScale
// - Deletion errors prevent normal cleanup
//
// WARNING: Using this annotation may orphan resources in LogScale if they still exist.
// Only use this when the resource is already deleted from LogScale or when cleanup
// is being handled manually.
//
// Returns true if the annotation is present with value "true", false otherwise.
// Safe to call even if the resource has no annotations (won't panic).
func ShouldForceFinalize(obj client.Object) bool {
	annotations := obj.GetAnnotations()
	return annotations != nil && annotations[ForceFinalizerAnnotation] == ForceFinalizerAnnotationValue
}

// DeleteRecreateRenameConfig configures delete-recreate rename behavior for a resource type
type DeleteRecreateRenameConfig struct {
	// ResourceType is the human-readable name (e.g., "parser", "user")
	ResourceType string

	// GetSpecName extracts the spec name field from the resource
	// Most resources use Spec.Name, but HumioUser uses Spec.UserName
	GetSpecName func(obj client.Object) string

	// SetSpecName sets the spec name field on the resource
	// Used to restore the old name when creating a copy for deletion
	SetSpecName func(obj client.Object, name string)

	// GetLastSyncedName extracts status.lastSyncedName from the resource
	GetLastSyncedName func(obj client.Object) string

	// SetLastSyncedName sets status.lastSyncedName on the resource
	SetLastSyncedName func(obj client.Object, name string)

	// DeleteResource deletes the old resource from LogScale
	DeleteResource func(ctx context.Context, client *humioapi.Client, obj client.Object) error

	// SetErrorState sets the error state on the resource
	// Most use setState(), EventForwarder uses setCondition()
	SetErrorState func(ctx context.Context, obj client.Object) error

	// Client provides access to get the latest resource version
	Client client.Client

	// StatusUpdater provides access to update status
	StatusUpdater client.StatusWriter
}

// HandleDeleteRecreateRename implements the common delete-recreate rename pattern.
// Returns (handled, result, error) where handled=true means rename was detected and processed.
func HandleDeleteRecreateRename(
	ctx context.Context,
	humioHttpClient *humioapi.Client,
	resource client.Object,
	config DeleteRecreateRenameConfig,
	logger logr.Logger,
) (bool, reconcile.Result, error) {

	// Common skeleton checks
	if resource.GetDeletionTimestamp() != nil {
		return false, reconcile.Result{}, nil
	}

	lastSyncedName := config.GetLastSyncedName(resource)
	if lastSyncedName == "" {
		return false, reconcile.Result{}, nil
	}

	specName := config.GetSpecName(resource)
	if lastSyncedName == specName {
		return false, reconcile.Result{}, nil
	}

	// Rename detected
	logger.Info("Name change detected",
		"resourceType", config.ResourceType,
		"oldName", lastSyncedName,
		"newName", specName)

	// Check for required annotation
	annotations := resource.GetAnnotations()
	if annotations["humio.com/allow-rename"] != AllowRenameAnnotationValue {
		err := fmt.Errorf(
			"%s name change detected (from %q to %q), but the required annotation is not set. "+
				"Rename operations use a delete-recreate strategy which will cause service interruption. "+
				"To proceed, add the annotation 'humio.com/allow-rename: \"true\"' to this resource",
			config.ResourceType, lastSyncedName, specName)

		if setErr := config.SetErrorState(ctx, resource); setErr != nil {
			return false, reconcile.Result{}, setErr
		}

		logger.Error(err, fmt.Sprintf("Blocking %s rename - annotation required", config.ResourceType))
		return true, reconcile.Result{}, nil
	}

	// Proceed with delete-recreate
	logger.Info(fmt.Sprintf("Deleting old %s to enable rename", config.ResourceType),
		"oldName", lastSyncedName,
		"newName", specName)

	// Create a copy with old name for deletion
	oldResource := resource.DeepCopyObject().(client.Object)
	// Restore the old name in the spec so DeleteResource uses the correct name
	config.SetSpecName(oldResource, lastSyncedName)
	config.SetLastSyncedName(oldResource, lastSyncedName)

	// Delete old resource
	if err := config.DeleteResource(ctx, humioHttpClient, oldResource); err != nil {
		var entityNotFound humioapi.EntityNotFound
		if !errors.As(err, &entityNotFound) {
			logger.Error(err, fmt.Sprintf("Failed to delete old %s during rename", config.ResourceType),
				"oldName", lastSyncedName)

			if setErr := config.SetErrorState(ctx, resource); setErr != nil {
				return false, reconcile.Result{}, setErr
			}

			return false, reconcile.Result{}, fmt.Errorf("failed to delete old %s %q: %w",
				config.ResourceType, lastSyncedName, err)
		}
		// EntityNotFound is OK - resource was already deleted
		logger.Info(fmt.Sprintf("Old %s already deleted", config.ResourceType),
			"oldName", lastSyncedName)
	} else {
		logger.Info(fmt.Sprintf("Successfully deleted old %s", config.ResourceType),
			"oldName", lastSyncedName)
	}

	// Clear lastSyncedName to trigger recreation with retry logic for conflict resolution
	config.SetLastSyncedName(resource, "")

	// Retry the status update with exponential backoff to handle concurrent updates
	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 100 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    5, // Max 5 retries (100ms, 200ms, 400ms, 800ms, 1600ms)
	}, func() (bool, error) {
		updateErr := config.StatusUpdater.Update(ctx, resource)
		if updateErr == nil {
			return true, nil // Success
		}

		if k8serrors.IsConflict(updateErr) {
			// Conflict error - resource was modified, refetch and retry
			logger.Info("Conflict updating status, retrying", "error", updateErr.Error())

			// Get the latest version of the resource
			key := client.ObjectKeyFromObject(resource)
			if getErr := config.Client.Get(ctx, key, resource); getErr != nil {
				return false, getErr // Fatal error, stop retrying
			}

			// Re-apply our change to the fresh resource
			config.SetLastSyncedName(resource, "")
			return false, nil // Retry
		}

		// Non-conflict error, don't retry
		return false, updateErr
	})

	if err != nil {
		return false, reconcile.Result{}, fmt.Errorf("failed to clear lastSyncedName: %w", err)
	}

	logger.Info(fmt.Sprintf("Cleared lastSyncedName, will recreate %s with new name", config.ResourceType),
		"newName", specName)

	return true, reconcile.Result{Requeue: true}, nil
}

// ValidateLogScaleName validates that a name meets LogScale's naming requirements.
// Returns an error if the name is invalid.
func ValidateLogScaleName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}

	// LogScale has a practical limit on name length
	if len(name) > 255 {
		return fmt.Errorf("name length cannot exceed 255 characters (got %d)", len(name))
	}

	// Check for reserved prefixes that LogScale uses internally
	reservedPrefixes := []string{"humio-", "__"}
	for _, prefix := range reservedPrefixes {
		if strings.HasPrefix(strings.ToLower(name), prefix) {
			return fmt.Errorf("name cannot start with reserved prefix %q", prefix)
		}
	}

	return nil
}
