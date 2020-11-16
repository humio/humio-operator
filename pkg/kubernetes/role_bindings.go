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

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConstructRoleBinding constructs a role binding which binds the given serviceAccountName to the role passed in
func ConstructRoleBinding(roleBindingName, roleName, humioClusterName, humioClusterNamespace, serviceAccountName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "Role",
			APIGroup: "rbac.authorization.k8s.io",
			Name:     roleName,
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

// GetRoleBinding returns the given role if it exists
func GetRoleBinding(ctx context.Context, c client.Client, roleBindingName, roleBindingNamespace string) (*rbacv1.RoleBinding, error) {
	var existingRoleBinding rbacv1.RoleBinding
	err := c.Get(ctx, types.NamespacedName{
		Name:      roleBindingName,
		Namespace: roleBindingNamespace,
	}, &existingRoleBinding)
	return &existingRoleBinding, err
}
