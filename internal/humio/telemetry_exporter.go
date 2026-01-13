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
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
)

// NonRetryableError represents an error that should not be retried
type NonRetryableError struct {
	Err error
}

func (e *NonRetryableError) Error() string {
	return e.Err.Error()
}

func (e *NonRetryableError) Unwrap() error {
	return e.Err
}

// HECClient handles HTTP Event Collector (HEC) requests to send telemetry data
type HECClient struct {
	URL        string
	Token      string
	HTTPClient *http.Client
	Logger     logr.Logger
}

// NewHECClient creates a new HEC client for telemetry export
func NewHECClient(url, token string, insecureSkipVerify bool, logger logr.Logger) *HECClient {
	// Log security warning when TLS verification is disabled
	if insecureSkipVerify {
		logger.Info("WARNING: TLS certificate verification is disabled. This should only be used in development/testing environments.")
	}

	// Configure HTTP client with reasonable timeouts and TLS settings
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecureSkipVerify, // #nosec G402 -- This is configurable for testing environments
			},
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	return &HECClient{
		URL:        url,
		Token:      token,
		HTTPClient: httpClient,
		Logger:     logger,
	}
}

// HECEvent represents the structure expected by HEC endpoints
type HECEvent struct {
	Time       *int64 `json:"time,omitempty"`       // Unix timestamp
	Host       string `json:"host,omitempty"`       // Hostname
	Source     string `json:"source,omitempty"`     // Source identifier
	SourceType string `json:"sourcetype,omitempty"` // Source type
	Index      string `json:"index,omitempty"`      // Target index
	Event      any    `json:"event"`                // The actual event data
}

// ExportTelemetryPayload sends a telemetry payload to the HEC endpoint
func (h *HECClient) ExportTelemetryPayload(ctx context.Context, payload TelemetryPayload) error {
	// Sanitize the payload to convert any microsecond timestamps to milliseconds
	// This prevents LogScale parsing errors when the telemetry data is processed

	// Marshal payload to JSON bytes to ensure proper type conversion
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload for sanitization: %w", err)
	}

	// Unmarshal to generic interface{} to get correct types for sanitization
	var genericPayload interface{}
	if err := json.Unmarshal(payloadJSON, &genericPayload); err != nil {
		return fmt.Errorf("failed to unmarshal payload for sanitization: %w", err)
	}

	// Sanitize the generic data to convert microsecond timestamps
	sanitizedData := SanitizeTimestamps(genericPayload)

	// Convert telemetry payload to HEC format with sanitized event data
	hecEvent := HECEvent{
		Time:       timeToUnixPtr(payload.Timestamp), // Use original timestamp for HEC time field
		Host:       payload.ClusterID,
		Source:     "humio-operator",
		SourceType: payload.SourceType,
		Event:      sanitizedData, // Use sanitized data for the event field
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(hecEvent)
	if err != nil {
		return fmt.Errorf("failed to marshal telemetry payload: %w", err)
	}

	// Log basic HEC payload info
	h.Logger.Info("Sending HEC payload to LogScale",
		"cluster_id", payload.ClusterID,
		"collection_type", payload.CollectionType,
		"size_bytes", len(jsonData))

	// Send with retry logic
	return h.sendWithRetry(ctx, jsonData, 3)
}

// sendWithRetry implements retry logic with exponential backoff
func (h *HECClient) sendWithRetry(ctx context.Context, jsonData []byte, maxRetries int) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s... with maximum of 30 seconds
			// Use a safer approach to prevent integer overflow
			var backoff time.Duration
			switch attempt {
			case 1:
				backoff = 1 * time.Second
			case 2:
				backoff = 2 * time.Second
			case 3:
				backoff = 4 * time.Second
			case 4:
				backoff = 8 * time.Second
			case 5:
				backoff = 16 * time.Second
			default:
				backoff = 30 * time.Second // Cap at 30 seconds for any attempt >= 6
			}
			h.Logger.Info("Retrying telemetry export", "attempt", attempt, "backoff", backoff)

			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(backoff):
				// Continue with retry
			}
		}

		err := h.sendRequest(ctx, jsonData)
		if err == nil {
			if attempt > 0 {
				h.Logger.Info("Telemetry export succeeded after retry", "attempts", attempt+1)
			}
			return nil
		}

		// Check if this is a non-retryable error
		var nonRetryableErr *NonRetryableError
		if errors.As(err, &nonRetryableErr) {
			h.Logger.Info("Non-retryable error encountered, stopping retries", "error", err)
			return nonRetryableErr.Err
		}

		lastErr = err
		h.Logger.Info("Telemetry export attempt failed", "attempt", attempt+1, "error", err)
	}

	return fmt.Errorf("telemetry export failed after %d attempts: %w", maxRetries+1, lastErr)
}

// sendRequest performs the actual HTTP request to the HEC endpoint
func (h *HECClient) sendRequest(ctx context.Context, jsonData []byte) error {
	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers - LogScale HEC format
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", h.Token))

	// Send request
	resp, err := h.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			h.Logger.Info("Failed to close response body", "error", closeErr)
		}
	}()

	// Read response body for error details
	bodyBytes, _ := io.ReadAll(resp.Body)

	// Check response status
	if resp.StatusCode >= 400 {
		errorMsg := fmt.Sprintf("HEC request failed with status %d: %s", resp.StatusCode, string(bodyBytes))

		// 4xx errors are client errors and should not be retried
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return &NonRetryableError{Err: errors.New(errorMsg)}
		}

		// 5xx errors are server errors and should be retried
		return errors.New(errorMsg)
	}

	h.Logger.Info("HEC request successful",
		"status", resp.StatusCode,
		"response", string(bodyBytes))

	return nil
}

// timeToUnixPtr converts a time.Time to a Unix timestamp pointer (in seconds, not milliseconds)
// LogScale HEC appears to expect seconds and then converts to milliseconds internally
func timeToUnixPtr(t time.Time) *int64 {
	unix := t.Unix() // Use seconds instead of milliseconds
	return &unix
}

// TelemetryExporter provides a high-level interface for exporting telemetry data
type TelemetryExporter struct {
	hecClient *HECClient
	logger    logr.Logger
}

// NewTelemetryExporter creates a new telemetry exporter
func NewTelemetryExporter(url, token string, insecureSkipVerify bool, logger logr.Logger) *TelemetryExporter {
	return &TelemetryExporter{
		hecClient: NewHECClient(url, token, insecureSkipVerify, logger),
		logger:    logger,
	}
}

// ExportPayloads exports telemetry payloads and returns any export errors
func (e *TelemetryExporter) ExportPayloads(ctx context.Context, payloads []TelemetryPayload) []TelemetryError {
	var exportErrors []TelemetryError

	for _, payload := range payloads {
		if err := e.hecClient.ExportTelemetryPayload(ctx, payload); err != nil {
			exportErrors = append(exportErrors, TelemetryError{
				Type:      TelemetryErrorTypeExport,
				Message:   fmt.Sprintf("Failed to export %s data: %v", payload.CollectionType, err),
				Timestamp: time.Now(),
			})
			e.logger.Error(err, "Failed to export telemetry payload",
				"collection_type", payload.CollectionType,
				"cluster_id", payload.ClusterID)
		} else {
			e.logger.Info("Successfully exported telemetry payload",
				"collection_type", payload.CollectionType,
				"cluster_id", payload.ClusterID)
		}
	}

	return exportErrors
}
