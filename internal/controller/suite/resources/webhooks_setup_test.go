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

package resources

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/helpers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Webhook Setup", Ordered, Label("envtest", "dummy", "real"), func() {

	Context("Webhook setup check", func() {
		It("Certificate/key should be on disk", func() {
			// on envtest we expect the certificate to exist on disk
			if helpers.UseEnvtest() {
				By("Verifying certificate files exist")
				certPath := filepath.Join(webhookCertPath, webhookCertName)
				keyPath := filepath.Join(webhookCertPath, webhookCertKey)

				Expect(certPath).To(BeAnExistingFile())
				Expect(keyPath).To(BeAnExistingFile())

				By("Verifying files are not empty")
				certInfo, err := os.Stat(certPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(certInfo.Size()).To(BeNumerically(">", 0))

				keyInfo, err := os.Stat(keyPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(keyInfo.Size()).To(BeNumerically(">", 0))
			}
		})
		It("Webhook validation svc should be created", func() {
			var expectedServiceName string
			if !helpers.UseEnvtest() {
				expectedServiceName = helpers.GetOperatorWebhookServiceName()
				serviceKey := client.ObjectKey{Name: expectedServiceName, Namespace: webhookNamespace}
				k8sWebhookService := &corev1.Service{}

				// Wait for the service to be created successfully
				Eventually(func() error {
					return k8sClient.Get(ctx, serviceKey, k8sWebhookService)
				}, testTimeout, suite.TestInterval).Should(Succeed())

				// Now safely assert on the service properties
				Expect(k8sWebhookService.Name).Should(Equal(expectedServiceName))
				Expect(k8sWebhookService.Spec.Ports).Should(HaveLen(1))
				Expect(k8sWebhookService.Spec.Ports[0].Name).Should(Equal("webhook"))
				Expect(k8sWebhookService.Spec.Ports[0].Port).Should(Equal(int32(443)))
				Expect(k8sWebhookService.Spec.Ports[0].TargetPort.IntVal).Should(Equal(int32(9443)))
			}
		})
		It("Webhook ValidatingWebhookConfiguration should be created", func() {
			expectedName := controller.ValidatingWebhookConfigurationName
			VWCKey := client.ObjectKey{Name: expectedName}
			k8sVWC := &admissionregistrationv1.ValidatingWebhookConfiguration{}

			// Wait for the ValidatingWebhookConfiguration to be created successfully
			Eventually(func() error {
				return k8sClient.Get(ctx, VWCKey, k8sVWC)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Now safely assert on the ValidatingWebhookConfiguration properties
			Expect(k8sVWC.Name).Should(Equal(expectedName))
			gvks := controller.GVKs
			Expect(k8sVWC.Webhooks).Should(HaveLen(len(gvks)))
		})
		It("Some Humio CRDs should be updated to contain a conversion webhook", func() {
			CRDs := controller.CRDsRequiringConversion
			webhookCertGenerator = helpers.NewCertGenerator(webhookCertPath, webhookCertName, webhookCertKey,
				webhookServiceHost, clusterKey.Namespace,
			)

			for _, crd := range CRDs {
				CRDKey := client.ObjectKey{Name: crd}
				k8sCRD := &apiextensionsv1.CustomResourceDefinition{}
				Expect(k8sClient.Get(ctx, CRDKey, k8sCRD)).Should(Succeed())
				Expect(k8sCRD.Spec.Conversion.Strategy).Should(Equal(apiextensionsv1.WebhookConverter))

				if helpers.UseEnvtest() {
					Eventually(func() error {
						if err := k8sClient.Get(ctx, CRDKey, k8sCRD); err != nil {
							return err
						}
						if k8sCRD.Spec.Conversion.Webhook.ClientConfig.URL == nil {
							return fmt.Errorf("URL is nil")
						}
						expectedURL := "https://127.0.0.1:9443/convert"
						if *k8sCRD.Spec.Conversion.Webhook.ClientConfig.URL != expectedURL {
							return fmt.Errorf("URL mismatch: got %s, want %s",
								*k8sCRD.Spec.Conversion.Webhook.ClientConfig.URL, expectedURL)
						}
						return nil
					}, testTimeout, suite.TestInterval).Should(Succeed())
				} else {
					Expect(k8sCRD.Spec.Conversion.Webhook.ClientConfig.Service.Name).Should(Equal(helpers.GetOperatorWebhookServiceName()))
				}
			}
		})
	})
})
