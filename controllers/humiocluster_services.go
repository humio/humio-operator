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
	"fmt"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// humioServiceLabels generates the set of labels to attach to the humio kubernetes service
func mergeHumioServiceLabels(clusterName string, serviceLabels map[string]string) map[string]string {
	labels := kubernetes.LabelsForHumio(clusterName)
	for k, v := range serviceLabels {
		if _, ok := labels[k]; ok {
			continue
		}
		labels[k] = v
	}
	return labels
}

func constructService(hnp *HumioNodePool) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        hnp.GetNodePoolName(),
			Namespace:   hnp.GetNamespace(),
			Labels:      mergeHumioServiceLabels(hnp.GetClusterName(), hnp.GetHumioServiceLabels()),
			Annotations: hnp.GetHumioServiceAnnotations(),
		},
		Spec: corev1.ServiceSpec{
			Type:     hnp.GetServiceType(),
			Selector: hnp.GetNodePoolLabels(),
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: hnp.GetHumioServicePort(),
				},
				{
					Name: "es",
					Port: hnp.GetHumioESServicePort(),
				},
			},
		},
	}
}

func constructHeadlessService(hc *humiov1alpha1.HumioCluster) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        headlessServiceName(hc.Name),
			Namespace:   hc.Namespace,
			Labels:      mergeHumioServiceLabels(hc.GetClusterName(), hc.Spec.HumioHeadlessServiceLabels),
			Annotations: humioHeadlessServiceAnnotationsOrDefault(hc),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
			Type:      corev1.ServiceTypeClusterIP,
			Selector:  kubernetes.LabelsForHumio(hc.Name),
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: humioPort,
				},
				{
					Name: "es",
					Port: elasticPort,
				},
			},
		},
	}
}

func headlessServiceName(prefix string) string {
	return fmt.Sprintf("%s-headless", prefix)
}
