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

func (r *ReconcileHumioCluster) constructInitServiceAccount(serviceAccountName string, hc *corev1alpha1.HumioCluster) (*corev1.ServiceAccount, error) {
	serviceAccount := corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name),
		},
	}
	if err := controllerutil.SetControllerReference(hc, &serviceAccount, r.scheme); err != nil {
		return &corev1.ServiceAccount{}, fmt.Errorf("could not set controller reference: %s", err)
	}
	return &serviceAccount, nil
}

// GetServiceAccount returns the service account
func (r *ReconcileHumioCluster) GetServiceAccount(context context.Context, serviceAccountName string, hc *corev1alpha1.HumioCluster) (*corev1.ServiceAccount, error) {
	var existingServiceAccount corev1.ServiceAccount
	err := r.client.Get(context, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      serviceAccountName,
	}, &existingServiceAccount)
	return &existingServiceAccount, err
}
