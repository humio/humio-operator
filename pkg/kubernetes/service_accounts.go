package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ConstructInitServiceAccount(serviceAccountName, humioClusterName, humioClusterNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
	}
}

// GetServiceAccount returns the service account
func GetServiceAccount(c client.Client, context context.Context, serviceAccountName, humioClusterNamespace string) (*corev1.ServiceAccount, error) {
	var existingServiceAccount corev1.ServiceAccount
	err := c.Get(context, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      serviceAccountName,
	}, &existingServiceAccount)
	return &existingServiceAccount, err
}
