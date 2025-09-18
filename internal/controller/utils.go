package controller

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/go-logr/logr"
	"golang.org/x/exp/constraints"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// GetKeyWithHighestValue returns the key corresponding to the highest value in a map. In case multiple keys have the same value, the first key is returned.
//
// An error is returned if the passed map is empty.
func GetKeyWithHighestValue[K comparable, V constraints.Ordered](inputMap map[K]V) (K, error) {
	if len(inputMap) == 0 {
		var zeroKey K
		return zeroKey, errors.New("map is empty")
	}

	var maxKey K
	var maxValue V
	firstIteration := true

	for k, v := range inputMap {
		if firstIteration || v > maxValue {
			maxKey = k
			maxValue = v
			firstIteration = false
		}
	}
	return maxKey, nil
}

// GetPodNameFromNodeUri extracts and returns the pod name from a given URI string. This is done by extracting the
// hostname from the URI, splitting it against the "." string, and returning the first part.
//
// Examples:
//   - for https://cloud-test-core-xbattq.svc.namespace:8080, cloud-test-core-xbattq is returned
//   - for http://cloud-test-core-xbattq:8080, cloud-test-core-xbattq is returned
//
// An error is returned in case the URI cannot be parsed, or if the hostname string split has 0 parts
func GetPodNameFromNodeUri(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	parts := strings.Split(u.Hostname(), ".")
	if len(parts) == 0 {
		return "", errors.New("unable to determine pod name")
	}
	return parts[0], nil
}

func RemoveIntFromSlice(slice []int, value int) []int {
	var result []int
	for _, v := range slice {
		if v != value {
			result = append(result, v)
		}
	}
	return result
}

// EnsureValidCAIssuerGeneric is a generic helper that can be used by any controller to ensure a valid CA Issuer exists
// This function follows the exact same pattern as HumioCluster's EnsureValidCAIssuer but is generic enough to be reused
func EnsureValidCAIssuerGeneric(ctx context.Context, client client.Client, owner metav1.Object, scheme *runtime.Scheme, config GenericCAIssuerConfig, log logr.Logger) error {
	log.Info("checking for an existing valid CA Issuer")
	validIssuer, err := validCAIssuer(ctx, client, config.Namespace, config.Name)
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("could not validate CA Issuer: %w", err)
	}
	if validIssuer {
		log.Info("found valid CA Issuer")
		return nil
	}

	var existingCAIssuer cmapi.Issuer
	if err = client.Get(ctx, types.NamespacedName{
		Namespace: config.Namespace,
		Name:      config.Name,
	}, &existingCAIssuer); err != nil {
		if k8serrors.IsNotFound(err) {
			caIssuer := cmapi.Issuer{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: config.Namespace,
					Name:      config.Name,
					Labels:    config.Labels,
				},
				Spec: cmapi.IssuerSpec{
					IssuerConfig: cmapi.IssuerConfig{
						CA: &cmapi.CAIssuer{
							SecretName: config.CASecretName,
						},
					},
				},
			}
			if err := controllerutil.SetControllerReference(owner, &caIssuer, scheme); err != nil {
				return fmt.Errorf("could not set controller reference: %w", err)
			}
			// should only create it if it doesn't exist
			log.Info(fmt.Sprintf("creating CA Issuer: %s", caIssuer.Name))
			if err = client.Create(ctx, &caIssuer); err != nil {
				return fmt.Errorf("could not create CA Issuer: %w", err)
			}
			return nil
		}
		return fmt.Errorf("could not get CA Issuer: %w", err)
	}

	return nil
}
