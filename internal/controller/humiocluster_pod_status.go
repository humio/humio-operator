package controller

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/kubernetes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	corev1 "k8s.io/api/core/v1"
)

const (
	containerStateCreating          = "ContainerCreating"
	containerStateCompleted         = "Completed"
	podInitializing                 = "PodInitializing"
	PodConditionReasonUnschedulable = "Unschedulable"
	podConditionReasonEvicted       = "Evicted"
)

type podsStatusState struct {
	// nodeCount holds the final number of expected pods set by the user (NodeCount).
	nodeCount int
	// readyCount holds the number of pods the pod condition PodReady is true.
	// This value gets initialized to 0 and incremented per pod where PodReady condition is true.
	readyCount int
	// notReadyCount holds the number of pods found we have not deemed ready.
	// This value gets initialized to the number of pods found.
	// For each pod found that has PodReady set to ConditionTrue, we decrement this value.
	notReadyCount int
	// notReadyDueToMinReadySeconds holds the number of pods that are ready, but have not been running for long enough
	notReadyDueToMinReadySeconds int
	// podRevisions is populated with the value of the pod annotation PodRevisionAnnotation.
	// The slice is sorted by pod name.
	podRevisions []int
	// podImageVersions holds the container image of the "humio" containers.
	// The slice is sorted by pod name.
	podImageVersions []string
	// podDeletionTimestampSet holds a boolean indicating if the pod is marked for deletion by checking if pod DeletionTimestamp is nil.
	// The slice is sorted by pod name.
	podDeletionTimestampSet []bool
	// podNames holds the pod name of the pods.
	// The slice is sorted by pod name.
	podNames []string
	// podAreUnschedulableOrHaveBadStatusConditions holds a list of pods that was detected as having errors, which is determined by the pod conditions.
	//
	// If pod conditions says it is unschedulable, it is added to podAreUnschedulableOrHaveBadStatusConditions.
	//
	// If pod condition ready is found with a value that is not ConditionTrue, we look at the pod ContainerStatuses.
	// When ContainerStatuses indicates the container is in Waiting status, we add it to podAreUnschedulableOrHaveBadStatusConditions if the reason
	// is not containerStateCreating nor podInitializing.
	// When ContainerStatuses indicates the container is in Terminated status, we add it to podAreUnschedulableOrHaveBadStatusConditions if the reason
	// is not containerStateCompleted.
	//
	// The slice is sorted by pod name.
	podAreUnschedulableOrHaveBadStatusConditions []corev1.Pod
	// podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists holds a list of pods that needs to be cleaned up due to being evicted, or if the pod is
	// stuck in phase Pending due to the use of a PVC that refers to a Kubernetes worker node that no longer exists.
	// The slice is sorted by pod name.
	podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists []corev1.Pod
	// podsReady holds the list of pods where pod condition PodReady is true
	// The slice is sorted by pod name.
	podsReady []corev1.Pod
	// scaledMaxUnavailable holds the maximum number of pods we allow to be unavailable at the same time.
	// When user defines a percentage, the value is rounded up to ensure scaledMaxUnavailable >= 1 as we cannot target
	// replacing no pods.
	// If the goal is to manually replace pods, the cluster update strategy should instead be set to
	// HumioClusterUpdateStrategyOnDelete.
	scaledMaxUnavailable int
	// minReadySeconds holds the number of seconds a pod must be in ready state for it to be treated as ready
	minReadySeconds int32
}

