/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package helpers

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	"github.com/humio/humio-operator/pkg/kubernetes"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ClusterInterface interface {
	Url(context.Context, client.Client) (*url.URL, error)
	Name() string
	Config() *humioapi.Config
	constructHumioConfig(context.Context, client.Client, bool) (*humioapi.Config, error)
}

type Cluster struct {
	managedClusterName  string
	externalClusterName string
	namespace           string
	certManagerEnabled  bool
	withAPIToken        bool
	humioConfig         *humioapi.Config
}

func NewCluster(ctx context.Context, k8sClient client.Client, managedClusterName, externalClusterName, namespace string, certManagerEnabled bool, withAPIToken bool) (ClusterInterface, error) {
	// Return error immediately if we do not have exactly one of the cluster names configured
	if managedClusterName != "" && externalClusterName != "" {
		return nil, fmt.Errorf("cannot have both ManagedClusterName and ExternalClusterName set at the same time")
	}
	if managedClusterName == "" && externalClusterName == "" {
		return nil, fmt.Errorf("must have one of ManagedClusterName and ExternalClusterName set")
	}
	if namespace == "" {
		return nil, fmt.Errorf("must have non-empty namespace set")
	}
	cluster := Cluster{
		externalClusterName: externalClusterName,
		managedClusterName:  managedClusterName,
		namespace:           namespace,
		certManagerEnabled:  certManagerEnabled,
		withAPIToken:        withAPIToken,
	}

	humioConfig, err := cluster.constructHumioConfig(ctx, k8sClient, withAPIToken)
	if err != nil {
		return nil, err
	}
	cluster.humioConfig = humioConfig

	return cluster, nil
}

func (c Cluster) Url(ctx context.Context, k8sClient client.Client) (*url.URL, error) {
	if c.managedClusterName != "" {
		// Lookup ManagedHumioCluster resource to figure out if we expect to use TLS or not
		var humioManagedCluster humiov1alpha1.HumioCluster
		err := k8sClient.Get(ctx, types.NamespacedName{
			Namespace: c.namespace,
			Name:      c.managedClusterName,
		}, &humioManagedCluster)
		if err != nil {
			return nil, err
		}

		protocol := "https"
		if !c.certManagerEnabled {
			protocol = "http"
		}
		if !TLSEnabled(&humioManagedCluster) {
			protocol = "http"
		}
		baseURL, _ := url.Parse(fmt.Sprintf("%s://%s-headless.%s:%d/", protocol, c.managedClusterName, c.namespace, 8080))
		return baseURL, nil
	}

	// Fetch the HumioExternalCluster instance
	var humioExternalCluster humiov1alpha1.HumioExternalCluster
	err := k8sClient.Get(ctx, types.NamespacedName{
		Namespace: c.namespace,
		Name:      c.externalClusterName,
	}, &humioExternalCluster)
	if err != nil {
		return nil, err
	}

	baseURL, err := url.Parse(humioExternalCluster.Spec.Url)
	if err != nil {
		return nil, err
	}
	return baseURL, nil
}

// Name returns the name of the Humio cluster
func (c Cluster) Name() string {
	if c.managedClusterName != "" {
		return c.managedClusterName
	}
	return c.externalClusterName
}

// Config returns the configuration that is currently set
func (c Cluster) Config() *humioapi.Config {
	return c.humioConfig
}

