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
	// HumioTelemetryExportStateEnabled is the state when telemetry export is active
	HumioTelemetryExportStateEnabled = "Enabled"
	// HumioTelemetryExportStateDisabled is the state when telemetry export is disabled
	HumioTelemetryExportStateDisabled = "Disabled"
	// HumioTelemetryExportStateConfigError is the state when there is a configuration error
	HumioTelemetryExportStateConfigError = "ConfigError"
	// HumioTelemetryExportStateExporting is the state when actively exporting data
	HumioTelemetryExportStateExporting = "Exporting"
)

// HumioTelemetryRemoteReportConfig defines configuration for exporting telemetry data
type HumioTelemetryRemoteReportConfig struct {
	// URL is the endpoint URL for the telemetry cluster HEC endpoint
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^https?://"
	URL string `json:"url"`

	// Token contains the authentication token for the telemetry cluster
	// +kubebuilder:validation:Required
	Token VarSource `json:"token"`

	// TLS contains TLS configuration for the connection
	// +optional
	TLS *HumioTelemetryRemoteReportTLSConfig `json:"tls,omitempty"`
}

// HumioTelemetryRemoteReportTLSConfig contains TLS configuration for telemetry client connections
type HumioTelemetryRemoteReportTLSConfig struct {
	// InsecureSkipVerify controls whether the client verifies the server's certificate chain and hostname
	// +optional
	// +kubebuilder:default=false
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// HumioTelemetryExportError represents an error that occurred during telemetry export operations
type HumioTelemetryExportError struct {
	// Type is the category of error (e.g., "export", "configuration")
	Type string `json:"type"`

	// Message is the human-readable error message
	Message string `json:"message"`

	// Timestamp indicates when this error occurred
	Timestamp metav1.Time `json:"timestamp"`

	// DataType indicates which data type this error relates to (if applicable)
	DataType string `json:"dataType,omitempty"`
}

// HumioTelemetryExportSpec defines the desired state of HumioTelemetryExport.
type HumioTelemetryExportSpec struct {
	// RemoteReport defines the configuration for sending telemetry data to a remote cluster
	// +kubebuilder:validation:Required
	RemoteReport HumioTelemetryRemoteReportConfig `json:"remoteReport"`

	// SendCollectionErrors controls whether collection errors are exported to the telemetry cluster
	// When enabled (default), collection errors are sent as telemetry payloads for analysis
	// When disabled, collection errors are only logged locally and recorded as Kubernetes events
	// +optional
	// +kubebuilder:default=true
	SendCollectionErrors *bool `json:"sendCollectionErrors,omitempty"`

	// RegisteredCollections lists the HumioTelemetryCollection resources to accept data from
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	RegisteredCollections []HumioTelemetryCollectionReference `json:"registeredCollections"`
}

// HumioTelemetryCollectionReference references a HumioTelemetryCollection resource
type HumioTelemetryCollectionReference struct {
	// Name of the HumioTelemetryCollection resource
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the HumioTelemetryCollection resource
	// If empty, defaults to the same namespace as this export resource
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// HumioTelemetryExportStatus defines the observed state of HumioTelemetryExport.
type HumioTelemetryExportStatus struct {
	// State represents the current state of the export
	State string `json:"state,omitempty"`

	// LastExportTime indicates when data was last exported successfully
	LastExportTime *metav1.Time `json:"lastExportTime,omitempty"`

	// ExportErrors contains any errors from data export
	ExportErrors []HumioTelemetryExportError `json:"exportErrors,omitempty"`

	// RegisteredCollectionStatus tracks the status of registration with collections
	RegisteredCollectionStatus map[string]HumioTelemetryCollectionRegistrationStatus `json:"registeredCollectionStatus,omitempty"`
}

// HumioTelemetryCollectionRegistrationStatus tracks the registration status with a specific collection
type HumioTelemetryCollectionRegistrationStatus struct {
	// Found indicates whether the referenced collection resource exists
	Found bool `json:"found"`

	// LastDataReceived indicates when data was last received from this collection
	LastDataReceived *metav1.Time `json:"lastDataReceived,omitempty"`

	// TotalExports is the total number of export attempts for data from this collection
	TotalExports int `json:"totalExports,omitempty"`

	// SuccessfulExports is the number of successful exports for data from this collection
	SuccessfulExports int `json:"successfulExports,omitempty"`

	// LastExportError contains the most recent export error for this collection
	LastExportError *HumioTelemetryExportError `json:"lastExportError,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiotelemetryexports,scope=Namespaced
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="Export state"
// +kubebuilder:printcolumn:name="Collections",type="integer",JSONPath=".spec.registeredCollections[*]",description="Number of registered collections"
// +kubebuilder:printcolumn:name="Last Export",type="date",JSONPath=".status.lastExportTime",description="Last export time"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// HumioTelemetryExport is the Schema for the humiotelemetryexports API
type HumioTelemetryExport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioTelemetryExportSpec   `json:"spec"`
	Status HumioTelemetryExportStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioTelemetryExportList contains a list of HumioTelemetryExport
type HumioTelemetryExportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioTelemetryExport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioTelemetryExport{}, &HumioTelemetryExportList{})
}
