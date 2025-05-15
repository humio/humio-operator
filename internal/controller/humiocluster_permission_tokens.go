package controller

import (
	"context"
	"errors"
	"fmt"

	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/types"

	"github.com/humio/humio-operator/api/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
)

// createAndGetAdminAccountUserID ensures a Humio admin account exists and returns the user ID for it
func (r *HumioClusterReconciler) createAndGetAdminAccountUserID(ctx context.Context, client *humioapi.Client, req reconcile.Request, username string) (string, error) {
	// List all users and grab the user ID for an existing user
	currentUserID, err := r.HumioClient.GetUserIDForUsername(ctx, client, req, username)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			// If we didn't find a user ID, create a user, extract the user ID and return it
			newUserID, err := r.HumioClient.AddUserAndGetUserID(ctx, client, req, username, true)
			if err != nil {
				return "", err
			}
			if newUserID != "" {
				return newUserID, nil
			}
		}
		// Error while grabbing the user
		return "", err
	}

	if currentUserID != "" {
		// If we found a user ID, return it
		return currentUserID, nil
	}

	// Return error if we didn't find a valid user ID
	return "", fmt.Errorf("could not obtain user ID")
}

// validateAdminSecretContent grabs the current token stored in kubernetes and returns nil if it is valid
func (r *HumioClusterReconciler) validateAdminSecretContent(ctx context.Context, hc *v1alpha1.HumioCluster, req reconcile.Request) error {
	// Get existing Kubernetes secret
	adminSecretName := fmt.Sprintf("%s-%s", hc.Name, kubernetes.ServiceTokenSecretNameSuffix)
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Name:      adminSecretName,
		Namespace: hc.Namespace,
	}
	if err := r.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("got err while trying to get existing secret from k8s: %w", err)
	}

	// Check if secret currently holds a valid humio api token
	if _, ok := secret.Data["token"]; ok {
		cluster, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
		if err != nil {
			return fmt.Errorf("got err while trying to authenticate using apiToken: %w", err)
		}
		clientNotReady :=
			cluster.Config().Token != string(secret.Data["token"]) ||
				cluster.Config().Address == nil
		if clientNotReady {
			_, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
			if err != nil {
				return fmt.Errorf("got err while trying to authenticate using apiToken: %w", err)
			}
		}

		humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)
		_, err = r.HumioClient.GetCluster(ctx, humioHttpClient)
		if err != nil {
			return fmt.Errorf("got err while trying to use apiToken: %w", err)
		}

		// We could successfully get information about the cluster, so the token must be valid
		return nil
	}
	return fmt.Errorf("unable to validate if kubernetes secret %s holds a valid humio API token", adminSecretName)
}

// ensureAdminSecretContent ensures the target Kubernetes secret contains the desired API token
func (r *HumioClusterReconciler) ensureAdminSecretContent(ctx context.Context, hc *v1alpha1.HumioCluster, desiredAPIToken string) error {
	// Get existing Kubernetes secret
	adminSecretName := fmt.Sprintf("%s-%s", hc.Name, kubernetes.ServiceTokenSecretNameSuffix)
	key := types.NamespacedName{
		Name:      adminSecretName,
		Namespace: hc.Namespace,
	}
	adminSecret := &corev1.Secret{}
	err := r.Get(ctx, key, adminSecret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// If the secret doesn't exist, create it
			desiredSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
					Labels:    kubernetes.LabelsForHumio(hc.Name),
				},
				StringData: map[string]string{
					"token": desiredAPIToken,
				},
				Type: corev1.SecretTypeOpaque,
			}
			if err := r.Create(ctx, &desiredSecret); err != nil {
				return r.logErrorAndReturn(err, "unable to create secret")
			}
			return nil
		}
		return r.logErrorAndReturn(err, "unable to get secret")
	}

	// If we got no error, we compare current token with desired token and update if needed.
	if adminSecret.StringData["token"] != desiredAPIToken {
		adminSecret.StringData = map[string]string{"token": desiredAPIToken}
		if err := r.Update(ctx, adminSecret); err != nil {
			return r.logErrorAndReturn(err, "unable to update secret")
		}
	}

	return nil
}

func (r *HumioClusterReconciler) createPersonalAPIToken(ctx context.Context, client *humioapi.Client, req reconcile.Request, hc *v1alpha1.HumioCluster, username string) error {
	r.Log.Info("ensuring admin user")

	// Get user ID of admin account
	userID, err := r.createAndGetAdminAccountUserID(ctx, client, req, username)
	if err != nil {
		return fmt.Errorf("got err trying to obtain user ID of admin user: %s", err)
	}

	if err := r.validateAdminSecretContent(ctx, hc, req); err == nil {
		return nil
	}

	// Get API token for user ID of admin account
	apiToken, err := r.HumioClient.RotateUserApiTokenAndGet(ctx, client, req, userID)
	if err != nil {
		return r.logErrorAndReturn(err, fmt.Sprintf("failed to rotate api key for userID %s", userID))
	}

	// Update Kubernetes secret if needed
	err = r.ensureAdminSecretContent(ctx, hc, apiToken)
	if err != nil {
		return r.logErrorAndReturn(err, "unable to ensure admin secret")

	}

	return nil
}
