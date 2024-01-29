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
	"reflect"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"

	humioapi "github.com/humio/cli/api"
)

const (
	ActionIdentifierAnnotation = "humio.com/action-id"

	ActionTypeWebhook          = "Webhook"
	ActionTypeSlack            = "Slack"
	ActionTypeSlackPostMessage = "SlackPostMessage"
	ActionTypePagerDuty        = "PagerDuty"
	ActionTypeVictorOps        = "VictorOps"
	ActionTypeHumioRepo        = "HumioRepo"
	ActionTypeEmail            = "Email"
	ActionTypeOpsGenie         = "OpsGenie"
)

func CRActionFromAPIAction(action *humioapi.Action) (*humiov1alpha1.HumioAction, error) {
	ha := &humiov1alpha1.HumioAction{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ActionIdentifierAnnotation: action.ID,
			},
		},
		Spec: humiov1alpha1.HumioActionSpec{
			Name: action.Name,
		},
	}

	if !reflect.ValueOf(action.EmailAction).IsZero() {
		ha.Spec.EmailProperties = &humiov1alpha1.HumioActionEmailProperties{
			Recipients: action.EmailAction.Recipients,
		}
		if action.EmailAction.BodyTemplate != "" {
			ha.Spec.EmailProperties.BodyTemplate = action.EmailAction.BodyTemplate
		}
		if action.EmailAction.SubjectTemplate != "" {
			ha.Spec.EmailProperties.SubjectTemplate = action.EmailAction.SubjectTemplate
		}
	}

	if !reflect.ValueOf(action.HumioRepoAction).IsZero() {
		ha.Spec.HumioRepositoryProperties = &humiov1alpha1.HumioActionRepositoryProperties{
			IngestToken: action.HumioRepoAction.IngestToken,
		}
	}

	if !reflect.ValueOf(action.OpsGenieAction).IsZero() {
		ha.Spec.OpsGenieProperties = &humiov1alpha1.HumioActionOpsGenieProperties{
			ApiUrl:   action.OpsGenieAction.ApiUrl,
			GenieKey: action.OpsGenieAction.GenieKey,
			UseProxy: action.OpsGenieAction.UseProxy,
		}
	}

	if !reflect.ValueOf(action.PagerDutyAction).IsZero() {
		ha.Spec.PagerDutyProperties = &humiov1alpha1.HumioActionPagerDutyProperties{
			RoutingKey: action.PagerDutyAction.RoutingKey,
			Severity:   action.PagerDutyAction.Severity,
			UseProxy:   action.PagerDutyAction.UseProxy,
		}
	}

	if !reflect.ValueOf(action.SlackAction).IsZero() {
		fields := make(map[string]string)
		for _, field := range action.SlackAction.Fields {
			fields[field.FieldName] = field.Value
		}
		ha.Spec.SlackProperties = &humiov1alpha1.HumioActionSlackProperties{
			Fields:   fields,
			Url:      action.SlackAction.Url,
			UseProxy: action.SlackAction.UseProxy,
		}
	}

	if !reflect.ValueOf(action.SlackPostMessageAction).IsZero() {
		fields := make(map[string]string)
		for _, field := range action.SlackPostMessageAction.Fields {
			fields[field.FieldName] = field.Value
		}
		ha.Spec.SlackPostMessageProperties = &humiov1alpha1.HumioActionSlackPostMessageProperties{
			Channels: action.SlackPostMessageAction.Channels,
			Fields:   fields,
			UseProxy: action.SlackPostMessageAction.UseProxy,
		}
		humiov1alpha1.SecretFromHa(ha, action.SlackPostMessageAction.ApiToken)
	}

	if !reflect.ValueOf(action.VictorOpsAction).IsZero() {
		ha.Spec.VictorOpsProperties = &humiov1alpha1.HumioActionVictorOpsProperties{
			MessageType: action.VictorOpsAction.MessageType,
			NotifyUrl:   action.VictorOpsAction.NotifyUrl,
			UseProxy:    action.VictorOpsAction.UseProxy,
		}
	}

	if !reflect.ValueOf(action.WebhookAction).IsZero() {
		headers := make(map[string]string)
		for _, field := range action.WebhookAction.Headers {
			headers[field.Header] = field.Value
		}
		ha.Spec.WebhookProperties = &humiov1alpha1.HumioActionWebhookProperties{
			BodyTemplate: action.WebhookAction.BodyTemplate,
			Headers:      headers,
			Method:       action.WebhookAction.Method,
			Url:          action.WebhookAction.Url,
			IgnoreSSL:    action.WebhookAction.IgnoreSSL,
			UseProxy:     action.WebhookAction.UseProxy,
		}
	}
	if reflect.ValueOf(action.EmailAction).IsZero() &&
		reflect.ValueOf(action.HumioRepoAction).IsZero() &&
		reflect.ValueOf(action.OpsGenieAction).IsZero() &&
		reflect.ValueOf(action.PagerDutyAction).IsZero() &&
		reflect.ValueOf(action.SlackAction).IsZero() &&
		reflect.ValueOf(action.SlackPostMessageAction).IsZero() &&
		reflect.ValueOf(action.VictorOpsAction).IsZero() &&
		reflect.ValueOf(action.WebhookAction).IsZero() {
		return nil, fmt.Errorf("no action configuration specified")
	}

	return ha, nil
}

