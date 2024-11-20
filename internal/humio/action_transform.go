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

package humio

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/kubernetes"
)

const (
	ActionTypeWebhook          = "Webhook"
	ActionTypeSlack            = "Slack"
	ActionTypeSlackPostMessage = "SlackPostMessage"
	ActionTypePagerDuty        = "PagerDuty"
	ActionTypeVictorOps        = "VictorOps"
	ActionTypeHumioRepo        = "HumioRepo"
	ActionTypeEmail            = "Email"
	ActionTypeOpsGenie         = "OpsGenie"
)

// ActionFromActionCR converts a HumioAction Kubernetes custom resource to an Action that is valid for the LogScale API.
// It assumes any referenced secret values have been resolved by method resolveSecrets on HumioActionReconciler.
func ActionFromActionCR(ha *humiov1alpha1.HumioAction) (humiographql.ActionDetails, error) {
	at, err := getActionType(ha)
	if err != nil {
		return nil, fmt.Errorf("could not find action type: %w", err)
	}
	switch at {
	case ActionTypeEmail:
		return emailAction(ha)
	case ActionTypeHumioRepo:
		return humioRepoAction(ha)
	case ActionTypeOpsGenie:
		return opsGenieAction(ha)
	case ActionTypePagerDuty:
		return pagerDutyAction(ha)
	case ActionTypeSlack:
		return slackAction(ha)
	case ActionTypeSlackPostMessage:
		return slackPostMessageAction(ha)
	case ActionTypeVictorOps:
		return victorOpsAction(ha)
	case ActionTypeWebhook:
		return webhookAction(ha)
	}
	return nil, fmt.Errorf("invalid action type: %s", at)
}

func emailAction(hn *humiov1alpha1.HumioAction) (*humiographql.ActionDetailsEmailAction, error) {
	var errorList []string
	if len(hn.Spec.EmailProperties.Recipients) == 0 {
		errorList = append(errorList, "property emailProperties.recipients is required")
	}
	if len(errorList) > 0 {
		return nil, ifErrors(ActionTypeEmail, errorList)
	}
	return &humiographql.ActionDetailsEmailAction{
		Name:              hn.Spec.Name,
		Recipients:        hn.Spec.EmailProperties.Recipients,
		EmailBodyTemplate: &hn.Spec.EmailProperties.BodyTemplate,
		SubjectTemplate:   &hn.Spec.EmailProperties.SubjectTemplate,
		UseProxy:          hn.Spec.EmailProperties.UseProxy,
	}, nil
}

func humioRepoAction(hn *humiov1alpha1.HumioAction) (*humiographql.ActionDetailsHumioRepoAction, error) {
	var errorList []string
	apiToken, found := kubernetes.GetSecretForHa(hn)
	if hn.Spec.HumioRepositoryProperties.IngestToken == "" && !found {
		errorList = append(errorList, "property humioRepositoryProperties.ingestToken is required")
	}
	if len(errorList) > 0 {
		return nil, ifErrors(ActionTypeHumioRepo, errorList)
	}
	action := &humiographql.ActionDetailsHumioRepoAction{
		Name: hn.Spec.Name,
	}
	if hn.Spec.HumioRepositoryProperties.IngestToken != "" {
		action.IngestToken = hn.Spec.HumioRepositoryProperties.IngestToken
	} else {
		action.IngestToken = apiToken
	}
	return action, nil
}

