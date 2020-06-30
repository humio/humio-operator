package kubernetes

import (
	"math/rand"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	NodeIdLabelName = "humio.com/node-id"
)

func LabelsForHumio(clusterName string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/instance":   clusterName,
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/name":       "humio",
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

func RandomString() string {
	rand.Seed(time.Now().UnixNano())
	chars := []rune("abcdefghijklmnopqrstuvwxyz")
	length := 6
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(chars[rand.Intn(len(chars))])
	}
	return b.String()
}
