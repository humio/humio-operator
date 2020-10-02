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

	"k8s.io/apimachinery/pkg/types"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ConstructClusterRoleBinding(clusterRoleBindingName, clusterRoleName, humioClusterName, humioClusterNamespace, serviceAccountName string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   clusterRoleBindingName,
			Labels: LabelsForHumio(humioClusterName),
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "ClusterRole",
			APIGroup: "rbac.authorization.k8s.io",
			Name:     clusterRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: humioClusterNamespace,
			},
		},
	}
}

// GetClusterRoleBinding returns the given cluster role binding if it exists
func GetClusterRoleBinding(ctx context.Context, c client.Client, clusterRoleBindingName string) (*rbacv1.ClusterRoleBinding, error) {
	var existingClusterRoleBinding rbacv1.ClusterRoleBinding
	err := c.Get(ctx, types.NamespacedName{
		Name: clusterRoleBindingName,
	}, &existingClusterRoleBinding)
	return &existingClusterRoleBinding, err
}