func opsGenieAction(hn *humiov1alpha1.HumioAction) (*humiographql.ActionDetailsOpsGenieAction, error) {
	var errorList []string
	apiToken, found := kubernetes.GetSecretForHa(hn)

	if hn.Spec.OpsGenieProperties.GenieKey == "" && !found {
		errorList = append(errorList, "property opsGenieProperties.genieKey is required")
	}
	if hn.Spec.OpsGenieProperties.ApiUrl == "" {
		errorList = append(errorList, "property opsGenieProperties.apiUrl is required")
	}
	if len(errorList) > 0 {
		return nil, ifErrors(ActionTypeOpsGenie, errorList)
	}
	action := &humiographql.ActionDetailsOpsGenieAction{
		Name:     hn.Spec.Name,
		ApiUrl:   hn.Spec.OpsGenieProperties.ApiUrl,
		UseProxy: hn.Spec.OpsGenieProperties.UseProxy,
	}
	if hn.Spec.OpsGenieProperties.GenieKey != "" {
		action.GenieKey = hn.Spec.OpsGenieProperties.GenieKey
	} else {
		action.GenieKey = apiToken
	}
	return action, nil
}

func pagerDutyAction(hn *humiov1alpha1.HumioAction) (*humiographql.ActionDetailsPagerDutyAction, error) {
	var errorList []string
	apiToken, found := kubernetes.GetSecretForHa(hn)
	if hn.Spec.PagerDutyProperties.RoutingKey == "" && !found {
		errorList = append(errorList, "property pagerDutyProperties.routingKey is required")
	}
	if hn.Spec.PagerDutyProperties.Severity == "" {
		errorList = append(errorList, "property pagerDutyProperties.severity is required")
	}
	var severity string
	if hn.Spec.PagerDutyProperties.Severity != "" {
		severity = strings.ToLower(hn.Spec.PagerDutyProperties.Severity)
		acceptedSeverities := []string{"critical", "error", "warning", "info"}
		if !stringInList(severity, acceptedSeverities) {
			errorList = append(errorList, fmt.Sprintf("unsupported severity for pagerDutyProperties: %q. must be one of: %s",
				hn.Spec.PagerDutyProperties.Severity, strings.Join(acceptedSeverities, ", ")))
		}
	}
	if len(errorList) > 0 {
		return nil, ifErrors(ActionTypePagerDuty, errorList)
	}
	action := &humiographql.ActionDetailsPagerDutyAction{
		Name:     hn.Spec.Name,
		Severity: severity,
		UseProxy: hn.Spec.PagerDutyProperties.UseProxy,
	}
	if hn.Spec.PagerDutyProperties.RoutingKey != "" {
		action.RoutingKey = hn.Spec.PagerDutyProperties.RoutingKey
	} else {
		action.RoutingKey = apiToken
	}
	return action, nil
}

func slackAction(hn *humiov1alpha1.HumioAction) (*humiographql.ActionDetailsSlackAction, error) {
	var errorList []string
	slackUrl, found := kubernetes.GetSecretForHa(hn)
	if hn.Spec.SlackProperties.Url == "" && !found {
		errorList = append(errorList, "property slackProperties.url is required")
	}
	if hn.Spec.SlackProperties.Fields == nil {
		errorList = append(errorList, "property slackProperties.fields is required")
	}
	action := &humiographql.ActionDetailsSlackAction{
		Name:     hn.Spec.Name,
		UseProxy: hn.Spec.SlackProperties.UseProxy,
	}
	if hn.Spec.SlackProperties.Url != "" {
		action.Url = hn.Spec.SlackProperties.Url
	} else {
		action.Url = slackUrl
	}
	if _, err := url.ParseRequestURI(action.Url); err != nil {
		errorList = append(errorList, fmt.Sprintf("invalid url for slackProperties.url: %s", err.Error()))
	}
	if len(errorList) > 0 {
		return nil, ifErrors(ActionTypeSlack, errorList)
	}
	for k, v := range hn.Spec.SlackProperties.Fields {
		action.Fields = append(action.Fields,
			humiographql.ActionDetailsFieldsSlackFieldEntry{
				FieldName: k,
				Value:     v,
			},
		)
	}
	return action, nil
}

