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

var nodeNameToZoneName = map[string]string{}

func GetZoneForNodeName(ctx context.Context, c client.Client, nodeName string) (string, error) {
	zone, inCache := nodeNameToZoneName[nodeName]
	if inCache {
		return zone, nil
	}

	node, err := GetNode(ctx, c, nodeName)
	if err != nil {
		return "", nil
	}
	zone, found := node.Labels[corev1.LabelZoneFailureDomainStable]
	if !found {
		zone = node.Labels[corev1.LabelZoneFailureDomain]
	}

	nodeNameToZoneName[nodeName] = zone
	return zone, nil
}
