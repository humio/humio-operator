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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/kubernetes"
)

var _ = Describe("HumioCluster Workload Type Services", func() {
	ctx := context.Background()

	Context("Workload type services for digest and ingest pools", Label("envtest", "dummy", "real"), func() {
		It("Should create aggregate services selecting pods by workload type label", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-workload-svc",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.WorkloadServices = []humiov1alpha1.WorkloadServiceSpec{
				{
					Name:         "digest",
					WorkloadType: "digest",
				},
				{
					Name:         "ingest",
					WorkloadType: "ingest",
				},
			}

			nodeSpec := suite.ConstructBasicNodeSpecForHumioCluster(key)
			nodeSpec.WorkloadTypes = &[]string{"digest", "ingest"}
			toCreate.Spec.NodePools = append(toCreate.Spec.NodePools, humiov1alpha1.HumioNodePoolSpec{
				Name:          "extra-pool",
				HumioNodeSpec: nodeSpec,
			})

			suite.UsingClusterBy(key.Name, "Creating HumioCluster with workload services")
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Verifying aggregate digest service exists")
			digestSvcKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-digest", key.Name),
				Namespace: key.Namespace,
			}
			digestSvc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, digestSvcKey, digestSvc)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(digestSvc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(digestSvc.Spec.Selector[kubernetes.WorkloadTypeLabelPrefix+"digest"]).To(Equal("true"))
			Expect(digestSvc.Spec.Selector["app.kubernetes.io/instance"]).To(Equal(key.Name))
			Expect(digestSvc.Spec.Ports).To(HaveLen(2))
			Expect(digestSvc.Spec.Ports[0].Port).To(Equal(int32(8080)))
			Expect(digestSvc.Spec.Ports[1].Port).To(Equal(int32(9200)))

			suite.UsingClusterBy(key.Name, "Verifying aggregate ingest service exists")
			ingestSvcKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-ingest", key.Name),
				Namespace: key.Namespace,
			}
			ingestSvc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, ingestSvcKey, ingestSvc)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(ingestSvc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(ingestSvc.Spec.Selector[kubernetes.WorkloadTypeLabelPrefix+"ingest"]).To(Equal("true"))
			Expect(ingestSvc.Spec.Selector["app.kubernetes.io/instance"]).To(Equal(key.Name))

			suite.UsingClusterBy(key.Name, "Verifying pods have workload type labels")
			Eventually(func() int {
				podList := &corev1.PodList{}
				matchLabels := map[string]string{
					"app.kubernetes.io/instance":                  key.Name,
					kubernetes.WorkloadTypeLabelPrefix + "digest": "true",
				}
				_ = k8sClient.List(ctx, podList, client.InNamespace(key.Namespace), client.MatchingLabels(matchLabels))
				return len(podList.Items)
			}, testTimeout, suite.TestInterval).Should(BeNumerically(">=", 1))

			suite.UsingClusterBy(key.Name, "Verifying selectors are distinct between services")
			Expect(digestSvc.Spec.Selector).NotTo(Equal(ingestSvc.Spec.Selector))
		})
	})

	Context("Per-pool service suppression", Label("envtest", "dummy", "real"), func() {
		It("Should not create per-pool service when enableNodePoolService is false", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-no-pool-svc",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.WorkloadServices = []humiov1alpha1.WorkloadServiceSpec{
				{
					Name:         "digest",
					WorkloadType: "digest",
				},
			}

			nodeSpec := suite.ConstructBasicNodeSpecForHumioCluster(key)
			nodeSpec.WorkloadTypes = &[]string{"digest"}
			nodeSpec.EnableNodePoolService = ptr.To(false)
			toCreate.Spec.NodePools = append(toCreate.Spec.NodePools, humiov1alpha1.HumioNodePoolSpec{
				Name:          "digest-pool",
				HumioNodeSpec: nodeSpec,
			})

			suite.UsingClusterBy(key.Name, "Creating HumioCluster with per-pool service disabled")
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Verifying aggregate digest service exists")
			digestSvcKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-digest", key.Name),
				Namespace: key.Namespace,
			}
			digestSvc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, digestSvcKey, digestSvc)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(digestSvc.Spec.Selector[kubernetes.WorkloadTypeLabelPrefix+"digest"]).To(Equal("true"))

			suite.UsingClusterBy(key.Name, "Verifying per-pool service does NOT exist")
			hnp := controller.NewHumioNodeManagerFromHumioNodePool(toCreate, &toCreate.Spec.NodePools[0])
			poolSvcKey := types.NamespacedName{
				Name:      hnp.GetNodePoolName(),
				Namespace: key.Namespace,
			}
			poolSvc := &corev1.Service{}
			Consistently(func() bool {
				err := k8sClient.Get(ctx, poolSvcKey, poolSvc)
				return err != nil
			}, "5s", suite.TestInterval).Should(BeTrue())
		})
	})
})
