package controllers

import (
	"fmt"
	"strconv"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"

	"github.com/humio/humio-operator/pkg/kubernetes"

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
	expectedRunningPods     int
	readyCount              int
	notReadyCount           int
	podRevisions            []int
	podImageVersions        []string
	podDeletionTimestampSet []bool
	podNames                []string
	podErrors               []corev1.Pod
	podsRequiringDeletion   []corev1.Pod
	podsReady               []corev1.Pod
}

func (r *HumioClusterReconciler) getPodsStatus(hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, foundPodList []corev1.Pod) (*podsStatusState, error) {
	status := podsStatusState{
		readyCount:          0,
		notReadyCount:       len(foundPodList),
		expectedRunningPods: hnp.GetNodeCount(),
	}
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
			// to the podsRequiringDeletion list will cause it to be deleted.
			if pod.Status.Phase == corev1.PodFailed && pod.Status.Reason == podConditionReasonEvicted {
				r.Log.Info(fmt.Sprintf("pod %s has errors, pod phase: %s, reason: %s", pod.Name, pod.Status.Phase, pod.Status.Reason))
				status.podsRequiringDeletion = append(status.podsRequiringDeletion, pod)
				continue
			}
			if pod.Status.Phase == corev1.PodPending {
				deletePod, err := r.isPodAttachedToOrphanedPvc(hc, hnp, pod)
				if !deletePod && err != nil {
					r.logErrorAndReturn(err, "unable to determine whether pod should be deleted")
				}
				if deletePod && hnp.OkToDeletePvc() {
					status.podsRequiringDeletion = append(status.podsRequiringDeletion, pod)
				}
			}
			// If a pod is Pending but unschedulable, we want to consider this an error state so it will be replaced
			// but only if the pod spec is updated (e.g. to lower the pod resources).
			for _, condition := range pod.Status.Conditions {
				if condition.Status == corev1.ConditionFalse {
					if condition.Reason == PodConditionReasonUnschedulable {
						r.Log.Info(fmt.Sprintf("pod %s has errors, container status: %s, reason: %s", pod.Name, condition.Status, condition.Reason))
						status.podErrors = append(status.podErrors, pod)
						continue
					}
				}
				if condition.Type == corev1.PodReady {
					if condition.Status == corev1.ConditionTrue {
						status.podsReady = append(status.podsReady, pod)
						podsReady = append(podsReady, pod.Name)
						status.readyCount++
						status.notReadyCount--
					} else {
						podsNotReady = append(podsNotReady, pod.Name)
						for _, containerStatus := range pod.Status.ContainerStatuses {
							if containerStatus.State.Waiting != nil && containerStatus.State.Waiting.Reason != containerStateCreating && containerStatus.State.Waiting.Reason != podInitializing {
								r.Log.Info(fmt.Sprintf("pod %s has errors, container state: Waiting, reason: %s", pod.Name, containerStatus.State.Waiting.Reason))
								status.podErrors = append(status.podErrors, pod)
							}
							if containerStatus.State.Terminated != nil && containerStatus.State.Terminated.Reason != containerStateCompleted {
								r.Log.Info(fmt.Sprintf("pod %s has errors, container state: Terminated, reason: %s", pod.Name, containerStatus.State.Terminated.Reason))
								status.podErrors = append(status.podErrors, pod)
							}
						}
					}
				}
			}
		}
	}
	r.Log.Info(fmt.Sprintf("pod status readyCount=%d notReadyCount=%d podsReady=%s podsNotReady=%s", status.readyCount, status.notReadyCount, podsReady, podsNotReady))
	// collect ready pods and not ready pods in separate lists and just print the lists here instead of a log entry per host
	return &status, nil
}

// waitingOnPods returns true when there are pods running that are not in a ready state. This does not include pods
// that are not ready due to container errors.
func (s *podsStatusState) waitingOnPods() bool {
	return (s.readyCount < s.expectedRunningPods || s.notReadyCount > 0) && !s.havePodsWithErrors() && !s.havePodsRequiringDeletion()
}

func (s *podsStatusState) podRevisionsInSync() bool {
	if len(s.podRevisions) < s.expectedRunningPods {
		return false
	}
	if s.expectedRunningPods == 1 {
		return true
	}
	revision := s.podRevisions[0]
	for i := 1; i < len(s.podRevisions); i++ {
		if s.podRevisions[i] != revision {
			return false
		}
	}
	return true
}

func (s *podsStatusState) havePodsWithErrors() bool {
	return len(s.podErrors) > 0
}

func (s *podsStatusState) havePodsRequiringDeletion() bool {
	return len(s.podsRequiringDeletion) > 0
}
