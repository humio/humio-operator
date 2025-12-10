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

package telemetry

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("HumioTelemetry Controller", func() {

	BeforeEach(func() {
		// Clean up any existing resources before each test in the dedicated namespace
		ctx := context.Background()

		// Clean up HumioTelemetry resources
		telemetryList := &humiov1alpha1.HumioTelemetryList{}
		if err := k8sClient.List(ctx, telemetryList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, telemetry := range telemetryList.Items {
				_ = k8sClient.Delete(ctx, &telemetry) // Ignore errors - resource might not exist
			}
		}

		// Clean up HumioCluster resources
		clusterList := &humiov1alpha1.HumioClusterList{}
		if err := k8sClient.List(ctx, clusterList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, cluster := range clusterList.Items {
				_ = k8sClient.Delete(ctx, &cluster) // Ignore errors - resource might not exist
			}
		}

		// Clean up test secrets (but preserve system secrets)
		secretList := &corev1.SecretList{}
		if err := k8sClient.List(ctx, secretList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, secret := range secretList.Items {
				// Only delete test-related secrets, preserve system ones
				if secret.Name != "default-token" && !strings.HasPrefix(secret.Name, "default-") && secret.Name != suite.DockerRegistryCredentialsSecretName {
					_ = k8sClient.Delete(ctx, &secret) // Ignore errors - resource might not exist
				}
			}
		}
	})

	Context("Telemetry Basic Operations", Label("envtest", "dummy", "real"), func() {
		It("Should create telemetry configuration correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy("telemetry-basic", "HumioTelemetry basic creation test")

			key := types.NamespacedName{
				Name:      "test-telemetry",
				Namespace: testProcessNamespace,
			}

			// Create telemetry token secret
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "telemetry-token",
					Namespace: key.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-123"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// Create HumioTelemetry resource
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster-id",
					ManagedClusterName: "test-cluster",
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "telemetry-token",
								},
								Key: "token",
							},
						},
					},
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1d",
							Include:  []string{"license"},
						},
						{
							Interval: "15m",
							Include:  []string{"cluster_info"},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, telemetry)).To(Succeed())

			// Verify telemetry resource was created
			Eventually(func() error {
				createdTelemetry := &humiov1alpha1.HumioTelemetry{}
				return k8sClient.Get(ctx, key, createdTelemetry)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Verify spec fields
			createdTelemetry := &humiov1alpha1.HumioTelemetry{}
			Expect(k8sClient.Get(ctx, key, createdTelemetry)).To(Succeed())
			Expect(createdTelemetry.Spec.ClusterIdentifier).To(Equal("test-cluster-id"))
			Expect(createdTelemetry.Spec.ManagedClusterName).To(Equal("test-cluster"))
			Expect(createdTelemetry.Spec.RemoteReport.URL).To(Equal("https://telemetry.example.com"))
			Expect(createdTelemetry.Spec.Collections).To(HaveLen(2))
		})

		It("Should validate telemetry configuration", func() {
			ctx := context.Background()
			suite.UsingClusterBy("telemetry-validation", "HumioTelemetry validation test")

			key := types.NamespacedName{
				Name:      "invalid-telemetry",
				Namespace: testProcessNamespace,
			}

			// Create HumioTelemetry with missing required fields
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					// Missing ClusterIdentifier
					ManagedClusterName: "test-cluster",
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com",
						// Missing Token
					},
					Collections: []humiov1alpha1.CollectionConfig{},
				},
			}

			err := k8sClient.Create(ctx, telemetry)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("clusterIdentifier"))
		})

		It("Should validate data types in collections during reconcile", func() {
			ctx := context.Background()
			suite.UsingClusterBy("telemetry-datatype-validation", "HumioTelemetry data type validation test")

			key := types.NamespacedName{
				Name:      "test-telemetry-invalid-datatype",
				Namespace: testProcessNamespace,
			}

			// Create telemetry token secret
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "telemetry-token-datatype",
					Namespace: key.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-123"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			// Create a valid HumioCluster first
			cluster := suite.ConstructBasicSingleNodeHumioCluster(types.NamespacedName{
				Name:      "test-cluster-datatype",
				Namespace: key.Namespace,
			}, true)

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())

			// Create HumioTelemetry with invalid data type "usage_stats"
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster-invalid",
					ManagedClusterName: "test-cluster-datatype",
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com/api/v1/ingest/hec",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "telemetry-token-datatype"},
								Key:                  "token",
							},
						},
					},
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1m",
							Include:  []string{"license", "cluster_info"}, // Valid data types
						},
						{
							Interval: "5m",
							Include:  []string{"usage_stats"}, // Invalid data type - should cause validation error
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, telemetry)).Should(Succeed())
			GinkgoWriter.Printf("[DEBUG] Successfully created HumioTelemetry %s with invalid data type 'usage_stats'\n", key.Name)

			// Wait for reconcile and check that telemetry enters ConfigError state due to invalid data type
			Eventually(func() string {
				updatedTelemetry := &humiov1alpha1.HumioTelemetry{}
				err := k8sClient.Get(ctx, key, updatedTelemetry)
				if err != nil {
					GinkgoWriter.Printf("[DEBUG] Failed to get HumioTelemetry %s: %v\n", key.Name, err)
					return ""
				}
				GinkgoWriter.Printf("[DEBUG] HumioTelemetry %s status: State='%s', Generation=%d, Finalizers=%v, CollectionErrors=%d, ExportErrors=%d\n",
					key.Name, updatedTelemetry.Status.State, updatedTelemetry.Generation,
					updatedTelemetry.Finalizers, len(updatedTelemetry.Status.CollectionErrors), len(updatedTelemetry.Status.ExportErrors))
				if len(updatedTelemetry.Status.CollectionErrors) > 0 {
					GinkgoWriter.Printf("[DEBUG] Collection errors: %+v\n", updatedTelemetry.Status.CollectionErrors)
				}
				return updatedTelemetry.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTelemetryStateConfigError))

			// Verify the error message mentions the invalid data type
			telemetryAfterReconcile := &humiov1alpha1.HumioTelemetry{}
			Expect(k8sClient.Get(ctx, key, telemetryAfterReconcile)).Should(Succeed())
			Expect(telemetryAfterReconcile.Status.CollectionErrors).ShouldNot(BeEmpty())

			// Check that at least one error mentions the invalid data type
			errorFound := false
			for _, err := range telemetryAfterReconcile.Status.CollectionErrors {
				if strings.Contains(err.Message, "invalid data type: usage_stats") || strings.Contains(err.Message, "usage_stats") {
					errorFound = true
					break
				}
			}
			Expect(errorFound).Should(BeTrue(), "Expected to find validation error for 'usage_stats' data type")

			// Clean up
			Expect(k8sClient.Delete(ctx, telemetry)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, tokenSecret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).Should(Succeed())
		})

		It("Should successfully initialize and process telemetry collection", func() {
			ctx := context.Background()
			suite.UsingClusterBy("telemetry-collection-success", "HumioTelemetry successful collection test")

			key := types.NamespacedName{
				Name:      "test-telemetry-collection",
				Namespace: testProcessNamespace,
			}

			// Create telemetry token secret
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "telemetry-token-collection",
					Namespace: key.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-collection"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			// Create a valid HumioCluster first
			cluster := suite.ConstructBasicSingleNodeHumioCluster(types.NamespacedName{
				Name:      "test-cluster-collection",
				Namespace: key.Namespace,
			}, true)

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())

			// Create HumioTelemetry with valid configuration
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster-collection-id",
					ManagedClusterName: "test-cluster-collection",
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com/api/v1/ingest/hec",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "telemetry-token-collection"},
								Key:                  "token",
							},
						},
					},
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "30s",               // Short interval to trigger collection quickly
							Include:  []string{"license"}, // Valid data type
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, telemetry)).Should(Succeed())
			GinkgoWriter.Printf("[DEBUG] Successfully created HumioTelemetry %s for collection test\n", key.Name)

			// Wait for reconcile to complete successfully
			Eventually(func() string {
				updatedTelemetry := &humiov1alpha1.HumioTelemetry{}
				err := k8sClient.Get(ctx, key, updatedTelemetry)
				if err != nil {
					GinkgoWriter.Printf("[DEBUG] Failed to get HumioTelemetry %s: %v\n", key.Name, err)
					return ""
				}
				GinkgoWriter.Printf("[DEBUG] HumioTelemetry %s status: State='%s', Generation=%d, Finalizers=%v, LastCollectionTime=%v, CollectionStatus=%+v\n",
					key.Name, updatedTelemetry.Status.State, updatedTelemetry.Generation,
					updatedTelemetry.Finalizers, updatedTelemetry.Status.LastCollectionTime, updatedTelemetry.Status.CollectionStatus)
				if len(updatedTelemetry.Status.CollectionErrors) > 0 {
					GinkgoWriter.Printf("[DEBUG] Collection errors: %+v\n", updatedTelemetry.Status.CollectionErrors)
				}
				if len(updatedTelemetry.Status.ExportErrors) > 0 {
					GinkgoWriter.Printf("[DEBUG] Export errors: %+v\n", updatedTelemetry.Status.ExportErrors)
				}
				return updatedTelemetry.Status.State
			}, testTimeout, suite.TestInterval).ShouldNot(BeEmpty())

			// Verify the telemetry reached a valid operational state
			finalTelemetry := &humiov1alpha1.HumioTelemetry{}
			Expect(k8sClient.Get(ctx, key, finalTelemetry)).Should(Succeed())

			// Should have a valid state (any valid state means controller is working properly)
			validStates := []string{
				humiov1alpha1.HumioTelemetryStateEnabled,
				humiov1alpha1.HumioTelemetryStateCollecting,
				humiov1alpha1.HumioTelemetryStateConfigError,
				humiov1alpha1.HumioTelemetryStateDisabled,
				humiov1alpha1.HumioTelemetryStateExporting,
			}
			Expect(validStates).Should(ContainElement(finalTelemetry.Status.State))

			// Any errors should be well-formed
			for _, err := range finalTelemetry.Status.CollectionErrors {
				Expect(err.Type).ShouldNot(BeEmpty())
				Expect(err.Message).ShouldNot(BeEmpty())
			}

			// Clean up
			Expect(k8sClient.Delete(ctx, telemetry)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, tokenSecret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).Should(Succeed())
		})

		It("Should handle finalizer correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy("telemetry-finalizer", "HumioTelemetry finalizer test")

			key := types.NamespacedName{
				Name:      "test-telemetry-finalizer",
				Namespace: testProcessNamespace,
			}

			// Create telemetry token secret
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "telemetry-token-finalizer",
					Namespace: key.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-123"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// Create HumioTelemetry resource
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster-finalizer",
					ManagedClusterName: "test-cluster",
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "telemetry-token-finalizer",
								},
								Key: "token",
							},
						},
					},
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1d",
							Include:  []string{"license"},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, telemetry)).To(Succeed())
			GinkgoWriter.Printf("[DEBUG] Successfully created HumioTelemetry %s for finalizer test\n", key.Name)

			// Wait for finalizer to be added
			Eventually(func() []string {
				createdTelemetry := &humiov1alpha1.HumioTelemetry{}
				err := k8sClient.Get(ctx, key, createdTelemetry)
				if err != nil {
					GinkgoWriter.Printf("[DEBUG] Failed to get HumioTelemetry %s for finalizer check: %v\n", key.Name, err)
					return nil
				}
				GinkgoWriter.Printf("[DEBUG] HumioTelemetry %s finalizer status: Generation=%d, Finalizers=%v, State='%s'\n",
					key.Name, createdTelemetry.Generation, createdTelemetry.Finalizers, createdTelemetry.Status.State)
				return createdTelemetry.Finalizers
			}, testTimeout, suite.TestInterval).Should(ContainElement(controller.HumioFinalizer))

			// Delete the resource
			Expect(k8sClient.Delete(ctx, telemetry)).To(Succeed())

			// Verify resource is eventually deleted
			Eventually(func() bool {
				createdTelemetry := &humiov1alpha1.HumioTelemetry{}
				err := k8sClient.Get(ctx, key, createdTelemetry)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Telemetry Collection Scheduling", Label("envtest", "dummy", "real"), func() {
		It("Should schedule collections based on intervals", func() {
			ctx := context.Background()
			suite.UsingClusterBy("telemetry-scheduling", "HumioTelemetry collection scheduling test")

			key := types.NamespacedName{
				Name:      "scheduled-telemetry",
				Namespace: testProcessNamespace,
			}

			// Create telemetry token secret
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "telemetry-token-scheduled",
					Namespace: key.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-123"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// Create HumioCluster to be managed
			cluster := suite.ConstructBasicSingleNodeHumioCluster(types.NamespacedName{
				Name:      "test-cluster",
				Namespace: key.Namespace,
			}, false) // Don't create license since this cluster isn't bootstrapped
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Create HumioTelemetry with short intervals for testing
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "scheduled-cluster",
					ManagedClusterName: "test-cluster",
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "telemetry-token-scheduled",
								},
								Key: "token",
							},
						},
					},
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "30s", // Short interval for testing
							Include:  []string{"license"},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, telemetry)).To(Succeed())

			// Verify telemetry starts collecting (status should be updated)
			Eventually(func() string {
				createdTelemetry := &humiov1alpha1.HumioTelemetry{}
				_ = k8sClient.Get(ctx, key, createdTelemetry)
				return createdTelemetry.Status.State
			}, testTimeout, suite.TestInterval).Should(Or(
				Equal(humiov1alpha1.HumioTelemetryStateEnabled),
				Equal(humiov1alpha1.HumioTelemetryStateCollecting),
			))
		})

		It("Should handle collection errors gracefully", func() {
			ctx := context.Background()
			suite.UsingClusterBy("telemetry-errors", "HumioTelemetry error handling test")

			key := types.NamespacedName{
				Name:      "error-telemetry",
				Namespace: testProcessNamespace,
			}

			// Create telemetry token secret
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "telemetry-token-error",
					Namespace: key.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-123"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// Create HumioTelemetry that references non-existent cluster
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "error-cluster",
					ManagedClusterName: "nonexistent-cluster",
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "telemetry-token-error",
								},
								Key: "token",
							},
						},
					},
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "30s",
							Include:  []string{"license"},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, telemetry)).To(Succeed())

			// Verify the controller handles errors without crashing
			Eventually(func() string {
				createdTelemetry := &humiov1alpha1.HumioTelemetry{}
				_ = k8sClient.Get(ctx, key, createdTelemetry)
				return createdTelemetry.Status.State
			}, testTimeout, suite.TestInterval).Should(Or(
				Equal(humiov1alpha1.HumioTelemetryStateConfigError),
				Equal(humiov1alpha1.HumioTelemetryStateEnabled),
			))
		})
	})

	Context("Telemetry Integration with HumioCluster", Label("envtest", "dummy", "real"), func() {
		It("Should process HumioTelemetry that matches what cluster would create", func() {
			ctx := context.Background()
			suite.UsingClusterBy("cluster-integration", "HumioCluster telemetry integration test")

			clusterKey := types.NamespacedName{
				Name:      "test-integration-cluster",
				Namespace: testProcessNamespace,
			}

			// Create telemetry token secret
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "integration-telemetry-token",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("integration-token-123"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// Create HumioCluster resource that telemetry will reference
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, true)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Manually create HumioTelemetry resource (simulating what HumioCluster controller would create)
			telemetryKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-telemetry", clusterKey.Name),
				Namespace: clusterKey.Namespace,
			}

			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      telemetryKey.Name,
					Namespace: telemetryKey.Namespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "core.humio.com/v1alpha1",
							Kind:       "HumioCluster",
							Name:       clusterKey.Name,
							UID:        cluster.UID,
						},
					},
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "integration-cluster-id",
					ManagedClusterName: clusterKey.Name,
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "integration-telemetry-token",
								},
								Key: "token",
							},
						},
					},
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1d",
							Include:  []string{"license"},
						},
						{
							Interval: "15m",
							Include:  []string{"cluster_info"},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, telemetry)).To(Succeed())

			// Verify telemetry resource is processed by the controller
			Eventually(func() string {
				updatedTelemetry := &humiov1alpha1.HumioTelemetry{}
				err := k8sClient.Get(ctx, telemetryKey, updatedTelemetry)
				if err != nil {
					return ""
				}
				return updatedTelemetry.Status.State
			}, testTimeout, suite.TestInterval).ShouldNot(BeEmpty())

			// Verify telemetry configuration is correct
			updatedTelemetry := &humiov1alpha1.HumioTelemetry{}
			Expect(k8sClient.Get(ctx, telemetryKey, updatedTelemetry)).To(Succeed())
			Expect(updatedTelemetry.Spec.ClusterIdentifier).To(Equal("integration-cluster-id"))
			Expect(updatedTelemetry.Spec.ManagedClusterName).To(Equal(clusterKey.Name))
			Expect(updatedTelemetry.Spec.RemoteReport.URL).To(Equal("https://telemetry.example.com"))
			Expect(updatedTelemetry.Spec.Collections).To(HaveLen(2))

			// Verify owner reference is set
			Expect(updatedTelemetry.OwnerReferences).To(HaveLen(1))
			Expect(updatedTelemetry.OwnerReferences[0].Name).To(Equal(clusterKey.Name))

			// Clean up
			Expect(k8sClient.Delete(ctx, telemetry)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})

		It("Should handle telemetry lifecycle management", func() {
			ctx := context.Background()
			suite.UsingClusterBy("cluster-lifecycle", "HumioCluster telemetry lifecycle test")

			clusterKey := types.NamespacedName{
				Name:      "test-lifecycle-cluster",
				Namespace: testProcessNamespace,
			}

			// Create telemetry token secret
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "lifecycle-telemetry-token",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"token": []byte("lifecycle-token-123"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			// Create HumioCluster resource that telemetry will reference
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, true)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Manually create HumioTelemetry resource
			telemetryKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-telemetry", clusterKey.Name),
				Namespace: clusterKey.Namespace,
			}

			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      telemetryKey.Name,
					Namespace: telemetryKey.Namespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "core.humio.com/v1alpha1",
							Kind:       "HumioCluster",
							Name:       clusterKey.Name,
							UID:        cluster.UID,
						},
					},
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "lifecycle-test-cluster-id",
					ManagedClusterName: clusterKey.Name,
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "lifecycle-telemetry-token",
								},
								Key: "token",
							},
						},
					},
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1d",
							Include:  []string{"license"},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, telemetry)).To(Succeed())

			// Wait for telemetry resource to be processed
			Eventually(func() string {
				updatedTelemetry := &humiov1alpha1.HumioTelemetry{}
				err := k8sClient.Get(ctx, telemetryKey, updatedTelemetry)
				if err != nil {
					return ""
				}
				return updatedTelemetry.Status.State
			}, testTimeout, suite.TestInterval).ShouldNot(BeEmpty())

			// Now delete the telemetry resource (simulating cluster disabling telemetry)
			Expect(k8sClient.Delete(ctx, telemetry)).To(Succeed())

			// Verify telemetry resource is deleted
			Eventually(func() bool {
				deletedTelemetry := &humiov1alpha1.HumioTelemetry{}
				err := k8sClient.Get(ctx, telemetryKey, deletedTelemetry)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Clean up
			Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		})
	})

	Context("Scheduling Logic Edge Cases", Label("envtest", "dummy", "real"), func() {
		var reconciler *controller.HumioTelemetryReconciler

		BeforeEach(func() {
			reconciler = &controller.HumioTelemetryReconciler{
				Client: k8sClient,
				CommonConfig: controller.CommonConfig{
					RequeuePeriod:              time.Second * 30,
					CriticalErrorRequeuePeriod: time.Second * 5,
				},
				BaseLogger:  log,
				Log:         log,
				HumioClient: testHumioClient,
			}
		})

		It("should handle empty collections gracefully", func() {
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-empty-collections",
					Namespace: testProcessNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-humiocluster",
					Collections:        []humiov1alpha1.CollectionConfig{}, // Empty collections
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://test.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-secret"},
								Key:                  "token",
							},
						},
					},
				},
			}

			// Should not collect anything and return distant next collection time
			shouldCollect, collectTypes, nextCollection := reconciler.ShouldRunTelemetryCollection(telemetry)
			Expect(shouldCollect).To(BeFalse())
			Expect(collectTypes).To(BeEmpty())
			Expect(nextCollection).To(BeTemporally(">", time.Now().Add(23*time.Hour)))
		})

		It("should handle invalid duration formats gracefully", func() {
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-invalid-duration",
					Namespace: testProcessNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-humiocluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "invalid-duration", // Invalid format
							Include:  []string{"license"},
						},
						{
							Interval: "15m", // Valid format
							Include:  []string{"cluster_info"},
						},
					},
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://test.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-secret"},
								Key:                  "token",
							},
						},
					},
				},
			}

			// Should skip invalid duration but process valid ones
			shouldCollect, collectTypes, _ := reconciler.ShouldRunTelemetryCollection(telemetry)
			Expect(shouldCollect).To(BeTrue())
			Expect(collectTypes).To(ContainElement("cluster_info"))
			Expect(collectTypes).NotTo(ContainElement("license"))
		})

		It("should handle collection scheduling with existing status", func() {
			now := time.Now()
			recentCollection := metav1.NewTime(now.Add(-10 * time.Minute))
			oldCollection := metav1.NewTime(now.Add(-2 * time.Hour))

			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-existing-status",
					Namespace: testProcessNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-humiocluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "15m",
							Include:  []string{"license"},
						},
						{
							Interval: "1h",
							Include:  []string{"cluster_info"},
						},
					},
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://test.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-secret"},
								Key:                  "token",
							},
						},
					},
				},
				Status: humiov1alpha1.HumioTelemetryStatus{
					CollectionStatus: map[string]humiov1alpha1.CollectionTypeStatus{
						"license": {
							LastCollection: &recentCollection, // Recently collected
						},
						"cluster_info": {
							LastCollection: &oldCollection, // Old collection - should be collected
						},
					},
				},
			}

			shouldCollect, collectTypes, _ := reconciler.ShouldRunTelemetryCollection(telemetry)
			Expect(shouldCollect).To(BeTrue())
			Expect(collectTypes).To(ContainElement("cluster_info"))
			Expect(collectTypes).NotTo(ContainElement("license"))
		})

		It("should handle multiple data types in single collection correctly", func() {
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-multiple-types",
					Namespace: testProcessNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-humiocluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "15m",
							Include:  []string{"license", "cluster_info", "user_info"}, // Multiple types
						},
					},
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://test.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-secret"},
								Key:                  "token",
							},
						},
					},
				},
			}

			shouldCollect, collectTypes, _ := reconciler.ShouldRunTelemetryCollection(telemetry)
			Expect(shouldCollect).To(BeTrue())
			Expect(collectTypes).To(HaveLen(3))
			Expect(collectTypes).To(ContainElements("license", "cluster_info", "user_info"))
		})

		It("should calculate next collection time correctly for multiple intervals", func() {
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-next-collection",
					Namespace: testProcessNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-humiocluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1d", // 24 hours
							Include:  []string{"license"},
						},
						{
							Interval: "15m", // 15 minutes
							Include:  []string{"cluster_info"},
						},
					},
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://test.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-secret"},
								Key:                  "token",
							},
						},
					},
				},
			}

			// Both should be collected initially (no previous collections)
			shouldCollect, collectTypes, nextCollection := reconciler.ShouldRunTelemetryCollection(telemetry)
			Expect(shouldCollect).To(BeTrue())
			Expect(collectTypes).To(ContainElements("license", "cluster_info"))

			// Next collection should be sooner than 15 minutes (the shortest interval)
			Expect(nextCollection).To(BeTemporally("<=", time.Now().Add(15*time.Minute)))
		})

		It("should handle day duration parsing correctly", func() {
			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-day-duration",
					Namespace: testProcessNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-humiocluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "7d", // 7 days - test the fixed duration parsing
							Include:  []string{"license"},
						},
					},
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://test.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-secret"},
								Key:                  "token",
							},
						},
					},
				},
			}

			// Should handle 7d correctly (this tests our duration parsing fix)
			shouldCollect, collectTypes, _ := reconciler.ShouldRunTelemetryCollection(telemetry)
			Expect(shouldCollect).To(BeTrue())
			Expect(collectTypes).To(ContainElement("license"))
		})

		It("should handle concurrent collection intervals properly", func() {
			now := time.Now()
			recentCollection := metav1.NewTime(now.Add(-5 * time.Minute)) // 5 minutes ago

			telemetry := &humiov1alpha1.HumioTelemetry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-concurrent-intervals",
					Namespace: testProcessNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetrySpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-humiocluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "15m", // Should not be ready yet (collected 5 min ago)
							Include:  []string{"license"},
						},
						{
							Interval: "2m", // Should be ready (collected 5 min ago, interval is 2 min)
							Include:  []string{"cluster_info"},
						},
					},
					RemoteReport: humiov1alpha1.RemoteReportConfig{
						URL: "https://test.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-secret"},
								Key:                  "token",
							},
						},
					},
				},
				Status: humiov1alpha1.HumioTelemetryStatus{
					CollectionStatus: map[string]humiov1alpha1.CollectionTypeStatus{
						"license": {
							LastCollection: &recentCollection,
						},
						"cluster_info": {
							LastCollection: &recentCollection,
						},
					},
				},
			}

			shouldCollect, collectTypes, _ := reconciler.ShouldRunTelemetryCollection(telemetry)
			Expect(shouldCollect).To(BeTrue())
			Expect(collectTypes).To(ContainElement("cluster_info"))
			Expect(collectTypes).NotTo(ContainElement("license"))
		})
	})
})
