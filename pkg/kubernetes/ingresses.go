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

	"k8s.io/apimachinery/pkg/types"

	v1beta1 "k8s.io/api/networking/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetIngress returns the ingress for the given ingress name if it exists
func GetIngress(ctx context.Context, c client.Client, ingressName, humioClusterNamespace string) (*v1beta1.Ingress, error) {
	var existingIngress v1beta1.Ingress
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      ingressName,
	}, &existingIngress)
	return &existingIngress, err
}

// ListIngresses grabs the list of all ingress objects associated to a an instance of HumioCluster
func ListIngresses(ctx context.Context, c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]v1beta1.Ingress, error) {
	var foundIngressList v1beta1.IngressList
	err := c.List(ctx, &foundIngressList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundIngressList.Items, nil
}