func slackPostMessageAction(hn *humiov1alpha1.HumioAction) (*humiographql.ActionDetailsSlackPostMessageAction, error) {
	var errorList []string
	apiToken, found := kubernetes.GetSecretForHa(hn)
	if hn.Spec.SlackPostMessageProperties.ApiToken == "" && !found {
		errorList = append(errorList, "property slackPostMessageProperties.apiToken is required")
	}
	if len(hn.Spec.SlackPostMessageProperties.Channels) == 0 {
		errorList = append(errorList, "property slackPostMessageProperties.channels is required")
	}
	if hn.Spec.SlackPostMessageProperties.Fields == nil {
		errorList = append(errorList, "property slackPostMessageProperties.fields is required")
	}
	if len(errorList) > 0 {
		return nil, ifErrors(ActionTypeSlackPostMessage, errorList)
	}
	action := &humiographql.ActionDetailsSlackPostMessageAction{
		Name:     hn.Spec.Name,
		UseProxy: hn.Spec.SlackPostMessageProperties.UseProxy,
		Channels: hn.Spec.SlackPostMessageProperties.Channels,
	}
	if hn.Spec.SlackPostMessageProperties.ApiToken != "" {
		action.ApiToken = hn.Spec.SlackPostMessageProperties.ApiToken
	} else {
		action.ApiToken = apiToken
	}
	for k, v := range hn.Spec.SlackPostMessageProperties.Fields {
		action.Fields = append(action.Fields,
			humiographql.ActionDetailsFieldsSlackFieldEntry{
				FieldName: k,
				Value:     v,
			},
		)
	}

	return action, nil
}

func victorOpsAction(hn *humiov1alpha1.HumioAction) (*humiographql.ActionDetailsVictorOpsAction, error) {
	var errorList []string
	apiToken, found := kubernetes.GetSecretForHa(hn)
	var messageType string
	if hn.Spec.VictorOpsProperties.NotifyUrl == "" && !found {
		errorList = append(errorList, "property victorOpsProperties.notifyUrl is required")
	}
	if hn.Spec.VictorOpsProperties.MessageType == "" {
		errorList = append(errorList, "property victorOpsProperties.messageType is required")
	}
	if hn.Spec.VictorOpsProperties.MessageType != "" {
		messageType = strings.ToLower(hn.Spec.VictorOpsProperties.MessageType)
		acceptedMessageTypes := []string{"critical", "warning", "acknowledgement", "info", "recovery"}
		if !stringInList(strings.ToLower(hn.Spec.VictorOpsProperties.MessageType), acceptedMessageTypes) {
			errorList = append(errorList, fmt.Sprintf("unsupported messageType for victorOpsProperties: %q. must be one of: %s",
				hn.Spec.VictorOpsProperties.MessageType, strings.Join(acceptedMessageTypes, ", ")))
		}
	}
	action := &humiographql.ActionDetailsVictorOpsAction{
		Name:        hn.Spec.Name,
		UseProxy:    hn.Spec.VictorOpsProperties.UseProxy,
		MessageType: messageType,
	}
	if hn.Spec.VictorOpsProperties.NotifyUrl != "" {
		action.NotifyUrl = hn.Spec.VictorOpsProperties.NotifyUrl
	} else {
		action.NotifyUrl = apiToken
	}
	if _, err := url.ParseRequestURI(action.NotifyUrl); err != nil {
		errorList = append(errorList, fmt.Sprintf("invalid url for victorOpsProperties.notifyUrl: %s", err.Error()))
	}
	if len(errorList) > 0 {
		return nil, ifErrors(ActionTypeVictorOps, errorList)
	}
	return action, nil
}

