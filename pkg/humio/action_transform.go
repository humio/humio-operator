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
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"

	"github.com/humio/humio-operator/pkg/helpers"

	humioapi "github.com/humio/cli/api"
)

const (
	ActionIdentifierAnnotation = "humio.com/action-id"
)

var (
	propertiesMap = map[string]string{
		humioapi.NotifierTypeWebHook:          "webhookProperties",
		humioapi.NotifierTypeVictorOps:        "victorOpsProperties",
		humioapi.NotifierTypePagerDuty:        "pagerDutyProperties",
		humioapi.NotifierTypeHumioRepo:        "humioRepositoryProperties",
		humioapi.NotifierTypeSlackPostMessage: "slackPostMessageProperties",
		humioapi.NotifierTypeSlack:            "victorOpsProperties",
		humioapi.NotifierTypeOpsGenie:         "opsGenieProperties",
		humioapi.NotifierTypeEmail:            "emailProperties",
	}
)

func ActionFromNotifier(notifier *humioapi.Notifier) (*humiov1alpha1.HumioAction, error) {
	ha := &humiov1alpha1.HumioAction{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				ActionIdentifierAnnotation: notifier.ID,
			},
		},
		Spec: humiov1alpha1.HumioActionSpec{
			Name: notifier.Name,
		},
	}

	switch notifier.Entity {
	case humioapi.NotifierTypeEmail:
		var recipients []string
		for _, r := range notifier.Properties["recipients"].([]interface{}) {
			recipients = append(recipients, r.(string))
		}
		ha.Spec.EmailProperties = &humiov1alpha1.HumioActionEmailProperties{
			Recipients: recipients,
		}
		if notifier.Properties["bodyTemplate"] != nil {
			ha.Spec.EmailProperties.BodyTemplate = notifier.Properties["bodyTemplate"].(string)
		}
		if notifier.Properties["subjectTemplate"] != nil {
			ha.Spec.EmailProperties.SubjectTemplate = notifier.Properties["subjectTemplate"].(string)
		}
	case humioapi.NotifierTypeHumioRepo:
		ha.Spec.HumioRepositoryProperties = &humiov1alpha1.HumioActionRepositoryProperties{}
		if notifier.Properties["ingestToken"] != nil {
			ha.Spec.HumioRepositoryProperties.IngestToken = notifier.Properties["ingestToken"].(string)
		}
	case humioapi.NotifierTypeOpsGenie:
		ha.Spec.OpsGenieProperties = &humiov1alpha1.HumioActionOpsGenieProperties{}
		if notifier.Properties["genieKey"] != nil {
			ha.Spec.OpsGenieProperties.GenieKey = notifier.Properties["genieKey"].(string)
		}
		if notifier.Properties["apiUrl"] != nil {
			ha.Spec.OpsGenieProperties.ApiUrl = notifier.Properties["apiUrl"].(string)
		}
		if notifier.Properties["useProxy"] != nil {
			ha.Spec.OpsGenieProperties.UseProxy = notifier.Properties["useProxy"].(bool)
		}
	case humioapi.NotifierTypePagerDuty:
		ha.Spec.PagerDutyProperties = &humiov1alpha1.HumioActionPagerDutyProperties{}
		if notifier.Properties["severity"] != nil {
			ha.Spec.PagerDutyProperties.Severity = notifier.Properties["severity"].(string)
		}
		if notifier.Properties["routingKey"] != nil {
			ha.Spec.PagerDutyProperties.RoutingKey = notifier.Properties["routingKey"].(string)
		}
	case humioapi.NotifierTypeSlack:
		fields := make(map[string]string)
		for k, v := range notifier.Properties["fields"].(map[string]interface{}) {
			fields[k] = v.(string)
		}
		ha.Spec.SlackProperties = &humiov1alpha1.HumioActionSlackProperties{
			Fields: fields,
		}
		if notifier.Properties["url"] != nil {
			ha.Spec.SlackProperties.Url = notifier.Properties["url"].(string)
		}
	case humioapi.NotifierTypeSlackPostMessage:
		fields := make(map[string]string)
		for k, v := range notifier.Properties["fields"].(map[string]interface{}) {
			fields[k] = v.(string)
		}
		var channels []string
		for _, c := range notifier.Properties["channels"].([]interface{}) {
			channels = append(channels, c.(string))
		}
		ha.Spec.SlackPostMessageProperties = &humiov1alpha1.HumioActionSlackPostMessageProperties{
			Channels: channels,
			Fields:   fields,
		}
		if notifier.Properties["apiToken"] != nil {
			ha.Spec.SlackPostMessageProperties.ApiToken = notifier.Properties["apiToken"].(string)
		}
		if notifier.Properties["useProxy"] != nil {
			ha.Spec.SlackPostMessageProperties.UseProxy = notifier.Properties["useProxy"].(bool)
		}
	case humioapi.NotifierTypeVictorOps:
		ha.Spec.VictorOpsProperties = &humiov1alpha1.HumioActionVictorOpsProperties{}
		if notifier.Properties["messageType"] != nil {
			ha.Spec.VictorOpsProperties.MessageType = notifier.Properties["messageType"].(string)
		}
		if notifier.Properties["notifyUrl"] != nil {
			ha.Spec.VictorOpsProperties.NotifyUrl = notifier.Properties["notifyUrl"].(string)
		}
	case humioapi.NotifierTypeWebHook:
		headers := make(map[string]string)
		for k, v := range notifier.Properties["headers"].(map[string]interface{}) {
			headers[k] = v.(string)
		}
		ha.Spec.WebhookProperties = &humiov1alpha1.HumioActionWebhookProperties{
			Headers: headers,
		}
		if notifier.Properties["bodyTemplate"] != nil {
			ha.Spec.WebhookProperties.BodyTemplate = notifier.Properties["bodyTemplate"].(string)
		}
		if notifier.Properties["method"] != nil {
			ha.Spec.WebhookProperties.Method = notifier.Properties["method"].(string)
		}
		if notifier.Properties["url"] != nil {
			ha.Spec.WebhookProperties.Url = notifier.Properties["url"].(string)
		}
	default:
		return &humiov1alpha1.HumioAction{}, fmt.Errorf("invalid notifier type: %s", notifier.Entity)
	}

	return ha, nil
}

