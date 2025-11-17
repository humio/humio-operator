package helpers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	operatorWebhookServiceName string = "humio-operator-webhook"
	operatorName               string = "humio-operator"
)

// GetOperatorName returns the operator name
func GetOperatorName() string {
	return operatorName
}

// GetOperatorNamespace returns the namespace where the operator is running
func GetOperatorNamespace() string {
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}

	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		return strings.TrimSpace(string(data))
	}

	return ""
}

// GetOperatorWebhookServiceName returns the service name for the webhook handler
func GetOperatorWebhookServiceName() string {
	return operatorWebhookServiceName
}

// Retry executes a function with retry logic using generics for type safety
func Retry[T any](fn func() (T, error), tries int, secondsBackoff time.Duration) (T, error) {
	var result T
	var err error

	for i := range tries {
		result, err = fn()
		if err == nil {
			return result, nil
		}
		if i < tries-1 {
			time.Sleep(secondsBackoff)
		}
	}
	return result, fmt.Errorf("operation failed after %d retries: %w", tries, err)
}

// GetClusterImageVersion returns the cluster's humio version
func GetClusterImageVersion(ctx context.Context, k8sClient client.Client, ns, managedClusterName, externalClusterName string) (string, error) {
	var image string
	var clusterName string

	if managedClusterName != "" {
		humioCluster := &humiov1alpha1.HumioCluster{}
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: managedClusterName}, humioCluster)
		if err != nil {
			return "", fmt.Errorf("unable to find requested managedCluster %s: %s", managedClusterName, err)
		}
		image = humioCluster.Status.Version
		clusterName = managedClusterName
	} else {
		humioCluster := &humiov1alpha1.HumioExternalCluster{}
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: externalClusterName}, humioCluster)
		if err != nil {
			return "", fmt.Errorf("unable to find requested externalCluster %s: %s", externalClusterName, err)
		}
		image = humioCluster.Status.Version
		clusterName = externalClusterName
	}

	if image == "" {
		return "", fmt.Errorf("version not available for cluster %s", clusterName)
	}
	parts := strings.Split(image, "-")

	return parts[0], nil
}

func FeatureExists(clusterVersion, minVersion string) (bool, error) {
	currentVersion, err := semver.NewVersion(clusterVersion)
	if err != nil {
		return false, fmt.Errorf("could not compute semver, currentVersion: %v", clusterVersion)
	}
	featureVersion, err := semver.NewVersion(minVersion)
	if err != nil {
		return false, fmt.Errorf("could not compute semver, featureVersion: %v", minVersion)
	}
	return currentVersion.GreaterThanEqual(featureVersion), nil
}
