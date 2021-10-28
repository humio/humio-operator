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

package controllers

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TODO: refactor, this is copied from humio/humio-operator/images/helper/main.go
const (
	// apiTokenMethodAnnotationName is used to signal what mechanism was used to obtain the API token
	apiTokenMethodAnnotationName = "humio.com/api-token-method"
	// apiTokenMethodFromAPI is used to indicate that the API token was obtained using an API call
	apiTokenMethodFromAPI = "api"
)

var _ = Describe("HumioCluster Controller", func() {

	BeforeEach(func() {
		// failed test runs that don't clean up leave resources behind.

	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test

	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Humio Cluster Simple", func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-simple",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = helpers.IntPtr(1)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Expanding the cluster")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, toCreate)
				if err != nil {
					return err
				}
				toCreate.Spec.NodeCount = helpers.IntPtr(2)
				return k8sClient.Update(ctx, toCreate)
			}, testTimeout, testInterval).Should(Succeed())
		})
	})

	Context("Humio Cluster Without Init Container", func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-no-init-container",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.DisableInitContainer = true

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)
		})
	})

	Context("Humio Cluster Multi Organizations", func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-multi-org",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{
				Name:  "ENABLE_ORGANIZATIONS",
				Value: "true",
			})
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{
				Name:  "ORGANIZATION_MODE",
				Value: "multi",
			})

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)
		})
	})

	Context("Humio Cluster Unsupported Version", func() {
		It("Creating cluster with unsupported version", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-unsupp-vers",
				Namespace: testProcessID,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					Image: "humio/humio-core:1.18.4",
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)
			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
	})

	Context("Humio Cluster Update Image", func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = "humio/humio-core:1.30.1"
			toCreate.Spec.NodeCount = helpers.IntPtr(1)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Expanding the cluster")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, toCreate)
				if err != nil {
					return err
				}
				toCreate.Spec.NodeCount = helpers.IntPtr(2)
				return k8sClient.Update(ctx, toCreate)
			}, testTimeout, testInterval).Should(Succeed())

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations[podRevisionAnnotation]).To(Equal("1"))
			}
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations[podRevisionAnnotation]).To(Equal("1"))

			usingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage := image
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsSimultaneousRestart(ctx, &updatedHumioCluster, key, 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			usingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations[podRevisionAnnotation]).To(Equal("2"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
			Expect(updatedClusterPods).To(HaveLen(*toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations[podRevisionAnnotation]).To(Equal("2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				usingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Image Source", func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image-source",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Image = "humio/humio-core:1.30.1"
			toCreate.Spec.NodeCount = helpers.IntPtr(1)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Expanding the cluster")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, toCreate)
				if err != nil {
					return err
				}
				toCreate.Spec.NodeCount = helpers.IntPtr(2)
				return k8sClient.Update(ctx, toCreate)
			}, testTimeout, testInterval).Should(Succeed())

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

			usingClusterBy(key.Name, "Adding missing imageSource to pod spec")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming the HumioCluster goes into ConfigError state since the configmap does not exist")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			usingClusterBy(key.Name, "Creating the imageSource configmap")
			updatedImage := image
			envVarSourceConfigMap := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "image-source",
					Namespace: key.Namespace,
				},
				Data: map[string]string{"tag": updatedImage},
			}
			Expect(k8sClient.Create(ctx, &envVarSourceConfigMap)).To(Succeed())

			usingClusterBy(key.Name, "Updating imageSource of pod spec")
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
			}, testTimeout, testInterval).Should(Succeed())

			ensurePodsSimultaneousRestart(ctx, &updatedHumioCluster, key, 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			usingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations[podRevisionAnnotation]).To(Equal("2"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
			Expect(updatedClusterPods).To(HaveLen(*toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations[podRevisionAnnotation]).To(Equal("2"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				usingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Using Wrong Image", func() {
		It("Update should correctly replace pods after using wrong image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-wrong-image",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = helpers.IntPtr(1)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Expanding the cluster")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, toCreate)
				if err != nil {
					return err
				}
				toCreate.Spec.NodeCount = helpers.IntPtr(2)
				return k8sClient.Update(ctx, toCreate)
			}, testTimeout, testInterval).Should(Succeed())

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations[podRevisionAnnotation]).To(Equal("1"))
			}
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations[podRevisionAnnotation]).To(Equal("1"))

			usingClusterBy(key.Name, "Updating the cluster image unsuccessfully")
			updatedImage := fmt.Sprintf("humio/humio-operator:%s-missing-image", HumioVersionMinimumSupported)
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			usingClusterBy(key.Name, "Waiting until pods are started with the bad image")
			Eventually(func() int {
				var badPodCount int
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if pod.Spec.Containers[humioIndex].Image == updatedImage && pod.Annotations[podRevisionAnnotation] == "2" {
						badPodCount++
					}
				}
				return badPodCount
			}, testTimeout, testInterval).Should(BeIdenticalTo(2))

			usingClusterBy(key.Name, "Simulating mock pods to be scheduled")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			markPodsAsRunning(ctx, k8sClient, clusterPods)

			usingClusterBy(key.Name, "Waiting for humio cluster state to be Running")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			usingClusterBy(key.Name, "Updating the cluster image successfully")
			updatedImage = image
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			ensurePodsSimultaneousRestart(ctx, &updatedHumioCluster, key, 3)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			usingClusterBy(key.Name, "Confirming pod revision is the same for all pods and the cluster itself")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			Expect(updatedHumioCluster.Annotations[podRevisionAnnotation]).To(Equal("3"))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
			Expect(updatedClusterPods).To(HaveLen(*toCreate.Spec.NodeCount))
			for _, pod := range updatedClusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations[podRevisionAnnotation]).To(Equal("3"))
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				usingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Helper Image", func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-helper-image",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HelperImage = ""
			toCreate.Spec.NodeCount = helpers.IntPtr(1)

			usingClusterBy(key.Name, "Creating a cluster with default helper image")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "Expanding the cluster")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, toCreate)
				if err != nil {
					return err
				}
				toCreate.Spec.NodeCount = helpers.IntPtr(2)
				return k8sClient.Update(ctx, toCreate)
			}, testTimeout, testInterval).Should(Succeed())

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

			usingClusterBy(key.Name, "Validating all pods have been created")
			Eventually(func() []corev1.Pod {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(ctx, k8sClient, clusterPods)

				return clusterPods
			}, testTimeout, testInterval).Should(HaveLen(*toCreate.Spec.NodeCount))

			usingClusterBy(key.Name, "Validating pod uses default helper image as init container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(ctx, k8sClient, clusterPods)

				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, initContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, testInterval).Should(Equal(helperImage))

			usingClusterBy(key.Name, "Validating pod uses default helper image as auth sidecar container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(ctx, k8sClient, clusterPods)

				for _, pod := range clusterPods {
					authIdx, _ := kubernetes.GetContainerIndexByName(pod, authContainerName)
					return pod.Spec.InitContainers[authIdx].Image
				}
				return ""
			}, testTimeout, testInterval).Should(Equal(helperImage))

			usingClusterBy(key.Name, "Overriding helper image")
			customHelperImage := "humio/humio-operator-helper:master"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HelperImage = customHelperImage
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			usingClusterBy(key.Name, "Validating pod is recreated using the explicitly defined helper image as init container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, initContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, testInterval).Should(Equal(customHelperImage))

			usingClusterBy(key.Name, "Validating pod is recreated using the explicitly defined helper image as auth sidecar container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					authIdx, _ := kubernetes.GetContainerIndexByName(pod, authContainerName)
					return pod.Spec.InitContainers[authIdx].Image
				}
				return ""
			}, testTimeout, testInterval).Should(Equal(customHelperImage))

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				usingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Update Environment Variable", func() {
		It("Should correctly replace pods to use new environment variable", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-envvar",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = helpers.IntPtr(1)
			toCreate.Spec.EnvironmentVariables = []corev1.EnvVar{
				{
					Name:  "test",
					Value: "",
				},
				{
					Name:  "HUMIO_JVM_ARGS",
					Value: "-Xss2m -Xms256m -Xmx1536m -server -XX:+UseParallelOldGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC -Dzookeeper.client.secure=false",
				},
				{
					Name:  "ZOOKEEPER_URL",
					Value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless.default:2181",
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
					Value: "single-user",
				},
				{
					Name:  "SINGLE_USER_PASSWORD",
					Value: "password",
				},
			}

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Expanding the cluster")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, toCreate)
				if err != nil {
					return err
				}
				toCreate.Spec.NodeCount = helpers.IntPtr(2)
				return k8sClient.Update(ctx, toCreate)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Validating all pods have been created")
			Eventually(func() []corev1.Pod {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(ctx, k8sClient, clusterPods)

				return clusterPods
			}, testTimeout, testInterval).Should(HaveLen(*toCreate.Spec.NodeCount))

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(toCreate.Spec.EnvironmentVariables[0]))
			}

			usingClusterBy(key.Name, "Updating the environment variable successfully")
			updatedEnvironmentVariables := []corev1.EnvVar{
				{
					Name:  "test",
					Value: "update",
				},
				{
					Name:  "HUMIO_JVM_ARGS",
					Value: "-Xss2m -Xms256m -Xmx1536m -server -XX:+UseParallelOldGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC -Dzookeeper.client.secure=false",
				},
				{
					Name:  "ZOOKEEPER_URL",
					Value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless.default:2181",
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
					Value: "single-user",
				},
				{
					Name:  "SINGLE_USER_PASSWORD",
					Value: "password",
				},
			}
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.EnvironmentVariables = updatedEnvironmentVariables
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				Expect(len(clusterPods)).To(BeIdenticalTo(2))

				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(updatedEnvironmentVariables[0]))
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			updatedClusterPods, _ := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				usingClusterBy(key.Name, "Ensuring pod names are not changed")
				Expect(podNames(clusterPods)).To(Equal(podNames(updatedClusterPods)))
			}
		})
	})

	Context("Humio Cluster Ingress", func() {
		It("Should correctly update ingresses to use new annotations variable", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-ingress",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Hostname = "humio.example.com"
			toCreate.Spec.ESHostname = "humio-es.humio.com"
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			desiredIngresses := []*networkingv1.Ingress{
				constructGeneralIngress(toCreate, toCreate.Spec.Hostname),
				constructStreamingQueryIngress(toCreate, toCreate.Spec.Hostname),
				constructIngestIngress(toCreate, toCreate.Spec.Hostname),
				constructESIngestIngress(toCreate, toCreate.Spec.ESHostname),
			}

			var foundIngressList []networkingv1.Ingress
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(4))

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

			usingClusterBy(key.Name, "Adding an additional ingress annotation successfully")
			var existingHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, key, &existingHumioCluster)).Should(Succeed())
				existingHumioCluster.Spec.Ingress.Annotations = map[string]string{"humio.com/new-important-annotation": "true"}
				return k8sClient.Update(ctx, &existingHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, ingress := range ingresses {
					if _, ok := ingress.Annotations["humio.com/new-important-annotation"]; !ok {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			Eventually(func() ([]networkingv1.Ingress, error) {
				return kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			}, testTimeout, testInterval).Should(HaveLen(4))

			usingClusterBy(key.Name, "Changing ingress hostnames successfully")
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, key, &existingHumioCluster)).Should(Succeed())
				existingHumioCluster.Spec.Hostname = "humio2.example.com"
				existingHumioCluster.Spec.ESHostname = "humio2-es.example.com"
				return k8sClient.Update(ctx, &existingHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			desiredIngresses = []*networkingv1.Ingress{
				constructGeneralIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				constructStreamingQueryIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				constructIngestIngress(&existingHumioCluster, existingHumioCluster.Spec.Hostname),
				constructESIngestIngress(&existingHumioCluster, existingHumioCluster.Spec.ESHostname),
			}
			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, ingress := range ingresses {
					for _, rule := range ingress.Spec.Rules {
						if rule.Host != "humio2.example.com" && rule.Host != "humio2-es.example.com" {
							return false
						}
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

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

			usingClusterBy(key.Name, "Removing an ingress annotation successfully")
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, key, &existingHumioCluster)).Should(Succeed())
				delete(existingHumioCluster.Spec.Ingress.Annotations, "humio.com/new-important-annotation")
				return k8sClient.Update(ctx, &existingHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, ingress := range ingresses {
					if _, ok := ingress.Annotations["humio.com/new-important-annotation"]; ok {
						return true
					}
				}
				return false
			}, testTimeout, testInterval).Should(BeFalse())

			foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, foundIngress := range foundIngressList {
				Expect(foundIngress.Annotations).ShouldNot(HaveKey("humio.com/new-important-annotation"))
			}

			usingClusterBy(key.Name, "Disabling ingress successfully")
			Eventually(func() error {
				Expect(k8sClient.Get(ctx, key, &existingHumioCluster)).Should(Succeed())
				existingHumioCluster.Spec.Ingress.Enabled = false
				return k8sClient.Update(ctx, &existingHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() ([]networkingv1.Ingress, error) {
				return kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			}, testTimeout, testInterval).Should(HaveLen(0))
		})
	})

	Context("Humio Cluster Pod Annotations", func() {
		It("Should be correctly annotated", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-pods",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.PodAnnotations = map[string]string{"humio.com/new-important-annotation": "true"}

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
				Expect(len(clusterPods)).To(BeIdenticalTo(*toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					Expect(pod.Annotations["humio.com/new-important-annotation"]).Should(Equal("true"))
					Expect(pod.Annotations["productName"]).Should(Equal("humio"))
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Custom Service", func() {
		It("Should correctly use default service", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-svc",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

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
			var clusterBeforeUpdate humiov1alpha1.HumioCluster
			Eventually(func() error {
				return k8sClient.Get(ctx, key, &clusterBeforeUpdate)
			}, testTimeout, testInterval).Should(Succeed())

			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "Updating service type")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServiceType = corev1.ServiceTypeLoadBalancer
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			// Wait for the new HumioCluster to finish any existing reconcile loop by waiting for the
			// status.observedGeneration to equal at least that of the current resource version. This will avoid race
			// conditions where the HumioCluster is updated and service is deleted mid-way through a reconcile.
			waitForReconcileToRun(ctx, key, k8sClient, clusterBeforeUpdate)
			Expect(k8sClient.Delete(ctx, constructService(&updatedHumioCluster))).To(Succeed())

			usingClusterBy(key.Name, "Confirming we can see the updated HumioCluster object")
			Eventually(func() corev1.ServiceType {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Spec.HumioServiceType
			}, testTimeout, testInterval).Should(BeIdenticalTo(corev1.ServiceTypeLoadBalancer))

			Eventually(func() types.UID {
				newSvc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				usingClusterBy(key.Name, fmt.Sprintf("Waiting for Service to get recreated. ServiceBeforeDeletion.Metadata=%#+v, CurrentServiceFromAPI.Metadata=%#+v", svc.ObjectMeta, newSvc.ObjectMeta))
				return newSvc.UID
			}, testTimeout, testInterval).ShouldNot(BeEquivalentTo(svc.UID))

			Eventually(func() corev1.ServiceType {
				svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				return svc.Spec.Type
			}, testTimeout, testInterval).Should(Equal(corev1.ServiceTypeLoadBalancer))

			Eventually(func() error {
				return k8sClient.Get(ctx, key, &clusterBeforeUpdate)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Updating Humio port")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServicePort = 443
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			// TODO: Right now the service is not updated properly, so we delete it ourselves to make the operator recreate the service
			// Wait for the new HumioCluster to finish any existing reconcile loop by waiting for the
			// status.observedGeneration to equal at least that of the current resource version. This will avoid race
			// conditions where the HumioCluster is updated and service is deleted mid-way through a reconcile.
			waitForReconcileToRun(ctx, key, k8sClient, clusterBeforeUpdate)
			Expect(k8sClient.Delete(ctx, constructService(&updatedHumioCluster))).To(Succeed())

			usingClusterBy(key.Name, "Confirming service gets recreated with correct Humio port")
			Eventually(func() types.UID {
				newSvc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				usingClusterBy(key.Name, fmt.Sprintf("Waiting for Service to get recreated. ServiceBeforeDeletion.Metadata=%#+v, CurrentServiceFromAPI.Metadata=%#+v", svc.ObjectMeta, newSvc.ObjectMeta))
				return newSvc.UID
			}, testTimeout, testInterval).ShouldNot(BeEquivalentTo(svc.UID))

			Eventually(func() int32 {
				svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				for _, port := range svc.Spec.Ports {
					if port.Name == "http" {
						return port.Port
					}
				}
				return -1
			}, testTimeout, testInterval).Should(Equal(int32(443)))

			Eventually(func() error {
				return k8sClient.Get(ctx, key, &clusterBeforeUpdate)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Updating ES port")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				updatedHumioCluster.Spec.HumioESServicePort = 9201
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			// TODO: Right now the service is not updated properly, so we delete it ourselves to make the operator recreate the service
			// Wait for the new HumioCluster to finish any existing reconcile loop by waiting for the
			// status.observedGeneration to equal at least that of the current resource version. This will avoid race
			// conditions where the HumioCluster is updated and service is deleted mid-way through a reconcile.
			waitForReconcileToRun(ctx, key, k8sClient, clusterBeforeUpdate)
			Expect(k8sClient.Delete(ctx, constructService(&updatedHumioCluster))).To(Succeed())

			usingClusterBy(key.Name, "Confirming service gets recreated with correct ES port")
			Eventually(func() types.UID {
				newSvc, _ := kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				usingClusterBy(key.Name, fmt.Sprintf("Waiting for Service to get recreated. ServiceBeforeDeletion.Metadata=%#+v, CurrentServiceFromAPI.Metadata=%#+v", svc.ObjectMeta, newSvc.ObjectMeta))
				return newSvc.UID
			}, testTimeout, testInterval).ShouldNot(BeEquivalentTo(svc.UID))

			Eventually(func() int32 {
				svc, _ = kubernetes.GetService(ctx, k8sClient, key.Name, key.Namespace)
				for _, port := range svc.Spec.Ports {
					if port.Name == "es" {
						return port.Port
					}
				}
				return -1
			}, testTimeout, testInterval).Should(Equal(int32(9201)))
		})
	})

	Context("Humio Cluster Container Arguments", func() {
		It("Should correctly configure container arguments and ephemeral disks env var", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-container-args",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully without ephemeral disks")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Args).To(Equal([]string{"-c", "export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"}))
				Expect(pod.Spec.Containers[humioIdx].Env).ToNot(ContainElement(corev1.EnvVar{
					Name:  "ZOOKEEPER_URL_FOR_NODE_UUID",
					Value: "$(ZOOKEEPER_URL)",
				}))
			}

			usingClusterBy(key.Name, "Updating node uuid prefix which includes ephemeral disks and zone")
			var updatedHumioCluster humiov1alpha1.HumioCluster

			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{Name: "USING_EPHEMERAL_DISKS", Value: "true"})
				updatedHumioCluster.Spec.NodeUUIDPrefix = "humio_{{.Zone}}_"
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if reflect.DeepEqual(pod.Spec.Containers[humioIdx].Args, []string{"-c", "export ZONE=$(cat /shared/availability-zone) && export ZOOKEEPER_PREFIX_FOR_NODE_UUID=/humio_$(cat /shared/availability-zone)_ && exec bash /app/humio/run.sh"}) {
						return true
					}
				}
				return false
			}, testTimeout, testInterval).Should(BeTrue())

			clusterPods, err := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			Expect(err).ToNot(HaveOccurred())
			humioIdx, _ := kubernetes.GetContainerIndexByName(clusterPods[0], humioContainerName)
			Expect(clusterPods[0].Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
				Name:  "ZOOKEEPER_URL_FOR_NODE_UUID",
				Value: "$(ZOOKEEPER_URL)",
			}))
		})
	})

	Context("Humio Cluster Container Arguments Without Zone", func() {
		It("Should correctly configure container arguments", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-container-without-zone-args",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Args).To(Equal([]string{"-c", "export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"}))
			}

			usingClusterBy(key.Name, "Updating node uuid prefix which includes ephemeral disks but not zone")
			var updatedHumioCluster humiov1alpha1.HumioCluster

			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{Name: "USING_EPHEMERAL_DISKS", Value: "true"})
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if reflect.DeepEqual(pod.Spec.Containers[humioIdx].Args, []string{"-c", "export ZONE=$(cat /shared/availability-zone) && export ZOOKEEPER_PREFIX_FOR_NODE_UUID=/humio_ && exec bash /app/humio/run.sh"}) {
						return true
					}
				}
				return false
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Service Account Annotations", func() {
		It("Should correctly handle service account annotations", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-sa-annotations",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			Eventually(func() error {
				_, err := kubernetes.GetServiceAccount(ctx, k8sClient, humioServiceAccountNameOrDefault(toCreate), key.Namespace)
				return err
			}, testTimeout, testInterval).Should(Succeed())
			serviceAccount, _ := kubernetes.GetServiceAccount(ctx, k8sClient, humioServiceAccountNameOrDefault(toCreate), key.Namespace)
			Expect(serviceAccount.Annotations).Should(BeNil())

			usingClusterBy(key.Name, "Adding an annotation successfully")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServiceAccountAnnotations = map[string]string{"some-annotation": "true"}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() bool {
				serviceAccount, _ = kubernetes.GetServiceAccount(ctx, k8sClient, humioServiceAccountNameOrDefault(toCreate), key.Namespace)
				_, ok := serviceAccount.Annotations["some-annotation"]
				return ok
			}, testTimeout, testInterval).Should(BeTrue())
			Expect(serviceAccount.Annotations["some-annotation"]).Should(Equal("true"))

			usingClusterBy(key.Name, "Removing all annotations successfully")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HumioServiceAccountAnnotations = nil
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() map[string]string {
				serviceAccount, _ = kubernetes.GetServiceAccount(ctx, k8sClient, humioServiceAccountNameOrDefault(toCreate), key.Namespace)
				return serviceAccount.Annotations
			}, testTimeout, testInterval).Should(BeNil())
		})
	})

	Context("Humio Cluster Pod Security Context", func() {
		It("Should correctly handle pod security context", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-podsecuritycontext",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(podSecurityContextOrDefault(toCreate)))
			}
			usingClusterBy(key.Name, "Updating Pod Security Context to be empty")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.PodSecurityContext = &corev1.PodSecurityContext{}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					if !reflect.DeepEqual(pod.Spec.SecurityContext, &corev1.PodSecurityContext{}) {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(&corev1.PodSecurityContext{}))
			}

			usingClusterBy(key.Name, "Updating Pod Security Context to be non-empty")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.PodSecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			Eventually(func() corev1.PodSecurityContext {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					return *pod.Spec.SecurityContext
				}
				return corev1.PodSecurityContext{}
			}, testTimeout, testInterval).Should(BeEquivalentTo(corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}))

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(&corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}))
			}
		})
	})

	Context("Humio Cluster Container Security Context", func() {
		It("Should correctly handle container security context", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-containersecuritycontext",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].SecurityContext).To(Equal(containerSecurityContextOrDefault(toCreate)))
			}
			usingClusterBy(key.Name, "Updating Container Security Context to be empty")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ContainerSecurityContext = &corev1.SecurityContext{}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if !reflect.DeepEqual(pod.Spec.Containers[humioIdx].SecurityContext, &corev1.SecurityContext{}) {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].SecurityContext).To(Equal(&corev1.SecurityContext{}))
			}

			usingClusterBy(key.Name, "Updating Container Security Context to be non-empty")
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			Eventually(func() corev1.SecurityContext {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return *pod.Spec.Containers[humioIdx].SecurityContext
				}
				return corev1.SecurityContext{}
			}, testTimeout, testInterval).Should(Equal(corev1.SecurityContext{
				Capabilities: &corev1.Capabilities{
					Add: []corev1.Capability{
						"NET_ADMIN",
					},
				},
			}))

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
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
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].ReadinessProbe).To(Equal(containerReadinessProbeOrDefault(toCreate)))
				Expect(pod.Spec.Containers[humioIdx].LivenessProbe).To(Equal(containerLivenessProbeOrDefault(toCreate)))
				Expect(pod.Spec.Containers[humioIdx].StartupProbe).To(Equal(containerStartupProbeOrDefault(toCreate)))
			}
			usingClusterBy(key.Name, "Updating Container probes to be empty")
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming pods have the updated revision")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			usingClusterBy(key.Name, "Confirming pods do not have a readiness probe set")
			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].ReadinessProbe
				}
				return &corev1.Probe{
					Handler: corev1.Handler{
						Exec: &corev1.ExecAction{Command: []string{"no-pods-found"}},
					},
				}
			}, testTimeout, testInterval).Should(BeNil())

			usingClusterBy(key.Name, "Confirming pods do not have a liveness probe set")
			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].LivenessProbe
				}
				return &corev1.Probe{
					Handler: corev1.Handler{
						Exec: &corev1.ExecAction{Command: []string{"no-pods-found"}},
					},
				}
			}, testTimeout, testInterval).Should(BeNil())

			usingClusterBy(key.Name, "Confirming pods do not have a startup probe set")
			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].StartupProbe
				}
				return &corev1.Probe{
					Handler: corev1.Handler{
						Exec: &corev1.ExecAction{Command: []string{"no-pods-found"}},
					},
				}
			}, testTimeout, testInterval).Should(BeNil())

			usingClusterBy(key.Name, "Updating Container probes to be non-empty")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ContainerReadinessProbe = &corev1.Probe{
					Handler: corev1.Handler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: humioPort},
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
					Handler: corev1.Handler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: humioPort},
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
					Handler: corev1.Handler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: humioPort},
							Scheme: getProbeScheme(&updatedHumioCluster),
						},
					},
					PeriodSeconds:    10,
					TimeoutSeconds:   4,
					SuccessThreshold: 1,
					FailureThreshold: 30,
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			Eventually(func() *corev1.Probe {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].ReadinessProbe
				}
				return &corev1.Probe{}
			}, testTimeout, testInterval).Should(Equal(&corev1.Probe{
				Handler: corev1.Handler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: humioPort},
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].LivenessProbe
				}
				return &corev1.Probe{}
			}, testTimeout, testInterval).Should(Equal(&corev1.Probe{
				Handler: corev1.Handler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: humioPort},
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
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].StartupProbe
				}
				return &corev1.Probe{}
			}, testTimeout, testInterval).Should(Equal(&corev1.Probe{
				Handler: corev1.Handler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/api/v1/config",
						Port:   intstr.IntOrString{IntVal: humioPort},
						Scheme: getProbeScheme(&updatedHumioCluster),
					},
				},
				PeriodSeconds:    10,
				TimeoutSeconds:   4,
				SuccessThreshold: 1,
				FailureThreshold: 30,
			}))

			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].ReadinessProbe).To(Equal(&corev1.Probe{
					Handler: corev1.Handler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: humioPort},
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
					Handler: corev1.Handler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: humioPort},
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
					Handler: corev1.Handler{
						HTTPGet: &corev1.HTTPGetAction{
							Path:   "/api/v1/config",
							Port:   intstr.IntOrString{IntVal: humioPort},
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
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully with extra kafka configs")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "EXTRA_KAFKA_CONFIGS_FILE",
					Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", extraKafkaPropertiesFilename),
				}))
			}

			usingClusterBy(key.Name, "Confirming pods have additional volume mounts for extra kafka configs")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, testInterval).Should(ContainElement(corev1.VolumeMount{
				Name:      "extra-kafka-configs",
				ReadOnly:  true,
				MountPath: "/var/lib/humio/extra-kafka-configs-configmap",
			}))

			usingClusterBy(key.Name, "Confirming pods have additional volumes for extra kafka configs")
			mode := int32(420)
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, testInterval).Should(ContainElement(corev1.Volume{
				Name: "extra-kafka-configs",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: extraKafkaConfigsConfigMapName(toCreate),
						},
						DefaultMode: &mode,
					},
				},
			}))

			usingClusterBy(key.Name, "Confirming config map contains desired extra kafka configs")
			configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, extraKafkaConfigsConfigMapName(toCreate), key.Namespace)
			Expect(configMap.Data[extraKafkaPropertiesFilename]).To(Equal(toCreate.Spec.ExtraKafkaConfigs))

			usingClusterBy(key.Name, "Removing extra kafka configs")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ExtraKafkaConfigs = ""
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming pods do not have environment variable enabling extra kafka configs")
			Eventually(func() []corev1.EnvVar {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, testInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "EXTRA_KAFKA_CONFIGS_FILE",
				Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", extraKafkaPropertiesFilename),
			}))

			usingClusterBy(key.Name, "Confirming pods do not have additional volume mounts for extra kafka configs")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, testInterval).ShouldNot(ContainElement(corev1.VolumeMount{
				Name:      "extra-kafka-configs",
				ReadOnly:  true,
				MountPath: "/var/lib/humio/extra-kafka-configs-configmap",
			}))

			usingClusterBy(key.Name, "Confirming pods do not have additional volumes for extra kafka configs")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, testInterval).ShouldNot(ContainElement(corev1.Volume{
				Name: "extra-kafka-configs",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: extraKafkaConfigsConfigMapName(toCreate),
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
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
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
			usingClusterBy(key.Name, "Creating the cluster successfully with view group permissions")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming config map was created")
			Eventually(func() error {
				_, err := kubernetes.GetConfigMap(ctx, k8sClient, viewGroupPermissionsConfigMapName(toCreate), toCreate.Namespace)
				return err
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming pods have the expected environment variable, volume and volume mounts")
			mode := int32(420)
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
					Value: "true",
				}))
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(ContainElement(corev1.VolumeMount{
					Name:      "view-group-permissions",
					ReadOnly:  true,
					MountPath: fmt.Sprintf("%s/%s", humioDataPath, viewGroupPermissionsFilename),
					SubPath:   viewGroupPermissionsFilename,
				}))
				Expect(pod.Spec.Volumes).To(ContainElement(corev1.Volume{
					Name: "view-group-permissions",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: viewGroupPermissionsConfigMapName(toCreate),
							},
							DefaultMode: &mode,
						},
					},
				}))
			}

			usingClusterBy(key.Name, "Confirming config map contains desired view group permissions")
			configMap, _ := kubernetes.GetConfigMap(ctx, k8sClient, viewGroupPermissionsConfigMapName(toCreate), key.Namespace)
			Expect(configMap.Data[viewGroupPermissionsFilename]).To(Equal(toCreate.Spec.ViewGroupPermissions))

			usingClusterBy(key.Name, "Removing view group permissions")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ViewGroupPermissions = ""
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming pods do not have environment variable enabling view group permissions")
			Eventually(func() []corev1.EnvVar {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, testInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
				Value: "true",
			}))

			usingClusterBy(key.Name, "Confirming pods do not have additional volume mounts for view group permissions")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, testInterval).ShouldNot(ContainElement(corev1.VolumeMount{
				Name:      "view-group-permissions",
				ReadOnly:  true,
				MountPath: fmt.Sprintf("%s/%s", humioDataPath, viewGroupPermissionsFilename),
				SubPath:   viewGroupPermissionsFilename,
			}))

			usingClusterBy(key.Name, "Confirming pods do not have additional volumes for view group permissions")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, testInterval).ShouldNot(ContainElement(corev1.Volume{
				Name: "view-group-permissions",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: viewGroupPermissionsConfigMapName(toCreate),
						},
						DefaultMode: &mode,
					},
				},
			}))

			usingClusterBy(key.Name, "Confirming config map was cleaned up")
			Eventually(func() bool {
				_, err := kubernetes.GetConfigMap(ctx, k8sClient, viewGroupPermissionsConfigMapName(toCreate), toCreate.Namespace)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Persistent Volumes", func() {
		It("Should correctly handle persistent volumes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-pvc",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = helpers.IntPtr(1)

			usingClusterBy(key.Name, "Bootstrapping the cluster successfully without persistent volumes")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Expanding the cluster")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, toCreate)
				if err != nil {
					return err
				}
				toCreate.Spec.NodeCount = helpers.IntPtr(2)
				return k8sClient.Update(ctx, toCreate)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Validating all pods have been created")
			Eventually(func() []corev1.Pod {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(ctx, k8sClient, clusterPods)

				return clusterPods
			}, testTimeout, testInterval).Should(HaveLen(*toCreate.Spec.NodeCount))

			Expect(kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))).To(HaveLen(0))

			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "Updating cluster to use persistent volumes")
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.DataVolumeSource = corev1.VolumeSource{}
				updatedHumioCluster.Spec.DataVolumePersistentVolumeClaimSpecTemplate = corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				}
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}).Should(Succeed())

			Eventually(func() ([]corev1.PersistentVolumeClaim, error) {
				return kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			}, testTimeout, testInterval).Should(HaveLen(*toCreate.Spec.NodeCount))

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			usingClusterBy(key.Name, "Confirming pods are using PVC's and no PVC is left unused")
			pvcList, _ := kubernetes.ListPersistentVolumeClaims(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			foundPodList, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range foundPodList {
				_, err := findPvcForPod(pvcList, pod)
				Expect(err).ShouldNot(HaveOccurred())
			}
			_, err := findNextAvailablePvc(pvcList, foundPodList)
			Expect(err).Should(HaveOccurred())
		})
	})

	Context("Humio Cluster Extra Volumes", func() {
		It("Should correctly handle extra volumes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-extra-volumes",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			initialExpectedVolumesCount := 6
			initialExpectedVolumeMountsCount := 4

			humioVersion, _ := HumioVersionFromCluster(toCreate)
			if ok, _ := humioVersion.AtLeast(HumioVersionWithNewTmpDir); !ok {
				initialExpectedVolumesCount += 1
				initialExpectedVolumeMountsCount += 1
			}

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				// if we run on a real cluster we have TLS enabled (using 2 volumes),
				// and k8s will automatically inject a service account token adding one more
				initialExpectedVolumesCount += 3
				initialExpectedVolumeMountsCount += 2
			}

			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.Volumes).To(HaveLen(initialExpectedVolumesCount))
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(HaveLen(initialExpectedVolumeMountsCount))
			}

			usingClusterBy(key.Name, "Adding additional volumes")
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
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() []corev1.Volume {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, testInterval).Should(HaveLen(initialExpectedVolumesCount + 1))
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, testInterval).Should(HaveLen(initialExpectedVolumeMountsCount + 1))
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.Volumes).Should(ContainElement(extraVolume))
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).Should(ContainElement(extraVolumeMount))
			}
		})
	})

	Context("Humio Cluster Custom Path", func() {
		It("Should correctly handle custom paths with ingress disabled", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-path-ing-disabled",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			protocol := "http"
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				protocol = "https"
			}

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming PUBLIC_URL is set to default value and PROXY_PREFIX_URL is not set")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(envVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal(fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)", protocol)))
				Expect(envVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL")).To(BeFalse())
			}

			usingClusterBy(key.Name, "Updating humio cluster path")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Path = "/logs"
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming PROXY_PREFIX_URL have been configured on all pods")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if !envVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL") {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			usingClusterBy(key.Name, "Confirming PUBLIC_URL and PROXY_PREFIX_URL have been correctly configured")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(envVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal(fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)/logs", protocol)))
				Expect(envVarHasValue(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL", "/logs")).To(BeTrue())
			}

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			usingClusterBy(key.Name, "Confirming cluster returns to Running state")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))

				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})

		It("Should correctly handle custom paths with ingress enabled", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-path-ing-enabled",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Hostname = "test-cluster.humio.com"
			toCreate.Spec.ESHostname = "test-cluster-es.humio.com"
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming PUBLIC_URL is set to default value and PROXY_PREFIX_URL is not set")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(envVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal("https://test-cluster.humio.com"))
				Expect(envVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL")).To(BeFalse())
			}

			usingClusterBy(key.Name, "Updating humio cluster path")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Path = "/logs"
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming PROXY_PREFIX_URL have been configured on all pods")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if !envVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL") {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			usingClusterBy(key.Name, "Confirming PUBLIC_URL and PROXY_PREFIX_URL have been correctly configured")
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(envVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal("https://test-cluster.humio.com/logs"))
				Expect(envVarHasValue(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL", "/logs")).To(BeTrue())
			}

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			usingClusterBy(key.Name, "Confirming cluster returns to Running state")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))

				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})
	})

	Context("Humio Cluster Config Errors", func() {
		It("Creating cluster with conflicting volume mount name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-volmnt-name",
				Namespace: testProcessID,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					ExtraHumioVolumeMounts: []corev1.VolumeMount{
						{
							Name: "humio-data",
						},
					},
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)
			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Creating cluster with conflicting volume mount mount path", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-mount-path",
				Namespace: testProcessID,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					ExtraHumioVolumeMounts: []corev1.VolumeMount{
						{
							Name:      "something-unique",
							MountPath: humioAppPath,
						},
					},
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Creating cluster with conflicting volume name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-vol-name",
				Namespace: testProcessID,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					ExtraVolumes: []corev1.Volume{
						{
							Name: "humio-data",
						},
					},
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Creating cluster with higher replication factor than nodes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-repl-factor",
				Namespace: testProcessID,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					TargetReplicationFactor: 2,
					NodeCount:               helpers.IntPtr(1),
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Creating cluster with conflicting storage configuration", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-conflict-storage-conf",
				Namespace: testProcessID,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					DataVolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
					DataVolumePersistentVolumeClaimSpecTemplate: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							"ReadWriteOnce",
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: *resource.NewQuantity(10*1024*1024*1024, resource.BinarySI),
							},
						},
					},
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Creating cluster with conflicting storage configuration", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-no-storage-conf",
				Namespace: testProcessID,
			}
			toCreate := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			usingClusterBy(key.Name, "should indicate cluster configuration error")
			Eventually(func() string {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
	})

	Context("Humio Cluster Without TLS for Ingress", func() {
		It("Creating cluster without TLS for ingress", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-without-tls-ingress",
				Namespace: testProcessID,
			}
			tlsDisabled := false
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Ingress.Enabled = true
			toCreate.Spec.Ingress.Controller = "nginx"
			toCreate.Spec.Ingress.TLS = &tlsDisabled
			toCreate.Spec.Hostname = "example.humio.com"
			toCreate.Spec.ESHostname = "es-example.humio.com"

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming ingress objects do not have TLS configured")
			var ingresses []networkingv1.Ingress
			Eventually(func() ([]networkingv1.Ingress, error) {
				return kubernetes.ListIngresses(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			}, testTimeout, testInterval).Should(HaveLen(4))

			ingresses, _ = kubernetes.ListIngresses(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			for _, ingress := range ingresses {
				Expect(ingress.Spec.TLS).To(BeNil())
			}
		})
	})

	Context("Humio Cluster Ingress", func() {
		It("Should correctly handle ingress when toggling both ESHostname and Hostname on/off", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-ingress-hostname",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Hostname = ""
			toCreate.Spec.ESHostname = ""
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			usingClusterBy(key.Name, "Creating the cluster successfully without any Hostnames defined")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming we did not create any ingresses")
			var foundIngressList []networkingv1.Ingress
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(0))

			usingClusterBy(key.Name, "Setting the Hostname")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			hostname := "test-cluster.humio.com"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Hostname = hostname
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming we only created ingresses with expected hostname")
			foundIngressList = []networkingv1.Ingress{}
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(3))
			foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					Expect(rule.Host).To(Equal(updatedHumioCluster.Spec.Hostname))
				}
			}

			usingClusterBy(key.Name, "Setting the ESHostname")
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			esHostname := "test-cluster-es.humio.com"
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ESHostname = esHostname
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming ingresses for ES Hostname gets created")
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(4))

			var ingressHostnames []string
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					ingressHostnames = append(ingressHostnames, rule.Host)
				}
			}
			Expect(ingressHostnames).To(ContainElement(esHostname))

			usingClusterBy(key.Name, "Removing the ESHostname")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ESHostname = ""
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming ingresses for ES Hostname gets removed")
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(3))

			ingressHostnames = []string{}
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					ingressHostnames = append(ingressHostnames, rule.Host)
				}
			}
			Expect(ingressHostnames).ToNot(ContainElement(esHostname))

			usingClusterBy(key.Name, "Creating the hostname secret")
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

			usingClusterBy(key.Name, "Setting the HostnameSource")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.Hostname = ""
				updatedHumioCluster.Spec.HostnameSource.SecretKeyRef = secretKeyRef
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming we only created ingresses with expected hostname")
			foundIngressList = []networkingv1.Ingress{}
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(3))
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
			}, testTimeout, testInterval).Should(Equal(updatedHostname))

			usingClusterBy(key.Name, "Removing the HostnameSource")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HostnameSource.SecretKeyRef = nil
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Deleting the hostname secret")
			Expect(k8sClient.Delete(ctx, &hostnameSecret)).To(Succeed())

			usingClusterBy(key.Name, "Creating the es hostname secret")
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

			usingClusterBy(key.Name, "Setting the ESHostnameSource")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ESHostname = ""
				updatedHumioCluster.Spec.ESHostnameSource.SecretKeyRef = secretKeyRef
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming we only created ingresses with expected es hostname")
			foundIngressList = []networkingv1.Ingress{}
			Eventually(func() []networkingv1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(1))
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
			}, testTimeout, testInterval).Should(Equal(updatedESHostname))

			usingClusterBy(key.Name, "Removing the ESHostnameSource")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				updatedHumioCluster.Spec.ESHostnameSource.SecretKeyRef = nil
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Deleting the es hostname secret")
			Expect(k8sClient.Delete(ctx, &esHostnameSecret)).To(Succeed())
		})
	})

	Context("Humio Cluster with non-existent custom service accounts", func() {
		It("Should correctly handle non-existent humio service account by marking cluster as ConfigError", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-humio-service-account",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceAccountName = "non-existent-humio-service-account"

			usingClusterBy(key.Name, "Creating cluster with non-existent service accounts")
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			Eventually(func() string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &cluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return cluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Should correctly handle non-existent init service account by marking cluster as ConfigError", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-init-service-account",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceAccountName = "non-existent-init-service-account"

			usingClusterBy(key.Name, "Creating cluster with non-existent service accounts")
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			Eventually(func() string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &cluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return cluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateConfigError))
		})
		It("Should correctly handle non-existent auth service account by marking cluster as ConfigError", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-auth-service-account",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceAccountName = "non-existent-auth-service-account"

			usingClusterBy(key.Name, "Creating cluster with non-existent service accounts")
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			Eventually(func() string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &cluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return cluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateConfigError))
		})
	})

	Context("Humio Cluster With Custom Service Accounts", func() {
		It("Creating cluster with custom service accounts", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-service-accounts",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.InitServiceAccountName = "init-custom-service-account"
			toCreate.Spec.AuthServiceAccountName = "auth-custom-service-account"
			toCreate.Spec.HumioServiceAccountName = "humio-custom-service-account"

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming init container is using the correct service account")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetInitContainerIndexByName(pod, initContainerName)
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
						Expect(secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]).To(Equal(toCreate.Spec.InitServiceAccountName))
					}
				}
			}
			usingClusterBy(key.Name, "Confirming auth container is using the correct service account")
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, authContainerName)
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
						Expect(secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]).To(Equal(toCreate.Spec.AuthServiceAccountName))
					}
				}
			}
			usingClusterBy(key.Name, "Confirming humio pod is using the correct service account")
			for _, pod := range clusterPods {
				Expect(pod.Spec.ServiceAccountName).To(Equal(toCreate.Spec.HumioServiceAccountName))
			}
		})

		It("Creating cluster with custom service accounts sharing the same name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-sa-same-name",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.InitServiceAccountName = "custom-service-account"
			toCreate.Spec.AuthServiceAccountName = "custom-service-account"
			toCreate.Spec.HumioServiceAccountName = "custom-service-account"

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming init container is using the correct service account")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetInitContainerIndexByName(pod, initContainerName)
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
						Expect(secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]).To(Equal(toCreate.Spec.InitServiceAccountName))
					}
				}
			}
			usingClusterBy(key.Name, "Confirming auth container is using the correct service account")
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, authContainerName)
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
						Expect(secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]).To(Equal(toCreate.Spec.AuthServiceAccountName))
					}
				}
			}
			usingClusterBy(key.Name, "Confirming humio pod is using the correct service account")
			for _, pod := range clusterPods {
				Expect(pod.Spec.ServiceAccountName).To(Equal(toCreate.Spec.HumioServiceAccountName))
			}
		})
	})

	Context("Humio Cluster With Service Annotations", func() {
		It("Creating cluster with custom service annotations", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-svc-annotations",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceAnnotations = map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-type":                              "nlb",
				"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": "false",
				"service.beta.kubernetes.io/aws-load-balancer-ssl-cert":                          "arn:aws:acm:region:account:certificate/123456789012-1234-1234-1234-12345678",
				"service.beta.kubernetes.io/aws-load-balancer-backend-protocol":                  "ssl",
				"service.beta.kubernetes.io/aws-load-balancer-ssl-ports":                         "443",
				"service.beta.kubernetes.io/aws-load-balancer-internal":                          "0.0.0.0/0",
			}

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming service was created using the correct annotations")
			svc, err := kubernetes.GetService(ctx, k8sClient, toCreate.Name, toCreate.Namespace)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range toCreate.Spec.HumioServiceAnnotations {
				Expect(svc.Annotations).To(HaveKeyWithValue(k, v))
			}
		})
	})

	Context("Humio Cluster With Custom Tolerations", func() {
		It("Creating cluster with custom tolerations", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-tolerations",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.Tolerations = []corev1.Toleration{
				{
					Key:      "key",
					Operator: "Equal",
					Value:    "value",
					Effect:   "NoSchedule",
				},
			}

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming the humio pods use the requested tolerations")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.Tolerations).To(ContainElement(toCreate.Spec.Tolerations[0]))
			}
		})
	})

	Context("Humio Cluster With Service Labels", func() {
		It("Creating cluster with custom service labels", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-svc-labels",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.HumioServiceLabels = map[string]string{
				"mirror.linkerd.io/exported": "true",
			}

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming service was created using the correct annotations")
			svc, err := kubernetes.GetService(ctx, k8sClient, toCreate.Name, toCreate.Namespace)
			Expect(err).ToNot(HaveOccurred())
			for k, v := range toCreate.Spec.HumioServiceLabels {
				Expect(svc.Labels).To(HaveKeyWithValue(k, v))
			}
		})
	})

	Context("Humio Cluster with shared process namespace and sidecars", func() {
		It("Creating cluster without shared process namespace and sidecar", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-sidecars",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.SidecarContainers = nil

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming the humio pods are not using shared process namespace nor additional sidecars")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				if pod.Spec.ShareProcessNamespace != nil {
					Expect(*pod.Spec.ShareProcessNamespace).To(BeFalse())
				}
				Expect(pod.Spec.Containers).Should(HaveLen(2))
			}

			usingClusterBy(key.Name, "Enabling shared process namespace and sidecars")
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
						Image:   image,
						Command: []string{"/bin/sh"},
						Args:    []string{"-c", "HUMIO_PID=$(ps -e | grep java | awk '{print $1'}); while :; do sleep 30 ; jmap -histo:live $HUMIO_PID | head -n203 ; done"},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "tmp",
								MountPath: tmpPath,
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming the humio pods use shared process namespace")
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					if pod.Spec.ShareProcessNamespace != nil {
						return *pod.Spec.ShareProcessNamespace
					}
				}
				return false
			}, testTimeout, testInterval).Should(BeTrue())

			usingClusterBy(key.Name, "Confirming pods contain the new sidecar")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					for _, container := range pod.Spec.Containers {
						if container.Name == humioContainerName {
							continue
						}
						if container.Name == authContainerName {
							continue
						}
						return container.Name
					}
				}
				return ""
			}, testTimeout, testInterval).Should(Equal("jmap"))
		})
	})

	Context("Humio Cluster pod termination grace period", func() {
		It("Should validate default configuration", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-grace-default",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.TerminationGracePeriodSeconds = nil

			usingClusterBy(key.Name, "Creating Humio cluster without a termination grace period set")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Validating pod is created with the default grace period")
			Eventually(func() int64 {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(ctx, k8sClient, clusterPods)

				for _, pod := range clusterPods {
					if pod.Spec.TerminationGracePeriodSeconds != nil {
						return *pod.Spec.TerminationGracePeriodSeconds
					}
				}
				return 0
			}, testTimeout, testInterval).Should(BeEquivalentTo(300))

			usingClusterBy(key.Name, "Overriding termination grace period")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.TerminationGracePeriodSeconds = helpers.Int64Ptr(120)
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Validating pod is recreated using the explicitly defined grace period")
			Eventually(func() int64 {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					if pod.Spec.TerminationGracePeriodSeconds != nil {
						return *pod.Spec.TerminationGracePeriodSeconds
					}
				}
				return 0
			}, testTimeout, testInterval).Should(BeEquivalentTo(120))
		})
	})

	Context("Humio Cluster install license", func() {
		It("Should fail when no license is present", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-no-license",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, false)
			toCreate.Spec.License = humiov1alpha1.HumioClusterLicenseSpec{}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())
			defer cleanupCluster(ctx, toCreate)

			Eventually(func() string {
				var cluster humiov1alpha1.HumioCluster
				err := k8sClient.Get(ctx, key, &cluster)
				if err != nil && !errors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				return cluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo("ConfigError"))

			// TODO: set a valid license
			// TODO: confirm cluster enters running
		})
		It("Should successfully install a license", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-license",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully with a license secret")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			secretName := fmt.Sprintf("%s-license", key.Name)
			secretKey := "license"
			var updatedHumioCluster humiov1alpha1.HumioCluster

			usingClusterBy(key.Name, "Updating the HumioCluster to add broken reference to license")
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Should indicate cluster configuration error due to missing license secret")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			usingClusterBy(key.Name, "Updating the HumioCluster to add a valid license")
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Should indicate cluster is no longer in a configuration error state")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			usingClusterBy(key.Name, "Ensuring the license is updated")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.LicenseStatus.Type
			}, testTimeout, testInterval).Should(BeIdenticalTo("onprem"))

			usingClusterBy(key.Name, "Updating the license secret to remove the key")
			var licenseSecret corev1.Secret
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Namespace: key.Namespace,
					Name:      secretName,
				}, &licenseSecret)
			}, testTimeout, testInterval).Should(Succeed())

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

			usingClusterBy(key.Name, "Should indicate cluster configuration error due to missing license secret key")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))
		})
	})

	Context("Humio Cluster state adjustment", func() {
		It("Should successfully set proper state", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-state",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Ensuring the state is Running")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			usingClusterBy(key.Name, "Updating the HumioCluster to ConfigError state")
			Eventually(func() error {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				err := k8sClient.Get(ctx, key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Status.State = humiov1alpha1.HumioClusterStateConfigError
				return k8sClient.Status().Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Should indicate healthy cluster resets state to Running")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})
	})

	Context("Humio Cluster with envSource configmap", func() {
		It("Creating cluster with envSource configmap", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-env-source-configmap",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming the humio pods are not using env var source")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], humioContainerName)
			Expect(err).ToNot(HaveOccurred())
			Expect(clusterPods[0].Spec.Containers[humioIdx].EnvFrom).To(BeNil())

			usingClusterBy(key.Name, "Adding missing envVarSource to pod spec")
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming the HumioCluster goes into ConfigError state since the configmap does not exist")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			usingClusterBy(key.Name, "Creating the envVarSource configmap")
			envVarSourceConfigMap := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "env-var-source",
					Namespace: key.Namespace,
				},
				Data: map[string]string{"SOME_ENV_VAR": "SOME_ENV_VALUE"},
			}
			Expect(k8sClient.Create(ctx, &envVarSourceConfigMap)).To(Succeed())

			waitForReconcileToSync(ctx, key, k8sClient, nil)

			usingClusterBy(key.Name, "Updating envVarSource of pod spec")
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			usingClusterBy(key.Name, "Confirming pods contain the new env vars")
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				var podsContainingEnvFrom int
				for _, pod := range clusterPods {
					humioIdx, err := kubernetes.GetContainerIndexByName(pod, humioContainerName)
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
			}, testTimeout, testInterval).Should(Equal(*toCreate.Spec.NodeCount))
		})
	})

	Context("Humio Cluster with envSource secret", func() {
		It("Creating cluster with envSource secret", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-env-source-secret",
				Namespace: testProcessID,
			}
			toCreate := constructBasicSingleNodeHumioCluster(key, true)

			usingClusterBy(key.Name, "Creating the cluster successfully")
			ctx := context.Background()
			createAndBootstrapCluster(ctx, toCreate, true)
			defer cleanupCluster(ctx, toCreate)

			usingClusterBy(key.Name, "Confirming the humio pods are not using env var source")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], humioContainerName)
			Expect(err).ToNot(HaveOccurred())
			Expect(clusterPods[0].Spec.Containers[humioIdx].EnvFrom).To(BeNil())

			usingClusterBy(key.Name, "Adding missing envVarSource to pod spec")
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
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Confirming the HumioCluster goes into ConfigError state since the secret does not exist")
			Eventually(func() string {
				updatedHumioCluster = humiov1alpha1.HumioCluster{}
				Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			usingClusterBy(key.Name, "Creating the envVarSource secret")
			envVarSourceSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "env-var-source",
					Namespace: key.Namespace,
				},
				StringData: map[string]string{"SOME_ENV_VAR": "SOME_ENV_VALUE"},
			}
			Expect(k8sClient.Create(ctx, &envVarSourceSecret)).To(Succeed())

			waitForReconcileToSync(ctx, key, k8sClient, nil)

			usingClusterBy(key.Name, "Updating envVarSource of pod spec")
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
				updatedHumioCluster.GetGeneration()
				return k8sClient.Update(ctx, &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			usingClusterBy(key.Name, "Restarting the cluster in a rolling fashion")
			ensurePodsRollingRestart(ctx, &updatedHumioCluster, key, 2)

			usingClusterBy(key.Name, "Confirming pods contain the new env vars")
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				var podsContainingEnvFrom int
				for _, pod := range clusterPods {
					humioIdx, err := kubernetes.GetContainerIndexByName(pod, humioContainerName)
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
			}, testTimeout, testInterval).Should(Equal(*toCreate.Spec.NodeCount))
		})
	})
})

