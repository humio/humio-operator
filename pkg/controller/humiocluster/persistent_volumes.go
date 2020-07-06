package humiocluster

import (
	"fmt"
	"reflect"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func constructPersistentVolumeClaim(hc *corev1alpha1.HumioCluster) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("%s-core-%s", hc.Name, kubernetes.RandomString()),
			Namespace:   hc.Namespace,
			Labels:      kubernetes.LabelsForHumio(hc.Name),
			Annotations: map[string]string{},
		},
		Spec: hc.Spec.DataVolumePersistentVolumeClaimSpecTemplate,
	}
}

func findPvcForPod(pvcList []corev1.PersistentVolumeClaim, pod corev1.Pod) (corev1.PersistentVolumeClaim, error) {
	for _, pvc := range pvcList {
		for _, volume := range pod.Spec.Volumes {
			if volume.Name == "humio-data" {
				if volume.VolumeSource.PersistentVolumeClaim.ClaimName == pvc.Name {
					return pvc, nil
				}
			}
		}
	}

	return corev1.PersistentVolumeClaim{}, fmt.Errorf("could not find a pvc for pod %s", pod.Name)
}

func findNextAvailablePvc(pvcList []corev1.PersistentVolumeClaim, podList []corev1.Pod) (string, error) {
	pvcLookup := make(map[string]struct{})
	for _, pod := range podList {
		for _, volume := range pod.Spec.Volumes {
			if volume.Name == "humio-data" {
				pvcLookup[volume.PersistentVolumeClaim.ClaimName] = struct{}{}
			}
		}
	}

	for _, pvc := range pvcList {
		if _, found := pvcLookup[pvc.Name]; !found {
			return pvc.Name, nil
		}
	}

	return "", fmt.Errorf("no available pvcs")
}

func pvcsEnabled(hc *corev1alpha1.HumioCluster) bool {
	emptyPersistentVolumeClaimSpec := corev1.PersistentVolumeClaimSpec{}
	return !reflect.DeepEqual(hc.Spec.DataVolumePersistentVolumeClaimSpecTemplate, emptyPersistentVolumeClaimSpec)
}
