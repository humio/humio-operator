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
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/humio/humio-operator/pkg/kubernetes"
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

func FindPvcForPod(pvcList []corev1.PersistentVolumeClaim, pod corev1.Pod) (corev1.PersistentVolumeClaim, error) {
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

func FindNextAvailablePvc(pvcList []corev1.PersistentVolumeClaim, podList []corev1.Pod, pvcClaimNamesInUse map[string]struct{}) (string, error) {
	if pvcClaimNamesInUse == nil {
		return "", fmt.Errorf("pvcClaimNamesInUse must not be nil")
	}
	// run through all pods and record PVC claim name for "humio-data" volume
	for _, pod := range podList {
		for _, volume := range pod.Spec.Volumes {
			if volume.Name == "humio-data" {
				if volume.PersistentVolumeClaim == nil {
					continue
				}
				pvcClaimNamesInUse[volume.PersistentVolumeClaim.ClaimName] = struct{}{}
			}
		}
	}
	sort.Slice(pvcList, func(i, j int) bool {
		if pvcList[i].Status.Phase == corev1.ClaimBound && pvcList[j].Status.Phase != corev1.ClaimBound {
			return true
		}
		if pvcList[i].Status.Phase != corev1.ClaimBound && pvcList[j].Status.Phase == corev1.ClaimBound {
			return false
		}
		return pvcList[i].Name < pvcList[j].Name
	})

	// return first PVC that is not used by any pods
	for _, pvc := range pvcList {
		if _, found := pvcClaimNamesInUse[pvc.Name]; !found {
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

func (r *HumioClusterReconciler) FilterSchedulablePVCs(ctx context.Context, persistentVolumeClaims []corev1.PersistentVolumeClaim) ([]corev1.PersistentVolumeClaim, error) {
	// Ensure the PVCs are bound to nodes that are actually schedulable in the case of local PVs
	schedulablePVCs := make([]corev1.PersistentVolumeClaim, 0)
	for _, pvc := range persistentVolumeClaims {
		if pvc.DeletionTimestamp != nil {
			continue
		}
		//Unbound PVCs are schedulable
		if pvc.Status.Phase == corev1.ClaimPending {
			schedulablePVCs = append(schedulablePVCs, pvc)
			continue
		}
		pv, err := kubernetes.GetPersistentVolume(ctx, r, pvc.Spec.VolumeName)
		if err != nil {
			return nil, r.logErrorAndReturn(err, fmt.Sprintf("failed to get persistent volume %s", pvc.Spec.VolumeName))
		}
		if pv.Spec.Local == nil {
			schedulablePVCs = append(schedulablePVCs, pvc)
			continue
		}
		nodeName := ""
		if pv.Spec.NodeAffinity != nil && len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms) > 0 &&
			len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions) > 0 &&
			len(pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values) > 0 {
			nodeName = pv.Spec.NodeAffinity.Required.NodeSelectorTerms[0].MatchExpressions[0].Values[0]
		}

		if nodeName == "" {
			return nil, fmt.Errorf("node name not found in PV spec")
		}

		node, err := kubernetes.GetNode(ctx, r, nodeName)
		if err != nil {
			return nil, r.logErrorAndReturn(err, fmt.Sprintf("failed to get node %s", nodeName))
		}
		if node.Spec.Unschedulable {
			r.Log.Info("PVC bound to unschedulable node skipping",
				"pvc", pvc.Name,
				"node", node.Name)
			continue
		}
		schedulablePVCs = append(schedulablePVCs, pvc)
	}
	return schedulablePVCs, nil
}
