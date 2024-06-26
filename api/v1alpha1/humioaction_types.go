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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// HumioActionStateUnknown is the Unknown state of the action
	HumioActionStateUnknown = "Unknown"
	// HumioActionStateExists is the Exists state of the action
	HumioActionStateExists = "Exists"
	// HumioActionStateNotFound is the NotFound state of the action
	HumioActionStateNotFound = "NotFound"
	// HumioActionStateConfigError is the state of the action when user-provided specification results in configuration error, such as non-existent humio cluster
	HumioActionStateConfigError = "ConfigError"
)

// HumioActionWebhookProperties defines the desired state of HumioActionWebhookProperties
type HumioActionWebhookProperties struct {
	BodyTemplate string `json:"bodyTemplate,omitempty"`
	// Headers specifies what HTTP headers to use.
	// If both Headers and SecretHeaders are specified, they will be merged together.
	Headers map[string]string `json:"headers,omitempty"`
	// SecretHeaders specifies what HTTP headers to use and where to fetch the values from.
	// If both Headers and SecretHeaders are specified, they will be merged together.
	SecretHeaders []HeadersSource `json:"secretHeaders,omitempty"`
	Method        string          `json:"method,omitempty"`
	// Url specifies what URL to use
	// If both Url and UrlSource are specified, Url will be used.
	Url string `json:"url,omitempty"`
	// UrlSource specifies where to fetch the URL from
	// If both Url and UrlSource are specified, Url will be used.
	UrlSource VarSource `json:"urlSource,omitempty"`
	IgnoreSSL bool      `json:"ignoreSSL,omitempty"`
	UseProxy  bool      `json:"useProxy,omitempty"`
}

// HeadersSource defines a header and corresponding source for the value of it.
type HeadersSource struct {
	// Name is the name of the header.
	Name string `json:"name,omitempty"`
	// ValueFrom defines where to fetch the value of the header from.
	ValueFrom VarSource `json:"valueFrom,omitempty"`
}

// HumioActionEmailProperties defines the desired state of HumioActionEmailProperties
type HumioActionEmailProperties struct {
	BodyTemplate    string   `json:"bodyTemplate,omitempty"`
	SubjectTemplate string   `json:"subjectTemplate,omitempty"`
	Recipients      []string `json:"recipients,omitempty"`
	UseProxy        bool     `json:"useProxy,omitempty"`
}

// HumioActionRepositoryProperties defines the desired state of HumioActionRepositoryProperties
type HumioActionRepositoryProperties struct {
	// IngestToken specifies what ingest token to use.
	// If both IngestToken and IngestTokenSource are specified, IngestToken will be used.
	IngestToken string `json:"ingestToken,omitempty"`
	// IngestToken specifies where to fetch the ingest token from.
	// If both IngestToken and IngestTokenSource are specified, IngestToken will be used.
	IngestTokenSource VarSource `json:"ingestTokenSource,omitempty"`
}

// HumioActionOpsGenieProperties defines the desired state of HumioActionOpsGenieProperties
type HumioActionOpsGenieProperties struct {
	ApiUrl string `json:"apiUrl,omitempty"`
	// GenieKey specifies what API key to use.
	// If both GenieKey and GenieKeySource are specified, GenieKey will be used.
	GenieKey string `json:"genieKey,omitempty"`
	// GenieKeySource specifies where to fetch the API key from.
	// If both GenieKey and GenieKeySource are specified, GenieKey will be used.
	GenieKeySource VarSource `json:"genieKeySource,omitempty"`
	UseProxy       bool      `json:"useProxy,omitempty"`
}

// HumioActionPagerDutyProperties defines the desired state of HumioActionPagerDutyProperties
type HumioActionPagerDutyProperties struct {
	// RoutingKey specifies what API key to use.
	// If both RoutingKey and RoutingKeySource are specified, RoutingKey will be used.
	RoutingKey string `json:"routingKey,omitempty"`
	// RoutingKeySource specifies where to fetch the routing key from.
	// If both RoutingKey and RoutingKeySource are specified, RoutingKey will be used.
	RoutingKeySource VarSource `json:"routingKeySource,omitempty"`
	Severity         string    `json:"severity,omitempty"`
	UseProxy         bool      `json:"useProxy,omitempty"`
}