func createAndBootstrapCluster(ctx context.Context, cluster *humiov1alpha1.HumioCluster, autoCreateLicense bool) {
	key := types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      cluster.Name,
	}

	if autoCreateLicense {
		usingClusterBy(cluster.Name, fmt.Sprintf("Creating the license secret %s", cluster.Spec.License.SecretKeyRef.Name))

		licenseSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-license", key.Name),
				Namespace: key.Namespace,
			},
			StringData: map[string]string{"license": os.Getenv("HUMIO_E2E_LICENSE")},
			Type:       corev1.SecretTypeOpaque,
		}
		Expect(k8sClient.Create(ctx, &licenseSecret)).To(Succeed())
	}

	if cluster.Spec.HumioServiceAccountName != "" {
		usingClusterBy(key.Name, "Creating service account for humio container")
		humioServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.HumioServiceAccountName, cluster.Name, cluster.Namespace, map[string]string{})
		Expect(k8sClient.Create(ctx, humioServiceAccount)).To(Succeed())
	}

	if !cluster.Spec.DisableInitContainer {
		if cluster.Spec.InitServiceAccountName != "" {
			if cluster.Spec.InitServiceAccountName != cluster.Spec.HumioServiceAccountName {
				usingClusterBy(key.Name, "Creating service account for init container")
				initServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.InitServiceAccountName, cluster.Name, cluster.Namespace, map[string]string{})
				Expect(k8sClient.Create(ctx, initServiceAccount)).To(Succeed())
			}

			usingClusterBy(key.Name, "Creating cluster role for init container")
			initClusterRole := kubernetes.ConstructInitClusterRole(cluster.Spec.InitServiceAccountName, key.Name)
			Expect(k8sClient.Create(ctx, initClusterRole)).To(Succeed())

			usingClusterBy(key.Name, "Creating cluster role binding for init container")
			initClusterRoleBinding := kubernetes.ConstructClusterRoleBinding(cluster.Spec.InitServiceAccountName, initClusterRole.Name, key.Name, key.Namespace, cluster.Spec.InitServiceAccountName)
			Expect(k8sClient.Create(ctx, initClusterRoleBinding)).To(Succeed())
		}
	}

	if cluster.Spec.AuthServiceAccountName != "" {
		if cluster.Spec.AuthServiceAccountName != cluster.Spec.HumioServiceAccountName {
			usingClusterBy(key.Name, "Creating service account for auth container")
			authServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.AuthServiceAccountName, cluster.Name, cluster.Namespace, map[string]string{})
			Expect(k8sClient.Create(ctx, authServiceAccount)).To(Succeed())
		}

		usingClusterBy(key.Name, "Creating role for auth container")
		authRole := kubernetes.ConstructAuthRole(cluster.Spec.AuthServiceAccountName, key.Name, key.Namespace)
		Expect(k8sClient.Create(ctx, authRole)).To(Succeed())

		usingClusterBy(key.Name, "Creating role binding for auth container")
		authRoleBinding := kubernetes.ConstructRoleBinding(cluster.Spec.AuthServiceAccountName, authRole.Name, key.Name, key.Namespace, cluster.Spec.AuthServiceAccountName)
		Expect(k8sClient.Create(ctx, authRoleBinding)).To(Succeed())
	}

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
		// Simulate sidecar creating the secret which contains the admin token use to authenticate with humio
		secretData := map[string][]byte{"token": []byte("")}
		adminTokenSecretName := fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix)
		usingClusterBy(key.Name, "Simulating the auth container creating the secret containing the API token")
		desiredSecret := kubernetes.ConstructSecret(key.Name, key.Namespace, adminTokenSecretName, secretData, nil)
		Expect(k8sClient.Create(ctx, desiredSecret)).To(Succeed())
	}

	usingClusterBy(key.Name, "Creating HumioCluster resource")
	Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())

	usingClusterBy(key.Name, "Confirming cluster enters running state")
	var updatedHumioCluster humiov1alpha1.HumioCluster
	Eventually(func() string {
		err := k8sClient.Get(ctx, key, &updatedHumioCluster)
		if err != nil && !errors.IsNotFound(err) {
			Expect(err).Should(Succeed())
		}
		return updatedHumioCluster.Status.State
	}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

	usingClusterBy(key.Name, "Waiting to have the correct number of pods")
	var clusterPods []corev1.Pod
	Eventually(func() []corev1.Pod {
		clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
		markPodsAsRunning(ctx, k8sClient, clusterPods)
		return clusterPods
	}, testTimeout, testInterval).Should(HaveLen(*cluster.Spec.NodeCount))

	humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], humioContainerName)
	Expect(err).ToNot(HaveOccurred())
	humioContainerArgs := strings.Join(clusterPods[0].Spec.Containers[humioIdx].Args, " ")
	if cluster.Spec.DisableInitContainer {
		usingClusterBy(key.Name, "Confirming pods do not use init container")
		Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(0))
		Expect(humioContainerArgs).ToNot(ContainSubstring("export ZONE="))
	} else {
		usingClusterBy(key.Name, "Confirming pods have an init container")
		Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(1))
		Expect(humioContainerArgs).To(ContainSubstring("export ZONE="))
	}

	usingClusterBy(key.Name, "Confirming cluster enters running state")
	Eventually(func() string {
		clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
		markPodsAsRunning(ctx, k8sClient, clusterPods)

		updatedHumioCluster = humiov1alpha1.HumioCluster{}
		Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
		return updatedHumioCluster.Status.State
	}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

	usingClusterBy(key.Name, "Validating cluster has expected pod revision annotation")
	Eventually(func() string {
		updatedHumioCluster = humiov1alpha1.HumioCluster{}
		Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
		val, _ := updatedHumioCluster.Annotations[podRevisionAnnotation]
		return val
	}, testTimeout, testInterval).Should(Equal("1"))

	usingClusterBy(key.Name, "Waiting for the auth sidecar to populate the secret containing the API token")
	Eventually(func() error {
		return k8sClient.Get(ctx, types.NamespacedName{
			Namespace: key.Namespace,
			Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix),
		}, &corev1.Secret{})
	}, testTimeout, testInterval).Should(Succeed())

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		usingClusterBy(key.Name, "Validating API token was obtained using the API method")
		var apiTokenSecret corev1.Secret
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Namespace: key.Namespace,
				Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix),
			}, &apiTokenSecret)
		}, testTimeout, testInterval).Should(Succeed())
		Expect(apiTokenSecret.Annotations).Should(HaveKeyWithValue(apiTokenMethodAnnotationName, apiTokenMethodFromAPI))
	}

	clusterConfig, err := helpers.NewCluster(ctx, k8sClient, key.Name, "", key.Namespace, helpers.UseCertManager(), true)
	Expect(err).To(BeNil())
	Expect(clusterConfig).ToNot(BeNil())
	Expect(clusterConfig.Config()).ToNot(BeNil())

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		usingClusterBy(key.Name, "Validating cluster nodes have ZONE configured correctly")
		if updatedHumioCluster.Spec.DisableInitContainer == true {
			Eventually(func() []string {
				cluster, err := humioClientForTestSuite.GetClusters(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
				if err != nil {
					return []string{"got err"}
				}
				if len(cluster.Nodes) < 1 {
					return []string{}
				}
				keys := make(map[string]bool)
				var zoneList []string
				for _, node := range cluster.Nodes {
					if _, value := keys[node.Zone]; !value {
						if node.Zone != "" {
							keys[node.Zone] = true
							zoneList = append(zoneList, node.Zone)
						}
					}
				}
				return zoneList
			}, testTimeout, testInterval).Should(BeEmpty())
		} else {
			Eventually(func() []string {
				cluster, err := humioClientForTestSuite.GetClusters(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
				if err != nil || len(cluster.Nodes) < 1 {
					return []string{}
				}
				keys := make(map[string]bool)
				var zoneList []string
				for _, node := range cluster.Nodes {
					if _, value := keys[node.Zone]; !value {
						if node.Zone != "" {
							keys[node.Zone] = true
							zoneList = append(zoneList, node.Zone)
						}
					}
				}
				return zoneList
			}, testTimeout, testInterval).ShouldNot(BeEmpty())
		}
	}

	usingClusterBy(key.Name, "Confirming replication factor environment variables are set correctly")
	for _, pod := range clusterPods {
		humioIdx, err = kubernetes.GetContainerIndexByName(pod, "humio")
		Expect(err).ToNot(HaveOccurred())
		Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElements([]corev1.EnvVar{
			{
				Name:  "DIGEST_REPLICATION_FACTOR",
				Value: strconv.Itoa(cluster.Spec.TargetReplicationFactor),
			},
			{
				Name:  "STORAGE_REPLICATION_FACTOR",
				Value: strconv.Itoa(cluster.Spec.TargetReplicationFactor),
			},
		}))
	}

	waitForReconcileToSync(ctx, key, k8sClient, nil)
}

