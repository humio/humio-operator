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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioActionReconciler reconciles a HumioAction object
type HumioActionReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions/finalizers,verbs=update

func (r *HumioActionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioAction")

	ha := &humiov1alpha1.HumioAction{}
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

	cluster, err := helpers.NewCluster(ctx, r, ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName, ha.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioActionStateConfigError, ha)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set action state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}

	err = r.resolveSecrets(ctx, ha)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not resolve secret references")
	}

	if _, validateErr := humio.ActionFromActionCR(ha); validateErr != nil {
		r.Log.Error(validateErr, "unable to validate action")
		setStateErr := r.setState(ctx, humiov1alpha1.HumioActionStateConfigError, ha)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set action state")
		}
		return reconcile.Result{}, validateErr
	}

	defer func(ctx context.Context, humioClient humio.Client, ha *humiov1alpha1.HumioAction) {
		_, err := r.HumioClient.GetAction(cluster.Config(), req, ha)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioActionStateNotFound, ha)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioActionStateUnknown, ha)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioActionStateExists, ha)
	}(ctx, r.HumioClient, ha)

	return r.reconcileHumioAction(ctx, cluster.Config(), ha, req)
}

func (r *HumioActionReconciler) reconcileHumioAction(ctx context.Context, config *humioapi.Config, ha *humiov1alpha1.HumioAction, req ctrl.Request) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if Action is marked to be deleted")
	isMarkedForDeletion := ha.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("Action marked to be deleted")
		if helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting Action")
			if err := r.HumioClient.DeleteAction(config, req, ha); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete Action returned error")
			}

			r.Log.Info("Action Deleted. Removing finalizer")
			ha.SetFinalizers(helpers.RemoveElement(ha.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, ha)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if Action requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to Action")
		ha.SetFinalizers(append(ha.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, ha)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if action needs to be created")
	// Add Action
	curAction, err := r.HumioClient.GetAction(config, req, ha)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		r.Log.Info("Action doesn't exist. Now adding action")
		addedAction, err := r.HumioClient.AddAction(config, req, ha)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create action")
		}
		r.Log.Info("Created action", "Action", ha.Spec.Name, "ID", addedAction.ID)
		return reconcile.Result{Requeue: true}, nil
	}
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if action exists")
	}

	r.Log.Info("Checking if action needs to be updated")
	// Update
	expectedAction, err := humio.ActionFromActionCR(ha)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not parse expected action")
	}
	sanitizeAction(curAction)
	sanitizeAction(expectedAction)
	if !cmp.Equal(*curAction, *expectedAction) {
		r.Log.Info("Action differs, triggering update", "actionDiff", cmp.Diff(*curAction, *expectedAction))
		action, err := r.HumioClient.UpdateAction(config, req, ha)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update action")
		}
		if action != nil {
			r.Log.Info(fmt.Sprintf("Updated action %q", ha.Spec.Name), "newAction", fmt.Sprintf("%#+v", action))
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

func (r *HumioActionReconciler) resolveSecrets(ctx context.Context, ha *humiov1alpha1.HumioAction) error {
	var err error
	var apiToken string

	if ha.Spec.SlackPostMessageProperties != nil {
		apiToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.SlackPostMessageProperties.ApiToken, ha.Spec.SlackPostMessageProperties.ApiTokenSource)
		if err != nil {
			return fmt.Errorf("slackPostMessageProperties.apiTokenSource.%v", err)
		}
	}

	if ha.Spec.SlackProperties != nil {
		apiToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.SlackProperties.Url, ha.Spec.SlackProperties.UrlSource)
		if err != nil {
			return fmt.Errorf("slackProperties.urlSource.%v", err)
		}

	}

	if ha.Spec.OpsGenieProperties != nil {
		apiToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.OpsGenieProperties.GenieKey, ha.Spec.OpsGenieProperties.GenieKeySource)
		if err != nil {
			return fmt.Errorf("opsGenieProperties.genieKeySource.%v", err)
		}
	}

	if ha.Spec.HumioRepositoryProperties != nil {
		apiToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.HumioRepositoryProperties.IngestToken, ha.Spec.HumioRepositoryProperties.IngestTokenSource)
		if err != nil {
			return fmt.Errorf("humioRepositoryProperties.ingestTokenSource.%v", err)
		}
	}

	if ha.Spec.PagerDutyProperties != nil {
		apiToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.PagerDutyProperties.RoutingKey, ha.Spec.PagerDutyProperties.RoutingKeySource)
		if err != nil {
			return fmt.Errorf("pagerDutyProperties.routingKeySource.%v", err)
		}
	}

	if ha.Spec.VictorOpsProperties != nil {
		apiToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.VictorOpsProperties.NotifyUrl, ha.Spec.VictorOpsProperties.NotifyUrlSource)
		if err != nil {
			return fmt.Errorf("victorOpsProperties.notifyUrlSource.%v", err)
		}
	}

	if ha.Spec.WebhookProperties != nil {
		apiToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.WebhookProperties.Url, ha.Spec.WebhookProperties.UrlSource)
		if err != nil {
			return fmt.Errorf("webhookProperties.UrlSource.%v", err)
		}

		allWebhookActionHeaders := map[string]string{}
		if ha.Spec.WebhookProperties.SecretHeaders != nil {
			for i := range ha.Spec.WebhookProperties.SecretHeaders {
				headerName := ha.Spec.WebhookProperties.SecretHeaders[i].Name
				headerValueSource := ha.Spec.WebhookProperties.SecretHeaders[i].ValueFrom
				allWebhookActionHeaders[headerName], err = r.resolveField(ctx, ha.Namespace, "", headerValueSource)
				if err != nil {
					return fmt.Errorf("webhookProperties.secretHeaders.%v", err)
				}
			}

		}
		kubernetes.StoreFullSetOfMergedWebhookActionHeaders(ha, allWebhookActionHeaders)
	}

	kubernetes.StoreSingleSecretForHa(ha, apiToken)

	return nil
}

func (r *HumioActionReconciler) resolveField(ctx context.Context, namespace, value string, ref humiov1alpha1.VarSource) (string, error) {
	if value != "" {
		return value, nil
	}

	if ref.SecretKeyRef != nil {
		secret, err := kubernetes.GetSecret(ctx, r, ref.SecretKeyRef.Name, namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return "", fmt.Errorf("secretKeyRef was set but no secret exists by name %s in namespace %s", ref.SecretKeyRef.Name, namespace)
			}
			return "", fmt.Errorf("unable to get secret with name %s in namespace %s", ref.SecretKeyRef.Name, namespace)
		}
		value, ok := secret.Data[ref.SecretKeyRef.Key]
		if !ok {
			return "", fmt.Errorf("secretKeyRef was found but it does not contain the key %s", ref.SecretKeyRef.Key)
		}
		return string(value), nil
	}

	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioActionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioAction{}).
		Complete(r)
}

func (r *HumioActionReconciler) setState(ctx context.Context, state string, hr *humiov1alpha1.HumioAction) error {
	if hr.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting action state to %s", state))
	hr.Status.State = state
	return r.Status().Update(ctx, hr)
}

func (r *HumioActionReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

func sanitizeAction(action *humioapi.Action) {
	action.ID = ""
}
