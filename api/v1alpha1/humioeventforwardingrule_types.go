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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// EventForwardingRuleConditionTypeReady indicates whether the event forwarding rule is ready in LogScale.
	EventForwardingRuleConditionTypeReady = "Ready"
	// EventForwardingRuleConditionTypeSynced indicates whether the configuration matches the desired state.
	EventForwardingRuleConditionTypeSynced = "Synced"

	// EventForwardingRuleReasonReady indicates the rule is ready (steady state).
	EventForwardingRuleReasonReady = "EventForwardingRuleReady"
	// EventForwardingRuleReasonCreated indicates the rule was just created (lifecycle event).
	EventForwardingRuleReasonCreated = "EventForwardingRuleCreated"
	// EventForwardingRuleReasonUpdated indicates the rule was just updated (lifecycle event).
	EventForwardingRuleReasonUpdated = "EventForwardingRuleUpdated"
	// EventForwardingRuleReasonRuleNotFound indicates the forwarding rule was not found in LogScale.
	EventForwardingRuleReasonRuleNotFound = "EventForwardingRuleNotFound"
	// EventForwardingRuleReasonConfigError indicates a configuration error occurred.
	EventForwardingRuleReasonConfigError = "EventForwardingRuleConfigurationError"
	// EventForwardingRuleReasonReconcileError indicates an error during reconciliation.
	EventForwardingRuleReasonReconcileError = "EventForwardingRuleReconciliationError"

	// EventForwardingRuleReasonConfigurationSynced indicates the configuration is synced with LogScale.
	EventForwardingRuleReasonConfigurationSynced = "EventForwardingRuleConfigurationSynced"
	// EventForwardingRuleReasonConfigurationDrifted indicates the configuration has drifted from the desired state.
	EventForwardingRuleReasonConfigurationDrifted = "EventForwardingRuleConfigurationDrifted"
	// EventForwardingRuleReasonInvalidForwarder indicates the specified event forwarder is invalid.
	EventForwardingRuleReasonInvalidForwarder = "EventForwardingRuleInvalidEventForwarder"
	// EventForwardingRuleReasonForwarderNotReady indicates the referenced event forwarder is not ready.
	EventForwardingRuleReasonForwarderNotReady = "EventForwardingRuleEventForwarderNotReady"
)

// EventForwarderReference references a HumioEventForwarder resource.
type EventForwarderReference struct {
	// Name of the HumioEventForwarder resource.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the HumioEventForwarder resource.
	// If empty, defaults to the same namespace as the HumioEventForwardingRule.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// HumioEventForwardingRuleSpec defines the desired state of HumioEventForwardingRule.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
// +kubebuilder:validation:XValidation:rule="(has(self.eventForwarderID) && self.eventForwarderID != \"\") != (has(self.eventForwarderRef) && self.eventForwarderRef != null)",message="Must specify exactly one of eventForwarderID or eventForwarderRef"
type HumioEventForwardingRuleSpec struct {
	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator where the Humio
	// resources should be created.
	// This conflicts with ExternalClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ManagedClusterName string `json:"managedClusterName,omitempty"`

	// ExternalClusterName refers to an object of type HumioExternalCluster where the Humio resources should be created.
	// This conflicts with ManagedClusterName.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	ExternalClusterName string `json:"externalClusterName,omitempty"`

	// Name is the Kubernetes-friendly identifier for this rule within the operator.
	// NOTE: This is NOT the LogScale rule ID. LogScale auto-generates UUIDs for event
	// forwarding rules, which are stored in the core.humio.com/event-forwarding-rule-id annotation.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// RepositoryName defines the repository where this event forwarding rule should be managed.
	// NOTE: Event forwarding rules can only be applied to repositories, not views.
	// This field must reference a HumioRepository resource or an existing repository in LogScale.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	RepositoryName string `json:"repositoryName"`

	// QueryString is the LogScale query language (LQL) query that filters and transforms events to forward.
	// This query determines which events are forwarded and how they are transformed before forwarding.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1048576
	// +kubebuilder:validation:Required
	QueryString string `json:"queryString"`

	// EventForwarderID is the ID of a pre-configured event forwarder to use.
	// Use this for externally-managed forwarders (configured outside the operator).
	// Mutually exclusive with EventForwarderRef.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Optional
	EventForwarderID string `json:"eventForwarderID,omitempty"`

	// EventForwarderRef references a HumioEventForwarder resource managed by the operator.
	// Use this to reference a forwarder created via the HumioEventForwarder CRD.
	// Mutually exclusive with EventForwarderID.
	// +kubebuilder:validation:Optional
	EventForwarderRef *EventForwarderReference `json:"eventForwarderRef,omitempty"`

	// LanguageVersion specifies the query language version to use.
	// Valid values: legacy, xdr1, xdrdetects1, filteralert, federated1
	// If not specified, the default version for the LogScale cluster will be used.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=legacy;xdr1;xdrdetects1;filteralert;federated1
	LanguageVersion *string `json:"languageVersion,omitempty"`
}

// HumioEventForwardingRuleStatus defines the observed state of HumioEventForwardingRule.
type HumioEventForwardingRuleStatus struct {
	// EventForwarderName is the human-readable name of the configured event forwarder.
	// This is fetched from LogScale and populated by the controller for better visibility.
	// +optional
	EventForwarderName string `json:"eventForwarderName,omitempty"`

	// ResolvedEventForwarderID is the actual forwarder ID resolved from either eventForwarderID or eventForwarderRef.
	// +optional
	ResolvedEventForwarderID string `json:"resolvedEventForwarderID,omitempty"`

	// Conditions represent the latest available observations of the event forwarding rule's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioeventforwardingrules,scope=Namespaced,shortName=hefr,categories={humio,all}
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status",description="Whether the event forwarding rule is ready"
// +kubebuilder:printcolumn:name="Repository",type="string",JSONPath=".spec.repositoryName",description="The repository name"
// +kubebuilder:printcolumn:name="Forwarder",type="string",JSONPath=".status.eventForwarderName",description="The event forwarder name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description="Creation timestamp"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Event Forwarding Rule"

// HumioEventForwardingRule is the Schema for the humioeventforwardingrules API.
// It configures a rule that filters and forwards events from a LogScale repository to an external system.
// NOTE: Event forwarding rules can only be applied to repositories, not views. This is a LogScale API limitation.
type HumioEventForwardingRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioEventForwardingRuleSpec   `json:"spec"`
	Status HumioEventForwardingRuleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioEventForwardingRuleList contains a list of HumioEventForwardingRule.
type HumioEventForwardingRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioEventForwardingRule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioEventForwardingRule{}, &HumioEventForwardingRuleList{})
}
