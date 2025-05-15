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

// HumioActionReconciler reconciles a HumioAction object
type HumioActionReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioactions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioactions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioactions/finalizers,verbs=update

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

	cluster, err := helpers.NewCluster(ctx, r, ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName, ha.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioActionStateConfigError, ha)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set action state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	defer func(ctx context.Context, ha *humiov1alpha1.HumioAction) {
		_, err := r.HumioClient.GetAction(ctx, humioHttpClient, ha)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioActionStateNotFound, ha)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioActionStateUnknown, ha)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioActionStateExists, ha)
	}(ctx, ha)

	return r.reconcileHumioAction(ctx, humioHttpClient, ha)
}

func (r *HumioActionReconciler) reconcileHumioAction(ctx context.Context, client *humioapi.Client, ha *humiov1alpha1.HumioAction) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if Action is marked to be deleted")
	if ha.GetDeletionTimestamp() != nil {
		r.Log.Info("Action marked to be deleted")
		if helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetAction(ctx, client, ha)
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
			r.Log.Info("Deleting Action")
			if err := r.HumioClient.DeleteAction(ctx, client, ha); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Delete Action returned error")
			}
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

	if err := r.resolveSecrets(ctx, ha); err != nil {
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

	r.Log.Info("Checking if action needs to be created")
	// Add Action
	curAction, err := r.HumioClient.GetAction(ctx, client, ha)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("Action doesn't exist. Now adding action")
			addErr := r.HumioClient.AddAction(ctx, client, ha)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create action")
			}
			r.Log.Info("Created action",
				"Action", ha.Spec.Name,
			)
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if action exists")
	}

	r.Log.Info("Checking if action needs to be updated")
	// Update
	expectedAction, err := humio.ActionFromActionCR(ha)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not parse expected action")
	}

	if asExpected, diffKeysAndValues := actionAlreadyAsExpected(expectedAction, curAction); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		err = r.HumioClient.UpdateAction(ctx, client, ha)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not update action")
		}
		r.Log.Info("Updated action",
			"Action", ha.Spec.Name,
		)
	}

	r.Log.Info("done reconciling, will requeue", "requeuePeriod", r.RequeuePeriod.String())
	return reconcile.Result{RequeueAfter: r.RequeuePeriod}, nil
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
		Named("humioaction").
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

// actionAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func actionAlreadyAsExpected(expectedAction humiographql.ActionDetails, currentAction humiographql.ActionDetails) (bool, map[string]string) {
	diffMap := compareActions(expectedAction, currentAction)
	actionType := getActionType(expectedAction)

	diffMapWithTypePrefix := addTypePrefix(diffMap, actionType)
	return len(diffMapWithTypePrefix) == 0, diffMapWithTypePrefix
}

func getActionType(action humiographql.ActionDetails) string {
	switch action.(type) {
	case *humiographql.ActionDetailsEmailAction:
		return "email"
	case *humiographql.ActionDetailsHumioRepoAction:
		return "humiorepo"
	case *humiographql.ActionDetailsOpsGenieAction:
		return "opsgenie"
	case *humiographql.ActionDetailsPagerDutyAction:
		return "pagerduty"
	case *humiographql.ActionDetailsSlackAction:
		return "slack"
	case *humiographql.ActionDetailsSlackPostMessageAction:
		return "slackpostmessage"
	case *humiographql.ActionDetailsVictorOpsAction:
		return "victorops"
	case *humiographql.ActionDetailsWebhookAction:
		return "webhook"
	default:
		return "unknown"
	}
}

func compareActions(expectedAction, currentAction humiographql.ActionDetails) map[string]string {
	switch e := expectedAction.(type) {
	case *humiographql.ActionDetailsEmailAction:
		return compareEmailAction(e, currentAction)
	case *humiographql.ActionDetailsHumioRepoAction:
		return compareHumioRepoAction(e, currentAction)
	case *humiographql.ActionDetailsOpsGenieAction:
		return compareOpsGenieAction(e, currentAction)
	case *humiographql.ActionDetailsPagerDutyAction:
		return comparePagerDutyAction(e, currentAction)
	case *humiographql.ActionDetailsSlackAction:
		return compareSlackAction(e, currentAction)
	case *humiographql.ActionDetailsSlackPostMessageAction:
		return compareSlackPostMessageAction(e, currentAction)
	case *humiographql.ActionDetailsVictorOpsAction:
		return compareVictorOpsAction(e, currentAction)
	case *humiographql.ActionDetailsWebhookAction:
		return compareWebhookAction(e, currentAction)
	default:
		return map[string]string{"wrongType": "unknown action type"}
	}
}

