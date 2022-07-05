package controllers

import (
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"
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

func (p *podLifecycleState) RemainingMinReadyWaitTime(pods []corev1.Pod) time.Duration {
	// We will only try to wait if we are performing a rolling restart and have MinReadySeconds set above 0.
	// Additionally, if we do a rolling restart and MinReadySeconds is unset, then we also do not want to wait.
	if !p.ShouldRollingRestart() || p.nodePool.GetUpdateStrategy().MinReadySeconds <= 0 {
		return -1
	}
	var minReadySeconds = p.nodePool.GetUpdateStrategy().MinReadySeconds
	var conditions []corev1.PodCondition
	for _, pod := range pods {
		if pod.Name == p.pod.Name {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				conditions = append(conditions, condition)
			}
		}
	}

	// We take the condition with the latest transition time among type PodReady conditions with Status true for ready pods.
	// Then we look at the condition with the latest transition time that is not for the pod that is a deletion candidate.
	// We then take the difference between the latest transition time and now and compare this to the MinReadySeconds setting.
	// This also means that if you quickly perform another rolling restart after another finished,
	// then you may initially wait for the minReadySeconds timer on the first pod.
	var latestTransitionTime = latestTransitionTime(conditions)
	if !latestTransitionTime.Time.IsZero() {
		var diff = time.Since(latestTransitionTime.Time).Milliseconds()
		var minRdy = (time.Second * time.Duration(minReadySeconds)).Milliseconds()
		if diff <= minRdy {
			return time.Second * time.Duration((minRdy-diff)/1000)
		}
	}
	return -1
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

func latestTransitionTime(conditions []corev1.PodCondition) metav1.Time {
	if len(conditions) == 0 {
		return metav1.NewTime(time.Time{})
	}
	var max = conditions[0].LastTransitionTime
	for idx, condition := range conditions {
		if condition.LastTransitionTime.Time.IsZero() {
			continue
		}
		if idx == 0 || condition.LastTransitionTime.Time.After(max.Time) {
			max = condition.LastTransitionTime
		}
	}
	return max
}
