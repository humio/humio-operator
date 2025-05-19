package controller

import (
	"fmt"

	"github.com/humio/humio-operator/internal/controller/versions"
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
			if b.ManagedHumioCluster.Spec.NodePools[0].Image != "" {
				return b.ManagedHumioCluster.Spec.NodePools[0].Image
			}
		}
	}
	return versions.DefaultHumioImageVersion()
}

func (b *HumioBootstrapTokenConfig) imageSource() *humiov1alpha1.HumioImageSource {

	if b.ManagedHumioCluster.Spec.ImageSource != nil {
		return b.ManagedHumioCluster.Spec.ImageSource
	}
	if b.ManagedHumioCluster != nil {
		if len(b.ManagedHumioCluster.Spec.NodePools) > 0 {
			if b.ManagedHumioCluster.Spec.NodePools[0].ImageSource != nil {
				return b.ManagedHumioCluster.Spec.NodePools[0].ImageSource
			}
		}
	}
	return nil
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

func (b *HumioBootstrapTokenConfig) affinity() *corev1.Affinity {
	if b.BootstrapToken.Spec.Affinity != nil {
		return b.BootstrapToken.Spec.Affinity
	}
	humioNodePools := getHumioNodePoolManagers(b.ManagedHumioCluster)
	for idx := range humioNodePools.Items {
		if humioNodePools.Items[idx].GetNodeCount() > 0 {
			pod, err := ConstructPod(humioNodePools.Items[idx], "", &podAttachments{})
			if err == nil {
				return pod.Spec.Affinity
			}
		}
	}
	return nil
}

func (b *HumioBootstrapTokenConfig) tolerations() []corev1.Toleration {
	if b.BootstrapToken.Spec.Tolerations != nil {
		return *b.BootstrapToken.Spec.Tolerations
	}
	humioNodePools := getHumioNodePoolManagers(b.ManagedHumioCluster)
	for idx := range humioNodePools.Items {
		if humioNodePools.Items[idx].GetNodeCount() > 0 {
			pod, err := ConstructPod(humioNodePools.Items[idx], "", &podAttachments{})
			if err == nil {
				return pod.Spec.Tolerations
			}
		}
	}
	return []corev1.Toleration{}
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

func (b *HumioBootstrapTokenConfig) PodName() string {
	return fmt.Sprintf("%s-%s", b.BootstrapToken.Name, bootstrapTokenPodNameSuffix)
}

func (b *HumioBootstrapTokenConfig) Namespace() string {
	return b.BootstrapToken.Namespace
}
