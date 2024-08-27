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
	"os"
	"reflect"
	"strings"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/controllers"
	"github.com/humio/humio-operator/controllers/suite"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	oldSupportedHumioVersion   = "humio/humio-core:1.118.0"
	upgradeJumpHumioVersion    = "humio/humio-core:1.128.0"
	oldUnsupportedHumioVersion = "humio/humio-core:1.18.4"

	upgradePatchBestEffortOldVersion = "humio/humio-core:1.124.1"
	upgradePatchBestEffortNewVersion = "humio/humio-core:1.124.2"

	upgradeRollingBestEffortVersionJumpOldVersion = "humio/humio-core:1.124.1"
	upgradeRollingBestEffortVersionJumpNewVersion = "humio/humio-core:1.131.1"
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
	Context("Humio Cluster Simple", func() {
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
		})
	})

	Context("Humio Cluster With Multiple Node Pools", func() {
		It("Should bootstrap multi node cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-multi-node-pool",
				Namespace: testProcessNamespace,
			}
			toCreate := constructBasicMultiNodePoolHumioCluster(key, true, 1)

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapMultiNodePoolCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning)

			Eventually(func() error {
				_, err := kubernetes.GetService(ctx, k8sClient, controllers.NewHumioNodeManagerFromHumioNodePool(toCreate, &toCreate.Spec.NodePools[0]).GetServiceName(), key.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() error {
				_, err := kubernetes.GetService(ctx, k8sClient, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetServiceName(), key.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			updatedHumioCluster := humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())

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
				_, err := kubernetes.GetService(ctx, k8sClient, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetServiceName(), key.Namespace)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
		})
	})

	Context("Humio Cluster With Node Pools Only", func() {
		It("Should bootstrap nodepools only cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-node-pool-only",
				Namespace: testProcessNamespace,
			}
			toCreate := constructBasicMultiNodePoolHumioCluster(key, true, 2)
			toCreate.Spec.NodeCount = 0
			toCreate.Spec.DataVolumeSource = corev1.VolumeSource{}
			toCreate.Spec.DataVolumePersistentVolumeClaimSpecTemplate = corev1.PersistentVolumeClaimSpec{}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapMultiNodePoolCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning)

			_, err := kubernetes.GetService(ctx, k8sClient, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetServiceName(), key.Namespace)
			Expect(k8serrors.IsNotFound(err)).Should(BeTrue())

			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
		})
	})

	Context("Humio Cluster Without Init Container", func() {
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

	Context("Humio Cluster Multi Organizations", func() {
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
				Value: "multi",
			})

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
		})
	})

	Context("Humio Cluster Unsupported Version", func() {
		It("Creating cluster with unsupported version", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-unsupp-vers",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = oldUnsupportedHumioVersion

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
			}, testTimeout, suite.TestInterval).Should(Equal(fmt.Sprintf("Humio version must be at least %s: unsupported Humio version: %s", controllers.HumioVersionMinimumSupported, strings.Split(oldUnsupportedHumioVersion, ":")[1])))
		})
	})

	Context("Humio Cluster Update Image", func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = oldSupportedHumioVersion
			toCreate.Spec.NodeCount = 2

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetHumioClusterNodePoolRevisionAnnotation()
			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "1"))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = upgradeJumpHumioVersion
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsSimultaneousRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "2"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(upgradeJumpHumioVersion))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Failed Pods", func() {
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

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				Expect(pod.Status.Phase).To(BeIdenticalTo(corev1.PodRunning))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}

			suite.UsingClusterBy(key.Name, "Updating the cluster resources successfully")
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
											Key:      "some-none-existant-label",
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

			ensurePodsGoPending(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2, 1)

			Eventually(func() int {
				var pendingPodsCount int
				updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				for _, pod := range updatedClusterPods {
					if pod.Status.Phase == corev1.PodPending {
						for _, condition := range pod.Status.Conditions {
							if condition.Type == corev1.PodScheduled {
								if condition.Status == corev1.ConditionFalse && condition.Reason == controllers.PodConditionReasonUnschedulable {
									pendingPodsCount++
								}
							}
						}
					}
				}
				return pendingPodsCount
			}, testTimeout, suite.TestInterval).Should(Equal(1))

			Eventually(func() string {
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Updating the cluster resources successfully")
			Eventually(func() error {
				updatedHumioCluster := humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioNodeSpec.Affinity = originalAffinity
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 3)

			Eventually(func() string {
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))
		})
	})

	Context("Humio Cluster Update Image Rolling Restart", func() {
		It("Update should correctly replace pods to use new image in a rolling fashion", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-rolling",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = oldSupportedHumioVersion
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdate,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetHumioClusterNodePoolRevisionAnnotation()
			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "1"))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = upgradeJumpHumioVersion
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Pods upgrade in a rolling fashion because update strategy is explicitly set to rolling update")
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "2"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(upgradeJumpHumioVersion))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Image Update Strategy OnDelete", func() {
		It("Update should not replace pods on image update when update strategy OnDelete is used", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-on-delete",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = oldSupportedHumioVersion
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyOnDelete,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetHumioClusterNodePoolRevisionAnnotation()
			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "1"))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := controllers.Image
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
			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}

			suite.UsingClusterBy(key.Name, "Simulating manual deletion of pods")
			for _, pod := range updatedClusterPods {
				Expect(k8sClient.Delete(ctx, &pod)).To(Succeed())
			}

			Eventually(func() []corev1.Pod {
				var clusterPods []corev1.Pod
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				_ = suite.MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)
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
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "2"))

			updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Image Rolling Best Effort Patch", func() {
		It("Update should correctly replace pods to use new image in a rolling fashion for patch updates", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-rolling-patch",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = upgradePatchBestEffortOldVersion
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdateBestEffort,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetHumioClusterNodePoolRevisionAnnotation()
			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "1"))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = upgradePatchBestEffortNewVersion
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Pods upgrade in a rolling fashion because the new version is a patch release")
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "2"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(upgradePatchBestEffortNewVersion))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Image Best Effort Version Jump", func() {
		It("Update should correctly replace pods in parallel to use new image for version jump updates", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-vj",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = upgradeRollingBestEffortVersionJumpOldVersion
			toCreate.Spec.NodeCount = 2
			toCreate.Spec.UpdateStrategy = &humiov1alpha1.HumioUpdateStrategy{
				Type: humiov1alpha1.HumioClusterUpdateStrategyRollingUpdateBestEffort,
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetHumioClusterNodePoolRevisionAnnotation()
			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "1"))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = upgradeRollingBestEffortVersionJumpNewVersion
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			suite.UsingClusterBy(key.Name, "Pods upgrade at the same time because the new version is more than one"+
				"minor revision greater than the previous version")
			ensurePodsSimultaneousRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "2"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(upgradeRollingBestEffortVersionJumpNewVersion))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update EXTERNAL_URL", func() {
		It("Update should correctly replace pods to use the new EXTERNAL_URL in a non-rolling fashion", func() {
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
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

				revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetHumioClusterNodePoolRevisionAnnotation()
				var updatedHumioCluster humiov1alpha1.HumioCluster
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).To(ContainElement(corev1.EnvVar{
						Name:  "EXTERNAL_URL",
						Value: "http://$(POD_NAME).humiocluster-update-ext-url-headless.$(POD_NAMESPACE):$(HUMIO_PORT)",
					}))
					Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
				}
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "1"))

				suite.UsingClusterBy(key.Name, "Waiting for pods to be Running")
				Eventually(func() int {
					var runningPods int
					clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
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

				ensurePodsSimultaneousRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

				Eventually(func() string {
					updatedHumioCluster = humiov1alpha1.HumioCluster{}
					Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
					return updatedHumioCluster.Status.State
				}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

				suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "2"))

				updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
				for _, pod := range updatedClusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).To(ContainElement(corev1.EnvVar{
						Name:  "EXTERNAL_URL",
						Value: "https://$(POD_NAME).humiocluster-update-ext-url-headless.$(POD_NAMESPACE):$(HUMIO_PORT)",
					}))
					Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
				}
			}
		})
	})

	Context("Humio Cluster Update Image Multi Node Pool", func() {
		It("Update should correctly replace pods to use new image in multiple node pools", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-np",
				Namespace: testProcessNamespace,
			}
			originalImage := oldSupportedHumioVersion
			toCreate := constructBasicMultiNodePoolHumioCluster(key, true, 1)
			toCreate.Spec.Image = originalImage
			toCreate.Spec.NodeCount = 1
			toCreate.Spec.NodePools[0].NodeCount = 1
			toCreate.Spec.NodePools[0].Image = originalImage

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapMultiNodePoolCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			mainNodePoolManager := controllers.NewHumioNodeManagerFromHumioCluster(toCreate)
			revisionKey, _ := mainNodePoolManager.GetHumioClusterNodePoolRevisionAnnotation()

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

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, mainNodePoolManager.GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "1"))

			suite.UsingClusterBy(key.Name, "Updating the cluster image on the main node pool successfully")
			updatedImage := upgradeJumpHumioVersion
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

			suite.UsingClusterBy(key.Name, "Confirming only one node pool is in the correct state")
			Eventually(func() int {
				var poolsInCorrectState int
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				for _, poolStatus := range updatedHumioCluster.Status.NodePoolStatus {
					if poolStatus.State == humiov1alpha1.HumioClusterStateUpgrading {
						poolsInCorrectState++
					}
				}
				return poolsInCorrectState
			}, testTimeout, suite.TestInterval).Should(Equal(1))

			ensurePodsSimultaneousRestart(ctx, mainNodePoolManager, 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for main pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "2"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, mainNodePoolManager.GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			suite.UsingClusterBy(key.Name, "Confirming pod revision did not change for the other node pool")
			additionalNodePoolManager := controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[0])

			nonUpdatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, additionalNodePoolManager.GetPodLabels())
			Expect(nonUpdatedClusterPods).To(HaveLen(toCreate.Spec.NodePools[0].NodeCount))
			Expect(updatedHumioCluster.Spec.NodePools[0].Image).To(Equal(originalImage))
			for _, pod := range nonUpdatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(originalImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
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

			suite.UsingClusterBy(key.Name, "Confirming only one node pool is in the correct state")
			Eventually(func() int {
				var poolsInCorrectState int
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				for _, poolStatus := range updatedHumioCluster.Status.NodePoolStatus {
					if poolStatus.State == humiov1alpha1.HumioClusterStateUpgrading {
						poolsInCorrectState++
					}
				}
				return poolsInCorrectState
			}, testTimeout, suite.TestInterval).Should(Equal(1))

			ensurePodsSimultaneousRestart(ctx, additionalNodePoolManager, 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			additionalPoolRevisionKey, _ := additionalNodePoolManager.GetHumioClusterNodePoolRevisionAnnotation()
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(additionalPoolRevisionKey, "2"))

			updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, additionalNodePoolManager.GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodePools[0].NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			suite.UsingClusterBy(key.Name, "Confirming pod revision did not change for the main node pool")

			updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, mainNodePoolManager.GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Image Source", func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-source",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = upgradePatchBestEffortOldVersion
			toCreate.Spec.NodeCount = 2

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

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
			updatedImage := upgradePatchBestEffortNewVersion
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

			ensurePodsSimultaneousRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetHumioClusterNodePoolRevisionAnnotation()
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "2"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Using Wrong Image", func() {
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
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
			}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetHumioClusterNodePoolRevisionAnnotation()
			Expect(updatedHumioCluster.Annotations).To(HaveKeyWithValue(revisionKey, "1"))

			suite.UsingClusterBy(key.Name, "Updating the cluster image unsuccessfully")
			updatedImage := fmt.Sprintf("%s-missing-image", controllers.Image)
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				suite.UsingClusterBy(key.Name, fmt.Sprintf("Found of %d pods", len(clusterPods)))
				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					suite.UsingClusterBy(key.Name, fmt.Sprintf("Pod %s uses image %s and is using revision %s", pod.Spec.NodeName, pod.Spec.Containers[humioIndex].Image, pod.Annotations[controllers.PodRevisionAnnotation]))
					if pod.Spec.Containers[humioIndex].Image == updatedImage && pod.Annotations[controllers.PodRevisionAnnotation] == "2" {
						badPodCount++
					}
				}
				return badPodCount
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(toCreate.Spec.NodeCount))

			suite.UsingClusterBy(key.Name, "Simulating mock pods to be scheduled")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			_ = suite.MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)

			suite.UsingClusterBy(key.Name, "Waiting for humio cluster state to be Running")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage = controllers.Image
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

			ensurePodsSimultaneousRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 3)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations[revisionKey]).To(Equal("3"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			Expect(updatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations[controllers.PodRevisionAnnotation]).To(Equal("3"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Helper Image", func() {
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
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)

				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controllers.InitContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(controllers.HelperImage))

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

			suite.UsingClusterBy(key.Name, "Validating pod uses default helper image as auth sidecar container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)

				for _, pod := range clusterPods {
					authIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.AuthContainerName)
					return pod.Spec.InitContainers[authIdx].Image
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(controllers.HelperImage))

			suite.UsingClusterBy(key.Name, "Overriding helper image")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			customHelperImage := "humio/humio-operator-helper:master"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HelperImage = customHelperImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			suite.UsingClusterBy(key.Name, "Validating pod is recreated using the explicitly defined helper image as init container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controllers.InitContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(customHelperImage))

			suite.UsingClusterBy(key.Name, "Validating pod is recreated using the explicitly defined helper image as auth sidecar container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					authIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.AuthContainerName)
					return pod.Spec.InitContainers[authIdx].Image
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(customHelperImage))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Environment Variable", func() {
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
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
				Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(updatedEnvironmentVariables[0]))
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Environment Variable Multi Node Pool", func() {
		It("Should correctly replace pods to use new environment variable for multi node pool clusters",
			Label("envvar"), func() {
				key := types.NamespacedName{
					Name:      "humiocluster-update-envvar-np",
					Namespace: testProcessNamespace,
				}
				toCreate := constructBasicMultiNodePoolHumioCluster(key, true, 1)
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
				createAndBootstrapMultiNodePoolCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning)
				defer suite.CleanupCluster(ctx, k8sClient, toCreate)

				mainNodePoolManager := controllers.NewHumioNodeManagerFromHumioCluster(toCreate)
				customNodePoolManager := controllers.NewHumioNodeManagerFromHumioNodePool(toCreate, &toCreate.Spec.NodePools[0])

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
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).To(ContainElements(append(expectedCommonVars, corev1.EnvVar{
						Name: "test", Value: ""})))
				}

				customClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, customNodePoolManager.GetPodLabels())
				Expect(clusterPods).To(HaveLen(toCreate.Spec.NodeCount))
				for _, pod := range customClusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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

				suite.UsingClusterBy(key.Name, "Confirming only one node pool is in the correct state")
				Eventually(func() int {
					var poolsInCorrectState int
					updatedHumioCluster = humiov1alpha1.HumioCluster{}
					Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
					for _, poolStatus := range updatedHumioCluster.Status.NodePoolStatus {
						if poolStatus.State == humiov1alpha1.HumioClusterStateRestarting {
							poolsInCorrectState++
						}
					}
					return poolsInCorrectState
				}, testTimeout, suite.TestInterval).Should(Equal(1))

				suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
				ensurePodsRollingRestart(ctx, mainNodePoolManager, 2)

				Eventually(func() string {
					updatedHumioCluster = humiov1alpha1.HumioCluster{}
					Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
					return updatedHumioCluster.Status.State
				}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

				Eventually(func() bool {
					clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, mainNodePoolManager.GetPodLabels())
					Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

					for _, pod := range clusterPods {
						humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
						Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(updatedEnvironmentVariables[0]))
					}
					return true
				}, testTimeout, suite.TestInterval).Should(BeTrue())

				updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, mainNodePoolManager.GetPodLabels())
				if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
					suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
					Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
				}

				suite.UsingClusterBy(key.Name, "Confirming pod revision did not change for the other node pool")
				additionalNodePoolManager := controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[0])

				nonUpdatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, additionalNodePoolManager.GetPodLabels())
				Expect(nonUpdatedClusterPods).To(HaveLen(toCreate.Spec.NodePools[0].NodeCount))
				Expect(updatedHumioCluster.Spec.NodePools[0].EnvironmentVariables).To(Equal(toCreate.Spec.NodePools[0].EnvironmentVariables))
				for _, pod := range nonUpdatedClusterPods {
					Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "1"))
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

				suite.UsingClusterBy(key.Name, "Confirming only one node pool is in the correct state")
				Eventually(func() int {
					var poolsInCorrectState int
					updatedHumioCluster = humiov1alpha1.HumioCluster{}
					Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
					for _, poolStatus := range updatedHumioCluster.Status.NodePoolStatus {
						if poolStatus.State == humiov1alpha1.HumioClusterStateRestarting {
							poolsInCorrectState++
						}
					}
					return poolsInCorrectState
				}, testTimeout, suite.TestInterval).Should(Equal(1))

				suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
				ensurePodsRollingRestart(ctx, additionalNodePoolManager, 2)

				Eventually(func() string {
					updatedHumioCluster = humiov1alpha1.HumioCluster{}
					Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
					return updatedHumioCluster.Status.State
				}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

				Eventually(func() bool {
					clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, additionalNodePoolManager.GetPodLabels())
					Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

					for _, pod := range clusterPods {
						humioIndex, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
						Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElements(npUpdatedEnvironmentVariables))
					}
					return true
				}, testTimeout, suite.TestInterval).Should(BeTrue())

				updatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, additionalNodePoolManager.GetPodLabels())
				if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
					suite.UsingClusterBy(key.Name, "Ensuring pod names are not changed")
					Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
				}

				suite.UsingClusterBy(key.Name, "Confirming pod revision did not change for the other main pool")

				nonUpdatedClusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, mainNodePoolManager.GetPodLabels())
				Expect(nonUpdatedClusterPods).To(HaveLen(toCreate.Spec.NodeCount))
				for _, pod := range nonUpdatedClusterPods {
					Expect(pod.Annotations).To(HaveKeyWithValue(controllers.PodRevisionAnnotation, "2"))
				}
			})
	})

	Context("Humio Cluster Ingress", func() {
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
				controllers.ConstructGeneralIngress(toCreate, toCreate.Spec.Hostname),
				controllers.ConstructStreamingQueryIngress(toCreate, toCreate.Spec.Hostname),
				controllers.ConstructIngestIngress(toCreate, toCreate.Spec.Hostname),
				controllers.ConstructESIngestIngress(toCreate, toCreate.Spec.ESHostname),
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
				controllers.ConstructGeneralIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				controllers.ConstructStreamingQueryIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				controllers.ConstructIngestIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				controllers.ConstructESIngestIngress(&existingHumioCluster, existingHumioCluster.Spec.ESHostname),
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
			}, testTimeout, suite.TestInterval).Should(HaveLen(0))
		})
	})

	Context("Humio Cluster Pod Annotations", func() {
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
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, toCreate.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				Expect(len(clusterPods)).To(BeIdenticalTo(toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					Expect(pod.Annotations["humio.com/new-important-annotation"]).Should(Equal("true"))
					Expect(pod.Annotations["productName"]).Should(Equal("humio"))
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Pod Labels", func() {
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
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, toCreate.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
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

	Context("Humio Cluster Custom Service", func() {
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
				if port.Name == "http" {
					Expect(port.Port).Should(Equal(int32(8080)))
				}
				if port.Name == "es" {
					Expect(port.Port).Should(Equal(int32(9200)))
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
			suite.IncrementGenerationAndWaitForReconcileToSync(ctx, key, k8sClient, testTimeout)
			Expect(k8sClient.Delete(ctx, controllers.ConstructService(controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)))).To(Succeed())

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
			suite.IncrementGenerationAndWaitForReconcileToSync(ctx, key, k8sClient, testTimeout)
			Expect(k8sClient.Delete(ctx, controllers.ConstructService(controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)))).To(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming service gets recreated with correct Humio port")
			Eventually(func() types.UID {
				newSvc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				suite.UsingClusterBy(key.Name, fmt.Sprintf("Waiting for Service to get recreated. ServiceBeforeDeletion.Metadata=%#+v, CurrentServiceFromAPI.Metadata=%#+v", svc.ObjectMeta, newSvc.ObjectMeta))
				return newSvc.UID
			}, testTimeout, suite.TestInterval).ShouldNot(BeEquivalentTo(svc.UID))

			Eventually(func() int32 {
				svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				for _, port := range svc.Spec.Ports {
					if port.Name == "http" {
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
			suite.IncrementGenerationAndWaitForReconcileToSync(ctx, key, k8sClient, testTimeout)
			Expect(k8sClient.Delete(ctx, controllers.ConstructService(controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)))).To(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming service gets recreated with correct ES port")
			Eventually(func() types.UID {
				newSvc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				suite.UsingClusterBy(key.Name, fmt.Sprintf("Waiting for Service to get recreated. ServiceBeforeDeletion.Metadata=%#+v, CurrentServiceFromAPI.Metadata=%#+v", svc.ObjectMeta, newSvc.ObjectMeta))
				return newSvc.UID
			}, testTimeout, suite.TestInterval).ShouldNot(BeEquivalentTo(svc.UID))

			Eventually(func() int32 {
				svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				for _, port := range svc.Spec.Ports {
					if port.Name == "es" {
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
				service := controllers.ConstructService(controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster))
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
				service := controllers.ConstructService(controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster))
				Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
				return service.Labels
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(updatedLabelsKey, updatedLabelsValue))

			// The selector is not controlled through the spec, but with the addition of node pools, the operator adds
			// a new selector. This test confirms the operator will be able to migrate to different selectors on the
			// service.
			suite.UsingClusterBy(key.Name, "Updating service selector for migration to node pools")
			service := controllers.ConstructService(controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster))
			Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
			delete(service.Spec.Selector, "humio.com/node-pool")
			Expect(k8sClient.Update(ctx, service)).To(Succeed())

			suite.IncrementGenerationAndWaitForReconcileToSync(ctx, key, k8sClient, testTimeout)

			Eventually(func() map[string]string {
				service := controllers.ConstructService(controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster))
				Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
				return service.Spec.Selector
			}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue("humio.com/node-pool", key.Name))

			suite.UsingClusterBy(key.Name, "Confirming headless service has the correct HTTP and ES ports")
			headlessSvc, _ := kubernetes.GetService(ctx, k8sClient, fmt.Sprintf("%s-headless", key.Name), key.Namespace)
			Expect(headlessSvc.Spec.Type).To(BeIdenticalTo(corev1.ServiceTypeClusterIP))
			for _, port := range headlessSvc.Spec.Ports {
				if port.Name == "http" {
					Expect(port.Port).Should(Equal(int32(8080)))
				}
				if port.Name == "es" {
					Expect(port.Port).Should(Equal(int32(9200)))
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
				if port.Name == "http" {
					Expect(port.Port).Should(Equal(int32(8080)))
				}
				if port.Name == "es" {
					Expect(port.Port).Should(Equal(int32(9200)))
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

	Context("Humio Cluster Container Arguments", func() {
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

			hnp := controllers.NewHumioNodeManagerFromHumioCluster(toCreate)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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

			hnp = controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)
			expectedContainerArgString := "export CORES=$(getconf _NPROCESSORS_ONLN) && export HUMIO_OPTS=\"$HUMIO_OPTS -XX:ActiveProcessorCount=$(getconf _NPROCESSORS_ONLN)\" && export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"

			Eventually(func() []string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
				if len(clusterPods) > 0 {
					humioIdx, _ := kubernetes.GetContainerIndexByName(clusterPods[0], controllers.HumioContainerName)
					return clusterPods[0].Spec.Containers[humioIdx].Args
				}
				return []string{}
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo([]string{"-c", expectedContainerArgString}))
		})
	})

	Context("Humio Cluster Container Arguments Without Zone", func() {
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
			hnp := controllers.NewHumioNodeManagerFromHumioCluster(toCreate)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, hnp.GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				if len(clusterPods) > 0 {
					humioIdx, _ := kubernetes.GetContainerIndexByName(clusterPods[0], controllers.HumioContainerName)
					return clusterPods[0].Spec.Containers[humioIdx].Args
				}
				return []string{}
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo([]string{"-c", expectedContainerArgString}))
		})
	})

	Context("Humio Cluster Service Account Annotations", func() {
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
			humioServiceAccountName := fmt.Sprintf("%s-%s", key.Name, controllers.HumioServiceAccountNameSuffix)

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

	Context("Humio Cluster Pod Security Context", func() {
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

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodSecurityContext()))
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
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					if !reflect.DeepEqual(pod.Spec.SecurityContext, &corev1.PodSecurityContext{}) {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
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
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() corev1.PodSecurityContext {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return *pod.Spec.SecurityContext
				}
				return corev1.PodSecurityContext{}
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}))

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(&corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}))
			}
		})
	})

	Context("Humio Cluster Container Security Context", func() {
		It("Should correctly handle container security context", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-containersecuritycontext",
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

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].SecurityContext).To(Equal(controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetContainerSecurityContext()))
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
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					if !reflect.DeepEqual(pod.Spec.Containers[humioIdx].SecurityContext, &corev1.SecurityContext{}) {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() corev1.SecurityContext {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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

	Context("Humio Cluster Container Probes", func() {
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

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].ReadinessProbe).To(Equal(controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetContainerReadinessProbe()))
				Expect(pod.Spec.Containers[humioIdx].LivenessProbe).To(Equal(controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetContainerLivenessProbe()))
				Expect(pod.Spec.Containers[humioIdx].StartupProbe).To(Equal(controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetContainerStartupProbe()))
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
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			suite.UsingClusterBy(key.Name, "Confirming pods do not have a readiness probe set")
			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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
							Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
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
							Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
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
							Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
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
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].ReadinessProbe
				}
				return &corev1.Probe{}
			}, testTimeout, suite.TestInterval).Should(Equal(&corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].LivenessProbe
				}
				return &corev1.Probe{}
			}, testTimeout, suite.TestInterval).Should(Equal(&corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].StartupProbe
				}
				return &corev1.Probe{}
			}, testTimeout, suite.TestInterval).Should(Equal(&corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
						Scheme: getProbeScheme(&updatedHumioCluster),
					},
				},
				PeriodSeconds:    10,
				TimeoutSeconds:   4,
				SuccessThreshold: 1,
				FailureThreshold: 30,
			}))

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].ReadinessProbe).To(Equal(&corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
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
							Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
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
							Port:   intstr.IntOrString{IntVal: controllers.HumioPort},
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

	Context("Humio Cluster Ekstra Kafka Configs", func() {
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

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "EXTRA_KAFKA_CONFIGS_FILE",
					Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", controllers.ExtraKafkaPropertiesFilename),
				}))
			}

			suite.UsingClusterBy(key.Name, "Confirming pods have additional volume mounts for extra kafka configs")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).Should(ContainElement(corev1.Volume{
				Name: "extra-kafka-configs",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetExtraKafkaConfigsConfigMapName(),
						},
						DefaultMode: &mode,
					},
				},
			}))

			suite.UsingClusterBy(key.Name, "Confirming config map contains desired extra kafka configs")
			configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetExtraKafkaConfigsConfigMapName(), key.Namespace)
			Expect(configMap.Data[controllers.ExtraKafkaPropertiesFilename]).To(Equal(toCreate.Spec.ExtraKafkaConfigs))

			suite.UsingClusterBy(key.Name, "Removing extra kafka configs")
			var updatedHumioCluster humiov1alpha1.HumioCluster
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "EXTRA_KAFKA_CONFIGS_FILE",
				Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", controllers.ExtraKafkaPropertiesFilename),
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volume mounts for extra kafka configs")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.Volume{
				Name: "extra-kafka-configs",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetExtraKafkaConfigsConfigMapName(),
						},
						DefaultMode: &mode,
					},
				},
			}))
		})
	})

	Context("Humio Cluster View Group Permissions", func() {
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
				_, err := kubernetes.GetConfigMap(ctx, k8sClient, controllers.ViewGroupPermissionsConfigMapName(toCreate), toCreate.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pods have the expected environment variable, volume and volume mounts")
			mode := int32(420)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
					Value: "true",
				}))
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(ContainElement(corev1.VolumeMount{
					Name:      "view-group-permissions",
					ReadOnly:  true,
					MountPath: fmt.Sprintf("%s/%s", controllers.HumioDataPath, controllers.ViewGroupPermissionsFilename),
					SubPath:   controllers.ViewGroupPermissionsFilename,
				}))
				Expect(pod.Spec.Volumes).To(ContainElement(corev1.Volume{
					Name: "view-group-permissions",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: controllers.ViewGroupPermissionsConfigMapName(toCreate),
							},
							DefaultMode: &mode,
						},
					},
				}))
			}

			suite.UsingClusterBy(key.Name, "Confirming config map contains desired view group permissions")
			configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controllers.ViewGroupPermissionsConfigMapName(toCreate), key.Namespace)
			Expect(configMap.Data[controllers.ViewGroupPermissionsFilename]).To(Equal(toCreate.Spec.ViewGroupPermissions))

			suite.UsingClusterBy(key.Name, "Removing view group permissions")
			var updatedHumioCluster humiov1alpha1.HumioCluster
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
				Value: "true",
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volume mounts for view group permissions")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.VolumeMount{
				Name:      "view-group-permissions",
				ReadOnly:  true,
				MountPath: fmt.Sprintf("%s/%s", controllers.HumioDataPath, controllers.ViewGroupPermissionsFilename),
				SubPath:   controllers.ViewGroupPermissionsFilename,
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volumes for view group permissions")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.Volume{
				Name: "view-group-permissions",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: controllers.ViewGroupPermissionsConfigMapName(toCreate),
						},
						DefaultMode: &mode,
					},
				},
			}))

			suite.UsingClusterBy(key.Name, "Confirming config map was cleaned up")
			Eventually(func() bool {
				_, err := kubernetes.GetConfigMap(ctx, k8sClient, controllers.ViewGroupPermissionsConfigMapName(toCreate), toCreate.Namespace)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Role Permissions", func() {
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
				_, err := kubernetes.GetConfigMap(ctx, k8sClient, controllers.RolePermissionsConfigMapName(toCreate), toCreate.Namespace)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Confirming pods have the expected environment variable, volume and volume mounts")
			mode := int32(420)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
					Value: "true",
				}))
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(ContainElement(corev1.VolumeMount{
					Name:      "role-permissions",
					ReadOnly:  true,
					MountPath: fmt.Sprintf("%s/%s", controllers.HumioDataPath, controllers.RolePermissionsFilename),
					SubPath:   controllers.RolePermissionsFilename,
				}))
				Expect(pod.Spec.Volumes).To(ContainElement(corev1.Volume{
					Name: "role-permissions",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: controllers.RolePermissionsConfigMapName(toCreate),
							},
							DefaultMode: &mode,
						},
					},
				}))
			}

			suite.UsingClusterBy(key.Name, "Confirming config map contains desired role permissions")
			configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, controllers.RolePermissionsConfigMapName(toCreate), key.Namespace)
			Expect(configMap.Data[controllers.RolePermissionsFilename]).To(Equal(toCreate.Spec.RolePermissions))

			suite.UsingClusterBy(key.Name, "Removing role permissions")
			var updatedHumioCluster humiov1alpha1.HumioCluster
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
				Value: "true",
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volume mounts for role permissions")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.VolumeMount{
				Name:      "role-permissions",
				ReadOnly:  true,
				MountPath: fmt.Sprintf("%s/%s", controllers.HumioDataPath, controllers.RolePermissionsFilename),
				SubPath:   controllers.RolePermissionsFilename,
			}))

			suite.UsingClusterBy(key.Name, "Confirming pods do not have additional volumes for role permissions")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).ShouldNot(ContainElement(corev1.Volume{
				Name: "role-permissions",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: controllers.RolePermissionsConfigMapName(toCreate),
						},
						DefaultMode: &mode,
					},
				},
			}))

			suite.UsingClusterBy(key.Name, "Confirming config map was cleaned up")
			Eventually(func() bool {
				_, err := kubernetes.GetConfigMap(ctx, k8sClient, controllers.RolePermissionsConfigMapName(toCreate), toCreate.Namespace)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Persistent Volumes", func() {
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

			Expect(kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetNodePoolLabels())).To(HaveLen(0))

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
				return kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetNodePoolLabels())
			}, testTimeout, suite.TestInterval).Should(HaveLen(toCreate.Spec.NodeCount))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			suite.UsingClusterBy(key.Name, "Confirming pods are using PVC's and no PVC is left unused")
			pvcList, _ := kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetNodePoolLabels())
			foundPodList, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetNodePoolLabels())
			for _, pod := range foundPodList {
				_, err := controllers.FindPvcForPod(pvcList, pod)
				Expect(err).ShouldNot(HaveOccurred())
			}
			_, err := controllers.FindNextAvailablePvc(pvcList, foundPodList, map[string]struct{}{})
			Expect(err).Should(HaveOccurred())
		})
	})

	Context("Humio Cluster Extra Volumes", func() {
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

			initialExpectedVolumesCount := 6
			initialExpectedVolumeMountsCount := 4

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				// if we run on a real cluster we have TLS enabled (using 2 volumes),
				// and k8s will automatically inject a service account token adding one more
				initialExpectedVolumesCount += 3
				initialExpectedVolumeMountsCount += 2
			}

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.Volumes).To(HaveLen(initialExpectedVolumesCount))
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(HaveLen(initialExpectedVolumeMountsCount))
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
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, suite.TestInterval).Should(HaveLen(initialExpectedVolumesCount + 1))
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, suite.TestInterval).Should(HaveLen(initialExpectedVolumeMountsCount + 1))
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.Volumes).Should(ContainElement(extraVolume))
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).Should(ContainElement(extraVolumeMount))
			}
		})
	})

	Context("Humio Cluster Custom Path", func() {
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
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				protocol = "https"
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming PUBLIC_URL is set to default value and PROXY_PREFIX_URL is not set")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(controllers.EnvVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal(fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)", protocol)))
				Expect(controllers.EnvVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL")).To(BeFalse())
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
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					if !controllers.EnvVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL") {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Confirming PUBLIC_URL and PROXY_PREFIX_URL have been correctly configured")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(controllers.EnvVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal(fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)/logs", protocol)))
				Expect(controllers.EnvVarHasValue(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL", "/logs")).To(BeTrue())
			}

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			suite.UsingClusterBy(key.Name, "Confirming cluster returns to Running state")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())

				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})

		It("Should correctly handle custom paths with ingress enabled", func() {
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
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(controllers.EnvVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal("https://test-cluster.humio.com"))
				Expect(controllers.EnvVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL")).To(BeFalse())
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
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
					if !controllers.EnvVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL") {
						return false
					}
				}
				return true
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Confirming PUBLIC_URL and PROXY_PREFIX_URL have been correctly configured")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
				Expect(controllers.EnvVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal("https://test-cluster.humio.com/logs"))
				Expect(controllers.EnvVarHasValue(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL", "/logs")).To(BeTrue())
			}

			suite.UsingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			suite.UsingClusterBy(key.Name, "Confirming cluster returns to Running state")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())

				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})
	})

	Context("Humio Cluster Config Errors", func() {
		It("Creating cluster with conflicting volume mount name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-volmnt-name",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioNodeSpec.ExtraHumioVolumeMounts = []corev1.VolumeMount{
				{
					Name: "humio-data",
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
			toCreate.Spec.HumioNodeSpec.ExtraHumioVolumeMounts = []corev1.VolumeMount{
				{
					Name:      "something-unique",
					MountPath: controllers.HumioDataPath,
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
			toCreate.Spec.HumioNodeSpec.ExtraVolumes = []corev1.Volume{
				{
					Name: "humio-data",
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
			toCreate.Spec.HumioNodeSpec.NodeCount = 1

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

	Context("Humio Cluster Without TLS for Ingress", func() {
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

	Context("Humio Cluster with additional hostnames for TLS", func() {
		It("Creating cluster with additional hostnames for TLS", func() {
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				key := types.NamespacedName{
					Name:      "humiocluster-tls-additional-hostnames",
					Namespace: testProcessNamespace,
				}
				toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
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
			}
		})
	})

	Context("Humio Cluster Ingress", func() {
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
			}, testTimeout, suite.TestInterval).Should(HaveLen(0))

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

	Context("Humio Cluster with non-existent custom service accounts", func() {
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

	Context("Humio Cluster With Custom Service Accounts", func() {
		It("Creating cluster with custom service accounts", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-service-accounts",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.InitServiceAccountName = "init-custom-service-account"
			toCreate.Spec.AuthServiceAccountName = "auth-custom-service-account"
			toCreate.Spec.HumioServiceAccountName = "humio-custom-service-account"

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming init container is using the correct service account")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controllers.InitContainerName)
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
						Expect(secret.ObjectMeta.Annotations[corev1.ServiceAccountNameKey]).To(Equal(toCreate.Spec.InitServiceAccountName))
					}
				}
			}
			suite.UsingClusterBy(key.Name, "Confirming auth container is using the correct service account")
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.AuthContainerName)
				var serviceAccountSecretVolumeName string
				for _, volumeMount := range pod.Spec.Containers[humioIdx].VolumeMounts {
					if volumeMount.MountPath == "/var/run/secrets/kubernetes.io/serviceaccount" {
						serviceAccountSecretVolumeName = volumeMount.Name
					}
				}
				Expect(serviceAccountSecretVolumeName).To(Not(BeEmpty()))
				for _, volume := range pod.Spec.Volumes {
					if volume.Name == serviceAccountSecretVolumeName {
						secret, err := kubernetes.GetSecret(ctx, k8sClient, volume.Secret.SecretName, key.Namespace)
						Expect(err).ShouldNot(HaveOccurred())
						Expect(secret.ObjectMeta.Annotations[corev1.ServiceAccountNameKey]).To(Equal(toCreate.Spec.AuthServiceAccountName))
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
			toCreate.Spec.AuthServiceAccountName = "custom-service-account"
			toCreate.Spec.HumioServiceAccountName = "custom-service-account"

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming init container is using the correct service account")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetInitContainerIndexByName(pod, controllers.InitContainerName)
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
						Expect(secret.ObjectMeta.Annotations[corev1.ServiceAccountNameKey]).To(Equal(toCreate.Spec.InitServiceAccountName))
					}
				}
			}
			suite.UsingClusterBy(key.Name, "Confirming auth container is using the correct service account")
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, controllers.AuthContainerName)
				var serviceAccountSecretVolumeName string
				for _, volumeMount := range pod.Spec.Containers[humioIdx].VolumeMounts {
					if volumeMount.MountPath == "/var/run/secrets/kubernetes.io/serviceaccount" {
						serviceAccountSecretVolumeName = volumeMount.Name
					}
				}
				Expect(serviceAccountSecretVolumeName).To(Not(BeEmpty()))
				for _, volume := range pod.Spec.Volumes {
					if volume.Name == serviceAccountSecretVolumeName {
						secret, err := kubernetes.GetSecret(ctx, k8sClient, volume.Secret.SecretName, key.Namespace)
						Expect(err).ShouldNot(HaveOccurred())
						Expect(secret.ObjectMeta.Annotations[corev1.ServiceAccountNameKey]).To(Equal(toCreate.Spec.AuthServiceAccountName))
					}
				}
			}
			suite.UsingClusterBy(key.Name, "Confirming humio pod is using the correct service account")
			for _, pod := range clusterPods {
				Expect(pod.Spec.ServiceAccountName).To(Equal(toCreate.Spec.HumioServiceAccountName))
			}
		})
	})

	Context("Humio Cluster With Service Annotations", func() {
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

	Context("Humio Cluster With Custom Tolerations", func() {
		It("Creating cluster with custom tolerations", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-tolerations",
				Namespace: testProcessNamespace,
			}
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Tolerations = []corev1.Toleration{
				{
					Key:      "key",
					Operator: "Equal",
					Value:    "value",
					Effect:   "NoSchedule",
				},
			}

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Confirming the humio pods use the requested tolerations")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.Tolerations).To(ContainElement(toCreate.Spec.Tolerations[0]))
			}
		})
	})

	Context("Humio Cluster With Custom Topology Spread Constraints", func() {
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
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.TopologySpreadConstraints).To(ContainElement(toCreate.Spec.TopologySpreadConstraints[0]))
			}
		})
	})

	Context("Humio Cluster With Custom Priority Class Name", func() {
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
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				Expect(pod.Spec.PriorityClassName).To(Equal(toCreate.Spec.PriorityClassName))
			}
		})
	})

	Context("Humio Cluster With Service Labels", func() {
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

	Context("Humio Cluster with shared process namespace and sidecars", func() {
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
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			for _, pod := range clusterPods {
				if pod.Spec.ShareProcessNamespace != nil {
					Expect(*pod.Spec.ShareProcessNamespace).To(BeFalse())
				}
				Expect(pod.Spec.Containers).Should(HaveLen(2))
			}

			suite.UsingClusterBy(key.Name, "Enabling shared process namespace and sidecars")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}

				updatedHumioCluster.Spec.ShareProcessNamespace = helpers.BoolPtr(true)
				updatedHumioCluster.Spec.SidecarContainers = []corev1.Container{
					{
						Name:    "jmap",
						Image:   controllers.Image,
						Command: []string{"/bin/sh"},
						Args:    []string{"-c", "HUMIO_PID=$(ps -e | grep java | awk '{print $1'}); while :; do sleep 30 ; jmap -histo:live $HUMIO_PID | head -n203 ; done"},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "tmp",
								MountPath: controllers.TmpPath,
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					if pod.Spec.ShareProcessNamespace != nil {
						return *pod.Spec.ShareProcessNamespace
					}
				}
				return false
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Confirming pods contain the new sidecar")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					for _, container := range pod.Spec.Containers {
						if container.Name == controllers.HumioContainerName {
							continue
						}
						if container.Name == controllers.AuthContainerName {
							continue
						}
						return container.Name
					}
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal("jmap"))
		})
	})

	Context("Humio Cluster pod termination grace period", func() {
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
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				_ = suite.MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)

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
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				for _, pod := range clusterPods {
					if pod.Spec.TerminationGracePeriodSeconds != nil {
						return *pod.Spec.TerminationGracePeriodSeconds
					}
				}
				return 0
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(120))
		})
	})

	Context("Humio Cluster install license", func() {
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

	Context("Humio Cluster state adjustment", func() {
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

	Context("Humio Cluster with envSource configmap", func() {
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
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], controllers.HumioContainerName)
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
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			suite.UsingClusterBy(key.Name, "Confirming pods contain the new env vars")
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				var podsContainingEnvFrom int
				for _, pod := range clusterPods {
					humioIdx, err := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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

	Context("Humio Cluster with envSource secret", func() {
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
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], controllers.HumioContainerName)
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
			ensurePodsRollingRestart(ctx, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster), 2)

			suite.UsingClusterBy(key.Name, "Confirming pods contain the new env vars")
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				var podsContainingEnvFrom int
				for _, pod := range clusterPods {
					humioIdx, err := kubernetes.GetContainerIndexByName(pod, controllers.HumioContainerName)
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

	Context("Humio Cluster with resources without node pool name label", func() {
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
				clusterPods, err = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
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
})
