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
	"os"
	"reflect"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Humio Resources Controllers", func() {

	BeforeEach(func() {
		By("Creating a shared humio cluster if it doesn't already exist")
		clusterKey := types.NamespacedName{
			Name:      "humiocluster-shared",
			Namespace: "default",
		}
		var existingCluster humiov1alpha1.HumioCluster
		var err error
		Eventually(func() bool {
			err = k8sClient.Get(context.TODO(), clusterKey, &existingCluster)
			if errors.IsNotFound(err) {
				// Object has not been created yet
				return true
			}
			if err != nil {
				// Some other error happened. Typically:
				//   <*cache.ErrCacheNotStarted | 0x31fc738>: {}
				//	   the cache is not started, can not read objects occurred
				return false
			}
			// At this point we know the object already exists.
			return true
		}, testTimeout, testInterval).Should(BeTrue())
		if errors.IsNotFound(err) {
			cluster := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterKey.Name,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					Image:                   image,
					NodeCount:               helpers.IntPtr(1),
					TargetReplicationFactor: 1,
					ExtraKafkaConfigs:       "security.protocol=PLAINTEXT",
					TLS:                     &humiov1alpha1.HumioClusterTLSSpec{Enabled: helpers.BoolPtr(false)},
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
							Value: clusterKey.Name,
						},
					},
				},
			}
			createAndBootstrapCluster(cluster)
		} else {
			Expect(err).ToNot(HaveOccurred())
		}
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test

	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Humio Ingest token", func() {
		It("should handle Humio Ingest Tokens correctly with a token target secret", func() {
			key := types.NamespacedName{
				Name:      "humioingesttoken-with-token-secret",
				Namespace: "default",
			}

			toCreate := &humiov1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioIngestTokenSpec{
					ManagedClusterName: "humiocluster-shared",
					Name:               key.Name,
					ParserName:         "json",
					RepositoryName:     "humio",
					TokenSecretName:    "target-secret-1",
				},
			}

			By("Creating the ingest token with token secret successfully")
			Expect(k8sClient.Create(context.Background(), toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetched)
				return fetched.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateExists))

			ingestTokenSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(
					context.Background(),
					types.NamespacedName{
						Namespace: key.Namespace,
						Name:      toCreate.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, testInterval).Should(Succeed())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}
			Expect(ingestTokenSecret.OwnerReferences).Should(HaveLen(1))

			By("Deleting ingest token secret successfully adds back secret")
			Expect(
				k8sClient.Delete(
					context.Background(),
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: key.Namespace,
							Name:      toCreate.Spec.TokenSecretName,
						},
					},
				),
			).Should(Succeed())

			Eventually(func() error {
				return k8sClient.Get(
					context.Background(),
					types.NamespacedName{
						Namespace: key.Namespace,
						Name:      toCreate.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, testInterval).Should(Succeed())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}

			By("Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetched)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetched)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())
		})

		It("Should handle ingest token correctly without token target secret", func() {
			key := types.NamespacedName{
				Name:      "humioingesttoken-without-token-secret",
				Namespace: "default",
			}

			toCreate := &humiov1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioIngestTokenSpec{
					ManagedClusterName: "humiocluster-shared",
					Name:               key.Name,
					ParserName:         "accesslog",
					RepositoryName:     "humio",
				},
			}

			By("Creating the ingest token without token secret successfully")
			Expect(k8sClient.Create(context.Background(), toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetched)
				return fetched.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateExists))

			By("Checking we do not create a token secret")
			var allSecrets corev1.SecretList
			k8sClient.List(context.Background(), &allSecrets, client.InNamespace(fetched.Namespace))
			for _, secret := range allSecrets.Items {
				for _, owner := range secret.OwnerReferences {
					Expect(owner.Name).ShouldNot(BeIdenticalTo(fetched.Name))
				}
			}

			By("Enabling token secret name successfully creates secret")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, fetched)
				fetched.Spec.TokenSecretName = "target-secret-2"
				return k8sClient.Update(context.Background(), fetched)
			}, testTimeout, testInterval).Should(Succeed())
			ingestTokenSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(
					context.Background(),
					types.NamespacedName{
						Namespace: fetched.Namespace,
						Name:      fetched.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, testInterval).Should(Succeed())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}

			By("Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetched)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetched)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio Repository", func() {
		It("Should handle repository correctly", func() {
			key := types.NamespacedName{
				Name:      "humiorepository",
				Namespace: "default",
			}

			toCreate := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioRepositorySpec{
					ManagedClusterName: "humiocluster-shared",
					Name:               "example-repository",
					Description:        "important description",
					Retention: humiov1alpha1.HumioRetention{
						TimeInDays:      30,
						IngestSizeInGB:  5,
						StorageSizeInGB: 1,
					},
				},
			}

			By("Creating the repository successfully")
			Expect(k8sClient.Create(context.Background(), toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetched)
				return fetched.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			initialRepository, err := humioClient.GetRepository(toCreate)
			Expect(err).To(BeNil())
			Expect(initialRepository).ToNot(BeNil())

			expectedInitialRepository := repositoryExpectation{
				Name:                   toCreate.Spec.Name,
				Description:            toCreate.Spec.Description,
				RetentionDays:          float64(toCreate.Spec.Retention.TimeInDays),
				IngestRetentionSizeGB:  float64(toCreate.Spec.Retention.IngestSizeInGB),
				StorageRetentionSizeGB: float64(toCreate.Spec.Retention.StorageSizeInGB),
			}
			Eventually(func() repositoryExpectation {
				initialRepository, err := humioClient.GetRepository(fetched)
				if err != nil {
					return repositoryExpectation{}
				}
				return repositoryExpectation{
					Name:                   initialRepository.Name,
					Description:            initialRepository.Description,
					RetentionDays:          initialRepository.RetentionDays,
					IngestRetentionSizeGB:  initialRepository.IngestRetentionSizeGB,
					StorageRetentionSizeGB: initialRepository.StorageRetentionSizeGB,
					SpaceUsed:              initialRepository.SpaceUsed,
				}
			}, testTimeout, testInterval).Should(Equal(expectedInitialRepository))

			By("Updating the repository successfully")
			updatedDescription := "important description - now updated"
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, fetched)
				fetched.Spec.Description = updatedDescription
				return k8sClient.Update(context.Background(), fetched)
			}, testTimeout, testInterval).Should(Succeed())

			updatedRepository, err := humioClient.GetRepository(fetched)
			Expect(err).To(BeNil())
			Expect(updatedRepository).ToNot(BeNil())

			expectedUpdatedRepository := repositoryExpectation{
				Name:                   toCreate.Spec.Name,
				Description:            updatedDescription,
				RetentionDays:          float64(toCreate.Spec.Retention.TimeInDays),
				IngestRetentionSizeGB:  float64(toCreate.Spec.Retention.IngestSizeInGB),
				StorageRetentionSizeGB: float64(toCreate.Spec.Retention.StorageSizeInGB),
			}
			Eventually(func() repositoryExpectation {
				updatedRepository, err := humioClient.GetRepository(fetched)
				if err != nil {
					return repositoryExpectation{}
				}

				return repositoryExpectation{
					Name:                   updatedRepository.Name,
					Description:            updatedRepository.Description,
					RetentionDays:          updatedRepository.RetentionDays,
					IngestRetentionSizeGB:  updatedRepository.IngestRetentionSizeGB,
					StorageRetentionSizeGB: updatedRepository.StorageRetentionSizeGB,
					SpaceUsed:              updatedRepository.SpaceUsed,
				}
			}, testTimeout, testInterval).Should(Equal(expectedUpdatedRepository))

			By("Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetched)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetched)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio View", func() {
		It("Should handle view correctly", func() {
			viewKey := types.NamespacedName{
				Name:      "humioview",
				Namespace: "default",
			}

			repositoryToCreate := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioRepositorySpec{
					ManagedClusterName: "humiocluster-shared",
					Name:               "example-repository-view",
					Description:        "important description",
					Retention: humiov1alpha1.HumioRetention{
						TimeInDays:      30,
						IngestSizeInGB:  5,
						StorageSizeInGB: 1,
					},
				},
			}

			connections := make([]humiov1alpha1.HumioViewConnection, 0)
			connections = append(connections, humiov1alpha1.HumioViewConnection{
				RepositoryName: "example-repository-view",
				Filter:         "*",
			})
			viewToCreate := &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ManagedClusterName: "humiocluster-shared",
					Name:               "example-view",
					Connections:        connections,
				},
			}

			By("Creating the repository successfully")
			Expect(k8sClient.Create(context.Background(), repositoryToCreate)).Should(Succeed())

			fetchedRepo := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), viewKey, fetchedRepo)
				return fetchedRepo.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			By("Creating the view successfully in k8s")
			Expect(k8sClient.Create(context.Background(), viewToCreate)).Should(Succeed())

			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))

			By("Creating the view successfully in Humio")
			initialView, err := humioClient.GetView(viewToCreate)
			Expect(err).To(BeNil())
			Expect(initialView).ToNot(BeNil())

			expectedInitialView := humioapi.View{
				Name:        viewToCreate.Spec.Name,
				Connections: viewToCreate.GetViewConnections(),
			}

			Eventually(func() humioapi.View {
				initialView, err := humioClient.GetView(fetchedView)
				if err != nil {
					return humioapi.View{}
				}
				return *initialView
			}, testTimeout, testInterval).Should(Equal(expectedInitialView))

			By("Updating the view successfully in k8s")
			updatedConnections := []humiov1alpha1.HumioViewConnection{
				{
					RepositoryName: "humio",
					Filter:         "*",
				},
			}
			Eventually(func() error {
				k8sClient.Get(context.Background(), viewKey, fetchedView)
				fetchedView.Spec.Connections = updatedConnections
				return k8sClient.Update(context.Background(), fetchedView)
			}, testTimeout, testInterval).Should(Succeed())

			By("Updating the view successfully in Humio")
			updatedView, err := humioClient.GetView(fetchedView)
			Expect(err).To(BeNil())
			Expect(updatedView).ToNot(BeNil())

			expectedUpdatedView := humioapi.View{
				Name:        viewToCreate.Spec.Name,
				Connections: fetchedView.GetViewConnections(),
			}
			Eventually(func() humioapi.View {
				updatedView, err := humioClient.GetView(fetchedView)
				if err != nil {
					return humioapi.View{}
				}
				return *updatedView
			}, testTimeout, testInterval).Should(Equal(expectedUpdatedView))

			By("Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetchedView)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), viewKey, fetchedView)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio Parser", func() {
		It("Should handle parser correctly", func() {
			spec := humiov1alpha1.HumioParserSpec{
				ManagedClusterName: "humiocluster-shared",
				Name:               "example-parser",
				RepositoryName:     "humio",
				ParserScript:       "kvParse()",
				TagFields:          []string{"@somefield"},
				TestData:           []string{"this is an example of rawstring"},
			}

			key := types.NamespacedName{
				Name:      "humioparser",
				Namespace: "default",
			}

			toCreate := &humiov1alpha1.HumioParser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: spec,
			}

			By("Creating the parser successfully")
			Expect(k8sClient.Create(context.Background(), toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioParser{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetched)
				return fetched.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioParserStateExists))

			initialParser, err := humioClient.GetParser(toCreate)
			Expect(err).To(BeNil())
			Expect(initialParser).ToNot(BeNil())

			expectedInitialParser := humioapi.Parser{
				Name:      spec.Name,
				Script:    spec.ParserScript,
				TagFields: spec.TagFields,
				Tests:     helpers.MapTests(spec.TestData, helpers.ToTestCase),
			}
			Expect(reflect.DeepEqual(*initialParser, expectedInitialParser)).To(BeTrue())

			By("Updating the parser successfully")
			updatedScript := "kvParse() | updated"
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, fetched)
				fetched.Spec.ParserScript = updatedScript
				return k8sClient.Update(context.Background(), fetched)
			}, testTimeout, testInterval).Should(Succeed())

			updatedParser, err := humioClient.GetParser(fetched)
			Expect(err).To(BeNil())
			Expect(updatedParser).ToNot(BeNil())

			expectedUpdatedParser := humioapi.Parser{
				Name:      spec.Name,
				Script:    updatedScript,
				TagFields: spec.TagFields,
				Tests:     helpers.MapTests(spec.TestData, helpers.ToTestCase),
			}
			Eventually(func() humioapi.Parser {
				updatedParser, err := humioClient.GetParser(fetched)
				if err != nil {
					return humioapi.Parser{}
				}
				return *updatedParser
			}, testTimeout, testInterval).Should(Equal(expectedUpdatedParser))

			By("Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetched)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetched)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("Humio External Cluster", func() {
		It("Should handle externalcluster correctly with token secret", func() {
			key := types.NamespacedName{
				Name:      "humioexternalcluster",
				Namespace: "default",
			}

			toCreate := &humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                "http://humiocluster-shared.default:8080/",
					APITokenSecretName: "humiocluster-shared-admin-token",
					Insecure:           true,
				},
			}

			By("Creating the external cluster successfully")
			Expect(k8sClient.Create(context.Background(), toCreate)).Should(Succeed())

			By("Confirming external cluster gets marked as ready")
			fetched := &humiov1alpha1.HumioExternalCluster{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetched)
				return fetched.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioExternalClusterStateReady))

			By("Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetched)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetched)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})
})

type repositoryExpectation struct {
	Name                   string
	Description            string
	RetentionDays          float64 `graphql:"timeBasedRetention"`
	IngestRetentionSizeGB  float64 `graphql:"ingestSizeBasedRetention"`
	StorageRetentionSizeGB float64 `graphql:"storageSizeBasedRetention"`
	SpaceUsed              int64   `graphql:"compressedByteSize"`
}
