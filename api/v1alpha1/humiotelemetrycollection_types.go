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
	// HumioTelemetryCollectionStateEnabled is the state when telemetry collection is active
	HumioTelemetryCollectionStateEnabled = "Enabled"
	// HumioTelemetryCollectionStateDisabled is the state when telemetry collection is disabled
	HumioTelemetryCollectionStateDisabled = "Disabled"
	// HumioTelemetryCollectionStateConfigError is the state when there is a configuration error
	HumioTelemetryCollectionStateConfigError = "ConfigError"
	// HumioTelemetryCollectionStateCollecting is the state when actively collecting data
	HumioTelemetryCollectionStateCollecting = "Collecting"
)

// CollectionConfig defines what data to collect and collection frequency
type CollectionConfig struct {
	// Interval defines how frequently to collect this data (e.g., "15m", "1h", "1d")
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern="^[0-9]+[smhd]$"
	Interval string `json:"interval"`

	// Include defines which data types to collect in this collection
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Items:Enum=license;cluster_info;user_info;repository_info;ingestion_metrics;repository_usage;user_activity;detailed_analytics
	Include []string `json:"include"`
}

// HumioTelemetryCollectionError represents an error that occurred during telemetry collection operations
type HumioTelemetryCollectionError struct {
	// Type is the category of error (e.g., "collection", "configuration")
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
	LastError *HumioTelemetryCollectionError `json:"lastError,omitempty"`
}

// HumioTelemetryCollectionSpec defines the desired state of HumioTelemetryCollection.
// +kubebuilder:validation:XValidation:rule="has(self.managedClusterName) && self.managedClusterName != \"\"",message="Must specify managedClusterName"
type HumioTelemetryCollectionSpec struct {
	// ClusterIdentifier is a unique identifier for this cluster used in telemetry data
	// If not specified, defaults to ManagedClusterName
	// +optional
	// +kubebuilder:validation:MinLength=1
	ClusterIdentifier string `json:"clusterIdentifier,omitempty"`

	// ManagedClusterName refers to an object of type HumioCluster that is managed by the operator
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ManagedClusterName string `json:"managedClusterName"`

	// Collections defines what data to collect and how frequently
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Collections []CollectionConfig `json:"collections"`
}

// HumioTelemetryCollectionStatus defines the observed state of HumioTelemetryCollection.
type HumioTelemetryCollectionStatus struct {
	// State represents the current state of the collection
	State string `json:"state,omitempty"`

	// LastCollectionTime indicates when data was last collected
	LastCollectionTime *metav1.Time `json:"lastCollectionTime,omitempty"`

	// NextScheduledCollection contains the next scheduled collection times by type
	NextScheduledCollection map[string]*metav1.Time `json:"nextScheduledCollection,omitempty"`

	// CollectionStatus contains the status of individual collection types
	CollectionStatus map[string]CollectionTypeStatus `json:"collectionStatus,omitempty"`

	// ExportPushResults tracks the results of pushing data to registered exporters
	ExportPushResults []ExportPushResult `json:"exportPushResults,omitempty"`
}

// ExportPushResult tracks the result of pushing data to a specific exporter
type ExportPushResult struct {
	// ExporterName is the name of the HumioTelemetryExport resource
	ExporterName string `json:"exporterName"`

	// ExporterNamespace is the namespace of the HumioTelemetryExport resource
	ExporterNamespace string `json:"exporterNamespace"`

	// LastPushTime indicates when data was last pushed to this exporter
	LastPushTime *metav1.Time `json:"lastPushTime,omitempty"`

	// LastPushSuccess indicates whether the last push was successful
	LastPushSuccess bool `json:"lastPushSuccess"`

	// LastPushError contains the most recent push error
	LastPushError *HumioTelemetryCollectionError `json:"lastPushError,omitempty"`

	// TotalPushes is the total number of push attempts to this exporter
	TotalPushes int `json:"totalPushes,omitempty"`

	// SuccessfulPushes is the number of successful pushes to this exporter
	SuccessfulPushes int `json:"successfulPushes,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=humiotelemetrycollections,scope=Namespaced
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.managedClusterName",description="Managed cluster name"
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="Collection state"
// +kubebuilder:printcolumn:name="Last Collection",type="date",JSONPath=".status.lastCollectionTime",description="Last collection time"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// HumioTelemetryCollection is the Schema for the humiotelemetrycollections API
type HumioTelemetryCollection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HumioTelemetryCollectionSpec   `json:"spec"`
	Status HumioTelemetryCollectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HumioTelemetryCollectionList contains a list of HumioTelemetryCollection
type HumioTelemetryCollectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HumioTelemetryCollection `json:"items"`
}

// HumioTelemetryConfig contains configuration for telemetry collection
type HumioTelemetryConfig struct {
	// RemoteReport defines the configuration for sending telemetry data to a remote cluster
	RemoteReport *HumioTelemetryRemoteReportConfig `json:"remoteReport,omitempty"`

	// ClusterIdentifier is a unique identifier for this cluster used in telemetry data
	ClusterIdentifier string `json:"clusterIdentifier,omitempty"`

	// Collections defines what data to collect and how frequently
	Collections []HumioTelemetryCollectionConfig `json:"collections,omitempty"`
}

// HumioTelemetryCollectionConfig defines what data to collect and collection frequency
type HumioTelemetryCollectionConfig struct {
	// Interval defines how frequently to collect this data (e.g., "15m", "1h", "1d")
	Interval string `json:"interval,omitempty"`

	// Include defines which data types to collect in this collection
	Include []string `json:"include,omitempty"`
}

func init() {
	SchemeBuilder.Register(&HumioTelemetryCollection{}, &HumioTelemetryCollectionList{})
}
