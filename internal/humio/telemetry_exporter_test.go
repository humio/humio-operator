package humio

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

const (
	testClusterName = "test-cluster"
	jsonSourceType  = "json"
)

func TestHECClient_ExportTelemetryPayload(t *testing.T) {
	tests := []struct {
		name            string
		payload         TelemetryPayload
		serverResponse  int
		serverBody      string
		expectedError   bool
		validateRequest func(t *testing.T, req *http.Request)
	}{
		{
			name: "successful export with Bearer authentication",
			payload: TelemetryPayload{
				ClusterID:      testClusterName,
				CollectionType: TelemetryCollectionTypeLicense,
				SourceType:     jsonSourceType,
				Timestamp:      time.Now(),
				Data:           map[string]interface{}{"test": "data"},
			},
			serverResponse: 200,
			serverBody:     `{"text":"Success","code":0}`,
			expectedError:  false,
			validateRequest: func(t *testing.T, req *http.Request) {
				// Validate HTTP method
				if req.Method != http.MethodPost {
					t.Errorf("Expected POST method, got %s", req.Method)
				}

				// Validate Content-Type header
				contentType := req.Header.Get("Content-Type")
				if contentType != "application/json" {
					t.Errorf("Expected Content-Type 'application/json', got '%s'", contentType)
				}

				// Validate Bearer authentication format (critical test)
				authHeader := req.Header.Get("Authorization")
				expectedAuth := "Bearer test-token"
				if authHeader != expectedAuth {
					t.Errorf("Expected Authorization '%s', got '%s'", expectedAuth, authHeader)
				}

				// Validate that old Splunk format is NOT used
				if strings.HasPrefix(authHeader, "Splunk ") {
					t.Errorf("Found deprecated Splunk authentication format: '%s'. Should use Bearer format", authHeader)
				}

				// Validate request body structure
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Errorf("Failed to read request body: %v", err)
					return
				}

				var hecEvent HECEvent
				if err := json.Unmarshal(body, &hecEvent); err != nil {
					t.Errorf("Failed to unmarshal HEC event: %v", err)
					return
				}

				// Validate HEC event structure
				if hecEvent.Host != testClusterName {
					t.Errorf("Expected Host '%s', got '%s'", testClusterName, hecEvent.Host)
				}
				if hecEvent.Source != "humio-operator" {
					t.Errorf("Expected Source 'humio-operator', got '%s'", hecEvent.Source)
				}
				if hecEvent.SourceType != jsonSourceType {
					t.Errorf("Expected SourceType '%s', got '%s'", jsonSourceType, hecEvent.SourceType)
				}
			},
		},
		{
			name: "handles server error correctly",
			payload: TelemetryPayload{
				ClusterID:      "test-cluster",
				CollectionType: TelemetryCollectionTypeClusterInfo,
				SourceType:     TelemetrySourceTypeJSON,
				Timestamp:      time.Now(),
				Data:           map[string]interface{}{"error": "test"},
			},
			serverResponse: 400,
			serverBody:     `{"text":"Invalid request","code":4}`,
			expectedError:  true,
			validateRequest: func(t *testing.T, req *http.Request) {
				// Still validate authentication format even on errors
				authHeader := req.Header.Get("Authorization")
				if !strings.HasPrefix(authHeader, "Bearer ") {
					t.Errorf("Expected Bearer authentication even on errors, got '%s'", authHeader)
				}
			},
		},
		{
			name: "validates token format in retry scenarios",
			payload: TelemetryPayload{
				ClusterID:      "retry-cluster",
				CollectionType: TelemetryCollectionTypeRepositoryUsage,
				SourceType:     TelemetrySourceTypeJSON,
				Timestamp:      time.Now(),
				Data:           map[string]interface{}{"retry": "test"},
			},
			serverResponse: 500, // Will trigger retry
			serverBody:     `{"text":"Server error","code":5}`,
			expectedError:  true,
			validateRequest: func(t *testing.T, req *http.Request) {
				// Ensure Bearer format is consistent across retries
				authHeader := req.Header.Get("Authorization")
				expectedAuth := "Bearer test-token"
				if authHeader != expectedAuth {
					t.Errorf("Expected consistent Bearer auth across retries: '%s', got '%s'", expectedAuth, authHeader)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestCount++

				// Validate request on first attempt (and retries if applicable)
				tt.validateRequest(t, r)

				w.WriteHeader(tt.serverResponse)
				_, _ = w.Write([]byte(tt.serverBody))
			}))
			defer server.Close()

			// Create HEC client with test server URL and token
			logger := logr.Discard() // Use discard logger for tests
			client := NewHECClient(server.URL, "test-token", true, logger)

			// Execute export
			err := client.ExportTelemetryPayload(context.Background(), tt.payload)

			// Validate error expectation
			if tt.expectedError && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			// Verify at least one request was made
			if requestCount == 0 {
				t.Errorf("Expected at least one HTTP request, got %d", requestCount)
			}
		})
	}
}