func waitForReconcileToSync(ctx context.Context, key types.NamespacedName, k8sClient client.Client, currentHumioCluster *humiov1alpha1.HumioCluster) {
	usingClusterBy(key.Name, "Waiting for the reconcile loop to complete")
	if currentHumioCluster == nil {
		var updatedHumioCluster humiov1alpha1.HumioCluster
		Eventually(func() error {
			return k8sClient.Get(ctx, key, &updatedHumioCluster)
		}, testTimeout, testInterval).Should(Succeed())
		currentHumioCluster = &updatedHumioCluster
	}

	beforeGeneration := currentHumioCluster.GetGeneration()
	Eventually(func() int64 {
		err := k8sClient.Get(ctx, key, currentHumioCluster)
		if err != nil {
			return -1
		}
		observedGen, err := strconv.Atoi(currentHumioCluster.Status.ObservedGeneration)
		if err != nil {
			return -2
		}
		return int64(observedGen)
	}, testTimeout, testInterval).Should(BeNumerically(">=", beforeGeneration))
}

func waitForReconcileToRun(ctx context.Context, key types.NamespacedName, k8sClient client.Client, humioClusterBeforeUpdate humiov1alpha1.HumioCluster) {
	usingClusterBy(key.Name, "Waiting for the next reconcile loop to run")
	beforeGeneration := humioClusterBeforeUpdate.GetGeneration()

	Eventually(func() int64 {
		err := k8sClient.Get(ctx, key, &humioClusterBeforeUpdate)
		if err != nil {
			return -1
		}
		observedGen, err := strconv.Atoi(humioClusterBeforeUpdate.Status.ObservedGeneration)
		if err != nil {
			return -2
		}
		return int64(observedGen)
	}, testTimeout, testInterval).Should(BeNumerically(">", beforeGeneration))
}

