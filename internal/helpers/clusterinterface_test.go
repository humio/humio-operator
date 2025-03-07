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
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCluster_HumioConfig_managedHumioCluster(t *testing.T) {
	tests := []struct {
		name                string
		managedHumioCluster humiov1alpha1.HumioCluster
		certManagerEnabled  bool
	}{
		{
			"test managed humio cluster with insecure and no cert-manager",
			humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-1",
					Namespace: "namespace-1",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					TLS: &humiov1alpha1.HumioClusterTLSSpec{
						Enabled: BoolPtr(false),
					},
				},
			},
			false,
		},
		{
			"test managed humio cluster with insecure and cert-manager",
			humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-2",
					Namespace: "namespace-2",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					TLS: &humiov1alpha1.HumioClusterTLSSpec{
						Enabled: BoolPtr(false),
					},
				},
			},
			true,
		},
		{
			"test managed humio cluster with secure and no cert-manager",
			humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-3",
					Namespace: "namespace-3",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					TLS: &humiov1alpha1.HumioClusterTLSSpec{
						Enabled: BoolPtr(true),
					},
				},
			},
			false,
		},
		{
			"test managed humio cluster with secure and cert-manager",
			humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-4",
					Namespace: "namespace-4",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					TLS: &humiov1alpha1.HumioClusterTLSSpec{
						Enabled: BoolPtr(true),
					},
				},
			},
			true,
		},
		{
			"test managed humio cluster with default tls and no cert-manager",
			humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-5",
					Namespace: "namespace-5",
				},
				Spec: humiov1alpha1.HumioClusterSpec{},
			},
			false,
		},
		{
			"test managed humio cluster with default tls and cert-manager",
			humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-6",
					Namespace: "namespace-6",
				},
				Spec: humiov1alpha1.HumioClusterSpec{},
			},
			true,
		},
		{
			"test managed humio cluster with default tls enabled and no cert-manager",
			humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-7",
					Namespace: "namespace-7",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					TLS: &humiov1alpha1.HumioClusterTLSSpec{},
				},
			},
			false,
		},
		{
			"test managed humio cluster with default tls enabled and cert-manager",
			humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-8",
					Namespace: "namespace-8",
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					TLS: &humiov1alpha1.HumioClusterTLSSpec{},
				},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiTokenSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-admin-token", tt.managedHumioCluster.Name),
					Namespace: tt.managedHumioCluster.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("secret-api-token"),
				},
			}
			bootstrapTokenSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-bootstrap-token", tt.managedHumioCluster.Name),
					Namespace: tt.managedHumioCluster.Namespace,
				},
				Data: map[string][]byte{
					"hashedToken": []byte("hashed-token"),
					"secret":      []byte("secret-api-token"),
				},
			}
			caCertificateSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.managedHumioCluster.Name,
					Namespace: tt.managedHumioCluster.Namespace,
				},
				StringData: map[string]string{
					"ca.crt": "secret-ca-certificate-in-pem-format",
				},
			}
			objs := []runtime.Object{
				&tt.managedHumioCluster,
				&apiTokenSecret,
				&bootstrapTokenSecret,
				&caCertificateSecret,
			}
			// Register operator types with the runtime scheme.
			s := scheme.Scheme
			s.AddKnownTypes(humiov1alpha1.GroupVersion, &tt.managedHumioCluster)

			cl := fake.NewClientBuilder().WithRuntimeObjects(objs...).Build()

			cluster, err := NewCluster(context.Background(), cl, tt.managedHumioCluster.Name, "", tt.managedHumioCluster.Namespace, tt.certManagerEnabled, true, false)
			if err != nil || cluster.Config() == nil {
				t.Errorf("unable to obtain humio client config: %s", err)
			}

			if TLSEnabled(&tt.managedHumioCluster) == cluster.Config().Insecure {
				t.Errorf("configuration mismatch, expected cluster to use TLSEnabled: %+v, certManagerEnabled: %+v, Insecure: %+v", TLSEnabled(&tt.managedHumioCluster), tt.certManagerEnabled, cluster.Config().Insecure)
			}

			protocol := "https"
			if !TLSEnabled(&tt.managedHumioCluster) {
				protocol = "http"
			}
			expectedURL := fmt.Sprintf("%s://%s-internal.%s:8080/", protocol, tt.managedHumioCluster.Name, tt.managedHumioCluster.Namespace)
			if cluster.Config().Address.String() != expectedURL {
				t.Errorf("url not correct, expected: %s, got: %s", expectedURL, cluster.Config().Address)
			}

			expectedAPIToken := string(apiTokenSecret.Data["token"])
			if expectedAPIToken != cluster.Config().Token {
				t.Errorf("config does not contain an API token, expected: %s, got: %s", expectedAPIToken, cluster.Config().Token)
			}

			if !tt.certManagerEnabled && cluster.Config().CACertificatePEM != "" {
				t.Errorf("config should not include CA certificate when cert-manager is disabled or cluster is marked insecure")
			} else {
				expectedCACertificate := string(caCertificateSecret.Data["ca.crt"])
				if expectedCACertificate != cluster.Config().CACertificatePEM {
					t.Errorf("config does not include CA certificate even though it should")
				}
			}
		})
	}
}

