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

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioViewPermissionRoleReconciler reconciles a HumioViewPermissionRole object
type HumioViewPermissionRoleReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewpermissionroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewpermissionroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewpermissionroles/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioViewPermissionRoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioViewPermissionRole")

	// Fetch the HumioViewPermissionRole instance
	hp := &humiov1alpha1.HumioViewPermissionRole{}
	err := r.Get(ctx, req.NamespacedName, hp)
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

	r.Log = r.Log.WithValues("Request.UID", hp.UID)

	cluster, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, hp, humiov1alpha1.ViewPermissionRoleConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.ViewPermissionRoleReasonConfigError, "Unable to obtain humio client config")
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set view permission role condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, hp)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle view permission role rename")
	}
	if renamed {
		return result, nil
	}

	// Handle deletion
	if hp.GetDeletionTimestamp() != nil {
		return r.handleViewPermissionRoleDeletion(ctx, humioHttpClient, hp)
	}

	// Add finalizer
	if !helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to viewPermissionRole")
		if err := r.addFinalizer(ctx, hp); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Defer status update
	defer r.updateViewPermissionRoleFinalStatus(ctx, humioHttpClient, hp)

	// Ensure view permission role exists and is updated
	if err := r.ensureViewPermissionRole(ctx, humioHttpClient, hp); err != nil {
		return reconcile.Result{}, err
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// handleViewPermissionRoleDeletion handles the deletion logic for view permission roles
func (r *HumioViewPermissionRoleReconciler) handleViewPermissionRoleDeletion(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioViewPermissionRole) (ctrl.Result, error) {
	r.Log.Info("ViewPermissionRole marked to be deleted")
	if !helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil
	}

	// Check for force finalize annotation
	if ShouldForceFinalize(hp) {
		r.Log.Info("Force finalize annotation detected, removing finalizer without cleanup",
			"resource", hp.Name,
			"namespace", hp.Namespace)
		hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hp)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Finalizer removed successfully via force-finalize annotation")
		return reconcile.Result{Requeue: true}, nil
	}

	_, err := r.HumioClient.GetViewPermissionRole(ctx, humioHttpClient, hp)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		// Role doesn't exist in LogScale - check if we should remove finalizer
		if !hp.Spec.AllowDataDeletion {
			return reconcile.Result{}, r.logErrorAndReturn(
				fmt.Errorf("view permission role may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion"),
				"data deletion not enabled")
		}
		hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hp)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("ViewPermissionRole contains finalizer so run finalizer method")
	if err := r.finalize(ctx, humioHttpClient, hp); err != nil {
		// Error during finalization
		// If the cluster is unavailable or the resource is already deleted, users can manually
		// add the 'humio.com/force-finalize: "true"' annotation to remove the finalizer
		r.Log.Error(err, "Failed to finalize view permission role during deletion. "+
			"If the resource is already deleted or the cluster is unavailable, "+
			"add the annotation 'humio.com/force-finalize: \"true\"' to remove the finalizer")
		return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
	}
	return reconcile.Result{Requeue: true}, nil
}

// ensureViewPermissionRole ensures the view permission role exists and is updated
func (r *HumioViewPermissionRoleReconciler) ensureViewPermissionRole(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioViewPermissionRole) error {
	r.Log.Info("get current viewPermissionRole")
	curViewPermissionRole, err := r.HumioClient.GetViewPermissionRole(ctx, humioHttpClient, hp)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("viewPermissionRole doesn't exist. Now adding viewPermissionRole")
			addErr := r.HumioClient.AddViewPermissionRole(ctx, humioHttpClient, hp)
			if addErr != nil {
				return r.logErrorAndReturn(addErr, "could not create viewPermissionRole")
			}
			r.Log.Info("created viewPermissionRole")
			return nil
		}
		return r.logErrorAndReturn(err, "could not check if viewPermissionRole exists")
	}

	if asExpected, diffKeysAndValues := viewPermissionRoleAlreadyAsExpected(hp, curViewPermissionRole); !asExpected {
		r.Log.Info("information differs, triggering update", "diff", diffKeysAndValues)
		err = r.HumioClient.UpdateViewPermissionRole(ctx, humioHttpClient, hp)
		if err != nil {
			return r.logErrorAndReturn(err, "could not update viewPermissionRole")
		}
	}
	return nil
}

