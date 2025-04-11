package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type HumioFeatureFlagReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiofeatureflags,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiofeatureflags/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiofeatureflags/finalizers,verbs=update

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
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", featureFlag.UID)

	cluster, err := helpers.NewCluster(ctx, r, featureFlag.Spec.ManagedClusterName, featureFlag.Spec.ExternalClusterName, featureFlag.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioFeatureFlagStateConfigError, featureFlag)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set feature flag state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(ctx context.Context, featureFlag *humiov1alpha1.HumioFeatureFlag) {
		enabled, err := r.HumioClient.IsFeatureFlagEnabled(ctx, humioHttpClient, req, featureFlag)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioFeatureFlagStateNotFound, featureFlag)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioFeatureFlagStateUnknown, featureFlag)
			return
		}
		if enabled {
			_ = r.setState(ctx, humiov1alpha1.HumioFeatureFlagStateExists, featureFlag)
		}
	}(ctx, featureFlag)

	// Delete
	r.Log.Info("Checking if feature flag is marked to be deleted")
	if featureFlag.GetDeletionTimestamp() != nil {
		r.Log.Info("Feature flag marked to be deleted")
		if helpers.ContainsElement(featureFlag.GetFinalizers(), humioFinalizer) {
			enabled, err := r.HumioClient.IsFeatureFlagEnabled(ctx, humioHttpClient, req, featureFlag)
			if errors.As(err, &humioapi.EntityNotFound{}) || !enabled {
				featureFlag.SetFinalizers(helpers.RemoveElement(featureFlag.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, featureFlag)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting feature flag")
			if err := r.HumioClient.DisableFeatureFlag(ctx, humioHttpClient, req, featureFlag); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "disable feature flag returned error")
			}
		}
		return reconcile.Result{}, nil
	}

	enabled, err := r.HumioClient.IsFeatureFlagEnabled(ctx, humioHttpClient, req, featureFlag)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "the specified feature flag does not exist")
	}

	r.Log.Info("Checking if feature flag needs to be updated")
	if !enabled {
		err = r.HumioClient.EnableFeatureFlag(ctx, humioHttpClient, req, featureFlag)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not enable feature flag")
		}
		r.Log.Info(fmt.Sprintf("Successfully enabled feature flag %s", featureFlag.Spec.Name))
	}

	// Add finalizer
	r.Log.Info("Checking if feature flag requires finalizer")
	if !helpers.ContainsElement(featureFlag.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to feature flag")
		featureFlag.SetFinalizers(append(featureFlag.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, featureFlag)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("done reconciling")
	return reconcile.Result{Requeue: true}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioFeatureFlagReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioFeatureFlag{}).
		Named("humiofeatureflag").
		Complete(r)
}

func (r *HumioFeatureFlagReconciler) setState(ctx context.Context, state string, featureFlag *humiov1alpha1.HumioFeatureFlag) error {
	if featureFlag.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting feature flag state to %s", state))
	featureFlag.Status.State = state
	return r.Status().Update(ctx, featureFlag)
}

func (r *HumioFeatureFlagReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
