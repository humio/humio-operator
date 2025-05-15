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
		setStateErr := r.setState(ctx, humiov1alpha1.HumioGroupStateConfigError, hg)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// delete
	r.Log.Info("checking if group is marked to be deleted")
	isMarkedForDeletion := hg.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("group marked to be deleted")
		if helpers.ContainsElement(hg.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetGroup(ctx, humioHttpClient, req, hg)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hg.SetFinalizers(helpers.RemoveElement(hg.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hg)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting Group")
			if err := r.HumioClient.DeleteGroup(ctx, humioHttpClient, req, hg); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete group returned error")
			}
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hg.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to group")
		hg.SetFinalizers(append(hg.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, hg)
		if err != nil {
			return reconcile.Result{}, err
		}
	}
	defer func(ctx context.Context, hg *humiov1alpha1.HumioGroup) {
		_, err := r.HumioClient.GetGroup(ctx, humioHttpClient, req, hg)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioGroupStateNotFound, hg)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioGroupStateUnknown, hg)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioGroupStateExists, hg)
	}(ctx, hg)

	r.Log.Info("get current group")
	curGroup, err := r.HumioClient.GetGroup(ctx, humioHttpClient, req, hg)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("Group doesn't exist. Now adding group")
			addErr := r.HumioClient.AddGroup(ctx, humioHttpClient, req, hg)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create group")
			}
			r.Log.Info("created group", "GroupName", hg.Spec.DisplayName)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if group exists")
	}

	if asExpected, diffKeysAndValues := groupAlreadyAsExpected(hg, curGroup); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		updateErr := r.HumioClient.UpdateGroup(ctx, humioHttpClient, req, hg)
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

func (r *HumioGroupReconciler) setState(ctx context.Context, state string, hg *humiov1alpha1.HumioGroup) error {
	if hg.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting group state to %s", state))
	hg.Status.State = state
	return r.Status().Update(ctx, hg)
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

	if diff := cmp.Diff(fromGraphQL.GetDisplayName(), &fromKubernetesCustomResource.Spec.DisplayName); diff != "" {
		keyValues["displayName"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetLookupName(), &fromKubernetesCustomResource.Spec.LookupName); diff != "" {
		keyValues["lookupName"] = diff
	}

	roleAssignmentsFromGraphQL := make([]string, len(fromGraphQL.GetRoles()))
	for _, assignment := range fromGraphQL.GetRoles() {
		role := assignment.GetRole()
		view := assignment.GetSearchDomain()
		roleAssignmentsFromGraphQL = append(roleAssignmentsFromGraphQL, formatRoleAssignmentString(role.GetDisplayName(), view.GetName()))
	}
	roleAssignmentsFromKubernetes := make([]string, len(fromKubernetesCustomResource.Spec.Assignments))
	for _, assignment := range fromKubernetesCustomResource.Spec.Assignments {
		roleAssignmentsFromKubernetes = append(roleAssignmentsFromKubernetes, formatRoleAssignmentString(assignment.RoleName, assignment.ViewName))
	}
	// sort the two lists for comparison
	sort.Strings(roleAssignmentsFromGraphQL)
	sort.Strings(roleAssignmentsFromKubernetes)
	if diff := cmp.Diff(roleAssignmentsFromGraphQL, roleAssignmentsFromKubernetes); diff != "" {
		keyValues["roleAssignments"] = diff
	}

	return len(keyValues) == 0, keyValues
}

func formatRoleAssignmentString(roleName string, viewName string) string {
	return fmt.Sprintf("%s:%s", roleName, viewName)
}
