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
	fromVersion *HumioVersion
	toVersion   *HumioVersion
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
		if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyRollingUpdateBestEffort {
			if p.versionDifference.fromVersion.Version().Major() == p.versionDifference.toVersion.Version().Major() {
				// if only the patch version changes, then we are safe to do a rolling upgrade
				if p.versionDifference.fromVersion.Version().Minor() == p.versionDifference.toVersion.Version().Minor() {
					return true
				}
				// if the version being upgraded is not a preview, and is only increasing my one revision, then we are
				// safe to do a rolling upgrade
				if p.versionDifference.toVersion.Version().Minor()%2 == 0 &&
					p.versionDifference.fromVersion.Version().Minor()+1 == p.versionDifference.toVersion.Version().Minor() {
					return true
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

func (p *podLifecycleState) WantsUpgrade() bool {
	return p.versionDifference != nil
}

func (p *podLifecycleState) WantsRestart() bool {
	return p.configurationDifference != nil
}

func (p *podLifecycleState) ShouldDeletePod() bool {
	if p.nodePool.GetUpdateStrategy().Type == humiov1alpha1.HumioClusterUpdateStrategyOnDelete {
		return false
	}
	return p.WantsUpgrade() || p.WantsRestart()
}
