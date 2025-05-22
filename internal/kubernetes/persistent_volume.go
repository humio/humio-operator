package kubernetes

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetPersistentVolume(ctx context.Context, c client.Client, name string) (*corev1.PersistentVolume, error) {
	var foundPersistentVolume corev1.PersistentVolume
	err := c.Get(ctx, client.ObjectKey{Name: name}, &foundPersistentVolume)
	if err != nil {
		return nil, err
	}
	return &foundPersistentVolume, nil
}
