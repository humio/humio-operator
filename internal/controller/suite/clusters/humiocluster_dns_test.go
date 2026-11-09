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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/kubernetes"
)

var _ = Describe("HumioCluster DNS Configuration", func() {
	ctx := context.Background()

	Context("DNS fields propagate to pods", Label("envtest", "dummy", "real"), func() {
		It("Should set dnsPolicy and dnsConfig on pods when configured in the HumioCluster spec", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-dns-fields",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.DNSPolicy = corev1.DNSNone
			toCreate.Spec.DNSConfig = &corev1.PodDNSConfig{
				Nameservers: []string{"8.8.8.8", "8.8.4.4"},
				Searches:    []string{"example.com"},
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster with DNS fields (not waiting for Running state)")
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStatePending, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Simulating bootstrap token so reconciler can create pods")
			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, toCreate)

			suite.UsingClusterBy(key.Name, "Waiting for pods to be created")
			var clusterPods []corev1.Pod
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				return len(clusterPods)
			}, testTimeout, suite.TestInterval).Should(BeNumerically(">=", 1))

			suite.UsingClusterBy(key.Name, "Verifying pods have correct dnsPolicy and dnsConfig")
			pod := clusterPods[0]
			Expect(pod.Spec.DNSPolicy).To(Equal(corev1.DNSNone))
			Expect(pod.Spec.DNSConfig).ToNot(BeNil())
			Expect(pod.Spec.DNSConfig.Nameservers).To(Equal([]string{"8.8.8.8", "8.8.4.4"}))
			Expect(pod.Spec.DNSConfig.Searches).To(Equal([]string{"example.com"}))
		})

		It("Should not set DNS fields when not configured in the HumioCluster spec", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-dns-default",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

			suite.UsingClusterBy(key.Name, "Creating HumioCluster without DNS fields")
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Verifying pods have Kubernetes default dnsPolicy")
			clusterPods, err := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			Expect(err).ToNot(HaveOccurred())
			Expect(clusterPods).ToNot(BeEmpty())

			pod := clusterPods[0]
			Expect(pod.Spec.DNSPolicy).To(Equal(corev1.DNSClusterFirst))
			Expect(pod.Spec.DNSConfig).To(BeNil())
		})
	})
})