func constructBasicSingleNodeHumioCluster(key types.NamespacedName, useAutoCreatedLicense bool) *humiov1alpha1.HumioCluster {
	humioCluster := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			Image:                   image,
			ExtraKafkaConfigs:       "security.protocol=PLAINTEXT",
			NodeCount:               helpers.IntPtr(1),
			TargetReplicationFactor: 1,
			EnvironmentVariables: []corev1.EnvVar{
				{
					Name:  "HUMIO_JVM_ARGS",
					Value: "-Xss2m -Xms256m -Xmx1536m -server -XX:+UseParallelOldGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC -Dzookeeper.client.secure=false",
				},
				{
					Name:  "ZOOKEEPER_URL",
					Value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless.default:2181",
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
					Value: "single-user",
				},
				{
					Name:  "SINGLE_USER_PASSWORD",
					Value: "password",
				},
			},
			DataVolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	if useAutoCreatedLicense {
		humioCluster.Spec.License = humiov1alpha1.HumioClusterLicenseSpec{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-license", key.Name),
				},
				Key: "license",
			},
		}
	}
	return humioCluster
}

func markPodsAsRunning(ctx context.Context, client client.Client, pods []corev1.Pod) error {
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		return nil
	}

	usingClusterBy("", "Simulating Humio container starts up and is marked Ready")
	for nodeID, pod := range pods {
		markPodAsRunning(ctx, client, nodeID, pod)
	}
	return nil
}

