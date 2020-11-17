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

// MatchingLabelsForSecret returns a MatchingLabels which can be passed on to the Kubernetes client to only return
// secrets related to a specific HumioCluster instance
func MatchingLabelsForSecret(clusterName, secretName string) client.MatchingLabels {
	var matchingLabels client.MatchingLabels
	matchingLabels = LabelsForSecret(clusterName, secretName)
	return matchingLabels
}

// ConstructSecret returns an opaque secret which holds the given data
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

// ConstructServiceAccountSecret returns a secret which holds the service account token for the given service account name
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

// ListSecrets returns all secrets in a given namespace which matches the label selector
func ListSecrets(ctx context.Context, c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]corev1.Secret, error) {
	var foundSecretList corev1.SecretList
	err := c.List(ctx, &foundSecretList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundSecretList.Items, nil
}

// GetSecret returns the given service if it exists
func GetSecret(ctx context.Context, c client.Client, secretName, humioClusterNamespace string) (*corev1.Secret, error) {
	var existingSecret corev1.Secret
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      secretName,
	}, &existingSecret)
	return &existingSecret, err
}
