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

package controllers

import (
	"context"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
)

func (r *HumioClusterReconciler) constructInitClusterRole(clusterRoleName string, hc *humiov1alpha1.HumioCluster) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   clusterRoleName,
			Labels: kubernetes.LabelsForHumio(hc.Name),
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
func (r *HumioClusterReconciler) GetClusterRole(ctx context.Context, clusterRoleName string, hc *humiov1alpha1.HumioCluster) (*rbacv1.ClusterRole, error) {
	var existingClusterRole rbacv1.ClusterRole
	err := r.Get(ctx, types.NamespacedName{
		Name: clusterRoleName,
	}, &existingClusterRole)
	return &existingClusterRole, err
}
