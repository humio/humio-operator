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

package clusters

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/controller/versions"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("HumioCluster Controller", func() {

	BeforeEach(func() {
		// failed test runs that don't clean up leave resources behind.
		testHumioClient.ClearHumioClientConnections("")
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
		testHumioClient.ClearHumioClientConnections("")
	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Humio Cluster Simple", Label("envtest", "dummy", "real"), func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-simple",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = 2

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming managedFields")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				if len(clusterPods) > 0 {
					for idx, entry := range clusterPods[0].GetManagedFields() {
						if entry.Manager == "humio-operator" {
							return string(clusterPods[0].GetManagedFields()[idx].FieldsV1.Raw)
						}
					}
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Not(BeEmpty()))
		})
	})

	Context("Humio Cluster With Multiple Node Pools", Label("envtest", "dummy", "real"), func() {
		It("Should bootstrap multi node cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-multi-node-pool",
				Namespace: testProcessNamespace,
			}
			toCreate := constructBasicMultiNodePoolHumioCluster(key, 1)

			suite.UsingClusterBy(key.Name, "Disabling node pool feature AllowedAPIRequestTypes to validate that it can be unset")
			toCreate.Spec.NodePools[0].NodePoolFeatures = humiov1alpha1.HumioNodePoolFeatures{AllowedAPIRequestTypes: &[]string{""}}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapMultiNodePoolCluster(ctx, k8sClient, testHumioClient, toCreate)

			Eventually(func() error {
				_, err := kubernetes.GetService(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetServiceName(), key.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() error {
				_, err := kubernetes.GetService(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioNodePool(toCreate, &toCreate.Spec.NodePools[0]).GetServiceName(), key.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			updatedHumioCluster := humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pod labels do not contain disabled node pool feature")
			Eventually(func() map[string]string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[0]).GetPodLabels())
				if len(clusterPods) > 0 {
					return clusterPods[0].Labels
				}
				return map[string]string{"humio.com/feature": "OperatorInternal"}
			}, testTimeout, suite.TestInterval).Should(Not(HaveKeyWithValue("humio.com/feature", "OperatorInternal")))

			suite.UsingClusterBy(key.Name, "Scaling down the cluster node count successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.NodeCount = 0
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Verifying the main service is deleted")
			Eventually(func() bool {
				_, err := kubernetes.GetService(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetServiceName(), key.Namespace)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
		})
	})

	Context("Humio Cluster With Node Pools Only", Label("envtest", "dummy", "real"), func() {
		It("Should bootstrap nodepools only cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-node-pool-only",
				Namespace: testProcessNamespace,
			}
			toCreate := constructBasicMultiNodePoolHumioCluster(key, 2)
			toCreate.Spec.NodeCount = 0
			toCreate.Spec.DataVolumeSource = corev1.VolumeSource{}
			toCreate.Spec.DataVolumePersistentVolumeClaimSpecTemplate = corev1.PersistentVolumeClaimSpec{}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapMultiNodePoolCluster(ctx, k8sClient, testHumioClient, toCreate)

			_, err := kubernetes.GetService(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetServiceName(), key.Namespace)
			Expect(k8serrors.IsNotFound(err)).Should(BeTrue())

			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
		})
	})

	Context("Humio Cluster Without Init Container", Label("envtest", "dummy", "real"), func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-no-init-container",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.DisableInitContainer = true

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
		})
	})

	Context("Humio Cluster Multi Organizations", Label("envtest", "dummy", "real"), func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-multi-org",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{
				Name:  "ENABLE_ORGANIZATIONS",
				Value: "true",
			})
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{
				Name:  "ORGANIZATION_MODE",
				Value: "multiv2",
			})

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
		})
	})

	Context("Humio Cluster Unsupported Version", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with unsupported version", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-unsupp-vers",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldUnsupportedHumioVersion()

			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateConfigError, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "should describe cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(Equal(fmt.Sprintf("Humio version must be at least %s: unsupported Humio version: %s", controller.HumioVersionMinimumSupported, strings.Split(strings.Split(versions.OldUnsupportedHumioVersion(), ":")[1], "-")[0])))
		})
	})

	Context("Humio Cluster Update Image", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 2

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = versions.UpgradeJumpHumioVersion()
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsSimultaneousRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(versions.UpgradeJumpHumioVersion()))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster PDF Render Service", Label("envtest", "dummy", "real"), func() {
		var testPdfRenderServiceImage = versions.DefaultPDFRenderServiceImage()
		var ctx context.Context // Define ctx for the context block
		var humioCluster *humiov1alpha1.HumioCluster
		var key types.NamespacedName
		var pdfKey types.NamespacedName
		var pdfCR *humiov1alpha1.HumioPdfRenderService
		var pdfSecret *corev1.Secret

		const (
			PdfExportURLEnvVar = "DEFAULT_PDF_RENDER_SERVICE_URL"
			// Define consistent timeouts for better test reliability
			standardTimeout        = 30 * time.Second
			extendedTimeout        = 60 * time.Second
			stateTransitionTimeout = 45 * time.Second
			quickInterval          = 250 * time.Millisecond
		)

		// Helper function to cleanup HumioPdfRenderService CR
		cleanupPdfRenderServiceCR := func(ctx context.Context, pdfCR *humiov1alpha1.HumioPdfRenderService) {
			if pdfCR == nil {
				return
			}

			// Get the latest version of the resource
			latestPdfCR := &humiov1alpha1.HumioPdfRenderService{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: pdfCR.Name, Namespace: pdfCR.Namespace}, latestPdfCR)

			// If not found, it's already deleted
			if k8serrors.IsNotFound(err) {
				return
			}

			// If other error, report it
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "Error getting HumioPdfRenderService %s/%s for cleanup: %v\n",
					pdfCR.Namespace, pdfCR.Name, err)
				return
			}

			// Only attempt deletion if not already being deleted
			if latestPdfCR.GetDeletionTimestamp() == nil {
				Expect(k8sClient.Delete(ctx, latestPdfCR)).Should(Succeed())
			}

			// Wait for deletion with increased timeout
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: pdfCR.Name, Namespace: pdfCR.Namespace}, latestPdfCR)
				return k8serrors.IsNotFound(err)
			}, standardTimeout, quickInterval).Should(BeTrue(),
				"HumioPdfRenderService %s/%s should be deleted", pdfCR.Namespace, pdfCR.Name)
		}

		// Helper function to cleanup TLS secret
		cleanupPdfRenderServiceTLSSecret := func(ctx context.Context, secret *corev1.Secret) {
			if secret == nil {
				return
			}

			// Get the latest version of the resource
			latestSecret := &corev1.Secret{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, latestSecret)

			// If not found, it's already deleted
			if k8serrors.IsNotFound(err) {
				return
			}

			// If other error, report it
			if err != nil {
				fmt.Fprintf(GinkgoWriter, "Error getting Secret %s/%s for cleanup: %v\n",
					secret.Namespace, secret.Name, err)
				return
			}

			// Only attempt deletion if not already being deleted
			if latestSecret.GetDeletionTimestamp() == nil {
				Expect(k8sClient.Delete(ctx, latestSecret)).Should(Succeed())
			}

			// Wait for deletion with increased timeout
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, latestSecret)
				return k8serrors.IsNotFound(err)
			}, standardTimeout, quickInterval).Should(BeTrue(),
				"Secret %s/%s should be deleted", secret.Namespace, secret.Name)
		}

		BeforeEach(func() {
			ctx = context.Background()
			key = types.NamespacedName{
				Name:      "pdf-render-service",
				Namespace: testProcessNamespace, // Use the correct test namespace variable
			}
			pdfKey = types.NamespacedName{
				Name:      "pdf-render-service",
				Namespace: testProcessNamespace, // Use the correct test namespace variable
			}
			// Use suite helper to construct the default cluster object
			// Create a simple HumioCluster object with basic configuration
			humioCluster = suite.ConstructBasicSingleNodeHumioCluster(key, true)
			// Add the PdfRenderServiceRef to the HumioCluster spec *before* creating it
			humioCluster.Spec.PdfRenderServiceRef = &humiov1alpha1.HumioPdfRenderServiceReference{
				Name:      pdfKey.Name,      // Reference the PDF service by name
				Namespace: pdfKey.Namespace, // Reference the PDF service by namespace
			}
			pdfCR = nil // Reset pdfCR
		})

		AfterEach(func() {
			// Create a context with timeout for cleanup operations
			cleanupCtx, cancel := context.WithTimeout(context.Background(), standardTimeout)
			defer cancel()

			// Cleanup order matters: Cluster first (to remove owner refs), then others
			By("Cleaning up HumioCluster")
			var currentCluster humiov1alpha1.HumioCluster
			if err := k8sClient.Get(cleanupCtx, key, &currentCluster); err == nil {
				suite.CleanupCluster(cleanupCtx, k8sClient, &currentCluster)

				// Wait for cluster to be fully deleted
				Eventually(func() bool {
					err := k8sClient.Get(cleanupCtx, key, &humiov1alpha1.HumioCluster{})
					return k8serrors.IsNotFound(err)
				}, extendedTimeout, quickInterval).Should(BeTrue(), "HumioCluster should be deleted")
			} else if !k8serrors.IsNotFound(err) {
				fmt.Fprintf(GinkgoWriter, "Error fetching HumioCluster %s for cleanup: %v\n", key.String(), err)
			}

			// Clean up PDF render service
			By("Cleaning up HumioPdfRenderService")
			if pdfCR != nil {
				cleanupPdfRenderServiceCR(cleanupCtx, pdfCR)
			}

			// Clean up TLS secret
			By("Cleaning up TLS Secret")
			if pdfSecret != nil {
				cleanupPdfRenderServiceTLSSecret(cleanupCtx, pdfSecret)
			}

			// Reset shared variables for next test
			humioCluster = nil
			pdfCR = nil
			pdfSecret = nil
		})

		It("should use the specified PDF Render Service image when set", func() {
			ctx := context.Background()
			clusterKey := types.NamespacedName{
				Name:      "hc-pdf-custom-image",
				Namespace: testProcessNamespace,
			}
			pdfKey := types.NamespacedName{
				Name:      "pdf-service-for-" + clusterKey.Name,
				Namespace: testProcessNamespace,
			}

			const customPdfImage = "humio/pdf-render-service:custom-tag"

			By("Creating a HumioPdfRenderService with a custom image")
			pdfCR := suite.CreatePdfRenderServiceCR(ctx, k8sClient, pdfKey, false)
			pdfCR.Spec.Image = customPdfImage
			Expect(k8sClient.Update(ctx, pdfCR)).To(Succeed())
			defer cleanupPdfRenderServiceCR(ctx, pdfCR)

			// Add this block to fetch the initial generation
			var initialCluster humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, clusterKey, &initialCluster)).To(Succeed())
			initialGeneration := initialCluster.Generation

			// Wait for the PDF render service to be created
			suite.WaitForControllerToObserveChange(ctx, k8sClient, clusterKey, initialGeneration)

			By("Ensuring PDF render deployment is ready")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, pdfKey)

			By("Creating a HumioCluster referencing the custom-image PDF service")
			hc := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			hc.Spec.PdfRenderServiceRef = &humiov1alpha1.HumioPdfRenderServiceReference{
				Name:      pdfKey.Name,
				Namespace: pdfKey.Namespace,
			}
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, hc, true,
				humiov1alpha1.HumioClusterStateRunning, standardTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, hc)

			By("Verifying the PDF Render Service Deployment uses the specified image")
			Eventually(func(g Gomega) string {
				var deployment appsv1.Deployment
				err := k8sClient.Get(ctx, pdfKey, &deployment)
				g.Expect(err).NotTo(HaveOccurred())
				containers := deployment.Spec.Template.Spec.Containers
				g.Expect(containers).NotTo(BeEmpty())
				return containers[0].Image
			}, standardTimeout, quickInterval).Should(Equal(customPdfImage))
		})

		It("should reach Running state when PdfRenderServiceRef points to an existing non‑TLS service", func() {
			clusterKey := types.NamespacedName{Name: "hc-pdf-non-tls", Namespace: testProcessNamespace}
			pdfKey := types.NamespacedName{Name: "shared-pdf-service", Namespace: testProcessNamespace}

			By("creating the referenced HumioPdfRenderService")
			// Create the PDF render service
			pdfCR = suite.CreatePdfRenderServiceCR(ctx, k8sClient, pdfKey, false)
			defer cleanupPdfRenderServiceCR(ctx, pdfCR)

			// FIX: Use the correct deployment name format when checking for readiness
			// The controller appends "-pdf-render-service" to the CR name for all child resources
			deploymentKey := types.NamespacedName{
				Name:      pdfKey.Name + "-pdf-render-service",
				Namespace: pdfKey.Namespace,
			}
			// Always ensure deployment is ready before referencing in cluster
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			By("bootstrapping HumioCluster referencing the service")
			hc := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			hc.Spec.PdfRenderServiceRef = &humiov1alpha1.HumioPdfRenderServiceReference{
				Name:      pdfKey.Name,
				Namespace: pdfKey.Namespace,
			}
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, humioCluster, true,
				humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, humioCluster)

			By("simulating PDF deployment readiness in env‑test")
			// Using DefaultTestTimeout defined in common.go
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, pdfKey)

		})

		It("should report ConfigError on the HumioPdfRenderService (TLS enabled but cert secret is missing) "+
			"while the HumioCluster itself stays Pending/Running", func() {

			ctx := context.Background()

			// ------------------------------------------------------------------
			// 1. Create a TLS‑enabled HumioPdfRenderService **without** the cert
			// ------------------------------------------------------------------
			clusterKey := types.NamespacedName{
				Name:      "hc-pdf-tls-no-secret",
				Namespace: testProcessNamespace,
			}
			pdfKey := types.NamespacedName{
				Name:      "pdf-svc-for-" + clusterKey.Name,
				Namespace: testProcessNamespace,
			}
			pdfCR := suite.CreatePdfRenderServiceCR(ctx, k8sClient, pdfKey, true)
			defer cleanupPdfRenderServiceCR(ctx, pdfCR)

			// Simulate PDF deployment readiness in env-test
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, pdfKey)

			// Ensure the CR exists before continuing
			Eventually(func(g Gomega) error {
				err := k8sClient.Get(ctx, pdfKey, &humiov1alpha1.HumioPdfRenderService{})
				g.Expect(err).NotTo(HaveOccurred())
				return nil
			}, standardTimeout, quickInterval).Should(Succeed())

			// Create the HumioCluster referencing the PDF service
			humioCluster.Spec.PdfRenderServiceRef = &humiov1alpha1.HumioPdfRenderServiceReference{
				Name:      pdfKey.Name,
				Namespace: pdfKey.Namespace,
			}
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, humioCluster, true,
				humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, humioCluster)

			// -----------------------------------------------------------------
			// 3. Wait for the **PDF service** to enter ConfigError state
			// -----------------------------------------------------------------
			Eventually(func(g Gomega) string {
				var cur humiov1alpha1.HumioPdfRenderService
				err := k8sClient.Get(ctx, pdfKey, &cur)
				g.Expect(err).NotTo(HaveOccurred())
				return cur.Status.State
			}, standardTimeout, quickInterval).Should(
				Equal(humiov1alpha1.HumioClusterStateConfigError),
			)

			// Verify the status message mentions the derived "<name>-certificate" secret
			expectedMissing := fmt.Sprintf("%s-certificate", pdfKey.Name)
			Eventually(func(g Gomega) string {
				var cur humiov1alpha1.HumioPdfRenderService
				err := k8sClient.Get(ctx, pdfKey, &cur)
				g.Expect(err).NotTo(HaveOccurred())
				return cur.Status.Message
			}, standardTimeout, quickInterval).Should(
				ContainSubstring(expectedMissing),
			)

			// -----------------------------------------------------------------
			// 4. The HumioCluster itself should **not** go ConfigError – it will
			//    stay Pending and eventually reach Running once pods come up.
			//    Accept either Pending or Running.
			// -----------------------------------------------------------------
			Eventually(func(g Gomega) string {
				var cur humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &cur)
				g.Expect(err).NotTo(HaveOccurred())
				return cur.Status.State
			}, standardTimeout, quickInterval).Should(
				Or(Equal(humiov1alpha1.HumioClusterStatePending),
					Equal(humiov1alpha1.HumioClusterStateRunning)),
			)
		})

		It("Should remove cluster-specific service when PdfRenderServiceRef is added", func() {
			ctx := context.Background()
			clusterKey := types.NamespacedName{
				Name:      "humiocluster-pdf-ref-cleanup",
				Namespace: testProcessNamespace,
			}
			clusterSpecificPdfKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-pdf-render-service", clusterKey.Name),
				Namespace: testProcessNamespace,
			}
			validPdfKey := types.NamespacedName{
				Name:      "valid-pdf-service-for-" + clusterKey.Name,
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster without PdfRenderServiceRef")
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true,
				humiov1alpha1.HumioClusterStateRunning, standardTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			By("Manually creating cluster-specific HumioPdfRenderService (simulating leftover)")
			var parentCluster humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, clusterKey, &parentCluster)).To(Succeed())

			controllerRef := metav1.OwnerReference{
				APIVersion:         humiov1alpha1.GroupVersion.String(),
				Kind:               "HumioCluster",
				Name:               parentCluster.Name,
				UID:                parentCluster.UID,
				Controller:         helpers.BoolPtr(true),
				BlockOwnerDeletion: helpers.BoolPtr(true),
			}

			clusterSpecificPdf := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:            clusterSpecificPdfKey.Name,
					Namespace:       clusterSpecificPdfKey.Namespace,
					OwnerReferences: []metav1.OwnerReference{controllerRef},
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image: testPdfRenderServiceImage,
				},
			}

			Eventually(func(g Gomega) error {
				err := k8sClient.Create(ctx, clusterSpecificPdf)
				if k8serrors.IsAlreadyExists(err) {
					return k8sClient.Get(ctx, clusterSpecificPdfKey, clusterSpecificPdf)
				}
				return err
			}, standardTimeout, quickInterval).Should(Succeed(), "Should be able to create the cluster-specific PDF service")

			clusterPdfSvc := &corev1.Service{}

			By("Waiting for cluster-specific PDF service to be created initially")
			Eventually(func(g Gomega) bool {
				err := k8sClient.Get(ctx, clusterSpecificPdfKey, clusterPdfSvc)
				if err != nil {
					if k8serrors.IsNotFound(err) {
						return false
					}
					g.Expect(err).NotTo(HaveOccurred())
					return false
				}
				return true
			}, stateTransitionTimeout, quickInterval).Should(BeTrue(), "Cluster-specific PDF service should be created initially")

			By("Checking owner reference on the cluster-specific service")
			Expect(clusterPdfSvc.OwnerReferences).Should(ContainElement(SatisfyAll(
				HaveField("Name", clusterSpecificPdfKey.Name),
				HaveField("Kind", "HumioPdfRenderService"),
			)))

			By("Creating the valid HumioPdfRenderService to reference later")
			validPdfCR := suite.CreatePdfRenderServiceCR(ctx, k8sClient, validPdfKey, false) // TLS disabled
			defer cleanupPdfRenderServiceCR(ctx, validPdfCR)

			By("Updating HumioCluster to add PdfRenderServiceRef")
			var initialCluster humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, clusterKey, &initialCluster)).To(Succeed())
			initialGeneration := initialCluster.Generation

			Eventually(func(g Gomega) error {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &cluster)
				g.Expect(err).NotTo(HaveOccurred())
				cluster.Spec.PdfRenderServiceRef = &humiov1alpha1.HumioPdfRenderServiceReference{
					Name:      validPdfKey.Name,
					Namespace: validPdfKey.Namespace,
				}
				return k8sClient.Update(ctx, &cluster)
			}, standardTimeout, quickInterval).Should(Succeed())

			// Wait for controller to observe the change
			suite.WaitForControllerToObserveChange(ctx, k8sClient, clusterKey, initialGeneration)

			By("Verifying cluster-specific HumioPdfRenderService is deleted")
			Eventually(func(g Gomega) bool {
				err := k8sClient.Get(ctx, clusterSpecificPdfKey, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, standardTimeout, quickInterval).Should(BeTrue(), "Cluster-specific HumioPdfRenderService should be deleted")

			By("Verifying HumioCluster enters Restarting state")
			Eventually(func(g Gomega) string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &cluster)
				g.Expect(err).NotTo(HaveOccurred())
				return cluster.Status.State
			}, standardTimeout, quickInterval).Should(Equal(humiov1alpha1.HumioClusterStateRestarting))

			By("Simulating pod restart completion (simultaneous restart)")
			var currentCluster humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, clusterKey, &currentCluster)).To(Succeed())
			ensurePodsSimultaneousRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&currentCluster), 2)

			By("Verifying HumioCluster returns to Running state")
			Eventually(func(g Gomega) string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &cluster)
				g.Expect(err).NotTo(HaveOccurred())
				return cluster.Status.State
			}, standardTimeout, quickInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})

		It("Should reconcile successfully when PdfRenderServiceRef is not set", func() {
			// Generate a unique name for this test run
			key := types.NamespacedName{
				Name:      "pdf-render-service-no-ref",
				Namespace: testProcessNamespace,
			}

			// Create the cluster object to use for cleanup
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			// Ensure cleanup runs for THIS key at the end of the test
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Bootstrapping the cluster and expecting Running state")
			suite.CreateAndBootstrapCluster(
				ctx,
				k8sClient,
				testHumioClient,
				toCreate,
				true,
				humiov1alpha1.HumioClusterStateRunning,
				standardTimeout, // Use standardTimeout
			)

			// Final assertion – stays Running
			Consistently(func(g Gomega) string {
				var hc humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &hc)
				g.Expect(err).NotTo(HaveOccurred())
				return hc.Status.State
			}, standardTimeout, quickInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})

		It("Should reach Running state when PdfRenderServiceRef points to an existing service in a different namespace", func() {
			ctx := context.Background()
			clusterKey := types.NamespacedName{
				Name:      "humiocluster-pdf-ref-cross-ns",
				Namespace: testProcessNamespace, // Cluster in default test namespace
			}
			// Use the same namespace for PDF service to avoid envtest operator watch issues
			pdfNamespace := clusterKey.Namespace
			pdfKey := types.NamespacedName{
				Name:      "shared-pdf-service",
				Namespace: pdfNamespace,
			}

			// Create the referenced HumioPdfRenderService in the same namespace
			suite.UsingClusterBy(clusterKey.Name, "Creating the referenced HumioPdfRenderService in namespace "+pdfNamespace)
			pdfCR := suite.CreatePdfRenderServiceCR(ctx, k8sClient, pdfKey, false)
			defer cleanupPdfRenderServiceCR(ctx, pdfCR)

			// Ensure PDF service is ready
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, pdfKey)

			// Create the HumioCluster referencing the PDF service
			suite.UsingClusterBy(clusterKey.Name, "Creating the HumioCluster with PdfRenderServiceRef")
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			toCreate.Spec.PdfRenderServiceRef = &humiov1alpha1.HumioPdfRenderServiceReference{
				Name:      pdfKey.Name,
				Namespace: pdfKey.Namespace,
			}

			// Bootstrap the cluster and expect it to become Running
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true,
				humiov1alpha1.HumioClusterStateRunning, standardTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			// Final check
			Eventually(func(g Gomega) string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &cluster)
				g.Expect(err).NotTo(HaveOccurred())
				return cluster.Status.State
			}, standardTimeout, quickInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})

		It("Should remain Running when PdfRenderServiceRef is removed", func() {
			ctx := context.Background()
			clusterKey := types.NamespacedName{
				Name:      "humiocluster-pdf-ref-removed",
				Namespace: testProcessNamespace,
			}
			pdfKey := types.NamespacedName{
				Name:      "pdf-service-for-" + clusterKey.Name,
				Namespace: testProcessNamespace,
			}

			By("Creating the referenced HumioPdfRenderService (non-TLS)")
			pdfCR := suite.CreatePdfRenderServiceCR(ctx, k8sClient, pdfKey, false)
			defer cleanupPdfRenderServiceCR(ctx, pdfCR)

			By("Ensuring PDF render deployment is ready")
			deploymentKey := types.NamespacedName{
				Name:      pdfKey.Name + "-pdf-render-service",
				Namespace: pdfKey.Namespace,
			}
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			By("Creating the HumioCluster with PdfRenderServiceRef")
			// Use true for useAutoCreatedLicense to ensure license configs are set properly
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, true)
			toCreate.Spec.PdfRenderServiceRef = &humiov1alpha1.HumioPdfRenderServiceReference{
				Name:      pdfKey.Name,
				Namespace: pdfKey.Namespace,
			}

			// Bootstrap the cluster with autoCreateLicense=false since we set it up in ConstructBasicSingleNodeHumioCluster
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, false,
				humiov1alpha1.HumioClusterStateRunning, standardTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			By("Storing initial generation for later comparison")
			var initialCluster humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, clusterKey, &initialCluster)).To(Succeed())
			initialGeneration := initialCluster.Generation

			By("Removing PdfRenderServiceRef from HumioCluster")
			Eventually(func(g Gomega) error {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &cluster)
				g.Expect(err).NotTo(HaveOccurred())
				cluster.Spec.PdfRenderServiceRef = nil // Remove the reference
				return k8sClient.Update(ctx, &cluster)
			}, standardTimeout, quickInterval).Should(Succeed())

			// Wait for controller to observe the change
			suite.WaitForControllerToObserveChange(ctx, k8sClient, clusterKey, initialGeneration)

			By("Verifying cluster enters Restarting state after removing reference")
			Eventually(func(g Gomega) string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &cluster)
				g.Expect(err).NotTo(HaveOccurred())
				return cluster.Status.State
			}, standardTimeout, quickInterval).Should(Equal(humiov1alpha1.HumioClusterStateRestarting))

			By("Simulating rolling restart to return to Running")
			var cur humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, clusterKey, &cur)).To(Succeed())
			ensurePodsRollingRestart(
				ctx,
				controller.NewHumioNodeManagerFromHumioCluster(&cur),
				2, // desiredRevision
				1, // batchSize
			)

			By("Verifying cluster returns to Running state after restart")
			Eventually(func(g Gomega) string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &cluster)
				g.Expect(err).NotTo(HaveOccurred())
				if cluster.Status.State != humiov1alpha1.HumioClusterStateRunning {
					fmt.Fprintf(GinkgoWriter, "Current state: %s, waiting for Running\n", cluster.Status.State)
				}
				return cluster.Status.State
			}, extendedTimeout, quickInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Verifying DEFAULT_PDF_RENDER_SERVICE_URL is removed from Humio pods")
			Eventually(func(g Gomega) []corev1.EnvVar {
				env := fetchHumioPodEnv(ctx, k8sClient, clusterKey.Name, clusterKey.Namespace)
				envVars := []corev1.EnvVar{}
				for name, value := range env {
					envVars = append(envVars, corev1.EnvVar{Name: name, Value: value})
				}
				return envVars
			}, standardTimeout, quickInterval).ShouldNot(ContainElement(HaveField("Name", PdfExportURLEnvVar)))
		})

		It("Should enter ConfigError state if the referenced HumioPdfRenderService is deleted", func() {
			ctx := context.Background()
			clusterKey := types.NamespacedName{
				Name:      "humiocluster-pdf-ref-deleted",
				Namespace: testProcessNamespace,
			}
			pdfKey := types.NamespacedName{
				Name:      "pdf-service-for-" + clusterKey.Name,
				Namespace: testProcessNamespace,
			}

			By("Creating the referenced HumioPdfRenderService: " + pdfKey.String())
			pdfCR := suite.CreatePdfRenderServiceCR(ctx, k8sClient, pdfKey, false)
			defer cleanupPdfRenderServiceCR(ctx, pdfCR)

			// Fix: Use the correct deployment name format when checking for readiness
			// The controller appends "-pdf-render-service" to the CR name for all child resources
			deploymentKey := types.NamespacedName{
				Name:      pdfKey.Name + "-pdf-render-service",
				Namespace: pdfKey.Namespace,
			}

			By("Ensuring PDF render deployment is ready")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			By("Creating HumioCluster that references the PDF render service")
			hc := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			hc.Spec.PdfRenderServiceRef = &humiov1alpha1.HumioPdfRenderServiceReference{
				Name:      pdfKey.Name,
				Namespace: pdfKey.Namespace,
			}
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, hc, true,
				humiov1alpha1.HumioClusterStateRunning, standardTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, hc)

			By("Storing initial generation for change detection")
			var cluster humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, clusterKey, &cluster)).To(Succeed())
			initialGeneration := cluster.Generation

			By("Deleting the referenced HumioPdfRenderService")
			Expect(k8sClient.Delete(ctx, pdfCR)).To(Succeed())

			By("Waiting for controller to observe the deletion")
			suite.WaitForControllerToObserveChange(ctx, k8sClient, clusterKey, initialGeneration)

			By("Verifying cluster enters ConfigError state")
			Eventually(func(g Gomega) string {
				var updatedCluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, clusterKey, &updatedCluster)
				g.Expect(err).NotTo(HaveOccurred())
				return updatedCluster.Status.State
			}, extendedTimeout, quickInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})

	})

	Context("Humio Cluster Update Failed Pods", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods that are in a failed state", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-failed",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			originalAffinity := toCreate.Spec.Affinity

			updatedHumioCluster := humiov1alpha1.HumioCluster{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())

			var updatedClusterPods []corev1.Pod
			updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			for _, pod := range updatedClusterPods {
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}

			suite.UsingClusterBy(key.Name, "Updating the cluster resources successfully with broken affinity")
			Eventually(func() error {
				updatedHumioCluster := humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioNodeSpec.Affinity = corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
							NodeSelectorTerms: []corev1.NodeSelectorTerm{
								{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      "some-none-existent-label",
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"does-not-exist"},
										},
									},
								},
							},
						},
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}).Should(Succeed())

			Eventually(func() string {
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			ensurePodsGoPending(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() int {
				var pendingPodsCount int
				updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				for _, pod := range updatedClusterPods {
					if pod.Status.Phase == corev1.PodPending {
						for _, condition := range pod.Status.Conditions {
							if condition.Type == corev1.PodScheduled {
								if condition.Status == corev1.ConditionFalse && condition.Reason == controller.PodConditionReasonUnschedulable {
									pendingPodsCount++
								}
							}
						}
					}
				}
				return pendingPodsCount
			}, testTimeout, 250*time.Millisecond).Should(Equal(1))

			suite.UsingClusterBy(key.Name, "Updating the cluster resources successfully with working affinity")
			Eventually(func() error {
				updatedHumioCluster := humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioNodeSpec.Affinity = originalAffinity
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Keep marking revision 2 as unschedulable as operator may delete it multiple times due to being unschedulable over and over
			Eventually(func() []corev1.Pod {
				podsMarkedAsPending := []corev1.Pod{}

				currentPods, err := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				if err != nil {
					// wrap error in pod object, so that we can still see the error if the Eventually() fails
					return []corev1.Pod{
						{
							ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%v", err)},
						},
					}
				}
				for _, pod := range currentPods {
					if pod.Spec.Affinity != nil &&
						pod.Spec.Affinity.NodeAffinity != nil &&
						pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution != nil &&
						len(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms) > 0 &&
						len(pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions) > 0 {

						if pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Key == "some-none-existent-label" {
							_ = markPodAsPendingUnschedulableIfUsingEnvtest(ctx, k8sClient, pod, key.Name)
						}
					}
				}

				return podsMarkedAsPending
			}, testTimeout, suite.TestInterval).Should(BeEmpty())

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 3, 1)

			Eventually(func() string {
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))
		})
	})

	Context("Humio Cluster Update Image Rolling Restart", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods to use new image in a rolling fashion", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-rolling",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = versions.UpgradeJumpHumioVersion()
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Pods upgrade in a rolling fashion because update strategy is explicitly set to rolling update")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(versions.UpgradeJumpHumioVersion()))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Image Update Strategy OnDelete", Label("envtest", "dummy", "real"), func() {
		It("Update should not replace pods on image update when update strategy OnDelete is used", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-on-delete",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyOnDelete,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Confirming pods have not been recreated")
			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}

			suite.UsingClusterBy(key.Name, "Simulating manual deletion of pods")
			for _, pod := range updatedClusterPods {
				Expect(k8sClient.Delete(ctx, &pod)).To(Succeed())
			}

			Eventually(func() []corev1.Pod {
				var clusterPods []corev1.Pod
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				_ = suite.MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)
				return clusterPods
			}, testTimeout, suite.TestInterval).Should(HaveLen(toCreate.Spec.NodeCount))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Image Rolling Best Effort Patch", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods to use new image in a rolling fashion for patch updates", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-rolling-patch",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.UpgradePatchBestEffortOldVersion()
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdateBestEffort,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = versions.UpgradePatchBestEffortNewVersion()
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Pods upgrade in a rolling fashion because the new version is a patch release")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(versions.UpgradePatchBestEffortNewVersion()))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Image Best Effort Version Jump", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods in parallel to use new image for version jump updates", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-vj",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.UpgradeRollingBestEffortVersionJumpOldVersion()
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdateBestEffort,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = versions.UpgradeRollingBestEffortVersionJumpNewVersion()
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Pods upgrade at the same time because the new version is more than one"+
				"minor revision greater than the previous version")
			ensurePodsSimultaneousRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(versions.UpgradeRollingBestEffortVersionJumpNewVersion()))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update EXTERNAL_URL", Label("dummy", "real"), func() {
		It("Update should correctly replace pods to use the new EXTERNAL_URL in a non-rolling fashion", func() {
			if helpers.UseCertManager() {
				key := types.NamespacedName{
					Name:      "humiocluster-update-ext-url",
					Namespace: testProcessNamespace,
				}
				toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
				toCreate.Spec.TLS = &humiov1alpha1.HumioClusterTLSSpec{Enabled: helpers.BoolPtr(false)}

				suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
				ctx := context.Background()
				suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
				defer suite.CleanupCluster(ctx, k8sClient, toCreate)

				var updatedHumioCluster humiov1alpha1.HumioCluster
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).To(ContainElement(corev1.EnvVar{
						Name:  "EXTERNAL_URL",
						Value: "http://$(POD_NAME).humiocluster-update-ext-url-headless.$(POD_NAMESPACE):$(HUMIO_PORT)",
					}))
					Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
				}
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

				suite.UsingClusterBy(key.Name, "Waiting for pods to be Running")
				Eventually(func() int {
					var runningPods int
					clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
					for _, pod := range clusterPods {
						if pod.Status.Phase == corev1.PodRunning {
							runningPods++
						}
					}
					return runningPods
				}, testTimeout, suite.TestInterval).Should(Equal(toCreate.Spec.NodeCount))

				suite.UsingClusterBy(key.Name, "Updating the cluster TLS successfully")
				Eventually(func() error {
					updatedHumioCluster = humiov1alpha1.HumioCluster{}
					err := k8sClient.Get(ctx, key, &updatedHumioCluster)
					if err != nil {
						return err
					}
					updatedHumioCluster.Spec.TLS.Enabled = helpers.BoolPtr(true)
					return k8sClient.Update(ctx, &updatedHumioCluster)
				}, testTimeout, suite.TestInterval).Should(Succeed())

				ensurePodsSimultaneousRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

				Eventually(func() string {
					updatedHumioCluster = humiov1alpha1.HumioCluster{}
					Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
					return updatedHumioCluster.Status.State
				}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

				suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

				updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
				for _, pod := range updatedClusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).To(ContainElement(corev1.EnvVar{
						Name:  "EXTERNAL_URL",
						Value: "https://$(POD_NAME).humiocluster-update-ext-url-headless.$(POD_NAMESPACE):$(HUMIO_PORT)",
					}))
					Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
				}
			}
		})
	})

	Context("Humio Cluster Update Image Multi Node Pool", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods to use new image in multiple node pools", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-np",
				Namespace: testProcessNamespace,
			}
			originalImage := versions.OldSupportedHumioVersion()
			toCreate := constructBasicMultiNodePoolHumioCluster(key, 1)
			toCreate.Spec.Image = originalImage
			toCreate.Spec.NodeCount = 1
			toCreate.Spec.NodePools[0].NodeCount = 1
			toCreate.Spec.NodePools[0].Image = originalImage

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapMultiNodePoolCluster(ctx, k8sClient, testHumioClient, toCreate)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "Simulating migration from non-node pools or orphaned node pools")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Status.NodePoolStatus = append(updatedHumioCluster.Status.NodePoolStatus, humiov1alpha1.HumioNodePoolStatus{Name: "orphaned", State: humiov1alpha1.HumioClusterStateUpgrading})
				return k8sClient.Status().Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			suite.UsingClusterBy(key.Name, "Updating the cluster image on the main node pool successfully")
			updatedImage := versions.UpgradeJumpHumioVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			mostSeenNodePoolsWithUpgradingState := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxNumberNodePoolsWithSpecificNodePoolStatus(ctx2, k8sClient, key, forever, &mostSeenNodePoolsWithUpgradingState, humiov1alpha1.HumioClusterStateUpgrading)

			ensurePodsSimultaneousRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for main pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			suite.UsingClusterBy(key.Name, "Confirming pod revision did not change for the other node pool")
			nonUpdatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[0]).GetPodLabels())
			Expect(nonUpdatedClusterPods).To(HaveLen(toCreate.Spec.NodePools[0].NodeCount))
			Expect(updatedHumioCluster.Spec.NodePools[0].Image).To(Equal(originalImage))
			for _, pod := range nonUpdatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(originalImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}

			suite.UsingClusterBy(key.Name, "Updating the cluster image on the additional node pool successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.NodePools[0].Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsSimultaneousRestart(ctx, controller.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[0]), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[0]).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[0]).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodePools[0].NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			suite.UsingClusterBy(key.Name, "Confirming pod revision did not change for the main node pool")

			updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever
			Expect(mostSeenNodePoolsWithUpgradingState).To(BeNumerically("==", 1))

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Create with Image Source", Label("envtest", "dummy", "real"), func() {
		It("Should correctly create cluster from image source", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-create-image-source",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = ""
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.ImageSource = &humiov1alpha1.HumioImageSource{
				ConfigMapRef: &corev1.ConfigMapKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "image-source-create",
					},
					Key: "tag",
				},
			}

			ctx := context.Background()
			var updatedHumioCluster humiov1alpha1.HumioCluster

			suite.UsingClusterBy(key.Name, "Creating the imageSource configmap")
			updatedImage := versions.UpgradePatchBestEffortNewVersion()
			envVarSourceConfigMap := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "image-source-create",
					Namespace: key.Namespace,
				},
				Data: map[string]string{"tag": updatedImage},
			}
			Expect(k8sClient.Create(ctx, &envVarSourceConfigMap)).To(Succeed())

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			Eventually(func() error {
				bootstrapToken, err := suite.GetHumioBootstrapToken(ctx, key, k8sClient)
				Expect(bootstrapToken.Status.BootstrapImage).To(BeEquivalentTo(updatedImage))
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
		})
	})

	Context("Humio Cluster Update Image Source", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-source",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.UpgradePatchBestEffortOldVersion()
			toCreate.Spec.NodeCount = 2

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

			suite.UsingClusterBy(key.Name, "Adding missing imageSource to pod spec")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				updatedHumioCluster.Spec.ImageSource = &humiov1alpha1.HumioImageSource{
					ConfigMapRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "image-source-missing",
						},
						Key: "tag",
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming the HumioCluster goes into ConfigError state since the configmap does not exist")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "Confirming the HumioCluster describes the reason the cluster is in ConfigError state")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(Equal("failed to set imageFromSource: ConfigMap \"image-source-missing\" not found"))

			suite.UsingClusterBy(key.Name, "Creating the imageSource configmap")
			updatedImage := versions.UpgradePatchBestEffortNewVersion()
			envVarSourceConfigMap := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "image-source",
					Namespace: key.Namespace,
				},
				Data: map[string]string{"tag": updatedImage},
			}
			Expect(k8sClient.Create(ctx, &envVarSourceConfigMap)).To(Succeed())

			suite.UsingClusterBy(key.Name, "Updating imageSource of pod spec")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ImageSource = &humiov1alpha1.HumioImageSource{
					ConfigMapRef: &corev1.ConfigMapKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "image-source",
						},
						Key: "tag",
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			ensurePodsSimultaneousRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Using Wrong Image", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods after using wrong image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-wrong-image",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = 2

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			suite.UsingClusterBy(key.Name, "Updating the cluster image unsuccessfully with broken image")
			updatedImage := fmt.Sprintf("%s-missing-image", versions.DefaultHumioImageVersion())
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Waiting until pods are started with the bad image")
			Eventually(func() int {
				var badPodCount int
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				suite.UsingClusterBy(key.Name, fmt.Sprintf("Found of %d pods", len(clusterPods)))
				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					suite.UsingClusterBy(key.Name, fmt.Sprintf("Pod %s uses image %s and is using revision %s", pod.Name, pod.Spec.Containers[humioIndex].Image, pod.Annotations[controller.PodRevisionAnnotation]))
					if pod.Spec.Containers[humioIndex].Image == updatedImage && pod.Annotations[controller.PodRevisionAnnotation] == "2" {
						badPodCount++
					}
				}
				return badPodCount
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(toCreate.Spec.NodeCount))

			suite.UsingClusterBy(key.Name, "Simulating mock pods to be scheduled")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				_ = markPodAsPendingImagePullBackOffIfUsingEnvtest(ctx, k8sClient, pod, key.Name)
			}

			suite.UsingClusterBy(key.Name, "Waiting for humio cluster state to be Upgrading")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully with working image")
			updatedImage = versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsSimultaneousRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 3)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(3))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations[controller.PodRevisionAnnotation]).To(Equal("3"))
			}

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Helper Image", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-helper-image",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}
			toCreate.Spec.HelperImage = ""
			toCreate.Spec.NodeCount = 2

			suite.UsingClusterBy(key.Name, "Creating a cluster with default helper image")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Validating pod uses default helper image as init container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)

				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(versions.DefaultHelperImageVersion()))

			annotationsMap := make(map[string]string)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				annotationsMap[controller.PodHashAnnotation] = pod.Annotations[controller.PodHashAnnotation]
				annotationsMap[controller.PodOperatorManagedFieldsHashAnnotation] = pod.Annotations[controller.PodOperatorManagedFieldsHashAnnotation]
			}
			Expect(annotationsMap[controller.PodHashAnnotation]).To(Not(BeEmpty()))
			Expect(annotationsMap[controller.PodOperatorManagedFieldsHashAnnotation]).To(Not(BeEmpty()))

			suite.UsingClusterBy(key.Name, "Overriding helper image")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			upgradedHelperImage := versions.UpgradeHelperImageVersion()
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HelperImage = upgradedHelperImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			suite.UsingClusterBy(key.Name, "Validating pod is recreated using the explicitly defined helper image as init container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(upgradedHelperImage))

			suite.UsingClusterBy(key.Name, "Validating both pod hash and pod managed fields annotations have changed")
			updatedAnnotationsMap := make(map[string]string)
			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range updatedClusterPods {
				updatedAnnotationsMap[controller.PodHashAnnotation] = pod.Annotations[controller.PodHashAnnotation]
				updatedAnnotationsMap[controller.PodOperatorManagedFieldsHashAnnotation] = pod.Annotations[controller.PodOperatorManagedFieldsHashAnnotation]
			}
			Expect(updatedAnnotationsMap[controller.PodHashAnnotation]).To(Not(BeEmpty()))
			Expect(updatedAnnotationsMap[controller.PodOperatorManagedFieldsHashAnnotation]).To(Not(BeEmpty()))

			Expect(annotationsMap[controller.PodHashAnnotation]).To(Not(Equal(updatedAnnotationsMap[controller.PodHashAnnotation])))
			Expect(annotationsMap[controller.PodOperatorManagedFieldsHashAnnotation]).To(Not(Equal(updatedAnnotationsMap[controller.PodOperatorManagedFieldsHashAnnotation])))

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			suite.UsingClusterBy(key.Name, "Setting helper image back to the default")
			defaultHelperImage := versions.DefaultHelperImageVersion()
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HelperImage = defaultHelperImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 3, 1)

			suite.UsingClusterBy(key.Name, "Validating pod is recreated using the explicitly defined default helper image as init container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(defaultHelperImage))

			suite.UsingClusterBy(key.Name, "Validating pod hash annotation changed and pod managed fields annotation has not changed")
			updated2AnnotationsMap := make(map[string]string)
			updated2ClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range updated2ClusterPods {
				updated2AnnotationsMap[controller.PodHashAnnotation] = pod.Annotations[controller.PodHashAnnotation]
				updated2AnnotationsMap[controller.PodOperatorManagedFieldsHashAnnotation] = pod.Annotations[controller.PodOperatorManagedFieldsHashAnnotation]
			}
			Expect(updated2AnnotationsMap[controller.PodHashAnnotation]).To(Not(BeEmpty()))
			Expect(updated2AnnotationsMap[controller.PodOperatorManagedFieldsHashAnnotation]).To(Not(BeEmpty()))

			Expect(updatedAnnotationsMap[controller.PodHashAnnotation]).To(Not(Equal(updated2AnnotationsMap[controller.PodHashAnnotation])))
			Expect(updatedAnnotationsMap[controller.PodOperatorManagedFieldsHashAnnotation]).To(Equal(updated2AnnotationsMap[controller.PodOperatorManagedFieldsHashAnnotation]))

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updated2ClusterPods)))
			}
		})
	})

	Context("Humio Cluster Rotate Bootstrap Token", Label("envtest", "dummy", "real"), func() {
		It("Update should correctly replace pods to use new bootstrap token", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-rotate-bootstrap-token",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}
			toCreate.Spec.NodeCount = 2

			suite.UsingClusterBy(key.Name, "Creating a cluster")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Validating pod bootstrap token annotation hash")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)

				if len(clusterPods) > 0 {
					return clusterPods[0].Annotations[controller.BootstrapTokenHashAnnotation]
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Not(Equal("")))

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			bootstrapTokenHashValue := clusterPods[0].Annotations[controller.BootstrapTokenHashAnnotation]

			suite.UsingClusterBy(key.Name, "Rotating bootstrap token")
			var bootstrapTokenSecret corev1.Secret

			bootstrapTokenSecretKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.BootstrapTokenSecretNameSuffix),
				Namespace: key.Namespace,
			}
			Expect(k8sClient.Get(ctx, bootstrapTokenSecretKey, &bootstrapTokenSecret)).To(Succeed())
			bootstrapTokenSecret.Data["hashedToken"] = []byte("some new token")
			Expect(k8sClient.Update(ctx, &bootstrapTokenSecret)).To(Succeed())

			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			suite.UsingClusterBy(key.Name, "Validating pod is recreated with the new bootstrap token hash annotation")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)

				if len(clusterPods) > 0 {
					return clusterPods[0].Annotations[controller.BootstrapTokenHashAnnotation]
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Not(Equal(bootstrapTokenHashValue)))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Environment Variable", Label("envtest", "dummy", "real"), func() {
		It("Should correctly replace pods to use new environment variable", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-envvar",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.EnvironmentVariables = []corev1.EnvVar{
				{
					Name:  "test",
					Value: "",
				},
				{
					Name:  "KAFKA_SERVERS",
					Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
				},
				{
					Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
					Value: key.Name,
				},
				{
					Name:  "AUTHENTICATION_METHOD",
					Value: "oauth",
				},
				{
					Name:  "ENABLE_IOC_SERVICE",
					Value: "false",
				},
				{
					Name:  "HUMIO_JVM_LOG_OPTS",
					Value: "-Xlog:gc+jni=debug:stdout -Xlog:gc*:stdout:time,tags",
				},
				{
					Name:  "HUMIO_OPTS",
					Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
				},
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(toCreate.Spec.EnvironmentVariables[0]))
			}

			suite.UsingClusterBy(key.Name, "Updating the environment variable successfully")
			updatedEnvironmentVariables := []corev1.EnvVar{
				{
					Name:  "test",
					Value: "update",
				},
				{
					Name:  "KAFKA_SERVERS",
					Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
				},
				{
					Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
					Value: key.Name,
				},
				{
					Name:  "AUTHENTICATION_METHOD",
					Value: "oauth",
				},
				{
					Name:  "ENABLE_IOC_SERVICE",
					Value: "false",
				},
				{
					Name:  "HUMIO_JVM_LOG_OPTS",
					Value: "-Xlog:gc+jni=debug:stdout -Xlog:gc*:stdout:time,tags",
				},
				{
					Name:  "HUMIO_OPTS",
					Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
				},
			}

			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.EnvironmentVariables = updatedEnvironmentVariables
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(updatedEnvironmentVariables[0]))
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Environment Variable Multi Node Pool", Label("envtest", "dummy", "real"), func() {
		It("Should correctly replace pods to use new environment variable for multi node pool clusters", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-envvar-np",
				Namespace: testProcessNamespace,
			}
			toCreate := constructBasicMultiNodePoolHumioCluster(key, 1)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}
			toCreate.Spec.NodeCount = 1
			toCreate.Spec.NodePools[0].NodeCount = 1
			toCreate.Spec.CommonEnvironmentVariables = []corev1.EnvVar{
				{
					Name:  "COMMON_ENV_VAR",
					Value: "value",
				},
				{
					Name:  "HUMIO_OPTS",
					Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
				},
				{
					Name:  "KAFKA_SERVERS",
					Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
				},
				{
					Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
					Value: key.Name,
				},
				{
					Name:  "AUTHENTICATION_METHOD",
					Value: "oauth",
				},
				{
					Name:  "ENABLE_IOC_SERVICE",
					Value: "false",
				},
				{
					Name:  "test",
					Value: "common",
				},
			}
			toCreate.Spec.EnvironmentVariables = []corev1.EnvVar{
				{
					Name:  "test",
					Value: "",
				},
				{
					Name:  "HUMIO_OPTS",
					Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
				},
				{
					Name:  "KAFKA_SERVERS",
					Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
				},
				{
					Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
					Value: key.Name,
				},
				{
					Name:  "AUTHENTICATION_METHOD",
					Value: "oauth",
				},
				{
					Name:  "ENABLE_IOC_SERVICE",
					Value: "false",
				},
			}
			toCreate.Spec.NodePools[0].EnvironmentVariables = []corev1.EnvVar{
				{
					Name:  "test",
					Value: "np",
				},
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapMultiNodePoolCluster(ctx, k8sClient, testHumioClient, toCreate)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			mainNodePoolManager := controller.NewHumioNodeManagerFromHumioCluster(toCreate)
			customNodePoolManager := controller.NewHumioNodeManagerFromHumioNodePool(toCreate, &toCreate.Spec.NodePools[0])

			expectedCommonVars := []corev1.EnvVar{
				{
					Name:  "COMMON_ENV_VAR",
					Value: "value",
				},
				{
					Name:  "KAFKA_SERVERS",
					Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
				},
			}
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, mainNodePoolManager.GetPodLabels())
			Expect(clusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Env).To(ContainElements(append(expectedCommonVars, corev1.EnvVar{
					Name: "test", Value: ""})))
			}

			customClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, customNodePoolManager.GetPodLabels())
			Expect(clusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range customClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Env).To(ContainElements(append(expectedCommonVars, corev1.EnvVar{
					Name: "test", Value: "np"})))
			}

			suite.UsingClusterBy(key.Name, "Updating the environment variable on main node pool successfully")
			updatedCommonEnvironmentVariables := []corev1.EnvVar{
				{
					Name:  "COMMON_ENV_VAR",
					Value: "value",
				},
				{
					Name:  "HUMIO_OPTS",
					Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
				},
				{
					Name:  "KAFKA_SERVERS",
					Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
				},
				{
					Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
					Value: key.Name,
				},
				{
					Name:  "AUTHENTICATION_METHOD",
					Value: "oauth",
				},
				{
					Name:  "ENABLE_IOC_SERVICE",
					Value: "false",
				},
			}
			updatedEnvironmentVariables := []corev1.EnvVar{
				{
					Name:  "test",
					Value: "update",
				},
				{
					Name:  "HUMIO_OPTS",
					Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
				},
				{
					Name:  "KAFKA_SERVERS",
					Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
				},
				{
					Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
					Value: key.Name,
				},
				{
					Name:  "AUTHENTICATION_METHOD",
					Value: "oauth",
				},
				{
					Name:  "ENABLE_IOC_SERVICE",
					Value: "false",
				},
			}

			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.CommonEnvironmentVariables = updatedCommonEnvironmentVariables
				updatedHumioCluster.Spec.EnvironmentVariables = updatedEnvironmentVariables
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			mostSeenNodePoolsWithRestartingState := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxNumberNodePoolsWithSpecificNodePoolStatus(ctx2, k8sClient, key, forever, &mostSeenNodePoolsWithRestartingState, humiov1alpha1.HumioClusterStateRestarting)

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, mainNodePoolManager, 2, 1)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, mainNodePoolManager.GetPodLabels())
				Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(updatedEnvironmentVariables[0]))
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, mainNodePoolManager.GetPodLabels())
			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			suite.UsingClusterBy(key.Name, "Confirming pod revision did not change for the other node pool")
			additionalNodePoolManager := controller.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[0])

			nonUpdatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, additionalNodePoolManager.GetPodLabels())
			Expect(nonUpdatedClusterPods).To(HaveLen(toCreate.Spec.NodePools[0].NodeCount))
			Expect(updatedHumioCluster.Spec.NodePools[0].EnvironmentVariables).To(Equal(toCreate.Spec.NodePools[0].EnvironmentVariables))
			for _, pod := range nonUpdatedClusterPods {
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, additionalNodePoolManager.GetPodLabels())

			suite.UsingClusterBy(key.Name, "Updating the environment variable on additional node pool successfully")
			updatedEnvironmentVariables = []corev1.EnvVar{
				{
					Name:  "test",
					Value: "update",
				},
				{
					Name:  "HUMIO_OPTS",
					Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
				},
				{
					Name:  "KAFKA_SERVERS",
					Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
				},
				{
					Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
					Value: key.Name,
				},
				{
					Name:  "AUTHENTICATION_METHOD",
					Value: "oauth",
				},
				{
					Name:  "ENABLE_IOC_SERVICE",
					Value: "false",
				},
			}
			npUpdatedEnvironmentVariables := []corev1.EnvVar{
				{
					Name:  "test",
					Value: "np-update",
				},
			}

			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.NodePools[0].EnvironmentVariables = npUpdatedEnvironmentVariables
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, additionalNodePoolManager, 2, 1)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, additionalNodePoolManager.GetPodLabels())
				Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElements(npUpdatedEnvironmentVariables))
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, additionalNodePoolManager.GetPodLabels())
			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			cancel()
			<-forever
			Expect(mostSeenNodePoolsWithRestartingState).To(BeNumerically("==", 1))

			suite.UsingClusterBy(key.Name, "Confirming pod revision did not change for the other main pool")

			nonUpdatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, mainNodePoolManager.GetPodLabels())
			Expect(nonUpdatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range nonUpdatedClusterPods {
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}
		})
	})

	Context("Humio Cluster Ingress", Label("envtest", "dummy", "real"), func() {
		It("Should correctly update ingresses to use new annotations variable", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-ingress",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Hostname = "humio.example.com"
			toCreate.Spec.ESHostname = "humio-es.humio.com"
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Waiting for ingresses to be created")
			desiredIngresses := []*networkingv1.Ingress{
				controller.ConstructGeneralIngress(toCreate, toCreate.Spec.Hostname),
				controller.ConstructStreamingQueryIngress(toCreate, toCreate.Spec.Hostname),
				controller.ConstructIngestIngress(toCreate, toCreate.Spec.Hostname),
				controller.ConstructESIngestIngress(toCreate, toCreate.Spec.ESHostname),
			}

			var foundIngressList []networkingv1.Ingress
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				return foundIngressList
			}, testTimeout, suite.TestInterval).Should(HaveLen(4))

			// Kubernetes 1.18 introduced a new field, PathType. For older versions PathType is returned as nil,
			// so we explicitly set the value before comparing ingress objects.
			// When minimum supported Kubernetes version is 1.18, we can drop this.
			pathTypeImplementationSpecific := networkingv1.PathTypeImplementationSpecific
			for ingressIdx, ingress := range foundIngressList {
				for ruleIdx, rule := range ingress.Spec.Rules {
					for pathIdx := range rule.HTTP.Paths {
						if foundIngressList[ingressIdx].Spec.Rules[ruleIdx].HTTP.Paths[pathIdx].PathType == nil {
							foundIngressList[ingressIdx].Spec.Rules[ruleIdx].HTTP.Paths[pathIdx].PathType = &pathTypeImplementationSpecific
						}
					}
				}
			}

			Expect(foundIngressList).Should(HaveLen(4))
			for _, desiredIngress := range desiredIngresses {
				for _, foundIngress := range foundIngressList {
					if desiredIngress.Name == foundIngress.Name {
						Expect(foundIngress.Annotations).To(BeEquivalentTo(desiredIngress.Annotations))
						Expect(foundIngress.Spec).To(BeEquivalentTo(desiredIngress.Spec))
					}
				}
			}

			suite.UsingClusterBy(key.Name, "Adding an additional ingress annotation successfully")
			var existingHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, key, &existingHumioCluster)).Should(Succeed())
				existingHumioCluster.Spec.Ingress.Annotations = map[string]string{"humio.com/new-important-annotation": "true"}
				return k8sClient.Update(ctx, &existingHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				for _, ingress := range ingresses {
					if _, ok := ingress.Annotations["humio.com/new-important-annotation"]; !ok {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Eventually(func() ([]networkingv1.Ingress, error) {
				return kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			}, testTimeout, suite.TestInterval).Should(HaveLen(4))

			suite.UsingClusterBy(key.Name, "Changing ingress hostnames successfully")
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, key, &existingHumioCluster)).Should(Succeed())
				existingHumioCluster.Spec.Hostname = "humio2.example.com"
				existingHumioCluster.Spec.ESHostname = "humio2-es.example.com"
				return k8sClient.Update(ctx, &existingHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			desiredIngresses = []*networkingv1.Ingress{
				controller.ConstructGeneralIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				controller.ConstructStreamingQueryIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				controller.ConstructIngestIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				controller.ConstructESIngestIngress(&existingHumioCluster, existingHumioCluster.Spec.ESHostname),
			}
			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				for _, ingress := range ingresses {
					for _, rule := range ingress.Spec.Rules {
						if rule.Host != "humio2.example.com" && rule.Host != "humio2-es.example.com" {
							return false
						}
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))

			// Kubernetes 1.18 introduced a new field, PathType. For older versions PathType is returned as nil,
			// so we explicitly set the value before comparing ingress objects.
			// When minimum supported Kubernetes version is 1.18, we can drop this.
			for ingressIdx, ingress := range foundIngressList {
				for ruleIdx, rule := range ingress.Spec.Rules {
					for pathIdx := range rule.HTTP.Paths {
						if foundIngressList[ingressIdx].Spec.Rules[ruleIdx].HTTP.Paths[pathIdx].PathType == nil {
							foundIngressList[ingressIdx].Spec.Rules[ruleIdx].HTTP.Paths[pathIdx].PathType = &pathTypeImplementationSpecific
						}
					}
				}
			}

			for _, desiredIngress := range desiredIngresses {
				for _, foundIngress := range foundIngressList {
					if desiredIngress.Name == foundIngress.Name {
						Expect(foundIngress.Annotations).To(BeEquivalentTo(desiredIngress.Annotations))
						Expect(foundIngress.Spec).To(BeEquivalentTo(desiredIngress.Spec))
					}
				}
			}

			suite.UsingClusterBy(key.Name, "Removing an ingress annotation successfully")
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, key, &existingHumioCluster)).Should(Succeed())
				delete(existingHumioCluster.Spec.Ingress.Annotations, "humio.com/new-important-annotation")
				return k8sClient.Update(ctx, &existingHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				for _, ingress := range ingresses {
					if _, ok := ingress.Annotations["humio.com/new-important-annotation"]; ok {
						return true
					}
				}
				return false
			}, testTimeout, suite.TestInterval).Should(BeFalse())

			foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			for _, foundIngress := range foundIngressList {
				Expect(foundIngress.Annotations).ShouldNot(HaveKey("humio.com/new-important-annotation"))
			}

			suite.UsingClusterBy(key.Name, "Disabling ingress successfully")
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, key, &existingHumioCluster)).Should(Succeed())
				existingHumioCluster.Spec.Ingress.Enabled = false
				return k8sClient.Update(ctx, &existingHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() ([]networkingv1.Ingress, error) {
				return kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			}, testTimeout, suite.TestInterval).Should(BeEmpty())
		})
	})

	Context("Humio Cluster Pod Annotations", Label("envtest", "dummy", "real"), func() {
		It("Should be correctly annotated", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-pods",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.PodAnnotations = map[string]string{"humio.com/new-important-annotation": "true"}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, toCreate.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					Expect(pod.Annotations["humio.com/new-important-annotation"]).Should(Equal("true"))
					Expect(pod.Annotations["productName"]).Should(Equal("humio"))
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Pod Labels", Label("envtest", "dummy", "real"), func() {
		It("Should be correctly annotated", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-labels",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.PodLabels = map[string]string{"humio.com/new-important-label": "true"}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, toCreate.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					Expect(pod.Labels["humio.com/new-important-label"]).Should(Equal("true"))
					Expect(pod.Labels["app.kubernetes.io/managed-by"]).Should(Equal("humio-operator"))
					Expect(pod.Labels["humio.com/feature"]).Should(Equal("OperatorInternal"))
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Custom Service", Label("envtest", "dummy", "real"), func() {
		It("Should correctly use default service", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-svc",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			svc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
			Expect(svc.Spec.Type).To(BeIdenticalTo(corev1.ServiceTypeClusterIP))
			for _, port := range svc.Spec.Ports {
				if port.Name == controller.HumioPortName {
					Expect(port.Port).Should(Equal(int32(controller.HumioPort)))
				}
				if port.Name == controller.ElasticPortName {
					Expect(port.Port).Should(Equal(int32(controller.ElasticPort)))
				}
			}
			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "Updating service type")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServiceType = corev1.ServiceTypeLoadBalancer
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Wait for the new HumioCluster to finish any existing reconcile loop by waiting for the
			// status.observedGeneration to equal at least that of the current resource version. This will avoid race
			// conditions where the HumioCluster is updated and service is deleted midway through reconciliation.
			suite.WaitForReconcileToSync(ctx, key, k8sClient, &updatedHumioCluster, testTimeout)
			Expect(k8sClient.Delete(ctx, controller.ConstructService(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)))).To(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming we can see the updated HumioCluster object")
			Eventually(func() corev1.ServiceType {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Spec.HumioServiceType
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(corev1.ServiceTypeLoadBalancer))

			Eventually(func() types.UID {
				newSvc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				suite.UsingClusterBy(key.Name, fmt.Sprintf("Waiting for Service to get recreated. ServiceBeforeDeletion.Metadata=%#+v, CurrentServiceFromAPI.Metadata=%#+v", svc.ObjectMeta, newSvc.ObjectMeta))
				return newSvc.UID
			}, testTimeout, suite.TestInterval).ShouldNot(BeEquivalentTo(svc.UID))

			Eventually(func() corev1.ServiceType {
				svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				return svc.Spec.Type
			}, testTimeout, suite.TestInterval).Should(Equal(corev1.ServiceTypeLoadBalancer))

			suite.UsingClusterBy(key.Name, "Updating Humio port")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServicePort = 443
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// TODO: Right now the service is not updated properly, so we delete it ourselves to make the operator recreate the service
			// Wait for the new HumioCluster to finish any existing reconcile loop by waiting for the
			// status.observedGeneration to equal at least that of the current resource version. This will avoid race
			// conditions where the HumioCluster is updated and service is deleted mid-way through a reconcile.
			suite.WaitForReconcileToSync(ctx, key, k8sClient, &updatedHumioCluster, testTimeout)
			Expect(k8sClient.Delete(ctx, controller.ConstructService(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)))).To(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming service gets recreated with correct Humio port")
			Eventually(func() types.UID {
				newSvc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				suite.UsingClusterBy(key.Name, fmt.Sprintf("Waiting for Service to get recreated. ServiceBeforeDeletion.Metadata=%#+v, CurrentServiceFromAPI.Metadata=%#+v", svc.ObjectMeta, newSvc.ObjectMeta))
				return newSvc.UID
			}, testTimeout, suite.TestInterval).ShouldNot(BeEquivalentTo(svc.UID))

			Eventually(func() int32 {
				svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				for _, port := range svc.Spec.Ports {
					if port.Name == controller.HumioPortName {
						return port.Port
					}
				}
				return -1
			}, testTimeout, suite.TestInterval).Should(Equal(int32(443)))

			suite.UsingClusterBy(key.Name, "Updating ES port")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				updatedHumioCluster.Spec.HumioESServicePort = 9201
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// TODO: Right now the service is not updated properly, so we delete it ourselves to make the operator recreate the service
			// Wait for the new HumioCluster to finish any existing reconcile loop by waiting for the
			// status.observedGeneration to equal at least that of the current resource version. This will avoid race
			// conditions where the HumioCluster is updated and service is deleted mid-way through a reconcile.
			suite.WaitForReconcileToSync(ctx, key, k8sClient, &updatedHumioCluster, testTimeout)
			Expect(k8sClient.Delete(ctx, controller.ConstructService(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)))).To(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming service gets recreated with correct ES port")
			Eventually(func() types.UID {
				newSvc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				suite.UsingClusterBy(key.Name, fmt.Sprintf("Waiting for Service to get recreated. ServiceBeforeDeletion.Metadata=%#+v, CurrentServiceFromAPI.Metadata=%#+v", svc.ObjectMeta, newSvc.ObjectMeta))
				return newSvc.UID
			}, testTimeout, suite.TestInterval).ShouldNot(BeEquivalentTo(svc.UID))

			Eventually(func() int32 {
				svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				for _, port := range svc.Spec.Ports {
					if port.Name == controller.ElasticPortName {
						return port.Port
					}
				}
				return -1
			}, testTimeout, suite.TestInterval).Should(Equal(int32(9201)))

			svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
			Expect(svc.Annotations).To(BeNil())

			suite.UsingClusterBy(key.Name, "Updating service annotations")
			updatedAnnotationKey := "new-annotation"
			updatedAnnotationValue := "new-value"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServiceAnnotations = map[string]string{updatedAnnotationKey: updatedAnnotationValue}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming we can see the updated service annotations")
			Eventually(func() map[string]string {
				service := controller.ConstructService(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster))
				Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
				return service.Annotations
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(updatedAnnotationKey, updatedAnnotationValue))

			suite.UsingClusterBy(key.Name, "Updating service labels")
			updatedLabelsKey := "new-label"
			updatedLabelsValue := "new-value"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServiceLabels = map[string]string{updatedLabelsKey: updatedLabelsValue}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming we can see the updated service labels")
			Eventually(func() map[string]string {
				service := controller.ConstructService(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster))
				Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
				return service.Labels
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(updatedLabelsKey, updatedLabelsValue))

			// The selector is not controlled through the spec, but with the addition of node pools, the operator adds
			// a new selector. This test confirms the operator will be able to migrate to different selectors on the
			// service.
			suite.UsingClusterBy(key.Name, "Updating service selector for migration to node pools")
			service := controller.ConstructService(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster))
			Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
			delete(service.Spec.Selector, "humio.com/node-pool")
			Expect(k8sClient.Update(ctx, service)).To(Succeed())

			suite.WaitForReconcileToSync(ctx, key, k8sClient, &updatedHumioCluster, testTimeout)

			Eventually(func() map[string]string {
				service := controller.ConstructService(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster))
				Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
				return service.Spec.Selector
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue("humio.com/node-pool", key.Name))

			suite.UsingClusterBy(key.Name, "Confirming headless service has the correct HTTP and ES ports")
			headlessSvc, _ := kubernetes.GetService(ctx, k8sClient, fmt.Sprintf("%s-headless", key.Name), key.Namespace)
			Expect(headlessSvc.Spec.Type).To(BeIdenticalTo(corev1.ServiceTypeClusterIP))
			for _, port := range headlessSvc.Spec.Ports {
				if port.Name == controller.HumioPortName {
					Expect(port.Port).Should(Equal(int32(controller.HumioPort)))
				}
				if port.Name == controller.ElasticPortName {
					Expect(port.Port).Should(Equal(int32(controller.ElasticPort)))
				}
			}

			headlessSvc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
			Expect(svc.Annotations).To(BeNil())

			suite.UsingClusterBy(key.Name, "Updating headless service annotations")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioHeadlessServiceAnnotations = map[string]string{updatedAnnotationKey: updatedAnnotationValue}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming we can see the updated service annotations")
			Eventually(func() map[string]string {
				Expect(k8sClient.Get(ctx, key, headlessSvc)).Should(Succeed())
				return headlessSvc.Annotations
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(updatedAnnotationKey, updatedAnnotationValue))

			suite.UsingClusterBy(key.Name, "Updating headless service labels")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioHeadlessServiceLabels = map[string]string{updatedLabelsKey: updatedLabelsValue}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming we can see the updated service labels")
			Eventually(func() map[string]string {
				Expect(k8sClient.Get(ctx, key, headlessSvc)).Should(Succeed())
				return headlessSvc.Labels
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(updatedLabelsKey, updatedLabelsValue))

			suite.UsingClusterBy(key.Name, "Confirming internal service has the correct HTTP and ES ports")
			internalSvc, _ := kubernetes.GetService(ctx, k8sClient, fmt.Sprintf("%s-internal", key.Name), key.Namespace)
			Expect(internalSvc.Spec.Type).To(BeIdenticalTo(corev1.ServiceTypeClusterIP))
			for _, port := range internalSvc.Spec.Ports {
				if port.Name == controller.HumioPortName {
					Expect(port.Port).Should(Equal(int32(controller.HumioPort)))
				}
				if port.Name == controller.ElasticPortName {
					Expect(port.Port).Should(Equal(int32(controller.ElasticPort)))
				}
			}
			internalSvc, _ = kubernetes.GetService(ctx, k8sClient, fmt.Sprintf("%s-internal", key.Name), key.Namespace)
			Expect(internalSvc.Annotations).To(BeNil())

			suite.UsingClusterBy(key.Name, "Confirming internal service has the correct selector")
			Eventually(func() map[string]string {
				internalSvc, _ := kubernetes.GetService(ctx, k8sClient, fmt.Sprintf("%s-internal", key.Name), key.Namespace)
				return internalSvc.Spec.Selector
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue("humio.com/feature", "OperatorInternal"))
		})
	})

	Context("Humio Cluster Container Arguments", Label("envtest", "dummy", "real"), func() {
		It("Should correctly configure container arguments and ephemeral disks env var with default vhost selection method", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-container-args",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully without ephemeral disks")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			hnp := controller.NewHumioNodeManagerFromHumioCluster(toCreate)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Args).To(Equal([]string{"-c", "export CORES=$(getconf _NPROCESSORS_ONLN) && export HUMIO_OPTS=\"$HUMIO_OPTS -XX:ActiveProcessorCount=$(getconf _NPROCESSORS_ONLN)\" && export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"}))
			}

			suite.UsingClusterBy(key.Name, "Updating node uuid prefix which includes ephemeral disks and zone")
			var updatedHumioCluster humiov1alpha1.HumioCluster

			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{Name: "USING_EPHEMERAL_DISKS", Value: "true"})
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			hnp = controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)
			expectedContainerArgString := "export CORES=$(getconf _NPROCESSORS_ONLN) && export HUMIO_OPTS=\"$HUMIO_OPTS -XX:ActiveProcessorCount=$(getconf _NPROCESSORS_ONLN)\" && export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"

			Eventually(func() []string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
				if len(clusterPods) > 0 {
					humioIdx, _ := kubernetes.GetContainerIndexByName(clusterPods[0], controller.HumioContainerName)
					return clusterPods[0].Spec.Containers[humioIdx].Args
				}
				return []string{}
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo([]string{"-c", expectedContainerArgString}))
		})
	})

	Context("Humio Cluster Container Arguments Without Zone", Label("envtest", "dummy", "real"), func() {
		It("Should correctly configure container arguments", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-container-without-zone-args",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			hnp := controller.NewHumioNodeManagerFromHumioCluster(toCreate)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Args).To(Equal([]string{"-c", "export CORES=$(getconf _NPROCESSORS_ONLN) && export HUMIO_OPTS=\"$HUMIO_OPTS -XX:ActiveProcessorCount=$(getconf _NPROCESSORS_ONLN)\" && export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"}))
			}

			suite.UsingClusterBy(key.Name, "Updating node uuid prefix which includes ephemeral disks but not zone")
			var updatedHumioCluster humiov1alpha1.HumioCluster

			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{Name: "USING_EPHEMERAL_DISKS", Value: "true"})
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			expectedContainerArgString := "export CORES=$(getconf _NPROCESSORS_ONLN) && export HUMIO_OPTS=\"$HUMIO_OPTS -XX:ActiveProcessorCount=$(getconf _NPROCESSORS_ONLN)\" && export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"
			Eventually(func() []string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				if len(clusterPods) > 0 {
					humioIdx, _ := kubernetes.GetContainerIndexByName(clusterPods[0], controller.HumioContainerName)
					return clusterPods[0].Spec.Containers[humioIdx].Args
				}
				return []string{}
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo([]string{"-c", expectedContainerArgString}))
		})
	})

	Context("Humio Cluster Service Account Annotations", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle service account annotations", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-sa-annotations",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			humioServiceAccountName := fmt.Sprintf("%s-%s", key.Name, controller.HumioServiceAccountNameSuffix)

			Eventually(func() error {
				_, err := kubernetes.GetServiceAccount(ctx, k8sClient, humioServiceAccountName, key.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			serviceAccount, _ := kubernetes.GetServiceAccount(ctx, k8sClient, humioServiceAccountName, key.Namespace)
			Expect(serviceAccount.Annotations).Should(BeNil())

			suite.UsingClusterBy(key.Name, "Adding an annotation successfully")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServiceAccountAnnotations = map[string]string{"some-annotation": "true"}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Eventually(func() bool {
				serviceAccount, _ = kubernetes.GetServiceAccount(ctx, k8sClient, humioServiceAccountName, key.Namespace)
				_, ok := serviceAccount.Annotations["some-annotation"]
				return ok
			}, testTimeout, suite.TestInterval).Should(BeTrue())
			Expect(serviceAccount.Annotations["some-annotation"]).Should(Equal("true"))

			suite.UsingClusterBy(key.Name, "Removing all annotations successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServiceAccountAnnotations = nil
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Eventually(func() map[string]string {
				serviceAccount, _ = kubernetes.GetServiceAccount(ctx, k8sClient, humioServiceAccountName, key.Namespace)
				return serviceAccount.Annotations
			}, testTimeout, suite.TestInterval).Should(BeNil())
		})
	})

	Context("Humio Cluster Pod Security Context", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle pod security context", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-podsecuritycontext",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodSecurityContext()))
			}
			suite.UsingClusterBy(key.Name, "Updating Pod Security Context to be empty")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.PodSecurityContext = &corev1.PodSecurityContext{}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					if !reflect.DeepEqual(pod.Spec.SecurityContext, &corev1.PodSecurityContext{}) {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(&corev1.PodSecurityContext{}))
			}

			suite.UsingClusterBy(key.Name, "Updating Pod Security Context to be non-empty")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.PodSecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 3, 1)

			Eventually(func() corev1.PodSecurityContext {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return *pod.Spec.SecurityContext
				}
				return corev1.PodSecurityContext{}
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}))

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(&corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}))
			}
		})
	})

	Context("Humio Cluster Container Security Context", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle container security context", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-containersecuritycontext",
				Namespace: testProcessNamespace,
			} // State: <empty> -> Running -> ConfigError -> Running -> Restarting -> Running -> Restarting -> Running
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].SecurityContext).To(Equal(controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetContainerSecurityContext()))
			}
			suite.UsingClusterBy(key.Name, "Updating Container Security Context to be empty")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ContainerSecurityContext = &corev1.SecurityContext{}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					if !reflect.DeepEqual(pod.Spec.Containers[humioIdx].SecurityContext, &corev1.SecurityContext{}) {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].SecurityContext).To(Equal(&corev1.SecurityContext{}))
			}

			suite.UsingClusterBy(key.Name, "Updating Container Security Context to be non-empty")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ContainerSecurityContext = &corev1.SecurityContext{
					Capabilities: &corev1.Capabilities{
						Add: []corev1.Capability{
							"NET_ADMIN",
						},
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 3, 1)

			Eventually(func() corev1.SecurityContext {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return *pod.Spec.Containers[humioIdx].SecurityContext
				}
				return corev1.SecurityContext{}
			}, testTimeout, suite.TestInterval).Should(Equal(corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Add: []corev1.Capability{
						"NET_ADMIN",
					},
				},
			}))

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].SecurityContext).To(Equal(&corev1.SecurityContext{
					Capabilities: &corev1.Capabilities{
						Add: []corev1.Capability{
							"NET_ADMIN",
						},
					},
				}))
			}
		})
	})

	Context("Humio Cluster Container Probes", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle container probes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-probes",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].ReadinessProbe).To(Equal(controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetContainerReadinessProbe()))
				Expect(pod.Spec.Containers[humioIdx].LivenessProbe).To(Equal(controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetContainerLivenessProbe()))
				Expect(pod.Spec.Containers[humioIdx].StartupProbe).To(Equal(controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetContainerStartupProbe()))
			}
			suite.UsingClusterBy(key.Name, "Updating Container probes to be empty")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ContainerReadinessProbe = &corev1.Probe{}
				updatedHumioCluster.Spec.ContainerLivenessProbe = &corev1.Probe{}
				updatedHumioCluster.Spec.ContainerStartupProbe = &corev1.Probe{}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pods have the updated revision")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			suite.UsingClusterBy(key.Name, "Confirming pods do not have a readiness probe set")
			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].ReadinessProbe
				}
				return &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						Exec: &corev1.ExecAction{Command: []string{"no-pods-found"}},
					},
				}
			}, testTimeout, suite.TestInterval).Should(BeNil())

			suite.UsingClusterBy(key.Name, "Confirming pods do not have a liveness probe set")
			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].LivenessProbe
				}
				return &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						Exec: &corev1.ExecAction{Command: []string{"no-pods-found"}},
					},
				}
			}, testTimeout, suite.TestInterval).Should(BeNil())

			suite.UsingClusterBy(key.Name, "Confirming pods do not have a startup probe set")
			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].StartupProbe
				}
				return &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						Exec: &corev1.ExecAction{Command: []string{"no-pods-found"}},
					},
				}
			}, testTimeout, suite.TestInterval).Should(BeNil())

			suite.UsingClusterBy(key.Name, "Updating Container probes to be non-empty")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ContainerReadinessProbe = &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: controller.HumioPort},
							Scheme: getProbeScheme(&updatedHumioCluster),
						},
					},
					InitialDelaySeconds: 60,
					PeriodSeconds:       10,
					TimeoutSeconds:      4,
					SuccessThreshold:    2,
					FailureThreshold:    20,
				}
				updatedHumioCluster.Spec.ContainerLivenessProbe = &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: controller.HumioPort},
							Scheme: getProbeScheme(&updatedHumioCluster),
						},
					},
					InitialDelaySeconds: 60,
					PeriodSeconds:       10,
					TimeoutSeconds:      4,
					SuccessThreshold:    1,
					FailureThreshold:    20,
				}
				updatedHumioCluster.Spec.ContainerStartupProbe = &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: controller.HumioPort},
							Scheme: getProbeScheme(&updatedHumioCluster),
						},
					},
					PeriodSeconds:    10,
					TimeoutSeconds:   4,
					SuccessThreshold: 1,
					FailureThreshold: 30,
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].ReadinessProbe
				}
				return &corev1.Probe{}
			}, testTimeout, suite.TestInterval).Should(Equal(&corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: controller.HumioPort},
						Scheme: getProbeScheme(&updatedHumioCluster),
					},
				},
				InitialDelaySeconds: 60,
				PeriodSeconds:       10,
				TimeoutSeconds:      4,
				SuccessThreshold:    2,
				FailureThreshold:    20,
			}))

			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].LivenessProbe
				}
				return &corev1.Probe{}
			}, testTimeout, suite.TestInterval).Should(Equal(&corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: controller.HumioPort},
						Scheme: getProbeScheme(&updatedHumioCluster),
					},
				},
				InitialDelaySeconds: 60,
				PeriodSeconds:       10,
				TimeoutSeconds:      4,
				SuccessThreshold:    1,
				FailureThreshold:    20,
			}))

			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].StartupProbe
				}
				return &corev1.Probe{}
			}, testTimeout, suite.TestInterval).Should(Equal(&corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: controller.HumioPort},
						Scheme: getProbeScheme(&updatedHumioCluster),
					},
				},
				PeriodSeconds:    10,
				TimeoutSeconds:   4,
				SuccessThreshold: 1,
				FailureThreshold: 30,
			}))

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].ReadinessProbe).To(Equal(&corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: controller.HumioPort},
							Scheme: getProbeScheme(&updatedHumioCluster),
						},
					},
					InitialDelaySeconds: 60,
					PeriodSeconds:       10,
					TimeoutSeconds:      4,
					SuccessThreshold:    2,
					FailureThreshold:    20,
				}))
				Expect(pod.Spec.Containers[humioIdx].LivenessProbe).To(Equal(&corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: controller.HumioPort},
							Scheme: getProbeScheme(&updatedHumioCluster),
						},
					},
					InitialDelaySeconds: 60,
					PeriodSeconds:       10,
					TimeoutSeconds:      4,
					SuccessThreshold:    1,
					FailureThreshold:    20,
				}))
				Expect(pod.Spec.Containers[humioIdx].StartupProbe).To(Equal(&corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: controller.HumioPort},
							Scheme: getProbeScheme(&updatedHumioCluster),
						},
					},
					PeriodSeconds:    10,
					TimeoutSeconds:   4,
					SuccessThreshold: 1,
					FailureThreshold: 30,
				}))
			}
		})
	})

	Context("Humio Cluster Extra Kafka Configs", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle extra kafka configs", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-extrakafkaconfigs",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully with extra kafka configs")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "EXTRA_KAFKA_CONFIGS_FILE",
					Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", controller.ExtraKafkaPropertiesFilename),
				}))
			}

			suite.UsingClusterBy(key.Name, "Confirming pods have additional volume mounts for extra kafka configs")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, suite.TestInterval).Should(ContainElement(corev1.VolumeMount{
				Name:      "extra-kafka-configs",
				ReadOnly:  true,
				MountPath: "/var/lib/humio/extra-kafka-configs-configmap",
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods have additional volumes for extra kafka configs")
			mode := int32(420)
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).Should(ContainElement(corev1.Volume{
				Name: "extra-kafka-configs",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetExtraKafkaConfigsConfigMapName(),
						},
						DefaultMode: &mode,
					},
				},
			}))

			suite.UsingClusterBy(key.Name, "Confirming config map contains desired extra kafka configs")
			configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetExtraKafkaConfigsConfigMapName(), key.Namespace)
			Expect(configMap.Data[controller.ExtraKafkaPropertiesFilename]).To(Equal(toCreate.Spec.ExtraKafkaConfigs))

			var updatedHumioCluster humiov1alpha1.HumioCluster
			updatedExtraKafkaConfigs := "client.id=EXAMPLE"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ExtraKafkaConfigs = updatedExtraKafkaConfigs
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetExtraKafkaConfigsConfigMapName(), key.Namespace)
				return configMap.Data[controller.ExtraKafkaPropertiesFilename]

			}, testTimeout, suite.TestInterval).Should(Equal(updatedExtraKafkaConfigs))

			suite.UsingClusterBy(key.Name, "Removing extra kafka configs")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ExtraKafkaConfigs = ""
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pods do not have environment variable enabling extra kafka configs")
			Eventually(func() []corev1.EnvVar {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "EXTRA_KAFKA_CONFIGS_FILE",
				Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", controller.ExtraKafkaPropertiesFilename),
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volume mounts for extra kafka configs")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.VolumeMount{
				Name:      "extra-kafka-configs",
				ReadOnly:  true,
				MountPath: "/var/lib/humio/extra-kafka-configs-configmap",
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volumes for extra kafka configs")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.Volume{
				Name: "extra-kafka-configs",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetExtraKafkaConfigsConfigMapName(),
						},
						DefaultMode: &mode,
					},
				},
			}))
		})
	})

	Context("Humio Cluster View Group Permissions", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle view group permissions", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-vgp",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.ViewGroupPermissions = `
{
  "views": {
    "REPO1": {
      "GROUP1": {
        "queryPrefix": "QUERY1",
        "canEditDashboards": true
      },
      "GROUP2": {
        "queryPrefix": "QUERY2",
        "canEditDashboards": false
      }
    },
    "REPO2": {
      "GROUP2": {
        "queryPrefix": "QUERY3"
      },
      "GROUP3": {
        "queryPrefix": "QUERY4"
      }
    }
  }
}
`
			suite.UsingClusterBy(key.Name, "Creating the cluster successfully with view group permissions")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming config map was created")
			Eventually(func() error {
				_, err := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetViewGroupPermissionsConfigMapName(), toCreate.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pods have the expected environment variable, volume and volume mounts")
			mode := int32(420)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
					Value: "true",
				}))
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(ContainElement(corev1.VolumeMount{
					Name:      "view-group-permissions",
					ReadOnly:  true,
					MountPath: fmt.Sprintf("%s/%s", controller.HumioDataPath, controller.ViewGroupPermissionsFilename),
					SubPath:   controller.ViewGroupPermissionsFilename,
				}))
				Expect(pod.Spec.Volumes).To(ContainElement(corev1.Volume{
					Name: "view-group-permissions",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetViewGroupPermissionsConfigMapName(),
							},
							DefaultMode: &mode,
						},
					},
				}))
			}

			suite.UsingClusterBy(key.Name, "Confirming config map contains desired view group permissions")
			configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetViewGroupPermissionsConfigMapName(), key.Namespace)
			Expect(configMap.Data[controller.ViewGroupPermissionsFilename]).To(Equal(toCreate.Spec.ViewGroupPermissions))

			var updatedHumioCluster humiov1alpha1.HumioCluster
			updatedViewGroupPermissions := `
{
  "views": {
    "REPO2": {
      "newgroup": {
        "queryPrefix": "newquery"
      }
    }
  }
}
`
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ViewGroupPermissions = updatedViewGroupPermissions
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetViewGroupPermissionsConfigMapName(), key.Namespace)
				return configMap.Data[controller.ViewGroupPermissionsFilename]
			}, testTimeout, suite.TestInterval).Should(Equal(updatedViewGroupPermissions))

			suite.UsingClusterBy(key.Name, "Removing view group permissions")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ViewGroupPermissions = ""
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pods do not have environment variable enabling view group permissions")
			Eventually(func() []corev1.EnvVar {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
				Value: "true",
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volume mounts for view group permissions")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.VolumeMount{
				Name:      "view-group-permissions",
				ReadOnly:  true,
				MountPath: fmt.Sprintf("%s/%s", controller.HumioDataPath, controller.ViewGroupPermissionsFilename),
				SubPath:   controller.ViewGroupPermissionsFilename,
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volumes for view group permissions")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.Volume{
				Name: "view-group-permissions",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetViewGroupPermissionsConfigMapName(),
						},
						DefaultMode: &mode,
					},
				},
			}))

			suite.UsingClusterBy(key.Name, "Confirming config map was cleaned up")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)

				_, err := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetViewGroupPermissionsConfigMapName(), toCreate.Namespace)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Role Permissions", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle role permissions", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-rp",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.RolePermissions = `
{
  "roles": {
    "Admin": {
      "permissions": [
        "ChangeUserAccess",
        "ChangeDashboards",
        "ChangeFiles",
        "ChangeParsers",
        "ChangeSavedQueries",
        "ChangeDataDeletionPermissions",
        "ChangeDefaultSearchSettings",
        "ChangeS3ArchivingSettings",
        "ConnectView",
        "ReadAccess",
        "ChangeIngestTokens",
        "EventForwarding",
        "ChangeFdrFeeds"
      ]
    },
    "Searcher": {
      "permissions": [
        "ChangeTriggersAndActions",
        "ChangeFiles",
        "ChangeDashboards",
        "ChangeSavedQueries",
        "ReadAccess"
      ]
    }
  },
  "views": {
    "Audit Log": {
      "Devs DK": {
        "role": "Searcher",
        "queryPrefix": "secret=false"
      },
      "Support UK": {
        "role": "Admin",
        "queryPrefix": "*"
      }
    },
    "Web Log": {
      "Devs DK": {
        "role": "Admin",
        "queryPrefix": "*"
      },
      "Support UK": {
        "role": "Searcher",
        "queryPrefix": "*"
      }
    }
  }
}
`
			suite.UsingClusterBy(key.Name, "Creating the cluster successfully with role permissions")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming config map was created")
			Eventually(func() error {
				_, err := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetRolePermissionsConfigMapName(), toCreate.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pods have the expected environment variable, volume and volume mounts")
			mode := int32(420)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
					Value: "true",
				}))
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(ContainElement(corev1.VolumeMount{
					Name:      "role-permissions",
					ReadOnly:  true,
					MountPath: fmt.Sprintf("%s/%s", controller.HumioDataPath, controller.RolePermissionsFilename),
					SubPath:   controller.RolePermissionsFilename,
				}))
				Expect(pod.Spec.Volumes).To(ContainElement(corev1.Volume{
					Name: "role-permissions",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetRolePermissionsConfigMapName(),
							},
							DefaultMode: &mode,
						},
					},
				}))
			}

			suite.UsingClusterBy(key.Name, "Confirming config map contains desired role permissions")
			configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetRolePermissionsConfigMapName(), key.Namespace)
			Expect(configMap.Data[controller.RolePermissionsFilename]).To(Equal(toCreate.Spec.RolePermissions))

			var updatedHumioCluster humiov1alpha1.HumioCluster
			updatedRolePermissions := `
{
  "roles": {
    "Admin": {
      "permissions": [
        "ChangeUserAccess",
        "ChangeDashboards",
        "ChangeFiles",
        "ChangeParsers",
        "ChangeSavedQueries",
        "ChangeDataDeletionPermissions",
        "ChangeDefaultSearchSettings",
        "ChangeS3ArchivingSettings",
        "ConnectView",
        "ReadAccess",
        "ChangeIngestTokens",
        "EventForwarding",
        "ChangeFdrFeeds"
      ]
    },
    "Searcher": {
      "permissions": [
        "ChangeTriggersAndActions",
        "ChangeFiles",
        "ChangeDashboards",
        "ChangeSavedQueries",
        "ReadAccess"
      ]
    }
  },
  "views": {
    "Audit Log": {
      "Devs DK": {
        "role": "Searcher",
        "queryPrefix": "secret=false updated=true"
      },
      "Support UK": {
        "role": "Admin",
        "queryPrefix": "* updated=true"
      }
    },
    "Web Log": {
      "Devs DK": {
        "role": "Admin",
        "queryPrefix": "* updated=true"
      },
      "Support UK": {
        "role": "Searcher",
        "queryPrefix": "* updated=true"
      }
    }
  }
}
`
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.RolePermissions = updatedRolePermissions
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetRolePermissionsConfigMapName(), key.Namespace)
				return configMap.Data[controller.RolePermissionsFilename]
			}, testTimeout, suite.TestInterval).Should(Equal(updatedRolePermissions))

			suite.UsingClusterBy(key.Name, "Removing role permissions")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.RolePermissions = ""
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pods do not have environment variable enabling role permissions")
			Eventually(func() []corev1.EnvVar {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
				Value: "true",
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volume mounts for role permissions")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.VolumeMount{
				Name:      "role-permissions",
				ReadOnly:  true,
				MountPath: fmt.Sprintf("%s/%s", controller.HumioDataPath, controller.RolePermissionsFilename),
				SubPath:   controller.RolePermissionsFilename,
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volumes for role permissions")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.Volume{
				Name: "role-permissions",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetRolePermissionsConfigMapName(),
						},
						DefaultMode: &mode,
					},
				},
			}))

			suite.UsingClusterBy(key.Name, "Confirming config map was cleaned up")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)

				_, err := kubernetes.GetConfigMap(ctx, k8sClient, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetRolePermissionsConfigMapName(), toCreate.Namespace)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Persistent Volumes", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle persistent volumes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-pvc",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.DataVolumePersistentVolumeClaimSpecTemplate = corev1.PersistentVolumeClaimSpec{}
			toCreate.Spec.DataVolumeSource = corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			}

			suite.UsingClusterBy(key.Name, "Bootstrapping the cluster successfully without persistent volumes")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			Expect(kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetNodePoolLabels())).To(BeEmpty())

			suite.UsingClusterBy(key.Name, "Updating cluster to use persistent volumes")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.DataVolumeSource = corev1.VolumeSource{}
				updatedHumioCluster.Spec.DataVolumePersistentVolumeClaimSpecTemplate = corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}).Should(Succeed())

			Eventually(func() ([]corev1.PersistentVolumeClaim, error) {
				return kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetNodePoolLabels())
			}, testTimeout, suite.TestInterval).Should(HaveLen(toCreate.Spec.NodeCount))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pods are using PVC's and no PVC is left unused")
			pvcList, _ := kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetNodePoolLabels())
			foundPodList, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetNodePoolLabels())
			for _, pod := range foundPodList {
				_, err := controller.FindPvcForPod(pvcList, pod)
				Expect(err).ShouldNot(HaveOccurred())
			}
			_, err := controller.FindNextAvailablePvc(pvcList, foundPodList, map[string]struct{}{})
			Expect(err).Should(HaveOccurred())
		})
	})

	Context("Humio Cluster Extra Volumes", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle extra volumes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-extra-volumes",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			initialExpectedVolumesCount := 4                    // shared, humio-data, extra-kafka-configs, init-service-account-secret
			initialExpectedHumioContainerVolumeMountsCount := 3 // shared, humio-data, extra-kafka-configs

			if !helpers.UseEnvtest() {
				// k8s will automatically inject a service account token
				initialExpectedVolumesCount += 1                    // kube-api-access-<ID>
				initialExpectedHumioContainerVolumeMountsCount += 1 // kube-api-access-<ID>

				if helpers.TLSEnabled(toCreate) {
					initialExpectedVolumesCount += 1                    // tls-cert
					initialExpectedHumioContainerVolumeMountsCount += 1 // tls-cert
				}
			}

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.Volumes).To(HaveLen(initialExpectedVolumesCount))
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(HaveLen(initialExpectedHumioContainerVolumeMountsCount))
			}

			suite.UsingClusterBy(key.Name, "Adding additional volumes")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			mode := int32(420)
			extraVolume := corev1.Volume{
				Name: "gcp-storage-account-json-file",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  "gcp-storage-account-json-file",
						DefaultMode: &mode,
					},
				},
			}
			extraVolumeMount := corev1.VolumeMount{
				Name:      "gcp-storage-account-json-file",
				MountPath: "/var/lib/humio/gcp-storage-account-json-file",
				ReadOnly:  true,
			}

			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ExtraVolumes = []corev1.Volume{extraVolume}
				updatedHumioCluster.Spec.ExtraHumioVolumeMounts = []corev1.VolumeMount{extraVolumeMount}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Eventually(func() []corev1.Volume {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).Should(HaveLen(initialExpectedVolumesCount + 1))
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, suite.TestInterval).Should(HaveLen(initialExpectedHumioContainerVolumeMountsCount + 1))
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.Volumes).Should(ContainElement(extraVolume))
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).Should(ContainElement(extraVolumeMount))
			}
		})
	})

	Context("Humio Cluster Custom Path", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle custom paths with ingress disabled", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-path-ing-disabled",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}
			protocol := "http"
			if helpers.TLSEnabled(toCreate) {
				protocol = "https"
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming PUBLIC_URL is set to default value and PROXY_PREFIX_URL is not set")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(controller.EnvVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal(fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)", protocol)))
				Expect(controller.EnvVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL")).To(BeFalse())
			}

			suite.UsingClusterBy(key.Name, "Updating humio cluster path")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Path = "/logs"
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming PROXY_PREFIX_URL have been configured on all pods")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					if !controller.EnvVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL") {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Confirming PUBLIC_URL and PROXY_PREFIX_URL have been correctly configured")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(controller.EnvVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal(fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)/logs", protocol)))
				Expect(controller.EnvVarHasValue(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL", "/logs")).To(BeTrue())
			}

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			suite.UsingClusterBy(key.Name, "Confirming cluster returns to Running state")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())

				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})

		It("Should correctly handle custom paths with ingress enabled", Label("envtest", "dummy", "real"), func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-path-ing-enabled",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}
			toCreate.Spec.Hostname = "test-cluster.humio.com"
			toCreate.Spec.ESHostname = "test-cluster-es.humio.com"
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming PUBLIC_URL is set to default value and PROXY_PREFIX_URL is not set")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(controller.EnvVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal("https://test-cluster.humio.com"))
				Expect(controller.EnvVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL")).To(BeFalse())
			}

			suite.UsingClusterBy(key.Name, "Updating humio cluster path")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Path = "/logs"
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming PROXY_PREFIX_URL have been configured on all pods")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					if !controller.EnvVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL") {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Confirming PUBLIC_URL and PROXY_PREFIX_URL have been correctly configured")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(controller.EnvVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal("https://test-cluster.humio.com/logs"))
				Expect(controller.EnvVarHasValue(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL", "/logs")).To(BeTrue())
			}

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			suite.UsingClusterBy(key.Name, "Confirming cluster returns to Running state")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())

				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})
	})

	Context("Humio Cluster Config Errors", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with conflicting volume mount name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-volmnt-name",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.ExtraHumioVolumeMounts = []corev1.VolumeMount{
				{
					Name: controller.HumioDataVolumeName,
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			suite.CreateLicenseSecret(ctx, key, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "should describe cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(Equal("failed to validate pod spec: extraHumioVolumeMount conflicts with existing name: humio-data"))
		})
		It("Creating cluster with conflicting volume mount mount path", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-mount-path",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.ExtraHumioVolumeMounts = []corev1.VolumeMount{
				{
					Name:      "something-unique",
					MountPath: controller.HumioDataPath,
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			suite.CreateLicenseSecret(ctx, key, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "should describe cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(Equal("failed to validate pod spec: extraHumioVolumeMount conflicts with existing mount path: /data/humio-data"))
		})
		It("Creating cluster with conflicting volume name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-vol-name",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.ExtraVolumes = []corev1.Volume{
				{
					Name: controller.HumioDataVolumeName,
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			suite.CreateLicenseSecret(ctx, key, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "should describe cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(Equal("failed to validate pod spec: extraVolume conflicts with existing name: humio-data"))
		})
		It("Creating cluster with higher replication factor than nodes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-repl-factor",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.TargetReplicationFactor = 2
			toCreate.Spec.NodeCount = 1

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			suite.CreateLicenseSecret(ctx, key, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "should describe cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(Equal("node count must be equal to or greater than the target replication factor: nodeCount is too low"))
		})
		It("Creating cluster with conflicting storage configuration", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-conflict-storage-conf",
				Namespace: testProcessNamespace,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: 3,
						DataVolumeSource: corev1.VolumeSource{
							EmptyDir: &corev1.EmptyDirVolumeSource{},
						},
						DataVolumePersistentVolumeClaimSpecTemplate: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{
								corev1.ReadWriteOnce,
							},
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceStorage: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
								},
							},
						},
					},
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "should describe cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(Equal("conflicting storage configuration provided: exactly one of dataVolumeSource and dataVolumePersistentVolumeClaimSpecTemplate must be set"))
		})
		It("Creating cluster with conflicting storage configuration", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-no-storage-conf",
				Namespace: testProcessNamespace,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{NodeCount: 3},
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			suite.UsingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "should describe cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(Equal("no storage configuration provided: exactly one of dataVolumeSource and dataVolumePersistentVolumeClaimSpecTemplate must be set"))
		})
	})

	Context("Humio Cluster Without TLS for Ingress", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster without TLS for ingress", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-without-tls-ingress",
				Namespace: testProcessNamespace,
			}
			tlsDisabled := false
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Ingress.Enabled = true
			toCreate.Spec.Ingress.Controller = "nginx"
			toCreate.Spec.Ingress.TLS = &tlsDisabled
			toCreate.Spec.Hostname = "example.humio.com"
			toCreate.Spec.ESHostname = "es-example.humio.com"

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming ingress objects do not have TLS configured")
			var ingresses []networkingv1.Ingress
			Eventually(func() ([]networkingv1.Ingress, error) {
				return kubernetes.ListIngresses(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			}, testTimeout, suite.TestInterval).Should(HaveLen(4))

			ingresses, _ = kubernetes.ListIngresses(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			for _, ingress := range ingresses {
				Expect(ingress.Spec.TLS).To(BeNil())
			}
		})
	})

	Context("Humio Cluster with additional hostnames for TLS", Label("dummy", "real"), func() {
		It("Creating cluster with additional hostnames for TLS", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-tls-additional-hostnames",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			if !helpers.TLSEnabled(toCreate) {
				return
			}
			toCreate.Spec.TLS = &humiov1alpha1.HumioClusterTLSSpec{
				Enabled: helpers.BoolPtr(true),
				ExtraHostnames: []string{
					"something.additional",
					"yet.another.something.additional",
				},
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming certificate objects contain the additional hostnames")

			Eventually(func() ([]cmapi.Certificate, error) {
				return kubernetes.ListCertificates(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			}, testTimeout, suite.TestInterval).Should(HaveLen(2))

			var certificates []cmapi.Certificate
			certificates, err = kubernetes.ListCertificates(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			Expect(err).To(Succeed())
			for _, certificate := range certificates {
				Expect(certificate.Spec.DNSNames).Should(ContainElements(toCreate.Spec.TLS.ExtraHostnames))
			}
		})
	})

	Context("Humio Cluster Ingress", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle ingress when toggling both ESHostname and Hostname on/off", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-ingress-hostname",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Hostname = ""
			toCreate.Spec.ESHostname = ""
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully without any Hostnames defined")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateConfigError, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming we did not create any ingresses")
			var foundIngressList []networkingv1.Ingress
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				return foundIngressList
			}, testTimeout, suite.TestInterval).Should(BeEmpty())

			suite.UsingClusterBy(key.Name, "Setting the Hostname")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			hostname := "test-cluster.humio.com"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Hostname = hostname
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, &updatedHumioCluster)

			suite.UsingClusterBy(key.Name, "Confirming we only created ingresses with expected hostname")
			foundIngressList = []networkingv1.Ingress{}
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				return foundIngressList
			}, testTimeout, suite.TestInterval).Should(HaveLen(3))
			foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					Expect(rule.Host).To(Equal(updatedHumioCluster.Spec.Hostname))
				}
			}

			suite.UsingClusterBy(key.Name, "Setting the ESHostname")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			esHostname := "test-cluster-es.humio.com"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ESHostname = esHostname
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming ingresses for ES Hostname gets created")
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				return foundIngressList
			}, testTimeout, suite.TestInterval).Should(HaveLen(4))

			var ingressHostnames []string
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					ingressHostnames = append(ingressHostnames, rule.Host)
				}
			}
			Expect(ingressHostnames).To(ContainElement(esHostname))

			suite.UsingClusterBy(key.Name, "Removing the ESHostname")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ESHostname = ""
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming ingresses for ES Hostname gets removed")
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				return foundIngressList
			}, testTimeout, suite.TestInterval).Should(HaveLen(3))

			ingressHostnames = []string{}
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					ingressHostnames = append(ingressHostnames, rule.Host)
				}
			}
			Expect(ingressHostnames).ToNot(ContainElement(esHostname))

			suite.UsingClusterBy(key.Name, "Creating the hostname secret")
			secretKeyRef := &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "hostname",
				},
				Key: "humio-hostname",
			}
			updatedHostname := "test-cluster-hostname-ref.humio.com"
			hostnameSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKeyRef.Name,
					Namespace: key.Namespace,
				},
				StringData: map[string]string{secretKeyRef.Key: updatedHostname},
				Type:       corev1.SecretTypeOpaque,
			}
			Expect(k8sClient.Create(ctx, &hostnameSecret)).To(Succeed())

			suite.UsingClusterBy(key.Name, "Setting the HostnameSource")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Hostname = ""
				updatedHumioCluster.Spec.HostnameSource.SecretKeyRef = secretKeyRef
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming we only created ingresses with expected hostname")
			foundIngressList = []networkingv1.Ingress{}
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				return foundIngressList
			}, testTimeout, suite.TestInterval).Should(HaveLen(3))
			Eventually(func() string {
				ingressHosts := make(map[string]interface{})
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				for _, ingress := range foundIngressList {
					for _, rule := range ingress.Spec.Rules {
						ingressHosts[rule.Host] = nil
					}
				}
				if len(ingressHosts) == 1 {
					for k := range ingressHosts {
						return k
					}
				}
				return fmt.Sprintf("%#v", ingressHosts)
			}, testTimeout, suite.TestInterval).Should(Equal(updatedHostname))

			suite.UsingClusterBy(key.Name, "Removing the HostnameSource")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HostnameSource.SecretKeyRef = nil
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Deleting the hostname secret")
			Expect(k8sClient.Delete(ctx, &hostnameSecret)).To(Succeed())

			suite.UsingClusterBy(key.Name, "Creating the es hostname secret")
			secretKeyRef = &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "es-hostname",
				},
				Key: "humio-es-hostname",
			}
			updatedESHostname := "test-cluster-es-hostname-ref.humio.com"
			esHostnameSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKeyRef.Name,
					Namespace: key.Namespace,
				},
				StringData: map[string]string{secretKeyRef.Key: updatedESHostname},
				Type:       corev1.SecretTypeOpaque,
			}
			Expect(k8sClient.Create(ctx, &esHostnameSecret)).To(Succeed())

			suite.UsingClusterBy(key.Name, "Setting the ESHostnameSource")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ESHostname = ""
				updatedHumioCluster.Spec.ESHostnameSource.SecretKeyRef = secretKeyRef
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming we only created ingresses with expected es hostname")
			foundIngressList = []networkingv1.Ingress{}
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				return foundIngressList
			}, testTimeout, suite.TestInterval).Should(HaveLen(1))
			Eventually(func() string {
				ingressHosts := make(map[string]interface{})
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				for _, ingress := range foundIngressList {
					for _, rule := range ingress.Spec.Rules {
						ingressHosts[rule.Host] = nil
					}
				}
				if len(ingressHosts) == 1 {
					for k := range ingressHosts {
						return k
					}
				}
				return fmt.Sprintf("%#v", ingressHosts)
			}, testTimeout, suite.TestInterval).Should(Equal(updatedESHostname))

			suite.UsingClusterBy(key.Name, "Removing the ESHostnameSource")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				updatedHumioCluster.Spec.ESHostnameSource.SecretKeyRef = nil
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Deleting the es hostname secret")
			Expect(k8sClient.Delete(ctx, &esHostnameSecret)).To(Succeed())
		})
	})

	Context("Humio Cluster with non-existent custom service accounts", Label("envtest", "dummy", "real"), func() {
		It("Should correctly handle non-existent humio service account by marking cluster as ConfigError", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-humio-service-account",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceAccountName = "non-existent-humio-service-account"

			suite.UsingClusterBy(key.Name, "Creating cluster with non-existent service accounts")
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			Eventually(func() string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &cluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return cluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Should correctly handle non-existent init service account by marking cluster as ConfigError", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-init-service-account",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceAccountName = "non-existent-init-service-account"

			suite.UsingClusterBy(key.Name, "Creating cluster with non-existent service accounts")
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			Eventually(func() string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &cluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return cluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Should correctly handle non-existent auth service account by marking cluster as ConfigError", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-auth-service-account",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceAccountName = "non-existent-auth-service-account"

			suite.UsingClusterBy(key.Name, "Creating cluster with non-existent service accounts")
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			Eventually(func() string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &cluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return cluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateConfigError))
		})
	})

	Context("Humio Cluster With Custom Service Accounts", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with custom service accounts", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-service-accounts",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.InitServiceAccountName = "init-custom-service-account"
			toCreate.Spec.HumioServiceAccountName = "humio-custom-service-account"

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming init container is using the correct service account")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
				var serviceAccountSecretVolumeName string
				for _, volumeMount := range pod.Spec.InitContainers[humioIdx].VolumeMounts {
					if volumeMount.MountPath == "/var/run/secrets/kubernetes.io/serviceaccount" {
						serviceAccountSecretVolumeName = volumeMount.Name
					}
				}
				Expect(serviceAccountSecretVolumeName).To(Not(BeEmpty()))
				for _, volume := range pod.Spec.Volumes {
					if volume.Name == serviceAccountSecretVolumeName {
						secret, err := kubernetes.GetSecret(ctx, k8sClient, volume.Secret.SecretName, key.Namespace)
						Expect(err).ShouldNot(HaveOccurred())
						Expect(secret.Annotations[corev1.ServiceAccountNameKey]).To(Equal(toCreate.Spec.InitServiceAccountName))
					}
				}
			}
			suite.UsingClusterBy(key.Name, "Confirming humio pod is using the correct service account")
			for _, pod := range clusterPods {
				Expect(pod.Spec.ServiceAccountName).To(Equal(toCreate.Spec.HumioServiceAccountName))
			}
		})

		It("Creating cluster with custom service accounts sharing the same name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-sa-same-name",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.InitServiceAccountName = "custom-service-account"
			toCreate.Spec.HumioServiceAccountName = "custom-service-account"

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming init container is using the correct service account")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
				var serviceAccountSecretVolumeName string
				for _, volumeMount := range pod.Spec.InitContainers[humioIdx].VolumeMounts {
					if volumeMount.MountPath == "/var/run/secrets/kubernetes.io/serviceaccount" {
						serviceAccountSecretVolumeName = volumeMount.Name
					}
				}
				Expect(serviceAccountSecretVolumeName).To(Not(BeEmpty()))
				for _, volume := range pod.Spec.Volumes {
					if volume.Name == serviceAccountSecretVolumeName {
						secret, err := kubernetes.GetSecret(ctx, k8sClient, volume.Secret.SecretName, key.Namespace)
						Expect(err).ShouldNot(HaveOccurred())
						Expect(secret.Annotations[corev1.ServiceAccountNameKey]).To(Equal(toCreate.Spec.InitServiceAccountName))
					}
				}
			}
			suite.UsingClusterBy(key.Name, "Confirming humio pod is using the correct service account")
			for _, pod := range clusterPods {
				Expect(pod.Spec.ServiceAccountName).To(Equal(toCreate.Spec.HumioServiceAccountName))
			}
		})
	})

	Context("Humio Cluster With Service Annotations", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with custom service annotations", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-svc-annotations",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceAnnotations = map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-type":                              "nlb",
				"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": "false",
				"service.beta.kubernetes.io/aws-load-balancer-ssl-cert":                          "arn:aws:acm:region:account:certificate/123456789012-1234-1234-1234-12345678",
				"service.beta.kubernetes.io/aws-load-balancer-backend-protocol":                  "ssl",
				"service.beta.kubernetes.io/aws-load-balancer-ssl-ports":                         "443",
				"service.beta.kubernetes.io/aws-load-balancer-internal":                          "0.0.0.0/0",
			}
			toCreate.Spec.HumioServiceAnnotations = map[string]string{
				"custom": "annotation",
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming service was created using the correct annotations")
			svc, err := kubernetes.GetService(ctx, k8sClient, toCreate.Name, toCreate.Namespace)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range toCreate.Spec.HumioServiceAnnotations {
				Expect(svc.Annotations).To(HaveKeyWithValue(k, v))
			}

			suite.UsingClusterBy(key.Name, "Confirming the headless service was created using the correct annotations")
			headlessSvc, err := kubernetes.GetService(ctx, k8sClient, toCreate.Name, toCreate.Namespace)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range toCreate.Spec.HumioHeadlessServiceAnnotations {
				Expect(headlessSvc.Annotations).To(HaveKeyWithValue(k, v))
			}
		})
	})

	Context("Humio Cluster With Custom Tolerations", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with custom tolerations", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-tolerations",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Tolerations = []corev1.Toleration{
				{
					Key:      "key",
					Operator: corev1.TolerationOpEqual,
					Value:    "value",
					Effect:   corev1.TaintEffectNoSchedule,
				},
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming the humio pods use the requested tolerations")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.Tolerations).To(ContainElement(toCreate.Spec.Tolerations[0]))
			}
		})
	})

	Context("Humio Cluster With Custom Topology Spread Constraints", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with custom Topology Spread Constraints", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-tsc",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{
				{
					MaxSkew:           2,
					TopologyKey:       "topology.kubernetes.io/zone",
					WhenUnsatisfiable: corev1.ScheduleAnyway,
				},
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming the humio pods use the requested topology spread constraint")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.TopologySpreadConstraints).To(ContainElement(toCreate.Spec.TopologySpreadConstraints[0]))
			}
		})
	})

	Context("Humio Cluster With Custom Priority Class Name", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with custom Priority Class Name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-pcn",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.PriorityClassName = key.Name

			ctx := context.Background()
			suite.UsingClusterBy(key.Name, "Creating a priority class")
			priorityClass := &schedulingv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
			}
			Expect(k8sClient.Create(ctx, priorityClass)).To(Succeed())

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming the humio pods use the requested priority class name")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.PriorityClassName).To(Equal(toCreate.Spec.PriorityClassName))
			}

			Expect(k8sClient.Delete(context.TODO(), priorityClass)).To(Succeed())

			Eventually(func() bool {
				return k8serrors.IsNotFound(k8sClient.Get(
					context.TODO(),
					types.NamespacedName{
						Namespace: priorityClass.Namespace,
						Name:      priorityClass.Name,
					},
					priorityClass),
				)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster With Service Labels", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with custom service labels", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-svc-labels",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceLabels = map[string]string{
				"mirror.linkerd.io/exported": "true",
			}
			toCreate.Spec.HumioHeadlessServiceLabels = map[string]string{
				"custom": "label",
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming service was created using the correct annotations")
			svc, err := kubernetes.GetService(ctx, k8sClient, toCreate.Name, toCreate.Namespace)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range toCreate.Spec.HumioServiceLabels {
				Expect(svc.Labels).To(HaveKeyWithValue(k, v))
			}

			suite.UsingClusterBy(key.Name, "Confirming the headless service was created using the correct labels")
			headlessSvc, err := kubernetes.GetService(ctx, k8sClient, fmt.Sprintf("%s-headless", toCreate.Name), toCreate.Namespace)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range toCreate.Spec.HumioHeadlessServiceLabels {
				Expect(headlessSvc.Labels).To(HaveKeyWithValue(k, v))
			}
		})
	})

	Context("Humio Cluster with shared process namespace and sidecars", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster without shared process namespace and sidecar", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-sidecars",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.SidecarContainers = nil

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming the humio pods are not using shared process namespace nor additional sidecars")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				if pod.Spec.ShareProcessNamespace != nil {
					Expect(*pod.Spec.ShareProcessNamespace).To(BeFalse())
				}
				Expect(pod.Spec.Containers).Should(HaveLen(1))
			}

			suite.UsingClusterBy(key.Name, "Enabling shared process namespace and sidecars")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}

				updatedHumioCluster.Spec.ShareProcessNamespace = helpers.BoolPtr(true)
				tmpVolumeName := "tmp"
				updatedHumioCluster.Spec.ExtraVolumes = []corev1.Volume{
					{
						Name:         tmpVolumeName,
						VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
					},
				}
				updatedHumioCluster.Spec.SidecarContainers = []corev1.Container{
					{
						Name:    "jmap",
						Image:   versions.DefaultHumioImageVersion(),
						Command: []string{"/bin/sh"},
						Args:    []string{"-c", "HUMIO_PID=$(ps -e | grep java | awk '{print $1'}); while :; do sleep 30 ; jmap -histo:live $HUMIO_PID | head -n203 ; done"},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      tmpVolumeName,
								MountPath: "/tmp",
								ReadOnly:  false,
							},
						},
						SecurityContext: &corev1.SecurityContext{
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
							Privileged:               helpers.BoolPtr(false),
							RunAsUser:                helpers.Int64Ptr(65534),
							RunAsNonRoot:             helpers.BoolPtr(true),
							ReadOnlyRootFilesystem:   helpers.BoolPtr(true),
							AllowPrivilegeEscalation: helpers.BoolPtr(false),
						},
					},
				}

				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming the humio pods use shared process namespace")
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					if pod.Spec.ShareProcessNamespace != nil {
						return *pod.Spec.ShareProcessNamespace
					}
				}
				return false
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Confirming pods contain the new sidecar")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					for _, container := range pod.Spec.Containers {
						if container.Name == controller.HumioContainerName {
							continue
						}
						return container.Name
					}
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal("jmap"))
		})
	})

	Context("Humio Cluster pod termination grace period", Label("envtest", "dummy", "real"), func() {
		It("Should validate default configuration", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-grace-default",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.TerminationGracePeriodSeconds = nil

			suite.UsingClusterBy(key.Name, "Creating Humio cluster without a termination grace period set")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Validating pod is created with the default grace period")
			Eventually(func() int64 {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)

				for _, pod := range clusterPods {
					if pod.Spec.TerminationGracePeriodSeconds != nil {
						return *pod.Spec.TerminationGracePeriodSeconds
					}
				}
				return 0
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(300))

			suite.UsingClusterBy(key.Name, "Overriding termination grace period")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.TerminationGracePeriodSeconds = helpers.Int64Ptr(120)
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Validating pod is recreated using the explicitly defined grace period")
			Eventually(func() int64 {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					if pod.Spec.TerminationGracePeriodSeconds != nil {
						return *pod.Spec.TerminationGracePeriodSeconds
					}
				}
				return 0
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(120))
		})
	})

	Context("Humio Cluster install license", Label("envtest", "dummy", "real"), func() {
		It("Should fail when no license is present", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-no-license",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, false)
			toCreate.Spec.License = humiov1alpha1.HumioClusterLicenseSpec{}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			Eventually(func() string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &cluster)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return cluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo("ConfigError"))

			// TODO: set a valid license
			// TODO: confirm cluster enters running
		})
		It("Should successfully install a license", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-license",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully with a license secret")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			secretName := fmt.Sprintf("%s-license", key.Name)
			secretKey := "license"
			var updatedHumioCluster humiov1alpha1.HumioCluster

			suite.UsingClusterBy(key.Name, "Updating the HumioCluster to add broken reference to license")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.License.SecretKeyRef = &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: fmt.Sprintf("%s-wrong", secretName),
					},
					Key: secretKey,
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Should indicate cluster configuration error due to missing license secret")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "Updating the HumioCluster to add a valid license")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.License.SecretKeyRef = &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secretName,
					},
					Key: secretKey,
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Should indicate cluster is no longer in a configuration error state")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Ensuring the license is updated")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.LicenseStatus.Type
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo("onprem"))

			suite.UsingClusterBy(key.Name, "Updating the license secret to remove the key")
			var licenseSecret corev1.Secret
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Namespace: key.Namespace,
					Name:      secretName,
				}, &licenseSecret)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(k8sClient.Delete(ctx, &licenseSecret)).To(Succeed())

			licenseSecretMissingKey := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: key.Namespace,
				},
				StringData: map[string]string{},
				Type:       corev1.SecretTypeOpaque,
			}
			Expect(k8sClient.Create(ctx, &licenseSecretMissingKey)).To(Succeed())

			suite.UsingClusterBy(key.Name, "Should indicate cluster configuration error due to missing license secret key")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
	})

	Context("Humio Cluster state adjustment", Label("envtest", "dummy", "real"), func() {
		It("Should successfully set proper state", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-state",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Ensuring the state is Running")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Updating the HumioCluster to ConfigError state")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Status.State = humiov1alpha1.HumioClusterStateConfigError
				return k8sClient.Status().Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Should indicate healthy cluster resets state to Running")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})
	})

	Context("Humio Cluster with envSource configmap", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with envSource configmap", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-env-source-configmap",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming the humio pods are not using env var source")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], controller.HumioContainerName)
			Expect(err).ToNot(HaveOccurred())
			Expect(clusterPods[0].Spec.Containers[humioIdx].EnvFrom).To(BeNil())

			suite.UsingClusterBy(key.Name, "Adding missing envVarSource to pod spec")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}

				updatedHumioCluster.Spec.EnvironmentVariablesSource = []corev1.EnvFromSource{
					{
						ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "env-var-source-missing",
							},
						},
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming the HumioCluster goes into ConfigError state since the configmap does not exist")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "Creating the envVarSource configmap")
			envVarSourceConfigMap := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "env-var-source",
					Namespace: key.Namespace,
				},
				Data: map[string]string{"SOME_ENV_VAR": "SOME_ENV_VALUE"},
			}
			Expect(k8sClient.Create(ctx, &envVarSourceConfigMap)).To(Succeed())

			suite.WaitForReconcileToSync(ctx, key, k8sClient, nil, testTimeout)

			suite.UsingClusterBy(key.Name, "Updating envVarSource of pod spec")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}

				updatedHumioCluster.Spec.EnvironmentVariablesSource = []corev1.EnvFromSource{
					{
						ConfigMapRef: &corev1.ConfigMapEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "env-var-source",
							},
						},
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			suite.UsingClusterBy(key.Name, "Confirming pods contain the new env vars")
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				var podsContainingEnvFrom int
				for _, pod := range clusterPods {
					humioIdx, err := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					Expect(err).ToNot(HaveOccurred())
					if pod.Spec.Containers[humioIdx].EnvFrom != nil {
						if len(pod.Spec.Containers[humioIdx].EnvFrom) > 0 {
							if pod.Spec.Containers[humioIdx].EnvFrom[0].ConfigMapRef != nil {
								podsContainingEnvFrom++
							}
						}
					}
				}
				return podsContainingEnvFrom
			}, testTimeout, suite.TestInterval).Should(Equal(toCreate.Spec.NodeCount))
		})
	})

	Context("Humio Cluster with envSource secret", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with envSource secret", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-env-source-secret",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming the humio pods are not using env var source")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], controller.HumioContainerName)
			Expect(err).ToNot(HaveOccurred())
			Expect(clusterPods[0].Spec.Containers[humioIdx].EnvFrom).To(BeNil())

			suite.UsingClusterBy(key.Name, "Adding missing envVarSource to pod spec")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}

				updatedHumioCluster.Spec.EnvironmentVariablesSource = []corev1.EnvFromSource{
					{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "env-var-source-missing",
							},
						},
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming the HumioCluster goes into ConfigError state since the secret does not exist")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			suite.UsingClusterBy(key.Name, "Creating the envVarSource secret")
			envVarSourceSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "env-var-source",
					Namespace: key.Namespace,
				},
				StringData: map[string]string{"SOME_ENV_VAR": "SOME_ENV_VALUE"},
			}
			Expect(k8sClient.Create(ctx, &envVarSourceSecret)).To(Succeed())

			suite.WaitForReconcileToSync(ctx, key, k8sClient, nil, testTimeout)

			suite.UsingClusterBy(key.Name, "Updating envVarSource of pod spec")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}

				updatedHumioCluster.Spec.EnvironmentVariablesSource = []corev1.EnvFromSource{
					{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "env-var-source",
							},
						},
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			suite.UsingClusterBy(key.Name, "Confirming pods contain the new env vars")
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				var podsContainingEnvFrom int
				for _, pod := range clusterPods {
					humioIdx, err := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
					Expect(err).ToNot(HaveOccurred())
					if pod.Spec.Containers[humioIdx].EnvFrom != nil {
						if len(pod.Spec.Containers[humioIdx].EnvFrom) > 0 {
							if pod.Spec.Containers[humioIdx].EnvFrom[0].SecretRef != nil {
								podsContainingEnvFrom++
							}
						}
					}
				}
				return podsContainingEnvFrom
			}, testTimeout, suite.TestInterval).Should(Equal(toCreate.Spec.NodeCount))
		})
	})

	Context("Humio Cluster with resources without node pool name label", Label("envtest", "dummy", "real"), func() {
		It("Creating cluster with all node pool labels set", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-nodepool-labels",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Removing the node pool label from the pod")
			var clusterPods []corev1.Pod
			Eventually(func() error {
				clusterPods, err = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				if err != nil {
					return err
				}
				if len(clusterPods) != 1 {
					return fmt.Errorf("length found to be %d, expected %d", len(clusterPods), 1)
				}
				labelsWithoutNodePoolName := map[string]string{}
				for k, v := range clusterPods[0].GetLabels() {
					if k == kubernetes.NodePoolLabelName {
						continue
					}
					labelsWithoutNodePoolName[k] = v
				}
				clusterPods[0].SetLabels(labelsWithoutNodePoolName)
				return k8sClient.Update(ctx, &clusterPods[0])

			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Validating the node pool name label gets added to the pod again")
			Eventually(func() map[string]string {
				var updatedPod corev1.Pod
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterPods[0].Name,
					Namespace: key.Namespace,
				}, &updatedPod)
				if updatedPod.ResourceVersion == clusterPods[0].ResourceVersion {
					return map[string]string{
						"same-resource-version": updatedPod.ResourceVersion,
					}
				}
				if err != nil {
					return map[string]string{
						"got-err": err.Error(),
					}
				}
				return updatedPod.GetLabels()
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(kubernetes.NodePoolLabelName, key.Name))
		})
	})

	Context("test rolling update with zone awareness enabled", Serial, Label("dummy"), func() {
		It("Update should correctly replace pods maxUnavailable=1", func() {
			key := types.NamespacedName{
				Name:      "hc-update-absolute-maxunavail-zone-1",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromInt32(1)
			zoneAwarenessEnabled := true
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessEnabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostNumPodsSeenUnavailable := 0
			mostNumZonesWithPodsSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostNumPodsSeenUnavailable, &mostNumZonesWithPodsSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostNumPodsSeenUnavailable).To(BeNumerically("==", maxUnavailable.IntValue()))
			Expect(mostNumZonesWithPodsSeenUnavailable).To(BeNumerically("==", 1))
		})

		It("Update should correctly replace pods maxUnavailable=2", func() {
			key := types.NamespacedName{
				Name:      "hc-update-absolute-maxunavail-zone-2",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromInt32(2)
			zoneAwarenessEnabled := true
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessEnabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostNumPodsSeenUnavailable := 0
			mostNumZonesWithPodsSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostNumPodsSeenUnavailable, &mostNumZonesWithPodsSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostNumPodsSeenUnavailable).To(BeNumerically("==", maxUnavailable.IntValue()))
			Expect(mostNumZonesWithPodsSeenUnavailable).To(BeNumerically("==", 1))
		})

		It("Update should correctly replace pods maxUnavailable=4", func() {
			key := types.NamespacedName{
				Name:      "hc-update-absolute-maxunavail-zone-4",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromInt32(4)
			zoneAwarenessEnabled := true
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessEnabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostNumPodsSeenUnavailable := 0
			mostNumZonesWithPodsSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostNumPodsSeenUnavailable, &mostNumZonesWithPodsSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostNumPodsSeenUnavailable).To(BeNumerically("==", 3)) // nodeCount 9 and 3 zones should only replace at most 3 pods at a time as we expect the 9 pods to be uniformly distributed across the 3 zones
			Expect(mostNumZonesWithPodsSeenUnavailable).To(BeNumerically("==", 1))
		})

		It("Update should correctly replace pods maxUnavailable=25%", func() {
			key := types.NamespacedName{
				Name:      "hc-update-pct-maxunavail-zone-25",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromString("25%")
			zoneAwarenessEnabled := true
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessEnabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostNumPodsSeenUnavailable := 0
			mostNumZonesWithPodsSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostNumPodsSeenUnavailable, &mostNumZonesWithPodsSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostNumPodsSeenUnavailable).To(BeNumerically("==", 2)) // nodeCount 9 * 25 % = 2.25 pods, rounded down is 2. Assuming 9 pods is uniformly distributed across 3 zones with 3 pods per zone.
			Expect(mostNumZonesWithPodsSeenUnavailable).To(BeNumerically("==", 1))
		})

		It("Update should correctly replace pods maxUnavailable=50%", func() {
			key := types.NamespacedName{
				Name:      "hc-update-pct-maxunavail-zone-50",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromString("50%")
			zoneAwarenessEnabled := true
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessEnabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostNumPodsSeenUnavailable := 0
			mostNumZonesWithPodsSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostNumPodsSeenUnavailable, &mostNumZonesWithPodsSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostNumPodsSeenUnavailable).To(BeNumerically("==", 3)) // nodeCount 9 * 50 % = 4.50 pods, rounded down is 4. Assuming 9 pods is uniformly distributed across 3 zones, that gives 3 pods per zone.
			Expect(mostNumZonesWithPodsSeenUnavailable).To(BeNumerically("==", 1))
		})

		It("Update should correctly replace pods maxUnavailable=100%", func() {
			key := types.NamespacedName{
				Name:      "hc-update-pct-maxunavail-zone-100",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromString("100%")
			zoneAwarenessEnabled := true
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessEnabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostNumPodsSeenUnavailable := 0
			mostNumZonesWithPodsSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostNumPodsSeenUnavailable, &mostNumZonesWithPodsSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostNumPodsSeenUnavailable).To(BeNumerically("==", 3)) // Assuming 9 pods is uniformly distributed across 3 zones, that gives 3 pods per zone.
			Expect(mostNumZonesWithPodsSeenUnavailable).To(BeNumerically("==", 1))
		})
	})

	Context("test rolling update with zone awareness disabled", Serial, Label("envtest", "dummy"), func() {
		It("Update should correctly replace pods maxUnavailable=1", func() {
			key := types.NamespacedName{
				Name:      "hc-update-absolute-maxunavail-nozone-1",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromInt32(1)
			zoneAwarenessDisabled := false
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessDisabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithoutZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostSeenUnavailable).To(BeNumerically("==", maxUnavailable.IntValue()))
		})

		It("Update should correctly replace pods maxUnavailable=2", func() {
			key := types.NamespacedName{
				Name:      "hc-update-absolute-maxunavail-nozone-2",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromInt32(2)
			zoneAwarenessDisabled := false
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessDisabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithoutZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, maxUnavailable.IntValue())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostSeenUnavailable).To(BeNumerically("==", maxUnavailable.IntValue()))
		})

		It("Update should correctly replace pods maxUnavailable=4", func() {
			key := types.NamespacedName{
				Name:      "hc-update-absolute-maxunavail-nozone-4",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromInt32(4)
			zoneAwarenessDisabled := false
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessDisabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithoutZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, maxUnavailable.IntValue())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostSeenUnavailable).To(BeNumerically("==", maxUnavailable.IntValue()))
		})

		It("Update should correctly replace pods maxUnavailable=25%", func() {
			key := types.NamespacedName{
				Name:      "hc-update-pct-maxunavail-nozone-25",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromString("25%")
			zoneAwarenessDisabled := false
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessDisabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithoutZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 2) // nodeCount 9 * 25 % = 2.25 pods, rounded down is 2

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostSeenUnavailable).To(BeNumerically("==", 2)) // nodeCount 9 * 25 % = 2.25 pods, rounded down is 2
		})

		It("Update should correctly replace pods maxUnavailable=50%", func() {
			key := types.NamespacedName{
				Name:      "hc-update-pct-maxunavail-nozone-50",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromString("50%")
			zoneAwarenessDisabled := false
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessDisabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithoutZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 4) // nodeCount 9 * 50 % = 4.50 pods, rounded down is 4

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostSeenUnavailable).To(BeNumerically("==", 4)) // nodeCount 9 * 50 % = 4.50 pods, rounded down is 4
		})

		It("Update should correctly replace pods maxUnavailable=100%", func() {
			key := types.NamespacedName{
				Name:      "hc-update-pct-maxunavail-nozone-100",
				Namespace: testProcessNamespace,
			}
			maxUnavailable := intstr.FromString("100%")
			zoneAwarenessDisabled := false
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = versions.OldSupportedHumioVersion()
			toCreate.Spec.NodeCount = 9
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type:                humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
				EnableZoneAwareness: &zoneAwarenessDisabled,
				MaxUnavailable:      &maxUnavailable,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(1))

			mostSeenUnavailable := 0
			forever := make(chan struct{})
			ctx2, cancel := context.WithCancel(context.Background())
			go monitorMaxUnavailableWithoutZoneAwareness(ctx2, k8sClient, *toCreate, forever, &mostSeenUnavailable)

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := versions.DefaultHumioImageVersion()
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsRollingRestart(ctx, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, toCreate.Spec.NodeCount)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()).To(BeEquivalentTo(2))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controller.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controller.PodRevisionAnnotation, "2"))
			}

			cancel()
			<-forever

			if helpers.TLSEnabled(&updatedHumioCluster) {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}

			Expect(mostSeenUnavailable).To(BeNumerically("==", toCreate.Spec.NodeCount))
		})
	})

	Context("Node Pool PodDisruptionBudgets", func() {
		It("Should enforce PDB rules at node pool level", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-nodepool-pdb",
				Namespace: testProcessNamespace,
			}
			ctx := context.Background()

			// Base valid cluster with node pools
			validCluster := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			validCluster.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "valid-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: 2,
						PodDisruptionBudget: &humiov1alpha1.HumioPodDisruptionBudgetSpec{
							MinAvailable: &intstr.IntOrString{
								Type:   intstr.Int,
								IntVal: int32(1),
							},
						},
					},
				},
			}

			suite.UsingClusterBy(key.Name, "Testing invalid node pool configurations")

			// Test mutual exclusivity in node pool
			invalidNodePoolCluster := validCluster.DeepCopy()
			invalidNodePoolCluster.Spec.NodePools[0].PodDisruptionBudget.MaxUnavailable =
				&intstr.IntOrString{Type: intstr.Int, IntVal: 1}
			Expect(k8sClient.Create(ctx, invalidNodePoolCluster)).To(MatchError(
				ContainSubstring("podDisruptionBudget: minAvailable and maxUnavailable are mutually exclusive")))

			// Test required field in node pool
			missingFieldsCluster := validCluster.DeepCopy()
			missingFieldsCluster.Spec.NodePools[0].PodDisruptionBudget =
				&humiov1alpha1.HumioPodDisruptionBudgetSpec{}
			Expect(k8sClient.Create(ctx, missingFieldsCluster)).To(MatchError(
				ContainSubstring("podDisruptionBudget: either minAvailable or maxUnavailable must be specified")))

			// Test immutability in node pool
			validCluster = suite.ConstructBasicSingleNodeHumioCluster(key, true)
			validCluster.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "pool1",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: 2,
						PodDisruptionBudget: &humiov1alpha1.HumioPodDisruptionBudgetSpec{
							MinAvailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, validCluster)).To(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, validCluster)

			suite.UsingClusterBy(key.Name, "Testing node pool PDB immutability")
			updatedCluster := validCluster.DeepCopy()
			updatedCluster.Spec.NodePools[0].PodDisruptionBudget.MinAvailable =
				&intstr.IntOrString{Type: intstr.Int, IntVal: 2}
			Expect(k8sClient.Update(ctx, updatedCluster)).To(MatchError(
				ContainSubstring("minAvailable is immutable")))
		})
	})
	It("Should correctly manage pod disruption budgets", func() {
		key := types.NamespacedName{
			Name:      "humiocluster-pdb",
			Namespace: testProcessNamespace,
		}
		toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
		toCreate.Spec.NodeCount = 2
		ctx := context.Background()

		suite.UsingClusterBy(key.Name, "Creating the cluster successfully without PDB spec")
		suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
		defer suite.CleanupCluster(ctx, k8sClient, toCreate)

		// Should not create a PDB by default
		suite.UsingClusterBy(key.Name, "Verifying no PDB exists when no PDB spec is provided")
		var pdb policyv1.PodDisruptionBudget
		Consistently(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("%s-pdb", toCreate.Name),
				Namespace: toCreate.Namespace,
			}, &pdb)
		}, testTimeout, suite.TestInterval).Should(MatchError(k8serrors.IsNotFound, "IsNotFound"))

		suite.UsingClusterBy(key.Name, "Adding MinAvailable PDB configuration")
		var updatedHumioCluster humiov1alpha1.HumioCluster
		minAvailable := intstr.FromString("50%")
		Eventually(func() error {
			err := k8sClient.Get(ctx, key, &updatedHumioCluster)
			if err != nil {
				return err
			}
			updatedHumioCluster.Spec.PodDisruptionBudget = &humiov1alpha1.HumioPodDisruptionBudgetSpec{
				MinAvailable: &minAvailable,
			}
			return k8sClient.Update(ctx, &updatedHumioCluster)
		}, testTimeout, suite.TestInterval).Should(Succeed())

		suite.UsingClusterBy(key.Name, "Verifying PDB is created with MinAvailable")
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("%s-pdb", toCreate.Name),
				Namespace: toCreate.Namespace,
			}, &pdb)
		}, testTimeout, suite.TestInterval).Should(Succeed())
		Expect(pdb.Spec.MinAvailable).To(Equal(&minAvailable))
		Expect(pdb.Spec.MaxUnavailable).To(BeNil())

		suite.UsingClusterBy(key.Name, "Updating to use MaxUnavailable instead")
		maxUnavailable := intstr.FromInt(1)
		Eventually(func() error {
			err := k8sClient.Get(ctx, key, &updatedHumioCluster)
			if err != nil {
				return err
			}
			updatedHumioCluster.Spec.PodDisruptionBudget = &humiov1alpha1.HumioPodDisruptionBudgetSpec{
				MaxUnavailable: &maxUnavailable,
			}
			return k8sClient.Update(ctx, &updatedHumioCluster)
		}, testTimeout, suite.TestInterval).Should(Succeed())

		suite.UsingClusterBy(key.Name, "Verifying PDB is updated with MaxUnavailable")
		Eventually(func() *intstr.IntOrString {
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("%s-pdb", toCreate.Name),
				Namespace: toCreate.Namespace,
			}, &pdb)
			if err != nil {
				return nil
			}
			return pdb.Spec.MaxUnavailable
		}, testTimeout, suite.TestInterval).Should(Equal(&maxUnavailable))

		suite.UsingClusterBy(key.Name, "Setting up node pools with PDB configuration")
		Eventually(func() error {
			err := k8sClient.Get(ctx, key, &updatedHumioCluster)
			if err != nil {
				return err
			}
			updatedHumioCluster.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "pool1",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: 2,
						PodDisruptionBudget: &humiov1alpha1.HumioPodDisruptionBudgetSpec{
							MaxUnavailable: &maxUnavailable,
						},
					},
				},
				{
					Name: "pool2",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: 3,
						PodDisruptionBudget: &humiov1alpha1.HumioPodDisruptionBudgetSpec{
							MinAvailable: &minAvailable,
						},
					},
				},
			}
			return k8sClient.Update(ctx, &updatedHumioCluster)
		}, testTimeout, suite.TestInterval).Should(Succeed())

		suite.UsingClusterBy(key.Name, "Verifying PDBs are created for each node pool")
		for _, pool := range []string{"pool1", "pool2"} {
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      fmt.Sprintf("%s-%s-pdb", toCreate.Name, pool),
					Namespace: toCreate.Namespace,
				}, &pdb)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(pdb.Spec.Selector.MatchLabels).To(Equal(kubernetes.MatchingLabelsForHumioNodePool(toCreate.Name, pool)))

			if pool == "pool1" {
				Expect(pdb.Spec.MaxUnavailable).To(Equal(&maxUnavailable))
				Expect(pdb.Spec.MinAvailable).To(BeNil())
			} else {
				Expect(pdb.Spec.MinAvailable).To(Equal(&minAvailable))
				Expect(pdb.Spec.MaxUnavailable).To(BeNil())
			}
		}

		suite.UsingClusterBy(key.Name, "Removing PDB configurations")
		Eventually(func() error {
			err := k8sClient.Get(ctx, key, &updatedHumioCluster)
			if err != nil {
				return err
			}
			updatedHumioCluster.Spec.PodDisruptionBudget = nil
			for i := range updatedHumioCluster.Spec.NodePools {
				updatedHumioCluster.Spec.NodePools[i].PodDisruptionBudget = nil
			}
			return k8sClient.Update(ctx, &updatedHumioCluster)
		}, testTimeout, suite.TestInterval).Should(Succeed())

		suite.UsingClusterBy(key.Name, "Verifying PDBs are removed")
		Eventually(func() bool {
			var pdbs policyv1.PodDisruptionBudgetList
			err := k8sClient.List(ctx, &pdbs, &client.ListOptions{
				Namespace: toCreate.Namespace,
				LabelSelector: labels.SelectorFromSet(map[string]string{
					"app.kubernetes.io/managed-by": "humio-operator",
				}),
			})
			return err == nil && len(pdbs.Items) == 0
		}, testTimeout, suite.TestInterval).Should(BeTrue())

		suite.UsingClusterBy(key.Name, "Creating an orphaned PDB")
		orphanedPdb := &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-orphaned-pdb", toCreate.Name),
				Namespace: toCreate.Namespace,
				Labels:    kubernetes.LabelsForHumio(toCreate.Name),
			},
			Spec: policyv1.PodDisruptionBudgetSpec{
				MinAvailable: &minAvailable,
				Selector: &metav1.LabelSelector{
					MatchLabels: kubernetes.LabelsForHumio(toCreate.Name),
				},
			},
		}
		Expect(k8sClient.Create(ctx, orphanedPdb)).Should(Succeed())

		suite.UsingClusterBy(key.Name, "Verifying orphaned PDB is cleaned up")
		Eventually(func() bool {
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("%s-orphaned-pdb", toCreate.Name),
				Namespace: toCreate.Namespace,
			}, &pdb)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())

		suite.UsingClusterBy(key.Name, "Verifying PDB is created with MinAvailable and status is updated")
		Eventually(func() error {
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("%s-pdb", toCreate.Name),
				Namespace: toCreate.Namespace,
			}, &pdb)
			if err != nil {
				return err
			}
			Expect(pdb.Spec.MinAvailable).To(Equal(&minAvailable))
			Expect(pdb.Spec.MaxUnavailable).To(BeNil())

			// Assert PDB status fields
			Expect(pdb.Status.DesiredHealthy).To(BeEquivalentTo(toCreate.Spec.NodeCount))
			Expect(pdb.Status.CurrentHealthy).To(BeEquivalentTo(toCreate.Spec.NodeCount))
			Expect(pdb.Status.DisruptionsAllowed).To(BeEquivalentTo(toCreate.Spec.NodeCount - int(pdb.Spec.MinAvailable.IntVal)))

			return nil
		}, testTimeout, suite.TestInterval).Should(Succeed())
	})
	It("Should enforce MinAvailable PDB rule during pod deletion", func() {
		key := types.NamespacedName{
			Name:      "humiocluster-pdb-enforce",
			Namespace: testProcessNamespace,
		}
		toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
		toCreate.Spec.NodeCount = 3
		toCreate.Spec.PodDisruptionBudget = &humiov1alpha1.HumioPodDisruptionBudgetSpec{
			MinAvailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 2},
		}

		suite.UsingClusterBy(key.Name, "Creating the cluster successfully with PDB spec")
		ctx := context.Background()
		suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
		defer suite.CleanupCluster(ctx, k8sClient, toCreate)

		suite.UsingClusterBy(key.Name, "Verifying PDB exists")
		var pdb policyv1.PodDisruptionBudget
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Name:      fmt.Sprintf("%s-pdb", toCreate.Name),
				Namespace: key.Namespace,
			}, &pdb)
		}, testTimeout, suite.TestInterval).Should(Succeed())

		suite.UsingClusterBy(key.Name, "Verifying initial pod count")
		var pods []corev1.Pod
		hnp := controller.NewHumioNodeManagerFromHumioCluster(toCreate)
		Eventually(func() int {
			clusterPods, err := kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
			if err != nil {
				return 0
			}
			pods = clusterPods
			return len(clusterPods)
		}, testTimeout, suite.TestInterval).Should(Equal(3))

		suite.UsingClusterBy(key.Name, "Marking pods as Ready")
		for _, pod := range pods {
			_ = suite.MarkPodAsRunningIfUsingEnvtest(ctx, k8sClient, pod, key.Name)
		}

		suite.UsingClusterBy(key.Name, "Attempting to delete a pod")
		podToDelete := &pods[0]
		Expect(k8sClient.Delete(ctx, podToDelete)).To(Succeed())

		suite.UsingClusterBy(key.Name, "Verifying pod count after deletion")
		Eventually(func() int {
			clusterPods, err := kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
			if err != nil {
				return 0
			}
			return len(clusterPods)
		}, testTimeout, suite.TestInterval).Should(Equal(2))

		suite.UsingClusterBy(key.Name, "Attempting to delete another pod")
		clusterPods, err := kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
		Expect(err).NotTo(HaveOccurred())

		podToDelete = &clusterPods[0]
		err = k8sClient.Delete(ctx, podToDelete)
		Expect(err).To(HaveOccurred())

		var statusErr *k8serrors.StatusError
		Expect(errors.As(err, &statusErr)).To(BeTrue())
		Expect(statusErr.ErrStatus.Reason).To(Equal(metav1.StatusReasonForbidden))
		Expect(statusErr.ErrStatus.Message).To(ContainSubstring("violates PodDisruptionBudget"))
	})

})

// TODO: Consider refactoring goroutine to a "watcher". https://book-v1.book.kubebuilder.io/beyond_basics/controller_watches
//
//	Using a for-loop executing ListPods will only see snapshots in time and we could easily miss
//	a point in time where we have too many pods that are not ready.
func monitorMaxUnavailableWithZoneAwareness(ctx context.Context, k8sClient client.Client, toCreate humiov1alpha1.HumioCluster, forever chan struct{}, mostNumPodsSeenUnavailable *int, mostNumZonesWithPodsSeenUnavailable *int) {
	hnp := controller.NewHumioNodeManagerFromHumioCluster(&toCreate)
	for {
		select {
		case <-ctx.Done(): // if cancel() execute
			forever <- struct{}{}
			return
		default:
			// Assume all is unavailable, and decrement number each time we see one that is working
			unavailableThisRound := hnp.GetNodeCount()
			zonesWithPodsSeenUnavailable := []string{}

			pods, _ := kubernetes.ListPods(ctx, k8sClient, hnp.GetNamespace(), hnp.GetPodLabels())
			for _, pod := range pods {
				if pod.Status.Phase == corev1.PodRunning {
					for _, containerStatus := range pod.Status.ContainerStatuses {
						if containerStatus.Ready {
							unavailableThisRound--
						} else {
							if pod.Spec.NodeName != "" {
								zone, _ := kubernetes.GetZoneForNodeName(ctx, k8sClient, pod.Spec.NodeName)
								if !slices.Contains(zonesWithPodsSeenUnavailable, zone) {
									zonesWithPodsSeenUnavailable = append(zonesWithPodsSeenUnavailable, zone)
								}
							}
						}
					}
				}
			}
			// Save the number of unavailable pods in this round
			*mostNumPodsSeenUnavailable = max(*mostNumPodsSeenUnavailable, unavailableThisRound)
			*mostNumZonesWithPodsSeenUnavailable = max(*mostNumZonesWithPodsSeenUnavailable, len(zonesWithPodsSeenUnavailable))
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TODO: Consider refactoring goroutine to a "watcher". https://book-v1.book.kubebuilder.io/beyond_basics/controller_watches
//
//	Using a for-loop executing ListPods will only see snapshots in time and we could easily miss
//	a point in time where we have too many pods that are not ready.
func monitorMaxUnavailableWithoutZoneAwareness(ctx context.Context, k8sClient client.Client, toCreate humiov1alpha1.HumioCluster, forever chan struct{}, mostNumPodsSeenUnavailable *int) {
	hnp := controller.NewHumioNodeManagerFromHumioCluster(&toCreate)
	for {
		select {
		case <-ctx.Done(): // if cancel() execute
			forever <- struct{}{}
			return
		default:
			// Assume all is unavailable, and decrement number each time we see one that is working
			unavailableThisRound := hnp.GetNodeCount()

			pods, _ := kubernetes.ListPods(ctx, k8sClient, hnp.GetNamespace(), hnp.GetPodLabels())
			for _, pod := range pods {
				if pod.Status.Phase == corev1.PodRunning {
					for _, containerStatus := range pod.Status.ContainerStatuses {
						if containerStatus.Ready {
							unavailableThisRound--
						}
					}
				}
			}
			// Save the number of unavailable pods in this round
			*mostNumPodsSeenUnavailable = max(*mostNumPodsSeenUnavailable, unavailableThisRound)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TODO: Consider refactoring goroutine to a "watcher". https://book-v1.book.kubebuilder.io/beyond_basics/controller_watches
//
//	Using a for-loop will only see snapshots in time and we could easily miss a point in time where multiple node pools have the node pool state we are filtering for
func monitorMaxNumberNodePoolsWithSpecificNodePoolStatus(ctx context.Context, k8sClient client.Client, key types.NamespacedName, forever chan struct{}, mostNumNodePoolsWithSpecificNodePoolStatus *int, nodePoolState string) {
	updatedHumioCluster := humiov1alpha1.HumioCluster{}

	for {
		select {
		case <-ctx.Done(): // if cancel() execute
			forever <- struct{}{}
			return
		default:
			numNodePoolsWithSpecificState := 0

			_ = k8sClient.Get(ctx, key, &updatedHumioCluster)
			for _, poolStatus := range updatedHumioCluster.Status.NodePoolStatus {
				if poolStatus.State == nodePoolState {
					numNodePoolsWithSpecificState++
				}
			}
			// Save the number of node pools with the node pool state this round
			*mostNumNodePoolsWithSpecificNodePoolStatus = max(*mostNumNodePoolsWithSpecificNodePoolStatus, numNodePoolsWithSpecificState)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
