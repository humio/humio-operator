package kubernetes

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ConstructInitClusterRole(clusterRoleName, humioClusterName string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   clusterRoleName,
			Labels: LabelsForHumio(humioClusterName),
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
func GetClusterRole(c client.Client, context context.Context, clusterRoleName string) (*rbacv1.ClusterRole, error) {
	var existingClusterRole rbacv1.ClusterRole
	err := c.Get(context, types.NamespacedName{
		Name: clusterRoleName,
	}, &existingClusterRole)
	return &existingClusterRole, err
}
