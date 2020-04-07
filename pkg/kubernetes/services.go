package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ConstructService(humioClusterName, humioClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      humioClusterName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: LabelsForHumio(humioClusterName),
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 8080,
				},
				{
					Name: "es",
					Port: 9200,
				},
			},
		},
	}
}

func GetService(c client.Client, context context.Context, humioClusterName, humioClusterNamespace string) (*corev1.Service, error) {
	var existingService corev1.Service
	err := c.Get(context, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      humioClusterName,
	}, &existingService)
	return &existingService, err
}
