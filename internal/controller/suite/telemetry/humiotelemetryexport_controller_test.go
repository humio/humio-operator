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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("HumioTelemetryExport Controller", func() {

	BeforeEach(func() {
		// Clean up any existing resources before each test in the dedicated namespace
		ctx := context.Background()

		// Clean up HumioTelemetryExport resources
		exportList := &humiov1alpha1.HumioTelemetryExportList{}
		if err := k8sClient.List(ctx, exportList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, export := range exportList.Items {
				_ = k8sClient.Delete(ctx, &export) // Ignore errors - resource might not exist
			}
		}

		// Clean up HumioTelemetryCollection resources
		collectionList := &humiov1alpha1.HumioTelemetryCollectionList{}
		if err := k8sClient.List(ctx, collectionList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, collection := range collectionList.Items {
				_ = k8sClient.Delete(ctx, &collection) // Ignore errors - resource might not exist
			}
		}

		// Clean up Secrets
		secretList := &corev1.SecretList{}
		if err := k8sClient.List(ctx, secretList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, secret := range secretList.Items {
				_ = k8sClient.Delete(ctx, &secret) // Ignore errors - resource might not exist
			}
		}

		// Wait for cleanup to complete
		Eventually(func() bool {
			exports := &humiov1alpha1.HumioTelemetryExportList{}
			if err := k8sClient.List(ctx, exports, client.InNamespace(testProcessNamespace)); err != nil {
				return true
			}
			return len(exports.Items) == 0
		}, testTimeout, suite.TestInterval).Should(BeTrue())
	})

	Context("Basic Export Resource Management", func() {
		It("should create a HumioTelemetryExport successfully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-export",
				Namespace: testProcessNamespace,
			}

			// Create token secret first
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token-secret",
					Namespace: testProcessNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-value"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "https://test-telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name: "test-collection",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryExport{}
				return k8sClient.Get(ctx, key, found) == nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should validate export configuration", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-export-invalid",
				Namespace: testProcessNamespace,
			}

			// Create export without token secret (invalid configuration)
			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "https://test-telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "non-existent-secret"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name: "test-collection",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create the reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: key}
			_, err := exportReconciler.Reconcile(ctx, req)

			// Should handle the error gracefully
			Expect(err).NotTo(HaveOccurred())

			// Check that status reflects configuration error
			Eventually(func() string {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, key, found) != nil {
					return ""
				}
				return found.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTelemetryExportStateConfigError))
		})

		It("should validate URL format", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-export-invalid-url",
				Namespace: testProcessNamespace,
			}

			// Create token secret first
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token-secret-url",
					Namespace: testProcessNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-value"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "invalid-url-format",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret-url"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name: "test-collection",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create the reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: key}
			_, err := exportReconciler.Reconcile(ctx, req)

			// Should handle the error gracefully
			Expect(err).NotTo(HaveOccurred())

			// Check that status reflects configuration error
			Eventually(func() string {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, key, found) != nil {
					return ""
				}
				return found.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTelemetryExportStateConfigError))
		})
	})

	Context("Collection Registration", func() {
		It("should verify registered collections exist", func() {
			ctx := context.Background()
			exportKey := types.NamespacedName{
				Name:      "test-export-registration",
				Namespace: testProcessNamespace,
			}
			collectionKey := types.NamespacedName{
				Name:      "test-collection-exists",
				Namespace: testProcessNamespace,
			}

			// Create the collection first
			collection := &humiov1alpha1.HumioTelemetryCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collectionKey.Name,
					Namespace: collectionKey.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryCollectionSpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-managed-cluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1h",
							Include:  []string{"license", "cluster_info"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, collection)).Should(Succeed())

			// Create token secret
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token-secret-reg",
					Namespace: testProcessNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-value"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			// Create export that references the existing collection
			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      exportKey.Name,
					Namespace: exportKey.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "https://test-telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret-reg"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name: collectionKey.Name,
							// Namespace defaults to export's namespace
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create the reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: exportKey}
			_, err := exportReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check that status shows collection is found
			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, exportKey, found) != nil {
					return false
				}
				collectionStatus, exists := found.Status.RegisteredCollectionStatus[collectionKey.Name]
				return exists && collectionStatus.Found
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle missing registered collections", func() {
			ctx := context.Background()
			exportKey := types.NamespacedName{
				Name:      "test-export-missing-collection",
				Namespace: testProcessNamespace,
			}

			// Create token secret
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token-secret-missing",
					Namespace: testProcessNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-value"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			// Create export that references a non-existent collection
			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      exportKey.Name,
					Namespace: exportKey.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "https://test-telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret-missing"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name: "non-existent-collection",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create the reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: exportKey}
			_, err := exportReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check that status shows collection is not found
			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, exportKey, found) != nil {
					return false
				}
				collectionStatus, exists := found.Status.RegisteredCollectionStatus["non-existent-collection"]
				return exists && !collectionStatus.Found
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle cross-namespace collection references", func() {
			ctx := context.Background()
			exportKey := types.NamespacedName{
				Name:      "test-export-cross-ns",
				Namespace: testProcessNamespace,
			}

			// Create a collection in a different namespace (simulate)
			otherNamespace := testProcessNamespace + "-other"
			collectionInOtherNs := "collection-in-other-ns"

			// Create token secret
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token-secret-cross-ns",
					Namespace: testProcessNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-value"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			// Create export that references collection in different namespace
			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      exportKey.Name,
					Namespace: exportKey.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "https://test-telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret-cross-ns"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name:      collectionInOtherNs,
							Namespace: otherNamespace,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create the reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: exportKey}
			_, err := exportReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check that status shows collection is not found (since we didn't create it)
			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, exportKey, found) != nil {
					return false
				}
				collectionStatus, exists := found.Status.RegisteredCollectionStatus[collectionInOtherNs]
				return exists && !collectionStatus.Found
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should register multiple collections", func() {
			ctx := context.Background()
			exportKey := types.NamespacedName{
				Name:      "test-export-multi-collections",
				Namespace: testProcessNamespace,
			}

			// Create multiple collections
			collectionNames := []string{"collection-1", "collection-2", "collection-3"}
			for _, name := range collectionNames {
				collection := &humiov1alpha1.HumioTelemetryCollection{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: testProcessNamespace,
					},
					Spec: humiov1alpha1.HumioTelemetryCollectionSpec{
						ClusterIdentifier:  "test-cluster",
						ManagedClusterName: "test-managed-cluster",
						Collections: []humiov1alpha1.CollectionConfig{
							{
								Interval: "1h",
								Include:  []string{"license", "cluster_info"},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, collection)).Should(Succeed())
			}

			// Create token secret
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token-secret-multi",
					Namespace: testProcessNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-value"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			// Create export that references all collections
			var registeredCollections []humiov1alpha1.HumioTelemetryCollectionReference
			for _, name := range collectionNames {
				registeredCollections = append(registeredCollections, humiov1alpha1.HumioTelemetryCollectionReference{
					Name: name,
				})
			}

			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      exportKey.Name,
					Namespace: exportKey.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "https://test-telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret-multi"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: registeredCollections,
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create the reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: exportKey}
			_, err := exportReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check that all collections are registered and found
			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, exportKey, found) != nil {
					return false
				}

				for _, name := range collectionNames {
					collectionStatus, exists := found.Status.RegisteredCollectionStatus[name]
					if !exists || !collectionStatus.Found {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Status Updates", func() {
		It("should update export status correctly", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-export-status",
				Namespace: testProcessNamespace,
			}

			// Create token secret first
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token-secret-status",
					Namespace: testProcessNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-value"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "https://test-telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret-status"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name: "test-collection",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: key}
			_, err := exportReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check status is updated
			Eventually(func() string {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, key, found) != nil {
					return ""
				}
				return found.Status.State
			}, testTimeout, suite.TestInterval).Should(Not(BeEmpty()))
		})
	})

	Context("Error Handling", func() {
		It("should handle resource not found during reconciliation", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "non-existent-export",
				Namespace: testProcessNamespace,
			}

			// Create reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation on non-existent resource
			req := reconcile.Request{NamespacedName: key}
			result, err := exportReconciler.Reconcile(ctx, req)

			// Should handle gracefully
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).Should(BeFalse())
		})

		It("should handle configuration updates", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-export-update",
				Namespace: testProcessNamespace,
			}

			// Create token secret first
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token-secret-update",
					Namespace: testProcessNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("test-token-value"),
				},
			}
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: "https://test-telemetry.example.com",
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret-update"},
								Key:                  "token",
							},
						},
					},
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name: "test-collection",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create reconciler
			exportReconciler := &controller.HumioTelemetryExportReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Initial reconciliation
			req := reconcile.Request{NamespacedName: key}
			_, err := exportReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Update the export configuration
			Eventually(func() error {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if err := k8sClient.Get(ctx, key, found); err != nil {
					return err
				}
				found.Spec.RemoteReport.URL = "https://updated-telemetry.example.com"
				return k8sClient.Update(ctx, found)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Reconcile again after update
			_, err = exportReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify the update was handled
			Eventually(func() string {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, key, found) != nil {
					return ""
				}
				return found.Spec.RemoteReport.URL
			}, testTimeout, suite.TestInterval).Should(Equal("https://updated-telemetry.example.com"))
		})
	})
})
