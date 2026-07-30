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

// Package v1alpha1 contains API Schema definitions for the core v1alpha1 API group
package v1alpha1

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

// TestAutoscalingSpec_JSONSerialization tests that AutoscalingSpec serializes correctly to JSON
func TestAutoscalingSpec_JSONSerialization(t *testing.T) {
	tests := []struct {
		name     string
		spec     AutoscalingSpec
		expected string
	}{
		{
			name: "both minReplicas and maxReplicas set",
			spec: AutoscalingSpec{
				MinReplicas: ptr.To(int32(1)),
				MaxReplicas: 5,
			},
			expected: `{"minReplicas":1,"maxReplicas":5}`,
		},
		{
			name: "only maxReplicas set (minReplicas nil)",
			spec: AutoscalingSpec{
				MinReplicas: nil,
				MaxReplicas: 10,
			},
			expected: `{"maxReplicas":10}`,
		},
		{
			name: "maxReplicas equals minReplicas",
			spec: AutoscalingSpec{
				MinReplicas: ptr.To(int32(3)),
				MaxReplicas: 3,
			},
			expected: `{"minReplicas":3,"maxReplicas":3}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.spec)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}

			if string(data) != tt.expected {
				t.Errorf("expected JSON %s, got %s", tt.expected, string(data))
			}
		})
	}
}

// TestAutoscalingSpec_ValidationRules tests kubebuilder validation marker behavior
// NOTE: This is a contract test that documents expected validation behavior.
// Actual validation is performed by the API server via kubebuilder-generated CRD schema.
func TestAutoscalingSpec_ValidationRules(t *testing.T) {
	tests := []struct {
		name          string
		spec          AutoscalingSpec
		shouldBeValid bool
		reason        string
	}{
		{
			name: "valid: minReplicas=1, maxReplicas=5",
			spec: AutoscalingSpec{
				MinReplicas: ptr.To(int32(1)),
				MaxReplicas: 5,
			},
			shouldBeValid: true,
			reason:        "both within valid range",
		},
		{
			name: "invalid: minReplicas=0",
			spec: AutoscalingSpec{
				MinReplicas: ptr.To(int32(0)),
				MaxReplicas: 5,
			},
			shouldBeValid: false,
			reason:        "minReplicas must be >= 1 (kubebuilder:validation:Minimum=1)",
		},
		{
			name: "invalid: maxReplicas=0",
			spec: AutoscalingSpec{
				MaxReplicas: 0,
			},
			shouldBeValid: false,
			reason:        "maxReplicas must be >= 1 (kubebuilder:validation:Minimum=1)",
		},
		{
			name: "valid: minReplicas nil, maxReplicas=10",
			spec: AutoscalingSpec{
				MinReplicas: nil,
				MaxReplicas: 10,
			},
			shouldBeValid: true,
			reason:        "minReplicas is optional",
		},
		{
			name: "edge case: minReplicas > maxReplicas",
			spec: AutoscalingSpec{
				MinReplicas: ptr.To(int32(10)),
				MaxReplicas: 5,
			},
			shouldBeValid: true,
			reason:        "kubebuilder markers cannot express cross-field validation; operator handles at runtime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test documents validation contracts.
			// Actual validation happens at API server admission time via CRD schema.
			// We verify JSON serialization as a proxy for schema compatibility.
			data, err := json.Marshal(tt.spec)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}

			// For invalid specs with 0 values, verify they serialize (will be rejected by API server)
			if !tt.shouldBeValid {
				t.Logf("Invalid spec serializes as: %s (expected to be rejected by API server: %s)", string(data), tt.reason)
			} else {
				t.Logf("Valid spec serializes as: %s", string(data))
			}
		})
	}
}

// TestAutoscalingSpec_JSONDeserialization tests that JSON unmarshals correctly into AutoscalingSpec
func TestAutoscalingSpec_JSONDeserialization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected AutoscalingSpec
	}{
		{
			name:  "both fields present",
			input: `{"minReplicas":1,"maxReplicas":5}`,
			expected: AutoscalingSpec{
				MinReplicas: ptr.To(int32(1)),
				MaxReplicas: 5,
			},
		},
		{
			name:  "only maxReplicas present (minReplicas omitted)",
			input: `{"maxReplicas":10}`,
			expected: AutoscalingSpec{
				MinReplicas: nil,
				MaxReplicas: 10,
			},
		},
		{
			name:  "minReplicas explicitly null",
			input: `{"minReplicas":null,"maxReplicas":8}`,
			expected: AutoscalingSpec{
				MinReplicas: nil,
				MaxReplicas: 8,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec AutoscalingSpec
			err := json.Unmarshal([]byte(tt.input), &spec)
			if err != nil {
				t.Fatalf("json.Unmarshal failed: %v", err)
			}

			// Compare MinReplicas
			if (spec.MinReplicas == nil) != (tt.expected.MinReplicas == nil) {
				t.Errorf("MinReplicas pointer mismatch: got nil=%v, expected nil=%v",
					spec.MinReplicas == nil, tt.expected.MinReplicas == nil)
			}
			if spec.MinReplicas != nil && tt.expected.MinReplicas != nil {
				if *spec.MinReplicas != *tt.expected.MinReplicas {
					t.Errorf("MinReplicas value mismatch: got %d, expected %d",
						*spec.MinReplicas, *tt.expected.MinReplicas)
				}
			}

			// Compare MaxReplicas
			if spec.MaxReplicas != tt.expected.MaxReplicas {
				t.Errorf("MaxReplicas mismatch: got %d, expected %d",
					spec.MaxReplicas, tt.expected.MaxReplicas)
			}
		})
	}
}

// TestNodeCount_JSONSerialization tests that NodeCount pointer serializes correctly to JSON
func TestNodeCount_JSONSerialization(t *testing.T) {
	tests := []struct {
		name              string
		spec              HumioNodeSpec
		expectedNodeCount *int32
		expectFieldOmit   bool
	}{
		{
			name: "explicit nodeCount 3",
			spec: HumioNodeSpec{
				NodeCount: ptr.To(int32(3)),
			},
			expectedNodeCount: ptr.To(int32(3)),
			expectFieldOmit:   false,
		},
		{
			name: "explicit nodeCount 0",
			spec: HumioNodeSpec{
				NodeCount: ptr.To(int32(0)),
			},
			expectedNodeCount: ptr.To(int32(0)),
			expectFieldOmit:   false,
		},
		{
			name: "nil nodeCount",
			spec: HumioNodeSpec{
				NodeCount: nil,
			},
			expectedNodeCount: nil,
			expectFieldOmit:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.spec)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}

			// Unmarshal to check the nodeCount field
			var result map[string]interface{}
			err = json.Unmarshal(data, &result)
			if err != nil {
				t.Fatalf("json.Unmarshal to map failed: %v", err)
			}

			nodeCountVal, hasNodeCount := result["nodeCount"]
			if tt.expectFieldOmit {
				if hasNodeCount {
					t.Errorf("expected nodeCount to be omitted, but got: %v", nodeCountVal)
				}
			} else {
				if !hasNodeCount {
					t.Errorf("expected nodeCount to be present, but it was omitted")
				} else {
					// Convert to int32 for comparison
					var actualVal int32
					switch v := nodeCountVal.(type) {
					case float64:
						actualVal = int32(v)
					default:
						t.Fatalf("unexpected type for nodeCount: %T", v)
					}
					if actualVal != *tt.expectedNodeCount {
						t.Errorf("expected nodeCount %d, got %d", *tt.expectedNodeCount, actualVal)
					}
				}
			}
		})
	}
}

// TestNodeCount_JSONDeserialization tests that JSON unmarshals correctly into NodeCount pointer
func TestNodeCount_JSONDeserialization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected HumioNodeSpec
	}{
		{
			name:  "nodeCount 5 present",
			input: `{"nodeCount":5}`,
			expected: HumioNodeSpec{
				NodeCount: ptr.To(int32(5)),
			},
		},
		{
			name:  "nodeCount 0 present",
			input: `{"nodeCount":0}`,
			expected: HumioNodeSpec{
				NodeCount: ptr.To(int32(0)),
			},
		},
		{
			name:  "nodeCount omitted",
			input: `{}`,
			expected: HumioNodeSpec{
				NodeCount: nil,
			},
		},
		{
			name:  "nodeCount explicitly null",
			input: `{"nodeCount":null}`,
			expected: HumioNodeSpec{
				NodeCount: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var spec HumioNodeSpec
			err := json.Unmarshal([]byte(tt.input), &spec)
			if err != nil {
				t.Fatalf("json.Unmarshal failed: %v", err)
			}

			// Compare NodeCount
			if (spec.NodeCount == nil) != (tt.expected.NodeCount == nil) {
				t.Errorf("NodeCount pointer mismatch: got nil=%v, expected nil=%v",
					spec.NodeCount == nil, tt.expected.NodeCount == nil)
			}
			if spec.NodeCount != nil && tt.expected.NodeCount != nil {
				if *spec.NodeCount != *tt.expected.NodeCount {
					t.Errorf("NodeCount value mismatch: got %d, expected %d",
						*spec.NodeCount, *tt.expected.NodeCount)
				}
			}
		})
	}
}

func TestHumioNodeSpec_WithAutoscaling(t *testing.T) {
	spec := HumioNodeSpec{
		NodeCount: nil,
		Autoscaling: &AutoscalingSpec{
			MinReplicas: ptr.To(int32(2)),
			MaxReplicas: 8,
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	s := string(data)
	for _, want := range []string{`"autoscaling"`, `"minReplicas":2`, `"maxReplicas":8`} {
		if !json.Valid(data) {
			t.Fatalf("invalid JSON: %s", s)
		}
		found := false
		for i := 0; i <= len(s)-len(want); i++ {
			if s[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %s in JSON, got: %s", want, s)
		}
	}
}

func TestHumioNodeSpec_WithoutAutoscaling(t *testing.T) {
	spec := HumioNodeSpec{
		NodeCount:   ptr.To(int32(3)),
		Autoscaling: nil,
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	s := string(data)
	unwanted := `"autoscaling"`
	for i := 0; i <= len(s)-len(unwanted); i++ {
		if s[i:i+len(unwanted)] == unwanted {
			t.Errorf("expected no autoscaling field in JSON, got: %s", s)
			break
		}
	}
}

func TestHumioNodeSpecDNSFields(t *testing.T) {
	tests := []struct {
		name              string
		spec              HumioNodeSpec
		expectedPolicy    corev1.DNSPolicy
		expectedConfigNil bool
	}{
		{
			name:              "unset dns fields",
			spec:              HumioNodeSpec{},
			expectedPolicy:    "",
			expectedConfigNil: true,
		},
		{
			name: "dnsPolicy set to None",
			spec: HumioNodeSpec{
				DNSPolicy: "None",
			},
			expectedPolicy:    "None",
			expectedConfigNil: true,
		},
		{
			name: "dnsConfig set with nameservers",
			spec: HumioNodeSpec{
				DNSConfig: &corev1.PodDNSConfig{
					Nameservers: []string{"1.1.1.1"},
				},
			},
			expectedPolicy:    "",
			expectedConfigNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.spec.DNSPolicy != tt.expectedPolicy {
				t.Errorf("DNSPolicy = %v, want %v", tt.spec.DNSPolicy, tt.expectedPolicy)
			}
			if (tt.spec.DNSConfig == nil) != tt.expectedConfigNil {
				t.Errorf("DNSConfig nil = %v, want %v", tt.spec.DNSConfig == nil, tt.expectedConfigNil)
			}
		})
	}
}
