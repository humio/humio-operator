package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ConstructServiceAccount(serviceAccountName, humioClusterName, humioClusterNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
	}
}

// GetServiceAccount returns the service account
func GetServiceAccount(ctx context.Context, c client.Client, serviceAccountName, humioClusterNamespace string) (*corev1.ServiceAccount, error) {
	var existingServiceAccount corev1.ServiceAccount
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      serviceAccountName,
	}, &existingServiceAccount)
	return &existingServiceAccount, err
}
