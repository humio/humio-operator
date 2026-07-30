package controller

import (
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// effectiveMinAvailable computes the PDB minAvailable and maxUnavailable values.
func effectiveMinAvailable(
	state string,
	userMinAvailable *intstr.IntOrString,
	userMaxUnavailable *intstr.IntOrString,
	replicaCount int,
	freezeDuringUpdate *bool,
) (minAvail *intstr.IntOrString, maxUnavail *intstr.IntOrString) {
	freeze := freezeDuringUpdate != nil && *freezeDuringUpdate

	// Freeze override: set minAvailable to replica count, nil maxUnavailable.
	// Only applies during Upgrading/Restarting AND when replicas > 0.
	if freeze && replicaCount > 0 && isFreezeState(state) {
		v := intstr.FromInt(replicaCount)
		return &v, nil
	}

	// No freeze: return user's configured fields respecting mutual exclusion.
	if userMinAvailable != nil {
		return userMinAvailable, nil
	}
	if userMaxUnavailable != nil {
		return nil, userMaxUnavailable
	}
	// Neither set — caller handles default.
	return nil, nil
}

// isFreezeState reports whether the given cluster state triggers PDB freeze.
func isFreezeState(state string) bool {
	return state == humiov1alpha1.HumioClusterStateUpgrading ||
		state == humiov1alpha1.HumioClusterStateRestarting
}
