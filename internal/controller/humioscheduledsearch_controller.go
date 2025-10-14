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

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humiov1beta1 "github.com/humio/humio-operator/api/v1beta1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioScheduledSearchReconciler reconciles a HumioScheduledSearch object
type HumioScheduledSearchReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioscheduledsearches/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioScheduledSearchReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioScheduledSearch")

	// we reconcile only with the latest version, humiov1beta1 for now
	hss := &humiov1beta1.HumioScheduledSearch{}
	err := r.Get(ctx, req.NamespacedName, hss)
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

	r.Log = r.Log.WithValues("Request.UID", hss.UID)

	cluster, err := helpers.NewCluster(ctx, r, hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName, hss.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1beta1.HumioScheduledSearchStateConfigError, hss)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set scheduled search state")
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(ctx context.Context, hss *humiov1beta1.HumioScheduledSearch) {
		_, err := r.getScheduledSearchVersionAware(ctx, humioHttpClient, hss)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1beta1.HumioScheduledSearchStateNotFound, hss)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1beta1.HumioScheduledSearchStateUnknown, hss)
			return
		}
		_ = r.setState(ctx, humiov1beta1.HumioScheduledSearchStateExists, hss)
	}(ctx, hss)

	return r.reconcileHumioScheduledSearch(ctx, humioHttpClient, hss)
}

func (r *HumioScheduledSearchReconciler) reconcileHumioScheduledSearch(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) (reconcile.Result, error) {
	// depending on the humio version we will be calling different HumioClient functions
	r.Log.Info("Checking if scheduled search is marked to be deleted")
	isMarkedForDeletion := hss.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("ScheduledSearch marked to be deleted")
		if helpers.ContainsElement(hss.GetFinalizers(), HumioFinalizer) {
			_, err := r.getScheduledSearchVersionAware(ctx, client, hss)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hss.SetFinalizers(helpers.RemoveElement(hss.GetFinalizers(), HumioFinalizer))
				err := r.Update(ctx, hss)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for HumioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting scheduled search")
			if err := r.deleteScheduledSearchVersionAware(ctx, client, hss); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete scheduled search returned error")
			}
			// If no error was detected, we need to requeue so that we can remove the finalizer
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if scheduled search requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(hss.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to scheduled search")
		hss.SetFinalizers(append(hss.GetFinalizers(), HumioFinalizer))
		err := r.Update(ctx, hss)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if scheduled search needs to be created")
	curScheduledSearch, err := r.getScheduledSearchVersionAware(ctx, client, hss)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("ScheduledSearch doesn't exist. Now adding scheduled search")
			addErr := r.addScheduledSearchVersionAware(ctx, client, hss)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create scheduled search")
			}
			r.Log.Info("Created scheduled search", "ScheduledSearch", hss.Spec.Name)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if scheduled search")
	}

	r.Log.Info("Checking if scheduled search needs to be updated")
	if err := r.validateActionsForScheduledSearchVersionAware(ctx, client, hss); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not get action id mapping")
	}

	if asExpected, diffKeysAndValues := scheduledSearchAlreadyAsExpectedV2(hss, curScheduledSearch); !asExpected {
		r.Log.Info("information differs, triggering update", "diff", diffKeysAndValues)
		updateErr := r.updateScheduledSearchVersionAware(ctx, client, hss)
		if updateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(updateErr, "could not update scheduled search")
		}
		r.Log.Info("Updated scheduled search", "ScheduledSearch", hss.Spec.Name)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioScheduledSearchReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1beta1.HumioScheduledSearch{}).
		Named("humioscheduledsearch").
		Complete(r)
}

// shouldUseV2API determines if we should use the V2 API based on cluster version
func (r *HumioScheduledSearchReconciler) shouldUseV2API(ctx context.Context, hss *humiov1beta1.HumioScheduledSearch) (bool, error) {
	var scheduledSearchV2MinVersion = humiov1beta1.HumioScheduledSearchV1alpha1DeprecatedInVersion

	clusterVersion, err := helpers.GetClusterImageVersion(ctx, r.Client, hss.Namespace, hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster version: %w", err)
	}
	// Use V2 API if the current version is >= the minimum V2 version
	hasV2, err := helpers.FeatureExists(clusterVersion, scheduledSearchV2MinVersion)
	if err != nil {
		return false, fmt.Errorf("failed to compare versions: %w", err)
	}
	return hasV2, nil
}

