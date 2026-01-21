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

var _ = Describe("HumioTelemetryCollection Controller", func() {

	BeforeEach(func() {
		// Clean up any existing resources before each test in the dedicated namespace
		ctx := context.Background()

		// Clean up HumioTelemetryCollection resources
		collectionList := &humiov1alpha1.HumioTelemetryCollectionList{}
		if err := k8sClient.List(ctx, collectionList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, collection := range collectionList.Items {
				_ = k8sClient.Delete(ctx, &collection) // Ignore errors - resource might not exist
			}
		}

		// Clean up HumioTelemetryExport resources
		exportList := &humiov1alpha1.HumioTelemetryExportList{}
		if err := k8sClient.List(ctx, exportList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, export := range exportList.Items {
				_ = k8sClient.Delete(ctx, &export) // Ignore errors - resource might not exist
			}
		}

		// Clean up HumioCluster resources
		clusterList := &humiov1alpha1.HumioClusterList{}
		if err := k8sClient.List(ctx, clusterList, client.InNamespace(testProcessNamespace)); err == nil {
			for _, cluster := range clusterList.Items {
				_ = k8sClient.Delete(ctx, &cluster) // Ignore errors - resource might not exist
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
			collections := &humiov1alpha1.HumioTelemetryCollectionList{}
			if err := k8sClient.List(ctx, collections, client.InNamespace(testProcessNamespace)); err != nil {
				return true
			}
			return len(collections.Items) == 0
		}, testTimeout, suite.TestInterval).Should(BeTrue())
	})

	Context("Basic Collection Resource Management", func() {
		It("should create a HumioTelemetryCollection successfully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-collection",
				Namespace: testProcessNamespace,
			}

			collection := &humiov1alpha1.HumioTelemetryCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
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

			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				return k8sClient.Get(ctx, key, found) == nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should validate collection configuration", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-collection-invalid",
				Namespace: testProcessNamespace,
			}

			collection := &humiov1alpha1.HumioTelemetryCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryCollectionSpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "test-managed-cluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1h",
							Include:  []string{"invalid_data_type"},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, collection)).Should(Succeed())

			// Create the reconciler
			collectionReconciler := &controller.HumioTelemetryCollectionReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: key}
			_, err := collectionReconciler.Reconcile(ctx, req)

			// Should handle the error gracefully
			Expect(err).NotTo(HaveOccurred())

			// Check that status reflects configuration error
			Eventually(func() string {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, key, found) != nil {
					return ""
				}
				return found.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTelemetryCollectionStateConfigError))
		})
	})

	Context("Exporter Discovery", func() {
		It("should discover registered exporters", func() {
			ctx := context.Background()
			collectionKey := types.NamespacedName{
				Name:      "test-collection-discovery",
				Namespace: testProcessNamespace,
			}
			exportKey := types.NamespacedName{
				Name:      "test-export-discovery",
				Namespace: testProcessNamespace,
			}

			// Create a collection
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

			// Create an export that references the collection
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
								LocalObjectReference: corev1.LocalObjectReference{Name: "test-token-secret"},
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

			// Create token secret
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
			Expect(k8sClient.Create(ctx, collection)).Should(Succeed())
			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			// Create reconciler and test exporter discovery
			collectionReconciler := &controller.HumioTelemetryCollectionReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: collectionKey}
			_, err := collectionReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify the collection can find the registered exporter
			Eventually(func() int {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, collectionKey, found) != nil {
					return 0
				}
				return len(found.Status.ExportPushResults)
			}, testTimeout, suite.TestInterval).Should(BeNumerically(">", 0))
		})

		It("should handle many-to-many relationships", func() {
			ctx := context.Background()
			collectionKey := types.NamespacedName{
				Name:      "test-collection-many",
				Namespace: testProcessNamespace,
			}

			// Create a collection
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

			// Create multiple exports that reference the same collection
			exports := []string{"export1", "export2", "export3"}
			for i, exportName := range exports {
				tokenSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("token-secret-%d", i+1),
						Namespace: testProcessNamespace,
					},
					Data: map[string][]byte{
						"token": []byte(fmt.Sprintf("test-token-value-%d", i+1)),
					},
				}

				export := &humiov1alpha1.HumioTelemetryExport{
					ObjectMeta: metav1.ObjectMeta{
						Name:      exportName,
						Namespace: testProcessNamespace,
					},
					Spec: humiov1alpha1.HumioTelemetryExportSpec{
						RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
							URL: fmt.Sprintf("https://telemetry-%d.example.com", i+1),
							Token: humiov1alpha1.VarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: tokenSecret.Name},
									Key:                  "token",
								},
							},
						},
						RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
							{
								Name: collectionKey.Name,
							},
						},
					},
				}

				Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())
				Expect(k8sClient.Create(ctx, export)).Should(Succeed())
			}

			Expect(k8sClient.Create(ctx, collection)).Should(Succeed())

			// Create reconciler and test discovery of multiple exporters
			collectionReconciler := &controller.HumioTelemetryCollectionReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: collectionKey}
			_, err := collectionReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify the collection discovered all 3 exporters
			Eventually(func() int {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, collectionKey, found) != nil {
					return 0
				}
				return len(found.Status.ExportPushResults)
			}, testTimeout, suite.TestInterval).Should(Equal(len(exports)))
		})
	})

	Context("Status Updates", func() {
		It("should update collection status correctly", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-collection-status",
				Namespace: testProcessNamespace,
			}

			collection := &humiov1alpha1.HumioTelemetryCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
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

			// Create reconciler
			collectionReconciler := &controller.HumioTelemetryCollectionReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: key}
			_, err := collectionReconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check status is updated
			Eventually(func() string {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, key, found) != nil {
					return ""
				}
				return found.Status.State
			}, testTimeout, suite.TestInterval).Should(Not(BeEmpty()))
		})
	})

	Context("Error Handling", func() {
		It("should handle missing managed cluster gracefully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-collection-no-cluster",
				Namespace: testProcessNamespace,
			}

			collection := &humiov1alpha1.HumioTelemetryCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioTelemetryCollectionSpec{
					ClusterIdentifier:  "test-cluster",
					ManagedClusterName: "non-existent-cluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "1h",
							Include:  []string{"license", "cluster_info"},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, collection)).Should(Succeed())

			// Create reconciler
			collectionReconciler := &controller.HumioTelemetryCollectionReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation
			req := reconcile.Request{NamespacedName: key}
			_, err := collectionReconciler.Reconcile(ctx, req)

			// Should handle gracefully without fatal error
			Expect(err).NotTo(HaveOccurred())

			// Status should reflect the error
			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, key, found) != nil {
					return false
				}
				// Should be in some error state or have collection status showing the issue
				return found.Status.State != "" &&
					(found.Status.State == humiov1alpha1.HumioTelemetryCollectionStateConfigError ||
						len(found.Status.CollectionStatus) > 0)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle resource not found during reconciliation", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "non-existent-collection",
				Namespace: testProcessNamespace,
			}

			// Create reconciler
			collectionReconciler := &controller.HumioTelemetryCollectionReconciler{
				Client:       k8sClient,
				CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 10},
				BaseLogger:   log,
			}

			// Run reconciliation on non-existent resource
			req := reconcile.Request{NamespacedName: key}
			result, err := collectionReconciler.Reconcile(ctx, req)

			// Should handle gracefully
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).Should(BeFalse())
		})
	})
})
