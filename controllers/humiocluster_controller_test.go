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
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/networking/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const autoCleanupAfterTestAnnotationName = "humio.com/auto-cleanup-after-test"

// TODO: refactor, this is copied from humio/humio-operator/images/helper/main.go
const (
	// apiTokenMethodAnnotationName is used to signal what mechanism was used to obtain the API token
	apiTokenMethodAnnotationName = "humio.com/api-token-method"
	// apiTokenMethodFromAPI is used to indicate that the API token was obtained using an API call
	apiTokenMethodFromAPI = "api"
	// apiTokenMethodFromFile is used to indicate that the API token was obtained using the global snapshot file
	apiTokenMethodFromFile = "file"
)

var _ = Describe("HumioCluster Controller", func() {

	BeforeEach(func() {
		// failed test runs that don't clean up leave resources behind.

	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
		var existingClusters humiov1alpha1.HumioClusterList
		k8sClient.List(context.Background(), &existingClusters)
		for _, cluster := range existingClusters.Items {
			if _, ok := cluster.Annotations[autoCleanupAfterTestAnnotationName]; ok {
				k8sClient.Delete(context.Background(), &cluster)
			}
		}
	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Humio Cluster Simple", func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-simple",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.NodeCount = helpers.IntPtr(2)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)
		})
	})

	Context("Humio Cluster Without Init Container", func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-no-init-container",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.DisableInitContainer = true

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)
		})
	})

	Context("Humio Cluster Multi Organizations", func() {
		It("Should bootstrap cluster correctly", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-multi-org",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{
				Name:  "ENABLE_ORGANIZATIONS",
				Value: "true",
			})
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{
				Name:  "ORGANIZATION_MODE",
				Value: "multi",
			})

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)
		})
	})

	Context("Humio Cluster Update Image", func() {
		It("Update should correctly replace pods to use new image", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-image",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.Image = "humio/humio-core:1.14.5"
			toCreate.Spec.NodeCount = helpers.IntPtr(2)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(toCreate.Spec.Image))
				Expect(pod.Annotations[podRevisionAnnotation]).To(Equal("1"))
			}
			k8sClient.Get(context.Background(), key, &updatedHumioCluster)
			Expect(updatedHumioCluster.Annotations[podRevisionAnnotation]).To(Equal("1"))

			By("Updating the cluster image successfully")
			updatedImage := image
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.Image = updatedImage
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() string {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateUpgrading))

			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			By("Confirming pod revision is the same for all pods and the cluster itself")
			k8sClient.Get(context.Background(), key, &updatedHumioCluster)
			Expect(updatedHumioCluster.Annotations[podRevisionAnnotation]).To(Equal("2"))

			clusterPods, _ = kubernetes.ListPods(k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
			Expect(clusterPods).To(HaveLen(*toCreate.Spec.NodeCount))
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Image).To(BeIdenticalTo(updatedImage))
				Expect(pod.Annotations[podRevisionAnnotation]).To(Equal("2"))
			}
		})
	})

	Context("Humio Cluster Update Helper Image", func() {
		It("Update should correctly replace pods to use new image", func() {
			By("Creating a cluster with default helper image")
			key := types.NamespacedName{
				Name:      "humiocluster-update-helper-image",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.HelperImage = ""
			createAndBootstrapCluster(toCreate)

			By("Validating pod uses default helper image as init container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, initContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, testInterval).Should(Equal(helperImage))

			By("Validating pod uses default helper image as auth sidecar container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				for _, pod := range clusterPods {
					authIdx, _ := kubernetes.GetContainerIndexByName(pod, authContainerName)
					return pod.Spec.InitContainers[authIdx].Image
				}
				return ""
			}, testTimeout, testInterval).Should(Equal(helperImage))

			By("Overriding helper image")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			customHelperImage := "custom/helper-image:0.0.1"
			Eventually(func() error {
				err := k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.HelperImage = customHelperImage
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Validating pod is recreated using the explicitly defined helper image as init container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				for _, pod := range clusterPods {
					initIdx, _ := kubernetes.GetInitContainerIndexByName(pod, initContainerName)
					return pod.Spec.InitContainers[initIdx].Image
				}
				return ""
			}, testTimeout, testInterval).Should(Equal(customHelperImage))

			By("Validating pod is recreated using the explicitly defined helper image as auth sidecar container")
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				for _, pod := range clusterPods {
					authIdx, _ := kubernetes.GetContainerIndexByName(pod, authContainerName)
					return pod.Spec.InitContainers[authIdx].Image
				}
				return ""
			}, testTimeout, testInterval).Should(Equal(customHelperImage))

		})
	})

	Context("Humio Cluster Update Environment Variable", func() {
		It("Should correctly replace pods to use new environment variable", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-update-envvar",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
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
			}

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(toCreate.Spec.EnvironmentVariables[0]))
			}

			By("Updating the environment variable successfully")
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
			}
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.EnvironmentVariables = updatedEnvironmentVariables
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() string {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				Expect(len(clusterPods)).To(BeIdenticalTo(*toCreate.Spec.NodeCount))

				for _, pod := range clusterPods {
					humioIndex, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					Expect(pod.Spec.Containers[humioIndex].Env).Should(ContainElement(updatedEnvironmentVariables[0]))
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Ingress", func() {
		It("Should correctly update ingresses to use new annotations variable", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-ingress",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.Hostname = "humio.example.com"
			toCreate.Spec.ESHostname = "humio-es.humio.com"
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			desiredIngresses := []*v1beta1.Ingress{
				constructGeneralIngress(toCreate),
				constructStreamingQueryIngress(toCreate),
				constructIngestIngress(toCreate),
				constructESIngestIngress(toCreate),
			}

			var foundIngressList []v1beta1.Ingress
			Eventually(func() []v1beta1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(4))

			// Kubernetes 1.18 introduced a new field, PathType. For older versions PathType is returned as nil,
			// so we explicitly set the value before comparing ingress objects.
			// When minimum supported Kubernetes version is 1.18, we can drop this.
			pathTypeImplementationSpecific := v1beta1.PathTypeImplementationSpecific
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

			By("Adding an additional ingress annotation successfully")
			var existingHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &existingHumioCluster)
				existingHumioCluster.Spec.Ingress.Annotations = map[string]string{"humio.com/new-important-annotation": "true"}
				return k8sClient.Update(context.Background(), &existingHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, ingress := range ingresses {
					if _, ok := ingress.Annotations["humio.com/new-important-annotation"]; !ok {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			Eventually(func() ([]v1beta1.Ingress, error) {
				return kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			}, testTimeout, testInterval).Should(HaveLen(4))

			By("Changing ingress hostnames successfully")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &existingHumioCluster)
				existingHumioCluster.Spec.Hostname = "humio2.example.com"
				existingHumioCluster.Spec.ESHostname = "humio2-es.example.com"
				return k8sClient.Update(context.Background(), &existingHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			desiredIngresses = []*v1beta1.Ingress{
				constructGeneralIngress(&existingHumioCluster),
				constructStreamingQueryIngress(&existingHumioCluster),
				constructIngestIngress(&existingHumioCluster),
				constructESIngestIngress(&existingHumioCluster),
			}
			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, ingress := range ingresses {
					for _, rule := range ingress.Spec.Rules {
						if rule.Host != "humio2.example.com" && rule.Host != "humio2-es.example.com" {
							return false
						}
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			foundIngressList, _ = kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))

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

			By("Removing an ingress annotation successfully")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &existingHumioCluster)
				delete(existingHumioCluster.Spec.Ingress.Annotations, "humio.com/new-important-annotation")
				return k8sClient.Update(context.Background(), &existingHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() bool {
				ingresses, _ := kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, ingress := range ingresses {
					if _, ok := ingress.Annotations["humio.com/new-important-annotation"]; ok {
						return true
					}
				}
				return false
			}, testTimeout, testInterval).Should(BeFalse())

			foundIngressList, _ = kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, foundIngress := range foundIngressList {
				Expect(foundIngress.Annotations).ShouldNot(HaveKey("humio.com/new-important-annotation"))
			}

			By("Disabling ingress successfully")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &existingHumioCluster)
				existingHumioCluster.Spec.Ingress.Enabled = false
				return k8sClient.Update(context.Background(), &existingHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() ([]v1beta1.Ingress, error) {
				return kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			}, testTimeout, testInterval).Should(HaveLen(0))
		})
	})

	Context("Humio Cluster Pod Annotations", func() {
		It("Should be correctly annotated", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-pods",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.PodAnnotations = map[string]string{"humio.com/new-important-annotation": "true"}

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
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
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			svc, _ := kubernetes.GetService(context.Background(), k8sClient, key.Name, key.Namespace)
			Expect(svc.Spec.Type).To(BeIdenticalTo(corev1.ServiceTypeClusterIP))
			for _, port := range svc.Spec.Ports {
				if port.Name == "http" {
					Expect(port.Port).Should(Equal(int32(8080)))
				}
				if port.Name == "es" {
					Expect(port.Port).Should(Equal(int32(9200)))
				}
			}

			By("Updating service type")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.HumioServiceType = corev1.ServiceTypeLoadBalancer
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			// TODO: Right now the service is not updated properly, so we delete it ourselves to make the operator recreate the service
			Expect(k8sClient.Delete(context.Background(), constructService(&updatedHumioCluster))).To(Succeed())
			Eventually(func() corev1.ServiceType {
				svc, _ = kubernetes.GetService(context.Background(), k8sClient, key.Name, key.Namespace)
				return svc.Spec.Type
			}, testTimeout, testInterval).Should(Equal(corev1.ServiceTypeLoadBalancer))

			By("Updating Humio port")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.HumioServicePort = 443
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			// TODO: Right now the service is not updated properly, so we delete it ourselves to make the operator recreate the service
			Expect(k8sClient.Delete(context.Background(), constructService(&updatedHumioCluster))).To(Succeed())
			Eventually(func() int32 {
				svc, _ = kubernetes.GetService(context.Background(), k8sClient, key.Name, key.Namespace)
				for _, port := range svc.Spec.Ports {
					if port.Name == "http" {
						return port.Port
					}
				}
				return -1
			}, testTimeout, testInterval).Should(Equal(int32(443)))

			By("Updating ES port")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.HumioESServicePort = 9201
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			// TODO: Right now the service is not updated properly, so we delete it ourselves to make the operator recreate the service
			Expect(k8sClient.Delete(context.Background(), constructService(&updatedHumioCluster))).To(Succeed())
			Eventually(func() int32 {
				svc, _ = kubernetes.GetService(context.Background(), k8sClient, key.Name, key.Namespace)
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
		It("Should correctly configure container arguments", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-container-args",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Args).To(Equal([]string{"-c", "export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"}))
			}

			By("Updating node uuid prefix which includes ephemeral disks and zone")
			var updatedHumioCluster humiov1alpha1.HumioCluster

			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{Name: "USING_EPHEMERAL_DISKS", Value: "true"})
				updatedHumioCluster.Spec.NodeUUIDPrefix = "humio_{{.Zone}}_"
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if reflect.DeepEqual(pod.Spec.Containers[humioIdx].Args, []string{"-c", "export ZONE=$(cat /shared/availability-zone) && export ZOOKEEPER_PREFIX_FOR_NODE_UUID=/humio_$(cat /shared/availability-zone)_ && exec bash /app/humio/run.sh"}) {
						return true
					}
				}
				return false
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Container Arguments Without Zone", func() {
		It("Should correctly configure container arguments", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-container-without-zone-args",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Args).To(Equal([]string{"-c", "export ZONE=$(cat /shared/availability-zone) && exec bash /app/humio/run.sh"}))
			}

			By("Updating node uuid prefix which includes ephemeral disks but not zone")
			var updatedHumioCluster humiov1alpha1.HumioCluster

			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables, corev1.EnvVar{Name: "USING_EPHEMERAL_DISKS", Value: "true"})
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)
			Eventually(func() error {
				_, err := kubernetes.GetServiceAccount(context.Background(), k8sClient, humioServiceAccountNameOrDefault(toCreate), key.Namespace)
				return err
			}, testTimeout, testInterval).Should(Succeed())
			serviceAccount, _ := kubernetes.GetServiceAccount(context.Background(), k8sClient, humioServiceAccountNameOrDefault(toCreate), key.Namespace)
			Expect(serviceAccount.Annotations).Should(BeNil())

			By("Adding an annotation successfully")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.HumioServiceAccountAnnotations = map[string]string{"some-annotation": "true"}
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() bool {
				serviceAccount, _ = kubernetes.GetServiceAccount(context.Background(), k8sClient, humioServiceAccountNameOrDefault(toCreate), key.Namespace)
				_, ok := serviceAccount.Annotations["some-annotation"]
				return ok
			}, testTimeout, testInterval).Should(BeTrue())
			Expect(serviceAccount.Annotations["some-annotation"]).Should(Equal("true"))

			By("Removing all annotations successfully")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.HumioServiceAccountAnnotations = nil
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() map[string]string {
				serviceAccount, _ = kubernetes.GetServiceAccount(context.Background(), k8sClient, humioServiceAccountNameOrDefault(toCreate), key.Namespace)
				return serviceAccount.Annotations
			}, testTimeout, testInterval).Should(BeNil())
		})
	})

	Context("Humio Cluster Pod Security Context", func() {
		It("Should correctly handle pod security context", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-podsecuritycontext",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(podSecurityContextOrDefault(toCreate)))
			}
			By("Updating Pod Security Context to be empty")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.PodSecurityContext = &corev1.PodSecurityContext{}
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					if !reflect.DeepEqual(pod.Spec.SecurityContext, &corev1.PodSecurityContext{}) {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(&corev1.PodSecurityContext{}))
			}

			By("Updating Pod Security Context to be non-empty")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.PodSecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() corev1.PodSecurityContext {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(k8sClient, clusterPods)
				for _, pod := range clusterPods {
					return *pod.Spec.SecurityContext
				}
				return corev1.PodSecurityContext{}
			}, testTimeout, testInterval).Should(BeEquivalentTo(corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}))

			clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.SecurityContext).To(Equal(&corev1.PodSecurityContext{RunAsNonRoot: helpers.BoolPtr(true)}))
			}
		})
	})

	Context("Humio Cluster Container Security Context", func() {
		It("Should correctly handle container security context", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-containersecuritycontext",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].SecurityContext).To(Equal(containerSecurityContextOrDefault(toCreate)))
			}
			By("Updating Container Security Context to be empty")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.ContainerSecurityContext = &corev1.SecurityContext{}
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if !reflect.DeepEqual(pod.Spec.Containers[humioIdx].SecurityContext, &corev1.SecurityContext{}) {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].SecurityContext).To(Equal(&corev1.SecurityContext{}))
			}

			By("Updating Container Security Context to be non-empty")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.ContainerSecurityContext = &corev1.SecurityContext{
					Capabilities: &corev1.Capabilities{
						Add: []corev1.Capability{
							"NET_ADMIN",
						},
					},
				}
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			Eventually(func() corev1.SecurityContext {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(k8sClient, clusterPods)

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

			clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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

	Context("Humio Cluster Ekstra Kafka Configs", func() {
		It("Should correctly handle extra kafka configs", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-extrakafkaconfigs",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)

			By("Creating the cluster successfully with extra kafka configs")
			createAndBootstrapCluster(toCreate)
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElement(corev1.EnvVar{
					Name:  "EXTRA_KAFKA_CONFIGS_FILE",
					Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", extraKafkaPropertiesFilename),
				}))
			}

			By("Confirming pods have additional volume mounts for extra kafka configs")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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

			By("Confirming pods have additional volumes for extra kafka configs")
			mode := int32(420)
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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

			By("Confirming config map contains desired extra kafka configs")
			configMap, _ := kubernetes.GetConfigMap(context.Background(), k8sClient, extraKafkaConfigsConfigMapName(toCreate), key.Namespace)
			Expect(configMap.Data[extraKafkaPropertiesFilename]).To(Equal(toCreate.Spec.ExtraKafkaConfigs))

			By("Removing extra kafka configs")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.ExtraKafkaConfigs = ""
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Confirming pods do not have environment variable enabling extra kafka configs")
			Eventually(func() []corev1.EnvVar {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, testInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "EXTRA_KAFKA_CONFIGS_FILE",
				Value: fmt.Sprintf("/var/lib/humio/extra-kafka-configs-configmap/%s", extraKafkaPropertiesFilename),
			}))

			By("Confirming pods do not have additional volume mounts for extra kafka configs")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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

			By("Confirming pods do not have additional volumes for extra kafka configs")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
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
			By("Creating the cluster successfully with view group permissions")
			createAndBootstrapCluster(toCreate)

			By("Confirming config map was created")
			Eventually(func() error {
				_, err := kubernetes.GetConfigMap(context.Background(), k8sClient, viewGroupPermissionsConfigMapName(toCreate), toCreate.Namespace)
				return err
			}, testTimeout, testInterval).Should(Succeed())

			By("Confirming pods have the expected environment variable, volume and volume mounts")
			mode := int32(420)
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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

			By("Confirming config map contains desired view group permissions")
			configMap, _ := kubernetes.GetConfigMap(context.Background(), k8sClient, viewGroupPermissionsConfigMapName(toCreate), key.Namespace)
			Expect(configMap.Data[viewGroupPermissionsFilename]).To(Equal(toCreate.Spec.ViewGroupPermissions))

			By("Removing view group permissions")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.ViewGroupPermissions = ""
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Confirming pods do not have environment variable enabling view group permissions")
			Eventually(func() []corev1.EnvVar {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].Env
				}
				return []corev1.EnvVar{}
			}, testTimeout, testInterval).ShouldNot(ContainElement(corev1.EnvVar{
				Name:  "READ_GROUP_PERMISSIONS_FROM_FILE",
				Value: "true",
			}))

			By("Confirming pods do not have additional volume mounts for view group permissions")
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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

			By("Confirming pods do not have additional volumes for view group permissions")
			Eventually(func() []corev1.Volume {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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

			By("Confirming config map was cleaned up")
			Eventually(func() bool {
				_, err := kubernetes.GetConfigMap(context.Background(), k8sClient, viewGroupPermissionsConfigMapName(toCreate), toCreate.Namespace)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio Cluster Persistent Volumes", func() {
		It("Should correctly handle persistent volumes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-pvc",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.NodeCount = helpers.IntPtr(2)

			By("Bootstrapping the cluster successfully without persistent volumes")
			createAndBootstrapCluster(toCreate)
			Expect(kubernetes.ListPersistentVolumeClaims(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))).To(HaveLen(0))

			By("Updating cluster to use persistent volumes")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.DataVolumePersistentVolumeClaimSpecTemplate = corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				}
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}).Should(Succeed())

			Eventually(func() ([]corev1.PersistentVolumeClaim, error) {
				return kubernetes.ListPersistentVolumeClaims(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			}, testTimeout, testInterval).Should(HaveLen(*toCreate.Spec.NodeCount))

			Eventually(func() string {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRestarting))

			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

			By("Confirming pods are using PVC's and no PVC is left unused")
			pvcList, _ := kubernetes.ListPersistentVolumeClaims(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			foundPodList, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			initialExpectedVolumesCount := 7
			initialExpectedVolumeMountsCount := 5

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				// if we run on a real cluster we have TLS enabled (using 2 volumes),
				// and k8s will automatically inject a service account token adding one more
				initialExpectedVolumesCount += 3
				initialExpectedVolumeMountsCount += 2
			}

			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.Volumes).To(HaveLen(initialExpectedVolumesCount))
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(pod.Spec.Containers[humioIdx].VolumeMounts).To(HaveLen(initialExpectedVolumeMountsCount))
			}

			By("Adding additional volumes")
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
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.ExtraVolumes = []corev1.Volume{extraVolume}
				updatedHumioCluster.Spec.ExtraHumioVolumeMounts = []corev1.VolumeMount{extraVolumeMount}
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())
			Eventually(func() []corev1.Volume {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					return pod.Spec.Volumes
				}
				return []corev1.Volume{}
			}, testTimeout, testInterval).Should(HaveLen(initialExpectedVolumesCount + 1))
			Eventually(func() []corev1.VolumeMount {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					return pod.Spec.Containers[humioIdx].VolumeMounts
				}
				return []corev1.VolumeMount{}
			}, testTimeout, testInterval).Should(HaveLen(initialExpectedVolumeMountsCount + 1))
			clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			protocol := "http"
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				protocol = "https"
			}

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming PUBLIC_URL is set to default value and PROXY_PREFIX_URL is not set")
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(envVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal(fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)", protocol)))
				Expect(envVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL")).To(BeFalse())
			}

			By("Updating humio cluster path")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.Path = "/logs"
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Confirming PROXY_PREFIX_URL have been configured on all pods")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if !envVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL") {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			By("Confirming PUBLIC_URL and PROXY_PREFIX_URL have been correctly configured")
			clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(envVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal(fmt.Sprintf("%s://$(THIS_POD_IP):$(HUMIO_PORT)/logs", protocol)))
				Expect(envVarHasValue(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL", "/logs")).To(BeTrue())
			}

			By("Confirming cluster returns to Running state")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})

		It("Should correctly handle custom paths with ingress enabled", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-path-ing-enabled",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.Hostname = "test-cluster.humio.com"
			toCreate.Spec.ESHostname = "test-cluster-es.humio.com"
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming PUBLIC_URL is set to default value and PROXY_PREFIX_URL is not set")
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(envVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal("https://test-cluster.humio.com"))
				Expect(envVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL")).To(BeFalse())
			}

			By("Updating humio cluster path")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				updatedHumioCluster.Spec.Path = "/logs"
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Confirming PROXY_PREFIX_URL have been configured on all pods")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
					if !envVarHasKey(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL") {
						return false
					}
				}
				return true
			}, testTimeout, testInterval).Should(BeTrue())

			By("Confirming PUBLIC_URL and PROXY_PREFIX_URL have been correctly configured")
			clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				humioIdx, _ := kubernetes.GetContainerIndexByName(pod, humioContainerName)
				Expect(envVarValue(pod.Spec.Containers[humioIdx].Env, "PUBLIC_URL")).Should(Equal("https://test-cluster.humio.com/logs"))
				Expect(envVarHasValue(pod.Spec.Containers[humioIdx].Env, "PROXY_PREFIX_URL", "/logs")).To(BeTrue())
			}

			By("Confirming cluster returns to Running state")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(k8sClient, updatedHumioCluster.Namespace, kubernetes.MatchingLabelsForHumio(updatedHumioCluster.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
		})
	})

	Context("Humio Cluster Config Errors", func() {
		It("Creating cluster with conflicting volume mount name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-volmnt-name",
				Namespace: "default",
			}
			cluster := &humiov1alpha1.HumioCluster{
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
			Expect(k8sClient.Create(context.Background(), cluster)).Should(Succeed())
			var updatedHumioCluster humiov1alpha1.HumioCluster
			By("should indicate cluster configuration error")
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			k8sClient.Delete(context.Background(), &updatedHumioCluster)
		})
		It("Creating cluster with conflicting volume mount mount path", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-mount-path",
				Namespace: "default",
			}
			cluster := &humiov1alpha1.HumioCluster{
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
			Expect(k8sClient.Create(context.Background(), cluster)).Should(Succeed())

			var updatedHumioCluster humiov1alpha1.HumioCluster
			By("should indicate cluster configuration error")
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			k8sClient.Delete(context.Background(), &updatedHumioCluster)
		})
		It("Creating cluster with conflicting volume name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-vol-name",
				Namespace: "default",
			}
			cluster := &humiov1alpha1.HumioCluster{
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
			k8sClient.Create(context.Background(), cluster)

			var updatedHumioCluster humiov1alpha1.HumioCluster
			By("should indicate cluster configuration error")
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			k8sClient.Delete(context.Background(), &updatedHumioCluster)
		})
		It("Creating cluster with higher replication factor than nodes", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-err-repl-factor",
				Namespace: "default",
			}
			cluster := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					TargetReplicationFactor: 2,
					NodeCount:               helpers.IntPtr(1),
				},
			}
			Expect(k8sClient.Create(context.Background(), cluster)).Should(Succeed())
			var updatedHumioCluster humiov1alpha1.HumioCluster
			By("should indicate cluster configuration error")
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				return updatedHumioCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateConfigError))

			k8sClient.Delete(context.Background(), &updatedHumioCluster)
		})
	})

	Context("Humio Cluster Without TLS for Ingress", func() {
		It("Creating cluster without TLS for ingress", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-without-tls-ingress",
				Namespace: "default",
			}
			tlsDisabled := false
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.Ingress.Enabled = true
			toCreate.Spec.Ingress.Controller = "nginx"
			toCreate.Spec.Ingress.TLS = &tlsDisabled

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming ingress objects do not have TLS configured")
			var ingresses []v1beta1.Ingress
			Eventually(func() ([]v1beta1.Ingress, error) {
				return kubernetes.ListIngresses(k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			}, testTimeout, testInterval).Should(HaveLen(4))

			ingresses, _ = kubernetes.ListIngresses(k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			for _, ingress := range ingresses {
				Expect(ingress.Spec.TLS).To(BeNil())
			}
		})
	})

	Context("Humio Cluster Ingress", func() {
		It("Should correctly handle ingress when toggling ESHostname on/off", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-ingress-hostname",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.Hostname = "test-cluster.humio.com"
			toCreate.Spec.ESHostname = ""
			toCreate.Spec.Ingress = humiov1alpha1.HumioClusterIngressSpec{
				Enabled:    true,
				Controller: "nginx",
			}

			By("Creating the cluster successfully without ESHostname defined")
			createAndBootstrapCluster(toCreate)

			By("Confirming we only created ingresses with expected hostname")
			var foundIngressList []v1beta1.Ingress
			Eventually(func() []v1beta1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(3))
			foundIngressList, _ = kubernetes.ListIngresses(k8sClient, toCreate.Namespace, kubernetes.MatchingLabelsForHumio(toCreate.Name))
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					Expect(rule.Host).To(Equal(toCreate.Spec.Hostname))
				}
			}

			By("Setting the ESHostname")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			esHostname := "test-cluster-es.humio.com"
			Eventually(func() error {
				err := k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ESHostname = esHostname
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Confirming ingresses for ES Hostname gets created")
			Eventually(func() []v1beta1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(4))

			var ingressHostnames []string
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					ingressHostnames = append(ingressHostnames, rule.Host)
				}
			}
			Expect(ingressHostnames).To(ContainElement(esHostname))

			By("Removing the ESHostname")
			Eventually(func() error {
				err := k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.ESHostname = ""
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Confirming ingresses for ES Hostname gets removed")
			Eventually(func() []v1beta1.Ingress {
				foundIngressList, _ = kubernetes.ListIngresses(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				return foundIngressList
			}, testTimeout, testInterval).Should(HaveLen(3))

			ingressHostnames = []string{}
			for _, ingress := range foundIngressList {
				for _, rule := range ingress.Spec.Rules {
					ingressHostnames = append(ingressHostnames, rule.Host)
				}
			}
			Expect(ingressHostnames).ToNot(ContainElement(esHostname))
		})
	})

	Context("Humio Cluster With Custom Service Accounts", func() {
		It("Creating cluster with custom service accounts", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-service-accounts",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.InitServiceAccountName = "init-custom-service-account"
			toCreate.Spec.AuthServiceAccountName = "auth-custom-service-account"
			toCreate.Spec.HumioServiceAccountName = "humio-custom-service-account"

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming init container is using the correct service account")
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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
						secret, err := kubernetes.GetSecret(context.Background(), k8sClient, volume.Secret.SecretName, key.Namespace)
						Expect(err).ShouldNot(HaveOccurred())
						Expect(secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]).To(Equal(toCreate.Spec.InitServiceAccountName))
					}
				}
			}
			By("Confirming auth container is using the correct service account")
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
						secret, err := kubernetes.GetSecret(context.Background(), k8sClient, volume.Secret.SecretName, key.Namespace)
						Expect(err).ShouldNot(HaveOccurred())
						Expect(secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]).To(Equal(toCreate.Spec.AuthServiceAccountName))
					}
				}
			}
			By("Confirming humio pod is using the correct service account")
			for _, pod := range clusterPods {
				Expect(pod.Spec.ServiceAccountName).To(Equal(toCreate.Spec.HumioServiceAccountName))
			}
		})

		It("Creating cluster with custom service accounts sharing the same name", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-sa-same-name",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.InitServiceAccountName = "custom-service-account"
			toCreate.Spec.AuthServiceAccountName = "custom-service-account"
			toCreate.Spec.HumioServiceAccountName = "custom-service-account"

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming init container is using the correct service account")
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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
						secret, err := kubernetes.GetSecret(context.Background(), k8sClient, volume.Secret.SecretName, key.Namespace)
						Expect(err).ShouldNot(HaveOccurred())
						Expect(secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]).To(Equal(toCreate.Spec.InitServiceAccountName))
					}
				}
			}
			By("Confirming auth container is using the correct service account")
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
						secret, err := kubernetes.GetSecret(context.Background(), k8sClient, volume.Secret.SecretName, key.Namespace)
						Expect(err).ShouldNot(HaveOccurred())
						Expect(secret.ObjectMeta.Annotations["kubernetes.io/service-account.name"]).To(Equal(toCreate.Spec.AuthServiceAccountName))
					}
				}
			}
			By("Confirming humio pod is using the correct service account")
			for _, pod := range clusterPods {
				Expect(pod.Spec.ServiceAccountName).To(Equal(toCreate.Spec.HumioServiceAccountName))
			}
		})
	})

	Context("Humio Cluster With Service Annotations", func() {
		It("Creating cluster with custom service annotations", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-svc-annotations",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.HumioServiceAnnotations = map[string]string{
				"service.beta.kubernetes.io/aws-load-balancer-type":                              "nlb",
				"service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled": "false",
				"service.beta.kubernetes.io/aws-load-balancer-ssl-cert":                          "arn:aws:acm:region:account:certificate/123456789012-1234-1234-1234-12345678",
				"service.beta.kubernetes.io/aws-load-balancer-backend-protocol":                  "ssl",
				"service.beta.kubernetes.io/aws-load-balancer-ssl-ports":                         "443",
				"service.beta.kubernetes.io/aws-load-balancer-internal":                          "0.0.0.0/0",
			}

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming service was created using the correct annotations")
			svc, err := kubernetes.GetService(context.Background(), k8sClient, toCreate.Name, toCreate.Namespace)
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
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.Tolerations = []corev1.Toleration{
				{
					Key:      "key",
					Operator: "Equal",
					Value:    "value",
					Effect:   "NoSchedule",
				},
			}

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming the humio pods use the requested tolerations")
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				Expect(pod.Spec.Tolerations).To(ContainElement(toCreate.Spec.Tolerations[0]))
			}
		})
	})

	Context("Humio Cluster With Service Labels", func() {
		It("Creating cluster with custom service labels", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-custom-svc-labels",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.HumioServiceLabels = map[string]string{
				"mirror.linkerd.io/exported": "true",
			}

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming service was created using the correct annotations")
			svc, err := kubernetes.GetService(context.Background(), k8sClient, toCreate.Name, toCreate.Namespace)
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
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.SidecarContainers = nil

			By("Creating the cluster successfully")
			createAndBootstrapCluster(toCreate)

			By("Confirming the humio pods are not using shared process namespace nor additional sidecars")
			clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
			for _, pod := range clusterPods {
				if pod.Spec.ShareProcessNamespace != nil {
					Expect(*pod.Spec.ShareProcessNamespace).To(BeFalse())
				}
				Expect(pod.Spec.Containers).Should(HaveLen(2))
			}

			By("Enabling shared process namespace and sidecars")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(context.Background(), key, &updatedHumioCluster)
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

				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Confirming the humio pods use shared process namespace")
			Eventually(func() bool {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					if pod.Spec.ShareProcessNamespace != nil {
						return *pod.Spec.ShareProcessNamespace
					}
				}
				return false
			}, testTimeout, testInterval).Should(BeTrue())

			By("Confirming pods contain the new sidecar")
			Eventually(func() string {
				clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
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
			By("Creating Humio cluster without a termination grace period set")
			key := types.NamespacedName{
				Name:      "humiocluster-grace-default",
				Namespace: "default",
			}
			toCreate := constructBasicSingleNodeHumioCluster(key)
			toCreate.Spec.TerminationGracePeriodSeconds = nil

			Eventually(func() error {
				return k8sClient.Create(context.Background(), toCreate)
			}, testTimeout, testInterval).Should(Succeed())

			By("Validating pod is created with the default grace period")
			Eventually(func() int64 {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				markPodsAsRunning(k8sClient, clusterPods)

				for _, pod := range clusterPods {
					if pod.Spec.TerminationGracePeriodSeconds != nil {
						return *pod.Spec.TerminationGracePeriodSeconds
					}
				}
				return 0
			}, testTimeout, testInterval).Should(BeEquivalentTo(300))

			By("Overriding termination grace period")
			var updatedHumioCluster humiov1alpha1.HumioCluster
			Eventually(func() error {
				err := k8sClient.Get(context.Background(), key, &updatedHumioCluster)
				if err != nil {
					return err
				}
				updatedHumioCluster.Spec.TerminationGracePeriodSeconds = helpers.Int64Ptr(120)
				return k8sClient.Update(context.Background(), &updatedHumioCluster)
			}, testTimeout, testInterval).Should(Succeed())

			By("Validating pod is recreated using the explicitly defined grace period")
			Eventually(func() int64 {
				clusterPods, _ := kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
				for _, pod := range clusterPods {
					if pod.Spec.TerminationGracePeriodSeconds != nil {
						return *pod.Spec.TerminationGracePeriodSeconds
					}
				}
				return 0
			}, testTimeout, testInterval).Should(BeEquivalentTo(120))
		})
	})
})

