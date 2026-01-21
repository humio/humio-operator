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
	// EventForwarderConditionTypeReady indicates whether the event forwarder is ready in LogScale.
	EventForwarderConditionTypeReady = "Ready"
	// EventForwarderConditionTypeSynced indicates whether the configuration matches the desired state.
	EventForwarderConditionTypeSynced = "Synced"

	// EventForwarderReasonReady indicates the forwarder is ready (steady state).
	EventForwarderReasonReady = "EventForwarderReady"
	// EventForwarderReasonCreated indicates the forwarder was just created (lifecycle event).
	EventForwarderReasonCreated = "EventForwarderCreated"
	// EventForwarderReasonUpdated indicates the forwarder was just updated (lifecycle event).
	EventForwarderReasonUpdated = "EventForwarderUpdated"
	// EventForwarderReasonAdoptionSuccessful indicates the forwarder was successfully adopted (lifecycle event).
	EventForwarderReasonAdoptionSuccessful = "AdoptionSuccessful"
	// EventForwarderReasonAdoptionRejected indicates adoption was rejected due to config mismatch.
	EventForwarderReasonAdoptionRejected = "AdoptionRejected"
	// EventForwarderReasonNotFound indicates the forwarder was not found in LogScale.
	EventForwarderReasonNotFound = "EventForwarderNotFound"
	// EventForwarderReasonConfigError indicates a configuration error occurred.
	EventForwarderReasonConfigError = "ConfigurationError"
	// EventForwarderReasonReconcileError indicates an error during reconciliation.
	EventForwarderReasonReconcileError = "ReconciliationError"

	// EventForwarderReasonConfigurationSynced indicates the configuration is synced with LogScale.
	EventForwarderReasonConfigurationSynced = "ConfigurationSynced"
	// EventForwarderReasonConfigurationDrifted indicates the configuration has drifted from the desired state.
	EventForwarderReasonConfigurationDrifted = "ConfigurationDrifted"
)

// HumioEventForwarderSpec defines the desired state of HumioEventForwarder.
// +kubebuilder:validation:XValidation:rule="(has(self.managedClusterName) && self.managedClusterName != \"\") != (has(self.externalClusterName) && self.externalClusterName != \"\")",message="Must specify exactly one of managedClusterName or externalClusterName"
// +kubebuilder:validation:XValidation:rule="self.forwarderType != 'kafka' || has(self.kafkaConfig)",message="kafkaConfig is required when forwarderType is kafka"
type HumioEventForwarderSpec struct {
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

	// Name of the event forwarder in LogScale.
	// This is the human-readable name that will be displayed in the UI.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Description of the event forwarder.
	// +kubebuilder:validation:Required
	Description string `json:"description"`

	// ForwarderType specifies the type of forwarder.
	// Currently only "kafka" is supported.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=kafka
	// +kubebuilder:default=kafka
	ForwarderType string `json:"forwarderType"`

	// Enabled controls whether the forwarder is active.
	// When disabled, events will not be forwarded to this destination.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// KafkaConfig contains Kafka-specific configuration.
	// Required when forwarderType is "kafka".
	// +kubebuilder:validation:Optional
	KafkaConfig *KafkaEventForwarderConfig `json:"kafkaConfig,omitempty"`
}

// SecretKeyReference references a key within a Kubernetes Secret.
type SecretKeyReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key within the Secret containing the data.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// Namespace of the Secret. If empty, defaults to the same namespace as the HumioEventForwarder.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// KafkaEventForwarderConfig defines the Kafka-specific configuration for event forwarding.
// +kubebuilder:validation:XValidation:rule="has(self.properties) || has(self.propertiesSecretRef)",message="At least one of properties or propertiesSecretRef must be specified"
type KafkaEventForwarderConfig struct {
	// Topic is the Kafka topic to forward events to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Topic string `json:"topic"`

	// Properties contains non-sensitive Kafka configuration in x.y.z=abc format.
	// Newline-separated list of properties.
	// For sensitive properties (passwords, keys), use PropertiesSecretRef instead.
	// See: https://library.humio.com/falcon-logscale-self-hosted/ingesting-data-event-forwarders.html#ingesting-data-event-forwarders-kafka
	// Example:
	//   bootstrap.servers=kafka.example.com:9092
	//   security.protocol=SASL_SSL
	//   sasl.mechanism=PLAIN
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=1048576
	Properties string `json:"properties,omitempty"`

	// PropertiesSecretRef references a Kubernetes Secret containing sensitive Kafka properties.
	// The secret data should be in the same x.y.z=abc format as Properties.
	// Properties from both fields are merged, with PropertiesSecretRef taking precedence.
	// This is the recommended way to provide sensitive configuration like passwords and SSL keys.
	// +kubebuilder:validation:Optional
	PropertiesSecretRef *SecretKeyReference `json:"propertiesSecretRef,omitempty"`
}

// HumioEventForwarderStatus defines the observed state of HumioEventForwarder.
type HumioEventForwarderStatus struct {
	// EventForwarderID is the LogScale-generated ID for this forwarder.
	// This ID is used internally by LogScale to reference the forwarder.
	// +optional
	EventForwarderID string `json:"eventForwarderID,omitempty"`

	// ManagedByOperator indicates whether the operator successfully owns this LogScale event forwarder.
	// Set to true when the operator creates a new forwarder or successfully adopts an existing one.
	// Set to false when adoption is rejected.
	// When false, the finalizer will not delete the forwarder from LogScale.
	// +optional
	ManagedByOperator *bool `json:"managedByOperator,omitempty"`

	// Conditions represent the latest available observations of the forwarder's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humioeventforwarders,scope=Namespaced,shortName=heff,categories={humio,all}
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status",description="Ready status"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.forwarderType",description="Forwarder type"
// +kubebuilder:printcolumn:name="Topic",type="string",JSONPath=".spec.kafkaConfig.topic",description="Kafka topic (if kafka type)"
// +kubebuilder:printcolumn:name="Enabled",type="boolean",JSONPath=".spec.enabled",description="Whether forwarder is enabled"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +operator-sdk:gen-csv:customresourcedefinitions.displayName="Humio Event Forwarder"

// HumioEventForwarder is the Schema for the humioeventforwarders API.
// It manages event forwarder destinations in LogScale where events can be forwarded.
type HumioEventForwarder struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   HumioEventForwarderSpec   `json:"spec"`
	Status HumioEventForwarderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioEventForwarderList contains a list of HumioEventForwarder.
type HumioEventForwarderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioEventForwarder `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioEventForwarder{}, &HumioEventForwarderList{})
}
