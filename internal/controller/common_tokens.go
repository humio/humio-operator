package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	"github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// common constants used across controllers
const (
	SecretFieldName      string        = "secret"
	TokenFieldName       string        = "token"
	ResourceFieldName    string        = "resourceName"
	CriticalErrorRequeue time.Duration = time.Minute * 1
)

// TokenResource defines the interface for token resources (View/System/Organization)
type TokenResource interface {
	client.Object
	GetSpec() *v1alpha1.HumioTokenSpec
	GetStatus() *v1alpha1.HumioTokenStatus
}

// TokenController defines the interface for controllers(reconcilers) that manage tokens  (View/System/Organization)
type TokenController interface {
	client.Client
	Scheme() *runtime.Scheme
	Logger() logr.Logger
	GetRecorder() record.EventRecorder
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

func logErrorAndReturn(logger logr.Logger, err error, msg string) error {
	logger.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// ensureTokenSecretExists is a generic function to manage token secrets across all token types
func ensureTokenSecretExists(ctx context.Context, controller TokenController, tokenResource TokenResource, cluster helpers.ClusterInterface, tokenTypeName string) error {
	logger := controller.Logger()

	if tokenResource.GetSpec().TokenSecretName == "" {
		return fmt.Errorf("%s.Spec.TokenSecretName is mandatory but missing", tokenTypeName)
	}
	if tokenResource.GetStatus().Token == "" {
		return fmt.Errorf("%s.Status.Token is mandatory but missing", tokenTypeName)
	}

	secret, err := decryptToken(ctx, controller, cluster, tokenResource.GetStatus().Token, tokenResource.GetNamespace())
	if err != nil {
		return err
	}

	secretData := map[string][]byte{
		TokenFieldName:    []byte(secret),
		ResourceFieldName: []byte(tokenResource.GetSpec().Name),
	}

	desiredSecret := kubernetes.ConstructSecret(
		cluster.Name(),
		tokenResource.GetNamespace(),
		tokenResource.GetSpec().TokenSecretName,
		secretData,
		tokenResource.GetSpec().TokenSecretLabels,
		tokenResource.GetSpec().TokenSecretAnnotations,
	)

	if err := controllerutil.SetControllerReference(tokenResource, desiredSecret, controller.Scheme()); err != nil {
		return logErrorAndReturn(logger, err, "could not set controller reference")
	}

	existingSecret, err := kubernetes.GetSecret(ctx, controller, tokenResource.GetSpec().TokenSecretName, tokenResource.GetNamespace())
	if err != nil {
		if k8serrors.IsNotFound(err) {
			err = controller.Create(ctx, desiredSecret)
			if err != nil {
				return fmt.Errorf("unable to create %s token secret: %w", tokenTypeName, err)
			}
			logger.Info("successfully created token secret", "TokenSecretName", tokenResource.GetSpec().TokenSecretName, "TokenType", tokenTypeName)
		}
	} else {
		// kubernetes secret exists, check if we can/need to update it
		logger.Info("token secret already exists", "TokenSecretName", tokenResource.GetSpec().TokenSecretName, "TokenType", tokenTypeName)
		// prevent updating a secret with same name but different humio resource
		if string(existingSecret.Data[ResourceFieldName]) != "" && string(existingSecret.Data[ResourceFieldName]) != tokenResource.GetSpec().Name {
			return logErrorAndReturn(logger, fmt.Errorf("secret exists but has a different resource name: %s", string(existingSecret.Data[ResourceFieldName])), fmt.Sprintf("unable to update %s token secret", tokenTypeName))
		}
		if string(existingSecret.Data[TokenFieldName]) != string(desiredSecret.Data[TokenFieldName]) ||
			!cmp.Equal(existingSecret.Labels, desiredSecret.Labels) ||
			!cmp.Equal(existingSecret.Annotations, desiredSecret.Annotations) {
			logger.Info("secret does not match the token in Humio. Updating token", "TokenSecretName", tokenResource.GetSpec().TokenSecretName, "TokenType", tokenTypeName)
			if err = controller.Update(ctx, desiredSecret); err != nil {
				return logErrorAndReturn(logger, err, fmt.Sprintf("unable to update %s token secret", tokenTypeName))
			}
		}
	}
	return nil
}

// setState updates CR Status fields
func setState(ctx context.Context, controller TokenController, tokenResource TokenResource, state string, id string, secret string) error {
	controller.Logger().Info(fmt.Sprintf("updating %s Status: state=%s, id=%s, token=%s", tokenResource.GetSpec().Name, state, id, redactToken(secret)))
	if tokenResource.GetStatus().State == state && tokenResource.GetStatus().ID == id && tokenResource.GetStatus().Token == secret {
		controller.Logger().Info("so changes for Status, skipping")
		return nil
	}
	tokenResource.GetStatus().State = state
	tokenResource.GetStatus().ID = id
	tokenResource.GetStatus().Token = secret
	err := controller.Status().Update(ctx, tokenResource)
	if err == nil {
		controller.Logger().Info(fmt.Sprintf("successfully updated state for Humio Token %s", tokenResource.GetSpec().Name))
	}
	return err
}

// update state, log error and record k8s event
func handleCriticalError(ctx context.Context, controller TokenController, tokenResource TokenResource, err error) (reconcile.Result, error) {
	_ = logErrorAndReturn(controller.Logger(), err, "unrecoverable error encountered")
	_ = setState(ctx, controller, tokenResource, v1alpha1.HumioTokenConfigError, tokenResource.GetStatus().ID, tokenResource.GetStatus().Token)
	controller.GetRecorder().Event(tokenResource, corev1.EventTypeWarning, "unrecoverable error", err.Error())
	// we requeue after 1 minute since the error is not self healing and requires user intervention
	return reconcile.Result{RequeueAfter: CriticalErrorRequeue}, nil
}

// addFinalizer adds a finalizer to the CR to ensure cleanup function runs before deletion
func addFinalizer(ctx context.Context, controller TokenController, tokenResource TokenResource) error {
	controller.Logger().Info(fmt.Sprintf("adding Finalizer to Humio Token %s", tokenResource.GetSpec().Name))
	tokenResource.SetFinalizers(append(tokenResource.GetFinalizers(), humioFinalizer))
	err := controller.Update(ctx, tokenResource)
	if err != nil {
		return logErrorAndReturn(controller.Logger(), err, fmt.Sprintf("failed to add Finalizer to Humio Token %s", tokenResource.GetSpec().Name))
	}
	controller.Logger().Info(fmt.Sprintf("successfully added Finalizer to Humio Token %s", tokenResource.GetSpec().Name))
	return nil
}