func compareEmailAction(expected *humiographql.ActionDetailsEmailAction, current humiographql.ActionDetails) map[string]string {
	diffMap := map[string]string{}

	if c, ok := current.(*humiographql.ActionDetailsEmailAction); ok {
		compareField(diffMap, "name", c.GetName(), expected.GetName())
		compareField(diffMap, "recipients", c.GetRecipients(), expected.GetRecipients())
		compareField(diffMap, "subjectTemplate", c.GetSubjectTemplate(), expected.GetSubjectTemplate())
		compareField(diffMap, "bodyTemplate", c.GetEmailBodyTemplate(), expected.GetEmailBodyTemplate())
		compareField(diffMap, "useProxy", c.GetUseProxy(), expected.GetUseProxy())
	} else {
		diffMap["wrongType"] = fmt.Sprintf("expected type %T but current is %T", expected, current)
	}

	return diffMap
}

func compareHumioRepoAction(expected *humiographql.ActionDetailsHumioRepoAction, current humiographql.ActionDetails) map[string]string {
	diffMap := map[string]string{}

	if c, ok := current.(*humiographql.ActionDetailsHumioRepoAction); ok {
		compareField(diffMap, "name", c.GetName(), expected.GetName())
		compareField(diffMap, "ingestToken", c.GetIngestToken(), expected.GetIngestToken())
	} else {
		diffMap["wrongType"] = fmt.Sprintf("expected type %T but current is %T", expected, current)
	}

	return diffMap
}

func compareOpsGenieAction(expected *humiographql.ActionDetailsOpsGenieAction, current humiographql.ActionDetails) map[string]string {
	diffMap := map[string]string{}

	if c, ok := current.(*humiographql.ActionDetailsOpsGenieAction); ok {
		compareField(diffMap, "name", c.GetName(), expected.GetName())
		compareField(diffMap, "apiUrl", c.GetApiUrl(), expected.GetApiUrl())
		compareField(diffMap, "genieKey", c.GetGenieKey(), expected.GetGenieKey())
		compareField(diffMap, "useProxy", c.GetUseProxy(), expected.GetUseProxy())
	} else {
		diffMap["wrongType"] = fmt.Sprintf("expected type %T but current is %T", expected, current)
	}

	return diffMap
}

func comparePagerDutyAction(expected *humiographql.ActionDetailsPagerDutyAction, current humiographql.ActionDetails) map[string]string {
	diffMap := map[string]string{}

	if c, ok := current.(*humiographql.ActionDetailsPagerDutyAction); ok {
		compareField(diffMap, "name", c.GetName(), expected.GetName())
		compareField(diffMap, "routingKey", c.GetRoutingKey(), expected.GetRoutingKey())
		compareField(diffMap, "severity", c.GetSeverity(), expected.GetSeverity())
		compareField(diffMap, "useProxy", c.GetUseProxy(), expected.GetUseProxy())
	} else {
		diffMap["wrongType"] = fmt.Sprintf("expected type %T but current is %T", expected, current)
	}

	return diffMap
}

func compareSlackAction(expected *humiographql.ActionDetailsSlackAction, current humiographql.ActionDetails) map[string]string {
	diffMap := map[string]string{}

	if c, ok := current.(*humiographql.ActionDetailsSlackAction); ok {
		compareField(diffMap, "name", c.GetName(), expected.GetName())
		compareField(diffMap, "fields", c.GetFields(), expected.GetFields())
		compareField(diffMap, "url", c.GetUrl(), expected.GetUrl())
		compareField(diffMap, "useProxy", c.GetUseProxy(), expected.GetUseProxy())
	} else {
		diffMap["wrongType"] = fmt.Sprintf("expected type %T but current is %T", expected, current)
	}

	return diffMap
}