func (r *HumioClusterReconciler) getPodsStatus(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, foundPodList []corev1.Pod) (*podsStatusState, error) {
	status := podsStatusState{
		// initially, we assume no pods are ready
		readyCount: 0,
		// initially, we assume all pods found are not ready
		notReadyCount: len(foundPodList),
		// the number of pods we expect to have running is the nodeCount value set by the user
		nodeCount: hnp.GetNodeCount(),
		// the number of seconds a pod must be in ready state to be treated as ready
		minReadySeconds: hnp.GetUpdateStrategy().MinReadySeconds,
	}
	sort.Slice(foundPodList, func(i, j int) bool {
		return foundPodList[i].Name < foundPodList[j].Name
	})

	updateStrategy := hnp.GetUpdateStrategy()
	scaledMaxUnavailable, err := intstr.GetScaledValueFromIntOrPercent(updateStrategy.MaxUnavailable, hnp.GetNodeCount(), false)
	if err != nil {
		return &status, fmt.Errorf("unable to fetch rounded up scaled value for maxUnavailable based on %s with total of %d", updateStrategy.MaxUnavailable.String(), hnp.GetNodeCount())
	}

	// We ensure to always replace at least 1 pod, just in case the user specified maxUnavailable 0 or 0%, or
	// scaledMaxUnavailable becomes 0 as it is rounded down
	status.scaledMaxUnavailable = max(scaledMaxUnavailable, 1)

	var podsReady, podsNotReady []string
	for _, pod := range foundPodList {
		podRevisionStr := pod.Annotations[PodRevisionAnnotation]
		if podRevision, err := strconv.Atoi(podRevisionStr); err == nil {
			status.podRevisions = append(status.podRevisions, podRevision)
		} else {
			return &status, r.logErrorAndReturn(err, fmt.Sprintf("unable to identify pod revision for pod %s", pod.Name))
		}
		status.podDeletionTimestampSet = append(status.podDeletionTimestampSet, pod.DeletionTimestamp != nil)
		status.podNames = append(status.podNames, pod.Name)
		humioIdx, _ := kubernetes.GetContainerIndexByName(pod, "humio")
		status.podImageVersions = append(status.podImageVersions, pod.Spec.Containers[humioIdx].Image)

		// pods that were just deleted may still have a status of Ready, but we should not consider them ready
		if pod.DeletionTimestamp == nil {
			// If a pod is evicted, we don't want to wait for a new pod spec since the eviction could happen for a
			// number of reasons. If we delete the pod then we will re-create it on the next reconcile. Adding the pod
			// to the podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists list will cause it to be deleted.
			if pod.Status.Phase == corev1.PodFailed && pod.Status.Reason == podConditionReasonEvicted {
				r.Log.Info(fmt.Sprintf("pod %s has errors, pod phase: %s, reason: %s", pod.Name, pod.Status.Phase, pod.Status.Reason))
				status.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists = append(status.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists, pod)
				continue
			}
			if pod.Status.Phase == corev1.PodPending {
				deletePod, err := r.isPodAttachedToOrphanedPvc(ctx, hc, hnp, pod)
				if !deletePod && err != nil {
					return &status, r.logErrorAndReturn(err, "unable to determine whether pod should be deleted")
				}
				if deletePod && hnp.OkToDeletePvc() {
					status.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists = append(status.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists, pod)
				}
			}
			// If a pod is Pending but unschedulable, we want to consider this an error state so it will be replaced
			// but only if the pod spec is updated (e.g. to lower the pod resources).
			for _, condition := range pod.Status.Conditions {
				if condition.Status == corev1.ConditionFalse {
					if condition.Reason == PodConditionReasonUnschedulable {
						r.Log.Info(fmt.Sprintf("pod %s has errors, container status: %s, reason: %s", pod.Name, condition.Status, condition.Reason))
						status.podAreUnschedulableOrHaveBadStatusConditions = append(status.podAreUnschedulableOrHaveBadStatusConditions, pod)
						continue
					}
				}
				if condition.Type == corev1.PodReady {
					remainingMinReadyWaitTime := status.remainingMinReadyWaitTime(pod)
					if condition.Status == corev1.ConditionTrue && remainingMinReadyWaitTime <= 0 {
						status.podsReady = append(status.podsReady, pod)
						podsReady = append(podsReady, pod.Name)
						status.readyCount++
						status.notReadyCount--
					} else {
						podsNotReady = append(podsNotReady, pod.Name)
						if remainingMinReadyWaitTime > 0 {
							r.Log.Info(fmt.Sprintf("pod %s has not been ready for enough time yet according to minReadySeconds, remainingMinReadyWaitTimeSeconds=%f", pod.Name, remainingMinReadyWaitTime.Seconds()))
							status.notReadyDueToMinReadySeconds++
						}
						for _, containerStatus := range pod.Status.ContainerStatuses {
							if containerStatus.State.Waiting != nil && containerStatus.State.Waiting.Reason != containerStateCreating && containerStatus.State.Waiting.Reason != podInitializing {
								r.Log.Info(fmt.Sprintf("pod %s has errors, container state: Waiting, reason: %s", pod.Name, containerStatus.State.Waiting.Reason))
								status.podAreUnschedulableOrHaveBadStatusConditions = append(status.podAreUnschedulableOrHaveBadStatusConditions, pod)
							}
							if containerStatus.State.Terminated != nil && containerStatus.State.Terminated.Reason != containerStateCompleted {
								r.Log.Info(fmt.Sprintf("pod %s has errors, container state: Terminated, reason: %s", pod.Name, containerStatus.State.Terminated.Reason))
								status.podAreUnschedulableOrHaveBadStatusConditions = append(status.podAreUnschedulableOrHaveBadStatusConditions, pod)
							}
						}
					}
				}
			}
		}
	}
	r.Log.Info(fmt.Sprintf("pod status nodePoolName=%s readyCount=%d notReadyCount=%d podsReady=%s podsNotReady=%s maxUnavailable=%s scaledMaxUnavailable=%d minReadySeconds=%d", hnp.GetNodePoolName(), status.readyCount, status.notReadyCount, podsReady, podsNotReady, updateStrategy.MaxUnavailable.String(), scaledMaxUnavailable, status.minReadySeconds))
	// collect ready pods and not ready pods in separate lists and just print the lists here instead of a log entry per host
	return &status, nil
}

