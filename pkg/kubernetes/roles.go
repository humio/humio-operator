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

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConstructAuthRole returns the role used by the auth sidecar container to make an API token available for the
// humio-operator. This API token can be used to obtain insights into the health of the Humio cluster and make changes.
func ConstructAuthRole(roleName, humioClusterName, humioClusterNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				Verbs:         []string{"get", "list", "watch", "create", "update", "delete"},
				ResourceNames: []string{fmt.Sprintf("%s-%s", humioClusterName, ServiceTokenSecretNameSuffix)},
			},
		},
	}
}

// GetRole returns the given role if it exists
func GetRole(ctx context.Context, c client.Client, roleName, roleNamespace string) (*rbacv1.Role, error) {
	var existingRole rbacv1.Role
	err := c.Get(ctx, types.NamespacedName{
		Name:      roleName,
		Namespace: roleNamespace,
	}, &existingRole)
	return &existingRole, err
}
