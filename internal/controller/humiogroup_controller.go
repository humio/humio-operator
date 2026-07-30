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
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioGroupReconciler reconciles a HumioGroup object
type HumioGroupReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiogroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiogroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiogroups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioGroup")

	// Fetch the HumioGroup instance
	hg := &humiov1alpha1.HumioGroup{}
	err := r.Get(ctx, req.NamespacedName, hg)
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

	r.Log = r.Log.WithValues("Request.UID", hg.UID)

	cluster, err := helpers.NewCluster(ctx, r, hg.Spec.ManagedClusterName, hg.Spec.ExternalClusterName, hg.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, hg, humiov1alpha1.GroupConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.GroupReasonConfigError, "Unable to obtain humio client config")
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set group condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	// This ensures we handle the delete-recreate before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, hg)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle group rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with creation
		return result, nil
	}

	// delete
	r.Log.Info("checking if group is marked to be deleted")
	isMarkedForDeletion := hg.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("group marked to be deleted")
		if helpers.ContainsElement(hg.GetFinalizers(), HumioFinalizer) {
			if ShouldSkipFinalizer(r.CommonConfig, hg) {
				r.Log.Info("Finalizer skip triggered, removing finalizer without cleanup")
				hg.SetFinalizers(helpers.RemoveElement(hg.GetFinalizers(), HumioFinalizer))
				if err := r.Update(ctx, hg); err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{Requeue: true}, nil
			}
			_, err := r.HumioClient.GetGroup(ctx, humioHttpClient, hg)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hg.SetFinalizers(helpers.RemoveElement(hg.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hg)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for HumioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting Group")
			if err := r.HumioClient.DeleteGroup(ctx, humioHttpClient, hg); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete group returned error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !ShouldSkipFinalizer(r.CommonConfig, hg) && !helpers.ContainsElement(hg.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to group")
		hg.SetFinalizers(append(hg.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hg)
		if err != nil {
			return reconcile.Result{}, err
		}
	}
	defer func(ctx context.Context, hg *humiov1alpha1.HumioGroup) {
		_, err := r.HumioClient.GetGroup(ctx, humioHttpClient, hg)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setCondition(ctx, hg, humiov1alpha1.GroupConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.GroupReasonNotFound, "Group not found")
			return
		}
		if err != nil {
			_ = r.setCondition(ctx, hg, humiov1alpha1.GroupConditionTypeReady, metav1.ConditionUnknown, humiov1alpha1.GroupReasonConfigError, fmt.Sprintf("Failed to get group: %v", err))
			return
		}
		_ = r.setCondition(ctx, hg, humiov1alpha1.GroupConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.GroupReasonReady, "Group is ready")
	}(ctx, hg)

	r.Log.Info("get current group")
	curGroup, err := r.HumioClient.GetGroup(ctx, humioHttpClient, hg)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("Group doesn't exist. Now adding group")
			addErr := r.HumioClient.AddGroup(ctx, humioHttpClient, hg)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create group")
			}
			r.Log.Info("created group", "GroupName", hg.Spec.Name)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if group exists")
	}

	if asExpected, diffKeysAndValues := groupAlreadyAsExpected(hg, curGroup); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		updateErr := r.HumioClient.UpdateGroup(ctx, humioHttpClient, hg)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update group")
		}
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioGroup{}).
		Named("humiogroup").
		Complete(r)
}

// setCondition sets a condition on the HumioGroup resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioGroupReconciler) setCondition(ctx context.Context,
	hg *humiov1alpha1.HumioGroup,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioGroup{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hg), latest); err != nil {
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
		latest.Status.State = groupStateFromCondition(status, reason)

		// Track the synced name when group is ready
		if conditionType == humiov1alpha1.GroupConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.Name
		}

		return r.Status().Update(ctx, latest)
	})
}

func groupStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioGroupStateExists
	}
	switch reason {
	case humiov1alpha1.GroupReasonNotFound:
		return humiov1alpha1.HumioGroupStateNotFound
	case humiov1alpha1.GroupReasonConfigError:
		return humiov1alpha1.HumioGroupStateConfigError
	default:
		return humiov1alpha1.HumioGroupStateUnknown
	}
}

func (r *HumioGroupReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// groupAlreadyAsExpected compares the group from the custom resource with the group from the GraphQL API.
// It returns a boolean indicating if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func groupAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioGroup, fromGraphQL *humiographql.GroupDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetLookupName(), fromKubernetesCustomResource.Spec.ExternalMappingName); diff != "" {
		keyValues["externalMappingName"] = diff
	}

	return len(keyValues) == 0, keyValues
}

// detectAndHandleRename checks if the group name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
func (r *HumioGroupReconciler) detectAndHandleRename(ctx context.Context,
	httpClient *humioapi.Client, hg *humiov1alpha1.HumioGroup) (bool, reconcile.Result, error) {

	config := DeleteRecreateRenameConfig{
		ResourceType: "group",
		GetSpecName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioGroup).Spec.Name
		},
		SetSpecName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioGroup).Spec.Name = name
		},
		GetLastSyncedName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioGroup).Status.LastSyncedName
		},
		SetLastSyncedName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioGroup).Status.LastSyncedName = name
		},
		DeleteResource: func(ctx context.Context, apiClient *humioapi.Client, obj client.Object) error {
			return r.HumioClient.DeleteGroup(ctx, apiClient, obj.(*humiov1alpha1.HumioGroup))
		},
		SetErrorState: func(ctx context.Context, obj client.Object) error {
			return r.setCondition(ctx, obj.(*humiov1alpha1.HumioGroup),
				humiov1alpha1.GroupConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.GroupReasonConfigError,
				"Configuration error during rename")
		},
		Client:        r.Client,
		StatusUpdater: r.Status(),
	}

	return HandleDeleteRecreateRename(ctx, httpClient, hg, config, r.Log)
}
