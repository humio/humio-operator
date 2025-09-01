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
	"sort"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// HumioViewTokenReconciler reconciles a HumioViewToken object
type HumioViewTokenReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// This is a manually maintained map of permissions
var EquivalentSpecificPermissions = map[string][]string{
	"ChangeFiles": {
		"CreateFiles",
		"UpdateFiles",
		"DeleteFiles",
	},
	"ChangeDashboards": {
		"CreateDashboards",
		"UpdateDashboards",
		"DeleteDashboards",
	},
	"ChangeSavedQueries": {
		"CreateSavedQueries",
		"UpdateSavedQueries",
		"DeleteSavedQueries",
	},
	"ChangeScheduledReports": {
		"CreateScheduledReports",
		"UpdateScheduledReports",
		"DeleteScheduledReports",
	},
	"ChangeTriggers": {
		"CreateTriggers",
		"UpdateTriggers",
		"DeleteTriggers",
	},
	"ChangeActions": {
		"CreateActions",
		"UpdateActions",
		"DeleteActions",
	},
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
		setStateErr := r.setState(ctx, hvt, humiov1alpha1.HumioViewTokenConfigError, hvt.Status.ID, hvt.Status.Token)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}

	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// handle delete logic
	isHumioViewTokenMarkedToBeDeleted := hvt.GetDeletionTimestamp() != nil
	if isHumioViewTokenMarkedToBeDeleted {
		r.Log.Info("ViewToken marked to be deleted")
		if helpers.ContainsElement(hvt.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetViewToken(ctx, humioHttpClient, hvt)
			// first iteration on delete we don't enter here since ViewToken exists
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
			r.Log.Info("ViewToken contains finalizer so run finalizer method")
			if err := r.finalize(ctx, humioHttpClient, hvt); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for ViewToken so we can run cleanup on delete
	if !helpers.ContainsElement(hvt.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to ViewToken")
		if err := r.addFinalizer(ctx, hvt); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Get or create ViewToken
	r.Log.Info("get current ViewToken")
	currentViewToken, err := r.HumioClient.GetViewToken(ctx, humioHttpClient, hvt)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("ViewToken doesn't exist. Now creating ViewToken")
			// run validation across spec fields
			validation, err := r.validateDependencies(ctx, humioHttpClient, hvt)
			if err != nil {
				_ = r.setState(ctx, hvt, humiov1alpha1.HumioViewTokenConfigError, hvt.Status.ID, hvt.Status.Token)
				return reconcile.Result{}, r.logErrorAndReturn(err, "dependencies validation failed, unrecoverable error, check CR dependencies")
			}
			// create the ViewToken after successful validation
			tokenResponse, secret, addErr := r.HumioClient.CreateViewToken(ctx, humioHttpClient, hvt, validation.IPFilterID, validation.ViewIDs, validation.Permissions)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create ViewToken")
			}
			// we only see secret once so any failed actions that depend on it are not recoverable
			encSecret, encErr := r.encryptToken(ctx, cluster, hvt, secret)
			if encErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(encErr, "could not encrypt ViewToken token, this is unrecoverable, recreate the ViewToken CR")
			}
			// set Status with the returned token id and the encrypted secret
			err = r.setState(ctx, hvt, humiov1alpha1.HumioViewTokenExists, tokenResponse.GetId(), encSecret)
			if err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "could not update ViewToken Status, this is unrecoverable, recreate the ViewToken CR")
			}
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if ViewToken exists")
	}

	// ViewToken exists, we check for differences
	asExpected, diffKeysAndValues, err := r.viewTokenAlreadyAsExpected(hvt, currentViewToken)
	if err != nil {
		_ = r.setState(ctx, hvt, humiov1alpha1.HumioViewTokenConfigError, hvt.Status.ID, hvt.Status.Token)
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not compute diferences")
	}
	if !asExpected {
		r.Log.Info("information differs, triggering update", "diff", diffKeysAndValues)
		updateErr := r.HumioClient.UpdateViewToken(ctx, humioHttpClient, hvt)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update ViewToken")
		}
	}

	// ensure associated K8s secret exists if token is set
	if hvt.Status.Token != "" {
		err = r.ensureViewTokenSecretExists(ctx, humioHttpClient, hvt, cluster)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not ensure ViewToken secret exists")
		}
	}

	// At the end of successful reconcile:
	humioViewToken, err := r.HumioClient.GetViewToken(ctx, humioHttpClient, hvt)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		_ = r.setState(ctx, hvt, humiov1alpha1.HumioViewTokenNotFound, hvt.Status.ID, hvt.Status.Token)
	} else if err != nil {
		_ = r.setState(ctx, hvt, humiov1alpha1.HumioViewTokenUnknown, hvt.Status.ID, hvt.Status.Token)
	} else {
		_ = r.setState(ctx, hvt, humiov1alpha1.HumioViewTokenExists, humioViewToken.GetId(), hvt.Status.Token)
	}
	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioViewTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioViewToken{}).
		Named("humioviewtoken").
		Complete(r)
}