// updateViewPermissionRoleFinalStatus updates the final status of the view permission role
func (r *HumioViewPermissionRoleReconciler) updateViewPermissionRoleFinalStatus(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioViewPermissionRole) {
	_, err := r.HumioClient.GetViewPermissionRole(ctx, humioHttpClient, hp)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		_ = r.setCondition(ctx, hp, humiov1alpha1.ViewPermissionRoleConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.ViewPermissionRoleReasonNotFound, "View permission role not found")
		return
	}
	if err != nil {
		_ = r.setCondition(ctx, hp, humiov1alpha1.ViewPermissionRoleConditionTypeReady, metav1.ConditionUnknown, humiov1alpha1.ViewPermissionRoleReasonConfigError, fmt.Sprintf("Failed to get view permission role: %v", err))
		return
	}
	_ = r.setCondition(ctx, hp, humiov1alpha1.ViewPermissionRoleConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.ViewPermissionRoleReasonReady, "View permission role is ready")
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioViewPermissionRoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioViewPermissionRole{}).
		Named("humioviewpermissionrole").
		Complete(r)
}

func (r *HumioViewPermissionRoleReconciler) finalize(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioViewPermissionRole) error {
	_, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Check if data deletion is allowed
	if !hp.Spec.AllowDataDeletion {
		return fmt.Errorf("view permission role may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
	}

	// Audit log before deletion
	r.Log.Info("Proceeding with view permission role deletion",
		"allowDataDeletion", hp.Spec.AllowDataDeletion,
		"roleName", hp.Spec.Name,
		"namespace", hp.Namespace,
		"deletionTimestamp", hp.GetDeletionTimestamp(),
	)

	return r.HumioClient.DeleteViewPermissionRole(ctx, client, hp)
}

func (r *HumioViewPermissionRoleReconciler) addFinalizer(ctx context.Context, hp *humiov1alpha1.HumioViewPermissionRole) error {
	r.Log.Info("Adding Finalizer for the HumioViewPermissionRole")
	hp.SetFinalizers(append(hp.GetFinalizers(), HumioFinalizer))

	// Update CR
	err := r.Update(ctx, hp)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioViewPermissionRole with finalizer")
	}
	return nil
}

// setCondition sets a condition on the HumioViewPermissionRole resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioViewPermissionRoleReconciler) setCondition(ctx context.Context,
	hp *humiov1alpha1.HumioViewPermissionRole,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioViewPermissionRole{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hp), latest); err != nil {
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
		latest.Status.State = viewPermissionRoleStateFromCondition(status, reason)

		// Track the synced name when view permission role is ready
		if conditionType == humiov1alpha1.ViewPermissionRoleConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.Name
		}

		return r.Status().Update(ctx, latest)
	})
}

func viewPermissionRoleStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioViewPermissionRoleStateExists
	}
	switch reason {
	case humiov1alpha1.ViewPermissionRoleReasonNotFound:
		return humiov1alpha1.HumioViewPermissionRoleStateNotFound
	case humiov1alpha1.ViewPermissionRoleReasonConfigError:
		return humiov1alpha1.HumioViewPermissionRoleStateConfigError
	default:
		return humiov1alpha1.HumioViewPermissionRoleStateUnknown
	}
}

func (r *HumioViewPermissionRoleReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// viewPermissionRoleAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func viewPermissionRoleAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioViewPermissionRole, fromGraphQL *humiographql.RoleDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetDisplayName(), fromKubernetesCustomResource.Spec.Name); diff != "" {
		keyValues["name"] = diff
	}
	permissionsFromGraphQL := fromGraphQL.GetViewPermissions()
	viewPermissionsToStrings := make([]string, len(permissionsFromGraphQL))
	for idx := range permissionsFromGraphQL {
		viewPermissionsToStrings[idx] = string(permissionsFromGraphQL[idx])
	}
	sort.Strings(viewPermissionsToStrings)
	sort.Strings(fromKubernetesCustomResource.Spec.Permissions)
	if diff := cmp.Diff(viewPermissionsToStrings, fromKubernetesCustomResource.Spec.Permissions); diff != "" {
		keyValues["permissions"] = diff
	}

	roleAssignmentsFromGraphQL := []humiov1alpha1.HumioViewPermissionRoleAssignment{}
	for _, group := range fromGraphQL.GetGroups() {
		for _, role := range group.GetRoles() {
			respSearchDomain := role.GetSearchDomain()
			roleAssignmentsFromGraphQL = append(roleAssignmentsFromGraphQL, humiov1alpha1.HumioViewPermissionRoleAssignment{
				GroupName:      group.GetDisplayName(),
				RepoOrViewName: respSearchDomain.GetName(),
			})
		}
	}
	sort.Slice(roleAssignmentsFromGraphQL, func(i, j int) bool {
		// Primary sort by RepoOrViewName
		if roleAssignmentsFromGraphQL[i].RepoOrViewName != roleAssignmentsFromGraphQL[j].RepoOrViewName {
			return roleAssignmentsFromGraphQL[i].RepoOrViewName < roleAssignmentsFromGraphQL[j].RepoOrViewName
		}
		// Secondary sort by GroupName if RepoOrViewName is the same
		return roleAssignmentsFromGraphQL[i].GroupName < roleAssignmentsFromGraphQL[j].GroupName
	})
	sort.Slice(fromKubernetesCustomResource.Spec.RoleAssignments, func(i, j int) bool {
		// Primary sort by RepoOrViewName
		if fromKubernetesCustomResource.Spec.RoleAssignments[i].RepoOrViewName != fromKubernetesCustomResource.Spec.RoleAssignments[j].RepoOrViewName {
			return fromKubernetesCustomResource.Spec.RoleAssignments[i].RepoOrViewName < fromKubernetesCustomResource.Spec.RoleAssignments[j].RepoOrViewName
		}
		// Secondary sort by GroupName if RepoOrViewName is the same
		return fromKubernetesCustomResource.Spec.RoleAssignments[i].GroupName < fromKubernetesCustomResource.Spec.RoleAssignments[j].GroupName
	})
	if diff := cmp.Diff(roleAssignmentsFromGraphQL, fromKubernetesCustomResource.Spec.RoleAssignments); diff != "" {
		keyValues["roleAssignments"] = diff
	}

	return len(keyValues) == 0, keyValues
}

// detectAndHandleRename checks if the view permission role name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
func (r *HumioViewPermissionRoleReconciler) detectAndHandleRename(ctx context.Context,
	httpClient *humioapi.Client, hvpr *humiov1alpha1.HumioViewPermissionRole) (bool, reconcile.Result, error) {

	config := DeleteRecreateRenameConfig{
		ResourceType: "view permission role",
		GetSpecName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioViewPermissionRole).Spec.Name
		},
		SetSpecName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioViewPermissionRole).Spec.Name = name
		},
		GetLastSyncedName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioViewPermissionRole).Status.LastSyncedName
		},
		SetLastSyncedName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioViewPermissionRole).Status.LastSyncedName = name
		},
		DeleteResource: func(ctx context.Context, apiClient *humioapi.Client, obj client.Object) error {
			return r.HumioClient.DeleteViewPermissionRole(ctx, apiClient, obj.(*humiov1alpha1.HumioViewPermissionRole))
		},
		SetErrorState: func(ctx context.Context, obj client.Object) error {
			return r.setCondition(ctx, obj.(*humiov1alpha1.HumioViewPermissionRole),
				humiov1alpha1.ViewPermissionRoleConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.ViewPermissionRoleReasonConfigError,
				"Configuration error during rename")
		},
		Client:        r.Client,
		StatusUpdater: r.Status(),
	}

	return HandleDeleteRecreateRename(ctx, httpClient, hvpr, config, r.Log)
}
