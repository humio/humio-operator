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

package controller

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/humio"
)

// TelemetryExporter interface for exporting telemetry payloads
type TelemetryExporter interface {
	ExportPayloads(ctx context.Context, payloads []humio.TelemetryPayload) []ExportError
}

// ExportError represents an error that occurred during telemetry export
type ExportError struct {
	Type      string
	Message   string
	Timestamp time.Time
}

// ExporterInfo contains information about a registered exporter
type ExporterInfo struct {
	Name      string
	Namespace string
	Config    *humiov1alpha1.HumioTelemetryExport
}

// ExportResult contains the result of pushing data to an exporter
type ExportResult struct {
	ExporterName      string
	ExporterNamespace string
	Success           bool
	Error             error
	ExportedAt        time.Time
	PayloadCount      int
}

// validDataTypes contains the list of valid telemetry data types
var validDataTypes = map[string]bool{
	"license":            true,
	"cluster_info":       true,
	"user_info":          true,
	"repository_info":    true,
	"ingestion_metrics":  true,
	"repository_usage":   true,
	"user_activity":      true,
	"detailed_analytics": true,
}

// isValidDataType checks if the given data type is valid for telemetry collection
func isValidDataType(dataType string) bool {
	return validDataTypes[dataType]
}

// parseDuration parses a duration string with support for s, m, h, d units
func parseDuration(durationStr string) (time.Duration, error) {
	durationPattern := regexp.MustCompile(`^(\d+)([smhd])$`)
	matches := durationPattern.FindStringSubmatch(strings.TrimSpace(durationStr))

	if len(matches) != 3 {
		return 0, fmt.Errorf("invalid duration format: %s (expected format: <number><unit> where unit is s, m, h, or d)", durationStr)
	}

	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid duration value: %s", matches[1])
	}

	unit := matches[2]
	var duration time.Duration

	switch unit {
	case "s":
		duration = time.Duration(value) * time.Second
	case "m":
		duration = time.Duration(value) * time.Minute
	case "h":
		duration = time.Duration(value) * time.Hour
	case "d":
		duration = time.Duration(value) * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid duration unit: %s (must be s, m, h, or d)", unit)
	}

	return duration, nil
}

// validateHumioTelemetryConfiguration validates the telemetry configuration in a HumioCluster
func (r *HumioClusterReconciler) validateHumioTelemetryConfiguration(hc *humiov1alpha1.HumioCluster) error {
	if hc.Spec.TelemetryConfig == nil {
		return fmt.Errorf("telemetry configuration is nil")
	}

	if hc.Spec.TelemetryConfig.RemoteReport == nil {
		return fmt.Errorf("remote report configuration is required")
	}

	if hc.Spec.TelemetryConfig.RemoteReport.URL == "" {
		return fmt.Errorf("remote report URL is required")
	}

	if len(hc.Spec.TelemetryConfig.Collections) == 0 {
		return fmt.Errorf("at least one telemetry collection configuration is required")
	}

	return nil
}