func markPodAsRunning(ctx context.Context, client client.Client, nodeID int, pod corev1.Pod) error {
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		return nil
	}

	usingClusterBy("", fmt.Sprintf("Simulating Humio container starts up and is marked Ready (container %d)", nodeID))
	pod.Status.PodIP = fmt.Sprintf("192.168.0.%d", nodeID)
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		},
	}
	if err := client.Status().Update(ctx, &pod); err != nil {
		return fmt.Errorf("failed to mark pod as ready: %s", err)
	}
	return nil
}

func podReadyCount(ctx context.Context, key types.NamespacedName, expectedPodRevision int, expectedReadyCount int) int {
	var readyCount int
	expectedPodRevisionStr := strconv.Itoa(expectedPodRevision)
	clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
	for nodeID, pod := range clusterPods {
		if pod.Annotations[podRevisionAnnotation] == expectedPodRevisionStr {
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				if pod.DeletionTimestamp == nil {
					for _, condition := range pod.Status.Conditions {
						if condition.Type == corev1.PodReady {
							if condition.Status == corev1.ConditionTrue {
								readyCount++
							}
						}
					}
				}
			} else {
				if nodeID+1 <= expectedReadyCount {
					markPodAsRunning(ctx, k8sClient, nodeID, pod)
					readyCount++
					continue
				}
			}
		}
	}
	return readyCount
}

