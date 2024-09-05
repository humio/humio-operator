package controllers

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

const (
	bootstrapTokenSecretSuffix  = "bootstrap-token"
	bootstrapTokenPodNameSuffix = "bootstrap-token-onetime"
)

type HumioBootstrapTokenConfig struct {
	BootstrapToken      *humiov1alpha1.HumioBootstrapToken
	ManagedHumioCluster *humiov1alpha1.HumioCluster
}

func NewHumioBootstrapTokenConfig(bootstrapToken *humiov1alpha1.HumioBootstrapToken, managedHumioCluster *humiov1alpha1.HumioCluster) HumioBootstrapTokenConfig {
	return HumioBootstrapTokenConfig{BootstrapToken: bootstrapToken, ManagedHumioCluster: managedHumioCluster}
}

func (b *HumioBootstrapTokenConfig) bootstrapTokenName() string {
	return b.BootstrapToken.Name
}

func (b *HumioBootstrapTokenConfig) bootstrapTokenSecretName() string {
	if b.BootstrapToken.Spec.TokenSecret.SecretKeyRef != nil {
		return b.BootstrapToken.Spec.TokenSecret.SecretKeyRef.Name
	}
	return fmt.Sprintf("%s-%s", b.BootstrapToken.Name, bootstrapTokenSecretSuffix)
}

func (b *HumioBootstrapTokenConfig) create() (bool, error) {
	if err := b.validate(); err != nil {
		return false, err
	}
	if b.BootstrapToken.Spec.TokenSecret.SecretKeyRef == nil && b.BootstrapToken.Spec.HashedTokenSecret.SecretKeyRef == nil {
		return true, nil
	}
	return false, nil
}

func (b *HumioBootstrapTokenConfig) validate() error {
	if b.BootstrapToken.Spec.TokenSecret.SecretKeyRef == nil && b.BootstrapToken.Spec.HashedTokenSecret.SecretKeyRef == nil {
		return nil
	}
	if b.BootstrapToken.Spec.TokenSecret.SecretKeyRef != nil && b.BootstrapToken.Spec.HashedTokenSecret.SecretKeyRef != nil {
		return nil
	}
	return fmt.Errorf("must set both tokenSecret.secretKeyRef as well as hashedTokenSecret.secretKeyRef")
}

func (b *HumioBootstrapTokenConfig) image() string {
	if b.BootstrapToken.Spec.Image != "" {
		return b.BootstrapToken.Spec.Image
	}
	if b.ManagedHumioCluster.Spec.Image != "" {
		return b.ManagedHumioCluster.Spec.Image
	}
	if b.ManagedHumioCluster != nil {
		if len(b.ManagedHumioCluster.Spec.NodePools) > 0 {
			return b.ManagedHumioCluster.Spec.NodePools[0].Image
		}
	}
	return Image
}

func (b *HumioBootstrapTokenConfig) imagePullSecrets() []corev1.LocalObjectReference {
	if len(b.BootstrapToken.Spec.ImagePullSecrets) > 0 {
		return b.BootstrapToken.Spec.ImagePullSecrets
	}
	if len(b.ManagedHumioCluster.Spec.ImagePullSecrets) > 0 {
		return b.ManagedHumioCluster.Spec.ImagePullSecrets
	}
	if b.ManagedHumioCluster != nil {
		if len(b.ManagedHumioCluster.Spec.NodePools) > 0 {
			if len(b.ManagedHumioCluster.Spec.NodePools[0].ImagePullSecrets) > 0 {
				return b.ManagedHumioCluster.Spec.NodePools[0].ImagePullSecrets
			}
		}
	}
	return []corev1.LocalObjectReference{}
}

func (b *HumioBootstrapTokenConfig) resources() corev1.ResourceRequirements {
	if b.BootstrapToken.Spec.Resources != nil {
		return *b.BootstrapToken.Spec.Resources
	}
	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(500*1024*1024, resource.BinarySI),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    *resource.NewMilliQuantity(100, resource.DecimalSI),
			corev1.ResourceMemory: *resource.NewQuantity(50*1024*1024, resource.BinarySI),
		},
	}
}

func (b *HumioBootstrapTokenConfig) podName() string {
	return fmt.Sprintf("%s-%s", b.BootstrapToken.Name, bootstrapTokenPodNameSuffix)
}

func (b *HumioBootstrapTokenConfig) namespace() string {
	return b.BootstrapToken.Namespace
}