func (r *HumioViewTokenReconciler) finalize(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioViewToken) error {
	err := r.HumioClient.DeleteViewToken(ctx, client, hvt)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hvt.Spec.TokenSecretName,
			Namespace: hvt.ObjectMeta.Namespace,
		},
	}
	// this is for test environment as in real k8s env garbage collection will delete it
	_ = r.Client.Delete(ctx, secret)

	// set Status.ID to "" so we force a not found error
	_ = r.setState(ctx, hvt, humiov1alpha1.HumioViewStateNotFound, "", "")
	return err
}

func (r *HumioViewTokenReconciler) addFinalizer(ctx context.Context, hvt *humiov1alpha1.HumioViewToken) error {
	r.Log.Info("Adding Finalizer for the HumioViewToken")
	hvt.SetFinalizers(append(hvt.GetFinalizers(), humioFinalizer))
	err := r.Update(ctx, hvt)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioViewToken with finalizer")
	}
	return nil
}

func (r *HumioViewTokenReconciler) setState(ctx context.Context, hvt *humiov1alpha1.HumioViewToken, state string, id string, secret string) error {
	if hvt.Status.State == state && hvt.Status.ID == id {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting ViewToken state to %s", state))
	hvt.Status.State = state
	hvt.Status.ID = id
	if secret != "" {
		hvt.Status.Token = secret
	}
	return r.Status().Update(ctx, hvt)
}

func (r *HumioViewTokenReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

func (r *HumioViewTokenReconciler) validateIPFilter(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioViewToken) (*humiographql.IPFilterDetails, error) {
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

	return ipFilterDetails, nil
}

func (r *HumioViewTokenReconciler) validateViews(ctx context.Context, humioClient *humioapi.Client, hvt *humiov1alpha1.HumioViewToken) ([]string, error) {
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

	for _, view := range viewList.Items {
		if slices.Contains(hvt.Spec.ViewNames, view.Spec.Name) {
			humioView, err := r.HumioClient.GetView(ctx, humioClient, &view, true)
			if err != nil {
				notFound = append(notFound, view.Spec.Name)
			} else {
				foundIds = append(foundIds, humioView.GetId())
			}
		} else {
			notFound = append(notFound, view.Spec.Name)
		}
	}
	if len(foundIds) != len(hvt.Spec.ViewNames) {
		return nil, fmt.Errorf("One or more of the configured viewNames do not exist: %v", notFound)
	}
	return foundIds, nil
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
		return nil, fmt.Errorf("One or more of the configured Permissions do not exist: %v", invalidPermissions)
	}

	return perms, nil
}

type ValidationResult struct {
	IPFilterID  string
	ViewIDs     []string
	Permissions []humiographql.Permission
}

func (r *HumioViewTokenReconciler) validateDependencies(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioViewToken) (*ValidationResult, error) {
	// validate dependencies
	// HumioIPFilter
	ipFilter, err := r.validateIPFilter(ctx, client, hvt)
	if err != nil {
		return nil, fmt.Errorf("ipFilterName validation failed: %w", err)
	}
	//HumioViews
	viewIds, err := r.validateViews(ctx, client, hvt)
	if err != nil {
		return nil, fmt.Errorf("viewsNames validation failed: %w", err)
	}
	//Permissions
	permissions, err := r.validatePermissions(hvt.Spec.Permissions)
	if err != nil {
		return nil, fmt.Errorf("Permissions validation failed: %w", err)
	}
	return &ValidationResult{
		IPFilterID:  ipFilter.GetId(),
		ViewIDs:     viewIds,
		Permissions: permissions,
	}, nil
}

func (r *HumioViewTokenReconciler) ensureViewTokenSecretExists(ctx context.Context, client *humioapi.Client, hvt *humiov1alpha1.HumioViewToken, cluster helpers.ClusterInterface) error {
	if hvt.Spec.TokenSecretName == "" {
		// unexpected situation as TokenSecretName is mandatory
		return fmt.Errorf("ViewToken.Spec.TokenSecretName is mandatory but missing")
	}

	secret, err := r.decryptToken(ctx, cluster, hvt)
	if err != nil {
		return err
	}

	secretData := map[string][]byte{"token": []byte(secret)}
	desiredSecret := kubernetes.ConstructSecret(cluster.Name(), hvt.Namespace, hvt.Spec.TokenSecretName, secretData, hvt.Spec.TokenSecretLabels, hvt.Spec.TokenSecretAnnotations)
	if err := controllerutil.SetControllerReference(hvt, desiredSecret, r.Scheme()); err != nil {
		return fmt.Errorf("could not set controller reference: %w", err)
	}

	existingSecret, err := kubernetes.GetSecret(ctx, r, hvt.Spec.TokenSecretName, hvt.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			err = r.Create(ctx, desiredSecret)
			if err != nil {
				return fmt.Errorf("unable to create view token secret for HumioViewToken: %w", err)
			}
			r.Log.Info("successfully created view token secret", "TokenSecretName", hvt.Spec.TokenSecretName)
		}
	} else {
		// kubernetes secret exists, check if we need to update it
		r.Log.Info("view token secret already exists", "TokenSecretName", hvt.Spec.TokenSecretName)
		if string(existingSecret.Data["token"]) != string(desiredSecret.Data["token"]) ||
			!cmp.Equal(existingSecret.Labels, desiredSecret.Labels) ||
			!cmp.Equal(existingSecret.Annotations, desiredSecret.Annotations) {
			r.Log.Info("secret does not match the token in Humio. Updating token", "TokenSecretName", hvt.Spec.TokenSecretName)
			if err = r.Update(ctx, desiredSecret); err != nil {
				return r.logErrorAndReturn(err, "unable to update view token secret")
			}
		}
	}
	return nil
}

