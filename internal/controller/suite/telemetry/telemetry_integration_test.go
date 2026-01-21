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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/humio"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Telemetry Integration Validation", Label("envtest", "dummy", "real"), func() {

	Context("Sample YAML Validation", func() {
		It("should validate HumioTelemetry sample", func() {
			Skip("HumioTelemetry CRD has been replaced with split telemetry resources")
		})

		It("should validate HumioCluster with telemetry sample", func() {
			samplePath := filepath.Join("..", "..", "..", "..", "config", "samples", "core_v1alpha1_humiocluster_with_telemetry.yaml")
			content, err := os.ReadFile(samplePath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred(), "Sample file should exist: %s", samplePath)

			sampleContent := string(content)

			// Validate basic structure
			Expect(sampleContent).To(ContainSubstring("apiVersion: core.humio.com"))
			Expect(sampleContent).To(ContainSubstring("kind: HumioCluster"))
			Expect(sampleContent).To(ContainSubstring("telemetryConfig:"))
			Expect(sampleContent).To(ContainSubstring("remoteReport:"))
			Expect(sampleContent).To(ContainSubstring("metadata:"))
			Expect(sampleContent).To(ContainSubstring("spec:"))
		})
	})

	Context("GraphQL Schema Validation", func() {
		It("should contain telemetry-related queries and mutations", func() {
			schemaPath := filepath.Join("..", "..", "..", "..", "internal", "api", "humiographql", "graphql", "license.graphql")
			content, err := os.ReadFile(schemaPath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred(), "GraphQL schema file should exist: %s", schemaPath)

			schemaContent := string(content)

			// Check for license-related queries that telemetry uses
			Expect(schemaContent).To(ContainSubstring("GetLicenseForTelemetry"))

			// Verify it's valid GraphQL syntax
			Expect(schemaContent).To(ContainSubstring("query"))
			Expect(schemaContent).To(ContainSubstring("License"))
		})
	})

	Context("Telemetry Export Integration", func() {
		It("should export telemetry events with @timestamp fields", func() {
			// Track HTTP requests to validate payload structure
			var capturedRequests []*http.Request
			var capturedBodies []string

			// Create test server to capture HEC requests
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Capture the request for analysis
				capturedRequests = append(capturedRequests, r)

				// Read and capture the request body
				body, err := io.ReadAll(r.Body)
				Expect(err).NotTo(HaveOccurred(), "Should be able to read request body")
				capturedBodies = append(capturedBodies, string(body))

				// Return successful response
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"text":"Success","code":0}`))
			}))
			defer testServer.Close()

			// Create telemetry exporter with test server
			logger := logr.Discard()
			exporter := humio.NewTelemetryExporter(testServer.URL, "test-token", true, logger)

			// Create test telemetry payloads with various data structures
			testTimestamp := time.Date(2025, 12, 18, 15, 41, 38, 8000000, time.UTC) // Fixed timestamp for testing
			testPayloads := []humio.TelemetryPayload{
				{
					Timestamp:      testTimestamp,
					ClusterID:      "integration-test-cluster",
					CollectionType: humio.TelemetryCollectionTypeLicense,
					SourceType:     humio.TelemetrySourceTypeJSON,
					Data: map[string]interface{}{
						"license_uid":  "test-license-123",
						"license_type": "trial",
						"owner":        "test-owner",
					},
				},
				{
					Timestamp:      testTimestamp,
					ClusterID:      "integration-test-cluster",
					CollectionType: humio.TelemetryCollectionTypeRepositoryUsage,
					SourceType:     humio.TelemetrySourceTypeJSON,
					Data: map[string]interface{}{
						"name":                 "test-repo",
						"ingest_volume_gb_24h": 10.5,
						"event_count_24h":      int64(1000),
						"last_activity_time":   testTimestamp.Unix(), // Test timestamp sanitization
						"dataspace":            "test-dataspace",
					},
				},
			}

			// Export the payloads through the actual export flow
			exportErrors := exporter.ExportPayloads(context.Background(), testPayloads)

			// Verify no export errors occurred
			Expect(exportErrors).To(BeEmpty(), "Export should succeed without errors")

			// Verify we captured the expected number of requests
			Expect(capturedRequests).To(HaveLen(2), "Should have captured 2 HTTP requests")
			Expect(capturedBodies).To(HaveLen(2), "Should have captured 2 request bodies")

			// Validate each captured request and payload
			for i, body := range capturedBodies {
				By(fmt.Sprintf("Validating request %d payload structure", i+1))

				// Parse the HEC event from the request body
				var hecEvent humio.HECEvent
				err := json.Unmarshal([]byte(body), &hecEvent)
				Expect(err).NotTo(HaveOccurred(), "Should be able to parse HEC event JSON")

				// Verify HEC event structure
				Expect(hecEvent.Host).To(Equal("integration-test-cluster"), "HEC Host should match cluster ID")
				Expect(hecEvent.Source).To(Equal("humio-operator"), "HEC Source should be humio-operator")
				Expect(hecEvent.SourceType).To(Equal("json"), "HEC SourceType should be json")
				Expect(hecEvent.Time).NotTo(BeNil(), "HEC Time field should be set")

				// Convert event data back to JSON to inspect field names
				eventJSON, err := json.Marshal(hecEvent.Event)
				Expect(err).NotTo(HaveOccurred(), "Should be able to marshal event data")
				eventStr := string(eventJSON)

				// CRITICAL TEST: Verify @timestamp field is present (not timestamp)
				Expect(eventStr).To(ContainSubstring("\"@timestamp\":"), "Event should contain @timestamp field")
				Expect(eventStr).NotTo(ContainSubstring("\"timestamp\":"), "Event should NOT contain regular timestamp field")

				// Verify @timestamp field contains proper ISO format or Unix seconds
				var eventData map[string]interface{}
				err = json.Unmarshal(eventJSON, &eventData)
				Expect(err).NotTo(HaveOccurred(), "Should be able to unmarshal event data")

				timestampValue, exists := eventData["@timestamp"]
				Expect(exists).To(BeTrue(), "@timestamp field must exist in event data")
				Expect(timestampValue).NotTo(BeNil(), "@timestamp field must not be nil")

				// Validate @timestamp format (should be ISO string)
				timestampStr, ok := timestampValue.(string)
				Expect(ok).To(BeTrue(), "@timestamp should be a string in ISO format")
				Expect(timestampStr).To(MatchRegexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`), "@timestamp should follow ISO format")

				By(fmt.Sprintf("✓ Request %d contains proper @timestamp field: %s", i+1, timestampStr))

				// Additional validation for repository usage data
				if i == 1 { // Repository usage payload
					// Verify that last_activity_time timestamp was sanitized to ISO format
					if dataMap, ok := eventData["data"].(map[string]interface{}); ok {
						if lastActivityTime, exists := dataMap["last_activity_time"]; exists {
							// Should be converted from Unix timestamp to ISO string
							lastActivityStr, ok := lastActivityTime.(string)
							Expect(ok).To(BeTrue(), "last_activity_time should be sanitized to ISO string")
							Expect(lastActivityStr).To(MatchRegexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`), "last_activity_time should be in ISO format")
							By(fmt.Sprintf("✓ last_activity_time properly sanitized to ISO: %s", lastActivityStr))
						}
					}

					// Verify dataspace field is present and is a string (not array)
					if dataMap, ok := eventData["data"].(map[string]interface{}); ok {
						dataspace, exists := dataMap["dataspace"]
						Expect(exists).To(BeTrue(), "dataspace field should exist")
						dataspaceStr, ok := dataspace.(string)
						Expect(ok).To(BeTrue(), "dataspace should be a string, not an array")
						Expect(dataspaceStr).To(Equal("test-dataspace"), "dataspace value should match")
						By("✓ dataspace field is properly structured as single string")
					}
				}
			}

			// Verify HTTP request headers
			for i, req := range capturedRequests {
				By(fmt.Sprintf("Validating request %d HTTP headers", i+1))

				// Verify authentication format
				authHeader := req.Header.Get("Authorization")
				Expect(authHeader).To(Equal("Bearer test-token"), "Should use Bearer authentication")
				Expect(authHeader).NotTo(HavePrefix("Splunk "), "Should NOT use deprecated Splunk auth format")

				// Verify content type
				contentType := req.Header.Get("Content-Type")
				Expect(contentType).To(Equal("application/json"), "Content-Type should be application/json")

				// Verify HTTP method
				Expect(req.Method).To(Equal("POST"), "HTTP method should be POST")

				By(fmt.Sprintf("✓ Request %d has proper HTTP headers", i+1))
			}

			By("✅ Integration test completed successfully - all telemetry events use @timestamp fields")
		})
	})

	Context("Split Telemetry CRDs Validation", func() {
		It("should validate HumioTelemetryCollection CRD structure", func() {
			crdPath := filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "core.humio.com_humiotelemetrycollections.yaml")
			content, err := os.ReadFile(crdPath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred(), "HumioTelemetryCollection CRD file should exist: %s", crdPath)

			crdContent := string(content)
			requiredFields := []string{"clusterIdentifier", "managedClusterName", "collections"}

			for _, field := range requiredFields {
				Expect(crdContent).To(ContainSubstring(field), "CRD should contain required field: %s", field)
			}

			// Parse CRD to validate structure
			var crd apiextensionsv1.CustomResourceDefinition
			err = yaml.Unmarshal(content, &crd)
			Expect(err).NotTo(HaveOccurred(), "CRD should be valid YAML")

			Expect(crd.Spec.Names.Kind).To(Equal("HumioTelemetryCollection"))
			Expect(crd.Spec.Group).To(Equal("core.humio.com"))

			// Verify print columns are defined for kubectl output
			Expect(crd.Spec.Versions[0].AdditionalPrinterColumns).NotTo(BeEmpty(), "Should have printer columns for kubectl")
		})

		It("should validate HumioTelemetryExport CRD structure", func() {
			crdPath := filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "core.humio.com_humiotelemetryexports.yaml")
			content, err := os.ReadFile(crdPath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred(), "HumioTelemetryExport CRD file should exist: %s", crdPath)

			crdContent := string(content)
			requiredFields := []string{"remoteReport", "registeredCollections"}

			for _, field := range requiredFields {
				Expect(crdContent).To(ContainSubstring(field), "CRD should contain required field: %s", field)
			}

			// Parse CRD to validate structure
			var crd apiextensionsv1.CustomResourceDefinition
			err = yaml.Unmarshal(content, &crd)
			Expect(err).NotTo(HaveOccurred(), "CRD should be valid YAML")

			Expect(crd.Spec.Names.Kind).To(Equal("HumioTelemetryExport"))
			Expect(crd.Spec.Group).To(Equal("core.humio.com"))

			// Verify print columns are defined for kubectl output
			Expect(crd.Spec.Versions[0].AdditionalPrinterColumns).NotTo(BeEmpty(), "Should have printer columns for kubectl")
		})

		It("should validate both CRDs support namespaced scope", func() {
			// HumioTelemetryCollection
			collectionCRDPath := filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "core.humio.com_humiotelemetrycollections.yaml")
			collectionContent, err := os.ReadFile(collectionCRDPath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred())

			var collectionCRD apiextensionsv1.CustomResourceDefinition
			err = yaml.Unmarshal(collectionContent, &collectionCRD)
			Expect(err).NotTo(HaveOccurred())
			Expect(collectionCRD.Spec.Scope).To(Equal(apiextensionsv1.NamespaceScoped))

			// HumioTelemetryExport
			exportCRDPath := filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "core.humio.com_humiotelemetryexports.yaml")
			exportContent, err := os.ReadFile(exportCRDPath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred())

			var exportCRD apiextensionsv1.CustomResourceDefinition
			err = yaml.Unmarshal(exportContent, &exportCRD)
			Expect(err).NotTo(HaveOccurred())
			Expect(exportCRD.Spec.Scope).To(Equal(apiextensionsv1.NamespaceScoped))
		})
	})

	Context("Split Telemetry End-to-End Integration", func() {
		var (
			collectionName string
			exportName     string
			secretName     string
			testNamespace  string
		)

		BeforeEach(func() {
			collectionName = "integration-test-collection"
			exportName = "integration-test-export"
			secretName = "integration-test-token"
			testNamespace = testProcessNamespace
		})

		It("should create collection and export resources that work together", func() {
			ctx := context.Background()

			// Set up test HTTP server to capture telemetry exports
			var capturedRequests []*http.Request
			var capturedBodies []string

			testServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedRequests = append(capturedRequests, r)

				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				capturedBodies = append(capturedBodies, string(body))

				// Log what we received for debugging
				_, _ = fmt.Fprintf(GinkgoWriter, "Received telemetry export: %s\n", string(body))

				w.WriteHeader(http.StatusOK)
			}))
			defer testServer.Close()

			By("Creating a HumioCluster resource for telemetry collection")
			cluster := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-managed-cluster",
					Namespace: testNamespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image:     "humio/humio-core:1.70.0",
						NodeCount: 1,
					},
					TargetReplicationFactor: 1,
					StoragePartitionsCount:  24,
					DigestPartitionsCount:   24,
				},
			}

			// Create resources in order
			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())

			By("Creating a HumioTelemetryCollection resource")
			collection := &humiov1alpha1.HumioTelemetryCollection{
				ObjectMeta: metav1.ObjectMeta{
					Name:      collectionName,
					Namespace: testNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetryCollectionSpec{
					ClusterIdentifier:  "split-integration-test",
					ManagedClusterName: "test-managed-cluster",
					Collections: []humiov1alpha1.CollectionConfig{
						{
							Interval: "5m", // Short interval for testing
							Include:  []string{"license", "cluster_info"},
						},
					},
				},
			}

			By("Creating an authentication token secret")
			tokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: testNamespace,
				},
				Data: map[string][]byte{
					"token": []byte("integration-test-bearer-token"),
				},
			}

			By("Creating a HumioTelemetryExport resource that references the collection")
			export := &humiov1alpha1.HumioTelemetryExport{
				ObjectMeta: metav1.ObjectMeta{
					Name:      exportName,
					Namespace: testNamespace,
				},
				Spec: humiov1alpha1.HumioTelemetryExportSpec{
					RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
						URL: testServer.URL, // Use test server URL
						Token: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
								Key:                  "token",
							},
						},
					},
					SendCollectionErrors: func() *bool { b := true; return &b }(),
					RegisteredCollections: []humiov1alpha1.HumioTelemetryCollectionReference{
						{
							Name: collectionName,
							// Namespace defaults to export's namespace (same as collection)
						},
					},
				},
			}

			// Create resources in order
			Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())
			Expect(k8sClient.Create(ctx, collection)).Should(Succeed())
			Expect(k8sClient.Create(ctx, export)).Should(Succeed())

			By("Verifying HumioTelemetryCollection status shows proper state")
			Eventually(func() string {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: collectionName, Namespace: testNamespace}, found) != nil {
					return ""
				}
				return found.Status.State
			}, testTimeout, suite.TestInterval).Should(Not(BeEmpty()))

			By("Verifying HumioTelemetryExport status shows collection is registered")
			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: exportName, Namespace: testNamespace}, found) != nil {
					return false
				}

				collectionKey := fmt.Sprintf("%s/%s", testNamespace, collectionName)
				collectionStatus, exists := found.Status.RegisteredCollectionStatus[collectionKey]
				return exists && collectionStatus.Found
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			By("Verifying HumioTelemetryCollection can discover the registered exporter")
			Eventually(func() int {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: collectionName, Namespace: testNamespace}, found) != nil {
					return 0
				}
				return len(found.Status.ExportPushResults)
			}, testTimeout, suite.TestInterval).Should(BeNumerically(">", 0))

			By("Verifying immediate push architecture works (collection pushes to export directly)")
			// Note: In a real environment with actual HumioClusters, the collection would
			// collect data and push to the export. In this test environment, we verify
			// the discovery mechanism works and status tracking is proper.

			Eventually(func() bool {
				found := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: collectionName, Namespace: testNamespace}, found) != nil {
					return false
				}

				// Check if we have export push results indicating discovery worked
				for _, result := range found.Status.ExportPushResults {
					if result.ExporterName == exportName && result.ExporterNamespace == testNamespace {
						return true
					}
				}
				return false
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			By("Cleaning up test resources")
			_ = k8sClient.Delete(ctx, export)
			_ = k8sClient.Delete(ctx, collection)
			_ = k8sClient.Delete(ctx, cluster)
			_ = k8sClient.Delete(ctx, tokenSecret)
		})

		It("should support many-to-many relationships between collections and exports", func() {
			ctx := context.Background()

			// Create multiple collections and exports to test many-to-many relationships
			collectionNames := []string{"collection-1", "collection-2", "collection-3"}
			exportNames := []string{"export-1", "export-2"}

			By("Creating multiple HumioCluster resources for telemetry collections")
			for _, name := range collectionNames {
				cluster := &humiov1alpha1.HumioCluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("managed-%s", name),
						Namespace: testNamespace,
					},
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							Image:     "humio/humio-core:1.70.0",
							NodeCount: 1,
						},
						TargetReplicationFactor: 1,
						StoragePartitionsCount:  24,
						DigestPartitionsCount:   24,
					},
				}
				Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			}

			By("Creating multiple HumioTelemetryCollection resources")
			for _, name := range collectionNames {
				collection := &humiov1alpha1.HumioTelemetryCollection{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: testNamespace,
					},
					Spec: humiov1alpha1.HumioTelemetryCollectionSpec{
						ClusterIdentifier:  "many-to-many-test",
						ManagedClusterName: fmt.Sprintf("managed-%s", name),
						Collections: []humiov1alpha1.CollectionConfig{
							{
								Interval: "10m",
								Include:  []string{"license", "cluster_info"},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, collection)).Should(Succeed())
			}

			By("Creating multiple HumioTelemetryExport resources with different collection references")
			for i, exportName := range exportNames {
				// Create token secret for each export
				tokenSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("token-%d", i+1),
						Namespace: testNamespace,
					},
					Data: map[string][]byte{
						"token": []byte(fmt.Sprintf("test-token-%d", i+1)),
					},
				}
				Expect(k8sClient.Create(ctx, tokenSecret)).Should(Succeed())

				// Configure different collection references for each export
				var registeredCollections []humiov1alpha1.HumioTelemetryCollectionReference
				if i == 0 {
					// Export-1 consumes from collection-1 and collection-2
					registeredCollections = []humiov1alpha1.HumioTelemetryCollectionReference{
						{Name: "collection-1"},
						{Name: "collection-2"},
					}
				} else {
					// Export-2 consumes from collection-2 and collection-3
					registeredCollections = []humiov1alpha1.HumioTelemetryCollectionReference{
						{Name: "collection-2"},
						{Name: "collection-3"},
					}
				}

				export := &humiov1alpha1.HumioTelemetryExport{
					ObjectMeta: metav1.ObjectMeta{
						Name:      exportName,
						Namespace: testNamespace,
					},
					Spec: humiov1alpha1.HumioTelemetryExportSpec{
						RemoteReport: humiov1alpha1.HumioTelemetryRemoteReportConfig{
							URL: fmt.Sprintf("https://telemetry-%d.example.com", i+1),
							Token: humiov1alpha1.VarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: fmt.Sprintf("token-%d", i+1)},
									Key:                  "token",
								},
							},
						},
						RegisteredCollections: registeredCollections,
					},
				}
				Expect(k8sClient.Create(ctx, export)).Should(Succeed())
			}

			By("Verifying export-1 registers collection-1 and collection-2")
			Eventually(func() bool {
				export1 := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: "export-1", Namespace: testNamespace}, export1) != nil {
					return false
				}

				collection1Key := fmt.Sprintf("%s/%s", testNamespace, "collection-1")
				collection2Key := fmt.Sprintf("%s/%s", testNamespace, "collection-2")
				collection1Status, exists1 := export1.Status.RegisteredCollectionStatus[collection1Key]
				collection2Status, exists2 := export1.Status.RegisteredCollectionStatus[collection2Key]

				return exists1 && collection1Status.Found && exists2 && collection2Status.Found
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			By("Verifying export-2 registers collection-2 and collection-3")
			Eventually(func() bool {
				export2 := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: "export-2", Namespace: testNamespace}, export2) != nil {
					return false
				}

				collection2Key := fmt.Sprintf("%s/%s", testNamespace, "collection-2")
				collection3Key := fmt.Sprintf("%s/%s", testNamespace, "collection-3")
				collection2Status, exists2 := export2.Status.RegisteredCollectionStatus[collection2Key]
				collection3Status, exists3 := export2.Status.RegisteredCollectionStatus[collection3Key]

				return exists2 && collection2Status.Found && exists3 && collection3Status.Found
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			By("Verifying collection-2 discovers both exporters (many-to-many relationship)")
			Eventually(func() bool {
				collection2 := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: "collection-2", Namespace: testNamespace}, collection2) != nil {
					return false
				}

				foundExport1 := false
				foundExport2 := false
				for _, result := range collection2.Status.ExportPushResults {
					if result.ExporterName == "export-1" {
						foundExport1 = true
					}
					if result.ExporterName == "export-2" {
						foundExport2 = true
					}
				}

				return foundExport1 && foundExport2
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			By("Cleaning up many-to-many test resources")
			for _, name := range exportNames {
				export := &humiov1alpha1.HumioTelemetryExport{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, export) == nil {
					_ = k8sClient.Delete(ctx, export)
				}
			}
			for _, name := range collectionNames {
				collection := &humiov1alpha1.HumioTelemetryCollection{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, collection) == nil {
					_ = k8sClient.Delete(ctx, collection)
				}
				cluster := &humiov1alpha1.HumioCluster{}
				if k8sClient.Get(ctx, types.NamespacedName{Name: fmt.Sprintf("managed-%s", name), Namespace: testNamespace}, cluster) == nil {
					_ = k8sClient.Delete(ctx, cluster)
				}
			}
		})
	})

})

// Helper functions
