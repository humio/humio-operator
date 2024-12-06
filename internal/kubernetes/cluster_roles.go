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

// ConstructInitClusterRole returns the cluster role used by the init container to obtain information about the
// Kubernetes worker node that the Humio cluster pod was scheduled on
func ConstructInitClusterRole(clusterRoleName string, labels map[string]string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   clusterRoleName,
			Labels: labels,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"nodes"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}

// GetClusterRole returns the given cluster role if it exists
func GetClusterRole(ctx context.Context, c client.Client, clusterRoleName string) (*rbacv1.ClusterRole, error) {
	var existingClusterRole rbacv1.ClusterRole
	err := c.Get(ctx, types.NamespacedName{
		Name: clusterRoleName,
	}, &existingClusterRole)
	return &existingClusterRole, err
}
