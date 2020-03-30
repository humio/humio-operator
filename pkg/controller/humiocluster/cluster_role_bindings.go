package humiocluster

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *ReconcileHumioCluster) constructInitClusterRoleBinding(clusterRoleBindingName string, clusterRoleName string, hc *corev1alpha1.HumioCluster) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   clusterRoleBindingName,
			Labels: labelsForHumio(hc.Name),
		},
		RoleRef: rbacv1.RoleRef{
			Kind:     "ClusterRole",
			APIGroup: "rbac.authorization.k8s.io",
			Name:     clusterRoleName,
		},
		Subjects: []rbacv1.Subject{
			rbacv1.Subject{
				Kind:      "ServiceAccount",
				Name:      clusterRoleBindingName,
				Namespace: hc.Namespace,
			},
		},
	}
}

// GetClusterRoleBinding returns the given cluster role binding if it exists
func (r *ReconcileHumioCluster) GetClusterRoleBinding(context context.Context, clusterRoleBindingName string, hc *corev1alpha1.HumioCluster) (*rbacv1.ClusterRoleBinding, error) {
	var existingClusterRoleBinding rbacv1.ClusterRoleBinding
	err := r.client.Get(context, types.NamespacedName{
		Name: clusterRoleBindingName,
	}, &existingClusterRoleBinding)
	return &existingClusterRoleBinding, err
}