func ActionFromActionCR(ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	at, err := actionType(ha)
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

func emailAction(hn *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	var errorList []string
	action, err := baseAction(hn)
	if err != nil {
		return nil, err
	}

	if len(hn.Spec.EmailProperties.Recipients) == 0 {
		errorList = append(errorList, "property emailProperties.recipients is required")
	}
	if len(errorList) > 0 {
		return ifErrors(action, ActionTypeEmail, errorList)
	}
	action.Type = humioapi.ActionTypeEmail
	action.EmailAction.Recipients = hn.Spec.EmailProperties.Recipients
	action.EmailAction.BodyTemplate = hn.Spec.EmailProperties.BodyTemplate
	action.EmailAction.BodyTemplate = hn.Spec.EmailProperties.BodyTemplate
	action.EmailAction.SubjectTemplate = hn.Spec.EmailProperties.SubjectTemplate
	action.EmailAction.UseProxy = hn.Spec.EmailProperties.UseProxy

	return action, nil
}

func humioRepoAction(hn *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	var errorList []string
	action, err := baseAction(hn)
	if err != nil {
		return action, err
	}

	if hn.Spec.HumioRepositoryProperties.IngestToken == "" {
		errorList = append(errorList, "property humioRepositoryProperties.ingestToken is required")
	}
	if len(errorList) > 0 {
		return ifErrors(action, ActionTypeHumioRepo, errorList)
	}
	action.Type = humioapi.ActionTypeHumioRepo
	action.HumioRepoAction.IngestToken = hn.Spec.HumioRepositoryProperties.IngestToken

	return action, nil
}

func opsGenieAction(hn *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	var errorList []string
	action, err := baseAction(hn)
	if err != nil {
		return action, err
	}

	if hn.Spec.OpsGenieProperties.GenieKey == "" {
		errorList = append(errorList, "property opsGenieProperties.genieKey is required")
	}
	if hn.Spec.OpsGenieProperties.ApiUrl == "" {
		errorList = append(errorList, "property opsGenieProperties.apiUrl is required")
	}
	if len(errorList) > 0 {
		return ifErrors(action, ActionTypeOpsGenie, errorList)
	}
	action.Type = humioapi.ActionTypeOpsGenie
	action.OpsGenieAction.GenieKey = hn.Spec.OpsGenieProperties.GenieKey
	action.OpsGenieAction.ApiUrl = hn.Spec.OpsGenieProperties.ApiUrl
	action.OpsGenieAction.UseProxy = hn.Spec.OpsGenieProperties.UseProxy

	return action, nil
}

func pagerDutyAction(hn *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	var errorList []string
	action, err := baseAction(hn)
	if err != nil {
		return action, err
	}

	var severity string
	if hn.Spec.PagerDutyProperties.RoutingKey == "" {
		errorList = append(errorList, "property pagerDutyProperties.routingKey is required")
	}
	if hn.Spec.PagerDutyProperties.Severity == "" {
		errorList = append(errorList, "property pagerDutyProperties.severity is required")
	}
	if hn.Spec.PagerDutyProperties.Severity != "" {
		severity = strings.ToLower(hn.Spec.PagerDutyProperties.Severity)
		acceptedSeverities := []string{"critical", "error", "warning", "info"}
		if !stringInList(severity, acceptedSeverities) {
			errorList = append(errorList, fmt.Sprintf("unsupported severity for pagerDutyProperties: %q. must be one of: %s",
				hn.Spec.PagerDutyProperties.Severity, strings.Join(acceptedSeverities, ", ")))
		}
	}
	if len(errorList) > 0 {
		return ifErrors(action, ActionTypePagerDuty, errorList)
	}
	action.Type = humioapi.ActionTypePagerDuty
	action.PagerDutyAction.RoutingKey = hn.Spec.PagerDutyProperties.RoutingKey
	action.PagerDutyAction.Severity = severity
	action.PagerDutyAction.UseProxy = hn.Spec.PagerDutyProperties.UseProxy

	return action, nil
}

func slackAction(hn *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	var errorList []string
	action, err := baseAction(hn)
	if err != nil {
		return action, err
	}

	if hn.Spec.SlackProperties.Fields == nil {
		errorList = append(errorList, "property slackProperties.fields is required")
	}
	if _, err := url.ParseRequestURI(hn.Spec.SlackProperties.Url); err != nil {
		errorList = append(errorList, fmt.Sprintf("invalid url for slackProperties.url: %s", err.Error()))
	}
	if len(errorList) > 0 {
		return ifErrors(action, ActionTypeSlack, errorList)
	}
	action.Type = humioapi.ActionTypeSlack
	action.SlackAction.Url = hn.Spec.SlackProperties.Url
	action.SlackAction.UseProxy = hn.Spec.SlackProperties.UseProxy
	action.SlackAction.Fields = []humioapi.SlackFieldEntryInput{}
	for k, v := range hn.Spec.SlackProperties.Fields {
		action.SlackAction.Fields = append(action.SlackAction.Fields,
			humioapi.SlackFieldEntryInput{
				FieldName: k,
				Value:     v,
			},
		)
	}

	return action, nil
}

func slackPostMessageAction(hn *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	var errorList []string
	action, err := baseAction(hn)
	if err != nil {
		return action, err
	}

	apiToken, found := humiov1alpha1.HaHasSecret(hn)
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
		return ifErrors(action, ActionTypeSlackPostMessage, errorList)
	}
	if hn.Spec.SlackPostMessageProperties.ApiToken != "" {
		action.SlackPostMessageAction.ApiToken = hn.Spec.SlackPostMessageProperties.ApiToken
	} else {
		action.SlackPostMessageAction.ApiToken = apiToken
	}

	action.Type = humioapi.ActionTypeSlackPostMessage
	action.SlackPostMessageAction.Channels = hn.Spec.SlackPostMessageProperties.Channels
	action.SlackPostMessageAction.UseProxy = hn.Spec.SlackPostMessageProperties.UseProxy
	action.SlackPostMessageAction.Fields = []humioapi.SlackFieldEntryInput{}
	for k, v := range hn.Spec.SlackPostMessageProperties.Fields {
		action.SlackPostMessageAction.Fields = append(action.SlackPostMessageAction.Fields,
			humioapi.SlackFieldEntryInput{
				FieldName: k,
				Value:     v,
			},
		)
	}

	return action, nil
}

