package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// common constants used across controllers
const (
	SecretFieldName      string        = "secret"
	TokenFieldName       string        = "token"
	ResourceFieldName    string        = "resourceName"
	CriticalErrorRequeue time.Duration = time.Minute * 1
)

// CommonConfig has common configuration parameters for all controllers.
type CommonConfig struct {
	RequeuePeriod time.Duration // How frequently to requeue a resource for reconcile.
}

// redactToken ensures that token secrers (even if encrypted) are not logged in full
func redactToken(token string) string {
	if len(token) == 0 {
		return "***empty***"
	}
	if len(token) <= 6 {
		return "***redacted***"
	}
	return token[:6] + "***"
}

// readBootstrapTokenSecret reads the BootstrapTokenSecret used to encrypt/decrypt tokens
func readBootstrapTokenSecret(ctx context.Context, client client.Client, cluster helpers.ClusterInterface, namespace string) (string, error) {
	secretName := fmt.Sprintf("%s-%s", cluster.Name(), bootstrapTokenSecretSuffix)
	existingSecret, err := kubernetes.GetSecret(ctx, client, secretName, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get bootstrap token secret %s: %w", secretName, err)
	}

	tokenBytes, exists := existingSecret.Data[SecretFieldName]
	if !exists {
		return "", fmt.Errorf("token key not found in secret %s", secretName)
	}

	return string(tokenBytes), nil
}

// encryptToken encrypts a text using the BootstrapTokenSecret as key
func encryptToken(ctx context.Context, client client.Client, cluster helpers.ClusterInterface, text string, namespace string) (string, error) {
	key, err := readBootstrapTokenSecret(ctx, client, cluster, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to read BootstrapTokenSecret: %s", err.Error())
	}
	encSecret, err := EncryptSecret(text, key)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt text: %s", err.Error())
	}
	return encSecret, nil
}

// decryptToken decrypts a token encrypted via the bootstraptoken
func decryptToken(ctx context.Context, client client.Client, cluster helpers.ClusterInterface, cyphertext string, namespace string) (string, error) {
	key, err := readBootstrapTokenSecret(ctx, client, cluster, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to read BootstrapTokenSecret: %s", err.Error())
	}
	decSecret, err := DecryptSecret(cyphertext, key)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt cyphertext: %s", err.Error())
	}
	return decSecret, nil
}
