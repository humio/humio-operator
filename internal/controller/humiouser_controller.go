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

// HumioUserReconciler reconciles a HumioUser object
type HumioUserReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiousers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiousers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiousers/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioUserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioUser")

	// Fetch the HumioUser instance
	hp := &humiov1alpha1.HumioUser{}
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
		setConditionErr := r.setCondition(ctx, hp, humiov1alpha1.UserConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.UserReasonConfigError, "Unable to obtain humio client config")
		if setConditionErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setConditionErr, "unable to set user condition")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// Check for rename BEFORE processing the resource
	// This ensures we handle the delete-recreate before normal reconciliation
	renamed, result, err := r.detectAndHandleRename(ctx, humioHttpClient, hp)
	if err != nil {
		return result, r.logErrorAndReturn(err, "failed to handle user rename")
	}
	if renamed {
		// Rename was initiated, requeue to continue with creation
		return result, nil
	}

	r.Log.Info("Checking if user is marked to be deleted")
	// Check if the HumioUser instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioUserMarkedToBeDeleted := hp.GetDeletionTimestamp() != nil
	if isHumioUserMarkedToBeDeleted {
		r.Log.Info("User marked to be deleted")
		if helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
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

			_, err := r.HumioClient.GetUser(ctx, humioHttpClient, hp)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hp)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for HumioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("User contains finalizer so run finalizer method")
			if err := r.finalize(ctx, humioHttpClient, hp); err != nil {
				// Error during finalization
				// If the cluster is unavailable or the resource is already deleted, users can manually
				// add the 'humio.com/force-finalize: "true"' annotation to remove the finalizer
				r.Log.Error(err, "Failed to finalize user during deletion. "+
					"If the resource is already deleted or the cluster is unavailable, "+
					"add the annotation 'humio.com/force-finalize: \"true\"' to remove the finalizer")
				return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to user")
		if err := r.addFinalizer(ctx, hp); err != nil {
			return reconcile.Result{}, err
		}
	}

	defer func(ctx context.Context, humioClient humio.Client, hp *humiov1alpha1.HumioUser) {
		_, err := humioClient.GetUser(ctx, humioHttpClient, hp)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setCondition(ctx, hp, humiov1alpha1.UserConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.UserReasonNotFound, "User not found")
			return
		}
		if err != nil {
			_ = r.setCondition(ctx, hp, humiov1alpha1.UserConditionTypeReady, metav1.ConditionUnknown, humiov1alpha1.UserReasonConfigError, fmt.Sprintf("Failed to get user: %v", err))
			return
		}
		_ = r.setCondition(ctx, hp, humiov1alpha1.UserConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.UserReasonReady, "User is ready")
	}(ctx, r.HumioClient, hp)

	// Get current user
	r.Log.Info("get current user")
	curUser, err := r.HumioClient.GetUser(ctx, humioHttpClient, hp)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("user doesn't exist. Now adding user")
			// create user
			addErr := r.HumioClient.AddUser(ctx, humioHttpClient, hp)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create user")
			}
			r.Log.Info("created user")
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if user exists")
	}

	if asExpected, diffKeysAndValues := userAlreadyAsExpected(hp, curUser); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		err = r.HumioClient.UpdateUser(ctx, humioHttpClient, hp)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update user")
		}
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioUserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioUser{}).
		Named("humiouser").
		Complete(r)
}

func (r *HumioUserReconciler) finalize(ctx context.Context, client *humioapi.Client, hp *humiov1alpha1.HumioUser) error {
	_, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	// Check if data deletion is allowed
	if !hp.Spec.AllowDataDeletion {
		return fmt.Errorf("user may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion")
	}

	// Audit log before deletion
	r.Log.Info("Proceeding with user deletion",
		"allowDataDeletion", hp.Spec.AllowDataDeletion,
		"userName", hp.Spec.UserName,
		"namespace", hp.Namespace,
		"deletionTimestamp", hp.GetDeletionTimestamp(),
	)

	err = r.HumioClient.DeleteUser(ctx, client, hp)
	if err != nil {
		return err
	}

	r.Log.Info("Successfully deleted user", "userName", hp.Spec.UserName)
	return nil
}

func (r *HumioUserReconciler) addFinalizer(ctx context.Context, hp *humiov1alpha1.HumioUser) error {
	r.Log.Info("Adding Finalizer for the HumioUser")
	hp.SetFinalizers(append(hp.GetFinalizers(), HumioFinalizer))

	// Update CR
	err := r.Update(ctx, hp)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioUser with finalizer")
	}
	return nil
}

// setCondition sets a condition on the HumioUser resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (r *HumioUserReconciler) setCondition(ctx context.Context,
	hp *humiov1alpha1.HumioUser,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioUser{}
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
		latest.Status.State = userStateFromCondition(status, reason)

		// Track the synced name when user is ready
		if conditionType == humiov1alpha1.UserConditionTypeReady && status == metav1.ConditionTrue {
			latest.Status.LastSyncedName = latest.Spec.UserName
		}

		return r.Status().Update(ctx, latest)
	})
}

func userStateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioUserStateExists
	}
	switch reason {
	case humiov1alpha1.UserReasonNotFound:
		return humiov1alpha1.HumioUserStateNotFound
	case humiov1alpha1.UserReasonConfigError:
		return humiov1alpha1.HumioUserStateConfigError
	default:
		return humiov1alpha1.HumioUserStateUnknown
	}
}

func (r *HumioUserReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// userAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func userAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioUser, fromGraphQL *humiographql.UserDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetIsRoot(), helpers.BoolFalse(fromKubernetesCustomResource.Spec.IsRoot)); diff != "" {
		keyValues["isRoot"] = diff
	}

	return len(keyValues) == 0, keyValues
}

// detectAndHandleRename checks if the user name has changed and performs delete-recreate
// Returns true if a rename was initiated, false otherwise
func (r *HumioUserReconciler) detectAndHandleRename(ctx context.Context,
	httpClient *humioapi.Client, hp *humiov1alpha1.HumioUser) (bool, reconcile.Result, error) {

	config := DeleteRecreateRenameConfig{
		ResourceType: "user",
		GetSpecName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioUser).Spec.UserName
		},
		SetSpecName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioUser).Spec.UserName = name
		},
		GetLastSyncedName: func(obj client.Object) string {
			return obj.(*humiov1alpha1.HumioUser).Status.LastSyncedName
		},
		SetLastSyncedName: func(obj client.Object, name string) {
			obj.(*humiov1alpha1.HumioUser).Status.LastSyncedName = name
		},
		DeleteResource: func(ctx context.Context, apiClient *humioapi.Client, obj client.Object) error {
			return r.HumioClient.DeleteUser(ctx, apiClient, obj.(*humiov1alpha1.HumioUser))
		},
		SetErrorState: func(ctx context.Context, obj client.Object) error {
			return r.setCondition(ctx, obj.(*humiov1alpha1.HumioUser),
				humiov1alpha1.UserConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.UserReasonConfigError,
				"Configuration error during rename")
		},
		Client:        r.Client,
		StatusUpdater: r.Status(),
	}

	return HandleDeleteRecreateRename(ctx, httpClient, hp, config, r.Log)
}
