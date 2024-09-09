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
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

const (
	certHashAnnotation         = "humio.com/certificate-hash"
	PodHashAnnotation          = "humio.com/pod-hash"
	PodRevisionAnnotation      = "humio.com/pod-revision"
	envVarSourceHashAnnotation = "humio.com/env-var-source-hash"
	pvcHashAnnotation          = "humio_pvc_hash"
	// #nosec G101
	bootstrapTokenHashAnnotation = "humio.com/bootstrap-token-hash"
)

func (r *HumioClusterReconciler) setPodRevision(pod *corev1.Pod, newRevision int) {
	pod.Annotations[PodRevisionAnnotation] = strconv.Itoa(newRevision)
}