func TestTelemetryExporter_ExportPayloads(t *testing.T) {
	tests := []struct {
		name            string
		payloads        []TelemetryPayload
		serverResponses []int // Response codes for each payload
		expectedErrors  int   // Number of expected export errors
	}{
		{
			name: "exports multiple payloads with Bearer auth",
			payloads: []TelemetryPayload{
				{
					ClusterID:      "multi-test-cluster",
					CollectionType: TelemetryCollectionTypeLicense,
					SourceType:     TelemetrySourceTypeJSON,
					Timestamp:      time.Now(),
					Data:           map[string]interface{}{"license": "data"},
				},
				{
					ClusterID:      "multi-test-cluster",
					CollectionType: TelemetryCollectionTypeClusterInfo,
					SourceType:     TelemetrySourceTypeJSON,
					Timestamp:      time.Now(),
					Data:           map[string]interface{}{"cluster": "info"},
				},
			},
			serverResponses: []int{200, 200},
			expectedErrors:  0,
		},
		{
			name: "handles mixed success/failure with consistent auth",
			payloads: []TelemetryPayload{
				{
					ClusterID:      "mixed-test-cluster",
					CollectionType: TelemetryCollectionTypeLicense,
					SourceType:     TelemetrySourceTypeJSON,
					Timestamp:      time.Now(),
					Data:           map[string]interface{}{"success": "data"},
				},
				{
					ClusterID:      "mixed-test-cluster",
					CollectionType: TelemetryCollectionTypeClusterInfo,
					SourceType:     TelemetrySourceTypeJSON,
					Timestamp:      time.Now(),
					Data:           map[string]interface{}{"failure": "data"},
				},
			},
			serverResponses: []int{200, 400},
			expectedErrors:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestIndex := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Validate Bearer authentication format for all requests
				authHeader := r.Header.Get("Authorization")
				expectedAuth := "Bearer multi-test-token"
				if authHeader != expectedAuth {
					t.Errorf("Request %d: Expected Authorization '%s', got '%s'", requestIndex, expectedAuth, authHeader)
				}

				// Ensure NOT using Splunk format
				if strings.HasPrefix(authHeader, "Splunk ") {
					t.Errorf("Request %d: Found deprecated Splunk format: '%s'", requestIndex, authHeader)
				}

				// Return appropriate response
				if requestIndex < len(tt.serverResponses) {
					w.WriteHeader(tt.serverResponses[requestIndex])
				} else {
					w.WriteHeader(500)
				}
				_, _ = w.Write([]byte(`{"text":"Response"}`))

				requestIndex++
			}))
			defer server.Close()

			// Create telemetry exporter
			logger := logr.Discard()
			exporter := NewTelemetryExporter(server.URL, "multi-test-token", true, logger)

			// Export payloads
			errors := exporter.ExportPayloads(context.Background(), tt.payloads)

			// Validate error count
			if len(errors) != tt.expectedErrors {
				t.Errorf("Expected %d errors, got %d", tt.expectedErrors, len(errors))
			}

			// Validate that all requests were made
			if requestIndex != len(tt.payloads) {
				t.Errorf("Expected %d requests, got %d", len(tt.payloads), requestIndex)
			}
		})
	}
}

func TestHECEvent_Structure(t *testing.T) {
	// Test HEC event structure matches LogScale expectations
	payload := TelemetryPayload{
		ClusterID:      "struct-test-cluster",
		CollectionType: TelemetryCollectionTypeLicense,
		SourceType:     TelemetrySourceTypeJSON,
		Timestamp:      time.Date(2023, 12, 25, 10, 30, 0, 0, time.UTC),
		Data:           map[string]interface{}{"license": "test-data", "nodes": 3},
	}

	// Convert to HEC format
	hecEvent := HECEvent{
		Time:       timeToUnixPtr(payload.Timestamp),
		Host:       payload.ClusterID,
		Source:     "humio-operator",
		SourceType: payload.SourceType,
		Event:      payload,
	}

	// Validate structure
	if hecEvent.Host != "struct-test-cluster" {
		t.Errorf("Expected Host 'struct-test-cluster', got '%s'", hecEvent.Host)
	}
	if hecEvent.Source != "humio-operator" {
		t.Errorf("Expected Source 'humio-operator', got '%s'", hecEvent.Source)
	}
	if hecEvent.SourceType != "json" {
		t.Errorf("Expected SourceType 'json', got '%s'", hecEvent.SourceType)
	}
	if hecEvent.Time == nil || *hecEvent.Time != 1703500200 {
		t.Errorf("Expected Unix timestamp 1703500200 (seconds), got %v", hecEvent.Time)
	}

	// Validate JSON serialization
	jsonData, err := json.Marshal(hecEvent)
	if err != nil {
		t.Errorf("Failed to marshal HEC event: %v", err)
	}

	// Validate JSON contains expected fields
	jsonStr := string(jsonData)
	expectedFields := []string{
		`"host":"struct-test-cluster"`,
		`"source":"humio-operator"`,
		`"sourcetype":"json"`,
		`"time":1703500200`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(jsonStr, field) {
			t.Errorf("Expected JSON to contain '%s', got: %s", field, jsonStr)
		}
	}
}

func TestNewHECClient_Configuration(t *testing.T) {
	logger := logr.Discard()

	tests := []struct {
		name               string
		url                string
		token              string
		insecureSkipVerify bool
	}{
		{
			name:               "secure configuration",
			url:                "https://logscale.example.com/api/v1/ingest/hec",
			token:              "secure-token-123",
			insecureSkipVerify: false,
		},
		{
			name:               "insecure testing configuration",
			url:                "http://localhost:8080/api/v1/ingest/hec",
			token:              "test-token-456",
			insecureSkipVerify: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewHECClient(tt.url, tt.token, tt.insecureSkipVerify, logger)

			// Validate client configuration
			if client.URL != tt.url {
				t.Errorf("Expected URL '%s', got '%s'", tt.url, client.URL)
			}
			if client.Token != tt.token {
				t.Errorf("Expected Token '%s', got '%s'", tt.token, client.Token)
			}
			if client.HTTPClient == nil {
				t.Errorf("Expected HTTPClient to be initialized")
			}
			if client.Logger != logger {
				t.Errorf("Expected Logger to be set")
			}
		})
	}
}
