package humiocluster

import (
	"context"
	"fmt"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *ReconcileHumioCluster) constructService(hc *corev1alpha1.HumioCluster) (*corev1.Service, error) {
	var service corev1.Service
	service = corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hc.Name,
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labelsForHumio(hc.Name),
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
	if err := controllerutil.SetControllerReference(hc, &service, r.scheme); err != nil {
		return &corev1.Service{}, fmt.Errorf("could not set controller reference: %s", err)
	}
	return &service, nil
}

func (r *ReconcileHumioCluster) GetService(context context.Context, hc *corev1alpha1.HumioCluster) (*corev1.Service, error) {
	var existingService corev1.Service
	err := r.client.Get(context, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, &existingService)
	return &existingService, err
}