// HumioActionSlackProperties defines the desired state of HumioActionSlackProperties
type HumioActionSlackProperties struct {
	Fields map[string]string `json:"fields,omitempty"`
	// Url specifies what URL to use.
	// If both Url and UrlSource are specified, Url will be used.
	Url string `json:"url,omitempty"`
	// UrlSource specifies where to fetch the URL from.
	// If both Url and UrlSource are specified, Url will be used.
	UrlSource VarSource `json:"urlSource,omitempty"`
	UseProxy  bool      `json:"useProxy,omitempty"`
}

// HumioActionSlackPostMessageProperties defines the desired state of HumioActionSlackPostMessageProperties
type HumioActionSlackPostMessageProperties struct {
	// ApiToken specifies what API key to use.
	// If both ApiToken and ApiTokenSource are specified, ApiToken will be used.
	ApiToken string `json:"apiToken,omitempty"`
	// ApiTokenSource specifies where to fetch the API key from.
	// If both ApiToken and ApiTokenSource are specified, ApiToken will be used.
	ApiTokenSource VarSource         `json:"apiTokenSource,omitempty"`
	Channels       []string          `json:"channels,omitempty"`
	Fields         map[string]string `json:"fields,omitempty"`
	UseProxy       bool              `json:"useProxy,omitempty"`
}

// HumioActionVictorOpsProperties defines the desired state of HumioActionVictorOpsProperties
type HumioActionVictorOpsProperties struct {
	MessageType string `json:"messageType,omitempty"`
	// NotifyUrl specifies what URL to use.
	// If both NotifyUrl and NotifyUrlSource are specified, NotifyUrl will be used.
	NotifyUrl string `json:"notifyUrl,omitempty"`
	// NotifyUrlSource specifies where to fetch the URL from.
	// If both NotifyUrl and NotifyUrlSource are specified, NotifyUrl will be used.
	NotifyUrlSource VarSource `json:"notifyUrlSource"`
	UseProxy        bool      `json:"useProxy,omitempty"`
}

// VarSource is used to specify where a value should be pulled from
type VarSource struct {
	// SecretKeyRef allows specifying which secret and what key in that secret holds the value we want to use
	SecretKeyRef *corev1.SecretKeySelector `json:"secretKeyRef,omitempty"`
}

// HumioActionSpec defines the desired state of HumioAction
type HumioActionSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	ManagedClusterName string `json:"managedClusterName,omitempty"`
	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	ExternalClusterName string `json:"externalClusterName,omitempty"`
	// Name is the name of the Action
	Name string `json:"name"`
	// ViewName is the name of the Humio View under which the Action will be managed. This can also be a Repository
	ViewName string `json:"viewName"`
	// EmailProperties indicates this is an Email Action, and contains the corresponding properties
	EmailProperties *HumioActionEmailProperties `json:"emailProperties,omitempty"`
	// HumioRepositoryProperties indicates this is a Humio Repository Action, and contains the corresponding properties
	HumioRepositoryProperties *HumioActionRepositoryProperties `json:"humioRepositoryProperties,omitempty"`
	// OpsGenieProperties indicates this is a Ops Genie Action, and contains the corresponding properties
	OpsGenieProperties *HumioActionOpsGenieProperties `json:"opsGenieProperties,omitempty"`
	// PagerDutyProperties indicates this is a PagerDuty Action, and contains the corresponding properties
	PagerDutyProperties *HumioActionPagerDutyProperties `json:"pagerDutyProperties,omitempty"`
	// SlackProperties indicates this is a Slack Action, and contains the corresponding properties
	SlackProperties *HumioActionSlackProperties `json:"slackProperties,omitempty"`
	// SlackPostMessageProperties indicates this is a Slack Post Message Action, and contains the corresponding properties
	SlackPostMessageProperties *HumioActionSlackPostMessageProperties `json:"slackPostMessageProperties,omitempty"`
	// VictorOpsProperties indicates this is a VictorOps Action, and contains the corresponding properties
	VictorOpsProperties *HumioActionVictorOpsProperties `json:"victorOpsProperties,omitempty"`
	// WebhookProperties indicates this is a Webhook Action, and contains the corresponding properties
	WebhookProperties *HumioActionWebhookProperties `json:"webhookProperties,omitempty"`
}

// HumioActionStatus defines the observed state of HumioAction
type HumioActionStatus struct {
	// State reflects the current state of the HumioAction
	State string `json:"state,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// HumioAction is the Schema for the humioactions API
type HumioAction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioActionSpec   `json:"spec,omitempty"`
	Status HumioActionStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// HumioActionList contains a list of HumioAction
type HumioActionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioAction `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioAction{}, &HumioActionList{})
}
