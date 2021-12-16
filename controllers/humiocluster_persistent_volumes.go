/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	waitForPvcTimeoutSeconds = 30
)

func constructPersistentVolumeClaim(hnp *HumioNodePool) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("%s-core-%s", hnp.GetNodePoolName(), kubernetes.RandomString()),
			Namespace:   hnp.GetNamespace(),
			Labels:      hnp.GetNodePoolLabels(),
			Annotations: map[string]string{},
		},
		Spec: hnp.GetDataVolumePersistentVolumeClaimSpecTemplateRAW(),
	}
}

func findPvcForPod(pvcList []corev1.PersistentVolumeClaim, pod corev1.Pod) (corev1.PersistentVolumeClaim, error) {
	for _, pvc := range pvcList {
		for _, volume := range pod.Spec.Volumes {
			if volume.Name == "humio-data" {
				if volume.VolumeSource.PersistentVolumeClaim == nil {
					continue
				}
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
				if volume.PersistentVolumeClaim == nil {
					continue
				}
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

func (r *HumioClusterReconciler) waitForNewPvc(ctx context.Context, hnp *HumioNodePool, expectedPvc *corev1.PersistentVolumeClaim) error {
	for i := 0; i < waitForPvcTimeoutSeconds; i++ {
		r.Log.Info(fmt.Sprintf("validating new pvc was created. waiting for pvc with name %s", expectedPvc.Name))
		latestPvcList, err := kubernetes.ListPersistentVolumeClaims(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
		if err != nil {
			return fmt.Errorf("failed to list pvcs: %w", err)
		}
		for _, pvc := range latestPvcList {
			if pvc.Name == expectedPvc.Name {
				return nil
			}
		}
		time.Sleep(time.Second * 1)
	}
	return fmt.Errorf("timed out waiting to validate new pvc with name %s was created", expectedPvc.Name)
}
