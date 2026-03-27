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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

// TokenController interface method
func (r *HumioOrganizationTokenReconciler) GetCommonConfig() CommonConfig {
	return r.CommonConfig
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioorganizationtokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioorganizationtokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioorganizationtokens/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioOrganizationTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" && r.Namespace != req.Namespace {
		return reconcile.Result{}, nil
	}
	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("reconciling HumioOrganizationToken")

	// reading k8s object
	hot, err := r.getHumioOrganizationToken(ctx, req)
	if hot == nil {
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	// setup humio client configuration
	cluster, err := helpers.NewCluster(ctx, r, hot.Spec.ManagedClusterName, hot.Spec.ExternalClusterName, hot.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setConditionErr := setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.TokenReasonConfigError, "Unable to obtain humio client config", hot.Status.HumioID)
		if setConditionErr != nil {
			return reconcile.Result{}, logErrorAndReturn(r.Log, setConditionErr, "unable to set cluster state")
		}
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "unable to obtain humio client config")
	}

	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// handle delete logic
	if hot.GetDeletionTimestamp() != nil {
		return r.handleOrganizationTokenDeletion(ctx, humioHttpClient, hot)
	}

	// Add finalizer for OrganizationToken so we can run cleanup on delete
	if err := addFinalizer(ctx, r, hot); err != nil {
		return reconcile.Result{}, err
	}

	// Get or create OrganizationToken
	result, currentOrganizationToken, err := r.ensureOrganizationTokenExists(ctx, humioHttpClient, hot, cluster)
	if err != nil || result.Requeue || result.RequeueAfter > 0 { //nolint:staticcheck // SA1019: result.Requeue used intentionally
		return result, err
	}

	// Update if needed
	asExpected, err := r.updateOrganizationTokenIfNeeded(ctx, humioHttpClient, hot, currentOrganizationToken)
	if err != nil {
		return reconcile.Result{}, err
	}

	// ensure associated k8s secret exists
	if err := r.ensureTokenSecret(ctx, hot, humioHttpClient, cluster); err != nil {
		return reconcile.Result{}, err
	}

	// Update final status
	if err := r.updateOrganizationTokenFinalStatus(ctx, humioHttpClient, hot, asExpected, currentOrganizationToken); err != nil {
		return reconcile.Result{}, err
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// handleOrganizationTokenDeletion handles the deletion logic for organization tokens
func (r *HumioOrganizationTokenReconciler) handleOrganizationTokenDeletion(ctx context.Context, humioHttpClient *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken) (ctrl.Result, error) {
	r.Log.Info("OrganizationToken marked to be deleted")
	if !helpers.ContainsElement(hot.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil
	}

	// Check for force finalize annotation
	if ShouldForceFinalize(hot) {
		r.Log.Info("Force finalize annotation detected, removing finalizer without cleanup",
			"resource", hot.Name,
			"namespace", hot.Namespace)
		hot.SetFinalizers(helpers.RemoveElement(hot.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hot)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("Finalizer removed successfully via force-finalize annotation")
		return reconcile.Result{Requeue: true}, nil
	}

	_, err := r.HumioClient.GetOrganizationToken(ctx, humioHttpClient, hot)
	// first iteration on delete we don't enter here since OrganizationToken should exist
	if errors.As(err, &humioapi.EntityNotFound{}) {
		hot.SetFinalizers(helpers.RemoveElement(hot.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hot)
		if err != nil {
			return reconcile.Result{}, err
		}
		r.Log.Info("finalizer removed successfully")
		return reconcile.Result{Requeue: true}, nil
	}

	// first iteration on delete we run the finalize function which includes delete
	r.Log.Info("OrganizationToken contains finalizer so run finalize method")
	if err := r.finalize(ctx, humioHttpClient, hot); err != nil {
		// Error during finalization
		// If the cluster is unavailable or the resource is already deleted, users can manually
		// add the 'humio.com/force-finalize: "true"' annotation to remove the finalizer
		r.Log.Error(err, "Failed to finalize organization token during deletion. "+
			"If the resource is already deleted or the cluster is unavailable, "+
			"add the annotation 'humio.com/force-finalize: \"true\"' to remove the finalizer")
		_ = setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.TokenReasonConfigError, fmt.Sprintf("Finalize error: %v", err), hot.Status.HumioID)
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "finalize method returned an error")
	}
	// If no error was detected, we need to requeue so that we can remove the finalizer
	return reconcile.Result{Requeue: true}, nil
}

// ensureOrganizationTokenExists gets or creates the organization token
func (r *HumioOrganizationTokenReconciler) ensureOrganizationTokenExists(ctx context.Context, humioHttpClient *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken, cluster helpers.ClusterInterface) (ctrl.Result, *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken, error) {
	r.Log.Info("get current OrganizationToken")
	currentOrganizationToken, err := r.HumioClient.GetOrganizationToken(ctx, humioHttpClient, hot)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("OrganizationToken doesn't exist. Now creating")
			// run validation across spec fields
			validation, err := r.validateDependencies(ctx, humioHttpClient, hot, currentOrganizationToken)
			if err != nil {
				result, returnErr := handleCriticalError(ctx, r, hot, err)
				return result, nil, returnErr
			}
			// create the OrganizationToken after successful validation
			tokenId, secret, addErr := r.HumioClient.CreateOrganizationToken(ctx, humioHttpClient, hot, validation.IPFilterID, validation.Permissions)
			if addErr != nil {
				return reconcile.Result{}, nil, logErrorAndReturn(r.Log, addErr, "could not create OrganizationToken")
			}
			err = setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.TokenReasonCreated, "OrganizationToken created successfully", tokenId)
			if err != nil {
				// we lost the tokenId so we need to reconcile
				return reconcile.Result{}, nil, logErrorAndReturn(r.Log, addErr, "could not set Status.HumioID")
			}
			// create k8s secret
			err = ensureTokenSecretExists(ctx, r, hot, cluster, nil, hot.Spec.Name, secret)
			if err != nil {
				// we lost the humio generated secret so we need to rotateToken
				_ = setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.TokenReasonConfigError, "Failed to create k8s secret", tokenId)
				return reconcile.Result{}, nil, logErrorAndReturn(r.Log, addErr, "could not create k8s secret for OrganizationToken")
			}
			r.Log.Info("successfully created OrganizationToken")
			return reconcile.Result{RequeueAfter: time.Second * 5}, nil, nil
		}
		return reconcile.Result{}, nil, logErrorAndReturn(r.Log, err, "could not check if OrganizationToken exists")
	}
	return reconcile.Result{}, currentOrganizationToken, nil
}

