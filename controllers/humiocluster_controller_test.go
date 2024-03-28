package controllers

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
)

func TestMergeEnvVars(t *testing.T) {
	testCases := []struct {
		name     string
		common   []corev1.EnvVar
		nodepool []corev1.EnvVar
		expected []corev1.EnvVar
	}{
		{
			name:   "no common env vars",
			common: []corev1.EnvVar{},
			nodepool: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
		},
		{
			name: "no duplicate env vars",
			common: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
			nodepool: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODEPOOL_ENV_VAR", Value: "nodepool_value"},
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
		},
		{
			name: "duplicate env vars",
			common: []corev1.EnvVar{
				{Name: "DUPLICATE_ENV_VAR", Value: "common_value"},
			},
			nodepool: []corev1.EnvVar{
				{Name: "NODE_ENV_VAR", Value: "nodepool_value"},
				{Name: "DUPLICATE_ENV_VAR", Value: "nodepool_value"},
			},
			expected: []corev1.EnvVar{
				{Name: "NODE_ENV_VAR", Value: "nodepool_value"},
				{Name: "DUPLICATE_ENV_VAR", Value: "nodepool_value"},
			},
		},
		{
			name: "no nodepool env vars",
			common: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
			nodepool: []corev1.EnvVar{},
			expected: []corev1.EnvVar{
				{Name: "COMMON_ENV_VAR", Value: "common_value"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := mergeCommonEnvVars(tc.common, tc.nodepool)
			if d := cmp.Diff(tc.expected, actual); d != "" {
				t.Errorf("expected: %v, got: %v", tc.expected, actual)
			}
		})
	}
}
