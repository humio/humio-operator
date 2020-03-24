package kubernetes

import (
	"context"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListPods grabs the list of all pods associated to a an instance of HumioCluster
func ListPods(c client.Client, hc *corev1alpha1.HumioCluster) ([]corev1.Pod, error) {
	var foundPodList corev1.PodList
	matchingLabels := client.MatchingLabels{
		"app":      "humio",
		"humio_cr": hc.Name,
	}
	// client.MatchingField also exists?

	err := c.List(context.TODO(), &foundPodList, client.InNamespace(hc.Namespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundPodList.Items, nil
}
