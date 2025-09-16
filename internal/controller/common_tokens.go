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
	ResourceFieldID      string        = "humioResourceID"
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
	GetCommonConfig() CommonConfig
}

func logErrorAndReturn(logger logr.Logger, err error, msg string) error {
	logger.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// ensureTokenSecretExists is a generic function to manage token secrets across all token types
func ensureTokenSecretExists(ctx context.Context, controller TokenController, tokenResource TokenResource, cluster helpers.ClusterInterface, existingSecret *corev1.Secret, tokenTypeName string, secret string) error {
	logger := controller.Logger()
	var secretValue string

	if tokenResource.GetSpec().TokenSecretName == "" {
		return fmt.Errorf("%s.Spec.TokenSecretName is mandatory but missing", tokenTypeName)
	}
	if tokenResource.GetStatus().HumioID == "" {
		return fmt.Errorf("%s.Status.HumioID is mandatory but missing", tokenTypeName)
	}

	if existingSecret != nil && secret == "" {
		secretValue = string(existingSecret.Data[TokenFieldName])
	} else {
		secretValue = secret
	}

	secretData := map[string][]byte{
		TokenFieldName:    []byte(secretValue),
		ResourceFieldName: []byte(tokenResource.GetSpec().Name),
		ResourceFieldID:   []byte(tokenResource.GetStatus().HumioID),
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

	// ensure finalizer is added to secret to prevent accidental deletion
	if !helpers.ContainsElement(desiredSecret.GetFinalizers(), HumioFinalizer) {
		controllerutil.AddFinalizer(desiredSecret, HumioFinalizer)
	}

	if existingSecret != nil {
		// prevent updating a secret with same name but different humio resource
		if string(existingSecret.Data[ResourceFieldName]) != tokenResource.GetSpec().Name {
			return logErrorAndReturn(logger, fmt.Errorf("secret exists but has a different resource name: %s", string(existingSecret.Data[ResourceFieldName])), fmt.Sprintf("unable to update %s token secret", tokenTypeName))
		}
		if string(existingSecret.Data[ResourceFieldID]) != string(desiredSecret.Data[ResourceFieldID]) ||
			string(existingSecret.Data[TokenFieldName]) != string(desiredSecret.Data[TokenFieldName]) ||
			!cmp.Equal(existingSecret.Labels, desiredSecret.Labels) ||
			!cmp.Equal(existingSecret.Annotations, desiredSecret.Annotations) {
			logger.Info("k8s secret does not match the CR. Updating token", "TokenSecretName", tokenResource.GetSpec().TokenSecretName, "TokenType", tokenTypeName)
			if err := controller.Update(ctx, desiredSecret); err != nil {
				return logErrorAndReturn(logger, err, fmt.Sprintf("unable to update %s token secret", tokenTypeName))
			}
		}
	} else {
		err := controller.Create(ctx, desiredSecret)
		if err != nil {
			return logErrorAndReturn(logger, err, fmt.Sprintf("unable to create %s token k8s secret: %v", tokenTypeName, err))
		}
	}
	return nil
}

// setState updates CR Status fields
func setState(ctx context.Context, controller TokenController, tokenResource TokenResource, state string, id string) error {
	controller.Logger().Info(fmt.Sprintf("updating %s Status: state=%s, id=%s", tokenResource.GetSpec().Name, state, id))
	if tokenResource.GetStatus().State == state && tokenResource.GetStatus().HumioID == id {
		controller.Logger().Info("no changes for Status, skipping")
		return nil
	}
	tokenResource.GetStatus().State = state
	tokenResource.GetStatus().HumioID = id
	err := controller.Status().Update(ctx, tokenResource)
	if err == nil {
		controller.Logger().Info(fmt.Sprintf("successfully updated state for Humio Token %s", tokenResource.GetSpec().Name))
	}
	return err
}

// update state, log error and record k8s event
func handleCriticalError(ctx context.Context, controller TokenController, tokenResource TokenResource, err error) (reconcile.Result, error) {
	_ = logErrorAndReturn(controller.Logger(), err, "unrecoverable error encountered")
	_ = setState(ctx, controller, tokenResource, v1alpha1.HumioTokenConfigError, tokenResource.GetStatus().HumioID)
	controller.GetRecorder().Event(tokenResource, corev1.EventTypeWarning, "unrecoverable error", err.Error())

	// Use configurable requeue time, fallback to default if not set
	requeue := CriticalErrorRequeue
	if controller.GetCommonConfig().CriticalErrorRequeuePeriod > 0 {
		requeue = controller.GetCommonConfig().CriticalErrorRequeuePeriod
	}
	return reconcile.Result{RequeueAfter: requeue}, nil
}

// addFinalizer adds a finalizer to the CR to ensure cleanup function runs before deletion
func addFinalizer(ctx context.Context, controller TokenController, tokenResource TokenResource) error {
	if !helpers.ContainsElement(tokenResource.GetFinalizers(), HumioFinalizer) {
		controller.Logger().Info(fmt.Sprintf("adding Finalizer to Humio Token %s", tokenResource.GetSpec().Name))
		tokenResource.SetFinalizers(append(tokenResource.GetFinalizers(), HumioFinalizer))
		err := controller.Update(ctx, tokenResource)
		if err != nil {
			return logErrorAndReturn(controller.Logger(), err, fmt.Sprintf("failed to add Finalizer to Humio Token %s", tokenResource.GetSpec().Name))
		}
		controller.Logger().Info(fmt.Sprintf("successfully added Finalizer to Humio Token %s", tokenResource.GetSpec().Name))
	}
	return nil
}
