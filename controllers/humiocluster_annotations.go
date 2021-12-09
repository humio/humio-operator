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

	"k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/client-go/util/retry"

	corev1 "k8s.io/api/core/v1"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

const (
	certHashAnnotation         = "humio.com/certificate-hash"
	podHashAnnotation          = "humio.com/pod-hash"
	podRevisionAnnotation      = "humio.com/pod-revision"
	envVarSourceHashAnnotation = "humio.com/env-var-source-hash"
	podRestartPolicyAnnotation = "humio.com/pod-restart-policy"
	PodRestartPolicyRolling    = "rolling"
	PodRestartPolicyRecreate   = "recreate"
	pvcHashAnnotation          = "humio_pvc_hash"
)

func (r *HumioClusterReconciler) incrementHumioClusterPodRevision(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, restartPolicy string) (int, error) {
	revisionKey, revisionValue := hnp.GetHumioClusterNodePoolRevisionAnnotation()
	revisionValue++
	r.Log.Info(fmt.Sprintf("setting cluster pod revision %s=%d", revisionKey, revisionValue))
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.getLatestHumioCluster(ctx, hc)
		if err != nil {
			if !errors.IsNotFound(err) {
				return err
			}
		}
		hc.Annotations[revisionKey] = strconv.Itoa(revisionValue)
		r.setRestartPolicy(hc, restartPolicy)
		return r.Update(ctx, hc)
	})
	if err != nil {
		return -1, fmt.Errorf("unable to set annotation %s on HumioCluster: %s", revisionKey, err)
	}
	return revisionValue, nil
}

func (r *HumioClusterReconciler) setPodRevision(pod *corev1.Pod, newRevision int) {
	pod.Annotations[podRevisionAnnotation] = strconv.Itoa(newRevision)
}

func (r *HumioClusterReconciler) setRestartPolicy(hc *humiov1alpha1.HumioCluster, policy string) {
	r.Log.Info(fmt.Sprintf("setting HumioCluster annotation %s to %s", podRestartPolicyAnnotation, policy))
	hc.Annotations[podRestartPolicyAnnotation] = policy
}