func createAndBootstrapCluster(cluster *humiov1alpha1.HumioCluster) {
	key := types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      cluster.Name,
	}

	if cluster.Spec.HumioServiceAccountName != "" {
		By("Creating service account for humio container")
		humioServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.HumioServiceAccountName, cluster.Name, cluster.Namespace, map[string]string{})
		Expect(k8sClient.Create(context.Background(), humioServiceAccount)).To(Succeed())
	}

	if !cluster.Spec.DisableInitContainer {
		if cluster.Spec.InitServiceAccountName != "" {
			if cluster.Spec.InitServiceAccountName != cluster.Spec.HumioServiceAccountName {
				By("Creating service account for init container")
				initServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.InitServiceAccountName, cluster.Name, cluster.Namespace, map[string]string{})
				Expect(k8sClient.Create(context.Background(), initServiceAccount)).To(Succeed())
			}

			By("Creating cluster role for init container")
			initClusterRole := kubernetes.ConstructInitClusterRole(cluster.Spec.InitServiceAccountName, key.Name)
			Expect(k8sClient.Create(context.Background(), initClusterRole)).To(Succeed())

			By("Creating cluster role binding for init container")
			initClusterRoleBinding := kubernetes.ConstructClusterRoleBinding(cluster.Spec.InitServiceAccountName, initClusterRole.Name, key.Name, key.Namespace, cluster.Spec.InitServiceAccountName)
			Expect(k8sClient.Create(context.Background(), initClusterRoleBinding)).To(Succeed())
		}
	}

	if cluster.Spec.AuthServiceAccountName != "" {
		if cluster.Spec.AuthServiceAccountName != cluster.Spec.HumioServiceAccountName {
			By("Creating service account for auth container")
			authServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.AuthServiceAccountName, cluster.Name, cluster.Namespace, map[string]string{})
			Expect(k8sClient.Create(context.Background(), authServiceAccount)).To(Succeed())
		}

		By("Creating role for auth container")
		authRole := kubernetes.ConstructAuthRole(cluster.Spec.AuthServiceAccountName, key.Name, key.Namespace)
		Expect(k8sClient.Create(context.Background(), authRole)).To(Succeed())

		By("Creating role binding for auth container")
		authRoleBinding := kubernetes.ConstructRoleBinding(cluster.Spec.AuthServiceAccountName, authRole.Name, key.Name, key.Namespace, cluster.Spec.AuthServiceAccountName)
		Expect(k8sClient.Create(context.Background(), authRoleBinding)).To(Succeed())
	}

	By("Creating HumioCluster resource")
	Expect(k8sClient.Create(context.Background(), cluster)).Should(Succeed())

	By("Confirming cluster enters bootstrapping state")
	var updatedHumioCluster humiov1alpha1.HumioCluster
	Eventually(func() string {
		k8sClient.Get(context.Background(), key, &updatedHumioCluster)
		return updatedHumioCluster.Status.State
	}, testTimeout, testInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateBootstrapping))

	By("Waiting to have the correct number of pods")
	var clusterPods []corev1.Pod
	Eventually(func() []corev1.Pod {
		clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
		markPodsAsRunning(k8sClient, clusterPods)
		return clusterPods
	}, testTimeout, testInterval).Should(HaveLen(*cluster.Spec.NodeCount))

	humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], humioContainerName)
	Expect(err).ToNot(HaveOccurred())
	humioContainerArgs := strings.Join(clusterPods[0].Spec.Containers[humioIdx].Args, " ")
	if cluster.Spec.DisableInitContainer {
		By("Confirming pods do not use init container")
		Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(0))
		Expect(humioContainerArgs).ToNot(ContainSubstring("export ZONE="))
	} else {
		By("Confirming pods have an init container")
		Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(1))
		Expect(humioContainerArgs).To(ContainSubstring("export ZONE="))
	}

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
		// Simulate sidecar creating the secret which contains the admin token use to authenticate with humio
		secretData := map[string][]byte{"token": []byte("")}
		adminTokenSecretName := fmt.Sprintf("%s-%s", updatedHumioCluster.Name, kubernetes.ServiceTokenSecretNameSuffix)
		By("Simulating the auth container creating the secret containing the API token")
		desiredSecret := kubernetes.ConstructSecret(updatedHumioCluster.Name, updatedHumioCluster.Namespace, adminTokenSecretName, secretData)
		Expect(k8sClient.Create(context.Background(), desiredSecret)).To(Succeed())
	}

	By("Confirming cluster enters running state")
	Eventually(func() string {
		clusterPods, _ = kubernetes.ListPods(k8sClient, key.Namespace, kubernetes.MatchingLabelsForHumio(key.Name))
		markPodsAsRunning(k8sClient, clusterPods)

		k8sClient.Get(context.Background(), key, &updatedHumioCluster)
		return updatedHumioCluster.Status.State
	}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

	By("Validating cluster has expected pod revision annotation")
	Eventually(func() string {
		k8sClient.Get(context.Background(), key, &updatedHumioCluster)
		val, _ := updatedHumioCluster.Annotations[podRevisionAnnotation]
		return val
	}, testTimeout, testInterval).Should(Equal("1"))

	By("Waiting for the auth sidecar to populate the secret containing the API token")
	Eventually(func() error {
		return k8sClient.Get(context.Background(), types.NamespacedName{
			Namespace: key.Namespace,
			Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix),
		}, &corev1.Secret{})
	}, testTimeout, testInterval).Should(Succeed())

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		By("Validating API token was obtained using the expected method")
		humioVersion, err := HumioVersionFromCluster(cluster)
		Expect(err).ToNot(HaveOccurred())
		var apiTokenSecret corev1.Secret
		Eventually(func() error {
			return k8sClient.Get(context.Background(), types.NamespacedName{
				Namespace: key.Namespace,
				Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix),
			}, &apiTokenSecret)
		}, testTimeout, testInterval).Should(Succeed())

		ok, err := humioVersion.AtLeast(HumioVersionWhichContainsAPITokenRotationMutation)
		Expect(err).ToNot(HaveOccurred())
		if ok {
			By(fmt.Sprintf("Should be using API because of image %s", cluster.Spec.Image))
			Expect(apiTokenSecret.Annotations).Should(HaveKeyWithValue(apiTokenMethodAnnotationName, apiTokenMethodFromAPI))
		} else {
			By(fmt.Sprintf("Should be using File because of image %s", cluster.Spec.Image))
			Expect(apiTokenSecret.Annotations).Should(HaveKeyWithValue(apiTokenMethodAnnotationName, apiTokenMethodFromFile))
		}
	}

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		// TODO: We can drop this version comparison when we only support 1.16 and newer.
		By("Validating cluster nodes have ZONE configured correctly")
		if humioVersion, err := HumioVersionFromCluster(cluster); err != nil {
			if ok, err := humioVersion.AtLeast(HumioVersionWhichContainsZone); ok && err != nil {
				By("Validating zone is set on Humio nodes")
				Eventually(func() []string {
					cluster, err := humioClient.GetClusters()
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
	}
}

func constructBasicSingleNodeHumioCluster(key types.NamespacedName) *humiov1alpha1.HumioCluster {
	return &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:        key.Name,
			Namespace:   key.Namespace,
			Annotations: map[string]string{autoCleanupAfterTestAnnotationName: "true"},
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
			},
		},
	}
}

func markPodsAsRunning(client client.Client, pods []corev1.Pod) error {
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		return nil
	}

	By("Simulating Humio container starts up and is marked Ready")
	for nodeID, pod := range pods {
		pod.Status.PodIP = fmt.Sprintf("192.168.0.%d", nodeID)
		pod.Status.Conditions = []corev1.PodCondition{
			{
				Type:   corev1.PodConditionType("Ready"),
				Status: corev1.ConditionTrue,
			},
		}
		err := client.Status().Update(context.TODO(), &pod)
		if err != nil {
			return fmt.Errorf("failed to update pods to prepare for testing the labels: %s", err)
		}
	}
	return nil
}
