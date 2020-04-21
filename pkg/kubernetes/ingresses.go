package kubernetes

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	v1beta1 "k8s.io/api/networking/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetIngress returns the ingress for the given ingress name if it exists
func GetIngress(ctx context.Context, c client.Client, ingressName, humioClusterNamespace string) (*v1beta1.Ingress, error) {
	var existingIngress v1beta1.Ingress
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      ingressName,
	}, &existingIngress)
	return &existingIngress, err
}

// ListPods grabs the list of all pods associated to a an instance of HumioCluster
func ListIngresses(c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]v1beta1.Ingress, error) {
	var foundIngressList v1beta1.IngressList
	err := c.List(context.TODO(), &foundIngressList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundIngressList.Items, nil
}
