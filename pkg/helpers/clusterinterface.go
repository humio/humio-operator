/*
Copyright 2020 Humio.

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

package helpers

import (
	"context"
	"fmt"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"

	"github.com/humio/humio-operator/pkg/kubernetes"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ClusterInterface interface {
	Url(client.Client) (string, error)
	Name() string
}

type Cluster struct {
	managedClusterName  string
	externalClusterName string
	namespace           string
}

func NewCluster(managedClusterName, externalClusterName, namespace string) (ClusterInterface, error) {
	// Return error immediately if we do not have exactly one of the cluster names configured
	if managedClusterName != "" && externalClusterName != "" {
		return Cluster{}, fmt.Errorf("ingest token cannot have both ManagedClusterName and ExternalClusterName set at the same time")
	}
	if managedClusterName == "" && externalClusterName == "" {
		return Cluster{}, fmt.Errorf("ingest token must have one of ManagedClusterName and ExternalClusterName set")
	}
	return Cluster{
		externalClusterName: externalClusterName,
		managedClusterName:  managedClusterName,
		namespace:           namespace,
	}, nil
}

func (c Cluster) Url(k8sClient client.Client) (string, error) {
	if c.managedClusterName != "" {
		service := kubernetes.ConstructService(c.Name(), c.namespace)
		// TODO: do not hardcode port here
		return fmt.Sprintf("http://%s.%s:8080/", service.Name, service.Namespace), nil
	}

	// Fetch the HumioIngestToken instance
	var humioExternalCluster corev1alpha1.HumioExternalCluster
	err := k8sClient.Get(context.TODO(), types.NamespacedName{
		Namespace: c.namespace,
		Name:      c.externalClusterName,
	}, &humioExternalCluster)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return "", fmt.Errorf("could not find humio external cluster: %s", err)
		}
		// Error reading the object - requeue the request.
		return "", err
	}

	return humioExternalCluster.Spec.Url, nil
}

func (c Cluster) Name() string {
	if c.managedClusterName != "" {
		return c.managedClusterName
	}
	return c.externalClusterName
}