func NotifierFromAction(ha *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	at, err := actionType(ha)
	if err != nil {
		return &humioapi.Notifier{}, fmt.Errorf("could not find action type: %s", err)
	}
	switch at {
	case humioapi.NotifierTypeEmail:
		return emailAction(ha)
	case humioapi.NotifierTypeHumioRepo:
		return humioRepoAction(ha)
	case humioapi.NotifierTypeOpsGenie:
		return opsGenieAction(ha)
	case humioapi.NotifierTypePagerDuty:
		return pagerDutyAction(ha)
	case humioapi.NotifierTypeSlack:
		return slackAction(ha)
	case humioapi.NotifierTypeSlackPostMessage:
		return slackPostMessageAction(ha)
	case humioapi.NotifierTypeVictorOps:
		return victorOpsAction(ha)
	case humioapi.NotifierTypeWebHook:
		return webhookAction(ha)
	}
	return &humioapi.Notifier{}, fmt.Errorf("invalid action type: %s", at)
}

func emailAction(hn *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	var errorList []string
	notifier, err := baseNotifier(hn)
	if err != nil {
		return notifier, err
	}
	if err := setListOfStringsProperty(notifier, "recipients", "emailProperties.recipients",
		hn.Spec.EmailProperties.Recipients, []interface{}{""}, true); err != nil {
		errorList = append(errorList, err.Error())
	}
	if err := setStringProperty(notifier, "bodyTemplate", "emailProperties.bodyTemplate",
		hn.Spec.EmailProperties.BodyTemplate, "", false); err != nil {
		errorList = append(errorList, err.Error())
	}
	if err := setStringProperty(notifier, "subjectTemplate", "emailProperties.subjectTemplate",
		hn.Spec.EmailProperties.SubjectTemplate, "", false); err != nil {
		errorList = append(errorList, err.Error())
	}
	return ifErrors(notifier, humioapi.NotifierTypeEmail, errorList)
}

