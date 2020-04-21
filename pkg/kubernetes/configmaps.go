package kubernetes

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ConstructExtraKafkaConfigsConfigmap(extraKafkaConfigsConfigmapName, extraKafkaPropertiesFilename, extraKafkaConfigsConfigmapData, humioClusterName, humioClusterNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      extraKafkaConfigsConfigmapName,
			Namespace: humioClusterNamespace,
			Labels:    LabelsForHumio(humioClusterName),
		},
		Data: map[string]string{extraKafkaPropertiesFilename: extraKafkaConfigsConfigmapData},
	}
}

// GetConfigmap returns the configmap for the given configmap name if it exists
func GetConfigmap(ctx context.Context, c client.Client, configmapName, humioClusterNamespace string) (*corev1.ConfigMap, error) {
	var existingConfigmap corev1.ConfigMap
	err := c.Get(ctx, types.NamespacedName{
		Namespace: humioClusterNamespace,
		Name:      configmapName,
	}, &existingConfigmap)
	return &existingConfigmap, err
}
