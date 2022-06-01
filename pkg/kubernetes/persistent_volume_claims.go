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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ListPersistentVolumeClaims grabs the list of all persistent volume claims associated to a an instance of HumioCluster
func ListPersistentVolumeClaims(ctx context.Context, c client.Client, humioClusterNamespace string, matchingLabels client.MatchingLabels) ([]corev1.PersistentVolumeClaim, error) {
	var foundPersistentVolumeClaimList corev1.PersistentVolumeClaimList
	err := c.List(ctx, &foundPersistentVolumeClaimList, client.InNamespace(humioClusterNamespace), matchingLabels)
	if err != nil {
		return nil, err
	}

	return foundPersistentVolumeClaimList.Items, nil
}

func GetPersistentVolumeClaim(ctx context.Context, c client.Client, humioClusterNamespace string, persistentVolumeClaimName string) (*corev1.PersistentVolumeClaim, error) {
	var foundPersistentVolumeClaim corev1.PersistentVolumeClaim
	err := c.Get(ctx, types.NamespacedName{
		Name:      persistentVolumeClaimName,
		Namespace: humioClusterNamespace,
	}, &foundPersistentVolumeClaim)
	return &foundPersistentVolumeClaim, err
}
