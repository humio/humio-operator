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

// HumioSystemPermissionRoleReconciler reconciles a HumioSystemPermissionRole object
type HumioSystemPermissionRoleReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiosystempermissionroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiosystempermissionroles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiosystempermissionroles/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioSystemPermissionRoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioSystemPermissionRole")

	// Fetch the HumioSystemPermissionRole instance
	hp := &humiov1alpha1.HumioSystemPermissionRole{}
	err := r.Get(ctx, req.NamespacedName, hp)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", hp.UID)

	cluster, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := r.setCondition(ctx, hp, humiov1alpha1.SystemPermissionRoleConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.SystemPermissionRoleReasonConfigError, "Unable to obtain humio client config")
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, hp)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle system permission role rename")
	}
	if renamed {
		return result, nil
	}

	// Handle deletion
	if hp.GetDeletionTimestamp() != nil {
		return r.handleSystemPermissionRoleDeletion(ctx, humioHttpClient, hp)
	}

	// Add finalizer
	if !ShouldSkipFinalizer(r.CommonConfig, hp) && !helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to systemPermissionRole")
		if err := r.addFinalizer(ctx, hp); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Defer status update
	defer r.updateSystemPermissionRoleFinalStatus(ctx, humioHttpClient, hp)

	// Ensure system permission role exists and is updated
	if err := r.ensureSystemPermissionRole(ctx, humioHttpClient, hp); err != nil {
		return reconcile.Result{}, err
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// handleSystemPermissionRoleDeletion handles the deletion logic for system permission roles
func (r *HumioSystemPermissionRoleReconciler) handleSystemPermissionRoleDeletion(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioSystemPermissionRole) (ctrl.Result, error) {
	r.Log.Info("SystemPermissionRole marked to be deleted")
	if !helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil
	}

	if ShouldSkipFinalizer(r.CommonConfig, hp) {
		r.Log.Info("Finalizer skip triggered, removing finalizer without cleanup",
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

	_, err := r.HumioClient.GetSystemPermissionRole(ctx, humioHttpClient, hp)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		// Role doesn't exist in LogScale - check if we should remove finalizer
		if !hp.Spec.AllowDataDeletion {
			return reconcile.Result{}, r.logErrorAndReturn(
				fmt.Errorf("system permission role may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion"),
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

	r.Log.Info("SystemPermissionRole contains finalizer so run finalizer method")
	if err := r.finalize(ctx, humioHttpClient, hp); err != nil {
		// Error during finalization
		// If the cluster is unavailable or the resource is already deleted, users can manually
		// add the 'humio.com/force-finalize: "true"' annotation to remove the finalizer
		r.Log.Error(err, "Failed to finalize system permission role during deletion. "+
			"If the resource is already deleted or the cluster is unavailable, "+
			"add the annotation 'humio.com/force-finalize: \"true\"' to remove the finalizer")
		return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
	}
	return reconcile.Result{Requeue: true}, nil
}

// ensureSystemPermissionRole ensures the system permission role exists and is updated
func (r *HumioSystemPermissionRoleReconciler) ensureSystemPermissionRole(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioSystemPermissionRole) error {
	r.Log.Info("get current systemPermissionRole")
	curSystemPermissionRole, err := r.HumioClient.GetSystemPermissionRole(ctx, humioHttpClient, hp)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("systemPermissionRole doesn't exist. Now adding systemPermissionRole")
			addErr := r.HumioClient.AddSystemPermissionRole(ctx, humioHttpClient, hp)
			if addErr != nil {
				return r.logErrorAndReturn(addErr, "could not create systemPermissionRole")
			}
			r.Log.Info("created systemPermissionRole")
			return nil
		}
		return r.logErrorAndReturn(err, "could not check if systemPermissionRole exists")
	}

	if asExpected, diffKeysAndValues := systemPermissionRoleAlreadyAsExpected(hp, curSystemPermissionRole); !asExpected {
		r.Log.Info("information differs, triggering update", "diff", diffKeysAndValues)
		err = r.HumioClient.UpdateSystemPermissionRole(ctx, humioHttpClient, hp)
		if err != nil {
			return r.logErrorAndReturn(err, "could not update systemPermissionRole")
		}
	}
	return nil
}

// updateSystemPermissionRoleFinalStatus updates the final status of the system permission role
func (r *HumioSystemPermissionRoleReconciler) updateSystemPermissionRoleFinalStatus(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioSystemPermissionRole) {
	_, err := r.HumioClient.GetSystemPermissionRole(ctx, humioHttpClient, hp)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		_ = r.setCondition(ctx, hp, humiov1alpha1.SystemPermissionRoleConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.SystemPermissionRoleReasonNotFound, "System permission role not found")
		return
	}
	if err != nil {
		_ = r.setCondition(ctx, hp, humiov1alpha1.SystemPermissionRoleConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.SystemPermissionRoleReasonConfigError, fmt.Sprintf("Failed to get system permission role: %v", err))
		return
	}
	_ = r.setCondition(ctx, hp, humiov1alpha1.SystemPermissionRoleConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.SystemPermissionRoleReasonReady, "System permission role is ready")
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioSystemPermissionRoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioSystemPermissionRole{}).
		Named("humiosystempermissionrole").
		Complete(r)
}

