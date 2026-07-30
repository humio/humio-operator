package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
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

type HumioFeatureFlagReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiofeatureflags,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiofeatureflags/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiofeatureflags/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioFeatureFlagReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioFeatureFlag")

	featureFlag := &humiov1alpha1.HumioFeatureFlag{}
	err := r.Get(ctx, req.NamespacedName, featureFlag)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", featureFlag.UID)

	cluster, err := helpers.NewCluster(ctx, r, featureFlag.Spec.ManagedClusterName, featureFlag.Spec.ExternalClusterName, featureFlag.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setCondition(ctx, featureFlag,
			humiov1alpha1.FeatureFlagConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.FeatureFlagReasonConfigError,
			fmt.Sprintf("Unable to obtain humio client config: %v", err))
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set feature flag state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}

	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for deletion FIRST, before any validation or processing
	r.Log.Info("Checking if feature flag is marked to be deleted")
	if featureFlag.GetDeletionTimestamp() != nil {
		r.Log.Info("Feature flag marked to be deleted")
		return r.handleFeatureFlagDeletion(ctx, featureFlag, humioHttpClient, req)
	}

	// Validate that the feature flag name exists in LogScale
	if err := r.validateFeatureFlagName(ctx, featureFlag, humioHttpClient); err != nil {
		return reconcile.Result{RequeueAfter: 5 * time.Second}, err
	}

	// Register defer for status updates
	defer r.updateFeatureFlagStatus(ctx, featureFlag, humioHttpClient)

	// Ensure the feature flag is enabled
	if err := r.ensureFeatureFlagEnabled(ctx, featureFlag, humioHttpClient); err != nil {
		return reconcile.Result{}, err
	}

	// Add finalizer if not present
	if err := r.ensureFinalizer(ctx, featureFlag); err != nil {
		return reconcile.Result{}, err
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// handleFeatureFlagDeletion handles the deletion logic for feature flags
func (r *HumioFeatureFlagReconciler) handleFeatureFlagDeletion(ctx context.Context, featureFlag *humiov1alpha1.HumioFeatureFlag, humioHttpClient *humioapi.Client, req ctrl.Request) (ctrl.Result, error) {
	if !helpers.ContainsElement(featureFlag.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil
	}

	if ShouldSkipFinalizer(r.CommonConfig, featureFlag) {
		r.Log.Info("Finalizer skip triggered, removing finalizer without cleanup")
		featureFlag.SetFinalizers(helpers.RemoveElement(featureFlag.GetFinalizers(), HumioFinalizer))
		if err := r.Update(ctx, featureFlag); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	// Check if resource is in ConfigError state - if so, skip LogScale cleanup
	condition := meta.FindStatusCondition(featureFlag.Status.Conditions, humiov1alpha1.FeatureFlagConditionTypeReady)
	inConfigError := condition != nil && condition.Status == metav1.ConditionFalse && condition.Reason == humiov1alpha1.FeatureFlagReasonConfigError

	if inConfigError {
		r.Log.Info("Feature flag is in ConfigError state, skipping LogScale cleanup and removing finalizer")
		featureFlag.SetFinalizers(helpers.RemoveElement(featureFlag.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, featureFlag)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil
	}

	enabled, err := r.HumioClient.IsFeatureFlagEnabled(ctx, humioHttpClient, featureFlag)
	objErr := r.Get(ctx, req.NamespacedName, featureFlag)
	if errors.As(objErr, &humioapi.EntityNotFound{}) || !enabled || errors.As(err, &humioapi.EntityNotFound{}) {
		featureFlag.SetFinalizers(helpers.RemoveElement(featureFlag.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, featureFlag)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil
	}

	// Run finalization logic - disable the feature flag
	r.Log.Info("Deleting feature flag")
	if err := r.HumioClient.DisableFeatureFlag(ctx, humioHttpClient, featureFlag); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "disable feature flag returned error")
	}
	// If no error was detected, we need to requeue so that we can remove the finalizer
	return reconcile.Result{Requeue: true}, nil
}

// validateFeatureFlagName validates that the specified feature flag name exists in LogScale
func (r *HumioFeatureFlagReconciler) validateFeatureFlagName(ctx context.Context, featureFlag *humiov1alpha1.HumioFeatureFlag, humioHttpClient *humioapi.Client) error {
	featureFlagNames, err := r.HumioClient.GetFeatureFlags(ctx, humioHttpClient)
	if !slices.Contains(featureFlagNames, featureFlag.Spec.Name) {
		setStateErr := r.setCondition(ctx, featureFlag,
			humiov1alpha1.FeatureFlagConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.FeatureFlagReasonConfigError,
			fmt.Sprintf("Feature flag '%s' does not exist. Supported feature flags: %s", featureFlag.Spec.Name, strings.Join(featureFlagNames, ", ")))
		if setStateErr != nil {
			return r.logErrorAndReturn(setStateErr, "unable to set feature flag state")
		}
		return r.logErrorAndReturn(err, "feature flag with the specified name does not exist supported feature flags: "+strings.Join(featureFlagNames, ", "))
	}
	return nil
}

// updateFeatureFlagStatus updates the status conditions based on current state in LogScale
func (r *HumioFeatureFlagReconciler) updateFeatureFlagStatus(ctx context.Context, featureFlag *humiov1alpha1.HumioFeatureFlag, humioHttpClient *humioapi.Client) {
	// Skip status updates if the resource is being deleted
	if featureFlag.GetDeletionTimestamp() != nil {
		return
	}

	enabled, err := r.HumioClient.IsFeatureFlagEnabled(ctx, humioHttpClient, featureFlag)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		_ = r.setCondition(ctx, featureFlag,
			humiov1alpha1.FeatureFlagConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.FeatureFlagReasonNotFound,
			"Feature flag not found")
		return
	}
	if enabled {
		_ = r.setCondition(ctx, featureFlag,
			humiov1alpha1.FeatureFlagConditionTypeReady,
			metav1.ConditionTrue,
			humiov1alpha1.FeatureFlagReasonReady,
			"Feature flag is enabled")
		return
	}
	if err != nil {
		_ = r.setCondition(ctx, featureFlag,
			humiov1alpha1.FeatureFlagConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.FeatureFlagReasonUnknown,
			fmt.Sprintf("Unable to determine feature flag state: %v", err))
	}
}

// ensureFeatureFlagEnabled ensures the feature flag is enabled in LogScale
func (r *HumioFeatureFlagReconciler) ensureFeatureFlagEnabled(ctx context.Context, featureFlag *humiov1alpha1.HumioFeatureFlag, humioHttpClient *humioapi.Client) error {
	enabled, err := r.HumioClient.IsFeatureFlagEnabled(ctx, humioHttpClient, featureFlag)
	// Treat EntityNotFound as "disabled" - the flag exists in LogScale's supported list but isn't enabled yet
	if err != nil && !errors.As(err, &humioapi.EntityNotFound{}) {
		return r.logErrorAndReturn(err, "failed to check feature flag status")
	}

	r.Log.Info("Checking if feature flag needs to be updated")
	// Enable the flag if it's not enabled (including EntityNotFound case)
	if !enabled || errors.As(err, &humioapi.EntityNotFound{}) {
		err = r.HumioClient.EnableFeatureFlag(ctx, humioHttpClient, featureFlag)
		if err != nil {
			return r.logErrorAndReturn(err, "could not enable feature flag")
		}
		r.Log.Info(fmt.Sprintf("Successfully enabled feature flag %s", featureFlag.Spec.Name))
	}
	return nil
}

// ensureFinalizer adds the finalizer if it's not present
func (r *HumioFeatureFlagReconciler) ensureFinalizer(ctx context.Context, featureFlag *humiov1alpha1.HumioFeatureFlag) error {
	r.Log.Info("Checking if feature flag requires finalizer")
	if !ShouldSkipFinalizer(r.CommonConfig, featureFlag) && !helpers.ContainsElement(featureFlag.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to feature flag")
		featureFlag.SetFinalizers(append(featureFlag.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, featureFlag)
		if err != nil {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioFeatureFlagReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioFeatureFlag{}).
		Named("humiofeatureflag").
		Complete(r)
}

// setCondition sets a condition on the HumioFeatureFlag resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioFeatureFlagReconciler) setCondition(ctx context.Context,
	featureFlag *humiov1alpha1.HumioFeatureFlag,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioFeatureFlag{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(featureFlag), latest); err != nil {
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
		latest.Status.State = r.stateFromCondition(status, reason)

		return r.Status().Update(ctx, latest)
	})
}

// stateFromCondition converts condition status and reason to legacy State field value
//
//nolint:unparam // reason parameter kept for consistency with other controllers
func (r *HumioFeatureFlagReconciler) stateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioFeatureFlagStateExists
	}
	switch reason {
	case humiov1alpha1.FeatureFlagReasonNotFound:
		return humiov1alpha1.HumioFeatureFlagStateNotFound
	case humiov1alpha1.FeatureFlagReasonConfigError:
		return humiov1alpha1.HumioFeatureFlagStateConfigError
	default:
		return humiov1alpha1.HumioFeatureFlagStateUnknown
	}
}

func (r *HumioFeatureFlagReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
