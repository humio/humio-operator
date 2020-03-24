package humiocluster

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func labelsForHumio(clusterName string) map[string]string {
	labels := map[string]string{
		"app":      "humio",
		"humio_cr": clusterName,
	}
	return labels
}

func matchingLabelsForHumio(clusterName string) client.MatchingLabels {
	var matchingLabels client.MatchingLabels
	matchingLabels = labelsForHumio(clusterName)
	return matchingLabels
}
