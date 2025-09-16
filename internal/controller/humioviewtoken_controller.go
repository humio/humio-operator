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

// HumioViewTokenReconciler reconciles a HumioViewToken object
type HumioViewTokenReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
	Recorder    record.EventRecorder
}

// TokenController interface method
func (r *HumioViewTokenReconciler) Logger() logr.Logger {
	return r.Log
}

// TokenController interface method
func (r *HumioViewTokenReconciler) GetRecorder() record.EventRecorder {
	return r.Recorder
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewtokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewtokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioviewtokens/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioViewTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioViewToken")

	// reading k8s object
	hvt := &humiov1alpha1.HumioViewToken{}
	err := r.Get(ctx, req.NamespacedName, hvt)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// setup humio client configuration
	cluster, err := helpers.NewCluster(ctx, r, hvt.Spec.ManagedClusterName, hvt.Spec.ExternalClusterName, hvt.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := setState(ctx, r, hvt, humiov1alpha1.HumioTokenConfigError, hvt.Status.ID, hvt.Status.Token)
		if setStateErr != nil {
			return reconcile.Result{}, logErrorAndReturn(r.Log, setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "unable to obtain humio client config")
	}

	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// handle delete logic
	isHumioViewTokenMarkedToBeDeleted := hvt.GetDeletionTimestamp() != nil
	if isHumioViewTokenMarkedToBeDeleted {
		r.Log.Info("ViewToken marked to be deleted")
		if helpers.ContainsElement(hvt.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetViewToken(ctx, humioHttpClient, hvt)
			// first iteration on delete we don't enter here since ViewToken should exist
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hvt.SetFinalizers(helpers.RemoveElement(hvt.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hvt)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}
			// first iteration on delete we run the finalize function which includes delete
			r.Log.Info("ViewToken contains finalizer so run finalize method")
			if err := r.finalize(ctx, humioHttpClient, hvt); err != nil {
				_ = setState(ctx, r, hvt, humiov1alpha1.HumioTokenUnknown, hvt.Status.ID, hvt.Status.Token)
				return reconcile.Result{}, logErrorAndReturn(r.Log, err, "Finalize method returned an error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for ViewToken so we can run cleanup on delete
	if !helpers.ContainsElement(hvt.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to ViewToken")
		if err := addFinalizer(ctx, r, hvt); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Get or create ViewToken
	r.Log.Info("get current ViewToken")
	currentViewToken, err := r.HumioClient.GetViewToken(ctx, humioHttpClient, hvt)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("ViewToken doesn't exist. Now creating")
			// run validation across spec fields
			validation, err := r.validateDependencies(ctx, humioHttpClient, hvt, currentViewToken)
			if err != nil {
				return handleCriticalError(ctx, r, hvt, err)
			}
			// create the ViewToken after successful validation
			tokenId, secret, addErr := r.HumioClient.CreateViewToken(ctx, humioHttpClient, hvt, validation.IPFilterID, validation.ViewIDs, validation.Permissions)
			if addErr != nil {
				return reconcile.Result{}, logErrorAndReturn(r.Log, addErr, "could not create ViewToken")
			}
			r.Log.Info("Successfully created ViewToken")
			// we only see secret once so any failed actions that depend on it are not recoverable
			encSecret, encErr := encryptToken(ctx, r, cluster, secret, hvt.Namespace)
			if encErr != nil {
				return handleCriticalError(ctx, r, hvt, encErr)
			}
			// set Status with the returned token id and the encrypted secret
			err = setState(ctx, r, hvt, humiov1alpha1.HumioTokenExists, tokenId, encSecret)
			if err != nil {
				return handleCriticalError(ctx, r, hvt, err)
			}
			r.Log.Info("Successfully updated ViewToken Status")
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "could not check if ViewToken exists")
	}

	// ViewToken exists, we check for differences
	asExpected, diffKeysAndValues := r.viewTokenAlreadyAsExpected(hvt, currentViewToken)
	if !asExpected {
		// we plan to update so we validate dependencies
		validation, err := r.validateDependencies(ctx, humioHttpClient, hvt, currentViewToken)
		if err != nil {
			return handleCriticalError(ctx, r, hvt, err)
		}
		r.Log.Info("information differs, triggering update for ViewToken", "diff", diffKeysAndValues)
		updateErr := r.HumioClient.UpdateViewToken(ctx, humioHttpClient, hvt, validation.Permissions)
		if updateErr != nil {
			return reconcile.Result{}, logErrorAndReturn(r.Log, updateErr, "could not update ViewToken")
		}
	}

	// ensure associated K8s secret exists if token is set
	err = ensureTokenSecretExists(ctx, r, hvt, cluster, "ViewToken")
	if err != nil {
		_ = setState(ctx, r, hvt, humiov1alpha1.HumioTokenConfigError, hvt.Status.ID, hvt.Status.Token)
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "could not ensure ViewToken secret exists")
	}

	// At the end of successful reconcile refetch in case of updated state
	var humioViewToken *humiographql.ViewTokenDetailsViewPermissionsToken
	var lastErr error

	if asExpected { // no updates
		humioViewToken = currentViewToken
	} else {
		// refresh ViewToken
		humioViewToken, lastErr = r.HumioClient.GetViewToken(ctx, humioHttpClient, hvt)
	}

	if errors.As(lastErr, &humioapi.EntityNotFound{}) {
		_ = setState(ctx, r, hvt, humiov1alpha1.HumioTokenNotFound, hvt.Status.ID, hvt.Status.Token)
	} else if lastErr != nil {
		_ = setState(ctx, r, hvt, humiov1alpha1.HumioTokenUnknown, hvt.Status.ID, hvt.Status.Token)
	} else {
		// on every reconcile validate dependencies that can change outside of k8s
		_, depErr := r.validateDependencies(ctx, humioHttpClient, hvt, humioViewToken)
		if depErr != nil {
			return handleCriticalError(ctx, r, hvt, depErr)
		}
		_ = setState(ctx, r, hvt, humiov1alpha1.HumioTokenExists, humioViewToken.Id, hvt.Status.Token)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioViewTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("humioviewtoken-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioViewToken{}).
		Named("humioviewtoken").
		Complete(r)
}

func (r *HumioViewTokenReconciler) finalize(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioViewToken) error {
	if hvt.Status.ID == "" {
		// unexpected but we should not err
		return nil
	}
	err := r.HumioClient.DeleteViewToken(ctx, client, hvt)
	if err != nil {
		return logErrorAndReturn(r.Log, err, "error in finalize function when trying to delete Humio Token")
	}
	// this is for test environment as in real k8s env garbage collection will delete it
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hvt.Spec.TokenSecretName,
			Namespace: hvt.Namespace,
		},
	}
	_ = r.Delete(ctx, secret)
	r.Log.Info("Successfully ran finalize method")
	return nil
}

type ViewTokenValidationResult struct {
	IPFilterID  string
	ViewIDs     []string
	Permissions []humiographql.Permission
}

// TODO cache validation results so we don't make the calls on each reconcile
func (r *HumioViewTokenReconciler) validateDependencies(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioViewToken, vt *humiographql.ViewTokenDetailsViewPermissionsToken) (*ViewTokenValidationResult, error) {
	// we validate in order fastest to slowest
	// validate ExpireAt
	err := r.validateExpireAt(hvt, vt)
	if err != nil {
		return nil, fmt.Errorf("ExpireAt validation failed: %w", err)
	}
	//validate Permissions
	permissions, err := r.validatePermissions(hvt.Spec.Permissions)
	if err != nil {
		return nil, fmt.Errorf("permissions validation failed: %w", err)
	}
	//validate HumioIPFilter
	var ipFilterId string
	if hvt.Spec.IPFilterName != "" {
		ipFilter, err := r.validateIPFilter(ctx, client, hvt, vt)
		if err != nil {
			return nil, fmt.Errorf("ipFilterName validation failed: %w", err)
		}
		if ipFilter != nil {
			ipFilterId = ipFilter.Id
		}
	}
	//validate HumioViews
	viewIds, err := r.validateViews(ctx, client, hvt, vt)
	if err != nil {
		return nil, fmt.Errorf("viewsNames validation failed: %w", err)
	}
	return &ViewTokenValidationResult{
		IPFilterID:  ipFilterId,
		ViewIDs:     viewIds,
		Permissions: permissions,
	}, nil
}

func (r *HumioViewTokenReconciler) validateExpireAt(hvt *humiov1alpha1.HumioViewToken, vt *humiographql.ViewTokenDetailsViewPermissionsToken) error {
	if vt == nil { // we are validating before token creation
		if hvt.Spec.ExpiresAt != nil && hvt.Spec.ExpiresAt.Time.Before(time.Now()) {
			return fmt.Errorf("ExpiresAt time must be in the future")
		}
	}
	return nil
}

func (r *HumioViewTokenReconciler) validatePermissions(permissions []string) ([]humiographql.Permission, error) {
	var invalidPermissions []string
	perms := make([]humiographql.Permission, 0, len(permissions))
	validPermissions := make(map[string]humiographql.Permission)

	for _, perm := range humiographql.AllPermission {
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

func (r *HumioViewTokenReconciler) validateIPFilter(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioViewToken, vt *humiographql.ViewTokenDetailsViewPermissionsToken) (*humiographql.IPFilterDetails, error) {
	// build a temp structure
	ipFilter := &humiov1alpha1.HumioIPFilter{
		Spec: humiov1alpha1.HumioIPFilterSpec{
			Name:                hvt.Spec.IPFilterName,
			ManagedClusterName:  hvt.Spec.ManagedClusterName,
			ExternalClusterName: hvt.Spec.ExternalClusterName,
		},
	}
	ipFilterDetails, err := r.HumioClient.GetIPFilter(ctx, client, ipFilter)
	if err != nil {
		return nil, fmt.Errorf("IPFilter with Spec.Name %s not found: %v", hvt.Spec.IPFilterName, err.Error())
	}
	if vt != nil {
		// we have an existing token so we need to ensure the ipFilter Id matches
		if ipFilterDetails.Id != "" && vt.IpFilterV2 != nil && ipFilterDetails.Id != vt.IpFilterV2.Id {
			return nil, fmt.Errorf("external dependency ipFilter changed: current=%v vs desired=%v", ipFilterDetails.Id, vt.IpFilterV2.Id)
		}
	}

	return ipFilterDetails, nil
}

func (r *HumioViewTokenReconciler) validateViews(ctx context.Context, humioClient *humioapi.Client, hvt *humiov1alpha1.HumioViewToken, vt *humiographql.ViewTokenDetailsViewPermissionsToken) ([]string, error) {
	// views can be either managed or unmanaged so we build fake humiov1alpha1.HumioView for all
	viewList := humiov1alpha1.HumioViewList{Items: []humiov1alpha1.HumioView{}}
	for _, name := range hvt.Spec.ViewNames {
		item := humiov1alpha1.HumioView{
			Spec: humiov1alpha1.HumioViewSpec{
				Name:                name,
				ManagedClusterName:  hvt.Spec.ManagedClusterName,
				ExternalClusterName: hvt.Spec.ExternalClusterName,
			},
		}
		viewList.Items = append(viewList.Items, item)
	}
	foundIds := make([]string, 0, len(hvt.Spec.ViewNames))
	notFound := make([]string, 0, len(hvt.Spec.ViewNames))

	type ViewResult struct {
		ViewName string
		Result   *humiographql.GetSearchDomainSearchDomainView
		Err      error
	}

	results := make(chan ViewResult, len(viewList.Items))
	for _, view := range viewList.Items {
		go func(v humiov1alpha1.HumioView) {
			humioView, err := r.HumioClient.GetView(ctx, humioClient, &v, true)
			results <- ViewResult{ViewName: v.Spec.Name, Result: humioView, Err: err}
		}(view)
	}
	for i := 0; i < len(viewList.Items); i++ {
		result := <-results
		if result.Err != nil {
			notFound = append(notFound, result.ViewName)
		} else {
			foundIds = append(foundIds, result.Result.Id)
		}
	}

	if len(foundIds) != len(hvt.Spec.ViewNames) {
		return nil, fmt.Errorf("one or more of the configured viewNames do not exist: %v", notFound)
	}

	// // Check if desired K8s views ids match with Humio Token views ids since a View can be deleted and recreated outside of K8s
	if vt != nil {
		slices.Sort(foundIds)
		existingViewIds := make([]string, 0, len(vt.Views))
		for _, view := range vt.Views {
			existingViewIds = append(existingViewIds, view.GetId())
		}
		slices.Sort(existingViewIds)
		if !slices.Equal(foundIds, existingViewIds) {
			return nil, fmt.Errorf("view IDs have changed externally: expected %v, found %v", foundIds, existingViewIds)
		}
	}
	return foundIds, nil
}

// TODO add comparison for the rest of the fields to be able to cache validation results
func (r *HumioViewTokenReconciler) viewTokenAlreadyAsExpected(fromK8s *humiov1alpha1.HumioViewToken, fromGql *humiographql.ViewTokenDetailsViewPermissionsToken) (bool, map[string]string) {
	// we can only update assigned permissions (in theory, in practice depends on the ViewToken security policy)
	keyValues := map[string]string{}

	permsFromK8s := humio.FixPermissions(fromK8s.Spec.Permissions)
	permsFromGql := humio.FixPermissions(fromGql.Permissions)
	slices.Sort(permsFromK8s)
	slices.Sort(permsFromGql)
	if diff := cmp.Diff(permsFromK8s, permsFromGql); diff != "" {
		keyValues["permissions"] = diff
	}

	return len(keyValues) == 0, keyValues
}
