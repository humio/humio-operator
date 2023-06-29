package controllers

import (
	"fmt"

	"github.com/humio/humio-operator/api/v1alpha1"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

const (
	BootstrapTokenSuffix       = "bootstrap-token"
	HashedBootstrapTokenSuffix = "hashed-bootstrap-token"
)

type HumioBootstrapTokenConfig struct {
	BootstrapToken      *v1alpha1.HumioBootstrapToken
	ManagedHumioCluster *v1alpha1.HumioCluster
}

func NewHumioBootstrapTokenConfig(bootstrapToken *humiov1alpha1.HumioBootstrapToken, managedHumioCluster *v1alpha1.HumioCluster) HumioBootstrapTokenConfig {
	return HumioBootstrapTokenConfig{BootstrapToken: bootstrapToken, ManagedHumioCluster: managedHumioCluster}
}

func (b *HumioBootstrapTokenConfig) bootstrapTokenName() string {
	if b.BootstrapToken.Spec.TokenSecret.SecretKeyRef != nil {
		return b.BootstrapToken.Spec.TokenSecret.SecretKeyRef.Name
	}
	return fmt.Sprintf("%s-%s", b.BootstrapToken.Name, BootstrapTokenSuffix)
}

func (b *HumioBootstrapTokenConfig) hashedBootstrapTokenName() string {
	if b.BootstrapToken.Spec.HashedTokenSecret.SecretKeyRef != nil {
		return b.BootstrapToken.Spec.HashedTokenSecret.SecretKeyRef.Name
	}
	return fmt.Sprintf("%s-%s", b.BootstrapToken.Name, HashedBootstrapTokenSuffix)
}

func (b *HumioBootstrapTokenConfig) tokenSecretCreateIfMissing() bool {
	if b.BootstrapToken.Spec.TokenSecret.CreateIfMissing != nil {
		return *b.BootstrapToken.Spec.TokenSecret.CreateIfMissing
	}
	return true
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

func (b *HumioBootstrapTokenConfig) name() string {
	return b.BootstrapToken.Name
}

func (b *HumioBootstrapTokenConfig) namespace() string {
	return b.BootstrapToken.Namespace
}