// constructHumioConfig returns a config to use with Humio API client with the necessary CA and API token.
func (c Cluster) constructHumioConfig(ctx context.Context, k8sClient client.Client, withAPIToken bool) (*humioapi.Config, error) {
	if c.managedClusterName != "" {
		// Lookup ManagedHumioCluster resource to figure out if we expect to use TLS or not
		var humioManagedCluster humiov1alpha1.HumioCluster
		err := k8sClient.Get(ctx, types.NamespacedName{
			Namespace: c.namespace,
			Name:      c.managedClusterName,
		}, &humioManagedCluster)
		if err != nil {
			return nil, err
		}

		// Get the URL we want to use
		clusterURL, err := c.Url(ctx, k8sClient)
		if err != nil {
			return nil, err
		}

		config := &humioapi.Config{
			Address: clusterURL,
		}

		var apiToken corev1.Secret
		if withAPIToken {
			// Get API token
			err = k8sClient.Get(ctx, types.NamespacedName{
				Namespace: c.namespace,
				Name:      fmt.Sprintf("%s-%s", c.managedClusterName, kubernetes.ServiceTokenSecretNameSuffix),
			}, &apiToken)
			if err != nil {
				return nil, fmt.Errorf("unable to get secret containing api token: %w", err)
			}
			config.Token = string(apiToken.Data["token"])
		}

		// If we do not use TLS, return a client without CA certificate
		if !c.certManagerEnabled || !TLSEnabled(&humioManagedCluster) {
			config.Insecure = true
			return config, nil
		}

		// Look up the CA certificate stored in the cluster CA bundle
		var caCertificate corev1.Secret
		err = k8sClient.Get(ctx, types.NamespacedName{
			Namespace: c.namespace,
			Name:      c.managedClusterName,
		}, &caCertificate)
		if err != nil {
			return nil, fmt.Errorf("unable to get CA certificate: %w", err)
		}

		config.CACertificatePEM = string(caCertificate.Data["ca.crt"])
		return config, nil
	}

	// Fetch the HumioExternalCluster instance
	var humioExternalCluster humiov1alpha1.HumioExternalCluster
	err := k8sClient.Get(ctx, types.NamespacedName{
		Namespace: c.namespace,
		Name:      c.externalClusterName,
	}, &humioExternalCluster)
	if err != nil {
		return nil, err
	}

	if humioExternalCluster.Spec.Url == "" {
		return nil, fmt.Errorf("no url specified")
	}

	if humioExternalCluster.Spec.APITokenSecretName == "" {
		return nil, fmt.Errorf("no api token secret name specified")
	}

	if strings.HasPrefix(humioExternalCluster.Spec.Url, "http://") && !humioExternalCluster.Spec.Insecure {
		return nil, fmt.Errorf("not possible to run secure cluster with plain http")
	}

	// Get API token
	var apiToken corev1.Secret
	err = k8sClient.Get(ctx, types.NamespacedName{
		Namespace: c.namespace,
		Name:      humioExternalCluster.Spec.APITokenSecretName,
	}, &apiToken)
	if err != nil {
		return nil, fmt.Errorf("unable to get secret containing api token: %w", err)
	}

	clusterURL, err := url.Parse(humioExternalCluster.Spec.Url)
	if err != nil {
		return nil, err
	}

	// If we do not use TLS, return a config without CA certificate
	if humioExternalCluster.Spec.Insecure {
		return &humioapi.Config{
			Address:  clusterURL,
			Token:    string(apiToken.Data["token"]),
			Insecure: humioExternalCluster.Spec.Insecure,
		}, nil
	}

	// If CA secret is specified, return a configuration which loads the CA
	if humioExternalCluster.Spec.CASecretName != "" {
		var caCertificate corev1.Secret
		err = k8sClient.Get(ctx, types.NamespacedName{
			Namespace: c.namespace,
			Name:      humioExternalCluster.Spec.CASecretName,
		}, &caCertificate)
		if err != nil {
			return nil, fmt.Errorf("unable to get CA certificate: %w", err)
		}
		return &humioapi.Config{
			Address:          clusterURL,
			Token:            string(apiToken.Data["token"]),
			CACertificatePEM: string(caCertificate.Data["ca.crt"]),
			Insecure:         humioExternalCluster.Spec.Insecure,
		}, nil
	}

	return &humioapi.Config{
		Address:  clusterURL,
		Token:    string(apiToken.Data["token"]),
		Insecure: humioExternalCluster.Spec.Insecure,
	}, nil
}