// waitingOnPods returns true when there are pods running that are not in a ready state. This does not include pods
// that are not ready due to container errors.
func (s *podsStatusState) waitingOnPods() bool {
	lessPodsReadyThanNodeCount := s.readyCount < s.nodeCount
	somePodIsNotReady := s.notReadyCount > 0
	return (lessPodsReadyThanNodeCount || somePodIsNotReady) &&
		!s.haveUnschedulablePodsOrPodsWithBadStatusConditions() &&
		!s.foundEvictedPodsOrPodsWithOrpahanedPVCs()
}

// scaledMaxUnavailableMinusNotReadyDueToMinReadySeconds returns an absolute number of pods we can delete.
func (s *podsStatusState) scaledMaxUnavailableMinusNotReadyDueToMinReadySeconds() int {
	deletionBudget := s.scaledMaxUnavailable - s.notReadyDueToMinReadySeconds
	return max(deletionBudget, 0)
}

// podRevisionCountMatchesNodeCountAndAllPodsHaveRevision returns true if we have the correct number of pods
// and all the pods have the same specified revision
func (s *podsStatusState) podRevisionCountMatchesNodeCountAndAllPodsHaveRevision(podRevision int) bool {
	// return early if number of revisions doesn't match nodeCount, this means we may have more or less pods than
	// the target nodeCount
	if len(s.podRevisions) != s.nodeCount {
		return false
	}

	numCorrectRevisionsFound := 0
	for i := 0; i < len(s.podRevisions); i++ {
		if s.podRevisions[i] == podRevision {
			numCorrectRevisionsFound++
		}
	}

	return numCorrectRevisionsFound == s.nodeCount
}

func (s *podsStatusState) haveUnschedulablePodsOrPodsWithBadStatusConditions() bool {
	return len(s.podAreUnschedulableOrHaveBadStatusConditions) > 0
}

func (s *podsStatusState) foundEvictedPodsOrPodsWithOrpahanedPVCs() bool {
	return len(s.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists) > 0
}

func (s *podsStatusState) remainingMinReadyWaitTime(pod corev1.Pod) time.Duration {
	var minReadySeconds = s.minReadySeconds
	var conditions []corev1.PodCondition

	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			conditions = append(conditions, condition)
		}
	}

	// We take the condition with the latest transition time among type PodReady conditions with Status true for ready pods.
	// Then we look at the condition with the latest transition time that is not for the pod that is a deletion candidate.
	// We then take the difference between the latest transition time and now and compare this to the MinReadySeconds setting.
	// This also means that if you quickly perform another rolling restart after another finished,
	// then you may initially wait for the minReadySeconds timer on the first pod.
	var latestTransitionTime = s.latestTransitionTime(conditions)
	if !latestTransitionTime.Time.IsZero() {
		var diff = time.Since(latestTransitionTime.Time).Milliseconds()
		var minRdy = (time.Second * time.Duration(minReadySeconds)).Milliseconds()
		if diff <= minRdy {
			remainingWaitTime := time.Second * time.Duration((minRdy-diff)/1000)
			return min(remainingWaitTime, MaximumMinReadyRequeue)
		}
	}
	return -1
}

func (s *podsStatusState) latestTransitionTime(conditions []corev1.PodCondition) metav1.Time {
	if len(conditions) == 0 {
		return metav1.NewTime(time.Time{})
	}
	var mostRecentTransitionTime = conditions[0].LastTransitionTime
	for idx, condition := range conditions {
		if condition.LastTransitionTime.Time.IsZero() {
			continue
		}
		if idx == 0 || condition.LastTransitionTime.Time.After(mostRecentTransitionTime.Time) {
			mostRecentTransitionTime = condition.LastTransitionTime
		}
	}
	return mostRecentTransitionTime
}