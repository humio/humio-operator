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

// HumioSystemTokenReconciler reconciles a HumioSystemToken object
type HumioSystemTokenReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
	Recorder    record.EventRecorder
}

// TokenController interface method
func (r *HumioSystemTokenReconciler) Logger() logr.Logger {
	return r.Log
}

// TokenController interface method
func (r *HumioSystemTokenReconciler) GetRecorder() record.EventRecorder {
	return r.Recorder
}

// TokenController interface method
func (r *HumioSystemTokenReconciler) GetCommonConfig() CommonConfig {
	return r.CommonConfig
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiosystemtokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiosystemtokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiosystemtokens/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioSystemTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" && r.Namespace != req.Namespace {
		return reconcile.Result{}, nil
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("reconciling HumioSystemToken")

	// reading k8s object
	hst, err := r.getHumioSystemToken(ctx, req)
	if hst == nil {
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	// setup humio client configuration
	cluster, err := helpers.NewCluster(ctx, r, hst.Spec.ManagedClusterName, hst.Spec.ExternalClusterName, hst.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := setState(ctx, r, hst, humiov1alpha1.HumioTokenConfigError, hst.Status.HumioID)
		if setStateErr != nil {
			return reconcile.Result{}, logErrorAndReturn(r.Log, setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "unable to obtain humio client config")
	}

	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// handle delete logic
	isHumioSystemTokenMarkedToBeDeleted := hst.GetDeletionTimestamp() != nil
	if isHumioSystemTokenMarkedToBeDeleted {
		r.Log.Info("SystemToken marked to be deleted")
		if helpers.ContainsElement(hst.GetFinalizers(), HumioFinalizer) {
			_, err := r.HumioClient.GetSystemToken(ctx, humioHttpClient, hst)
			// first iteration on delete we don't enter here since SystemToken should exist
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hst.SetFinalizers(helpers.RemoveElement(hst.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hst)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}
			// first iteration on delete we run the finalize function which includes delete
			r.Log.Info("SystemToken contains finalizer so run finalize method")
			if err := r.finalize(ctx, humioHttpClient, hst); err != nil {
				_ = setState(ctx, r, hst, humiov1alpha1.HumioTokenUnknown, hst.Status.HumioID)
				return reconcile.Result{}, logErrorAndReturn(r.Log, err, "Finalize method returned an error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for SystemToken so we can run cleanup on delete
	if err := addFinalizer(ctx, r, hst); err != nil {
		return reconcile.Result{}, err
	}

	// Get or create SystemToken
	r.Log.Info("get current SystemToken")
	currentSystemToken, err := r.HumioClient.GetSystemToken(ctx, humioHttpClient, hst)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("SystemToken doesn't exist. Now creating")
			// run validation across spec fields
			validation, err := r.validateDependencies(ctx, humioHttpClient, hst, currentSystemToken)
			if err != nil {
				return handleCriticalError(ctx, r, hst, err)
			}
			// create the SystemToken after successful validation
			tokenId, secret, addErr := r.HumioClient.CreateSystemToken(ctx, humioHttpClient, hst, validation.IPFilterID, validation.Permissions)
			if addErr != nil {
				return reconcile.Result{}, logErrorAndReturn(r.Log, addErr, "could not create SystemToken")
			}
			err = setState(ctx, r, hst, humiov1alpha1.HumioTokenExists, tokenId)
			if err != nil {
				// we lost the tokenId so we need to reconcile
				return reconcile.Result{}, logErrorAndReturn(r.Log, addErr, "could not set Status.HumioID")
			}
			// create k8s secret
			err = ensureTokenSecretExists(ctx, r, hst, cluster, nil, hst.Spec.Name, secret)
			if err != nil {
				// we lost the humio generated secret so we need to rotateToken
				_ = setState(ctx, r, hst, humiov1alpha1.HumioTokenConfigError, tokenId)
				return reconcile.Result{}, logErrorAndReturn(r.Log, addErr, "could not create k8s secret for SystemToken")
			}
			r.Log.Info("Successfully created SystemToken")
			return reconcile.Result{RequeueAfter: time.Second * 5}, nil
		}
		return reconcile.Result{}, logErrorAndReturn(r.Log, err, "could not check if SystemToken exists")
	}

	// SystemToken exists, we check for differences
	asExpected, diffKeysAndValues := r.systemTokenAlreadyAsExpected(hst, currentSystemToken)
	if !asExpected {
		// we plan to update so we validate dependencies
		validation, err := r.validateDependencies(ctx, humioHttpClient, hst, currentSystemToken)
		if err != nil {
			return handleCriticalError(ctx, r, hst, err)
		}
		r.Log.Info("information differs, triggering update for SystemToken", "diff", diffKeysAndValues)
		updateErr := r.HumioClient.UpdateSystemToken(ctx, humioHttpClient, hst, validation.Permissions)
		if updateErr != nil {
			return reconcile.Result{}, logErrorAndReturn(r.Log, updateErr, "could not update SystemToken")
		}
	}

	// ensure associated k8s secret exists
	if err := r.ensureTokenSecret(ctx, hst, humioHttpClient, cluster); err != nil {
		return reconcile.Result{}, err
	}

	// At the end of successful reconcile refetch in case of updated state and validate dependencies
	var humioSystemToken *humiographql.SystemTokenDetailsSystemPermissionsToken
	var lastErr error

	if asExpected { // no updates
		humioSystemToken = currentSystemToken
	} else {
		// refresh SystemToken
		humioSystemToken, lastErr = r.HumioClient.GetSystemToken(ctx, humioHttpClient, hst)
	}

	if errors.As(lastErr, &humioapi.EntityNotFound{}) {
		_ = setState(ctx, r, hst, humiov1alpha1.HumioTokenNotFound, hst.Status.HumioID)
	} else if lastErr != nil {
		_ = setState(ctx, r, hst, humiov1alpha1.HumioTokenUnknown, hst.Status.HumioID)
	} else {
		// on every reconcile validate dependencies that can change outside of k8s
		_, lastErr := r.validateDependencies(ctx, humioHttpClient, hst, humioSystemToken)
		if lastErr != nil {
			return handleCriticalError(ctx, r, hst, lastErr)
		}
		_ = setState(ctx, r, hst, humiov1alpha1.HumioTokenExists, hst.Status.HumioID)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioSystemTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("humiosystemtoken-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioSystemToken{}).
		Named("humioSystemToken").
		Complete(r)
}

func (r *HumioSystemTokenReconciler) getHumioSystemToken(ctx context.Context, req ctrl.Request) (*humiov1alpha1.HumioSystemToken, error) {
	hst := &humiov1alpha1.HumioSystemToken{}
	err := r.Get(ctx, req.NamespacedName, hst)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return hst, nil
}

func (r *HumioSystemTokenReconciler) finalize(ctx context.Context, client *humioapi.Client, hst *humiov1alpha1.HumioSystemToken) error {
	if hst.Status.HumioID != "" {
		err := r.HumioClient.DeleteSystemToken(ctx, client, hst)
		if err != nil {
			return logErrorAndReturn(r.Log, err, "error in finalize function when trying to delete Humio Token")
		}
	}
	//cleanup k8s secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hst.Spec.TokenSecretName,
			Namespace: hst.Namespace,
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

type SystemTokenValidationResult struct {
	IPFilterID  string
	Permissions []humiographql.SystemPermission
}

// TODO cache validation results so we don't make the calls on each reconcile
func (r *HumioSystemTokenReconciler) validateDependencies(ctx context.Context, client *humioapi.Client, hst *humiov1alpha1.HumioSystemToken, vt *humiographql.SystemTokenDetailsSystemPermissionsToken) (*SystemTokenValidationResult, error) {
	// we validate in order fastest to slowest
	// validate ExpireAt
	err := r.validateExpireAt(hst, vt)
	if err != nil {
		return nil, fmt.Errorf("ExpireAt validation failed: %w", err)
	}
	//validate Permissions
	permissions, err := r.validatePermissions(hst.Spec.Permissions)
	if err != nil {
		return nil, fmt.Errorf("permissions validation failed: %w", err)
	}
	//validate HumioIPFilter
	var ipFilterId string
	if hst.Spec.IPFilterName != "" {
		ipFilter, err := r.validateIPFilter(ctx, client, hst, vt)
		if err != nil {
			return nil, fmt.Errorf("ipFilterName validation failed: %w", err)
		}
		if ipFilter != nil {
			ipFilterId = ipFilter.Id
		}
	}
	return &SystemTokenValidationResult{
		IPFilterID:  ipFilterId,
		Permissions: permissions,
	}, nil
}

func (r *HumioSystemTokenReconciler) validateExpireAt(hst *humiov1alpha1.HumioSystemToken, vt *humiographql.SystemTokenDetailsSystemPermissionsToken) error {
	if vt == nil { // we are validating before token creation
		if hst.Spec.ExpiresAt != nil && hst.Spec.ExpiresAt.Time.Before(time.Now()) {
			return fmt.Errorf("ExpiresAt time must be in the future")
		}
	}
	return nil
}

func (r *HumioSystemTokenReconciler) validatePermissions(permissions []string) ([]humiographql.SystemPermission, error) {
	var invalidPermissions []string
	perms := make([]humiographql.SystemPermission, 0, len(permissions))
	validPermissions := make(map[string]humiographql.SystemPermission)

	for _, perm := range humiographql.AllSystemPermission {
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

func (r *HumioSystemTokenReconciler) validateIPFilter(ctx context.Context, client *humioapi.Client, hst *humiov1alpha1.HumioSystemToken, vt *humiographql.SystemTokenDetailsSystemPermissionsToken) (*humiographql.IPFilterDetails, error) {
	// build a temp structure
	ipFilter := &humiov1alpha1.HumioIPFilter{
		Spec: humiov1alpha1.HumioIPFilterSpec{
			Name:                hst.Spec.IPFilterName,
			ManagedClusterName:  hst.Spec.ManagedClusterName,
			ExternalClusterName: hst.Spec.ExternalClusterName,
		},
	}
	ipFilterDetails, err := r.HumioClient.GetIPFilter(ctx, client, ipFilter)
	if err != nil {
		return nil, fmt.Errorf("IPFilter with Spec.Name %s not found: %v", hst.Spec.IPFilterName, err.Error())
	}
	if vt != nil {
		// we have an existing token so we need to ensure the ipFilter Id matches
		if ipFilterDetails.Id != "" && vt.IpFilterV2 != nil && ipFilterDetails.Id != vt.IpFilterV2.Id {
			return nil, fmt.Errorf("external dependency ipFilter changed: current=%v vs desired=%v", ipFilterDetails.Id, vt.IpFilterV2.Id)
		}
	}
	return ipFilterDetails, nil
}

func (r *HumioSystemTokenReconciler) systemTokenAlreadyAsExpected(fromK8s *humiov1alpha1.HumioSystemToken, fromGql *humiographql.SystemTokenDetailsSystemPermissionsToken) (bool, map[string]string) {
	// we can only update assigned permissions (in theory, in practice depends on the SystemToken security policy so we might err if we try)
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

func (r *HumioSystemTokenReconciler) ensureTokenSecret(ctx context.Context, hst *humiov1alpha1.HumioSystemToken, humioHttpClient *humioapi.Client, cluster helpers.ClusterInterface) error {
	r.Log.Info("looking for secret", "TokenSecretName", hst.Spec.TokenSecretName, "namespace", hst.Namespace)
	existingSecret, err := kubernetes.GetSecret(ctx, r, hst.Spec.TokenSecretName, hst.Namespace)
	if err != nil {
		// k8s secret doesn't exist anymore, we have to rotate the Humio token
		if k8serrors.IsNotFound(err) {
			r.Log.Info("SystemToken k8s secret doesn't exist, rotating SystemToken")
			tokenId, secret, err := r.HumioClient.RotateSystemToken(ctx, humioHttpClient, hst)
			if err != nil {
				// re can try rotate again on the next reconcile
				return logErrorAndReturn(r.Log, err, "could not rotate SystemToken")
			}
			err = setState(ctx, r, hst, humiov1alpha1.HumioTokenExists, tokenId)
			if err != nil {
				// we lost the Humio ID so we need to reconcile
				return logErrorAndReturn(r.Log, err, "could not update SystemToken Status with tokenId")
			}
			err = ensureTokenSecretExists(ctx, r, hst, cluster, nil, hst.Spec.Name, secret)
			if err != nil {
				// if we can't create k8s secret its critical because we lost the secret
				return logErrorAndReturn(r.Log, err, "could not create k8s secret for SystemToken")
			}
		} else {
			return err
		}
	} else {
		// k8s secret exists, ensure it is up to date
		err = ensureTokenSecretExists(ctx, r, hst, cluster, existingSecret, "SystemToken", "")
		if err != nil {
			_ = setState(ctx, r, hst, humiov1alpha1.HumioTokenConfigError, hst.Status.HumioID)
			return logErrorAndReturn(r.Log, err, "could not ensure SystemToken k8s secret exists")
		}
	}
	return nil
}
