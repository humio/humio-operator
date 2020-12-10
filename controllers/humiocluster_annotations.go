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
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

const (
	certHashAnnotation         = "humio.com/certificate-hash"
	podHashAnnotation          = "humio.com/pod-hash"
	podRevisionAnnotation      = "humio.com/pod-revision"
	podRestartPolicyAnnotation = "humio.com/pod-restart-policy"
	PodRestartPolicyRolling    = "rolling"
	PodRestartPolicyRecreate   = "recreate"
	pvcHashAnnotation          = "humio_pvc_hash"
)

func (r *HumioClusterReconciler) incrementHumioClusterPodRevision(ctx context.Context, hc *humiov1alpha1.HumioCluster, restartPolicy string) (int, error) {
	newRevision, err := r.getHumioClusterPodRevision(hc)
	if err != nil {
		return -1, err
	}
	newRevision++
	r.Log.Info(fmt.Sprintf("setting cluster pod revision to %d", newRevision))
	hc.Annotations[podRevisionAnnotation] = strconv.Itoa(newRevision)

	r.setRestartPolicy(hc, restartPolicy)

	err = r.Update(ctx, hc)
	if err != nil {
		return -1, fmt.Errorf("unable to set annotation %s on HumioCluster: %s", podRevisionAnnotation, err)
	}

	var hasNewRevision bool
	for i := 0; i < 30; i++ {
		r.getLatestHumioCluster(ctx, hc)
		if hc.Annotations[podRevisionAnnotation] == strconv.Itoa(newRevision) {
			hasNewRevision = true
			break
		}
		r.Log.Info("waiting for revision to be updated on the HumioCluster")
		time.Sleep(time.Second * 1)
	}
	if !hasNewRevision {
		return newRevision, fmt.Errorf("failed to verify the new revision has been added to the HumioCluster")
	}

	return newRevision, nil
}

func (r *HumioClusterReconciler) getHumioClusterPodRevision(hc *humiov1alpha1.HumioCluster) (int, error) {
	if hc.Annotations == nil {
		hc.Annotations = map[string]string{}
	}
	revision, ok := hc.Annotations[podRevisionAnnotation]
	if !ok {
		revision = "0"
	}
	existingRevision, err := strconv.Atoi(revision)
	if err != nil {
		return -1, fmt.Errorf("unable to read annotation %s on HumioCluster: %s", podRevisionAnnotation, err)
	}
	return existingRevision, nil
}

func (r *HumioClusterReconciler) getHumioClusterPodRestartPolicy(hc *humiov1alpha1.HumioCluster) string {
	if hc.Annotations == nil {
		hc.Annotations = map[string]string{}
	}
	existingPolicy, ok := hc.Annotations[podRestartPolicyAnnotation]
	if !ok {
		existingPolicy = PodRestartPolicyRecreate
	}
	return existingPolicy
}

func (r *HumioClusterReconciler) setPodRevision(pod *corev1.Pod, newRevision int) error {
	pod.Annotations[podRevisionAnnotation] = strconv.Itoa(newRevision)
	return nil
}

func (r *HumioClusterReconciler) setRestartPolicy(hc *humiov1alpha1.HumioCluster, policy string) {
	r.Log.Info(fmt.Sprintf("setting HumioCluster annotation %s to %s", podRestartPolicyAnnotation, policy))
	hc.Annotations[podRestartPolicyAnnotation] = policy
}