func humioRepoAction(hn *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	var errorList []string
	notifier, err := baseNotifier(hn)
	if err != nil {
		return notifier, err
	}
	if err := setStringProperty(notifier, "ingestToken", "humioRepository.ingestToken",
		hn.Spec.HumioRepositoryProperties.IngestToken, "", true); err != nil {
		errorList = append(errorList, err.Error())
	}
	return ifErrors(notifier, humioapi.NotifierTypeHumioRepo, errorList)
}

func opsGenieAction(hn *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	var errorList []string
	notifier, err := baseNotifier(hn)
	if err != nil {
		return notifier, err
	}
	if err := setStringProperty(notifier, "apiUrl", "opsGenieProperties.apiUrl",
		hn.Spec.OpsGenieProperties.ApiUrl, "https://api.opsgenie.com", false); err != nil {
		errorList = append(errorList, err.Error())
	}
	if err := setStringProperty(notifier, "genieKey", "opsGenieProperties.genieKey",
		hn.Spec.OpsGenieProperties.GenieKey, "", true); err != nil {
		errorList = append(errorList, err.Error())
	}
	if err := setBoolProperty(notifier, "useProxy", "opsGenieProperties.useProxy",
		helpers.BoolPtr(hn.Spec.OpsGenieProperties.UseProxy), helpers.BoolPtr(true), false); err != nil {
		errorList = append(errorList, err.Error())
	}
	return ifErrors(notifier, humioapi.NotifierTypeOpsGenie, errorList)
}

func pagerDutyAction(hn *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	var errorList []string
	notifier, err := baseNotifier(hn)
	if err != nil {
		return notifier, err
	}
	if err := setStringProperty(notifier, "routingKey", "pagerDutyProperties.routingKey",
		hn.Spec.PagerDutyProperties.RoutingKey, "", true); err != nil {
		errorList = append(errorList, err.Error())
	}

	if err := setStringProperty(notifier, "severity", "pagerDutyProperties.severity",
		strings.ToLower(hn.Spec.PagerDutyProperties.Severity), "", true); err == nil {
		acceptedSeverities := []string{"critical", "error", "warning", "info"}
		if !stringInList(strings.ToLower(hn.Spec.PagerDutyProperties.Severity), acceptedSeverities) {
			errorList = append(errorList, fmt.Sprintf("unsupported severity for PagerdutyProperties: \"%s\". must be one of: %s",
				hn.Spec.PagerDutyProperties.Severity, strings.Join(acceptedSeverities, ", ")))
		}
	} else {
		errorList = append(errorList, err.Error())
	}

	return ifErrors(notifier, humioapi.NotifierTypePagerDuty, errorList)
}

func slackAction(hn *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	var errorList []string
	notifier, err := baseNotifier(hn)
	if err != nil {
		return notifier, err
	}
	if err := setMapOfStringsProperty(notifier, "fields", "slackProperties.fields",
		hn.Spec.SlackProperties.Fields, map[string]interface{}{}, true); err != nil {
		errorList = append(errorList, err.Error())
	}
	if _, err := url.ParseRequestURI(hn.Spec.SlackProperties.Url); err == nil {
		if err := setStringProperty(notifier, "url", "slackProperties.url",
			hn.Spec.SlackProperties.Url, "", true); err != nil {
			errorList = append(errorList, err.Error())
		}
	} else {
		errorList = append(errorList, fmt.Sprintf("invalid url for slackProperties.url: %s", err))
	}

	return ifErrors(notifier, humioapi.NotifierTypeSlack, errorList)
}

