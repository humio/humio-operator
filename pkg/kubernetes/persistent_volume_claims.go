package kubernetes

import (
	"context"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListPersistentVolumeClaims grabs the list of all persistent volume claims associated to a an instance of HumioCluster
func ListPersistentVolumeClaims(c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]corev1.PersistentVolumeClaim, error) {
	var foundPersistentVolumeClaimList corev1.PersistentVolumeClaimList
	err := c.List(context.TODO(), &foundPersistentVolumeClaimList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundPersistentVolumeClaimList.Items, nil
}

func LabelsForPersistentVolume(clusterName string, nodeID int) map[string]string {
	labels := LabelsForHumio(clusterName)
	labels[NodeIdLabelName] = strconv.Itoa(nodeID)
	return labels
}