func compareSlackPostMessageAction(expected *humiographql.ActionDetailsSlackPostMessageAction, current humiographql.ActionDetails) map[string]string {
	diffMap := map[string]string{}

	if c, ok := current.(*humiographql.ActionDetailsSlackPostMessageAction); ok {
		compareField(diffMap, "name", c.GetName(), expected.GetName())
		compareField(diffMap, "apiToken", c.GetApiToken(), expected.GetApiToken())
		compareField(diffMap, "channels", c.GetChannels(), expected.GetChannels())
		compareField(diffMap, "fields", c.GetFields(), expected.GetFields())
		compareField(diffMap, "useProxy", c.GetUseProxy(), expected.GetUseProxy())
	} else {
		diffMap["wrongType"] = fmt.Sprintf("expected type %T but current is %T", expected, current)
	}

	return diffMap
}

func compareVictorOpsAction(expected *humiographql.ActionDetailsVictorOpsAction, current humiographql.ActionDetails) map[string]string {
	diffMap := map[string]string{}

	if c, ok := current.(*humiographql.ActionDetailsVictorOpsAction); ok {
		compareField(diffMap, "name", c.GetName(), expected.GetName())
		compareField(diffMap, "messageType", c.GetMessageType(), expected.GetMessageType())
		compareField(diffMap, "notifyUrl", c.GetNotifyUrl(), expected.GetNotifyUrl())
		compareField(diffMap, "useProxy", c.GetUseProxy(), expected.GetUseProxy())
	} else {
		diffMap["wrongType"] = fmt.Sprintf("expected type %T but current is %T", expected, current)
	}

	return diffMap
}

func compareWebhookAction(expected *humiographql.ActionDetailsWebhookAction, current humiographql.ActionDetails) map[string]string {
	diffMap := map[string]string{}

	if c, ok := current.(*humiographql.ActionDetailsWebhookAction); ok {
		// Sort headers before comparison
		currentHeaders := c.GetHeaders()
		expectedHeaders := expected.GetHeaders()
		sortHeaders(currentHeaders)
		sortHeaders(expectedHeaders)

		compareField(diffMap, "method", c.GetMethod(), expected.GetMethod())
		compareField(diffMap, "name", c.GetName(), expected.GetName())
		compareField(diffMap, "bodyTemplate", c.GetWebhookBodyTemplate(), expected.GetWebhookBodyTemplate())
		compareField(diffMap, "headers", currentHeaders, expectedHeaders)
		compareField(diffMap, "url", c.GetUrl(), expected.GetUrl())
		compareField(diffMap, "ignoreSSL", c.GetIgnoreSSL(), expected.GetIgnoreSSL())
		compareField(diffMap, "useProxy", c.GetUseProxy(), expected.GetUseProxy())
	} else {
		diffMap["wrongType"] = fmt.Sprintf("expected type %T but current is %T", expected, current)
	}

	return diffMap
}

func sortHeaders(headers []humiographql.ActionDetailsHeadersHttpHeaderEntry) {
	sort.SliceStable(headers, func(i, j int) bool {
		return headers[i].Header > headers[j].Header || headers[i].Value > headers[j].Value
	})
}

func compareField(diffMap map[string]string, fieldName string, current, expected interface{}) {
	if diff := cmp.Diff(current, expected); diff != "" {
		if isSecretField(fieldName) {
			diffMap[fieldName] = "<redacted>"
		} else {
			diffMap[fieldName] = diff
		}
	}
}

func isSecretField(fieldName string) bool {
	secretFields := map[string]bool{
		"apiToken":    true,
		"genieKey":    true,
		"headers":     true,
		"ingestToken": true,
		"notifyUrl":   true,
		"routingKey":  true,
		"url":         true,
	}
	return secretFields[fieldName]
}

func addTypePrefix(diffMap map[string]string, actionType string) map[string]string {
	result := make(map[string]string, len(diffMap))
	for k, v := range diffMap {
		result[fmt.Sprintf("%s.%s", actionType, k)] = v
	}
	return result
}
