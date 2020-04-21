package kubernetes

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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
func GetRoleBinding(c client.Client, context context.Context, roleBindingName, roleBindingNamespace string) (*rbacv1.RoleBinding, error) {
	var existingRoleBinding rbacv1.RoleBinding
	err := c.Get(context, types.NamespacedName{
		Name:      roleBindingName,
		Namespace: roleBindingNamespace,
	}, &existingRoleBinding)
	return &existingRoleBinding, err
}
