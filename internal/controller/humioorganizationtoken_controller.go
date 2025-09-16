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
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
)

// HumioOrganizationTokenReconciler reconciles a HumioOrganizationToken object
type HumioOrganizationTokenReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
	Recorder    record.EventRecorder
}

// TokenController interface method
func (r *HumioOrganizationTokenReconciler) Logger() logr.Logger {
	return r.Log
}

// TokenController interface method
func (r *HumioOrganizationTokenReconciler) GetRecorder() record.EventRecorder {
	return r.Recorder
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioorganizationtokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioorganizationtokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioorganizationtokens/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioOrganizationTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioOrganizationToken")

	// reading k8s object
	hot := &humiov1alpha1.HumioOrganizationToken{}
	err := r.Get(ctx, req.NamespacedName, hot)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// setup humio client configuration
	cluster, err := helpers.NewCluster(ctx, r, hot.Spec.ManagedClusterName, hot.Spec.ExternalClusterName, hot.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := setState(ctx, r, hot, humiov1alpha1.HumioTokenConfigError, hot.Status.ID, hot.Status.Token)
		if setStateErr != nil {
			return reconcile.Result{}, logErrorAndReturn(r.Log, setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "unable to obtain humio client config")
	}

	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// handle delete logic
	isHumioOrganizationTokenMarkedToBeDeleted := hot.GetDeletionTimestamp() != nil
	if isHumioOrganizationTokenMarkedToBeDeleted {
		r.Log.Info("OrganizationToken marked to be deleted")
		if helpers.ContainsElement(hot.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetOrganizationToken(ctx, humioHttpClient, hot)
			// first iteration on delete we don't enter here since OrganizationToken should exist
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hot.SetFinalizers(helpers.RemoveElement(hot.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hot)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}
			// first iteration on delete we run the finalize function which includes delete
			r.Log.Info("OrganizationToken contains finalizer so run finalize method")
			if err := r.finalize(ctx, humioHttpClient, hot); err != nil {
				_ = setState(ctx, r, hot, humiov1alpha1.HumioTokenUnknown, hot.Status.ID, hot.Status.Token)
				return reconcile.Result{}, logErrorAndReturn(r.Log, err, "Finalize method returned an error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for OrganizationToken so we can run cleanup on delete
	if !helpers.ContainsElement(hot.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to OrganizationToken")
		if err := addFinalizer(ctx, r, hot); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Get or create OrganizationToken
	r.Log.Info("get current OrganizationToken")
	currentOrganizationToken, err := r.HumioClient.GetOrganizationToken(ctx, humioHttpClient, hot)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("OrganizationToken doesn't exist. Now creating")
			// run validation across spec fields
			validation, err := r.validateDependencies(ctx, humioHttpClient, hot, currentOrganizationToken)
			if err != nil {
				return handleCriticalError(ctx, r, hot, err)
			}
			// create the OrganizationToken after successful validation
			tokenId, secret, addErr := r.HumioClient.CreateOrganizationToken(ctx, humioHttpClient, hot, validation.IPFilterID, validation.Permissions)
			if addErr != nil {
				return reconcile.Result{}, logErrorAndReturn(r.Log, addErr, "could not create OrganizationToken")
			}
			r.Log.Info("Successfully created OrganizationToken")
			// we only see secret once so any failed actions that depend on it are not recoverable
			encSecret, encErr := encryptToken(ctx, r, cluster, secret, hot.Namespace)
			if encErr != nil {
				return handleCriticalError(ctx, r, hot, encErr)
			}
			// set Status with the returned token id and the encrypted secret
			err = setState(ctx, r, hot, humiov1alpha1.HumioTokenExists, tokenId, encSecret)
			if err != nil {
				return handleCriticalError(ctx, r, hot, err)
			}
			r.Log.Info("Successfully updated OrganizationToken Status")
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "could not check if OrganizationToken exists")
	}

	// OrganizationToken exists, we check for differences
	asExpected, diffKeysAndValues := r.organizationTokenAlreadyAsExpected(hot, currentOrganizationToken)
	if !asExpected {
		// we plan to update so we validate dependencies
		validation, err := r.validateDependencies(ctx, humioHttpClient, hot, currentOrganizationToken)
		if err != nil {
			return handleCriticalError(ctx, r, hot, err)
		}
		r.Log.Info("information differs, triggering update for OrganizationToken", "diff", diffKeysAndValues)
		updateErr := r.HumioClient.UpdateOrganizationToken(ctx, humioHttpClient, hot, validation.Permissions)
		if updateErr != nil {
			return reconcile.Result{}, logErrorAndReturn(r.Log, updateErr, "could not update OrganizationToken")
		}
	}

	// ensure associated K8s secret exists if token is set
	err = ensureTokenSecretExists(ctx, r, hot, cluster, "OrganizationToken")
	if err != nil {
		_ = setState(ctx, r, hot, humiov1alpha1.HumioTokenConfigError, hot.Status.ID, hot.Status.Token)
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "could not ensure OrganizationToken secret exists")
	}

	// At the end of successful reconcile refetch in case of updated state
	var humioOrganizationToken *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken
	var lastErr error

	if asExpected { // no updates
		humioOrganizationToken = currentOrganizationToken
	} else {
		// refresh OrganizationToken
		humioOrganizationToken, lastErr = r.HumioClient.GetOrganizationToken(ctx, humioHttpClient, hot)
	}

	if errors.As(lastErr, &humioapi.EntityNotFound{}) {
		_ = setState(ctx, r, hot, humiov1alpha1.HumioTokenNotFound, hot.Status.ID, hot.Status.Token)
	} else if lastErr != nil {
		_ = setState(ctx, r, hot, humiov1alpha1.HumioTokenUnknown, hot.Status.ID, hot.Status.Token)
	} else {
		// on every reconcile validate dependencies that can change outside of k8s
		_, depErr := r.validateDependencies(ctx, humioHttpClient, hot, humioOrganizationToken)
		if depErr != nil {
			return handleCriticalError(ctx, r, hot, depErr)
		}
		_ = setState(ctx, r, hot, humiov1alpha1.HumioTokenExists, humioOrganizationToken.Id, hot.Status.Token)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioOrganizationTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("humioorganizationtoken-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioOrganizationToken{}).
		Named("humioOrganizationToken").
		Complete(r)
}

func (r *HumioOrganizationTokenReconciler) finalize(ctx context.Context, client *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken) error {
	if hot.Status.ID == "" {
		// unexpected but we should not err
		return nil
	}
	err := r.HumioClient.DeleteOrganizationToken(ctx, client, hot)
	if err != nil {
		return logErrorAndReturn(r.Log, err, "error in finalize function when trying to delete Humio Token")
	}
	// this is for test environment as in real k8s env garbage collection will delete it
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hot.Spec.TokenSecretName,
			Namespace: hot.Namespace,
		},
	}
	_ = r.Delete(ctx, secret)
	r.Log.Info("Successfully ran finalize method")
	return nil
}

type OrganizationTokenValidationResult struct {
	IPFilterID  string
	Permissions []humiographql.OrganizationPermission
}

// TODO cache validation results so we don't make the calls on each reconcile
func (r *HumioOrganizationTokenReconciler) validateDependencies(ctx context.Context, client *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken, ot *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken) (*OrganizationTokenValidationResult, error) {
	// we validate in order fastest to slowest
	// validate ExpireAt
	err := r.validateExpireAt(hot, ot)
	if err != nil {
		return nil, fmt.Errorf("ExpireAt validation failed: %w", err)
	}
	//validate Permissions
	permissions, err := r.validatePermissions(hot.Spec.Permissions)
	if err != nil {
		return nil, fmt.Errorf("permissions validation failed: %w", err)
	}
	//validate HumioIPFilter
	var ipFilterId string
	if hot.Spec.IPFilterName != "" {
		ipFilter, err := r.validateIPFilter(ctx, client, hot, ot)
		if err != nil {
			return nil, fmt.Errorf("ipFilterName validation failed: %w", err)
		}
		if ipFilter != nil {
			ipFilterId = ipFilter.Id
		}
	}

	return &OrganizationTokenValidationResult{
		IPFilterID:  ipFilterId,
		Permissions: permissions,
	}, nil
}

func (r *HumioOrganizationTokenReconciler) validateExpireAt(hot *humiov1alpha1.HumioOrganizationToken, ot *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken) error {
	if ot == nil { // we are validating before token creation
		if hot.Spec.ExpiresAt != nil && hot.Spec.ExpiresAt.Time.Before(time.Now()) {
			return fmt.Errorf("ExpiresAt time must be in the future")
		}
	}
	return nil
}

func (r *HumioOrganizationTokenReconciler) validatePermissions(permissions []string) ([]humiographql.OrganizationPermission, error) {
	var invalidPermissions []string
	perms := make([]humiographql.OrganizationPermission, 0, len(permissions))
	validPermissions := make(map[string]humiographql.OrganizationPermission)

	for _, perm := range humiographql.AllOrganizationPermission {
		validPermissions[string(perm)] = perm
	}
	for _, perm := range permissions {
		if _, ok := validPermissions[perm]; !ok {
			invalidPermissions = append(invalidPermissions, perm)
		} else {
			perms = append(perms, validPermissions[perm])
		}
	}
	if len(invalidPermissions) > 0 {
		return nil, fmt.Errorf("one or more of the configured Permissions do not exist: %v", invalidPermissions)
	}
	return perms, nil
}

func (r *HumioOrganizationTokenReconciler) validateIPFilter(ctx context.Context, client *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken, ot *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken) (*humiographql.IPFilterDetails, error) {
	// build a temp structure
	ipFilter := &humiov1alpha1.HumioIPFilter{
		Spec: humiov1alpha1.HumioIPFilterSpec{
			Name:                hot.Spec.IPFilterName,
			ManagedClusterName:  hot.Spec.ManagedClusterName,
			ExternalClusterName: hot.Spec.ExternalClusterName,
		},
	}
	ipFilterDetails, err := r.HumioClient.GetIPFilter(ctx, client, ipFilter)
	if err != nil {
		return nil, fmt.Errorf("IPFilter with Spec.Name %s not found: %v", hot.Spec.IPFilterName, err.Error())
	}
	if ot != nil {
		// we have an existing token so we need to ensure the ipFilter Id matches
		if ipFilterDetails.Id != "" && ot.IpFilterV2 != nil && ipFilterDetails.Id != ot.IpFilterV2.Id {
			return nil, fmt.Errorf("external dependency ipFilter changed: current=%v vs desired=%v", ipFilterDetails.Id, ot.IpFilterV2.Id)
		}
	}

	return ipFilterDetails, nil
}

func (r *HumioOrganizationTokenReconciler) organizationTokenAlreadyAsExpected(fromK8s *humiov1alpha1.HumioOrganizationToken, fromGql *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken) (bool, map[string]string) {
	// we can only update assigned permissions (in theory, in practice depends on the OrganizationToken security policy so we might err if we try)
	keyValues := map[string]string{}
	permsFromK8s := fromK8s.Spec.Permissions
	permsFromGql := fromGql.Permissions
	slices.Sort(permsFromK8s)
	slices.Sort(permsFromGql)
	if diff := cmp.Diff(permsFromK8s, permsFromGql); diff != "" {
		keyValues["permissions"] = diff
	}

	return len(keyValues) == 0, keyValues
}