func victorOpsAction(hn *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	var errorList []string
	action, err := baseAction(hn)
	if err != nil {
		return action, err
	}

	var messageType string
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
	if _, err := url.ParseRequestURI(hn.Spec.VictorOpsProperties.NotifyUrl); err != nil {
		errorList = append(errorList, fmt.Sprintf("invalid url for victorOpsProperties.notifyUrl: %s", err.Error()))
	}
	if len(errorList) > 0 {
		return ifErrors(action, ActionTypeVictorOps, errorList)
	}
	action.Type = humioapi.ActionTypeVictorOps
	action.VictorOpsAction.MessageType = messageType
	action.VictorOpsAction.NotifyUrl = hn.Spec.VictorOpsProperties.NotifyUrl
	action.VictorOpsAction.UseProxy = hn.Spec.VictorOpsProperties.UseProxy

	return action, nil
}

func webhookAction(hn *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	var errorList []string
	action, err := baseAction(hn)
	if err != nil {
		return action, err
	}

	var method string
	if hn.Spec.WebhookProperties.BodyTemplate == "" {
		errorList = append(errorList, "property webhookProperties.bodyTemplate is required")
	}
	if len(hn.Spec.WebhookProperties.Headers) == 0 {
		errorList = append(errorList, "property webhookProperties.headers is required")
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
	if _, err := url.ParseRequestURI(hn.Spec.WebhookProperties.Url); err != nil {
		errorList = append(errorList, fmt.Sprintf("invalid url for webhookProperties.url: %s", err.Error()))
	}
	if len(errorList) > 0 {
		return ifErrors(action, ActionTypeWebhook, errorList)
	}
	action.Type = humioapi.ActionTypeWebhook
	action.WebhookAction.BodyTemplate = hn.Spec.WebhookProperties.BodyTemplate
	action.WebhookAction.Method = method
	action.WebhookAction.Url = hn.Spec.WebhookProperties.Url
	action.WebhookAction.UseProxy = hn.Spec.WebhookProperties.UseProxy
	action.WebhookAction.Headers = []humioapi.HttpHeaderEntryInput{}
	for k, v := range hn.Spec.WebhookProperties.Headers {
		action.WebhookAction.Headers = append(action.WebhookAction.Headers,
			humioapi.HttpHeaderEntryInput{
				Header: k,
				Value:  v,
			},
		)
	}

	return action, nil
}

func ifErrors(action *humioapi.Action, actionType string, errorList []string) (*humioapi.Action, error) {
	if len(errorList) > 0 {
		return nil, fmt.Errorf("%s failed due to errors: %s", actionType, strings.Join(errorList, ", "))
	}
	return action, nil
}

func baseAction(ha *humiov1alpha1.HumioAction) (*humioapi.Action, error) {
	action := &humioapi.Action{
		Name: ha.Spec.Name,
	}
	if _, ok := ha.ObjectMeta.Annotations[ActionIdentifierAnnotation]; ok {
		action.ID = ha.ObjectMeta.Annotations[ActionIdentifierAnnotation]
	}
	return action, nil
}

func actionType(ha *humiov1alpha1.HumioAction) (string, error) {
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