// determineAPIVersion determines which API to use and returns the converted v1alpha1 resource if V1 should be used
// Returns (v1alpha1_resource, use_v1_api, error)
func (r *HumioScheduledSearchReconciler) determineAPIVersion(ctx context.Context, hss *humiov1beta1.HumioScheduledSearch) (*humiov1alpha1.HumioScheduledSearch, bool, error) {
	// First check if cluster supports V1 API
	useV2, err := r.shouldUseV2API(ctx, hss)
	if err != nil {
		return nil, false, err
	}

	if useV2 {
		// Cluster supports V2 API
		return nil, false, nil
	}

	// Cluster supports V1 API, check if resource can be converted
	hssV1 := r.convertToV1Alpha1(hss)
	if hssV1 == nil {
		// Resource was originally v1beta1, must use V2 API
		return nil, false, nil
	}

	// Both cluster supports V1 and resource can be converted - use V1 API
	return hssV1, true, nil
}

// getScheduledSearchVersionAware wraps the HumioClient.GetScheduledSearch call with version detection
func (r *HumioScheduledSearchReconciler) getScheduledSearchVersionAware(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) (*humiographql.ScheduledSearchDetailsV2, error) {
	hssV1, useV1, err := r.determineAPIVersion(ctx, hss)
	if err != nil {
		return nil, err
	}

	if useV1 {
		// Use V1 API and convert result to V2 format
		resultV1, err := r.HumioClient.GetScheduledSearch(ctx, client, hssV1)
		if err != nil {
			return nil, err
		}
		// Use the same conversion logic as in the ConvertTo method
		endSeconds, _ := humiov1alpha1.ParseTimeStringToSeconds(resultV1.End)
		startSeconds, _ := humiov1alpha1.ParseTimeStringToSeconds(resultV1.Start)

		return &humiographql.ScheduledSearchDetailsV2{
			Id:             resultV1.Id,
			Name:           resultV1.Name,
			Description:    resultV1.Description,
			QueryString:    resultV1.QueryString,
			TimeZone:       resultV1.TimeZone,
			Schedule:       resultV1.Schedule,
			Enabled:        resultV1.Enabled,
			Labels:         resultV1.Labels,
			ActionsV2:      resultV1.ActionsV2,
			QueryOwnership: resultV1.QueryOwnership,
			// V2-specific fields - convert using the same logic as ConvertTo
			BackfillLimitV2:             helpers.IntPtr(resultV1.BackfillLimit),
			MaxWaitTimeSeconds:          nil, // V1 doesn't have this field
			QueryTimestampType:          "EventTimestamp",
			SearchIntervalSeconds:       startSeconds,
			SearchIntervalOffsetSeconds: helpers.Int64Ptr(endSeconds),
		}, nil
	} else {
		// Use V2 API directly
		return r.HumioClient.GetScheduledSearchV2(ctx, client, hss)
	}
}

// addScheduledSearchVersionAware wraps the HumioClient.AddScheduledSearch call with version detection
func (r *HumioScheduledSearchReconciler) addScheduledSearchVersionAware(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	hssV1, useV1, err := r.determineAPIVersion(ctx, hss)
	if err != nil {
		return err
	}

	if useV1 {
		return r.HumioClient.AddScheduledSearch(ctx, client, hssV1)
	} else {
		return r.HumioClient.AddScheduledSearchV2(ctx, client, hss)
	}
}

// deleteScheduledSearchVersionAware wraps the HumioClient.DeleteScheduledSearch call with version detection
func (r *HumioScheduledSearchReconciler) deleteScheduledSearchVersionAware(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	hssV1, useV1, err := r.determineAPIVersion(ctx, hss)
	if err != nil {
		return err
	}

	if useV1 {
		return r.HumioClient.DeleteScheduledSearch(ctx, client, hssV1)
	} else {
		return r.HumioClient.DeleteScheduledSearchV2(ctx, client, hss)
	}
}

// updateScheduledSearchVersionAware wraps the HumioClient.UpdateScheduledSearch call with version detection
func (r *HumioScheduledSearchReconciler) updateScheduledSearchVersionAware(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	hssV1, useV1, err := r.determineAPIVersion(ctx, hss)
	if err != nil {
		return err
	}

	if useV1 {
		return r.HumioClient.UpdateScheduledSearch(ctx, client, hssV1)
	} else {
		return r.HumioClient.UpdateScheduledSearchV2(ctx, client, hss)
	}
}

// validateActionsForScheduledSearchVersionAware wraps the HumioClient.ValidateActionsForScheduledSearch call with version detection
func (r *HumioScheduledSearchReconciler) validateActionsForScheduledSearchVersionAware(ctx context.Context, client *humioapi.Client, hss *humiov1beta1.HumioScheduledSearch) error {
	hssV1, useV1, err := r.determineAPIVersion(ctx, hss)
	if err != nil {
		return err
	}

	if useV1 {
		return r.HumioClient.ValidateActionsForScheduledSearch(ctx, client, hssV1)
	} else {
		return r.HumioClient.ValidateActionsForScheduledSearchV2(ctx, client, hss)
	}
}

