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
	// HumioTelemetryStateEnabled is the state when telemetry collection is active
	HumioTelemetryStateEnabled = "Enabled"
	// HumioTelemetryStateDisabled is the state when telemetry collection is disabled
	HumioTelemetryStateDisabled = "Disabled"
	// HumioTelemetryStateConfigError is the state when there is a configuration error
	HumioTelemetryStateConfigError = "ConfigError"
	// HumioTelemetryStateCollecting is the state when actively collecting data
	HumioTelemetryStateCollecting = "Collecting"
	// HumioTelemetryStateExporting is the state when actively exporting data
	HumioTelemetryStateExporting = "Exporting"
)

// HumioTelemetrySpec defines the desired state of HumioTelemetry.
// +kubebuilder:validation:XValidation:rule="has(self.managedClusterName) && self.managedClusterName != \"\"",message="Must specify managedClusterName"
type HumioTelemetrySpec struct {
	// ClusterIdentifier is a unique identifier for this cluster used in telemetry data
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ClusterIdentifier string `json:"clusterIdentifier"`

	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ManagedClusterName string `json:"managedClusterName"`

	// RemoteReport defines the configuration for sending telemetry data to a remote cluster
	// +kubebuilder:validation:Required
	RemoteReport RemoteReportConfig `json:"remoteReport"`

	// Collections defines what data to collect and how frequently
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Collections []CollectionConfig `json:"collections"`
}

// RemoteReportConfig defines configuration for exporting telemetry data
type RemoteReportConfig struct {
	// URL is the endpoint URL for the telemetry cluster HEC endpoint
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^https?://"
	URL string `json:"url"`

	// Token contains the authentication token for the telemetry cluster
	// +kubebuilder:validation:Required
	Token VarSource `json:"token"`

	// TLS contains TLS configuration for the connection
	// +optional
	TLS *TLSConfig `json:"tls,omitempty"`
}

// CollectionConfig defines what data to collect and collection frequency
type CollectionConfig struct {
	// Interval defines how frequently to collect this data (e.g., "15m", "1h", "1d")
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[0-9]+[smhd]$"
	Interval string `json:"interval"`

	// Include defines which data types to collect in this collection
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Items:Enum=license;cluster_info;user_info;repository_info
	Include []string `json:"include"`
}

// HumioTelemetryStatus defines the observed state of HumioTelemetry.
type HumioTelemetryStatus struct {
	// State represents the current state of the HumioTelemetry resource
	State string `json:"state,omitempty"`

	// LastCollectionTime indicates when data was last collected
	LastCollectionTime *metav1.Time `json:"lastCollectionTime,omitempty"`

	// LastExportTime indicates when data was last exported successfully
	LastExportTime *metav1.Time `json:"lastExportTime,omitempty"`

	// CollectionErrors contains any errors from data collection
	CollectionErrors []TelemetryError `json:"collectionErrors,omitempty"`

	// ExportErrors contains any errors from data export
	ExportErrors []TelemetryError `json:"exportErrors,omitempty"`

	// NextScheduledCollection contains the next scheduled collection times by type
	NextScheduledCollection map[string]*metav1.Time `json:"nextScheduledCollection,omitempty"`

	// CollectionStatus contains the status of individual collection types
	CollectionStatus map[string]CollectionTypeStatus `json:"collectionStatus,omitempty"`
}

// TelemetryError represents an error that occurred during telemetry operations
type TelemetryError struct {
	// Type is the category of error (e.g., "collection", "export", "configuration")
	Type string `json:"type"`

	// Message is the human-readable error message
	Message string `json:"message"`

	// Timestamp indicates when this error occurred
	Timestamp metav1.Time `json:"timestamp"`

	// DataType indicates which data type this error relates to (if applicable)
	DataType string `json:"dataType,omitempty"`
}

// CollectionTypeStatus represents the status of a specific collection type
type CollectionTypeStatus struct {
	// LastCollection indicates when this data type was last collected
	LastCollection *metav1.Time `json:"lastCollection,omitempty"`

	// LastExport indicates when this data type was last exported
	LastExport *metav1.Time `json:"lastExport,omitempty"`

	// CollectionCount is the total number of successful collections for this type
	CollectionCount int `json:"collectionCount,omitempty"`

	// ExportCount is the total number of successful exports for this type
	ExportCount int `json:"exportCount,omitempty"`

	// LastError contains the most recent error for this collection type
	LastError *TelemetryError `json:"lastError,omitempty"`
}

// TLSConfig contains TLS configuration for telemetry client connections
type TLSConfig struct {
	// InsecureSkipVerify controls whether the client verifies the server's certificate chain and hostname
	// +optional
	// +kubebuilder:default=false
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiotelemetries,scope=Namespaced
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.managedClusterName",description="Managed cluster name"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="Telemetry state"
// +kubebuilder:printcolumn:name="Last Collection",type="date",JSONPath=".status.lastCollectionTime",description="Last collection time"
// +kubebuilder:printcolumn:name="Last Export",type="date",JSONPath=".status.lastExportTime",description="Last export time"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// HumioTelemetry is the Schema for the humiotelemetries API
type HumioTelemetry struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioTelemetrySpec   `json:"spec"`
	Status HumioTelemetryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioTelemetryList contains a list of HumioTelemetry
type HumioTelemetryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioTelemetry `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HumioTelemetry{}, &HumioTelemetryList{})
}