func slackPostMessageAction(hn *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	var errorList []string
	notifier, err := baseNotifier(hn)
	if err != nil {
		return notifier, err
	}
	if err := setStringProperty(notifier, "apiToken", "slackPostMessageProperties.apiToken",
		hn.Spec.SlackPostMessageProperties.ApiToken, "", true); err != nil {
		errorList = append(errorList, err.Error())
	}
	if err := setListOfStringsProperty(notifier, "channels", "slackPostMessageProperties.channels",
		hn.Spec.SlackPostMessageProperties.Channels, []interface{}{""}, true); err != nil {
		errorList = append(errorList, err.Error())
	}
	if err := setMapOfStringsProperty(notifier, "fields", "slackPostMessageProperties.fields",
		hn.Spec.SlackPostMessageProperties.Fields, map[string]interface{}{}, true); err != nil {
		errorList = append(errorList, err.Error())
	}
	if err := setBoolProperty(notifier, "useProxy", "slackPostMessageProperties.useProxy",
		helpers.BoolPtr(hn.Spec.SlackPostMessageProperties.UseProxy), helpers.BoolPtr(true), false); err != nil {
		errorList = append(errorList, err.Error())
	}
	return ifErrors(notifier, humioapi.NotifierTypeSlackPostMessage, errorList)
}

func victorOpsAction(hn *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	var errorList []string
	notifier, err := baseNotifier(hn)
	if err != nil {
		return notifier, err
	}

	if err := setStringProperty(notifier, "messageType", "victorOpsProperties.messageType",
		hn.Spec.VictorOpsProperties.MessageType, "", true); err == nil {
		acceptedMessageTypes := []string{"critical", "warning", "acknowledgement", "info", "recovery"}
		if !stringInList(strings.ToLower(notifier.Properties["messageType"].(string)), acceptedMessageTypes) {
			errorList = append(errorList, fmt.Sprintf("unsupported messageType for victorOpsProperties: \"%s\". must be one of: %s",
				notifier.Properties["messageType"].(string), strings.Join(acceptedMessageTypes, ", ")))
		}
	} else {
		errorList = append(errorList, err.Error())
	}

	if err := setStringProperty(notifier, "notifyUrl", "victorOpsProperties.notifyUrl",
		hn.Spec.VictorOpsProperties.NotifyUrl, "", true); err == nil {
		if _, err := url.ParseRequestURI(notifier.Properties["notifyUrl"].(string)); err != nil {
			errorList = append(errorList, fmt.Sprintf("invalid url for victorOpsProperties.notifyUrl: %s", err))
		}
	} else {
		errorList = append(errorList, err.Error())
	}

	return ifErrors(notifier, humioapi.NotifierTypeVictorOps, errorList)
}

func webhookAction(hn *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	var errorList []string
	notifier, err := baseNotifier(hn)
	if err != nil {
		return notifier, err
	}

	if err := setStringProperty(notifier, "bodyTemplate", "webhookProperties.bodyTemplate",
		hn.Spec.WebhookProperties.BodyTemplate, "", true); err != nil {
		errorList = append(errorList, err.Error())
	}
	if err := setMapOfStringsProperty(notifier, "headers", "webhookProperties.headers",
		hn.Spec.WebhookProperties.Headers, map[string]interface{}{}, true); err != nil {
		errorList = append(errorList, err.Error())
	}
	// TODO: validate method
	if err := setStringProperty(notifier, "method", "webhookProperties.method",
		hn.Spec.WebhookProperties.Method, "", true); err != nil {
		errorList = append(errorList, err.Error())
	}
	// TODO: validate url
	if err := setStringProperty(notifier, "url", "webhookProperties.url",
		hn.Spec.WebhookProperties.Url, "", true); err != nil {
		errorList = append(errorList, err.Error())
	}
	return ifErrors(notifier, humioapi.NotifierTypeWebHook, errorList)
}

func ifErrors(notifier *humioapi.Notifier, actionType string, errorList []string) (*humioapi.Notifier, error) {
	if len(errorList) > 0 {
		return &humioapi.Notifier{}, fmt.Errorf("%s failed due to errors: %s", actionType, strings.Join(errorList, ", "))
	}
	return notifier, nil
}