func ensurePodsRollingRestart(ctx context.Context, hc *humiov1alpha1.HumioCluster, key types.NamespacedName, expectedPodRevision int) {
	usingClusterBy(hc.Name, "Ensuring replacement pods are ready one at a time")
	for expectedReadyCount := 1; expectedReadyCount < *hc.Spec.NodeCount+1; expectedReadyCount++ {
		Eventually(func() int {
			return podReadyCount(ctx, key, expectedPodRevision, expectedReadyCount)
		}, testTimeout, testInterval).Should(BeIdenticalTo(expectedReadyCount))
	}
}

func ensurePodsTerminate(ctx context.Context, key types.NamespacedName, expectedPodRevision int) {
	usingClusterBy(key.Name, "Ensuring all existing pods are terminated at the same time")
	Eventually(func() int {
		return podReadyCount(ctx, key, expectedPodRevision-1, 0)
	}, testTimeout, testInterval).Should(BeIdenticalTo(0))

	usingClusterBy(key.Name, "Ensuring replacement pods are not ready at the same time")
	Eventually(func() int {
		return podReadyCount(ctx, key, expectedPodRevision, 0)
	}, testTimeout, testInterval).Should(BeIdenticalTo(0))

}

func ensurePodsSimultaneousRestart(ctx context.Context, hc *humiov1alpha1.HumioCluster, key types.NamespacedName, expectedPodRevision int) {
	ensurePodsTerminate(ctx, key, expectedPodRevision)

	usingClusterBy(hc.Name, "Ensuring all pods come back up after terminating")
	Eventually(func() int {
		return podReadyCount(ctx, key, expectedPodRevision, expectedPodRevision)
	}, testTimeout, testInterval).Should(BeIdenticalTo(*hc.Spec.NodeCount))
}

