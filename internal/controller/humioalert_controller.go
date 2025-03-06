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

// HumioAlertReconciler reconciles a HumioAlert object
type HumioAlertReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioalerts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioalerts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioalerts/finalizers,verbs=update

func (r *HumioAlertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioAlert")

	ha := &humiov1alpha1.HumioAlert{}
	err := r.Get(ctx, req.NamespacedName, ha)
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

	r.Log = r.Log.WithValues("Request.UID", ha.UID)

	cluster, err := helpers.NewCluster(ctx, r, ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName, ha.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioAlertStateConfigError, ha)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set alert state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(ctx context.Context, humioClient humio.Client, ha *humiov1alpha1.HumioAlert) {
		_, err := r.HumioClient.GetAlert(ctx, humioHttpClient, req, ha)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioAlertStateNotFound, ha)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioAlertStateUnknown, ha)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioAlertStateExists, ha)
	}(ctx, r.HumioClient, ha)

	return r.reconcileHumioAlert(ctx, humioHttpClient, ha, req)
}

func (r *HumioAlertReconciler) reconcileHumioAlert(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAlert, req ctrl.Request) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if alert is marked to be deleted")
	if ha.GetDeletionTimestamp() != nil {
		r.Log.Info("Alert marked to be deleted")
		if helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetAlert(ctx, client, req, ha)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				ha.SetFinalizers(helpers.RemoveElement(ha.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, ha)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting alert")
			if err := r.HumioClient.DeleteAlert(ctx, client, req, ha); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete alert returned error")
			}
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if alert requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to alert")
		ha.SetFinalizers(append(ha.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, ha)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if alert needs to be created")
	// Add Alert
	curAlert, err := r.HumioClient.GetAlert(ctx, client, req, ha)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("Alert doesn't exist. Now adding alert")
			addErr := r.HumioClient.AddAlert(ctx, client, req, ha)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create alert")
			}
			r.Log.Info("Created alert",
				"Alert", ha.Spec.Name,
			)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if alert exists")
	}

	r.Log.Info("Checking if alert needs to be updated")

	if asExpected, diffKeysAndValues := alertAlreadyAsExpected(ha, curAlert); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		err = r.HumioClient.UpdateAlert(ctx, client, req, ha)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update alert")
		}
		r.Log.Info("Updated Alert",
			"Alert", ha.Spec.Name,
		)
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioAlertReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioAlert{}).
		Named("humioalert").
		Complete(r)
}

func (r *HumioAlertReconciler) setState(ctx context.Context, state string, ha *humiov1alpha1.HumioAlert) error {
	if ha.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting alert state to %s", state))
	ha.Status.State = state
	return r.Status().Update(ctx, ha)
}

func (r *HumioAlertReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// alertAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func alertAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioAlert, fromGraphQL *humiographql.AlertDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	if diff := cmp.Diff(fromGraphQL.GetDescription(), &fromKubernetesCustomResource.Spec.Description); diff != "" {
		keyValues["description"] = diff
	}
	labelsFromGraphQL := fromGraphQL.GetLabels()
	sort.Strings(labelsFromGraphQL)
	sort.Strings(fromKubernetesCustomResource.Spec.Labels)
	if diff := cmp.Diff(labelsFromGraphQL, fromKubernetesCustomResource.Spec.Labels); diff != "" {
		keyValues["labels"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetThrottleField(), fromKubernetesCustomResource.Spec.ThrottleField); diff != "" {
		keyValues["throttleField"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetThrottleTimeMillis(), int64(fromKubernetesCustomResource.Spec.ThrottleTimeMillis)); diff != "" {
		keyValues["throttleTimeMillis"] = diff
	}
	actionsFromGraphQL := humioapi.GetActionNames(fromGraphQL.GetActionsV2())
	sort.Strings(actionsFromGraphQL)
	sort.Strings(fromKubernetesCustomResource.Spec.Actions)
	if diff := cmp.Diff(actionsFromGraphQL, fromKubernetesCustomResource.Spec.Actions); diff != "" {
		keyValues["actions"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryString(), fromKubernetesCustomResource.Spec.Query.QueryString); diff != "" {
		keyValues["queryString"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetQueryStart(), fromKubernetesCustomResource.Spec.Query.Start); diff != "" {
		keyValues["queryString"] = diff
	}
	if diff := cmp.Diff(fromGraphQL.GetEnabled(), !fromKubernetesCustomResource.Spec.Silenced); diff != "" {
		keyValues["enabled"] = diff
	}
	if !humioapi.QueryOwnershipIsOrganizationOwnership(fromGraphQL.GetQueryOwnership()) {
		keyValues["queryOwnership"] = fmt.Sprintf("%+v", fromGraphQL.GetQueryOwnership())
	}

	return len(keyValues) == 0, keyValues
}
