package controllers

import (
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

type podLifecycleState struct {
	nodePool                HumioNodePool
	pod                     corev1.Pod
	versionDifference       *podLifecycleStateVersionDifference
	configurationDifference *podLifecycleStateConfigurationDifference
}

type podLifecycleStateVersionDifference struct {
	from *HumioVersion
	to   *HumioVersion
}

type podLifecycleStateConfigurationDifference struct {
	requiresSimultaneousRestart bool
}

func NewPodLifecycleState(hnp HumioNodePool, pod corev1.Pod) *podLifecycleState {
	return &podLifecycleState{
		nodePool: hnp,
		pod:      pod,
	}
}

func (p *podLifecycleState) ShouldRollingRestart() bool {
	if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate {
		return true
	}
	if p.WantsUpgrade() {
		// if we're trying to go to or from a "latest" image, we can't do any version comparison
		if p.versionDifference.from.IsLatest() || p.versionDifference.to.IsLatest() {
			return false
		}
		if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyRollingUpdateBestEffort {
			if p.versionDifference.from.SemVer().Major() == p.versionDifference.to.SemVer().Major() {
				// allow rolling upgrades and downgrades for patch releases
				if p.versionDifference.from.SemVer().Minor() == p.versionDifference.to.SemVer().Minor() {
					return true
				}
				// only allow rolling upgrades for stable releases (non-preview)
				if p.versionDifference.to.IsStable() {
					// only allow rolling upgrades that are changing by one minor version
					if p.versionDifference.from.SemVer().Minor()+1 == p.versionDifference.to.SemVer().Minor() {
						return true
					}
				}
				// only allow rolling downgrades for stable versions (non-preview)
				if p.versionDifference.from.IsStable() {
					// only allow rolling downgrades that are changing by one minor version
					if p.versionDifference.from.SemVer().Minor()-1 == p.versionDifference.to.SemVer().Minor() {
						return true
					}
				}
			}
		}
		return false
	}
	if p.configurationDifference != nil {
		return !p.configurationDifference.requiresSimultaneousRestart
	}

	return false
}

func (p *podLifecycleState) ShouldDeletePod() bool {
	if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyOnDelete {
		return false
	}
	return p.WantsUpgrade() || p.WantsRestart()
}

func (p *podLifecycleState) WantsUpgrade() bool {
	return p.versionDifference != nil
}

func (p *podLifecycleState) WantsRestart() bool {
	return p.configurationDifference != nil
}