// convertToV1Alpha1 converts a v1beta1.HumioScheduledSearch to v1alpha1.HumioScheduledSearch
// using the existing conversion method. If conversion fails (resource was originally v1beta1),
// returns nil to indicate V2 API should be used instead.
func (r *HumioScheduledSearchReconciler) convertToV1Alpha1(hss *humiov1beta1.HumioScheduledSearch) *humiov1alpha1.HumioScheduledSearch {
	hssV1 := &humiov1alpha1.HumioScheduledSearch{}
	err := hssV1.ConvertFrom(hss)
	if err != nil {
		// If conversion fails, this means the resource was originally v1beta1 (not converted from v1alpha1)
		// In this case, we should not be calling V1 APIs, so return nil
		r.Log.Info("resource was originally v1beta1, conversion to v1alpha1 not supported", "HumioScheduledSearch", hss.Name, "error", err.Error())
		return nil
	}
	return hssV1
}

func (r *HumioScheduledSearchReconciler) setState(ctx context.Context, state string, hss *humiov1beta1.HumioScheduledSearch) error {
	if hss.Status.State == state {
		return nil
	}
	// fetch fresh copy
	key := types.NamespacedName{
		Name:      hss.Name,
		Namespace: hss.Namespace,
	}
	_ = r.Get(ctx, key, hss)

	r.Log.Info(fmt.Sprintf("setting scheduled search to %s", state))
	hss.Status.State = state
	return r.Status().Update(ctx, hss)
}

func (r *HumioScheduledSearchReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// scheduledSearchAlreadyAsExpectedV2 compares v1beta1 resource and V2 GraphQL result
// It returns a boolean indicating if the details from GraphQL already matches what is in the desired state.
// If they do not match, a map is returned with details on what the diff is.
func scheduledSearchAlreadyAsExpectedV2(fromKubernetesCustomResource *humiov1beta1.HumioScheduledSearch, fromGraphQL *humiographql.ScheduledSearchDetailsV2) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetDescription(), &fromKubernetesCustomResource.Spec.Description); diff != "" {
		keyValues["description"] = diff
	}

	labelsFromGraphQL := fromGraphQL.GetLabels()
	labelsFromKubernetes := fromKubernetesCustomResource.Spec.Labels
	if labelsFromKubernetes == nil {
		labelsFromKubernetes = make([]string, 0)
	}
	sort.Strings(labelsFromGraphQL)
	sort.Strings(labelsFromKubernetes)
	if diff := cmp.Diff(labelsFromGraphQL, labelsFromKubernetes); diff != "" {
		keyValues["labels"] = diff
	}
	actionsFromGraphQL := humioapi.GetActionNames(fromGraphQL.GetActionsV2())
	sort.Strings(actionsFromGraphQL)
	sort.Strings(fromKubernetesCustomResource.Spec.Actions)
	if diff := cmp.Diff(actionsFromGraphQL, fromKubernetesCustomResource.Spec.Actions); diff != "" {
		keyValues["actions"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetTimeZone(), fromKubernetesCustomResource.Spec.TimeZone); diff != "" {
		keyValues["timeZone"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryString(), fromKubernetesCustomResource.Spec.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetSchedule(), fromKubernetesCustomResource.Spec.Schedule); diff != "" {
		keyValues["schedule"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetEnabled(), fromKubernetesCustomResource.Spec.Enabled); diff != "" {
		keyValues["enabled"] = diff
	}
	if !humioapi.QueryOwnershipIsOrganizationOwnership(fromGraphQL.GetQueryOwnership()) {
		keyValues["queryOwnership"] = fmt.Sprintf("%+v", fromGraphQL.GetQueryOwnership())
	}
	if diff := cmp.Diff(fromGraphQL.GetBackfillLimitV2(), fromKubernetesCustomResource.Spec.BackfillLimit); diff != "" {
		keyValues["backfillLimit"] = diff
	}
	gqlMaxWaitTimeSeconds := int64(0)
	if backfill := fromGraphQL.GetMaxWaitTimeSeconds(); backfill != nil {
		gqlMaxWaitTimeSeconds = *backfill
	}
	if diff := cmp.Diff(gqlMaxWaitTimeSeconds, fromKubernetesCustomResource.Spec.MaxWaitTimeSeconds); diff != "" {
		keyValues["maxWaitTimeSeconds"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryTimestampType(), fromKubernetesCustomResource.Spec.QueryTimestampType); diff != "" {
		keyValues["queryTimestampType"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetSearchIntervalSeconds(), fromKubernetesCustomResource.Spec.SearchIntervalSeconds); diff != "" {
		keyValues["searchIntervalSeconds"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetSearchIntervalOffsetSeconds(), fromKubernetesCustomResource.Spec.SearchIntervalOffsetSeconds); diff != "" {
		keyValues["searchIntervalOffsetSeconds"] = diff
	}
	return len(keyValues) == 0, keyValues
}
