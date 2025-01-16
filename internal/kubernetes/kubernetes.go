/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kubernetes

import (
	"math/rand"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	NodePoolLabelName        = "humio.com/node-pool"
	FeatureLabelName         = "humio.com/feature"
	PodMarkedForDataEviction = "humio.com/marked-for-data-eviction"
	LogScaleClusterVhost     = "humio.com/cluster-vhost"
)

// LabelsForHumio returns the set of common labels for Humio resources.
// NB: There is a copy of this function in images/helper/main.go to work around helper depending on main project.
func LabelsForHumio(clusterName string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/instance":   clusterName,
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/name":       "humio",
	}
	return labels
}

// MatchingLabelsForHumio returns a MatchingLabels which can be passed on to the Kubernetes client to only return
// objects related to a specific HumioCluster instance
func MatchingLabelsForHumio(clusterName string) client.MatchingLabels {
	return LabelsForHumio(clusterName)
}

// RandomString returns a string of fixed length. The random strings are valid to use in Kubernetes object names.
func RandomString() string {
	chars := []rune("abcdefghijklmnopqrstuvwxyz")
	length := 6
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(chars[rand.Intn(len(chars))]) // #nosec G404
	}
	return b.String()
}

// AnnotationsForHumio returns the set of annotations for humio pods
func AnnotationsForHumio(podAnnotations map[string]string, productVersion string) map[string]string {
	annotations := map[string]string{
		"productID":      "none",
		"productName":    "humio",
		"productVersion": productVersion,
	}
	if len(podAnnotations) == 0 {
		return annotations
	}
	for k, v := range podAnnotations {
		if _, ok := annotations[k]; ok {
			// TODO: Maybe log out here, if the user specifies annotations already existing?
			continue
		}
		annotations[k] = v
	}
	return annotations
}
