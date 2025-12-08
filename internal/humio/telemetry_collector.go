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
	"context"
	"fmt"
	"time"

	"github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
)

// CollectLicenseData implements license data collection for telemetry following existing patterns
func (h *ClientConfig) CollectLicenseData(ctx context.Context, client *api.Client) (*TelemetryLicenseData, error) {
	// Use the new GetLicenseForTelemetry GraphQL operation
	resp, err := humiographql.GetLicenseForTelemetry(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get license data: %w", err)
	}

	installedLicense := resp.GetInstalledLicense()
	if installedLicense == nil {
		return nil, fmt.Errorf("no license installed")
	}

	licenseData := &TelemetryLicenseData{}

	switch v := (*installedLicense).(type) {
	case *humiographql.GetLicenseForTelemetryInstalledLicenseOnPremLicense:
		licenseData.LicenseUID = v.GetUid()
		licenseData.LicenseType = "onprem"
		licenseData.ExpirationDate = v.GetExpiresAt()
		licenseData.IssuedDate = v.GetIssuedAt()
		licenseData.Owner = v.GetOwner()
		licenseData.MaxUsers = v.GetMaxUsers()
		isSaaS := v.GetIsSaaS()
		licenseData.IsSaaS = &isSaaS
		isOem := v.GetIsOem()
		licenseData.IsOem = &isOem

		// TODO: Implement raw license data extraction from JWT

	case *humiographql.GetLicenseForTelemetryInstalledLicenseTrialLicense:
		licenseData.LicenseType = "trial"
		licenseData.ExpirationDate = v.GetExpiresAt()
		licenseData.IssuedDate = v.GetIssuedAt()
		// Trial licenses don't have UID, owner, etc.

		// TODO: Implement raw license data extraction from JWT

	default:
		return nil, fmt.Errorf("unknown license type: %T", v)
	}

	return licenseData, nil
}

// CollectClusterInfo implements cluster information collection for telemetry
func (h *ClientConfig) CollectClusterInfo(ctx context.Context, client *api.Client) (*TelemetryClusterInfo, error) {
	clusterInfo := &TelemetryClusterInfo{}

	// Get cluster nodes information using existing GetCluster GraphQL operation
	clusterResp, err := humiographql.GetCluster(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster information: %w", err)
	}

	cluster := clusterResp.GetCluster()
	if nodes := cluster.GetNodes(); len(nodes) > 0 {
		clusterInfo.NodeCount = len(nodes)
	}

	// Get version information using existing Status API
	statusResp, err := client.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get version information: %w", err)
	}
	clusterInfo.Version = statusResp.Version

	// TODO: Implement actual user/repository collection via GraphQL

	return clusterInfo, nil
}

// CollectTelemetryData collects telemetry data based on the specified data types
func (h *ClientConfig) CollectTelemetryData(ctx context.Context, client *api.Client, dataTypes []string, clusterID string) ([]TelemetryPayload, error) {
	var payloads []TelemetryPayload
	var allErrors []TelemetryError

	timestamp := time.Now()

	for _, dataType := range dataTypes {
		var payload TelemetryPayload
		var collectionErrors []TelemetryError

		switch dataType {
		case "license":
			licenseData, err := h.CollectLicenseData(ctx, client)
			if err != nil {
				collectionErrors = append(collectionErrors, TelemetryError{
					Type:      "collection",
					Message:   fmt.Sprintf("Failed to collect license data: %v", err),
					Timestamp: timestamp,
				})
			} else {
				payload = TelemetryPayload{
					Timestamp:        timestamp,
					ClusterID:        clusterID,
					CollectionType:   "license",
					SourceType:       "json",
					Data:             licenseData,
					CollectionErrors: collectionErrors,
				}
				payloads = append(payloads, payload)
			}

		case "cluster_info":
			clusterInfo, err := h.CollectClusterInfo(ctx, client)
			if err != nil {
				collectionErrors = append(collectionErrors, TelemetryError{
					Type:      "collection",
					Message:   fmt.Sprintf("Failed to collect cluster info: %v", err),
					Timestamp: timestamp,
				})
			} else {
				payload = TelemetryPayload{
					Timestamp:        timestamp,
					ClusterID:        clusterID,
					CollectionType:   "cluster_info",
					SourceType:       "json",
					Data:             clusterInfo,
					CollectionErrors: collectionErrors,
				}
				payloads = append(payloads, payload)
			}

		default:
			collectionErrors = append(collectionErrors, TelemetryError{
				Type:      "configuration",
				Message:   fmt.Sprintf("Unknown data type: %s", dataType),
				Timestamp: timestamp,
			})
		}

		// Collect all errors
		allErrors = append(allErrors, collectionErrors...)
	}

	// If we have errors but no successful payloads, return the errors
	if len(payloads) == 0 && len(allErrors) > 0 {
		return nil, fmt.Errorf("failed to collect any telemetry data: %d errors occurred", len(allErrors))
	}

	return payloads, nil
}
