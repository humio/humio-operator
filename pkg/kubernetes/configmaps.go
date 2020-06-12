package kubernetes

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

// GetConfigMap returns the configmap for the given configmap name if it exists
func GetConfigMap(ctx context.Context, c client.Client, configMapName, humioClusterNamespace string) (*corev1.ConfigMap, error) {
	var existingConfigMap corev1.ConfigMap
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      configMapName,
	}, &existingConfigMap)
	return &existingConfigMap, err
}
