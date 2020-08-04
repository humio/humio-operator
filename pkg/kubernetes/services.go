package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetService(ctx context.Context, c client.Client, humioClusterName, humioClusterNamespace string) (*corev1.Service, error) {
	var existingService corev1.Service
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      humioClusterName,
	}, &existingService)
	return &existingService, err
}
