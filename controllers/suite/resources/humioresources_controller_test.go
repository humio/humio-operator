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
	"context"
	"fmt"
	"github.com/humio/humio-operator/controllers/suite"
	"github.com/humio/humio-operator/pkg/humio"
	"net/http"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Humio Resources Controllers", func() {

	BeforeEach(func() {
		// failed test runs that don't clean up leave resources behind.
		humioClient.ClearHumioClientConnections()
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
		humioClient.ClearHumioClientConnections()
	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Humio Ingest Token", func() {
		It("should handle ingest token with target secret correctly", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humioingesttoken-with-token-secret",
				Namespace: clusterKey.Namespace,
			}

			initialParserName := "json"
			toCreateIngestToken := &humiov1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioIngestTokenSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ParserName:         initialParserName,
					RepositoryName:     "humio",
					TokenSecretName:    "target-secret-1",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Creating the ingest token with token secret successfully")
			Expect(k8sClient.Create(ctx, toCreateIngestToken)).Should(Succeed())

			fetchedIngestToken := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedIngestToken)
				return fetchedIngestToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateExists))

			ingestTokenSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: key.Namespace,
						Name:      toCreateIngestToken.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}
			Expect(ingestTokenSecret.OwnerReferences).Should(HaveLen(1))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Checking correct parser assigned to ingest token")
			var humioIngestToken *humioapi.IngestToken
			Eventually(func() string {
				humioIngestToken, err = humioClient.GetIngestToken(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedIngestToken)
				if humioIngestToken != nil {
					return humioIngestToken.AssignedParser
				}
				return "nil"
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(initialParserName))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Updating parser for ingest token")
			updatedParserName := "accesslog"
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedIngestToken)
				fetchedIngestToken.Spec.ParserName = updatedParserName
				return k8sClient.Update(ctx, fetchedIngestToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() string {
				humioIngestToken, err = humioClient.GetIngestToken(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedIngestToken)
				if humioIngestToken != nil {
					return humioIngestToken.AssignedParser
				}
				return "nil"
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedParserName))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Deleting ingest token secret successfully adds back secret")
			Expect(
				k8sClient.Delete(
					ctx,
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
					ctx,
					types.NamespacedName{
						Namespace: key.Namespace,
						Name:      toCreateIngestToken.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedIngestToken)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedIngestToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle ingest without token target secret correctly", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humioingesttoken-without-token-secret",
				Namespace: clusterKey.Namespace,
			}

			toCreateIngestToken := &humiov1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioIngestTokenSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ParserName:         "accesslog",
					RepositoryName:     "humio",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Creating the ingest token without token secret successfully")
			Expect(k8sClient.Create(ctx, toCreateIngestToken)).Should(Succeed())

			fetchedIngestToken := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedIngestToken)
				return fetchedIngestToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Checking we do not create a token secret")
			var allSecrets corev1.SecretList
			k8sClient.List(ctx, &allSecrets, client.InNamespace(fetchedIngestToken.Namespace))
			for _, secret := range allSecrets.Items {
				for _, owner := range secret.OwnerReferences {
					Expect(owner.Name).ShouldNot(BeIdenticalTo(fetchedIngestToken.Name))
				}
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Enabling token secret name successfully creates secret")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedIngestToken)
				fetchedIngestToken.Spec.TokenSecretName = "target-secret-2"
				fetchedIngestToken.Spec.TokenSecretLabels = map[string]string{
					"custom-label": "custom-value",
				}
				return k8sClient.Update(ctx, fetchedIngestToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			ingestTokenSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(
					ctx,
					types.NamespacedName{
						Namespace: fetchedIngestToken.Namespace,
						Name:      fetchedIngestToken.Spec.TokenSecretName,
					},
					ingestTokenSecret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(ingestTokenSecret.Labels).Should(HaveKeyWithValue("custom-label", "custom-value"))

			if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
				Expect(string(ingestTokenSecret.Data["token"])).To(Equal("mocktoken"))
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedIngestToken)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedIngestToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

		})

		It("Creating ingest token pointing to non-existent managed cluster", func() {
			ctx := context.Background()
			keyErr := types.NamespacedName{
				Name:      "humioingesttoken-non-existent-managed-cluster",
				Namespace: clusterKey.Namespace,
			}
			toCreateIngestToken := &humiov1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyErr.Name,
					Namespace: keyErr.Namespace,
				},
				Spec: humiov1alpha1.HumioIngestTokenSpec{
					ManagedClusterName: "non-existent-managed-cluster",
					Name:               "ingesttokenname",
					ParserName:         "accesslog",
					RepositoryName:     "humio",
					TokenSecretName:    "thissecretname",
				},
			}
			Expect(k8sClient.Create(ctx, toCreateIngestToken)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioIngestToken: Validates resource enters state %s", humiov1alpha1.HumioIngestTokenStateConfigError))
			fetchedIngestToken := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				k8sClient.Get(ctx, keyErr, fetchedIngestToken)
				return fetchedIngestToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedIngestToken)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyErr, fetchedIngestToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Creating ingest token pointing to non-existent external cluster")
			keyErr = types.NamespacedName{
				Name:      "humioingesttoken-non-existent-external-cluster",
				Namespace: clusterKey.Namespace,
			}
			toCreateIngestToken = &humiov1alpha1.HumioIngestToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyErr.Name,
					Namespace: keyErr.Namespace,
				},
				Spec: humiov1alpha1.HumioIngestTokenSpec{
					ExternalClusterName: "non-existent-external-cluster",
					Name:                "ingesttokenname",
					ParserName:          "accesslog",
					RepositoryName:      "humio",
					TokenSecretName:     "thissecretname",
				},
			}
			Expect(k8sClient.Create(ctx, toCreateIngestToken)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioIngestToken: Validates resource enters state %s", humiov1alpha1.HumioIngestTokenStateConfigError))
			fetchedIngestToken = &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				k8sClient.Get(ctx, keyErr, fetchedIngestToken)
				return fetchedIngestToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedIngestToken)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyErr, fetchedIngestToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Repository and View", func() {
		It("should handle resources correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Should handle repository correctly")
			key := types.NamespacedName{
				Name:      "humiorepository",
				Namespace: clusterKey.Namespace,
			}

			toCreateRepository := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioRepositorySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "example-repository",
					Description:        "important description",
					Retention: humiov1alpha1.HumioRetention{
						TimeInDays:      30,
						IngestSizeInGB:  5,
						StorageSizeInGB: 1,
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Creating the repository successfully")
			Expect(k8sClient.Create(ctx, toCreateRepository)).Should(Succeed())

			fetchedRepository := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedRepository)
				return fetchedRepository.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			var initialRepository *humioapi.Repository
			Eventually(func() error {
				initialRepository, err = humioClient.GetRepository(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateRepository)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(initialRepository).ToNot(BeNil())

			expectedInitialRepository := repositoryExpectation{
				Name:                   toCreateRepository.Spec.Name,
				Description:            toCreateRepository.Spec.Description,
				RetentionDays:          float64(toCreateRepository.Spec.Retention.TimeInDays),
				IngestRetentionSizeGB:  float64(toCreateRepository.Spec.Retention.IngestSizeInGB),
				StorageRetentionSizeGB: float64(toCreateRepository.Spec.Retention.StorageSizeInGB),
			}
			Eventually(func() repositoryExpectation {
				initialRepository, err := humioClient.GetRepository(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedRepository)
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
			}, testTimeout, suite.TestInterval).Should(Equal(expectedInitialRepository))

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Updating the repository successfully")
			updatedDescription := "important description - now updated"
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedRepository)
				fetchedRepository.Spec.Description = updatedDescription
				return k8sClient.Update(ctx, fetchedRepository)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			var updatedRepository *humioapi.Repository
			Eventually(func() error {
				updatedRepository, err = humioClient.GetRepository(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedRepository)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(updatedRepository).ToNot(BeNil())

			expectedUpdatedRepository := repositoryExpectation{
				Name:                   fetchedRepository.Spec.Name,
				Description:            updatedDescription,
				RetentionDays:          float64(fetchedRepository.Spec.Retention.TimeInDays),
				IngestRetentionSizeGB:  float64(fetchedRepository.Spec.Retention.IngestSizeInGB),
				StorageRetentionSizeGB: float64(fetchedRepository.Spec.Retention.StorageSizeInGB),
			}
			Eventually(func() repositoryExpectation {
				updatedRepository, err := humioClient.GetRepository(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedRepository)
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
			}, testTimeout, suite.TestInterval).Should(Equal(expectedUpdatedRepository))

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedRepository)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedRepository)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Should handle view correctly")
			viewKey := types.NamespacedName{
				Name:      "humioview",
				Namespace: clusterKey.Namespace,
			}

			repositoryToCreate := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioRepositorySpec{
					ManagedClusterName: clusterKey.Name,
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
					ManagedClusterName: clusterKey.Name,
					Name:               "example-view",
					Connections:        connections,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Creating the repository successfully")
			Expect(k8sClient.Create(ctx, repositoryToCreate)).Should(Succeed())

			fetchedRepo := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				k8sClient.Get(ctx, viewKey, fetchedRepo)
				return fetchedRepo.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Creating the view successfully in k8s")
			Expect(k8sClient.Create(ctx, viewToCreate)).Should(Succeed())

			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				k8sClient.Get(ctx, viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Creating the view successfully in Humio")
			var initialView *humioapi.View
			Eventually(func() error {
				initialView, err = humioClient.GetView(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, viewToCreate)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(initialView).ToNot(BeNil())

			expectedInitialView := humioapi.View{
				Name:        viewToCreate.Spec.Name,
				Connections: viewToCreate.GetViewConnections(),
			}

			Eventually(func() humioapi.View {
				initialView, err := humioClient.GetView(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedView)
				if err != nil {
					return humioapi.View{}
				}
				return *initialView
			}, testTimeout, suite.TestInterval).Should(Equal(expectedInitialView))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Updating the view successfully in k8s")
			updatedConnections := []humiov1alpha1.HumioViewConnection{
				{
					RepositoryName: "humio",
					Filter:         "*",
				},
			}
			Eventually(func() error {
				k8sClient.Get(ctx, viewKey, fetchedView)
				fetchedView.Spec.Connections = updatedConnections
				return k8sClient.Update(ctx, fetchedView)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Updating the view successfully in Humio")
			var updatedView *humioapi.View
			Eventually(func() error {
				updatedView, err = humioClient.GetView(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedView)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(updatedView).ToNot(BeNil())

			expectedUpdatedView := humioapi.View{
				Name:        viewToCreate.Spec.Name,
				Connections: fetchedView.GetViewConnections(),
			}
			Eventually(func() humioapi.View {
				updatedView, err := humioClient.GetView(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedView)
				if err != nil {
					return humioapi.View{}
				}
				return *updatedView
			}, testTimeout, suite.TestInterval).Should(Equal(expectedUpdatedView))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Successfully deleting the view")
			Expect(k8sClient.Delete(ctx, fetchedView)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, viewKey, fetchedView)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Successfully deleting the repo")
			Expect(k8sClient.Delete(ctx, fetchedRepo)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, viewKey, fetchedRepo)
				suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("Waiting for repo to get deleted. Current status: %#+v", fetchedRepo.Status))
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

		})
	})

	Context("Humio Parser", func() {
		It("HumioParser: Should handle parser correctly", func() {
			ctx := context.Background()
			spec := humiov1alpha1.HumioParserSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-parser",
				RepositoryName:     "humio",
				ParserScript:       "kvParse()",
				TagFields:          []string{"@somefield"},
				TestData:           []string{"this is an example of rawstring"},
			}

			key := types.NamespacedName{
				Name:      "humioparser",
				Namespace: clusterKey.Namespace,
			}

			toCreateParser := &humiov1alpha1.HumioParser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: spec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioParser: Creating the parser successfully")
			Expect(k8sClient.Create(ctx, toCreateParser)).Should(Succeed())

			fetchedParser := &humiov1alpha1.HumioParser{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedParser)
				return fetchedParser.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioParserStateExists))

			var initialParser *humioapi.Parser
			Eventually(func() error {
				initialParser, err = humioClient.GetParser(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateParser)

				// Ignore the ID when comparing parser content
				initialParser.ID = ""

				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(initialParser).ToNot(BeNil())

			expectedInitialParser := humioapi.Parser{
				Name:      spec.Name,
				Script:    spec.ParserScript,
				TagFields: spec.TagFields,
				Tests:     spec.TestData,
			}
			Expect(*initialParser).To(Equal(expectedInitialParser))

			suite.UsingClusterBy(clusterKey.Name, "HumioParser: Updating the parser successfully")
			updatedScript := "kvParse() | updated"
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedParser)
				fetchedParser.Spec.ParserScript = updatedScript
				return k8sClient.Update(ctx, fetchedParser)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			var updatedParser *humioapi.Parser
			Eventually(func() error {
				updatedParser, err = humioClient.GetParser(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedParser)

				// Ignore the ID when comparing parser content
				updatedParser.ID = ""

				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(updatedParser).ToNot(BeNil())

			expectedUpdatedParser := humioapi.Parser{
				Name:      spec.Name,
				Script:    updatedScript,
				TagFields: spec.TagFields,
				Tests:     spec.TestData,
			}
			Eventually(func() humioapi.Parser {
				updatedParser, err := humioClient.GetParser(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedParser)
				if err != nil {
					return humioapi.Parser{}
				}

				// Ignore the ID when comparing parser content
				updatedParser.ID = ""

				return *updatedParser
			}, testTimeout, suite.TestInterval).Should(Equal(expectedUpdatedParser))

			suite.UsingClusterBy(clusterKey.Name, "HumioParser: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedParser)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedParser)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

		})
	})

	Context("Humio External Cluster", func() {
		It("should handle resources correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioExternalCluster: Should handle externalcluster correctly")
			key := types.NamespacedName{
				Name:      "humioexternalcluster",
				Namespace: clusterKey.Namespace,
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
					Url:                fmt.Sprintf("%s://%s.%s:8080/", protocol, clusterKey.Name, clusterKey.Namespace),
					APITokenSecretName: fmt.Sprintf("%s-admin-token", clusterKey.Name),
				},
			}

			if protocol == "https" {
				toCreateExternalCluster.Spec.CASecretName = clusterKey.Name

			} else {
				toCreateExternalCluster.Spec.Insecure = true
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioExternalCluster: Creating the external cluster successfully")
			Expect(k8sClient.Create(ctx, toCreateExternalCluster)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioExternalCluster: Confirming external cluster gets marked as ready")
			fetchedExternalCluster := &humiov1alpha1.HumioExternalCluster{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedExternalCluster)
				return fetchedExternalCluster.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioExternalClusterStateReady))

			suite.UsingClusterBy(clusterKey.Name, "HumioExternalCluster: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedExternalCluster)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedExternalCluster)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio resources errors", func() {
		It("HumioParser: Creating ingest token pointing to non-existent managed cluster", func() {
			ctx := context.Background()
			keyErr := types.NamespacedName{
				Name:      "humioparser-non-existent-managed-cluster",
				Namespace: clusterKey.Namespace,
			}
			toCreateParser := &humiov1alpha1.HumioParser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyErr.Name,
					Namespace: keyErr.Namespace,
				},
				Spec: humiov1alpha1.HumioParserSpec{
					ManagedClusterName: "non-existent-managed-cluster",
					Name:               "parsername",
					ParserScript:       "kvParse()",
					RepositoryName:     "humio",
				},
			}
			Expect(k8sClient.Create(ctx, toCreateParser)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioParser: Validates resource enters state %s", humiov1alpha1.HumioParserStateConfigError))
			fetchedParser := &humiov1alpha1.HumioParser{}
			Eventually(func() string {
				k8sClient.Get(ctx, keyErr, fetchedParser)
				return fetchedParser.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioParserStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioParser: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedParser)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyErr, fetchedParser)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioParser: Creating ingest token pointing to non-existent external cluster", func() {
			ctx := context.Background()
			keyErr := types.NamespacedName{
				Name:      "humioparser-non-existent-external-cluster",
				Namespace: clusterKey.Namespace,
			}
			toCreateParser := &humiov1alpha1.HumioParser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyErr.Name,
					Namespace: keyErr.Namespace,
				},
				Spec: humiov1alpha1.HumioParserSpec{
					ExternalClusterName: "non-existent-external-cluster",
					Name:                "parsername",
					ParserScript:        "kvParse()",
					RepositoryName:      "humio",
				},
			}
			Expect(k8sClient.Create(ctx, toCreateParser)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioParser: Validates resource enters state %s", humiov1alpha1.HumioParserStateConfigError))
			fetchedParser := &humiov1alpha1.HumioParser{}
			Eventually(func() string {
				k8sClient.Get(ctx, keyErr, fetchedParser)
				return fetchedParser.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioParserStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioParser: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedParser)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyErr, fetchedParser)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioRepository: Creating repository pointing to non-existent managed cluster", func() {
			ctx := context.Background()
			keyErr := types.NamespacedName{
				Name:      "humiorepository-non-existent-managed-cluster",
				Namespace: clusterKey.Namespace,
			}
			toCreateRepository := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyErr.Name,
					Namespace: keyErr.Namespace,
				},
				Spec: humiov1alpha1.HumioRepositorySpec{
					ManagedClusterName: "non-existent-managed-cluster",
					Name:               "parsername",
				},
			}
			Expect(k8sClient.Create(ctx, toCreateRepository)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioRepository: Validates resource enters state %s", humiov1alpha1.HumioRepositoryStateConfigError))
			fetchedRepository := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				k8sClient.Get(ctx, keyErr, fetchedRepository)
				return fetchedRepository.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedRepository)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyErr, fetchedRepository)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioRepository: Creating repository pointing to non-existent external cluster", func() {
			ctx := context.Background()
			keyErr := types.NamespacedName{
				Name:      "humiorepository-non-existent-external-cluster",
				Namespace: clusterKey.Namespace,
			}
			toCreateRepository := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyErr.Name,
					Namespace: keyErr.Namespace,
				},
				Spec: humiov1alpha1.HumioRepositorySpec{
					ExternalClusterName: "non-existent-external-cluster",
					Name:                "parsername",
				},
			}
			Expect(k8sClient.Create(ctx, toCreateRepository)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioRepository: Validates resource enters state %s", humiov1alpha1.HumioRepositoryStateConfigError))
			fetchedRepository := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				k8sClient.Get(ctx, keyErr, fetchedRepository)
				return fetchedRepository.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedRepository)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyErr, fetchedRepository)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioView: Creating repository pointing to non-existent managed cluster", func() {
			ctx := context.Background()
			keyErr := types.NamespacedName{
				Name:      "humioview-non-existent-managed-cluster",
				Namespace: clusterKey.Namespace,
			}
			toCreateView := &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyErr.Name,
					Namespace: keyErr.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ManagedClusterName: "non-existent-managed-cluster",
					Name:               "thisname",
					Connections: []humiov1alpha1.HumioViewConnection{
						{
							RepositoryName: "humio",
							Filter:         "*",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, toCreateView)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioView: Validates resource enters state %s", humiov1alpha1.HumioViewStateConfigError))
			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				k8sClient.Get(ctx, keyErr, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedView)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyErr, fetchedView)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioView: Creating repository pointing to non-existent external cluster", func() {
			ctx := context.Background()
			keyErr := types.NamespacedName{
				Name:      "humioview-non-existent-external-cluster",
				Namespace: clusterKey.Namespace,
			}
			toCreateView := &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyErr.Name,
					Namespace: keyErr.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ExternalClusterName: "non-existent-external-cluster",
					Name:                "thisname",
					Connections: []humiov1alpha1.HumioViewConnection{
						{
							RepositoryName: "humio",
							Filter:         "*",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, toCreateView)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioView: Validates resource enters state %s", humiov1alpha1.HumioViewStateConfigError))
			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				k8sClient.Get(ctx, keyErr, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateConfigError))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedView)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyErr, fetchedView)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Action", func() {
		It("should handle email action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle action correctly")
			emailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-action",
				ViewName:           "humio",
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{"example@example.com"},
				},
			}

			key := types.NamespacedName{
				Name:      "humioaction",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: emailActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the action successfully")
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			originalAction, err := humio.ActionFromActionCR(toCreateAction)
			Expect(err).To(BeNil())
			Expect(action.Name).To(Equal(originalAction.Name))

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.EmailProperties.Recipients).To(Equal(toCreateAction.Spec.EmailProperties.Recipients))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.EmailProperties.Recipients = []string{"updated@example.com"}
			updatedAction.Spec.EmailProperties.BodyTemplate = "updated body template"
			updatedAction.Spec.EmailProperties.SubjectTemplate = "updated subject template"

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.EmailProperties = updatedAction.Spec.EmailProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the action update succeeded")
			var expectedUpdatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(err).To(BeNil())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the action matches the expected")
			verifiedAction, err := humio.ActionFromActionCR(updatedAction)
			Expect(err).To(BeNil())
			Expect(verifiedAction).ToNot(BeNil())
			Eventually(func() humioapi.EmailAction {
				updatedAction, err := humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return humioapi.EmailAction{}
				}
				return updatedAction.EmailAction
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(verifiedAction.EmailAction))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle humio repo action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle humio repo action correctly")
			humioRepoActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-humio-repo-action",
				ViewName:           "humio",
				HumioRepositoryProperties: &humiov1alpha1.HumioActionRepositoryProperties{
					IngestToken: "some-token",
				},
			}

			key := types.NamespacedName{
				Name:      "humioaction",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humioRepoActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the humio repo action successfully")
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			action := &humioapi.Action{}
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			originalAction, err := humio.ActionFromActionCR(toCreateAction)
			Expect(err).To(BeNil())
			Expect(action.Name).To(Equal(originalAction.Name))

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.HumioRepositoryProperties.IngestToken).To(Equal(toCreateAction.Spec.HumioRepositoryProperties.IngestToken))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the humio repo action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.HumioRepositoryProperties.IngestToken = "updated-token"

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the humio repo action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.HumioRepositoryProperties = updatedAction.Spec.HumioRepositoryProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the humio repo action update succeeded")
			var expectedUpdatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the humio repo action matches the expected")
			verifiedAction, err := humio.ActionFromActionCR(updatedAction)
			Expect(err).To(BeNil())
			Expect(verifiedAction).ToNot(BeNil())
			Eventually(func() humioapi.HumioRepoAction {
				updatedAction, err := humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return humioapi.HumioRepoAction{}
				}
				return updatedAction.HumioRepoAction
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(verifiedAction.HumioRepoAction))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle ops genie action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle ops genie action correctly")
			opsGenieActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-ops-genie-action",
				ViewName:           "humio",
				OpsGenieProperties: &humiov1alpha1.HumioActionOpsGenieProperties{
					GenieKey: "somegeniekey",
					ApiUrl:   "https://humio.com",
				},
			}

			key := types.NamespacedName{
				Name:      "humio-ops-genie-action",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: opsGenieActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the ops genie action successfully")
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			originalAction, err := humio.ActionFromActionCR(toCreateAction)
			Expect(err).To(BeNil())
			Expect(action.Name).To(Equal(originalAction.Name))

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.OpsGenieProperties.GenieKey).To(Equal(toCreateAction.Spec.OpsGenieProperties.GenieKey))
			Expect(createdAction.Spec.OpsGenieProperties.ApiUrl).To(Equal(toCreateAction.Spec.OpsGenieProperties.ApiUrl))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the ops genie action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.OpsGenieProperties.GenieKey = "updatedgeniekey"
			updatedAction.Spec.OpsGenieProperties.ApiUrl = "https://example.com"

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the ops genie action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.OpsGenieProperties = updatedAction.Spec.OpsGenieProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the ops genie action update succeeded")
			var expectedUpdatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the ops genie action matches the expected")
			verifiedAction, err := humio.ActionFromActionCR(updatedAction)
			Expect(err).To(BeNil())
			Expect(verifiedAction).ToNot(BeNil())
			Eventually(func() humioapi.OpsGenieAction {
				updatedAction, err := humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return humioapi.OpsGenieAction{}
				}
				return updatedAction.OpsGenieAction
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(verifiedAction.OpsGenieAction))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle pagerduty action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle pagerduty action correctly")
			pagerDutyActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-pagerduty-action",
				ViewName:           "humio",
				PagerDutyProperties: &humiov1alpha1.HumioActionPagerDutyProperties{
					Severity:   "critical",
					RoutingKey: "someroutingkey",
				},
			}

			key := types.NamespacedName{
				Name:      "humio-pagerduty-action",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: pagerDutyActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the pagerduty action successfully")
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			originalAction, err := humio.ActionFromActionCR(toCreateAction)
			Expect(err).To(BeNil())
			Expect(action.Name).To(Equal(originalAction.Name))

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.PagerDutyProperties.Severity).To(Equal(toCreateAction.Spec.PagerDutyProperties.Severity))
			Expect(createdAction.Spec.PagerDutyProperties.RoutingKey).To(Equal(toCreateAction.Spec.PagerDutyProperties.RoutingKey))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the pagerduty action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.PagerDutyProperties.Severity = "error"
			updatedAction.Spec.PagerDutyProperties.RoutingKey = "updatedroutingkey"

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the pagerduty action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.PagerDutyProperties = updatedAction.Spec.PagerDutyProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the pagerduty action update succeeded")
			var expectedUpdatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the pagerduty action matches the expected")
			verifiedAction, err := humio.ActionFromActionCR(updatedAction)
			Expect(err).To(BeNil())
			Expect(verifiedAction).ToNot(BeNil())
			Eventually(func() humioapi.PagerDutyAction {
				updatedAction, err := humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return humioapi.PagerDutyAction{}
				}
				return updatedAction.PagerDutyAction
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(verifiedAction.PagerDutyAction))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle slack post message action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle slack post message action correctly")
			slackPostMessageActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-slack-post-message-action",
				ViewName:           "humio",
				SlackPostMessageProperties: &humiov1alpha1.HumioActionSlackPostMessageProperties{
					ApiToken: "some-token",
					Channels: []string{"#some-channel"},
					Fields: map[string]string{
						"some": "key",
					},
				},
			}

			key := types.NamespacedName{
				Name:      "humio-slack-post-message-action",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: slackPostMessageActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the slack post message action successfully")
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			originalAction, err := humio.ActionFromActionCR(toCreateAction)
			Expect(err).To(BeNil())
			Expect(action.Name).To(Equal(originalAction.Name))

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.SlackPostMessageProperties.ApiToken).To(Equal(toCreateAction.Spec.SlackPostMessageProperties.ApiToken))
			Expect(createdAction.Spec.SlackPostMessageProperties.Channels).To(Equal(toCreateAction.Spec.SlackPostMessageProperties.Channels))
			Expect(createdAction.Spec.SlackPostMessageProperties.Fields).To(Equal(toCreateAction.Spec.SlackPostMessageProperties.Fields))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the slack post message action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.SlackPostMessageProperties.ApiToken = "updated-token"
			updatedAction.Spec.SlackPostMessageProperties.Channels = []string{"#some-channel", "#other-channel"}
			updatedAction.Spec.SlackPostMessageProperties.Fields = map[string]string{
				"some": "updatedkey",
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the slack post message action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.SlackPostMessageProperties = updatedAction.Spec.SlackPostMessageProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack post message action update succeeded")
			var expectedUpdatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack post message action matches the expected")
			verifiedAction, err := humio.ActionFromActionCR(updatedAction)
			Expect(err).To(BeNil())
			Expect(verifiedAction).ToNot(BeNil())
			Eventually(func() humioapi.SlackPostMessageAction {
				updatedAction, err := humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return humioapi.SlackPostMessageAction{}
				}
				return updatedAction.SlackPostMessageAction
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(verifiedAction.SlackPostMessageAction))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle slack action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle slack action correctly")
			slackActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-slack-action",
				ViewName:           "humio",
				SlackProperties: &humiov1alpha1.HumioActionSlackProperties{
					Url: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
					Fields: map[string]string{
						"some": "key",
					},
				},
			}

			key := types.NamespacedName{
				Name:      "humio-slack-action",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: slackActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the slack action successfully")
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			originalAction, err := humio.ActionFromActionCR(toCreateAction)
			Expect(err).To(BeNil())
			Expect(action.Name).To(Equal(originalAction.Name))

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.SlackProperties.Url).To(Equal(toCreateAction.Spec.SlackProperties.Url))
			Expect(createdAction.Spec.SlackProperties.Fields).To(Equal(toCreateAction.Spec.SlackProperties.Fields))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the slack action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.SlackProperties.Url = "https://hooks.slack.com/services/T00000000/B00000000/YYYYYYYYYYYYYYYYYYYYYYYY"
			updatedAction.Spec.SlackProperties.Fields = map[string]string{
				"some": "updatedkey",
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the slack action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.SlackProperties = updatedAction.Spec.SlackProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack action update succeeded")
			var expectedUpdatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack action matches the expected")
			verifiedAction, err := humio.ActionFromActionCR(updatedAction)
			Expect(err).To(BeNil())
			Expect(verifiedAction).ToNot(BeNil())
			Eventually(func() humioapi.SlackAction {
				updatedAction, err := humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return humioapi.SlackAction{}
				}
				return updatedAction.SlackAction
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(verifiedAction.SlackAction))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

		})

		It("should handle victor ops action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle victor ops action correctly")
			victorOpsActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-victor-ops-action",
				ViewName:           "humio",
				VictorOpsProperties: &humiov1alpha1.HumioActionVictorOpsProperties{
					MessageType: "critical",
					NotifyUrl:   "https://alert.victorops.com/integrations/0000/alert/0000/routing_key",
				},
			}

			key := types.NamespacedName{
				Name:      "humio-victor-ops-action",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: victorOpsActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the victor ops action successfully")
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			originalAction, err := humio.ActionFromActionCR(toCreateAction)
			Expect(err).To(BeNil())
			Expect(action.Name).To(Equal(originalAction.Name))

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.VictorOpsProperties.MessageType).To(Equal(toCreateAction.Spec.VictorOpsProperties.MessageType))
			Expect(createdAction.Spec.VictorOpsProperties.NotifyUrl).To(Equal(toCreateAction.Spec.VictorOpsProperties.NotifyUrl))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the victor ops action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.VictorOpsProperties.MessageType = "recovery"
			updatedAction.Spec.VictorOpsProperties.NotifyUrl = "https://alert.victorops.com/integrations/1111/alert/1111/routing_key"

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the victor ops action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.VictorOpsProperties = updatedAction.Spec.VictorOpsProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the victor ops action update succeeded")
			var expectedUpdatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the victor ops action matches the expected")
			verifiedAction, err := humio.ActionFromActionCR(updatedAction)
			Expect(err).To(BeNil())
			Expect(verifiedAction).ToNot(BeNil())
			Eventually(func() humioapi.VictorOpsAction {
				updatedAction, err := humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return humioapi.VictorOpsAction{}
				}
				return updatedAction.VictorOpsAction
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(verifiedAction.VictorOpsAction))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle web hook action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle web hook action correctly")
			webHookActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-webhook-action",
				ViewName:           "humio",
				WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{
					Headers:      map[string]string{"some": "header"},
					BodyTemplate: "body template",
					Method:       http.MethodPost,
					Url:          "https://example.com/some/api",
				},
			}

			key := types.NamespacedName{
				Name:      "humio-webhook-action",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: webHookActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the web hook action successfully")
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			originalAction, err := humio.ActionFromActionCR(toCreateAction)
			Expect(err).To(BeNil())
			Expect(action.Name).To(Equal(originalAction.Name))

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.WebhookProperties.Headers).To(Equal(toCreateAction.Spec.WebhookProperties.Headers))
			Expect(createdAction.Spec.WebhookProperties.BodyTemplate).To(Equal(toCreateAction.Spec.WebhookProperties.BodyTemplate))
			Expect(createdAction.Spec.WebhookProperties.Method).To(Equal(toCreateAction.Spec.WebhookProperties.Method))
			Expect(createdAction.Spec.WebhookProperties.Url).To(Equal(toCreateAction.Spec.WebhookProperties.Url))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the web hook action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.WebhookProperties.Headers = map[string]string{"updated": "header"}
			updatedAction.Spec.WebhookProperties.BodyTemplate = "updated template"
			updatedAction.Spec.WebhookProperties.Method = http.MethodPut
			updatedAction.Spec.WebhookProperties.Url = "https://example.com/some/updated/api"

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the web hook action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.WebhookProperties = updatedAction.Spec.WebhookProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the web hook action update succeeded")
			var expectedUpdatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the web hook action matches the expected")
			verifiedAction, err := humio.ActionFromActionCR(updatedAction)
			Expect(err).To(BeNil())
			Expect(verifiedAction).ToNot(BeNil())
			Eventually(func() humioapi.WebhookAction {
				updatedAction, err := humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return humioapi.WebhookAction{}
				}
				return updatedAction.WebhookAction
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(verifiedAction.WebhookAction))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioAction: Should deny improperly configured action with missing properties", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-webhook-action",
				Namespace: clusterKey.Namespace,
			}

			toCreateInvalidAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "example-invalid-action",
					ViewName:           "humio",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the invalid action")
			Expect(k8sClient.Create(ctx, toCreateInvalidAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateConfigError))

			var invalidAction *humioapi.Action
			Eventually(func() error {
				invalidAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateInvalidAction)
				return err
			}, testTimeout, suite.TestInterval).ShouldNot(Succeed())
			Expect(invalidAction).To(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioAction: Should deny improperly configured action with extra properties", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-webhook-action",
				Namespace: clusterKey.Namespace,
			}
			toCreateInvalidAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "example-invalid-action",
					ViewName:           "humio",
					WebhookProperties:  &humiov1alpha1.HumioActionWebhookProperties{},
					EmailProperties:    &humiov1alpha1.HumioActionEmailProperties{},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the invalid action")
			Expect(k8sClient.Create(ctx, toCreateInvalidAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateConfigError))

			var invalidAction *humioapi.Action
			Eventually(func() error {
				invalidAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateInvalidAction)
				return err
			}, testTimeout, suite.TestInterval).ShouldNot(Succeed())
			Expect(invalidAction).To(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioAction: HumioRepositoryProperties: Should support referencing secrets", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-repository-action-secret",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           "humio",
					HumioRepositoryProperties: &humiov1alpha1.HumioActionRepositoryProperties{
						IngestTokenSource: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "action-humio-repository-secret",
								},
								Key: "key",
							},
						},
					},
				},
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-humio-repository-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte("secret-token"),
				},
			}

			Expect(k8sClient.Create(ctx, secret)).Should(Succeed())
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.HumioRepositoryProperties.IngestToken).To(Equal("secret-token"))
		})

		It("HumioAction: OpsGenieProperties: Should support referencing secrets", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "genie-action-secret",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           "humio",
					OpsGenieProperties: &humiov1alpha1.HumioActionOpsGenieProperties{
						ApiUrl: "https://humio.com",
						GenieKeySource: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "action-genie-secret",
								},
								Key: "key",
							},
						},
					},
				},
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-genie-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte("secret-token"),
				},
			}

			Expect(k8sClient.Create(ctx, secret)).Should(Succeed())
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.OpsGenieProperties.GenieKey).To(Equal("secret-token"))
			Expect(createdAction.Spec.OpsGenieProperties.ApiUrl).To(Equal("https://humio.com"))
		})

		It("HumioAction: OpsGenieProperties: Should support direct genie key", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "genie-action-direct",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           "humio",
					OpsGenieProperties: &humiov1alpha1.HumioActionOpsGenieProperties{
						GenieKey: "direct-token",
						ApiUrl:   "https://humio.com",
					},
				},
			}

			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.OpsGenieProperties.GenieKey).To(Equal("direct-token"))
			Expect(createdAction.Spec.OpsGenieProperties.ApiUrl).To(Equal("https://humio.com"))
		})

		It("HumioAction: SlackPostMessageProperties: Should support referencing secrets", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-slack-post-message-action-secret",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           "humio",
					SlackPostMessageProperties: &humiov1alpha1.HumioActionSlackPostMessageProperties{
						ApiTokenSource: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "action-slack-post-secret",
								},
								Key: "key",
							},
						},
						Channels: []string{"#some-channel"},
						Fields: map[string]string{
							"some": "key",
						},
					},
				},
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-slack-post-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte("secret-token"),
				},
			}

			Expect(k8sClient.Create(ctx, secret)).Should(Succeed())
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.SlackPostMessageProperties.ApiToken).To(Equal("secret-token"))
		})

		It("HumioAction: SlackPostMessageProperties: Should support direct api token", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-slack-post-message-action-direct",
				Namespace: clusterKey.Namespace,
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           "humio",
					SlackPostMessageProperties: &humiov1alpha1.HumioActionSlackPostMessageProperties{
						ApiToken: "direct-token",
						Channels: []string{"#some-channel"},
						Fields: map[string]string{
							"some": "key",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action *humioapi.Action
			Eventually(func() error {
				action, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			createdAction, err := humio.CRActionFromAPIAction(action)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.SlackPostMessageProperties.ApiToken).To(Equal("direct-token"))
		})
	})

	Context("Humio Alert", func() {
		It("should handle alert action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Should handle alert correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action",
				ViewName:           "humio",
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{"example@example.com"},
				},
			}

			actionKey := types.NamespacedName{
				Name:      "humioaction",
				Namespace: clusterKey.Namespace,
			}

			toCreateDependentAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      actionKey.Name,
					Namespace: actionKey.Namespace,
				},
				Spec: dependentEmailActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Creating the action required by the alert successfully")
			Expect(k8sClient.Create(ctx, toCreateDependentAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, actionKey, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			alertSpec := humiov1alpha1.HumioAlertSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-alert",
				ViewName:           "humio",
				Query: humiov1alpha1.HumioQuery{
					QueryString: "#repo = test | count()",
					Start:       "24h",
				},
				ThrottleTimeMillis: 60000,
				Silenced:           false,
				Description:        "humio alert",
				Actions:            []string{toCreateDependentAction.Spec.Name},
				Labels:             []string{"some-label"},
			}

			key := types.NamespacedName{
				Name:      "humio-alert",
				Namespace: clusterKey.Namespace,
			}

			toCreateAlert := &humiov1alpha1.HumioAlert{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: alertSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Creating the alert successfully")
			Expect(k8sClient.Create(ctx, toCreateAlert)).Should(Succeed())

			fetchedAlert := &humiov1alpha1.HumioAlert{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAlert)
				return fetchedAlert.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioAlertStateExists))

			var alert *humioapi.Alert
			Eventually(func() error {
				alert, err = humioClient.GetAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(alert).ToNot(BeNil())

			var actionIdMap map[string]string
			Eventually(func() error {
				actionIdMap, err = humioClient.GetActionIDsMapForAlerts(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())

			originalAlert, err := humio.AlertTransform(toCreateAlert, actionIdMap)
			Expect(err).To(BeNil())
			Expect(alert.Name).To(Equal(originalAlert.Name))
			Expect(alert.Description).To(Equal(originalAlert.Description))
			Expect(alert.Actions).To(Equal(originalAlert.Actions))
			Expect(alert.Labels).To(Equal(originalAlert.Labels))
			Expect(alert.ThrottleTimeMillis).To(Equal(originalAlert.ThrottleTimeMillis))
			Expect(alert.Enabled).To(Equal(originalAlert.Enabled))
			Expect(alert.QueryString).To(Equal(originalAlert.QueryString))
			Expect(alert.QueryStart).To(Equal(originalAlert.QueryStart))

			createdAlert := toCreateAlert
			err = humio.AlertHydrate(createdAlert, alert, actionIdMap)
			Expect(err).To(BeNil())
			Expect(createdAlert.Spec).To(Equal(toCreateAlert.Spec))

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Updating the alert successfully")
			updatedAlert := toCreateAlert
			updatedAlert.Spec.Query.QueryString = "#repo = test | updated=true | count()"
			updatedAlert.Spec.ThrottleTimeMillis = 70000
			updatedAlert.Spec.Silenced = true
			updatedAlert.Spec.Description = "updated humio alert"
			updatedAlert.Spec.Actions = []string{toCreateDependentAction.Spec.Name}

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Waiting for the alert to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAlert)
				fetchedAlert.Spec.Query = updatedAlert.Spec.Query
				fetchedAlert.Spec.ThrottleTimeMillis = updatedAlert.Spec.ThrottleTimeMillis
				fetchedAlert.Spec.Silenced = updatedAlert.Spec.Silenced
				fetchedAlert.Spec.Description = updatedAlert.Spec.Description
				return k8sClient.Update(ctx, fetchedAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Verifying the alert update succeeded")
			var expectedUpdatedAlert *humioapi.Alert
			Eventually(func() error {
				expectedUpdatedAlert, err = humioClient.GetAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAlert).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Verifying the alert matches the expected")
			verifiedAlert, err := humio.AlertTransform(updatedAlert, actionIdMap)
			Expect(err).To(BeNil())
			Eventually(func() humioapi.Alert {
				updatedAlert, err := humioClient.GetAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAlert)
				if err != nil {
					return *updatedAlert
				}
				// Ignore the ID
				updatedAlert.ID = ""
				return *updatedAlert
			}, testTimeout, suite.TestInterval).Should(Equal(*verifiedAlert))

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAlert)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAlert)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Successfully deleting the action")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, actionKey, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioAlert: Should deny improperly configured alert with missing required values", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-alert",
				Namespace: clusterKey.Namespace,
			}
			toCreateInvalidAlert := &humiov1alpha1.HumioAlert{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioAlertSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "example-invalid-alert",
					ViewName:           "humio",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Creating the invalid alert")
			Expect(k8sClient.Create(ctx, toCreateInvalidAlert)).Should(Not(Succeed()))
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
