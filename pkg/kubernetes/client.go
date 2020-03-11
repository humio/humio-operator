package kubernetes

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/common/log"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListPods grabs the list of all pods associated to a an instance of HumioCluster
func ListPods(c client.Client, hc *clusterv1alpha1.HumioCluster) ([]corev1.Pod, error) {
	var foundPodList corev1.PodList
	matchingLabels := client.MatchingLabels{
		"humio_cr": hc.Name,
	}
	// client.MatchingField also exists?

	err := c.List(context.TODO(), &foundPodList, client.InNamespace(hc.Namespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundPodList.Items, nil
}

// GetHumioBaseURL the first base URL for the first Humio node it can reach
func GetHumioBaseURL(c client.Client, hc *clusterv1alpha1.HumioCluster) (string, error) {
	allPodsForCluster, err := ListPods(c, hc)
	if err != nil {
		return "", fmt.Errorf("could not list pods for cluster: %v", err)
	}
	for _, p := range allPodsForCluster {
		if p.DeletionTimestamp == nil {
			// only consider pods not being deleted

			if p.Status.PodIP == "" {
				// skip pods with no pod IP
				continue
			}

			// check if we can reach the humio endpoint
			humioBaseURL := "http://" + p.Status.PodIP + ":8080/"
			resp, err := http.Get(humioBaseURL)
			if err != nil {
				log.Info(fmt.Sprintf("Humio API is unavailable: %v", err))
				continue
			}
			defer resp.Body.Close()

			// if request was ok, return the base URL
			if resp.StatusCode == http.StatusOK {
				return humioBaseURL, nil
			}
		}
	}
	return "", fmt.Errorf("did not find a valid base URL")
}

// ContainsNodePoolName returns true if any of the current node pools has the given name
func ContainsNodePoolName(poolName string, hc *clusterv1alpha1.HumioCluster) bool {
	for _, pool := range hc.Spec.NodePools {
		if pool.Name == poolName {
			return true
		}
	}
	return false
}
