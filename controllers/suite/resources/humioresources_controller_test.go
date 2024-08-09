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
	"net/http"
	"os"

	"github.com/humio/humio-operator/pkg/kubernetes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/controllers/suite"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
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
					RepositoryName:     testRepo.Spec.Name,
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
					RepositoryName:     testRepo.Spec.Name,
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
					RepositoryName:     testRepo.Spec.Name,
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
					RepositoryName:      testRepo.Spec.Name,
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
					AllowDataDeletion: true,
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
				AutomaticSearch:        true,
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
					AutomaticSearch:        initialRepository.AutomaticSearch,
				}
			}, testTimeout, suite.TestInterval).Should(Equal(expectedInitialRepository))

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Updating the repository successfully")
			updatedDescription := "important description - now updated"
			updatedAutomaticSearch := helpers.BoolPtr(false)
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedRepository)
				fetchedRepository.Spec.Description = updatedDescription
				fetchedRepository.Spec.AutomaticSearch = updatedAutomaticSearch
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
				AutomaticSearch:        *fetchedRepository.Spec.AutomaticSearch,
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
					AutomaticSearch:        updatedRepository.AutomaticSearch,
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
					AllowDataDeletion: true,
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
					Description:        "important description",
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
				Name:            viewToCreate.Spec.Name,
				Description:     viewToCreate.Spec.Description,
				Connections:     viewToCreate.GetViewConnections(),
				AutomaticSearch: true,
			}

			Eventually(func() humioapi.View {
				initialView, err := humioClient.GetView(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedView)
				if err != nil {
					return humioapi.View{}
				}
				return *initialView
			}, testTimeout, suite.TestInterval).Should(Equal(expectedInitialView))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Updating the view successfully in k8s")
			updatedViewDescription := "important description - now updated"
			updatedConnections := []humiov1alpha1.HumioViewConnection{
				{
					RepositoryName: testRepo.Spec.Name,
					Filter:         "*",
				},
			}
			updatedViewAutomaticSearch := helpers.BoolPtr(false)
			Eventually(func() error {
				k8sClient.Get(ctx, viewKey, fetchedView)
				fetchedView.Spec.Description = updatedViewDescription
				fetchedView.Spec.Connections = updatedConnections
				fetchedView.Spec.AutomaticSearch = updatedViewAutomaticSearch
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
				Name:            viewToCreate.Spec.Name,
				Description:     fetchedView.Spec.Description,
				Connections:     fetchedView.GetViewConnections(),
				AutomaticSearch: *fetchedView.Spec.AutomaticSearch,
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
				RepositoryName:     testRepo.Spec.Name,
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
				if err != nil {
					return err
				}

				// Ignore the ID when comparing parser content
				initialParser.ID = ""

				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(initialParser).ToNot(BeNil())

			expectedInitialParser := humioapi.Parser{
				Name:                           spec.Name,
				Script:                         spec.ParserScript,
				FieldsToTag:                    spec.TagFields,
				FieldsToBeRemovedBeforeParsing: []string{},
			}
			expectedInitialParser.TestCases = make([]humioapi.ParserTestCase, len(spec.TestData))
			for i := range spec.TestData {
				expectedInitialParser.TestCases[i] = humioapi.ParserTestCase{
					Event:      humioapi.ParserTestEvent{RawString: spec.TestData[i]},
					Assertions: []humioapi.ParserTestCaseAssertions{},
				}
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
				Name:                           spec.Name,
				Script:                         updatedScript,
				FieldsToTag:                    spec.TagFields,
				FieldsToBeRemovedBeforeParsing: []string{},
			}
			expectedUpdatedParser.TestCases = make([]humioapi.ParserTestCase, len(spec.TestData))
			for i := range spec.TestData {
				expectedUpdatedParser.TestCases[i] = humioapi.ParserTestCase{
					Event:      humioapi.ParserTestEvent{RawString: spec.TestData[i]},
					Assertions: []humioapi.ParserTestCaseAssertions{},
				}
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
					RepositoryName:     testRepo.Spec.Name,
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
					RepositoryName:      testRepo.Spec.Name,
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
					AllowDataDeletion:  true,
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
					AllowDataDeletion:   true,
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
							RepositoryName: testRepo.Spec.Name,
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
							RepositoryName: testRepo.Spec.Name,
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
				ViewName:           testRepo.Spec.Name,
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{"example@example.com"},
				},
			}

			key := types.NamespacedName{
				Name:      "humioemailaction",
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
			var expectedUpdatedAction, updatedAction2 *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(err).To(BeNil())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				return updatedAction2.EmailAction.BodyTemplate
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.EmailProperties.BodyTemplate))
			Expect(updatedAction2.EmailAction.SubjectTemplate).Should(BeEquivalentTo(updatedAction.Spec.EmailProperties.SubjectTemplate))
			Expect(updatedAction2.EmailAction.Recipients).Should(BeEquivalentTo(updatedAction.Spec.EmailProperties.Recipients))

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
			expectedSecretValue := "some-token"
			humioRepoActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-humio-repo-action",
				ViewName:           testRepo.Spec.Name,
				HumioRepositoryProperties: &humiov1alpha1.HumioActionRepositoryProperties{
					IngestToken: expectedSecretValue,
				},
			}

			key := types.NamespacedName{
				Name:      "humiorepoaction",
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

			// Should not be setting the API token in this case, but the secretMap should have the value
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

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
			var expectedUpdatedAction, updatedAction2 *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the humio repo action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				return updatedAction2.HumioRepoAction.IngestToken
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.HumioRepositoryProperties.IngestToken))

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
			expectedSecretValue := "somegeniekey"
			opsGenieActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-ops-genie-action",
				ViewName:           testRepo.Spec.Name,
				OpsGenieProperties: &humiov1alpha1.HumioActionOpsGenieProperties{
					GenieKey: expectedSecretValue,
					ApiUrl:   fmt.Sprintf("https://%s", testService1.Name),
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the ops genie action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.OpsGenieProperties.GenieKey = "updatedgeniekey"
			updatedAction.Spec.OpsGenieProperties.ApiUrl = fmt.Sprintf("https://%s", testService2.Name)

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the ops genie action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.OpsGenieProperties = updatedAction.Spec.OpsGenieProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the ops genie action update succeeded")
			var expectedUpdatedAction, updatedAction2 *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the ops genie action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				return updatedAction2.OpsGenieAction.GenieKey
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.OpsGenieProperties.GenieKey))
			Expect(updatedAction2.OpsGenieAction.ApiUrl).Should(BeEquivalentTo(updatedAction.Spec.OpsGenieProperties.ApiUrl))

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
			expectedSecretValue := "someroutingkey"
			pagerDutyActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-pagerduty-action",
				ViewName:           testRepo.Spec.Name,
				PagerDutyProperties: &humiov1alpha1.HumioActionPagerDutyProperties{
					Severity:   "critical",
					RoutingKey: expectedSecretValue,
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

			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

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
			var expectedUpdatedAction, updatedAction2 *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the pagerduty action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				return updatedAction2.PagerDutyAction.RoutingKey
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.PagerDutyProperties.RoutingKey))
			Expect(updatedAction2.PagerDutyAction.Severity).Should(BeEquivalentTo(updatedAction.Spec.PagerDutyProperties.Severity))

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
				ViewName:           testRepo.Spec.Name,
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

			// Check the secretMap rather than the apiToken in the ha.
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.SlackPostMessageProperties.ApiToken))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the slack post message action successfully")
			updatedAction := toCreateAction
			updatedFieldKey := "some"
			updatedFieldValue := "updatedvalue"
			updatedAction.Spec.SlackPostMessageProperties.ApiToken = "updated-token"
			updatedAction.Spec.SlackPostMessageProperties.Channels = []string{"#some-channel", "#other-channel"}
			updatedAction.Spec.SlackPostMessageProperties.Fields = map[string]string{
				updatedFieldKey: updatedFieldValue,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the slack post message action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.SlackPostMessageProperties = updatedAction.Spec.SlackPostMessageProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack post message action update succeeded")
			var expectedUpdatedAction, updatedAction2 *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack post message action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				return updatedAction2.SlackPostMessageAction.ApiToken
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.SlackPostMessageProperties.ApiToken))
			Expect(updatedAction2.SlackPostMessageAction.Channels).Should(BeEquivalentTo(updatedAction.Spec.SlackPostMessageProperties.Channels))
			Expect(updatedAction2.SlackPostMessageAction.Fields).Should(BeEquivalentTo([]humioapi.SlackFieldEntryInput{{
				FieldName: updatedFieldKey,
				Value:     updatedFieldValue,
			}}))

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
				ViewName:           testRepo.Spec.Name,
				SlackProperties: &humiov1alpha1.HumioActionSlackProperties{
					Url: fmt.Sprintf("https://%s/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX", testService1.Name),
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.SlackProperties.Url))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the slack action successfully")
			updatedAction := toCreateAction
			updatedFieldKey := "some"
			updatedFieldValue := "updatedvalue"
			updatedAction.Spec.SlackProperties.Url = fmt.Sprintf("https://%s/services/T00000000/B00000000/YYYYYYYYYYYYYYYYYYYYYYYY", testService1.Name)
			updatedAction.Spec.SlackProperties.Fields = map[string]string{
				updatedFieldKey: updatedFieldValue,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the slack action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.SlackProperties = updatedAction.Spec.SlackProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack action update succeeded")
			var expectedUpdatedAction, updatedAction2 *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				return updatedAction2.SlackAction.Url
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.SlackProperties.Url))
			Expect(updatedAction2.SlackAction.Fields).Should(BeEquivalentTo([]humioapi.SlackFieldEntryInput{{
				FieldName: updatedFieldKey,
				Value:     updatedFieldValue,
			}}))

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
				ViewName:           testRepo.Spec.Name,
				VictorOpsProperties: &humiov1alpha1.HumioActionVictorOpsProperties{
					MessageType: "critical",
					NotifyUrl:   fmt.Sprintf("https://%s/integrations/0000/alert/0000/routing_key", testService1.Name),
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

			// Check the SecretMap rather than the NotifyUrl on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.VictorOpsProperties.NotifyUrl))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the victor ops action successfully")
			updatedAction := toCreateAction
			updatedAction.Spec.VictorOpsProperties.MessageType = "recovery"
			updatedAction.Spec.VictorOpsProperties.NotifyUrl = fmt.Sprintf("https://%s/integrations/1111/alert/1111/routing_key", testService1.Name)

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the victor ops action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.VictorOpsProperties = updatedAction.Spec.VictorOpsProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the victor ops action update succeeded")
			var expectedUpdatedAction, updatedAction2 *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the victor ops action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				return updatedAction2.VictorOpsAction.MessageType
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.VictorOpsProperties.MessageType))
			Expect(updatedAction2.VictorOpsAction.NotifyUrl).Should(BeEquivalentTo(updatedAction.Spec.VictorOpsProperties.NotifyUrl))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle web hook action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle web hook action with url directly")
			webHookActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-webhook-action",
				ViewName:           testRepo.Spec.Name,
				WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{
					Headers:      map[string]string{"some": "header"},
					BodyTemplate: "body template",
					Method:       http.MethodPost,
					Url:          fmt.Sprintf("https://%s/some/api", testService1.Name),
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

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Updating the web hook action successfully")
			updatedHeaderKey := "updatedKey"
			updatedHeaderValue := "updatedValue"
			updatedWebhookActionProperties := &humiov1alpha1.HumioActionWebhookProperties{
				Headers:      map[string]string{updatedHeaderKey: updatedHeaderValue},
				BodyTemplate: "updated template",
				Method:       http.MethodPut,
				Url:          fmt.Sprintf("https://%s/some/updated/api", testService1.Name),
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Waiting for the web hook action to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.WebhookProperties = updatedWebhookActionProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the web hook action update succeeded")
			var expectedUpdatedAction, updatedAction *humioapi.Action
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the web hook action matches the expected")
			Eventually(func() string {
				updatedAction, err = humioClient.GetAction(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil || updatedAction == nil {
					return ""
				}
				return updatedAction.WebhookAction.Url
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedWebhookActionProperties.Url))
			Expect(updatedAction.WebhookAction.Headers).Should(BeEquivalentTo([]humioapi.HttpHeaderEntryInput{{
				Header: updatedHeaderKey,
				Value:  updatedHeaderValue,
			}}))
			Expect(updatedAction.WebhookAction.BodyTemplate).To(BeEquivalentTo(updatedWebhookActionProperties.BodyTemplate))
			Expect(updatedAction.WebhookAction.Method).To(BeEquivalentTo(updatedWebhookActionProperties.Method))

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
				Name:      "humio-webhook-action-missing",
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
					ViewName:           testRepo.Spec.Name,
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
				Name:      "humio-webhook-action-extra",
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
					ViewName:           testRepo.Spec.Name,
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
					ViewName:           testRepo.Spec.Name,
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

			expectedSecretValue := "secret-token"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-humio-repository-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte(expectedSecretValue),
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

			// Should not be setting the API token in this case, but the secretMap should have the value
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
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
					ViewName:           testRepo.Spec.Name,
					OpsGenieProperties: &humiov1alpha1.HumioActionOpsGenieProperties{
						ApiUrl: fmt.Sprintf("https://%s", testService1.Name),
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

			expectedSecretValue := "secret-token"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-genie-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte(expectedSecretValue),
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
		})

		It("HumioAction: OpsGenieProperties: Should support direct genie key", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "genie-action-direct",
				Namespace: clusterKey.Namespace,
			}

			expectedSecretValue := "direct-token"
			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					OpsGenieProperties: &humiov1alpha1.HumioActionOpsGenieProperties{
						GenieKey: expectedSecretValue,
						ApiUrl:   fmt.Sprintf("https://%s", testService1.Name),
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

			// Should not be setting the API token in this case, but the secretMap should have the value
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
		})

		It("HumioAction: VictorOpsProperties: Should support referencing secrets", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "victorops-action-secret",
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
					ViewName:           testRepo.Spec.Name,
					VictorOpsProperties: &humiov1alpha1.HumioActionVictorOpsProperties{
						MessageType: "critical",
						NotifyUrlSource: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "action-victorops-secret",
								},
								Key: "key",
							},
						},
					},
				},
			}

			expectedSecretValue := fmt.Sprintf("https://%s/integrations/0000/alert/0000/routing_key", testService1.Name)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-victorops-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte(expectedSecretValue),
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
		})

		It("HumioAction: VictorOpsProperties: Should support direct notify url", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "victorops-action-direct",
				Namespace: clusterKey.Namespace,
			}

			expectedSecretValue := fmt.Sprintf("https://%s/integrations/0000/alert/0000/routing_key", testService1.Name)
			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					VictorOpsProperties: &humiov1alpha1.HumioActionVictorOpsProperties{
						MessageType: "critical",
						NotifyUrl:   expectedSecretValue,
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

			// Check the SecretMap rather than the NotifyUrl on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
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
					ViewName:           testRepo.Spec.Name,
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

			expectedSecretValue := "secret-token"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-slack-post-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte(expectedSecretValue),
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

			// Should not be setting the API token in this case, but the secretMap should have the value
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
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
					ViewName:           testRepo.Spec.Name,
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.SlackPostMessageProperties.ApiToken))
		})

		It("HumioAction: SlackProperties: Should support referencing secrets", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-slack-action-secret",
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
					ViewName:           testRepo.Spec.Name,
					SlackProperties: &humiov1alpha1.HumioActionSlackProperties{
						UrlSource: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "action-slack-secret-from-secret",
								},
								Key: "key",
							},
						},
						Fields: map[string]string{
							"some": "key",
						},
					},
				},
			}

			expectedSecretValue := "https://hooks.slack.com/services/T00000000/B00000000/YYYYYYYYYYYYYYYYYYYYYYYY"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      toCreateAction.Spec.SlackProperties.UrlSource.SecretKeyRef.LocalObjectReference.Name,
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					toCreateAction.Spec.SlackProperties.UrlSource.SecretKeyRef.Key: []byte(expectedSecretValue),
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

			// Should not be setting the API token in this case, but the secretMap should have the value
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
		})

		It("HumioAction: SlackProperties: Should support direct url", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-slack-action-direct",
				Namespace: clusterKey.Namespace,
			}

			expectedSecretValue := "https://hooks.slack.com/services/T00000000/B00000000/YYYYYYYYYYYYYYYYYYYYYYYY"
			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					SlackProperties: &humiov1alpha1.HumioActionSlackProperties{
						Url: expectedSecretValue,
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.SlackProperties.Url))
		})

		It("HumioAction: PagerDutyProperties: Should support referencing secrets", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-pagerduty-action-secret",
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
					ViewName:           testRepo.Spec.Name,
					PagerDutyProperties: &humiov1alpha1.HumioActionPagerDutyProperties{
						RoutingKeySource: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "action-pagerduty-secret",
								},
								Key: "key",
							},
						},
						Severity: "critical",
					},
				},
			}

			expectedSecretValue := "secret-key"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-pagerduty-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte(expectedSecretValue),
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
		})

		It("HumioAction: PagerDutyProperties: Should support direct api token", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-pagerduty-action-direct",
				Namespace: clusterKey.Namespace,
			}

			expectedSecretValue := "direct-routing-key"
			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					PagerDutyProperties: &humiov1alpha1.HumioActionPagerDutyProperties{
						RoutingKey: expectedSecretValue,
						Severity:   "critical",
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

			// Check the secretMap rather than the apiToken in the ha.
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.PagerDutyProperties.RoutingKey))
		})

		It("HumioAction: WebhookProperties: Should support direct url", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-webhook-action-direct",
				Namespace: clusterKey.Namespace,
			}

			expectedSecretValue := fmt.Sprintf("https://%s/integrations/0000/alert/0000/routing_key", testService1.Name)
			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{
						BodyTemplate: "body template",
						Method:       http.MethodPost,
						Url:          expectedSecretValue,
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.WebhookProperties.Url))
		})

		It("HumioAction: WebhookProperties: Should support referencing secret url", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-webhook-action-secret",
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
					ViewName:           testRepo.Spec.Name,
					WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{
						BodyTemplate: "body template",
						Method:       http.MethodPost,
						UrlSource: humiov1alpha1.VarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{
									Name: "action-webhook-url-secret",
								},
								Key: "key",
							},
						},
					},
				},
			}

			expectedSecretValue := fmt.Sprintf("https://%s/integrations/0000/alert/0000/routing_key", testService1.Name)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-webhook-url-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte(expectedSecretValue),
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

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))
		})

		It("HumioAction: WebhookProperties: Should support direct url and headers", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-webhook-action-with-headers",
				Namespace: clusterKey.Namespace,
			}

			expectedUrl := fmt.Sprintf("https://%s/integrations/0000/alert/0000/routing_key", testService1.Name)
			nonsensitiveHeaderKey := "foo"
			nonsensitiveHeaderValue := "bar"
			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{
						BodyTemplate: "body template",
						Method:       http.MethodPost,
						Url:          expectedUrl,
						Headers: map[string]string{
							nonsensitiveHeaderKey: nonsensitiveHeaderValue,
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
			Expect(action.WebhookAction.Url).To(Equal(expectedUrl))
			Expect(action.WebhookAction.Headers).Should(ContainElements([]humioapi.HttpHeaderEntryInput{
				{
					Header: nonsensitiveHeaderKey,
					Value:  nonsensitiveHeaderValue,
				},
			}))

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.WebhookProperties.Url))

			allHeaders, found := kubernetes.GetFullSetOfMergedWebhookheaders(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(allHeaders).To(HaveKeyWithValue(nonsensitiveHeaderKey, nonsensitiveHeaderValue))
		})
		It("HumioAction: WebhookProperties: Should support direct url and mixed headers", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-webhook-action-with-mixed-headers",
				Namespace: clusterKey.Namespace,
			}

			expectedUrl := fmt.Sprintf("https://%s/integrations/0000/alert/0000/routing_key", testService1.Name)
			headerKey1 := "foo1"
			sensitiveHeaderValue1 := "bar1"
			headerKey2 := "foo2"
			nonsensitiveHeaderValue2 := "bar2"
			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{
						BodyTemplate: "body template",
						Method:       http.MethodPost,
						Url:          expectedUrl,
						Headers: map[string]string{
							headerKey2: nonsensitiveHeaderValue2,
						},
						SecretHeaders: []humiov1alpha1.HeadersSource{
							{
								Name: headerKey1,
								ValueFrom: humiov1alpha1.VarSource{
									SecretKeyRef: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "action-webhook-header-secret-mixed",
										},
										Key: "key",
									},
								},
							},
						},
					},
				},
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-webhook-header-secret-mixed",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte(sensitiveHeaderValue1),
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
			Expect(action.WebhookAction.Url).To(Equal(expectedUrl))
			Expect(action.WebhookAction.Headers).Should(ContainElements([]humioapi.HttpHeaderEntryInput{
				{
					Header: headerKey1,
					Value:  sensitiveHeaderValue1,
				},
				{
					Header: headerKey2,
					Value:  nonsensitiveHeaderValue2,
				},
			}))

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.WebhookProperties.Url))

			allHeaders, found := kubernetes.GetFullSetOfMergedWebhookheaders(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(allHeaders).To(HaveKeyWithValue(headerKey1, sensitiveHeaderValue1))
			Expect(allHeaders).To(HaveKeyWithValue(headerKey2, nonsensitiveHeaderValue2))
		})
		It("HumioAction: WebhookProperties: Should support direct url and secret headers", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-webhook-action-with-secret-headers",
				Namespace: clusterKey.Namespace,
			}

			expectedUrl := fmt.Sprintf("https://%s/integrations/0000/alert/0000/routing_key", testService1.Name)
			headerKey := "foo"
			sensitiveHeaderValue := "bar"
			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioActionSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					WebhookProperties: &humiov1alpha1.HumioActionWebhookProperties{
						BodyTemplate: "body template",
						Method:       http.MethodPost,
						Url:          expectedUrl,
						SecretHeaders: []humiov1alpha1.HeadersSource{
							{
								Name: headerKey,
								ValueFrom: humiov1alpha1.VarSource{
									SecretKeyRef: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "action-webhook-header-secret",
										},
										Key: "key",
									},
								},
							},
						},
					},
				},
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "action-webhook-header-secret",
					Namespace: clusterKey.Namespace,
				},
				Data: map[string][]byte{
					"key": []byte(sensitiveHeaderValue),
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
			Expect(action.WebhookAction.Url).To(Equal(expectedUrl))
			Expect(action.WebhookAction.Headers).Should(ContainElements([]humioapi.HttpHeaderEntryInput{
				{
					Header: headerKey,
					Value:  sensitiveHeaderValue,
				},
			}))

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.WebhookProperties.Url))

			allHeaders, found := kubernetes.GetFullSetOfMergedWebhookheaders(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(allHeaders).To(HaveKeyWithValue(headerKey, sensitiveHeaderValue))
		})
	})

	Context("Humio Alert", func() {
		It("should handle alert action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Should handle alert correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action",
				ViewName:           testRepo.Spec.Name,
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{"example@example.com"},
				},
			}

			actionKey := types.NamespacedName{
				Name:      "humiorepoactionforalert",
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
				ViewName:           testRepo.Spec.Name,
				Query: humiov1alpha1.HumioQuery{
					QueryString: "#repo = test | count()",
					Start:       "1d",
				},
				ThrottleTimeMillis: 60000,
				ThrottleField:      "some field",
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
			Expect(alert.ThrottleField).To(Equal(originalAlert.ThrottleField))
			Expect(alert.Enabled).To(Equal(originalAlert.Enabled))
			Expect(alert.QueryString).To(Equal(originalAlert.QueryString))
			Expect(alert.QueryStart).To(Equal(originalAlert.QueryStart))

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Updating the alert successfully")
			updatedAlert := toCreateAlert
			updatedAlert.Spec.Query.QueryString = "#repo = test | updated=true | count()"
			updatedAlert.Spec.ThrottleTimeMillis = 70000
			updatedAlert.Spec.ThrottleField = "some other field"
			updatedAlert.Spec.Silenced = true
			updatedAlert.Spec.Description = "updated humio alert"
			updatedAlert.Spec.Actions = []string{toCreateDependentAction.Spec.Name}

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Waiting for the alert to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAlert)
				fetchedAlert.Spec.Query = updatedAlert.Spec.Query
				fetchedAlert.Spec.ThrottleTimeMillis = updatedAlert.Spec.ThrottleTimeMillis
				fetchedAlert.Spec.ThrottleField = updatedAlert.Spec.ThrottleField
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

				// Ignore the ID, QueryOwnershipType and RunAsUserID
				updatedAlert.ID = ""
				updatedAlert.QueryOwnershipType = ""
				updatedAlert.RunAsUserID = ""

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
					ViewName:           testRepo.Spec.Name,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Creating the invalid alert")
			Expect(k8sClient.Create(ctx, toCreateInvalidAlert)).Should(Not(Succeed()))
		})
	})

	Context("Humio Filter Alert", func() {
		It("should handle filter alert action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Should handle filter alert correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action",
				ViewName:           testRepo.Spec.Name,
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

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Creating the action required by the filter alert successfully")
			Expect(k8sClient.Create(ctx, toCreateDependentAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, actionKey, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			filterAlertSpec := humiov1alpha1.HumioFilterAlertSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-filter-alert",
				ViewName:           testRepo.Spec.Name,
				QueryString:        "#repo = humio | error = true",
				Enabled:            true,
				Description:        "humio filter alert",
				Actions:            []string{toCreateDependentAction.Spec.Name},
				Labels:             []string{"some-label"},
			}

			key := types.NamespacedName{
				Name:      "humio-filter-alert",
				Namespace: clusterKey.Namespace,
			}

			toCreateFilterAlert := &humiov1alpha1.HumioFilterAlert{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: filterAlertSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Creating the filter alert successfully")
			Expect(k8sClient.Create(ctx, toCreateFilterAlert)).Should(Succeed())

			fetchedFilterAlert := &humiov1alpha1.HumioFilterAlert{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedFilterAlert)
				return fetchedFilterAlert.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioFilterAlertStateExists))

			var filterAlert *humioapi.FilterAlert
			Eventually(func() error {
				filterAlert, err = humioClient.GetFilterAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateFilterAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(filterAlert).ToNot(BeNil())

			Eventually(func() error {
				return humioClient.ValidateActionsForFilterAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateFilterAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			originalFilterAlert, err := humio.FilterAlertTransform(toCreateFilterAlert)
			Expect(err).To(BeNil())
			Expect(filterAlert.Name).To(Equal(originalFilterAlert.Name))
			Expect(filterAlert.Description).To(Equal(originalFilterAlert.Description))
			Expect(filterAlert.ThrottleTimeSeconds).To(Equal(originalFilterAlert.ThrottleTimeSeconds))
			Expect(filterAlert.ThrottleField).To(Equal(originalFilterAlert.ThrottleField))
			Expect(filterAlert.ActionNames).To(Equal(originalFilterAlert.ActionNames))
			Expect(filterAlert.Labels).To(Equal(originalFilterAlert.Labels))
			Expect(filterAlert.Enabled).To(Equal(originalFilterAlert.Enabled))
			Expect(filterAlert.QueryString).To(Equal(originalFilterAlert.QueryString))

			createdFilterAlert := toCreateFilterAlert
			err = humio.FilterAlertHydrate(createdFilterAlert, filterAlert)
			Expect(err).To(BeNil())
			Expect(createdFilterAlert.Spec).To(Equal(toCreateFilterAlert.Spec))

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Updating the filter alert successfully")
			updatedFilterAlert := toCreateFilterAlert
			updatedFilterAlert.Spec.QueryString = "#repo = humio | updated_field = true | error = true"
			updatedFilterAlert.Spec.Enabled = false
			updatedFilterAlert.Spec.Description = "updated humio filter alert"
			updatedFilterAlert.Spec.ThrottleTimeSeconds = 3600
			updatedFilterAlert.Spec.ThrottleField = "newfield"
			updatedFilterAlert.Spec.Actions = []string{toCreateDependentAction.Spec.Name}

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Waiting for the filter alert to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedFilterAlert)
				fetchedFilterAlert.Spec.QueryString = updatedFilterAlert.Spec.QueryString
				fetchedFilterAlert.Spec.Enabled = updatedFilterAlert.Spec.Enabled
				fetchedFilterAlert.Spec.Description = updatedFilterAlert.Spec.Description
				fetchedFilterAlert.Spec.ThrottleTimeSeconds = updatedFilterAlert.Spec.ThrottleTimeSeconds
				fetchedFilterAlert.Spec.ThrottleField = updatedFilterAlert.Spec.ThrottleField
				return k8sClient.Update(ctx, fetchedFilterAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Verifying the filter alert update succeeded")
			var expectedUpdatedFilterAlert *humioapi.FilterAlert
			Eventually(func() error {
				expectedUpdatedFilterAlert, err = humioClient.GetFilterAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedFilterAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedFilterAlert).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Verifying the alert matches the expected")
			verifiedFilterAlert, err := humio.FilterAlertTransform(updatedFilterAlert)
			verifiedFilterAlert.ID = ""
			verifiedFilterAlert.RunAsUserID = ""

			Expect(err).To(BeNil())
			Eventually(func() humioapi.FilterAlert {
				updatedFilterAlert, err := humioClient.GetFilterAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedFilterAlert)
				if err != nil {
					return *updatedFilterAlert
				}

				// Ignore the ID and RunAsUserID
				updatedFilterAlert.ID = ""
				updatedFilterAlert.RunAsUserID = ""

				return *updatedFilterAlert
			}, testTimeout, suite.TestInterval).Should(Equal(*verifiedFilterAlert))

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Successfully deleting the filter alert")
			Expect(k8sClient.Delete(ctx, fetchedFilterAlert)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedFilterAlert)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Successfully deleting the action")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, actionKey, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioFilterAlert: Should deny improperly configured filter alert with missing required values", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-filter-alert",
				Namespace: clusterKey.Namespace,
			}
			toCreateInvalidFilterAlert := &humiov1alpha1.HumioFilterAlert{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioFilterAlertSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "example-invalid-filter-alert",
					ViewName:           testRepo.Spec.Name,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Creating the invalid filter alert")
			Expect(k8sClient.Create(ctx, toCreateInvalidFilterAlert)).Should(Not(Succeed()))
		})
	})

	Context("Humio Aggregate Alert", func() {
		It("should handle aggregate alert action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Should handle aggregate alert correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action",
				ViewName:           testRepo.Spec.Name,
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

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Creating the action required by the aggregate alert successfully")
			Expect(k8sClient.Create(ctx, toCreateDependentAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, actionKey, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			aggregateAlertSpec := humiov1alpha1.HumioAggregateAlertSpec{
				ManagedClusterName:    clusterKey.Name,
				Name:                  "example-aggregate-alert",
				ViewName:              testRepo.Spec.Name,
				QueryString:           "#repo = humio | error = true",
				QueryTimestampType:    "EventTimestamp",
				TriggerMode:           "all",
				SearchIntervalSeconds: 60,
				ThrottleTimeSeconds:   120,
				ThrottleField:         "@timestamp",
				Enabled:               true,
				Description:           "humio aggregate alert",
				Actions:               []string{toCreateDependentAction.Spec.Name},
				Labels:                []string{"some-label"},
			}

			key := types.NamespacedName{
				Name:      "humio-aggregate-alert",
				Namespace: clusterKey.Namespace,
			}

			toCreateAggregateAlert := &humiov1alpha1.HumioAggregateAlert{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: aggregateAlertSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Creating the aggregate alert successfully")
			Expect(k8sClient.Create(ctx, toCreateAggregateAlert)).Should(Succeed())

			fetchedAggregateAlert := &humiov1alpha1.HumioAggregateAlert{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAggregateAlert)
				return fetchedAggregateAlert.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioAggregateAlertStateExists))

			var aggregateAlert *humioapi.AggregateAlert
			Eventually(func() error {
				aggregateAlert, err = humioClient.GetAggregateAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAggregateAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(aggregateAlert).ToNot(BeNil())

			Eventually(func() error {
				return humioClient.ValidateActionsForAggregateAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateAggregateAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			originalAggregateAlert, err := humio.AggregateAlertTransform(toCreateAggregateAlert)
			Expect(err).To(BeNil())
			Expect(aggregateAlert.Name).To(Equal(originalAggregateAlert.Name))
			Expect(aggregateAlert.Description).To(Equal(originalAggregateAlert.Description))
			Expect(aggregateAlert.ThrottleTimeSeconds).To(Equal(originalAggregateAlert.ThrottleTimeSeconds))
			Expect(aggregateAlert.ThrottleField).To(Equal(originalAggregateAlert.ThrottleField))
			Expect(aggregateAlert.ActionNames).To(Equal(originalAggregateAlert.ActionNames))
			Expect(aggregateAlert.Labels).To(Equal(originalAggregateAlert.Labels))

			createdAggregateAlert := toCreateAggregateAlert
			err = humio.AggregateAlertHydrate(createdAggregateAlert, aggregateAlert)
			Expect(err).To(BeNil())
			Expect(createdAggregateAlert.Spec).To(Equal(toCreateAggregateAlert.Spec))

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Updating the aggregate alert successfully")
			updatedAggregateAlert := toCreateAggregateAlert
			updatedAggregateAlert.Spec.QueryString = "#repo = humio | updated_field = true | error = true"
			updatedAggregateAlert.Spec.Enabled = false
			updatedAggregateAlert.Spec.Description = "updated humio aggregate alert"
			updatedAggregateAlert.Spec.SearchIntervalSeconds = 120
			updatedAggregateAlert.Spec.ThrottleTimeSeconds = 3600
			updatedAggregateAlert.Spec.ThrottleField = "newfield"
			updatedAggregateAlert.Spec.Actions = []string{toCreateDependentAction.Spec.Name}

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Waiting for the aggregate alert to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedAggregateAlert)
				fetchedAggregateAlert.Spec.QueryString = updatedAggregateAlert.Spec.QueryString
				fetchedAggregateAlert.Spec.Enabled = updatedAggregateAlert.Spec.Enabled
				fetchedAggregateAlert.Spec.Description = updatedAggregateAlert.Spec.Description
				fetchedAggregateAlert.Spec.SearchIntervalSeconds = updatedAggregateAlert.Spec.SearchIntervalSeconds
				fetchedAggregateAlert.Spec.ThrottleTimeSeconds = updatedAggregateAlert.Spec.ThrottleTimeSeconds
				fetchedAggregateAlert.Spec.ThrottleField = updatedAggregateAlert.Spec.ThrottleField
				return k8sClient.Update(ctx, fetchedAggregateAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Verifying the aggregate alert update succeeded")
			var expectedUpdatedAggregateAlert *humioapi.AggregateAlert
			Eventually(func() error {
				expectedUpdatedAggregateAlert, err = humioClient.GetAggregateAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAggregateAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAggregateAlert).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Verifying the alert matches the expected")
			verifiedAggregateAlert, err := humio.AggregateAlertTransform(updatedAggregateAlert)
			verifiedAggregateAlert.ID = ""
			verifiedAggregateAlert.RunAsUserID = ""

			Expect(err).To(BeNil())
			Eventually(func() humioapi.AggregateAlert {
				updatedAggregateAlert, err := humioClient.GetAggregateAlert(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedAggregateAlert)
				if err != nil {
					return *updatedAggregateAlert
				}

				// Ignore the ID and RunAsUserID
				updatedAggregateAlert.ID = ""
				updatedAggregateAlert.RunAsUserID = ""

				return *updatedAggregateAlert
			}, testTimeout, suite.TestInterval).Should(Equal(*verifiedAggregateAlert))

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Successfully deleting the aggregate alert")
			Expect(k8sClient.Delete(ctx, fetchedAggregateAlert)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAggregateAlert)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Successfully deleting the action")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, actionKey, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("HumioAggregateAlert: Should deny improperly configured aggregate alert with missing required values", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-aggregate-alert",
				Namespace: clusterKey.Namespace,
			}
			toCreateInvalidAggregateAlert := &humiov1alpha1.HumioAggregateAlert{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioAggregateAlertSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "example-invalid-aggregate-alert",
					ViewName:           testRepo.Spec.Name,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Creating the invalid aggregate alert")
			Expect(k8sClient.Create(ctx, toCreateInvalidAggregateAlert)).Should(Not(Succeed()))
		})
	})

	Context("Humio Scheduled Search", func() {
		It("should handle scheduled search action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Should handle scheduled search correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action2",
				ViewName:           testRepo.Spec.Name,
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{"example@example.com"},
				},
			}

			actionKey := types.NamespacedName{
				Name:      "humioaction2",
				Namespace: clusterKey.Namespace,
			}

			toCreateDependentAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      actionKey.Name,
					Namespace: actionKey.Namespace,
				},
				Spec: dependentEmailActionSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Creating the action required by the scheduled search successfully")
			Expect(k8sClient.Create(ctx, toCreateDependentAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, actionKey, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			scheduledSearchSpec := humiov1alpha1.HumioScheduledSearchSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-scheduled-search",
				ViewName:           testRepo.Spec.Name,
				QueryString:        "#repo = humio | error = true",
				QueryStart:         "1h",
				QueryEnd:           "now",
				Schedule:           "0 * * * *",
				TimeZone:           "UTC",
				BackfillLimit:      3,
				Enabled:            true,
				Description:        "humio scheduled search",
				Actions:            []string{toCreateDependentAction.Spec.Name},
				Labels:             []string{"some-label"},
			}

			key := types.NamespacedName{
				Name:      "humio-scheduled-search",
				Namespace: clusterKey.Namespace,
			}

			toCreateScheduledSearch := &humiov1alpha1.HumioScheduledSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: scheduledSearchSpec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Creating the scheduled search successfully")
			Expect(k8sClient.Create(ctx, toCreateScheduledSearch)).Should(Succeed())

			fetchedScheduledSearch := &humiov1alpha1.HumioScheduledSearch{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedScheduledSearch)
				return fetchedScheduledSearch.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioScheduledSearchStateExists))

			var scheduledSearch *humioapi.ScheduledSearch
			Eventually(func() error {
				scheduledSearch, err = humioClient.GetScheduledSearch(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateScheduledSearch)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(scheduledSearch).ToNot(BeNil())

			Eventually(func() error {
				return humioClient.ValidateActionsForScheduledSearch(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, toCreateScheduledSearch)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			originalScheduledSearch, err := humio.ScheduledSearchTransform(toCreateScheduledSearch)
			Expect(err).To(BeNil())
			Expect(scheduledSearch.Name).To(Equal(originalScheduledSearch.Name))
			Expect(scheduledSearch.Description).To(Equal(originalScheduledSearch.Description))
			Expect(scheduledSearch.ActionNames).To(Equal(originalScheduledSearch.ActionNames))
			Expect(scheduledSearch.Labels).To(Equal(originalScheduledSearch.Labels))
			Expect(scheduledSearch.Enabled).To(Equal(originalScheduledSearch.Enabled))
			Expect(scheduledSearch.QueryString).To(Equal(originalScheduledSearch.QueryString))
			Expect(scheduledSearch.QueryStart).To(Equal(originalScheduledSearch.QueryStart))
			Expect(scheduledSearch.QueryEnd).To(Equal(originalScheduledSearch.QueryEnd))
			Expect(scheduledSearch.Schedule).To(Equal(originalScheduledSearch.Schedule))
			Expect(scheduledSearch.TimeZone).To(Equal(originalScheduledSearch.TimeZone))
			Expect(scheduledSearch.BackfillLimit).To(Equal(originalScheduledSearch.BackfillLimit))

			createdScheduledSearch := toCreateScheduledSearch
			err = humio.ScheduledSearchHydrate(createdScheduledSearch, scheduledSearch)
			Expect(err).To(BeNil())
			Expect(createdScheduledSearch.Spec).To(Equal(toCreateScheduledSearch.Spec))

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Updating the scheduled search successfully")
			updatedScheduledSearch := toCreateScheduledSearch
			updatedScheduledSearch.Spec.QueryString = "#repo = humio | updated_field = true | error = true"
			updatedScheduledSearch.Spec.QueryStart = "2h"
			updatedScheduledSearch.Spec.QueryEnd = "30m"
			updatedScheduledSearch.Spec.Schedule = "0 0 * * *"
			updatedScheduledSearch.Spec.TimeZone = "UTC-01"
			updatedScheduledSearch.Spec.BackfillLimit = 5
			updatedScheduledSearch.Spec.Enabled = false
			updatedScheduledSearch.Spec.Description = "updated humio scheduled search"
			updatedScheduledSearch.Spec.Actions = []string{toCreateDependentAction.Spec.Name}

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Waiting for the scheduled search to be updated")
			Eventually(func() error {
				k8sClient.Get(ctx, key, fetchedScheduledSearch)
				fetchedScheduledSearch.Spec.QueryString = updatedScheduledSearch.Spec.QueryString
				fetchedScheduledSearch.Spec.QueryStart = updatedScheduledSearch.Spec.QueryStart
				fetchedScheduledSearch.Spec.QueryEnd = updatedScheduledSearch.Spec.QueryEnd
				fetchedScheduledSearch.Spec.Schedule = updatedScheduledSearch.Spec.Schedule
				fetchedScheduledSearch.Spec.TimeZone = updatedScheduledSearch.Spec.TimeZone
				fetchedScheduledSearch.Spec.BackfillLimit = updatedScheduledSearch.Spec.BackfillLimit
				fetchedScheduledSearch.Spec.Enabled = updatedScheduledSearch.Spec.Enabled
				fetchedScheduledSearch.Spec.Description = updatedScheduledSearch.Spec.Description
				return k8sClient.Update(ctx, fetchedScheduledSearch)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Verifying the scheduled search update succeeded")
			var expectedUpdatedScheduledSearch *humioapi.ScheduledSearch
			Eventually(func() error {
				expectedUpdatedScheduledSearch, err = humioClient.GetScheduledSearch(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedScheduledSearch)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedScheduledSearch).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Verifying the scheduled search matches the expected")
			verifiedScheduledSearch, err := humio.ScheduledSearchTransform(updatedScheduledSearch)
			verifiedScheduledSearch.ID = ""
			verifiedScheduledSearch.RunAsUserID = ""

			Expect(err).To(BeNil())
			Eventually(func() humioapi.ScheduledSearch {
				updatedScheduledSearch, err := humioClient.GetScheduledSearch(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey}, fetchedScheduledSearch)
				if err != nil {
					return *updatedScheduledSearch
				}

				// Ignore the ID and RunAsUserID
				updatedScheduledSearch.ID = ""
				updatedScheduledSearch.RunAsUserID = ""

				return *updatedScheduledSearch
			}, testTimeout, suite.TestInterval).Should(Equal(*verifiedScheduledSearch))

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Successfully deleting the scheduled search")
			Expect(k8sClient.Delete(ctx, fetchedScheduledSearch)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedScheduledSearch)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Successfully deleting the action")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, actionKey, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioScheduledSearch: Should deny improperly configured scheduled search with missing required values", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-scheduled-search",
				Namespace: clusterKey.Namespace,
			}
			toCreateInvalidScheduledSearch := &humiov1alpha1.HumioScheduledSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioScheduledSearchSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "example-invalid-scheduled-search",
					ViewName:           testRepo.Spec.Name,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Creating the invalid scheduled search")
			Expect(k8sClient.Create(ctx, toCreateInvalidScheduledSearch)).Should(Not(Succeed()))
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
	AutomaticSearch        bool
}
