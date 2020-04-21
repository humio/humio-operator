package kubernetes

import (
	"context"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListPods grabs the list of all pods associated to a an instance of HumioCluster
func ListPods(c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]corev1.Pod, error) {
	var foundPodList corev1.PodList
	err := c.List(context.TODO(), &foundPodList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundPodList.Items, nil
}

func LabelsForPod(clusterName string, nodeID int) map[string]string {
	labels := LabelsForHumio(clusterName)
	labels["node_id"] = strconv.Itoa(nodeID)
	return labels
}
