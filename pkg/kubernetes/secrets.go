package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ServiceTokenSecretName = "admin-token"
)

func ConstructSecret(humioClusterName, humioClusterNamespace, secretName string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
		Data: data,
	}
}

func ConstructServiceAccountSecret(humioClusterName, humioClusterNamespace, secretName string, serviceAccountName string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Namespace:   humioClusterNamespace,
			Labels:      LabelsForHumio(humioClusterName),
			Annotations: map[string]string{"kubernetes.io/service-account.name": serviceAccountName},
		},
		Type: "kubernetes.io/service-account-token",
	}
}

func GetSecret(c client.Client, context context.Context, secretName, humioClusterNamespace string) (*corev1.Secret, error) {
	var existingSecret corev1.Secret
	err := c.Get(context, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      secretName,
	}, &existingSecret)
	return &existingSecret, err
}
