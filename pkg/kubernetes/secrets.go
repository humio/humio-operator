package kubernetes

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ServiceTokenSecretNameSuffix = "admin-token"
	SecretNameLabelName          = "humio.com/secret-identifier"
)

func LabelsForSecret(clusterName string, secretName string) map[string]string {
	labels := LabelsForHumio(clusterName)
	labels[SecretNameLabelName] = secretName
	return labels
}

func MatchingLabelsForSecret(clusterName, secretName string) client.MatchingLabels {
	var matchingLabels client.MatchingLabels
	matchingLabels = LabelsForSecret(clusterName, secretName)
	return matchingLabels
}

func ConstructSecret(humioClusterName, humioClusterNamespace, secretName string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForSecret(humioClusterName, secretName),
		},
		Data: data,
	}
}

func ConstructServiceAccountSecret(humioClusterName, humioClusterNamespace, secretName string, serviceAccountName string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("%s-%s", secretName, RandomString()),
			Namespace:   humioClusterNamespace,
			Labels:      LabelsForSecret(humioClusterName, secretName),
			Annotations: map[string]string{"kubernetes.io/service-account.name": serviceAccountName},
		},
		Type: "kubernetes.io/service-account-token",
	}
}

func ListSecrets(ctx context.Context, c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]corev1.Secret, error) {
	var foundSecretList corev1.SecretList
	err := c.List(ctx, &foundSecretList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundSecretList.Items, nil
}

func GetSecret(ctx context.Context, c client.Client, secretName, humioClusterNamespace string) (*corev1.Secret, error) {
	var existingSecret corev1.Secret
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      secretName,
	}, &existingSecret)
	return &existingSecret, err
}
