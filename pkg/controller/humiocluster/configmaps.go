package humiocluster

import (
	"context"

	"k8s.io/apimachinery/pkg/types"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *ReconcileHumioCluster) constructExtraKafkaConfigsConfigmap(extraKafkaConfigsConfigmapName string, extraKafkaConfigsConfigmapData string, hc *corev1alpha1.HumioCluster) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      extraKafkaConfigsConfigmapName,
			Namespace: hc.Namespace,
		},
		Data: map[string]string{extraKafkaPropertiesFilename: extraKafkaConfigsConfigmapData},
	}
}

// GetConfigmap returns the configmap for the given configmap name if it exists
func (r *ReconcileHumioCluster) GetConfigmap(context context.Context, hc *corev1alpha1.HumioCluster, configmapName string) (*corev1.ConfigMap, error) {
	var existingConfigmap corev1.ConfigMap
	err := r.client.Get(context, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      configmapName,
	}, &existingConfigmap)
	return &existingConfigmap, err
}