func TestCluster_HumioConfig_externalHumioCluster(t *testing.T) {
	tests := []struct {
		name                  string
		externalHumioCluster  humiov1alpha1.HumioExternalCluster
		expectedConfigFailure bool
	}{
		{
			"external cluster with https and api token",
			humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-1",
					Namespace: "namespace-1",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                "https://humio-1.example.com/",
					APITokenSecretName: "cluster-1-admin-token",
				},
			},
			false,
		},
		{
			"external cluster with insecure https and api token",
			humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-2",
					Namespace: "namespace-2",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                "https://humio-2.example.com/",
					APITokenSecretName: "cluster-2-admin-token",
					Insecure:           true,
				},
			},
			false,
		},
		{
			"external cluster with http url and api token",
			humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-3",
					Namespace: "namespace-3",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                "http://humio-3.example.com/",
					APITokenSecretName: "cluster-3-admin-token",
					Insecure:           true,
				},
			},
			false,
		},
		{
			"external cluster with secure http url",
			humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-4",
					Namespace: "namespace-4",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                "http://humio-4.example.com/",
					APITokenSecretName: "cluster-4-admin-token",
					Insecure:           false,
				},
			},
			true,
		},
		{
			"external cluster with https url but no api token",
			humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-5",
					Namespace: "namespace-5",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url: "https://humio-5.example.com/",
				},
			},
			true,
		},

		{
			"external cluster with http url but no api token",
			humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-6",
					Namespace: "namespace-6",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url: "http://humio-6.example.com/",
				},
			},
			true,
		},
		{
			"external cluster with https url, api token and custom ca certificate",
			humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-7",
					Namespace: "namespace-7",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                "https://humio-7.example.com/",
					APITokenSecretName: "cluster-7-admin-token",
					CASecretName:       "cluster-7-ca-secret",
				},
			},
			false,
		},
		{
			"external cluster with http url, api token and custom ca certificate",
			humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-8",
					Namespace: "namespace-8",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                "http://humio-8.example.com/",
					APITokenSecretName: "cluster-8-admin-token",
					CASecretName:       "cluster-8-ca-secret",
				},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiTokenSecretName := tt.externalHumioCluster.Spec.APITokenSecretName
			if apiTokenSecretName == "" {
				apiTokenSecretName = fmt.Sprintf("%s-unspecified-admin-token", tt.externalHumioCluster.Name)
			}
			apiTokenSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      apiTokenSecretName,
					Namespace: tt.externalHumioCluster.Namespace,
				},
				StringData: map[string]string{
					"token": "secret-api-token",
				},
			}
			caCertificateSecretName := tt.externalHumioCluster.Spec.CASecretName
			if caCertificateSecretName == "" {
				caCertificateSecretName = fmt.Sprintf("%s-unspecified-ca-certificate", tt.externalHumioCluster.Name)
			}
			caCertificateSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caCertificateSecretName,
					Namespace: tt.externalHumioCluster.Namespace,
				},
				StringData: map[string]string{
					"ca.crt": "secret-ca-certificate-in-pem-format",
				},
			}
			objs := []runtime.Object{
				&tt.externalHumioCluster,
				&apiTokenSecret,
				&caCertificateSecret,
			}
			// Register operator types with the runtime scheme.
			s := scheme.Scheme
			s.AddKnownTypes(humiov1alpha1.GroupVersion, &tt.externalHumioCluster)

			cl := fake.NewClientBuilder().WithRuntimeObjects(objs...).Build()

			cluster, err := NewCluster(context.Background(), cl, "", tt.externalHumioCluster.Name, tt.externalHumioCluster.Namespace, false, true, false)
			if tt.expectedConfigFailure && (err == nil) {
				t.Errorf("unable to get a valid config: %s", err)
			}

			if !tt.expectedConfigFailure {
				if cluster.Config() == nil {
					t.Errorf("got nil config")

				}
				if cluster.Config() != nil {
					baseURL, err := url.Parse(tt.externalHumioCluster.Spec.Url)
					if err != nil {
						t.Errorf("could not parse url: %s", err)
					}
					if baseURL.String() != cluster.Config().Address.String() {
						t.Errorf("url not set in config, expected: %+v, got: %+v", baseURL.String(), cluster.Config().Address.String())
					}

					expectedAPIToken := string(apiTokenSecret.Data["token"])
					if expectedAPIToken != cluster.Config().Token {
						t.Errorf("config does not contain an API token, expected: %s, got: %s", expectedAPIToken, cluster.Config().Token)
					}

					if tt.externalHumioCluster.Spec.Insecure {
						if cluster.Config().CACertificatePEM != "" {
							t.Errorf("config should not include CA certificate when cert-manager is disabled or cluster is marked insecure")
						}

					} else {
						expectedCACertificate := string(caCertificateSecret.Data["ca.crt"])
						if expectedCACertificate != cluster.Config().CACertificatePEM {
							t.Errorf("config does not include CA certificate even though it should")
						}
					}
				}
			}
		})
	}
}

