package kubernetes

import (
	"context"
	"fmt"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

const (
	BootstrapTokenNameSuffix                  = "bootstrap-token"
	BootstrapTokenSecretNameSuffix            = "bootstrap-token"
	BootstrapTokenManagedClusterNameLabelName = "managed-cluster-name"
)

// LabelsForHumioBootstrapToken returns a map of labels which contains a common set of labels and additional user-defined humio bootstrap token labels.
// In case of overlap between the common labels and user-defined labels, the user-defined label will be ignored.
func LabelsForHumioBootstrapToken(clusterName string) map[string]string {
	labels := LabelsForHumio(clusterName)
	labels[BootstrapTokenManagedClusterNameLabelName] = clusterName
	return labels
}

// ConstructHumioBootstrapToken returns a HumioBootstrapToken
func ConstructHumioBootstrapToken(humioClusterName string, humioClusterNamespace string) *humiov1alpha1.HumioBootstrapToken {
	return &humiov1alpha1.HumioBootstrapToken{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", humioClusterName, BootstrapTokenNameSuffix),
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumioBootstrapToken(humioClusterName),
		},
		Spec: humiov1alpha1.HumioBootstrapTokenSpec{
			ManagedClusterName: humioClusterName,
		},
	}
}

// ListHumioBootstrapTokens returns all HumioBootstrapTokens in a given namespace which matches the label selector
func ListHumioBootstrapTokens(ctx context.Context, c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]humiov1alpha1.HumioBootstrapToken, error) {
	var foundHumioBootstrapTokenList humiov1alpha1.HumioBootstrapTokenList
	err := c.List(ctx, &foundHumioBootstrapTokenList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	// If for some reason the HumioBootstrapToken is not labeled with the managed-cluster-name label, look at the spec
	if len(foundHumioBootstrapTokenList.Items) == 0 {
		if humioClusterName, ok := matchingLabels[BootstrapTokenManagedClusterNameLabelName]; ok {
			var allHumioBootstrapTokensList humiov1alpha1.HumioBootstrapTokenList
			err := c.List(ctx, &allHumioBootstrapTokensList, client.InNamespace(humioClusterNamespace))
			if err != nil {
				return nil, err
			}
			for _, hbt := range allHumioBootstrapTokensList.Items {
				if hbt.Spec.ManagedClusterName == humioClusterName {
					foundHumioBootstrapTokenList.Items = append(foundHumioBootstrapTokenList.Items, hbt)
				}
			}
		}
	}

	return foundHumioBootstrapTokenList.Items, nil
}

// GetHumioBootstrapToken retrieves the HumioBootstrapToken given the humio cluster name and namespace
func GetHumioBootstrapToken(ctx context.Context, c client.Client, humioClusterName string, humioClusterNamespace string) (humiov1alpha1.HumioBootstrapToken, error) {
	matchingLabels := LabelsForHumio(humioClusterName)
	humioBootstrapTokenList, err := ListHumioBootstrapTokens(ctx, c, humioClusterNamespace, matchingLabels)
	if err != nil {
		return humiov1alpha1.HumioBootstrapToken{}, err
	}

	if len(humioBootstrapTokenList) == 0 {
		return humiov1alpha1.HumioBootstrapToken{}, fmt.Errorf("no HumioBootstrapTokens found with name %s in namespace %s", humioClusterName, humioClusterNamespace)
	} else if len(humioBootstrapTokenList) > 1 {
		return humiov1alpha1.HumioBootstrapToken{}, fmt.Errorf("too many HumioBootstrapTokens found with name %s in namespace %s (count %d)", humioClusterName, humioClusterNamespace, len(humioBootstrapTokenList))
	}
	return humioBootstrapTokenList[0], nil
}
