package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// PodHasher is an object that will create hashes when given a pod, and a second pod which contains only the fields which
// are managed and give the option to be excluded from the hash generation
type PodHasher struct {
	pod              *corev1.Pod
	managedFieldsPod *corev1.Pod
}

// NewPodHasher returns a new PodHasher
func NewPodHasher(pod *corev1.Pod, managedFieldsPod *corev1.Pod) *PodHasher {
	return &PodHasher{
		pod:              pod,
		managedFieldsPod: managedFieldsPod,
	}
}

// PodHashOnlyManagedFields creates a hash of the pod for only the fields which are managed
func (h *PodHasher) PodHashOnlyManagedFields() (string, error) {
	return h.podHasherOnlyManagedFields().calculateHash()
}

// PodHashMinusManagedFields creates a hash of the pod for only fields which are not managed
func (h *PodHasher) PodHashMinusManagedFields() (string, error) {
	return h.podHasherMinusManagedFields().calculateHash()
}

// podHasherMinusManagedFields returns a PodHasher using only the managed fields pod, which will cause the hash to only
// be evaluated for the managed fields
func (h *PodHasher) podHasherOnlyManagedFields() *PodHasher {
	return NewPodHasher(h.managedFieldsPod, nil)
}

// podHasherMinusManagedFields returns a PodHasher using a new pod that sanitizes the fields that are managed by
// the operator, tracked under the node pool. if new fields are managed by the operator, changes to this function will
// be required, along with changes to mergeContainers() in the controller defaults.
func (h *PodHasher) podHasherMinusManagedFields() *PodHasher {
	if h.managedFieldsPod == nil {
		return h
	}

	podExcludingManagedFields := h.pod.DeepCopy()
	for _, managedFieldsContainer := range h.managedFieldsPod.Spec.Containers {
		for idx, container := range podExcludingManagedFields.Spec.Containers {
			if container.Name == managedFieldsContainer.Name {
				if managedFieldsContainer.Image != "" {
					podExcludingManagedFields.Spec.Containers[idx].Image = ""
				}
				for _, managedEnvVar := range managedFieldsContainer.Env {
					for envVarIdx, envVar := range podExcludingManagedFields.Spec.Containers[idx].Env {
						if managedEnvVar.Name == envVar.Name {
							podExcludingManagedFields.Spec.Containers[idx].Env[envVarIdx].Value = ""
						}
					}
				}
			}
		}
	}

	for _, managedFieldsContainer := range h.managedFieldsPod.Spec.InitContainers {
		for idx, container := range podExcludingManagedFields.Spec.InitContainers {
			if container.Name == managedFieldsContainer.Name {
				if managedFieldsContainer.Image != "" {
					podExcludingManagedFields.Spec.InitContainers[idx].Image = ""
				}
				for _, managedEnvVar := range managedFieldsContainer.Env {
					for envVarIdx, envVar := range podExcludingManagedFields.Spec.InitContainers[idx].Env {
						if managedEnvVar.Name == envVar.Name {
							podExcludingManagedFields.Spec.InitContainers[idx].Env[envVarIdx].Value = ""
						}
					}
				}
			}
		}
	}
	return NewPodHasher(podExcludingManagedFields, nil)
}

func (h *PodHasher) calculateHash() (string, error) {
	if h.pod == nil {
		return "", fmt.Errorf("cannot calculate hash for nil pod")
	}
	podCopy := h.pod.DeepCopy()
	processedJSON, err := json.Marshal(podCopy.Spec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal processed map: %w", err)
	}

	hash := sha256.Sum256(processedJSON)
	return fmt.Sprintf("%x", hash), nil
}
