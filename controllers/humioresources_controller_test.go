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
		// failed test runs that don't clean up leave resources behind.

	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
		var existingClusters humiov1alpha1.HumioClusterList
		k8sClient.List(context.Background(), &existingClusters)
		for _, cluster := range existingClusters.Items {
			if val, ok := cluster.Annotations[autoCleanupAfterTestAnnotationName]; ok {
				if val == testProcessID {
					_ = k8sClient.Delete(context.Background(), &cluster)
				}
			}
		}
	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Humio Resources Controllers", func() {
		It("should handle resources correctly", func() {

			By("HumioCluster: Creating shared test cluster")
			clusterKey := types.NamespacedName{
				Name:      "humiocluster-shared",
				Namespace: "default",
			}
			cluster := constructBasicSingleNodeHumioCluster(clusterKey)
			createAndBootstrapCluster(cluster)

			By("HumioIngestToken: Creating Humio Ingest token with token target secret")
			key := types.NamespacedName{
				Name:      "humioingesttoken-with-token-secret",
				Namespace: "default",
			}

			toCreateIngestToken := &humiov1alpha1.HumioIngestToken{
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

			By("HumioIngestToken: Creating the ingest token with token secret successfully")
			Expect(k8sClient.Create(context.Background(), toCreateIngestToken)).Should(Succeed())

			fetchedIngestToken := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetchedIngestToken)
				return fetchedIngestToken.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateExists))

			ingestTokenSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(
					context.Background(),
					types.NamespacedName{
						Namespace: key.Namespace,
						Name:      toCreateIngestToken.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, testInterval).Should(Succeed())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}
			Expect(ingestTokenSecret.OwnerReferences).Should(HaveLen(1))

			By("HumioIngestToken: Deleting ingest token secret successfully adds back secret")
			Expect(
				k8sClient.Delete(
					context.Background(),
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: key.Namespace,
							Name:      toCreateIngestToken.Spec.TokenSecretName,
						},
					},
				),
			).Should(Succeed())

			Eventually(func() error {
				return k8sClient.Get(
					context.Background(),
					types.NamespacedName{
						Namespace: key.Namespace,
						Name:      toCreateIngestToken.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, testInterval).Should(Succeed())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}

			By("HumioIngestToken: Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetchedIngestToken)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetchedIngestToken)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			By("HumioIngestToken: Should handle ingest token correctly without token target secret")
			key = types.NamespacedName{
				Name:      "humioingesttoken-without-token-secret",
				Namespace: "default",
			}

			toCreateIngestToken = &humiov1alpha1.HumioIngestToken{
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

			By("HumioIngestToken: Creating the ingest token without token secret successfully")
			Expect(k8sClient.Create(context.Background(), toCreateIngestToken)).Should(Succeed())

			fetchedIngestToken = &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetchedIngestToken)
				return fetchedIngestToken.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateExists))

			By("HumioIngestToken: Checking we do not create a token secret")
			var allSecrets corev1.SecretList
			k8sClient.List(context.Background(), &allSecrets, client.InNamespace(fetchedIngestToken.Namespace))
			for _, secret := range allSecrets.Items {
				for _, owner := range secret.OwnerReferences {
					Expect(owner.Name).ShouldNot(BeIdenticalTo(fetchedIngestToken.Name))
				}
			}

			By("HumioIngestToken: Enabling token secret name successfully creates secret")
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, fetchedIngestToken)
				fetchedIngestToken.Spec.TokenSecretName = "target-secret-2"
				return k8sClient.Update(context.Background(), fetchedIngestToken)
			}, testTimeout, testInterval).Should(Succeed())
			ingestTokenSecret = &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(
					context.Background(),
					types.NamespacedName{
						Namespace: fetchedIngestToken.Namespace,
						Name:      fetchedIngestToken.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, testInterval).Should(Succeed())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}

			By("HumioIngestToken: Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetchedIngestToken)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetchedIngestToken)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			By("HumioRepository: Should handle repository correctly")
			key = types.NamespacedName{
				Name:      "humiorepository",
				Namespace: "default",
			}

			toCreateRepository := &humiov1alpha1.HumioRepository{
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

			By("HumioRepository: Creating the repository successfully")
			Expect(k8sClient.Create(context.Background(), toCreateRepository)).Should(Succeed())

			fetchedRepository := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetchedRepository)
				return fetchedRepository.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			initialRepository, err := humioClient.GetRepository(toCreateRepository)
			Expect(err).To(BeNil())
			Expect(initialRepository).ToNot(BeNil())

			expectedInitialRepository := repositoryExpectation{
				Name:                   toCreateRepository.Spec.Name,
				Description:            toCreateRepository.Spec.Description,
				RetentionDays:          float64(toCreateRepository.Spec.Retention.TimeInDays),
				IngestRetentionSizeGB:  float64(toCreateRepository.Spec.Retention.IngestSizeInGB),
				StorageRetentionSizeGB: float64(toCreateRepository.Spec.Retention.StorageSizeInGB),
			}
			Eventually(func() repositoryExpectation {
				initialRepository, err := humioClient.GetRepository(fetchedRepository)
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

			By("HumioRepository: Updating the repository successfully")
			updatedDescription := "important description - now updated"
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, fetchedRepository)
				fetchedRepository.Spec.Description = updatedDescription
				return k8sClient.Update(context.Background(), fetchedRepository)
			}, testTimeout, testInterval).Should(Succeed())

			updatedRepository, err := humioClient.GetRepository(fetchedRepository)
			Expect(err).To(BeNil())
			Expect(updatedRepository).ToNot(BeNil())

			expectedUpdatedRepository := repositoryExpectation{
				Name:                   fetchedRepository.Spec.Name,
				Description:            updatedDescription,
				RetentionDays:          float64(fetchedRepository.Spec.Retention.TimeInDays),
				IngestRetentionSizeGB:  float64(fetchedRepository.Spec.Retention.IngestSizeInGB),
				StorageRetentionSizeGB: float64(fetchedRepository.Spec.Retention.StorageSizeInGB),
			}
			Eventually(func() repositoryExpectation {
				updatedRepository, err := humioClient.GetRepository(fetchedRepository)
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

			By("HumioRepository: Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetchedRepository)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetchedRepository)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			By("HumioView: Should handle view correctly")
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

			By("HumioView: Creating the repository successfully")
			Expect(k8sClient.Create(context.Background(), repositoryToCreate)).Should(Succeed())

			fetchedRepo := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), viewKey, fetchedRepo)
				return fetchedRepo.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			By("HumioView: Creating the view successfully in k8s")
			Expect(k8sClient.Create(context.Background(), viewToCreate)).Should(Succeed())

			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))

			By("HumioView: Creating the view successfully in Humio")
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

			By("HumioView: Updating the view successfully in k8s")
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

			By("HumioView: Updating the view successfully in Humio")
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

			By("HumioView: Successfully deleting the view")
			Expect(k8sClient.Delete(context.Background(), fetchedView)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), viewKey, fetchedView)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			By("HumioView: Successfully deleting the repo")
			Expect(k8sClient.Delete(context.Background(), fetchedRepo)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), viewKey, fetchedRepo)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			By("HumioParser: Should handle parser correctly")
			spec := humiov1alpha1.HumioParserSpec{
				ManagedClusterName: "humiocluster-shared",
				Name:               "example-parser",
				RepositoryName:     "humio",
				ParserScript:       "kvParse()",
				TagFields:          []string{"@somefield"},
				TestData:           []string{"this is an example of rawstring"},
			}

			key = types.NamespacedName{
				Name:      "humioparser",
				Namespace: "default",
			}

			toCreateParser := &humiov1alpha1.HumioParser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: spec,
			}

			By("HumioParser: Creating the parser successfully")
			Expect(k8sClient.Create(context.Background(), toCreateParser)).Should(Succeed())

			fetchedParser := &humiov1alpha1.HumioParser{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetchedParser)
				return fetchedParser.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioParserStateExists))

			initialParser, err := humioClient.GetParser(toCreateParser)
			Expect(err).To(BeNil())
			Expect(initialParser).ToNot(BeNil())

			expectedInitialParser := humioapi.Parser{
				Name:      spec.Name,
				Script:    spec.ParserScript,
				TagFields: spec.TagFields,
				Tests:     helpers.MapTests(spec.TestData, helpers.ToTestCase),
			}
			Expect(reflect.DeepEqual(*initialParser, expectedInitialParser)).To(BeTrue())

			By("HumioParser: Updating the parser successfully")
			updatedScript := "kvParse() | updated"
			Eventually(func() error {
				k8sClient.Get(context.Background(), key, fetchedParser)
				fetchedParser.Spec.ParserScript = updatedScript
				return k8sClient.Update(context.Background(), fetchedParser)
			}, testTimeout, testInterval).Should(Succeed())

			updatedParser, err := humioClient.GetParser(fetchedParser)
			Expect(err).To(BeNil())
			Expect(updatedParser).ToNot(BeNil())

			expectedUpdatedParser := humioapi.Parser{
				Name:      spec.Name,
				Script:    updatedScript,
				TagFields: spec.TagFields,
				Tests:     helpers.MapTests(spec.TestData, helpers.ToTestCase),
			}
			Eventually(func() humioapi.Parser {
				updatedParser, err := humioClient.GetParser(fetchedParser)
				if err != nil {
					return humioapi.Parser{}
				}
				return *updatedParser
			}, testTimeout, testInterval).Should(Equal(expectedUpdatedParser))

			By("HumioParser: Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetchedParser)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetchedParser)
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			By("HumioExternalCluster: Should handle externalcluster correctly")
			key = types.NamespacedName{
				Name:      "humioexternalcluster",
				Namespace: "default",
			}
			protocol := "http"
			if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
				protocol = "https"
			}

			toCreateExternalCluster := &humiov1alpha1.HumioExternalCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioExternalClusterSpec{
					Url:                fmt.Sprintf("%s://humiocluster-shared.default:8080/", protocol),
					APITokenSecretName: "humiocluster-shared-admin-token",
					Insecure:           true,
				},
			}

			By("HumioExternalCluster: Creating the external cluster successfully")
			Expect(k8sClient.Create(context.Background(), toCreateExternalCluster)).Should(Succeed())

			By("HumioExternalCluster: Confirming external cluster gets marked as ready")
			fetchedExternalCluster := &humiov1alpha1.HumioExternalCluster{}
			Eventually(func() string {
				k8sClient.Get(context.Background(), key, fetchedExternalCluster)
				return fetchedExternalCluster.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioExternalClusterStateReady))

			By("HumioExternalCluster: Successfully deleting it")
			Expect(k8sClient.Delete(context.Background(), fetchedExternalCluster)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), key, fetchedExternalCluster)
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
