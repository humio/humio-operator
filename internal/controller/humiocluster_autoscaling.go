// Package controller implements the Kubernetes controllers for Humio resources.
package controller

import "github.com/humio/humio-operator/api/v1alpha1"

// clampReplicas ensures the value is within the specified min/max bounds.
// If max < min, min is treated as the authoritative bound.
func clampReplicas(value, min, max int32) int32 {
	// If max < min, min takes precedence
	if max < min {
		return min
	}

	// Normal clamping
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// effectiveMinReplicas returns the minimum replicas from the autoscaling spec,
// or the default value of 2 if not specified.
func effectiveMinReplicas(spec *v1alpha1.AutoscalingSpec) int32 {
	if spec == nil || spec.MinReplicas == nil {
		return 2
	}
	return *spec.MinReplicas
}

// effectiveMaxReplicas returns the maximum replicas, ensuring it is >= min.
func effectiveMaxReplicas(spec *v1alpha1.AutoscalingSpec, min int32) int32 {
	if spec == nil || spec.MaxReplicas < min {
		return min
	}
	return spec.MaxReplicas
}

// isExplicitOverride returns true when nodeCount is explicitly set (including 0).
func isExplicitOverride(specNodeCount *int32) bool {
	return specNodeCount != nil
}

const defaultMinReplicas int32 = 2

// resolveEffectiveNodeCount implements the precedence chain:
// 1. specNodeCount (explicit override, including 0)
// 2. statusDesiredReplicas (last HPA-written value)
// 3. autoscaling.MinReplicas
// 4. defaultMinReplicas (only when autoscaling is configured)
// 5. 0 (no autoscaling, no explicit count — backwards compatible with old int zero-value default)
func resolveEffectiveNodeCount(specNodeCount *int32, statusDesiredReplicas int32, autoscaling *v1alpha1.AutoscalingSpec) int32 {
	if specNodeCount != nil {
		return *specNodeCount
	}
	if statusDesiredReplicas > 0 {
		return statusDesiredReplicas
	}
	if autoscaling != nil {
		if autoscaling.MinReplicas != nil {
			return *autoscaling.MinReplicas
		}
		return defaultMinReplicas
	}
	return 0
}
