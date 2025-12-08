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
	"time"
)

// TelemetryLicenseData represents license information for telemetry
type TelemetryLicenseData struct {
	LicenseUID     string    `json:"license_uid,omitempty"`
	LicenseType    string    `json:"license_type"` // "onprem", "trial"
	ExpirationDate time.Time `json:"expiration_date"`
	IssuedDate     time.Time `json:"issued_date"`
	Owner          string    `json:"owner,omitempty"`
	MaxUsers       *int      `json:"max_users,omitempty"`
	IsSaaS         *bool     `json:"is_saas,omitempty"`
	IsOem          *bool     `json:"is_oem,omitempty"`

	// Raw license data for future implementation
	RawLicenseData map[string]interface{} `json:"raw_license_data,omitempty"`
}

// TelemetryClusterInfo represents cluster information for telemetry
type TelemetryClusterInfo struct {
	Version   string `json:"version"`
	NodeCount int    `json:"node_count"`

	// TODO: Implement user/repository collection via GraphQL
	UserCount       int `json:"user_count,omitempty"`
	RepositoryCount int `json:"repository_count,omitempty"`
}

// TelemetryPayload is the complete payload sent to the telemetry cluster
type TelemetryPayload struct {
	Timestamp        time.Time        `json:"timestamp"`
	ClusterID        string           `json:"cluster_id"`
	CollectionType   string           `json:"collection_type"` // "license", "cluster_info"
	SourceType       string           `json:"source_type"`     // Parser name for LogScale - typically "json" for structured data
	Data             interface{}      `json:"data"`
	CollectionErrors []TelemetryError `json:"collection_errors,omitempty"`
}

// TelemetryError represents an error that occurred during telemetry collection
type TelemetryError struct {
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// TelemetryCollectionResult contains the result of a telemetry collection operation
type TelemetryCollectionResult struct {
	Data   interface{}      `json:"data,omitempty"`
	Errors []TelemetryError `json:"errors,omitempty"`
}
