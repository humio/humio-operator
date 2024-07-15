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
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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

func ConstructService(hnp *HumioNodePool) *corev1.Service {
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
					Name:       "http",
					Port:       hnp.GetHumioServicePort(),
					TargetPort: intstr.IntOrString{IntVal: HumioPort},
				},
				{
					Name:       "es",
					Port:       hnp.GetHumioESServicePort(),
					TargetPort: intstr.IntOrString{IntVal: elasticPort},
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
			Labels:      mergeHumioServiceLabels(hc.Name, hc.Spec.HumioHeadlessServiceLabels),
			Annotations: humioHeadlessServiceAnnotationsOrDefault(hc),
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                "None",
			Type:                     corev1.ServiceTypeClusterIP,
			Selector:                 kubernetes.LabelsForHumio(hc.Name),
			PublishNotReadyAddresses: true,
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: HumioPort,
				},
				{
					Name: "es",
					Port: elasticPort,
				},
			},
		},
	}
}

func constructInternalService(hc *humiov1alpha1.HumioCluster) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      internalServiceName(hc.Name),
			Namespace: hc.Namespace,
			Labels:    kubernetes.LabelsForHumio(hc.Name),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: mergeHumioServiceLabels(hc.Name, map[string]string{
				kubernetes.FeatureLabelName: NodePoolFeatureAllowedAPIRequestType,
			}),
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: HumioPort,
				},
				{
					Name: "es",
					Port: elasticPort,
				},
			},
		},
	}
}

func headlessServiceName(clusterName string) string {
	return fmt.Sprintf("%s-headless", clusterName)
}

func internalServiceName(clusterName string) string {
	return fmt.Sprintf("%s-internal", clusterName)
}

func servicesMatch(existingService *corev1.Service, service *corev1.Service) (bool, error) {
	existingLabels := helpers.MapToSortedString(existingService.GetLabels())
	labels := helpers.MapToSortedString(service.GetLabels())
	if existingLabels != labels {
		return false, fmt.Errorf("service labels do not match: got %s, expected: %s", existingLabels, labels)
	}

	existingAnnotations := helpers.MapToSortedString(existingService.GetAnnotations())
	annotations := helpers.MapToSortedString(service.GetAnnotations())
	if existingAnnotations != annotations {
		return false, fmt.Errorf("service annotations do not match: got %s, expected: %s", existingAnnotations, annotations)
	}

	if existingService.Spec.PublishNotReadyAddresses != service.Spec.PublishNotReadyAddresses {
		return false, fmt.Errorf("service config for publishNotReadyAddresses isn't right: got %t, expected: %t",
			existingService.Spec.PublishNotReadyAddresses,
			service.Spec.PublishNotReadyAddresses)
	}

	existingSelector := helpers.MapToSortedString(existingService.Spec.Selector)
	selector := helpers.MapToSortedString(service.Spec.Selector)
	if existingSelector != selector {
		return false, fmt.Errorf("service selector does not match: got %s, expected: %s", existingSelector, selector)
	}
	return true, nil
}

func updateService(existingService *corev1.Service, service *corev1.Service) {
	existingService.Annotations = service.Annotations
	existingService.Labels = service.Labels
	existingService.Spec.Selector = service.Spec.Selector
	existingService.Spec.PublishNotReadyAddresses = service.Spec.PublishNotReadyAddresses
}