func webhookAction(hn *humiov1alpha1.HumioAction) (*humiographql.ActionDetailsWebhookAction, error) {
	var errorList []string
	apiToken, found := kubernetes.GetSecretForHa(hn)
	var method string
	if hn.Spec.WebhookProperties.Url == "" && !found {
		errorList = append(errorList, "property webhookProperties.url is required")
	}
	if hn.Spec.WebhookProperties.BodyTemplate == "" {
		errorList = append(errorList, "property webhookProperties.bodyTemplate is required")
	}
	if hn.Spec.WebhookProperties.Method == "" {
		errorList = append(errorList, "property webhookProperties.method is required")
	}
	if hn.Spec.WebhookProperties.Method != "" {
		method = strings.ToUpper(hn.Spec.WebhookProperties.Method)
		acceptedMethods := []string{http.MethodGet, http.MethodPost, http.MethodPut}
		if !stringInList(strings.ToUpper(hn.Spec.WebhookProperties.Method), acceptedMethods) {
			errorList = append(errorList, fmt.Sprintf("unsupported method for webhookProperties: %q. must be one of: %s",
				hn.Spec.WebhookProperties.Method, strings.Join(acceptedMethods, ", ")))
		}
	}
	action := &humiographql.ActionDetailsWebhookAction{
		Name:                hn.Spec.Name,
		WebhookBodyTemplate: hn.Spec.WebhookProperties.BodyTemplate,
		Method:              method,
		UseProxy:            hn.Spec.WebhookProperties.UseProxy,
		Headers:             []humiographql.ActionDetailsHeadersHttpHeaderEntry{},
	}
	if hn.Spec.WebhookProperties.Url != "" {
		action.Url = hn.Spec.WebhookProperties.Url
	} else {
		action.Url = apiToken
	}
	if _, err := url.ParseRequestURI(action.Url); err != nil {
		errorList = append(errorList, fmt.Sprintf("invalid url for webhookProperties.url: %s", err.Error()))
	}
	allHeaders, found := kubernetes.GetFullSetOfMergedWebhookheaders(hn)
	if len(allHeaders) != len(hn.Spec.WebhookProperties.Headers)+len(hn.Spec.WebhookProperties.SecretHeaders) {
		errorList = append(errorList, "webhookProperties contains duplicate keys")
	}
	if len(errorList) > 0 {
		return nil, ifErrors(ActionTypeWebhook, errorList)
	}

	if found {
		for k, v := range allHeaders {
			action.Headers = append(action.Headers,
				humiographql.ActionDetailsHeadersHttpHeaderEntry{
					Header: k,
					Value:  v,
				},
			)
		}
	}

	return action, nil
}

func ifErrors(actionType string, errorList []string) error {
	if len(errorList) > 0 {
		return fmt.Errorf("%s failed due to errors: %s", actionType, strings.Join(errorList, ", "))
	}
	return nil
}

func getActionType(ha *humiov1alpha1.HumioAction) (string, error) {
	var actionTypes []string

	if ha.Spec.WebhookProperties != nil {
		actionTypes = append(actionTypes, ActionTypeWebhook)
	}
	if ha.Spec.VictorOpsProperties != nil {
		actionTypes = append(actionTypes, ActionTypeVictorOps)
	}
	if ha.Spec.PagerDutyProperties != nil {
		actionTypes = append(actionTypes, ActionTypePagerDuty)
	}
	if ha.Spec.HumioRepositoryProperties != nil {
		actionTypes = append(actionTypes, ActionTypeHumioRepo)
	}
	if ha.Spec.SlackPostMessageProperties != nil {
		actionTypes = append(actionTypes, ActionTypeSlackPostMessage)
	}
	if ha.Spec.SlackProperties != nil {
		actionTypes = append(actionTypes, ActionTypeSlack)
	}
	if ha.Spec.OpsGenieProperties != nil {
		actionTypes = append(actionTypes, ActionTypeOpsGenie)
	}
	if ha.Spec.EmailProperties != nil {
		actionTypes = append(actionTypes, ActionTypeEmail)
	}

	if len(actionTypes) > 1 {
		return "", fmt.Errorf("found properties for more than one action: %s", strings.Join(actionTypes, ", "))
	}
	if len(actionTypes) < 1 {
		return "", fmt.Errorf("no properties specified for action")
	}
	return actionTypes[0], nil
}

func stringInList(s string, l []string) bool {
	for _, i := range l {
		if s == i {
			return true
		}
	}
	return false
}
