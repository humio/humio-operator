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

package pfdrenderservice

import (
	"context"
	"fmt"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/controller/versions"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	testInterval  = suite.TestInterval
	shortTimeout  = time.Second * 10
	mediumTimeout = time.Second * 30
	longTimeout   = time.Second * 60
)

var _ = Describe("HumioPDFRenderService Controller", func() {
	BeforeEach(func() {
		key := types.NamespacedName{Namespace: testProcessNamespace}
		CleanupPdfRenderServiceResources(context.Background(), k8sClient, key)
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
	})

	FContext("PDF Render Service with HumioCluster Integration", Label("envtest", "dummy", "real"), func() {
		It("should run independently and integrate with HumioCluster via environment variables", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-cluster-integration",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioPdfRenderService first (demonstrates independent deployment)")
			pdfService := suite.CreatePdfRenderServiceCR(ctx, k8sClient, key, false)

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Verifying PDF service reaches Running state independently")
			// PDF service should reach Running state without requiring HumioCluster
			// This demonstrates that PDF service deployment is independent of HumioCluster
			fetchedPDFService := &humiov1alpha1.HumioPdfRenderService{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedPDFService); err != nil {
					return ""
				}
				return fetchedPDFService.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioPdfRenderServiceStateRunning))

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true for API integration")
			// ENABLE_SCHEDULED_REPORT signals that the HumioCluster can use PDF features
			// but doesn't control PDF service deployment - that's already running independently
			clusterKey := types.NamespacedName{
				Name:      "hc-with-scheduled-reports",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
				{Name: "DEFAULT_PDF_RENDER_SERVICE_URL", Value: fmt.Sprintf("http://%s:%d",
					helpers.PdfRenderServiceChildName(key.Name), controller.DefaultPdfRenderServicePort)},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Verifying PDF service remains Running (demonstrates architecture)")
			// PDF service should remain Running, proving it's not dependent on HumioCluster for deployment
			suite.WaitForObservedGeneration(ctx, k8sClient, fetchedPDFService, testTimeout, testInterval)
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedPDFService); err != nil {
					return ""
				}
				return fetchedPDFService.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioPdfRenderServiceStateRunning))

			By("Verifying Deployment and Service exist with owner references")
			var deployment appsv1.Deployment
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, &deployment)
			}, testTimeout, testInterval).Should(Succeed())

			Expect(deployment.OwnerReferences).To(HaveLen(1))
			Expect(deployment.OwnerReferences[0].Name).To(Equal(key.Name))
			Expect(deployment.OwnerReferences[0].Kind).To(Equal("HumioPdfRenderService"))

			var service corev1.Service
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, &service)
			}, testTimeout, testInterval).Should(Succeed())

			Expect(service.OwnerReferences).To(HaveLen(1))
			Expect(service.OwnerReferences[0].Name).To(Equal(key.Name))
			Expect(service.OwnerReferences[0].Kind).To(Equal("HumioPdfRenderService"))
		})
	})

	FContext("PDF Render Service Independent Deployment", Label("envtest", "dummy", "real"), func() {
		It("should deploy PDF Render Service independently via helm chart (not triggered by HumioCluster)", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-independent-deployment",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioPdfRenderService independently (via helm chart deployment)")
			pdfService := suite.CreatePdfRenderServiceCR(ctx, k8sClient, key, false)

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Verifying PDF service deploys independently without requiring HumioCluster")
			// The PDF service should be able to start and reach Running state
			// without any HumioCluster being present, demonstrating independent deployment
			fetchedPDFService := &humiov1alpha1.HumioPdfRenderService{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedPDFService); err != nil {
					return ""
				}
				return fetchedPDFService.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioPdfRenderServiceStateRunning))

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true for API integration")
			// ENABLE_SCHEDULED_REPORT signals that HumioCluster supports PDF features,
			// but it doesn't trigger PDF service deployment - that's done via helm chart
			clusterKey := types.NamespacedName{
				Name:      "hc-with-reports-enabled",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
				{Name: "DEFAULT_PDF_RENDER_SERVICE_URL", Value: fmt.Sprintf("http://%s:%d",
					helpers.PdfRenderServiceChildName(key.Name), controller.DefaultPdfRenderServicePort)},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Verifying PDF render service is already Running (independent deployment)")
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedPDFService); err != nil {
					return ""
				}
				return fetchedPDFService.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioPdfRenderServiceStateRunning))

			By("Verifying Deployment exists with correct properties")
			var deployment appsv1.Deployment
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, &deployment)
			}, testTimeout, testInterval).Should(Succeed())

			Expect(*deployment.Spec.Replicas).To(Equal(int32(1)))
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(versions.DefaultPDFRenderServiceImage()))

			By("Verifying Service exists with correct port")
			var service corev1.Service
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, &service)
			}, testTimeout, testInterval).Should(Succeed())

			Expect(service.Spec.Ports[0].Port).To(Equal(int32(controller.DefaultPdfRenderServicePort)))
		})
	})

	FContext("PDF Render Service Update", Label("envtest", "dummy", "real"), func() {
		It("should update the Deployment when the HumioPdfRenderService is updated", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-update",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-update-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService")
			pdfService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    versions.DefaultPDFRenderServiceImage(),
					Replicas: 1,
					Port:     5123,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					TLS: &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
						Enabled: helpers.BoolPtr(false),
					},
				},
			}
			Expect(k8sClient.Create(ctx, pdfService)).Should(Succeed())

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Waiting for deployment to be ready")
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}

			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			if !helpers.UseEnvtest() {
				// Skip pod simulation in envtest
			} else {
				Expect(suite.EnsurePodsForDeploymentInEnvtest(ctx, k8sClient, deploymentKey, "1")).To(Succeed())
			}

			By("Updating HumioPdfRenderService spec")
			var updatedPdfService humiov1alpha1.HumioPdfRenderService
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, &updatedPdfService); err != nil {
					return err
				}
				updatedPdfService.Spec.Image = "humio/pdf-render-service:0.1.1--build-103--sha-76833d8fdc641dad51798fb2a4705e2d273393b7"
				updatedPdfService.Spec.Replicas = 2
				updatedPdfService.Spec.Port = 5124

				// Disable autoscaling to test manual replica scaling
				updatedPdfService.Spec.Autoscaling = &humiov1alpha1.HumioPdfRenderServiceAutoscalingSpec{
					Enabled: helpers.BoolPtr(false),
				}
				return k8sClient.Update(ctx, &updatedPdfService)
			}, testTimeout, testInterval).Should(Succeed())

			suite.WaitForObservedGeneration(ctx, k8sClient, &updatedPdfService, testTimeout, testInterval)

			By("Verifying deployment is updated")
			// Check image is updated
			Eventually(func() string {
				var deployment appsv1.Deployment
				if err := k8sClient.Get(ctx, deploymentKey, &deployment); err != nil {
					return ""
				}
				return deployment.Spec.Template.Spec.Containers[0].Image
			}, longTimeout, testInterval).Should(Equal("humio/pdf-render-service:0.1.1--build-103--sha-76833d8fdc641dad51798fb2a4705e2d273393b7"))

			// Check replicas are updated
			Eventually(func() int32 {
				var deployment appsv1.Deployment
				if err := k8sClient.Get(ctx, deploymentKey, &deployment); err != nil {
					return 0
				}
				return *deployment.Spec.Replicas
			}, longTimeout, testInterval).Should(Equal(int32(2)))

			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			// Wait for deployment to stabilize with new replica count
			Eventually(func() int32 {
				var dep appsv1.Deployment
				if err := k8sClient.Get(ctx, deploymentKey, &dep); err != nil {
					return 0
				}
				return dep.Status.ReadyReplicas
			}, longTimeout, testInterval).Should(Equal(int32(2)))

			if !helpers.UseEnvtest() {
				// Skip pod simulation in envtest
			} else {
				Expect(suite.EnsurePodsForDeploymentInEnvtest(ctx, k8sClient, deploymentKey, "2")).To(Succeed())
			}

			By("Verifying service port is updated")
			var service corev1.Service
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, deploymentKey, &service); err != nil {
					return 0
				}
				return service.Spec.Ports[0].Port
			}, testTimeout, testInterval).Should(Equal(int32(5124)))
		})
	})

	Context("PDF Render Service Upgrade", Label("dummy", "real"), func() {
		const (
			initialTestPdfImage  = "humio/pdf-render-service:0.1.1--build-103--sha-76833d8fdc641dad51798fb2a4705e2d273393b7"
			upgradedTestPdfImage = "humio/pdf-render-service:0.1.2--build-104--sha-76833d8fdc641dad51798fb2a4705e2d273393b8"
		)

		It("Should update the PDF render service deployment when its image is changed", func() {
			ctx := context.Background()

			pdfKey := types.NamespacedName{
				Name:      "pdf-svc-for-upgrade-" + kubernetes.RandomString(),
				Namespace: testProcessNamespace,
			}

			By("Creating HumioPdfRenderService with initial image: " + initialTestPdfImage)
			pdfCR := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pdfKey.Name,
					Namespace: pdfKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    initialTestPdfImage,
					Replicas: 1,
					TLS: &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
						Enabled: helpers.BoolPtr(false),
					},
				},
			}
			Expect(k8sClient.Create(ctx, pdfCR)).To(Succeed())
			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfCR)

			By("Waiting for PDF service to reach Running state")
			Eventually(func() string {
				if err := k8sClient.Get(ctx, pdfKey, pdfCR); err != nil {
					return ""
				}
				return pdfCR.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioPdfRenderServiceStateRunning))

			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(pdfKey.Name),
				Namespace: pdfKey.Namespace,
			}

			By("Verifying PDF service deployment uses initial image: " + initialTestPdfImage)
			Eventually(func(g Gomega) string {
				deployment := &appsv1.Deployment{}
				g.Expect(k8sClient.Get(ctx, deploymentKey, deployment)).To(Succeed())
				g.Expect(deployment.Spec.Template.Spec.Containers).NotTo(BeEmpty())
				return deployment.Spec.Template.Spec.Containers[0].Image
			}, testTimeout, testInterval).Should(Equal(initialTestPdfImage))

			By("Updating HumioPdfRenderService image to: " + upgradedTestPdfImage)
			Eventually(func() error {
				var pdf humiov1alpha1.HumioPdfRenderService
				if err := k8sClient.Get(ctx, pdfKey, &pdf); err != nil {
					return err
				}
				pdf.Spec.Image = upgradedTestPdfImage
				return k8sClient.Update(ctx, &pdf)
			}, testTimeout, testInterval).Should(Succeed())

			By("Waiting for PDF service deployment to reflect new image: " + upgradedTestPdfImage)
			Eventually(func(g Gomega) string {
				deployment := &appsv1.Deployment{}
				g.Expect(k8sClient.Get(ctx, deploymentKey, deployment)).To(Succeed())
				g.Expect(deployment.Spec.Template.Spec.Containers).NotTo(BeEmpty())
				return deployment.Spec.Template.Spec.Containers[0].Image
			}, testTimeout, testInterval).Should(Equal(upgradedTestPdfImage))

			By("Verifying PDF service remains Running after upgrade")
			Eventually(func() string {
				if err := k8sClient.Get(ctx, pdfKey, pdfCR); err != nil {
					return ""
				}
				return pdfCR.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioPdfRenderServiceStateRunning))
		})
	})

	FContext("PDF Render Service Resources and Probes", Label("envtest", "dummy", "real"), func() {
		It("should configure resources and probes correctly", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-resources",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-resources-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService with resources and probes")
			pdfService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    versions.DefaultPDFRenderServiceImage(),
					Replicas: 1,
					Port:     controller.DefaultPdfRenderServicePort,
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
					},
					TLS: &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
						Enabled: helpers.BoolPtr(false),
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/health",
								Port: intstr.FromInt(5123),
							},
						},
						InitialDelaySeconds: 30,
						PeriodSeconds:       10,
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/ready",
								Port: intstr.FromInt(5123),
							},
						},
						InitialDelaySeconds: 10,
						PeriodSeconds:       5,
					},
				},
			}

			Expect(k8sClient.Create(ctx, pdfService)).Should(Succeed())

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Verifying deployment has correct resources and probes")
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}

			var deployment appsv1.Deployment
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, &deployment)
			}, testTimeout, testInterval).Should(Succeed())

			container := deployment.Spec.Template.Spec.Containers[0]

			// Verify resources
			cpuLimit := container.Resources.Limits[corev1.ResourceCPU]
			Expect(cpuLimit.String()).To(Equal("500m"))
			memLimit := container.Resources.Limits[corev1.ResourceMemory]
			Expect(memLimit.String()).To(Equal("512Mi"))
			cpuReq := container.Resources.Requests[corev1.ResourceCPU]
			Expect(cpuReq.String()).To(Equal("250m"))
			memReq := container.Resources.Requests[corev1.ResourceMemory]
			Expect(memReq.String()).To(Equal("256Mi"))

			// Verify probes
			Expect(container.LivenessProbe.HTTPGet.Path).To(Equal("/health"))
			Expect(container.LivenessProbe.InitialDelaySeconds).To(Equal(int32(30)))
			Expect(container.ReadinessProbe.HTTPGet.Path).To(Equal("/ready"))
			Expect(container.ReadinessProbe.InitialDelaySeconds).To(Equal(int32(10)))
		})
	})

	FContext("PDF Render Service Environment Variables", Label("envtest", "dummy", "real"), func() {
		It("should configure environment variables correctly", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-env-vars",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-env-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService with environment variables")
			pdfService := suite.CreatePdfRenderServiceCR(ctx, k8sClient, key, false)

			// Update the existing CR with environment variables
			Eventually(func() error {
				var fetchedPDF humiov1alpha1.HumioPdfRenderService
				if err := k8sClient.Get(ctx, key, &fetchedPDF); err != nil {
					return err
				}
				fetchedPDF.Spec.EnvironmentVariables = []corev1.EnvVar{
					{Name: "CUSTOM_VAR", Value: "custom-value"},
					{Name: "LOG_LEVEL", Value: "debug"},
				}
				return k8sClient.Update(ctx, &fetchedPDF)
			}, testTimeout, testInterval).Should(Succeed())

			By("Verifying deployment has correct environment variables")
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}

			// Wait for the deployment to be updated with the environment variables
			Eventually(func() map[string]string {
				var deployment appsv1.Deployment
				if err := k8sClient.Get(ctx, deploymentKey, &deployment); err != nil {
					return map[string]string{}
				}
				envMap := make(map[string]string)
				for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
					envMap[env.Name] = env.Value
				}
				return envMap
			}, testTimeout, testInterval).Should(And(
				HaveKeyWithValue("CUSTOM_VAR", "custom-value"),
				HaveKeyWithValue("LOG_LEVEL", "debug"),
			))

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Updating environment variables")
			Eventually(func() error {
				var fetchedPDF humiov1alpha1.HumioPdfRenderService
				if err := k8sClient.Get(ctx, key, &fetchedPDF); err != nil {
					return err
				}
				fetchedPDF.Spec.EnvironmentVariables = []corev1.EnvVar{
					{Name: "CUSTOM_VAR", Value: "updated-value"},
					{Name: "NEW_VAR", Value: "new-value"},
				}
				return k8sClient.Update(ctx, &fetchedPDF)
			}, testTimeout, testInterval).Should(Succeed())

			suite.WaitForObservedGeneration(ctx, k8sClient, pdfService, testTimeout, testInterval)

			By("Verifying environment variables are updated")
			Eventually(func() map[string]string {
				var deployment appsv1.Deployment
				if err := k8sClient.Get(ctx, deploymentKey, &deployment); err != nil {
					return map[string]string{}
				}
				envMap := make(map[string]string)
				for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
					envMap[env.Name] = env.Value
				}
				return envMap
			}, testTimeout, testInterval).Should(And(
				HaveKeyWithValue("CUSTOM_VAR", "updated-value"),
				HaveKeyWithValue("NEW_VAR", "new-value"),
				Not(HaveKey("LOG_LEVEL")),
			))
		})
	})

	FContext("PDF Render Service with HumioCluster Environment Variable Integration", Label("envtest", "dummy", "real"), func() {
		It("Should demonstrate HumioCluster interaction with PDF service via DEFAULT_PDF_RENDER_SERVICE_URL", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-cluster-integration",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioPdfRenderService first")
			pdfService := suite.CreatePdfRenderServiceAndWait(ctx, k8sClient, key, "humio/humio-report:1.0.0", false)

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Creating HumioCluster with scheduled reports and PDF service URL")
			clusterKey := types.NamespacedName{
				Name:      "hc-with-pdf-url",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
				{Name: "DEFAULT_PDF_RENDER_SERVICE_URL", Value: fmt.Sprintf("http://%s:%d", helpers.PdfRenderServiceChildName(key.Name), 5123)},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Verifying PDF service is running")
			fetchedPDFService := &humiov1alpha1.HumioPdfRenderService{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedPDFService); err != nil {
					return ""
				}
				return fetchedPDFService.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioPdfRenderServiceStateRunning))

			By("Updating PDF service image multiple times")
			// First update
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedPDFService); err != nil {
					return err
				}
				fetchedPDFService.Spec.Image = "humio/humio-report:1.1.0"
				return k8sClient.Update(ctx, fetchedPDFService)
			}, testTimeout, testInterval).Should(Succeed())

			suite.WaitForObservedGeneration(ctx, k8sClient, fetchedPDFService, testTimeout, testInterval)

			// Second update
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedPDFService); err != nil {
					return err
				}
				fetchedPDFService.Spec.Image = "humio/humio-report:1.2.0"
				return k8sClient.Update(ctx, fetchedPDFService)
			}, testTimeout, testInterval).Should(Succeed())

			suite.WaitForObservedGeneration(ctx, k8sClient, fetchedPDFService, testTimeout, testInterval)

			By("Verifying final deployment image")
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}
			var deployment appsv1.Deployment
			Eventually(func() string {
				if err := k8sClient.Get(ctx, deploymentKey, &deployment); err != nil {
					return ""
				}
				return deployment.Spec.Template.Spec.Containers[0].Image
			}, testTimeout, testInterval).Should(Equal("humio/humio-report:1.2.0"))

			By("Disabling ENABLE_SCHEDULED_REPORT and verifying cleanup (future implementation)")
			// Note: Current implementation doesn't auto-cleanup when ENABLE_SCHEDULED_REPORT is disabled
			// This would be a future enhancement
		})
	})

	FContext("PDF Render Service HPA (Horizontal Pod Autoscaling)", Label("envtest", "dummy", "real"), func() {
		It("should create HPA when autoscaling is enabled", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-hpa-enabled",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-hpa-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService with HPA enabled")
			pdfService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    versions.DefaultPDFRenderServiceImage(),
					Replicas: 2,
					Port:     controller.DefaultPdfRenderServicePort,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					TLS: &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
						Enabled: helpers.BoolPtr(false),
					},
					Autoscaling: &humiov1alpha1.HumioPdfRenderServiceAutoscalingSpec{
						Enabled:     helpers.BoolPtr(true),
						MinReplicas: helpers.Int32Ptr(1),
						MaxReplicas: 5,
						Metrics: []autoscalingv2.MetricSpec{
							{
								Type: autoscalingv2.ResourceMetricSourceType,
								Resource: &autoscalingv2.ResourceMetricSource{
									Name: corev1.ResourceCPU,
									Target: autoscalingv2.MetricTarget{
										Type:               autoscalingv2.UtilizationMetricType,
										AverageUtilization: helpers.Int32Ptr(75),
									},
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, pdfService)).Should(Succeed())

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Verifying HPA is created")
			hpaKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceHpaName(key.Name),
				Namespace: key.Namespace,
			}
			var hpa autoscalingv2.HorizontalPodAutoscaler
			Eventually(func() error {
				return k8sClient.Get(ctx, hpaKey, &hpa)
			}, testTimeout, testInterval).Should(Succeed())

			Expect(*hpa.Spec.MinReplicas).To(Equal(int32(1)))
			Expect(hpa.Spec.MaxReplicas).To(Equal(int32(5)))
			Expect(hpa.Spec.Metrics[0].Resource.Name).To(Equal(corev1.ResourceCPU))
			Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(75)))
		})

		It("should not create HPA when autoscaling is disabled", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-hpa-disabled",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-no-hpa-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService with HPA disabled")
			pdfService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    versions.DefaultPDFRenderServiceImage(),
					Replicas: 3,
					Port:     controller.DefaultPdfRenderServicePort,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					TLS: &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
						Enabled: helpers.BoolPtr(false),
					},
					Autoscaling: &humiov1alpha1.HumioPdfRenderServiceAutoscalingSpec{
						Enabled: helpers.BoolPtr(false),
					},
				},
			}

			Expect(k8sClient.Create(ctx, pdfService)).Should(Succeed())

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Verifying HPA is not created")
			hpaKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceHpaName(key.Name),
				Namespace: key.Namespace,
			}
			var hpa autoscalingv2.HorizontalPodAutoscaler
			Consistently(func() bool {
				err := k8sClient.Get(ctx, hpaKey, &hpa)
				return k8serrors.IsNotFound(err)
			}, shortTimeout, testInterval).Should(BeTrue())

			By("Verifying deployment has manual replica count")
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}
			var deployment appsv1.Deployment
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, deploymentKey, &deployment); err != nil {
					return 0
				}
				return *deployment.Spec.Replicas
			}, testTimeout, testInterval).Should(Equal(int32(3)))
		})

		It("should support multiple metrics", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-hpa-multi-metrics",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-multi-metrics-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService with multiple HPA metrics")
			pdfService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    versions.DefaultPDFRenderServiceImage(),
					Replicas: 1,
					Port:     controller.DefaultPdfRenderServicePort,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					TLS: &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
						Enabled: helpers.BoolPtr(false),
					},
					Autoscaling: &humiov1alpha1.HumioPdfRenderServiceAutoscalingSpec{
						Enabled:     helpers.BoolPtr(true),
						MinReplicas: helpers.Int32Ptr(2),
						MaxReplicas: 10,
						Metrics: []autoscalingv2.MetricSpec{
							{
								Type: autoscalingv2.ResourceMetricSourceType,
								Resource: &autoscalingv2.ResourceMetricSource{
									Name: corev1.ResourceCPU,
									Target: autoscalingv2.MetricTarget{
										Type:               autoscalingv2.UtilizationMetricType,
										AverageUtilization: helpers.Int32Ptr(60),
									},
								},
							},
							{
								Type: autoscalingv2.ResourceMetricSourceType,
								Resource: &autoscalingv2.ResourceMetricSource{
									Name: corev1.ResourceMemory,
									Target: autoscalingv2.MetricTarget{
										Type:               autoscalingv2.UtilizationMetricType,
										AverageUtilization: helpers.Int32Ptr(80),
									},
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, pdfService)).Should(Succeed())

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Verifying HPA has both metrics")
			hpaKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceHpaName(key.Name),
				Namespace: key.Namespace,
			}
			var hpa autoscalingv2.HorizontalPodAutoscaler
			Eventually(func() int {
				if err := k8sClient.Get(ctx, hpaKey, &hpa); err != nil {
					return 0
				}
				return len(hpa.Spec.Metrics)
			}, testTimeout, testInterval).Should(Equal(2))

			Expect(hpa.Spec.Metrics[0].Resource.Name).To(Equal(corev1.ResourceCPU))
			Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(60)))
			Expect(hpa.Spec.Metrics[1].Resource.Name).To(Equal(corev1.ResourceMemory))
			Expect(*hpa.Spec.Metrics[1].Resource.Target.AverageUtilization).To(Equal(int32(80)))
		})

		It("should handle toggling HPA on and off", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-hpa-toggle",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-toggle-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService with HPA enabled")
			pdfService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    versions.DefaultPDFRenderServiceImage(),
					Replicas: 2,
					Port:     controller.DefaultPdfRenderServicePort,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					TLS: &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
						Enabled: helpers.BoolPtr(false),
					},
					Autoscaling: &humiov1alpha1.HumioPdfRenderServiceAutoscalingSpec{
						Enabled:     helpers.BoolPtr(true),
						MinReplicas: helpers.Int32Ptr(1),
						MaxReplicas: 5,
					},
				},
			}

			Expect(k8sClient.Create(ctx, pdfService)).Should(Succeed())

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Verifying HPA is created")
			hpaKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceHpaName(key.Name),
				Namespace: key.Namespace,
			}
			var hpa autoscalingv2.HorizontalPodAutoscaler
			Eventually(func() error {
				return k8sClient.Get(ctx, hpaKey, &hpa)
			}, testTimeout, testInterval).Should(Succeed())

			By("Disabling HPA")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, pdfService); err != nil {
					return err
				}
				pdfService.Spec.Autoscaling.Enabled = helpers.BoolPtr(false)
				pdfService.Spec.Replicas = 4
				return k8sClient.Update(ctx, pdfService)
			}, testTimeout, testInterval).Should(Succeed())

			suite.WaitForObservedGeneration(ctx, k8sClient, pdfService, testTimeout, testInterval)

			By("Verifying HPA is deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hpaKey, &hpa)
				return k8serrors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			By("Verifying deployment has manual replica count")
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}
			var deployment appsv1.Deployment
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, deploymentKey, &deployment); err != nil {
					return 0
				}
				return *deployment.Spec.Replicas
			}, testTimeout, testInterval).Should(Equal(int32(4)))
		})

		It("should use default metrics when none specified", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-hpa-default-metrics",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-default-metrics-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService with HPA but no metrics")
			pdfService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    versions.DefaultPDFRenderServiceImage(),
					Replicas: 1,
					Port:     controller.DefaultPdfRenderServicePort,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					TLS: &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
						Enabled: helpers.BoolPtr(false),
					},
					Autoscaling: &humiov1alpha1.HumioPdfRenderServiceAutoscalingSpec{
						Enabled:     helpers.BoolPtr(true),
						MinReplicas: helpers.Int32Ptr(1),
						MaxReplicas: 3,
						// No metrics specified - should use default
					},
				},
			}

			Expect(k8sClient.Create(ctx, pdfService)).Should(Succeed())

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Verifying HPA uses default CPU metric")
			hpaKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceHpaName(key.Name),
				Namespace: key.Namespace,
			}
			var hpa autoscalingv2.HorizontalPodAutoscaler
			Eventually(func() error {
				return k8sClient.Get(ctx, hpaKey, &hpa)
			}, testTimeout, testInterval).Should(Succeed())

			Expect(hpa.Spec.Metrics).To(HaveLen(1))
			Expect(hpa.Spec.Metrics[0].Resource.Name).To(Equal(corev1.ResourceCPU))
			Expect(*hpa.Spec.Metrics[0].Resource.Target.AverageUtilization).To(Equal(int32(80))) // Default value
		})
	})

	FContext("PDF Render Service Reconcile Loop", Label("envtest", "dummy", "real"), func() {
		It("should not trigger unnecessary updates for ImagePullPolicy", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-service-reconcile-test",
				Namespace: testProcessNamespace,
			}

			By("Creating HumioCluster with ENABLE_SCHEDULED_REPORT=true")
			clusterKey := types.NamespacedName{
				Name:      "hc-for-reconcile-test",
				Namespace: testProcessNamespace,
			}
			cluster := suite.ConstructBasicSingleNodeHumioCluster(clusterKey, false)
			cluster.Spec.EnvironmentVariables = []corev1.EnvVar{
				{Name: "ENABLE_SCHEDULED_REPORT", Value: "true"},
			}

			Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
			defer suite.CleanupCluster(ctx, k8sClient, cluster)

			By("Creating HumioPdfRenderService without ImagePullPolicy")
			pdfService := suite.CreatePdfRenderServiceCR(ctx, k8sClient, key, false)
			// Not setting ImagePullPolicy - should default appropriately

			defer CleanupPdfRenderServiceCR(ctx, k8sClient, pdfService)

			By("Waiting for initial deployment")
			deploymentKey := types.NamespacedName{
				Name:      helpers.PdfRenderServiceChildName(key.Name),
				Namespace: key.Namespace,
			}
			var deployment appsv1.Deployment
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, &deployment)
			}, testTimeout, testInterval).Should(Succeed())

			initialGeneration := deployment.Generation

			By("Waiting to ensure no spurious updates")
			Consistently(func() int64 {
				if err := k8sClient.Get(ctx, deploymentKey, &deployment); err != nil {
					return 0
				}
				return deployment.Generation
			}, shortTimeout, testInterval).Should(Equal(initialGeneration))
		})
	})
})
