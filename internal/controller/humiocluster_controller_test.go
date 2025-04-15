package controller

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestMergeEnvVars(t *testing.T) {
	testCases := []struct {
		name     string
		from     []corev1.EnvVar
		into     []corev1.EnvVar
		expected []corev1.EnvVar
	}{
		{
			name: "no from",
			from: []corev1.EnvVar{},
			into: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
		},
		{
			name: "no duplicates",
			from: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
			into: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
		},
		{
			name: "duplicates",
			from: []corev1.EnvVar{
				{Name: "DUPLICATE_ENV_VAR", Value: "common_value"},
			},
			into: []corev1.EnvVar{
				{Name: "NODE_ENV_VAR", Value: "nodepool_value"},
				{Name: "DUPLICATE_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODE_ENV_VAR", Value: "nodepool_value"},
				{Name: "DUPLICATE_ENV_VAR", Value: "nodepool_value"},
			},
		},
		{
			name: "no into",
			from: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
			into: []corev1.EnvVar{},
			expected: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := mergeEnvVars(tc.from, tc.into)
			if d := cmp.Diff(tc.expected, actual); d != "" {
				t.Errorf("expected: %v, got: %v", tc.expected, actual)
			}
		})
	}
}

func TestFindDuplicateEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		envVars  []corev1.EnvVar
		expected map[string]int
	}{
		{
			name: "No duplicates",
			envVars: []corev1.EnvVar{
				{Name: "VAR1", Value: "value1"},
				{Name: "VAR2", Value: "value2"},
			},
			expected: map[string]int{},
		},
		{
			name: "With duplicates",
			envVars: []corev1.EnvVar{
				{Name: "VAR1", Value: "value1"},
				{Name: "VAR1", Value: "value1-dup"},
				{Name: "VAR2", Value: "value2"},
				{Name: "VAR3", Value: "value3"},
				{Name: "VAR2", Value: "value2-dup"},
			},
			expected: map[string]int{
				"VAR1": 2,
				"VAR2": 2,
			},
		},
		{
			name: "Triple duplicate",
			envVars: []corev1.EnvVar{
				{Name: "VAR1", Value: "value1"},
				{Name: "VAR1", Value: "value1-dup1"},
				{Name: "VAR1", Value: "value1-dup2"},
			},
			expected: map[string]int{
				"VAR1": 3,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duplicates := findDuplicateEnvVars(tt.envVars)
			assert.Equal(t, tt.expected, duplicates)
		})
	}
}

func TestGetDuplicateEnvVarsErrorMessage(t *testing.T) {
	tests := []struct {
		name       string
		duplicates map[string]int
		expected   string
	}{
		{
			name:       "No duplicates",
			duplicates: map[string]int{},
			expected:   "",
		},
		{
			name:       "One duplicate",
			duplicates: map[string]int{"VAR1": 2},
			expected:   "Duplicate environment variables found in HumioCluster spec: 'VAR1' appears 2 times",
		},
		{
			name:       "Multiple duplicates",
			duplicates: map[string]int{"VAR1": 2, "VAR2": 3},
			expected:   "Duplicate environment variables found in HumioCluster spec: 'VAR1' appears 2 times, 'VAR2' appears 3 times",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := GetDuplicateEnvVarsErrorMessage(tt.duplicates)
			assert.Equal(t, tt.expected, message)
		})
	}
}
