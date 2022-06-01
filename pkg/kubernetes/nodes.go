package kubernetes

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetNode(ctx context.Context, c client.Client, nodeName string) (*corev1.Node, error) {
	var node corev1.Node
	err := c.Get(ctx, types.NamespacedName{
		Name: nodeName,
	}, &node)
	return &node, err
}
