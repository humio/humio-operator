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
	"context"
	"fmt"
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
	labels[NodeIdLabelName] = strconv.Itoa(nodeID)
	return labels
}

func GetContainerIndexByName(pod corev1.Pod, name string) (int, error) {
	for idx, container := range pod.Spec.Containers {
		if container.Name == name {
			return idx, nil
		}
	}
	return 0, fmt.Errorf("container with name %s not found", name)
}

func GetInitContainerIndexByName(pod corev1.Pod, name string) (int, error) {
	for idx, container := range pod.Spec.InitContainers {
		if container.Name == name {
			return idx, nil
		}
	}
	return 0, fmt.Errorf("initcontainer with name %s not found", name)
}
