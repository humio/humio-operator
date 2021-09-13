package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
)

const (
	waitForSecretTimeoutSeconds = 30
)

// waitForNewSecret can be used to wait for a new secret to be created after the create call is issued. It is important
// that the previousSecretList contains the list of secrets prior to when the new secret was created
func (r *HumioClusterReconciler) waitForNewSecret(ctx context.Context, hc *humiov1alpha1.HumioCluster, previousSecretList []corev1.Secret, expectedSecretName string) error {
	// We must check only secrets that existed prior to the new secret being created
	expectedSecretCount := len(previousSecretList) + 1

	for i := 0; i < waitForSecretTimeoutSeconds; i++ {
		foundSecretsList, err := kubernetes.ListSecrets(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForSecret(hc.Name, expectedSecretName))
		if err != nil {
			r.Log.Error(err, "unable list secrets", logFieldFunctionName, helpers.GetCurrentFuncName())
			return err
		}
		r.Log.Info(fmt.Sprintf("validating new secret was created. expected secret count %d, current secret count %d", expectedSecretCount, len(foundSecretsList)), logFieldFunctionName, helpers.GetCurrentFuncName())
		if len(foundSecretsList) >= expectedSecretCount {
			return nil
		}
		time.Sleep(time.Second * 1)
	}
	return fmt.Errorf("timed out waiting to validate new secret was created")
}