// updateOrganizationTokenIfNeeded checks if organization token differs and updates if needed
func (r *HumioOrganizationTokenReconciler) updateOrganizationTokenIfNeeded(ctx context.Context, humioHttpClient *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken, currentOrganizationToken *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken) (bool, error) {
	// OrganizationToken exists, we check for differences
	asExpected, diffKeysAndValues := r.organizationTokenAlreadyAsExpected(hot, currentOrganizationToken)
	if !asExpected {
		// we plan to update so we validate dependencies
		validation, err := r.validateDependencies(ctx, humioHttpClient, hot, currentOrganizationToken)
		if err != nil {
			_, returnErr := handleCriticalError(ctx, r, hot, err)
			return false, returnErr
		}
		r.Log.Info("information differs, triggering update for OrganizationToken", "diff", diffKeysAndValues)
		updateErr := r.HumioClient.UpdateOrganizationToken(ctx, humioHttpClient, hot, validation.Permissions)
		if updateErr != nil {
			return false, logErrorAndReturn(r.Log, updateErr, "could not update OrganizationToken")
		}
	}
	return asExpected, nil
}

// updateOrganizationTokenFinalStatus updates the final status of the organization token
func (r *HumioOrganizationTokenReconciler) updateOrganizationTokenFinalStatus(ctx context.Context, humioHttpClient *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken, asExpected bool, currentOrganizationToken *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken) error {
	// At the end of successful reconcile refetch in case of updated state and validate dependencies
	var humioOrganizationToken *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken
	var lastErr error

	if asExpected { // no updates
		humioOrganizationToken = currentOrganizationToken
	} else {
		// refresh OrganizationToken
		humioOrganizationToken, lastErr = r.HumioClient.GetOrganizationToken(ctx, humioHttpClient, hot)
	}

	if errors.As(lastErr, &humioapi.EntityNotFound{}) {
		_ = setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.TokenReasonNotFound, "OrganizationToken not found", hot.Status.HumioID)
	} else if lastErr != nil {
		_ = setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.TokenReasonConfigError, fmt.Sprintf("Failed to get token: %v", lastErr), hot.Status.HumioID)
	} else {
		// on every reconcile validate dependencies that can change outside of k8s
		_, lastErr := r.validateDependencies(ctx, humioHttpClient, hot, humioOrganizationToken)
		if lastErr != nil {
			_, returnErr := handleCriticalError(ctx, r, hot, lastErr)
			return returnErr
		}
		_ = setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.TokenReasonReady, "OrganizationToken is ready", hot.Status.HumioID)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioOrganizationTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("humioorganizationtoken-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioOrganizationToken{}).
		Named("humioOrganizationToken").
		Complete(r)
}