func TestCluster_NewCluster(t *testing.T) {
	tests := []struct {
		name                string
		managedClusterName  string
		externalClusterName string
		namespace           string
		expectError         bool
	}{
		{
			"two empty cluster names",
			"",
			"",
			"default",
			true,
		},
		{
			"two non-empty cluster names",
			"managed",
			"external",
			"default",
			true,
		},
		{
			"empty namespace",
			"managed",
			"",
			"",
			true,
		},
		{
			"managed cluster only",
			"managed",
			"",
			"default",
			false,
		},
		{
			"external cluster only",
			"",
			"external",
			"default",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			managedHumioCluster := humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed",
					Namespace: "default",
				},
				Spec: humiov1alpha1.HumioClusterSpec{},
			}
			externalHumioCluster := humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "external",
					Namespace: "default",
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                "https://127.0.0.1/",
					APITokenSecretName: "managed-admin-token",
					Insecure:           false,
				},
			}
			apiTokenSecrets := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "managed-admin-token",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"hashedToken": []byte("secret-api-token"),
					"secret":      []byte("secret-api-token"),
				},
			}

			objs := []runtime.Object{
				&managedHumioCluster,
				&externalHumioCluster,
				&apiTokenSecrets,
			}
			// Register operator types with the runtime scheme.
			s := scheme.Scheme
			s.AddKnownTypes(humiov1alpha1.GroupVersion, &managedHumioCluster)
			s.AddKnownTypes(humiov1alpha1.GroupVersion, &externalHumioCluster)

			cl := fake.NewClientBuilder().WithRuntimeObjects(objs...).Build()

			_, err := NewCluster(context.Background(), cl, tt.managedClusterName, tt.externalClusterName, tt.namespace, false, true, false)
			if tt.expectError == (err == nil) {
				t.Fatalf("expectError: %+v but got=%+v", tt.expectError, err)
			}
		})
	}
}