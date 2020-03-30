package humiocluster

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *ReconcileHumioCluster) constructInitClusterRole(clusterRoleName string, hc *corev1alpha1.HumioCluster) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   clusterRoleName,
			Labels: labelsForHumio(hc.Name),
		},
		Rules: []rbacv1.PolicyRule{
			rbacv1.PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"nodes"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
}

// GetClusterRole returns the given cluster role if it exists
func (r *ReconcileHumioCluster) GetClusterRole(context context.Context, clusterRoleName string, hc *corev1alpha1.HumioCluster) (*rbacv1.ClusterRole, error) {
	var existingClusterRole rbacv1.ClusterRole
	err := r.client.Get(context, types.NamespacedName{
		Name: clusterRoleName,
	}, &existingClusterRole)
	return &existingClusterRole, err
}
