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
func GetClusterRoleBinding(c client.Client, context context.Context, clusterRoleBindingName string) (*rbacv1.ClusterRoleBinding, error) {
	var existingClusterRoleBinding rbacv1.ClusterRoleBinding
	err := c.Get(context, types.NamespacedName{
		Name: clusterRoleBindingName,
	}, &existingClusterRoleBinding)
	return &existingClusterRoleBinding, err
}