func (r *HumioSystemPermissionRoleReconciler) finalize(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioSystemPermissionRole) error {
	_, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Check if data deletion is allowed
	if !hp.Spec.AllowDataDeletion {
		return fmt.Errorf("system permission role may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
	}

	// Audit log before deletion
	r.Log.Info("Proceeding with system permission role deletion",
		"allowDataDeletion", hp.Spec.AllowDataDeletion,
		"roleName", hp.Spec.Name,
		"namespace", hp.Namespace,
		"deletionTimestamp", hp.GetDeletionTimestamp(),
	)

	return r.HumioClient.DeleteSystemPermissionRole(ctx, client, hp)
}

func (r *HumioSystemPermissionRoleReconciler) addFinalizer(ctx context.Context, hp *humiov1alpha1.HumioSystemPermissionRole) error {
	r.Log.Info("Adding Finalizer for the HumioSystemPermissionRole")
	hp.SetFinalizers(append(hp.GetFinalizers(), HumioFinalizer))

	// Update CR
	err := r.Update(ctx, hp)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioSystemPermissionRole with finalizer")
	}
	return nil
}

// setCondition sets a condition on the HumioSystemPermissionRole resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioSystemPermissionRoleReconciler) setCondition(ctx context.Context,
	hp *humiov1alpha1.HumioSystemPermissionRole,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioSystemPermissionRole{}
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
		latest.Status.State = systemPermissionRoleStateFromCondition(status, reason)

		// Track the synced name when system permission role is ready
		if conditionType == humiov1alpha1.SystemPermissionRoleConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.Name
		}

		return r.Status().Update(ctx, latest)
	})
}

func systemPermissionRoleStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioSystemPermissionRoleStateExists
	}
	switch reason {
	case humiov1alpha1.SystemPermissionRoleReasonNotFound:
		return humiov1alpha1.HumioSystemPermissionRoleStateNotFound
	case humiov1alpha1.SystemPermissionRoleReasonConfigError:
		return humiov1alpha1.HumioSystemPermissionRoleStateConfigError
	default:
		return humiov1alpha1.HumioSystemPermissionRoleStateUnknown
	}
}

func (r *HumioSystemPermissionRoleReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// systemPermissionRoleAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func systemPermissionRoleAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioSystemPermissionRole, fromGraphQL *humiographql.RoleDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetDisplayName(), fromKubernetesCustomResource.Spec.Name); diff != "" {
		keyValues["name"] = diff
	}
	permissionsFromGraphQL := fromGraphQL.GetSystemPermissions()
	systemPermissionsToStrings := make([]string, len(permissionsFromGraphQL))
	for idx := range permissionsFromGraphQL {
		systemPermissionsToStrings[idx] = string(permissionsFromGraphQL[idx])
	}
	sort.Strings(systemPermissionsToStrings)
	sort.Strings(fromKubernetesCustomResource.Spec.Permissions)
	if diff := cmp.Diff(systemPermissionsToStrings, fromKubernetesCustomResource.Spec.Permissions); diff != "" {
		keyValues["permissions"] = diff
	}

	groupsFromGraphQL := fromGraphQL.GetGroups()
	groupsToStrings := make([]string, len(groupsFromGraphQL))
	for idx := range groupsFromGraphQL {
		groupsToStrings[idx] = groupsFromGraphQL[idx].GetDisplayName()
	}
	sort.Strings(groupsToStrings)
	sort.Strings(fromKubernetesCustomResource.Spec.RoleAssignmentGroupNames)
	if diff := cmp.Diff(groupsToStrings, fromKubernetesCustomResource.Spec.RoleAssignmentGroupNames); diff != "" {
		keyValues["roleAssignmentGroupNames"] = diff
	}

	return len(keyValues) == 0, keyValues
}

// detectAndHandleRename checks if the system permission role name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
func (r *HumioSystemPermissionRoleReconciler) detectAndHandleRename(ctx context.Context,
	httpClient *humioapi.Client, hspr *humiov1alpha1.HumioSystemPermissionRole) (bool, reconcile.Result, error) {

	config := DeleteRecreateRenameConfig{
		ResourceType: "system permission role",
		GetSpecName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioSystemPermissionRole).Spec.Name
		},
		SetSpecName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioSystemPermissionRole).Spec.Name = name
		},
		GetLastSyncedName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioSystemPermissionRole).Status.LastSyncedName
		},
		SetLastSyncedName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioSystemPermissionRole).Status.LastSyncedName = name
		},
		DeleteResource: func(ctx context.Context, apiClient *humioapi.Client, obj client.Object) error {
			return r.HumioClient.DeleteSystemPermissionRole(ctx, apiClient, obj.(*humiov1alpha1.HumioSystemPermissionRole))
		},
		SetErrorState: func(ctx context.Context, obj client.Object) error {
			return r.setCondition(ctx, obj.(*humiov1alpha1.HumioSystemPermissionRole),
				humiov1alpha1.SystemPermissionRoleConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.SystemPermissionRoleReasonConfigError,
				"Configuration error during rename")
		},
		Client:        r.Client,
		StatusUpdater: r.Status(),
	}

	return HandleDeleteRecreateRename(ctx, httpClient, hspr, config, r.Log)
}
