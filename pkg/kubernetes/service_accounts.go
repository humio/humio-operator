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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConstructServiceAccount constructs and returns a service account which can be used for the given cluster and which
// will contain the specified annotations on the service account
func ConstructServiceAccount(serviceAccountName, humioClusterNamespace string, serviceAccountAnnotations map[string]string, labels map[string]string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        serviceAccountName,
			Namespace:   humioClusterNamespace,
			Labels:      labels,
			Annotations: serviceAccountAnnotations,
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
