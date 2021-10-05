package controllers

import (
	"fmt"
	"strconv"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

const (
	containerStateCreating  = "ContainerCreating"
	containerStateCompleted = "Completed"
	podInitializing         = "PodInitializing"
)

type podsStatusState struct {
	expectedRunningPods     int
	readyCount              int
	notReadyCount           int
	podRevisions            []int
	podDeletionTimestampSet []bool
	podNames                []string
	podErrors               []corev1.Pod
}

func (r *HumioClusterReconciler) getPodsStatus(hc *humiov1alpha1.HumioCluster, foundPodList []corev1.Pod) (*podsStatusState, error) {
	status := podsStatusState{
		readyCount:          0,
		notReadyCount:       len(foundPodList),
		expectedRunningPods: nodeCountOrDefault(hc),
	}
	var podsReady, podsNotReady []string
	for _, pod := range foundPodList {
		podRevisionStr := pod.Annotations[podRevisionAnnotation]
		if podRevision, err := strconv.Atoi(podRevisionStr); err == nil {
			status.podRevisions = append(status.podRevisions, podRevision)
		} else {
			r.Log.Error(err, fmt.Sprintf("unable to identify pod revision for pod %s", pod.Name))
			return &status, err
		}
		status.podDeletionTimestampSet = append(status.podDeletionTimestampSet, pod.DeletionTimestamp != nil)
		status.podNames = append(status.podNames, pod.Name)

		// pods that were just deleted may still have a status of Ready, but we should not consider them ready
		if pod.DeletionTimestamp == nil {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady {
					if condition.Status == corev1.ConditionTrue {
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
	return (s.readyCount < s.expectedRunningPods || s.notReadyCount > 0) && !s.havePodsWithContainerStateWaitingErrors()
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

func (s *podsStatusState) allPodsReady() bool {
	return s.readyCount == s.expectedRunningPods
}

func (s *podsStatusState) haveMissingPods() bool {
	return s.readyCount < s.expectedRunningPods
}

func (s *podsStatusState) havePodsWithContainerStateWaitingErrors() bool {
	return len(s.podErrors) > 0
}
