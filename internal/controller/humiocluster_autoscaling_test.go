package controller

import (
	"testing"

	"github.com/humio/humio-operator/api/v1alpha1"
	"k8s.io/utils/ptr"
)

func TestClampReplicas(t *testing.T) {
	tests := []struct {
		name     string
		value    int32
		min      int32
		max      int32
		expected int32
	}{
		{
			name:     "value in bounds",
			value:    5,
			min:      2,
			max:      10,
			expected: 5,
		},
		{
			name:     "value below min",
			value:    1,
			min:      2,
			max:      10,
			expected: 2,
		},
		{
			name:     "value above max",
			value:    12,
			min:      2,
			max:      10,
			expected: 10,
		},
		{
			name:     "min equals max",
			value:    5,
			min:      5,
			max:      5,
			expected: 5,
		},
		{
			name:     "max less than min",
			value:    5,
			min:      10,
			max:      2,
			expected: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := clampReplicas(tt.value, tt.min, tt.max)
			if result != tt.expected {
				t.Errorf("clampReplicas(%d, %d, %d) = %d, want %d",
					tt.value, tt.min, tt.max, result, tt.expected)
			}
		})
	}
}

func TestEffectiveMaxReplicas(t *testing.T) {
	tests := []struct {
		name     string
		spec     *v1alpha1.AutoscalingSpec
		min      int32
		expected int32
	}{
		{
			name:     "valid max",
			spec:     &v1alpha1.AutoscalingSpec{MaxReplicas: 10},
			min:      3,
			expected: 10,
		},
		{
			name:     "max less than min",
			spec:     &v1alpha1.AutoscalingSpec{MaxReplicas: 1},
			min:      3,
			expected: 3,
		},
		{
			name:     "nil spec",
			spec:     nil,
			min:      3,
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := effectiveMaxReplicas(tt.spec, tt.min)
			if result != tt.expected {
				t.Errorf("effectiveMaxReplicas() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestIsExplicitOverride(t *testing.T) {
	tests := []struct {
		name     string
		input    *int32
		expected bool
	}{
		{
			name:     "explicit value",
			input:    ptr.To(int32(5)),
			expected: true,
		},
		{
			name:     "explicit zero",
			input:    ptr.To(int32(0)),
			expected: true,
		},
		{
			name:     "nil",
			input:    nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isExplicitOverride(tt.input)
			if result != tt.expected {
				t.Errorf("isExplicitOverride() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestResolveEffectiveNodeCount(t *testing.T) {
	tests := []struct {
		name                  string
		specNodeCount         *int32
		statusDesiredReplicas int32
		autoscaling           *v1alpha1.AutoscalingSpec
		expected              int32
	}{
		{
			name:                  "explicit override",
			specNodeCount:         ptr.To(int32(5)),
			statusDesiredReplicas: 7,
			autoscaling:           &v1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(2)), MaxReplicas: 10},
			expected:              5,
		},
		{
			name:                  "explicit zero",
			specNodeCount:         ptr.To(int32(0)),
			statusDesiredReplicas: 7,
			autoscaling:           &v1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(2)), MaxReplicas: 10},
			expected:              0,
		},
		{
			name:                  "from status",
			specNodeCount:         nil,
			statusDesiredReplicas: 7,
			autoscaling:           &v1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(2)), MaxReplicas: 10},
			expected:              7,
		},
		{
			name:                  "from min",
			specNodeCount:         nil,
			statusDesiredReplicas: 0,
			autoscaling:           &v1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(3)), MaxReplicas: 10},
			expected:              3,
		},
		{
			name:                  "default without autoscaling",
			specNodeCount:         nil,
			statusDesiredReplicas: 0,
			autoscaling:           nil,
			expected:              0,
		},
		{
			name:                  "default with autoscaling",
			specNodeCount:         nil,
			statusDesiredReplicas: 0,
			autoscaling:           &v1alpha1.AutoscalingSpec{MinReplicas: nil, MaxReplicas: 10},
			expected:              2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveEffectiveNodeCount(tt.specNodeCount, tt.statusDesiredReplicas, tt.autoscaling)
			if result != tt.expected {
				t.Errorf("resolveEffectiveNodeCount() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestEffectiveMinReplicas(t *testing.T) {
	tests := []struct {
		name     string
		spec     *v1alpha1.AutoscalingSpec
		expected int32
	}{
		{
			name: "explicit minReplicas set",
			spec: &v1alpha1.AutoscalingSpec{
				MinReplicas: ptr.To(int32(3)),
				MaxReplicas: 10,
			},
			expected: 3,
		},
		{
			name: "minReplicas nil defaults to 2",
			spec: &v1alpha1.AutoscalingSpec{
				MinReplicas: nil,
				MaxReplicas: 10,
			},
			expected: 2,
		},
		{
			name:     "nil spec defaults to 2",
			spec:     nil,
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := effectiveMinReplicas(tt.spec)
			if result != tt.expected {
				t.Errorf("effectiveMinReplicas() = %d, want %d", result, tt.expected)
			}
		})
	}
}