func podNames(pods []corev1.Pod) []string {
	var podNamesList []string
	for _, pod := range pods {
		if pod.Name != "" {
			podNamesList = append(podNamesList, pod.Name)
		}
	}
	sort.Strings(podNamesList)
	return podNamesList
}

func cleanupCluster(ctx context.Context, hc *humiov1alpha1.HumioCluster) {
	var cluster humiov1alpha1.HumioCluster
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: hc.Name, Namespace: hc.Namespace}, &cluster)).To(Succeed())
	usingClusterBy(cluster.Name, "Cleaning up any user-defined service account we've created")
	if cluster.Spec.HumioServiceAccountName != "" {
		serviceAccount, err := kubernetes.GetServiceAccount(ctx, k8sClient, cluster.Spec.HumioServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, serviceAccount)).To(Succeed())
		}
	}
	if cluster.Spec.InitServiceAccountName != "" {
		clusterRoleBinding, err := kubernetes.GetClusterRoleBinding(ctx, k8sClient, cluster.Spec.InitServiceAccountName)
		if err == nil {
			Expect(k8sClient.Delete(ctx, clusterRoleBinding)).To(Succeed())
		}

		clusterRole, err := kubernetes.GetClusterRole(ctx, k8sClient, cluster.Spec.InitServiceAccountName)
		if err == nil {
			Expect(k8sClient.Delete(ctx, clusterRole)).To(Succeed())
		}

		serviceAccount, err := kubernetes.GetServiceAccount(ctx, k8sClient, cluster.Spec.InitServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, serviceAccount)).To(Succeed())
		}
	}
	if cluster.Spec.AuthServiceAccountName != "" {
		roleBinding, err := kubernetes.GetRoleBinding(ctx, k8sClient, cluster.Spec.AuthServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, roleBinding)).To(Succeed())
		}

		role, err := kubernetes.GetRole(ctx, k8sClient, cluster.Spec.AuthServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, role)).To(Succeed())
		}

		serviceAccount, err := kubernetes.GetServiceAccount(ctx, k8sClient, cluster.Spec.AuthServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, serviceAccount)).To(Succeed())
		}
	}

	usingClusterBy(cluster.Name, "Cleaning up any secrets for the cluster")
	var allSecrets corev1.SecretList
	Expect(k8sClient.List(ctx, &allSecrets)).To(Succeed())
	for _, secret := range allSecrets.Items {
		if secret.Type == corev1.SecretTypeServiceAccountToken {
			// Secrets holding service account tokens are automatically GC'ed when the ServiceAccount goes away.
			continue
		}
		// Only consider secrets not already being marked for deletion
		if secret.DeletionTimestamp == nil {
			if secret.Name == cluster.Name ||
				secret.Name == fmt.Sprintf("%s-admin-token", cluster.Name) ||
				strings.HasPrefix(secret.Name, fmt.Sprintf("%s-core-", cluster.Name)) {
				// This includes the following objects which do not have an ownerReference pointing to the HumioCluster, so they will not automatically be cleaned up:
				// - <CLUSTER_NAME>: Holds the CA bundle for the TLS certificates, created by cert-manager because of a Certificate object and uses secret type kubernetes.io/tls.
				// - <CLUSTER_NAME>-admin-token: Holds the API token for the Humio API, created by the auth sidecar and uses secret type "Opaque".
				// - <CLUSTER_NAME>-core-XXXXXX: Holds the node-specific TLS certificate in a JKS bundle, created by cert-manager because of a Certificate object and uses secret type kubernetes.io/tls.

				usingClusterBy(cluster.Name, fmt.Sprintf("Cleaning up secret %s", secret.Name))
				_ = k8sClient.Delete(ctx, &secret)
			}
		}
	}

	usingClusterBy(cluster.Name, "Deleting the cluster")
	Expect(k8sClient.Delete(ctx, &cluster)).To(Succeed())

	if cluster.Spec.License.SecretKeyRef != nil {
		usingClusterBy(cluster.Name, fmt.Sprintf("Deleting the license secret %s", cluster.Spec.License.SecretKeyRef.Name))
		_ = k8sClient.Delete(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Spec.License.SecretKeyRef.Name,
				Namespace: cluster.Namespace,
			},
		})
	}
}
