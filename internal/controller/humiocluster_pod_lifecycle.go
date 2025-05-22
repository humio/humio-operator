package controller

import (
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// PodLifeCycleState is used to hold information on what the next action should be based on what configuration
// changes are detected. It holds information that is specific to a single HumioNodePool in nodePool and the pod field
// holds information about what pod should be deleted next.
type PodLifeCycleState struct {
	// nodePool holds the HumioNodePool that is used to access the details and resources related to the node pool
	nodePool HumioNodePool
	// podsToBeReplaced holds the details of existing pods that is the next targets for pod deletion due to some
	// difference between current state vs desired state.
	podsToBeReplaced []corev1.Pod
	// versionDifference holds information on what version we are upgrading from/to.
	// This will be nil when no image version difference has been detected.
	versionDifference *podLifecycleStateVersionDifference
	// configurationDifference holds information indicating that we have detected a configuration difference.
	// If the configuration difference requires all pods within the node pool to be replaced at the same time,
	// requiresSimultaneousRestart will be set in podLifecycleStateConfigurationDifference.
	// This will be nil when no configuration difference has been detected.
	configurationDifference *podLifecycleStateConfigurationDifference
}

type podLifecycleStateVersionDifference struct {
	from *HumioVersion
	to   *HumioVersion
}

type podLifecycleStateConfigurationDifference struct {
	requiresSimultaneousRestart bool
}

func NewPodLifecycleState(hnp HumioNodePool) *PodLifeCycleState {
	return &PodLifeCycleState{
		nodePool: hnp,
	}
}

func (p *PodLifeCycleState) ShouldRollingRestart() bool {
	if p.FoundVersionDifference() {
		// if we're trying to go to or from a "latest" image, we can't do any version comparison
		if p.versionDifference.from.IsLatest() || p.versionDifference.to.IsLatest() {
			return false
		}
	}

	// If the configuration difference requires simultaneous restart, we don't need to consider which update
	// strategy is configured. We do this because certain configuration changes can be important to keep in
	// sync across all the pods.
	if p.FoundConfigurationDifference() && p.configurationDifference.requiresSimultaneousRestart {
		return false
	}

	if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyReplaceAllOnUpdate {
		return false
	}
	if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate {
		return true
	}
	if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyRollingUpdateBestEffort {
		if p.versionDifference.from.SemVer().Major() == p.versionDifference.to.SemVer().Major() {
			// allow rolling upgrades and downgrades for patch releases
			if p.versionDifference.from.SemVer().Minor() == p.versionDifference.to.SemVer().Minor() {
				return true
			}
		}
		return false
	}

	// if the user did not specify which update strategy to use, we default to the same behavior as humiov1alpha1.HumioClusterUpdateStrategyReplaceAllOnUpdate
	return false
}

func (p *PodLifeCycleState) ADifferenceWasDetectedAndManualDeletionsNotEnabled() bool {
	if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyOnDelete {
		return false
	}
	return p.FoundVersionDifference() || p.FoundConfigurationDifference()
}

func (p *PodLifeCycleState) FoundVersionDifference() bool {
	return p.versionDifference != nil
}

func (p *PodLifeCycleState) FoundConfigurationDifference() bool {
	return p.configurationDifference != nil
}

func (p *PodLifeCycleState) namesOfPodsToBeReplaced() []string {
	podNames := []string{}
	for _, pod := range p.podsToBeReplaced {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}
