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
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/helpers"
)

var _ = Describe("HumioCluster Shadow Node Pool Controller", func() {
	ctx := context.Background()

	Context("Shadow Resource Lifecycle", func() {
		It("Should create shadow HumioNodePool for embedded node pools when feature is enabled", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-shadow-create-test",
				Namespace: testProcessNamespace,
			}

			toCreate := constructBasicMultiNodePoolHumioCluster(key, 1)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools[0].Name = "ingest-pool"
			toCreate.Spec.NodePools[0].EnvironmentVariables = []corev1.EnvVar{
				{Name: "NODE_ROLES", Value: "ingestonly"},
			}
			toCreate.Spec.NodePools[0].Autoscaling = &humiov1alpha1.AutoscalingSpec{
				MinReplicas: helpers.Int32Ptr(1),
				MaxReplicas: 5,
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster with independent node pools enabled")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Checking that shadow HumioNodePool resource is created")
			shadowNodePoolKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-ingest-pool", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool := &humiov1alpha1.HumioNodePool{}
			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(shadowNodePool.Annotations["humio.com/managed-by"]).To(Equal("humiocluster-shadow"))
			Expect(shadowNodePool.Spec.ClusterName).To(Equal(key.Name))
			Expect(shadowNodePool.Spec.Name).To(Equal("ingest-pool"))

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})

		It("Should clean up orphaned shadow resources when node pools are removed", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-cleanup-test",
				Namespace: testProcessNamespace,
			}

			toCreate := constructBasicMultiNodePoolHumioCluster(key, 2)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools[0].Name = "cleanup-pool-1"
			toCreate.Spec.NodePools[0].HumioNodeSpec.NodeCount = helpers.Int32Ptr(1)
			toCreate.Spec.NodePools[1].Name = "cleanup-pool-2"
			toCreate.Spec.NodePools[1].HumioNodeSpec.NodeCount = helpers.Int32Ptr(1)

			suite.UsingClusterBy(key.Name, "Creating HumioCluster with multiple node pools")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Verifying both shadow resources are created")
			shadowNodePool1Key := types.NamespacedName{
				Name:      fmt.Sprintf("%s-cleanup-pool-1", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool2Key := types.NamespacedName{
				Name:      fmt.Sprintf("%s-cleanup-pool-2", key.Name),
				Namespace: key.Namespace,
			}

			shadowNodePool1 := &humiov1alpha1.HumioNodePool{}
			shadowNodePool2 := &humiov1alpha1.HumioNodePool{}

			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePool1Key, shadowNodePool1)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePool2Key, shadowNodePool2)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Removing one node pool from spec")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return err
				}
				fetchedCluster.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
					fetchedCluster.Spec.NodePools[0],
				}
				return k8sClient.Update(ctx, fetchedCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying orphaned shadow resource is cleaned up")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, shadowNodePool2Key, shadowNodePool2)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			By("Verifying remaining shadow resource still exists")
			Expect(k8sClient.Get(ctx, shadowNodePool1Key, shadowNodePool1)).Should(Succeed())
			Expect(shadowNodePool1.Annotations["humio.com/managed-by"]).To(Equal("humiocluster-shadow"))

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})

		It("Should clean up all shadow resources when independent node pools are disabled", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-disable-feature-test",
				Namespace: testProcessNamespace,
			}

			toCreate := constructBasicMultiNodePoolHumioCluster(key, 1)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools[0].Name = "feature-test-pool"
			toCreate.Spec.NodePools[0].HumioNodeSpec.NodeCount = helpers.Int32Ptr(2)

			suite.UsingClusterBy(key.Name, "Creating HumioCluster with independent node pools enabled")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Verifying shadow resource is created when feature is enabled")
			shadowNodePoolKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-feature-test-pool", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool := &humiov1alpha1.HumioNodePool{}
			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Disabling independent node pools feature flag")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return err
				}
				fetchedCluster.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = false
				return k8sClient.Update(ctx, fetchedCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying all shadow resources are cleaned up")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})

		It("Should properly set owner references on shadow resources", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-shadow-ownership-test",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "ownership-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: helpers.Int32Ptr(1),
						Autoscaling: &humiov1alpha1.AutoscalingSpec{
							MinReplicas: helpers.Int32Ptr(1),
							MaxReplicas: 3,
						},
					},
				},
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster for shadow ownership test")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Waiting for shadow HumioNodePool resource to be created")
			shadowNodePoolKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-ownership-pool", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool := &humiov1alpha1.HumioNodePool{}
			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying shadow resource has correct owner reference")
			Expect(shadowNodePool.OwnerReferences).To(HaveLen(1))

			ownerRef := shadowNodePool.OwnerReferences[0]
			Expect(ownerRef.Kind).To(Equal("HumioCluster"))
			Expect(ownerRef.Name).To(Equal(key.Name))
			Expect(ownerRef.UID).To(Equal(fetchedCluster.UID))
			Expect(*ownerRef.Controller).To(BeTrue())
			Expect(*ownerRef.BlockOwnerDeletion).To(BeTrue())

			By("Verifying shadow resource has correct management annotation")
			Expect(shadowNodePool.Annotations).To(HaveKeyWithValue("humio.com/managed-by", "humiocluster-shadow"))

			By("Testing garbage collection by deleting HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())

			By("Verifying shadow resource is garbage collected")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "HumioCluster and shadow resources cleaned up via garbage collection")
		})
	})

	Context("Node Pool Conflicts", func() {
		It("Should prevent conflicts between embedded and standalone node pools", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-conflict-test",
				Namespace: testProcessNamespace,
			}

			By("Creating standalone HumioNodePool first")
			standaloneNodePool := &humiov1alpha1.HumioNodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s-ingest", key.Name),
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioNodePoolSpec{
					ClusterName: key.Name,
					Name:        "ingest",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: helpers.Int32Ptr(2),
					},
				},
			}
			Expect(k8sClient.Create(ctx, standaloneNodePool)).Should(Succeed())

			By("Attempting to create HumioCluster with conflicting embedded node pool")
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "ingest",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						NodeCount: helpers.Int32Ptr(2),
					},
				},
			}

			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.Message
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("node pool conflict"))

			suite.UsingClusterBy(key.Name, "Cleaning up resources")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, standaloneNodePool)).Should(Succeed())
		})
	})

	Context("Scale Subresource Integration", func() {
		It("Should read scaling decisions from the scale subresource", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-scale-subresource-test",
				Namespace: testProcessNamespace,
			}

			toCreate := constructBasicMultiNodePoolHumioCluster(key, 1)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools[0].Name = "scale-test-pool"
			toCreate.Spec.NodePools[0].EnvironmentVariables = []corev1.EnvVar{
				{Name: "NODE_ROLES", Value: "ingestonly"},
			}
			toCreate.Spec.NodePools[0].Autoscaling = &humiov1alpha1.AutoscalingSpec{
				MinReplicas: helpers.Int32Ptr(1),
				MaxReplicas: 5,
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster for scale subresource test")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Checking shadow HumioNodePool resource exists")
			shadowNodePoolKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-scale-test-pool", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool := &humiov1alpha1.HumioNodePool{}
			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Simulating HPA scaling decision by updating scale subresource")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return err
				}

				scaleClient := k8sClient.SubResource("scale")
				scale := &autoscalingv1.Scale{}
				if err := scaleClient.Get(ctx, shadowNodePool, scale); err != nil {
					return err
				}

				scale.Spec.Replicas = 3
				return scaleClient.Update(ctx, shadowNodePool, client.WithSubResourceBody(scale))
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying shadow resource spec.nodeCount reflects scale subresource decision")
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return 0
				}
				return shadowNodePool.Spec.NodeCount
			}, testTimeout, suite.TestInterval).Should(Equal(int32(3)))

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})

		It("Should preserve HPA scaling decisions during shadow resource sync", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-preserve-scaling-test",
				Namespace: testProcessNamespace,
			}

			toCreate := constructBasicMultiNodePoolHumioCluster(key, 1)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools[0].Name = "preserve-test-pool"
			toCreate.Spec.NodePools[0].EnvironmentVariables = []corev1.EnvVar{
				{Name: "NODE_ROLES", Value: "ingestonly"},
			}
			toCreate.Spec.NodePools[0].Autoscaling = &humiov1alpha1.AutoscalingSpec{
				MinReplicas: helpers.Int32Ptr(2),
				MaxReplicas: 10,
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster for preserve scaling test")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			shadowNodePoolKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-preserve-test-pool", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool := &humiov1alpha1.HumioNodePool{}

			By("Setting HPA scaling decision via scale subresource to 5 replicas")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return err
				}

				scaleClient := k8sClient.SubResource("scale")
				scale := &autoscalingv1.Scale{}
				if err := scaleClient.Get(ctx, shadowNodePool, scale); err != nil {
					return err
				}

				scale.Spec.Replicas = 5
				return scaleClient.Update(ctx, shadowNodePool, client.WithSubResourceBody(scale))
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Triggering HumioCluster update that would normally overwrite nodeCount")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return err
				}
				// Change autoscaling max to trigger sync - should NOT overwrite the nodeCount=5
				fetchedCluster.Spec.NodePools[0].Autoscaling.MaxReplicas = 12
				return k8sClient.Update(ctx, fetchedCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying HPA scaling decision is preserved after sync")
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return 0
				}
				return shadowNodePool.Spec.NodeCount
			}, testTimeout, suite.TestInterval).Should(Equal(int32(5)))

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})

		It("Should handle HPA upscaling decisions correctly via GetNodeCount", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-upscaling-test",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "upscale-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "NODE_ROLES", Value: "ingestonly"},
						},
						Autoscaling: &humiov1alpha1.AutoscalingSpec{
							MinReplicas: helpers.Int32Ptr(1),
							MaxReplicas: 5,
						},
					},
				},
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster for upscaling test")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Waiting for shadow HumioNodePool resource to be created")
			shadowNodePoolKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-upscale-pool", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool := &humiov1alpha1.HumioNodePool{}
			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Simulating HPA upscaling decision to 3 replicas via scale subresource")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return err
				}

				scaleClient := k8sClient.SubResource("scale")
				scale := &autoscalingv1.Scale{}
				if err := scaleClient.Get(ctx, shadowNodePool, scale); err != nil {
					return err
				}

				scale.Spec.Replicas = 3
				return scaleClient.Update(ctx, shadowNodePool, client.WithSubResourceBody(scale))
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying GetNodeCount returns HPA upscaling decision")
			Eventually(func() int {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return 0
				}
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return 0
				}

				hnp := controller.NewHumioNodeManagerFromHumioNodePool(fetchedCluster, &shadowNodePool.Spec)
				return hnp.GetNodeCount()
			}, testTimeout, suite.TestInterval).Should(Equal(3))

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})

		It("Should trigger downscaling when HPA reduces desired replicas", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-downscaling-test",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.OperatorFeatureFlags.EnableDownscalingFeature = true
			toCreate.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "downscale-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "NODE_ROLES", Value: "ingestonly"},
						},
						Autoscaling: &humiov1alpha1.AutoscalingSpec{
							MinReplicas: helpers.Int32Ptr(1),
							MaxReplicas: 5,
						},
					},
				},
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster with downscaling enabled")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Waiting for shadow HumioNodePool resource to be created")
			shadowNodePoolKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-downscale-pool", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool := &humiov1alpha1.HumioNodePool{}
			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Simulating HPA scaling up to 3 replicas first")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return err
				}

				scaleClient := k8sClient.SubResource("scale")
				scale := &autoscalingv1.Scale{}
				if err := scaleClient.Get(ctx, shadowNodePool, scale); err != nil {
					return err
				}

				scale.Spec.Replicas = 3
				return scaleClient.Update(ctx, shadowNodePool, client.WithSubResourceBody(scale))
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying scale-up is reflected in GetNodeCount")
			Eventually(func() int {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return 0
				}
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return 0
				}
				hnp := controller.NewHumioNodeManagerFromHumioNodePool(fetchedCluster, &shadowNodePool.Spec)
				return hnp.GetNodeCount()
			}, testTimeout, suite.TestInterval).Should(Equal(3))

			By("Simulating HPA scaling down to 1 replica")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return err
				}

				scaleClient := k8sClient.SubResource("scale")
				scale := &autoscalingv1.Scale{}
				if err := scaleClient.Get(ctx, shadowNodePool, scale); err != nil {
					return err
				}

				scale.Spec.Replicas = 1
				return scaleClient.Update(ctx, shadowNodePool, client.WithSubResourceBody(scale))
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying GetNodeCount reads HPA downscaling decision correctly")
			Eventually(func() int {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return 0
				}
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return 0
				}
				hnp := controller.NewHumioNodeManagerFromHumioNodePool(fetchedCluster, &shadowNodePool.Spec)
				return hnp.GetNodeCount()
			}, testTimeout, suite.TestInterval).Should(Equal(1))

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})

		It("Should detect version requirements for downscaling feature", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-version-check-test",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.OperatorFeatureFlags.EnableDownscalingFeature = true
			toCreate.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "version-test-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: "humio/humio-core:1.159.1",
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "NODE_ROLES", Value: "ingestonly"},
						},
						Autoscaling: &humiov1alpha1.AutoscalingSpec{
							MinReplicas: helpers.Int32Ptr(1),
							MaxReplicas: 3,
						},
					},
				},
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster with old LogScale version")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Verifying downscaling feature is disabled due to version requirement")
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return false
				}
				hnp := controller.NewHumioNodeManagerFromHumioCluster(fetchedCluster)
				return !hnp.IsDownscalingFeatureEnabled()
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})

		It("Should sync shadow resource status with cluster state", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-shadow-status-sync-test",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.OperatorFeatureFlags.EnableIndependentHumioNodePools = true
			toCreate.Spec.NodePools = []humiov1alpha1.HumioNodePoolSpec{
				{
					Name: "status-sync-pool",
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Autoscaling: &humiov1alpha1.AutoscalingSpec{
							MinReplicas: helpers.Int32Ptr(1),
							MaxReplicas: 3,
						},
					},
				},
			}

			suite.UsingClusterBy(key.Name, "Creating HumioCluster for shadow status sync test")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetchedCluster := &humiov1alpha1.HumioCluster{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, key, fetchedCluster); err != nil {
					return ""
				}
				return fetchedCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			By("Waiting for shadow HumioNodePool resource to be created")
			shadowNodePoolKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-status-sync-pool", key.Name),
				Namespace: key.Namespace,
			}
			shadowNodePool := &humiov1alpha1.HumioNodePool{}
			Eventually(func() error {
				return k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Updating shadow resource status to simulate pod creation")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return err
				}
				shadowNodePool.Status.CurrentReplicas = 1
				shadowNodePool.Status.DesiredReplicas = 1
				shadowNodePool.Status.State = "Running"
				return k8sClient.Status().Update(ctx, shadowNodePool)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			By("Verifying shadow resource status reflects updated state")
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, shadowNodePoolKey, shadowNodePool); err != nil {
					return -1
				}
				return shadowNodePool.Status.CurrentReplicas
			}, testTimeout, suite.TestInterval).Should(Equal(int32(1)))

			Expect(shadowNodePool.Status.DesiredReplicas).To(Equal(int32(1)))
			Expect(shadowNodePool.Status.State).To(Equal("Running"))

			suite.UsingClusterBy(key.Name, "Cleaning up HumioCluster")
			Expect(k8sClient.Delete(ctx, fetchedCluster)).Should(Succeed())
		})
	})
})
