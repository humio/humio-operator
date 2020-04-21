package kubernetes

import (
	"context"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ConstructAuthRole(roleName, humioClusterName, humioClusterNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "delete"},
			},
		},
	}
}

// GetRole returns the given role if it exists
func GetRole(c client.Client, context context.Context, roleName, roleNamespace string) (*rbacv1.Role, error) {
	var existingRole rbacv1.Role
	err := c.Get(context, types.NamespacedName{
		Name:      roleName,
		Namespace: roleNamespace,
	}, &existingRole)
	return &existingRole, err
}
