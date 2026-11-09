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

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DependencyCheckManagedClusterNameLabelName = "managed-cluster-name"
	DependencyCheckTypeLabelName               = "dependency-check-type"
)

// LabelsForHumioDependencyCheck returns labels for selecting HumioDependencyCheck resources owned by a cluster.
func LabelsForHumioDependencyCheck(clusterName string) map[string]string {
	labels := LabelsForHumio(clusterName)
	labels[DependencyCheckManagedClusterNameLabelName] = clusterName
	return labels
}

// DependencyCheckName generates the name for a HumioDependencyCheck CR.
// Format: {clusterName}-dep-{checkType} or {clusterName}-{poolName}-dep-{checkType} for named pools.
func DependencyCheckName(clusterName, poolName, checkType string) string {
	if poolName == "" {
		return fmt.Sprintf("%s-dep-%s", clusterName, checkType)
	}
	return fmt.Sprintf("%s-%s-dep-%s", clusterName, poolName, checkType)
}

// ConstructHumioDependencyCheck returns a new HumioDependencyCheck resource.
func ConstructHumioDependencyCheck(clusterName, namespace, nodePoolName, checkType string) *humiov1alpha1.HumioDependencyCheck {
	labels := LabelsForHumioDependencyCheck(clusterName)
	labels[DependencyCheckTypeLabelName] = checkType

	return &humiov1alpha1.HumioDependencyCheck{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DependencyCheckName(clusterName, nodePoolName, checkType),
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: humiov1alpha1.HumioDependencyCheckSpec{
			ManagedClusterName: clusterName,
			CheckType:          checkType,
			NodePoolName:       nodePoolName,
		},
	}
}

// ListHumioDependencyChecks returns all HumioDependencyChecks in a given namespace matching the label selector.
func ListHumioDependencyChecks(ctx context.Context, c client.Client, namespace string, matchingLabels client.MatchingLabels) ([]humiov1alpha1.HumioDependencyCheck, error) {
	var list humiov1alpha1.HumioDependencyCheckList
	err := c.List(ctx, &list, client.InNamespace(namespace), matchingLabels)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}