func (r *HumioOrganizationTokenReconciler) getHumioOrganizationToken(ctx context.Context, req ctrl.Request) (*humiov1alpha1.HumioOrganizationToken, error) {
	hot := &humiov1alpha1.HumioOrganizationToken{}
	err := r.Get(ctx, req.NamespacedName, hot)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return hot, nil
}

func (r *HumioOrganizationTokenReconciler) finalize(ctx context.Context, client *humioapi.Client, hot *humiov1alpha1.HumioOrganizationToken) error {
	// Check if data deletion is allowed
	if !hot.Spec.AllowDataDeletion {
		return fmt.Errorf("token may contain data and data deletion not enabled. Set allowDataDeletion to true to allow deletion or add the %s annotation to force deletion", ForceFinalizerAnnotation)
	}

	if hot.Status.HumioID != "" {
		err := r.HumioClient.DeleteOrganizationToken(ctx, client, hot)
		if err != nil {
			return logErrorAndReturn(r.Log, err, "error in finalize function when trying to delete Humio Token")
		}
	}
	// delete secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hot.Spec.TokenSecretName,
			Namespace: hot.Namespace,
		},
	}
	controllerutil.RemoveFinalizer(secret, HumioFinalizer)
	err := r.Update(ctx, secret)
	if err != nil {
		return logErrorAndReturn(r.Log, err, fmt.Sprintf("could not remove finalizer from associated k8s secret: %s", secret.Name))
	}
	// this is for test environment as in real k8s env garbage collection will delete it
	_ = r.Delete(ctx, secret)
	r.Log.Info("successfully ran finalize method")
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

func (r *HumioOrganizationTokenReconciler) ensureTokenSecret(ctx context.Context, hot *humiov1alpha1.HumioOrganizationToken, humioHttpClient *humioapi.Client, cluster helpers.ClusterInterface) error {
	r.Log.Info("looking for secret", "TokenSecretName", hot.Spec.TokenSecretName, "namespace", hot.Namespace)
	existingSecret, err := kubernetes.GetSecret(ctx, r, hot.Spec.TokenSecretName, hot.Namespace)
	if err != nil {
		// k8s secret doesn't exist anymore, we have to rotate the Humio token
		if k8serrors.IsNotFound(err) {
			r.Log.Info("organizationToken k8s secret doesn't exist, rotating OrganizationToken")
			tokenId, secret, err := r.HumioClient.RotateOrganizationToken(ctx, humioHttpClient, hot)
			if err != nil {
				// we can try rotate again on the next reconcile
				return logErrorAndReturn(r.Log, err, "could not rotate OrganizationToken")
			}
			err = setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionTrue, humiov1alpha1.TokenReasonUpdated, "OrganizationToken rotated successfully", tokenId)
			if err != nil {
				// we lost the Humio ID so we need to reconcile
				return logErrorAndReturn(r.Log, err, "could not update OrganizationToken Status with tokenId")
			}
			err = ensureTokenSecretExists(ctx, r, hot, cluster, nil, hot.Spec.Name, secret)
			if err != nil {
				// if we can't create k8s secret its critical because we lost the secret
				return logErrorAndReturn(r.Log, err, "could not create k8s secret for OrganizationToken")
			}
		} else {
			return err
		}
	} else {
		// k8s secret exists, ensure it is up to date
		err = ensureTokenSecretExists(ctx, r, hot, cluster, existingSecret, "OrganizationToken", "")
		if err != nil {
			_ = setCondition(ctx, r, hot, humiov1alpha1.TokenConditionTypeReady, metav1.ConditionFalse, humiov1alpha1.TokenReasonConfigError, "Failed to update k8s secret", hot.Status.HumioID)
			return logErrorAndReturn(r.Log, err, "could not ensure OrganizationToken k8s secret exists")
		}
	}
	return nil
}
