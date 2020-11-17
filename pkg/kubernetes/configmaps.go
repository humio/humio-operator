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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConstructExtraKafkaConfigsConfigMap constructs the ConfigMap object used to store the file which is passed on to
// Humio using the configuration option EXTRA_KAFKA_CONFIGS_FILE
func ConstructExtraKafkaConfigsConfigMap(extraKafkaConfigsConfigMapName, extraKafkaPropertiesFilename, extraKafkaConfigsConfigMapData, humioClusterName, humioClusterNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      extraKafkaConfigsConfigMapName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
		Data: map[string]string{extraKafkaPropertiesFilename: extraKafkaConfigsConfigMapData},
	}
}

// ConstructViewGroupPermissionsConfigMap constructs a ConfigMap object used to store the file which Humio uses when
// enabling READ_GROUP_PERMISSIONS_FROM_FILE to control RBAC using a file rather than the Humio UI
func ConstructViewGroupPermissionsConfigMap(viewGroupPermissionsConfigMapName, viewGroupPermissionsFilename, viewGroupPermissionsConfigMapData, humioClusterName, humioClusterNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      viewGroupPermissionsConfigMapName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
		Data: map[string]string{viewGroupPermissionsFilename: viewGroupPermissionsConfigMapData},
	}
}

// GetConfigMap returns the configmap for the given configmap name if it exists
func GetConfigMap(ctx context.Context, c client.Client, configMapName, humioClusterNamespace string) (*corev1.ConfigMap, error) {
	var existingConfigMap corev1.ConfigMap
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      configMapName,
	}, &existingConfigMap)
	return &existingConfigMap, err
}