func setBoolProperty(notifier *humioapi.Notifier, key string, propertyName string, property *bool, defaultProperty *bool, required bool) error {
	if property != nil {
		notifier.Properties[key] = *property
	} else {
		if required {
			return fmt.Errorf("property %s is required", propertyName)
		}
		if defaultProperty != nil {
			notifier.Properties[key] = *defaultProperty
		}
	}
	return nil
}

func setStringProperty(notifier *humioapi.Notifier, key string, propertyName string, property string, defaultProperty string, required bool) error {
	if property != "" {
		notifier.Properties[key] = property
	} else {
		if required {
			return fmt.Errorf("property %s is required", propertyName)
		}
		if defaultProperty != "" {
			notifier.Properties[key] = defaultProperty
		}
	}
	return nil
}

func setListOfStringsProperty(notifier *humioapi.Notifier, key string, propertyName string, properties []string, defaultProperty []interface{}, required bool) error {
	if len(properties) > 0 {
		var notifierProperties []interface{}
		for _, property := range properties {
			notifierProperties = append(notifierProperties, property)
		}
		notifier.Properties[key] = notifierProperties
		return nil
	}
	if required {
		return fmt.Errorf("property %s is required", propertyName)
	}
	if len(defaultProperty) > 0 {
		notifier.Properties[key] = defaultProperty
	}
	return nil
}

func setMapOfStringsProperty(notifier *humioapi.Notifier, key string, propertyName string, properties map[string]string, defaultProperty map[string]interface{}, required bool) error {
	if len(properties) > 0 {
		notifierProperties := make(map[string]interface{})
		for k, v := range properties {
			notifierProperties[k] = v
		}
		notifier.Properties[key] = notifierProperties
		return nil
	}
	if required {
		return fmt.Errorf("property %s is required", propertyName)
	}
	if len(defaultProperty) > 0 {
		notifier.Properties[key] = defaultProperty
	}
	return nil
}

func baseNotifier(ha *humiov1alpha1.HumioAction) (*humioapi.Notifier, error) {
	at, err := actionType(ha)
	if err != nil {
		return &humioapi.Notifier{}, fmt.Errorf("could not find action type: %s", err)
	}
	notifier := &humioapi.Notifier{
		Name:       ha.Spec.Name,
		Entity:     at,
		Properties: map[string]interface{}{},
	}
	if _, ok := ha.ObjectMeta.Annotations[ActionIdentifierAnnotation]; ok {
		notifier.ID = ha.ObjectMeta.Annotations[ActionIdentifierAnnotation]
	}
	return notifier, nil
}

func actionType(ha *humiov1alpha1.HumioAction) (string, error) {
	var actionTypes []string

	if ha.Spec.WebhookProperties != nil {
		actionTypes = append(actionTypes, humioapi.NotifierTypeWebHook)
	}
	if ha.Spec.VictorOpsProperties != nil {
		actionTypes = append(actionTypes, humioapi.NotifierTypeVictorOps)
	}
	if ha.Spec.PagerDutyProperties != nil {
		actionTypes = append(actionTypes, humioapi.NotifierTypePagerDuty)
	}
	if ha.Spec.HumioRepositoryProperties != nil {
		actionTypes = append(actionTypes, humioapi.NotifierTypeHumioRepo)
	}
	if ha.Spec.SlackPostMessageProperties != nil {
		actionTypes = append(actionTypes, humioapi.NotifierTypeSlackPostMessage)
	}
	if ha.Spec.SlackProperties != nil {
		actionTypes = append(actionTypes, humioapi.NotifierTypeSlack)
	}
	if ha.Spec.OpsGenieProperties != nil {
		actionTypes = append(actionTypes, humioapi.NotifierTypeOpsGenie)
	}
	if ha.Spec.EmailProperties != nil {
		actionTypes = append(actionTypes, humioapi.NotifierTypeEmail)
	}

	if len(actionTypes) > 1 {
		var props []string
		for _, a := range actionTypes {
			props = append(props, propertiesMap[a])
		}
		return "", fmt.Errorf("found properties for more than one action: %s", strings.Join(props, ", "))
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