func (r *HumioViewTokenReconciler) readBootstrapTokenSecret(ctx context.Context, cluster helpers.ClusterInterface, namespace string) (string, error) {
	secretName := fmt.Sprintf("%s-bootstrap-token", cluster.Name())
	existingSecret, err := kubernetes.GetSecret(ctx, r, secretName, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get bootstrap token secret %s: %w", secretName, err)
	}

	tokenBytes, exists := existingSecret.Data["secret"]
	if !exists {
		return "", fmt.Errorf("token key not found in secret %s", secretName)
	}

	return string(tokenBytes), nil
}

func (r *HumioViewTokenReconciler) encryptToken(ctx context.Context, cluster helpers.ClusterInterface, hvt *humiov1alpha1.HumioViewToken, token string) (string, error) {
	cypher, err := r.readBootstrapTokenSecret(ctx, cluster, hvt.Namespace)
	if err != nil {
		return "", r.logErrorAndReturn(err, "failed to read bootstrap token")
	}
	encSecret, err := EncryptSecret(token, cypher)
	if err != nil {
		return "", r.logErrorAndReturn(err, "failed to encrypt token")
	}
	return encSecret, nil
}

func (r *HumioViewTokenReconciler) decryptToken(ctx context.Context, cluster helpers.ClusterInterface, hvt *humiov1alpha1.HumioViewToken) (string, error) {
	cypher, err := r.readBootstrapTokenSecret(ctx, cluster, hvt.Namespace)
	if err != nil {
		return "", r.logErrorAndReturn(err, "read bootstrap token")
	}
	decSecret, err := DecryptSecret(hvt.Status.Token, cypher)
	if err != nil {
		return "", r.logErrorAndReturn(err, "failed to decrypt token")
	}
	return decSecret, nil
}

func (r *HumioViewTokenReconciler) viewTokenAlreadyAsExpected(fromK8s *humiov1alpha1.HumioViewToken, fromGql *humiographql.TokenDetailsViewPermissionsToken) (bool, map[string]string, error) {
	// we can only update assigned permissions
	keyValues := map[string]string{}

	// validate permissions
	_, err := r.validatePermissions(fromK8s.Spec.Permissions)
	if err != nil {
		return false, keyValues, r.logErrorAndReturn(err, "permissions validation failed")
	}
	//permissions
	permsFromK8s := fixPermissions(fromK8s.Spec.Permissions)
	permsFromGql := fromGql.Permissions
	sort.Strings(permsFromK8s)
	sort.Strings(permsFromGql)
	if diff := cmp.Diff(permsFromK8s, permsFromGql); diff != "" {
		keyValues["permissions"] = diff
	}

	return len(keyValues) == 0, keyValues, nil
}

// OrganizationOwnedQueries gets added when the token is created on self hosted
func fixPermissions(permissions []string) []string {
	permSet := make(map[string]bool)
	for _, perm := range permissions {
		permSet[perm] = true
	}
	// this one just gets added when Token is created
	permSet[string(humiographql.PermissionOrganizationownedqueries)] = true

	for perm := range permSet {
		if extPerms, found := EquivalentSpecificPermissions[perm]; found {
			for _, extPerm := range extPerms {
				permSet[extPerm] = true
			}
			delete(permSet, perm)
		}
	}

	result := make([]string, 0, len(permSet))
	for perm := range permSet {
		result = append(result, perm)
	}
	return result
}
