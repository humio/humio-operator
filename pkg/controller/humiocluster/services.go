package humiocluster

import (
	humioClusterv1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func constructService(hc *humioClusterv1alpha1.HumioCluster) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hc.Name,
			Namespace: hc.Namespace,
			Labels:    kubernetes.LabelsForHumio(hc.Name),
		},
		Spec: corev1.ServiceSpec{
			Type:     humioServiceTypeOrDefault(hc),
			Selector: kubernetes.LabelsForHumio(hc.Name),
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: humioServicePortOrDefault(hc),
				},
				{
					Name: "es",
					Port: humioESServicePortOrDefault(hc),
				},
			},
		},
	}
}
