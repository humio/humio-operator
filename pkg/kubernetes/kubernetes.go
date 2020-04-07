package kubernetes

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func LabelsForHumio(clusterName string) map[string]string {
	labels := map[string]string{
		"app":      "humio",
		"humio_cr": clusterName,
	}
	return labels
}

func MatchingLabelsForHumio(clusterName string) client.MatchingLabels {
	var matchingLabels client.MatchingLabels
	matchingLabels = LabelsForHumio(clusterName)
	return matchingLabels
}

func LabelListContainsLabel(labelList map[string]string, label string) bool {
	for labelName := range labelList {
		if labelName == label {
			return true
		}
	}
	return false
}
