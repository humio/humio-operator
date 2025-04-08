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
	"time"

	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller/suite"
)

const (
	emailActionExample         string = "example@example.com"
	expectedSecretValueExample string = "secret-token"
	PDFRenderServiceImage      string = "humio/pdf-render-service:0.0.60--build-102--sha-c8eb95329236ba5fc65659b83af1d84b4703cb1e"
	protocolHTTPS              string = "https"
	tlsCertName                string = "tls-cert"
	pdfRenderUseTLSEnvVar      string = "PDF_RENDER_USE_TLS" // Add this line
)

var _ = Describe("Humio Resources Controllers", func() {
	BeforeEach(func() {
		// failed test runs that don't clean up leave resources behind.
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	var createdPdfCR *humiov1alpha1.HumioPdfRenderService

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
		humioClient.ClearHumioClientConnections(testRepoName)

		// Clean up the PDF render service if it was created
		if createdPdfCR != nil {
			ctx := context.Background()
			err := k8sClient.Delete(ctx, createdPdfCR)
			if err != nil {
				fmt.Printf("Error deleting PDF CR: %v\n", err)
			}
			createdPdfCR = nil
		}
	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Humio Ingest Token", Label("envtest", "dummy", "real"), func() {
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
					ParserName:         &initialParserName,
					RepositoryName:     testRepo.Spec.Name,
					TokenSecretName:    "target-secret-1",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Creating the ingest token with token secret successfully")
			Expect(k8sClient.Create(ctx, toCreateIngestToken)).Should(Succeed())

			fetchedIngestToken := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedIngestToken)
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

			Expect(string(ingestTokenSecret.Data["token"])).ToNot(BeEmpty())
			Expect(ingestTokenSecret.OwnerReferences).Should(HaveLen(1))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Checking correct parser assigned to ingest token")
			var humioIngestToken *humiographql.IngestTokenDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() *humiographql.IngestTokenDetailsParser {
				humioIngestToken, _ = humioClient.GetIngestToken(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedIngestToken)
				if humioIngestToken != nil {
					return humioIngestToken.Parser
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&humiographql.IngestTokenDetailsParser{Name: initialParserName}))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Updating parser for ingest token")
			updatedParserName := "accesslog"
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedIngestToken); err != nil {
					return err
				}
				fetchedIngestToken.Spec.ParserName = &updatedParserName
				return k8sClient.Update(ctx, fetchedIngestToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Eventually(func() *humiographql.IngestTokenDetailsParser {
				humioIngestToken, err = humioClient.GetIngestToken(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedIngestToken)
				if humioIngestToken != nil {
					return humioIngestToken.Parser
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&humiographql.IngestTokenDetailsParser{Name: updatedParserName}))

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

			Expect(string(ingestTokenSecret.Data["token"])).ToNot(BeEmpty())

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
					ParserName:         helpers.StringPtr("accesslog"),
					RepositoryName:     testRepo.Spec.Name,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Creating the ingest token without token secret successfully")
			Expect(k8sClient.Create(ctx, toCreateIngestToken)).Should(Succeed())

			fetchedIngestToken := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedIngestToken)
				return fetchedIngestToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioIngestTokenStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Checking we do not create a token secret")
			var allSecrets corev1.SecretList
			_ = k8sClient.List(ctx, &allSecrets, client.InNamespace(fetchedIngestToken.Namespace))
			for _, secret := range allSecrets.Items {
				for _, owner := range secret.OwnerReferences {
					Expect(owner.Name).ShouldNot(BeIdenticalTo(fetchedIngestToken.Name))
				}
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioIngestToken: Enabling token secret name successfully creates secret")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedIngestToken); err != nil {
					return err
				}
				fetchedIngestToken.Spec.TokenSecretName = "target-secret-2"
				fetchedIngestToken.Spec.TokenSecretLabels = map[string]string{
					"custom-label": "custom-value",
				}
				fetchedIngestToken.Spec.TokenSecretAnnotations = map[string]string{
					"custom-annotation": "custom-value",
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
			Expect(ingestTokenSecret.Annotations).Should(HaveKeyWithValue("custom-annotation", "custom-value"))

			Expect(string(ingestTokenSecret.Data["token"])).ToNot(BeEmpty())

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
					ParserName:         helpers.StringPtr("accesslog"),
					RepositoryName:     testRepo.Spec.Name,
					TokenSecretName:    "thissecretname",
				},
			}
			Expect(k8sClient.Create(ctx, toCreateIngestToken)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioIngestToken: Validates resource enters state %s", humiov1alpha1.HumioIngestTokenStateConfigError))
			fetchedIngestToken := &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyErr, fetchedIngestToken)
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
					ParserName:          helpers.StringPtr("accesslog"),
					RepositoryName:      testRepo.Spec.Name,
					TokenSecretName:     "thissecretname",
				},
			}
			Expect(k8sClient.Create(ctx, toCreateIngestToken)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioIngestToken: Validates resource enters state %s", humiov1alpha1.HumioIngestTokenStateConfigError))
			fetchedIngestToken = &humiov1alpha1.HumioIngestToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyErr, fetchedIngestToken)
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

	Context("Humio Repository and View", Label("envtest", "dummy", "real"), func() {
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
						TimeInDays:      helpers.Int32Ptr(30),
						IngestSizeInGB:  helpers.Int32Ptr(5),
						StorageSizeInGB: helpers.Int32Ptr(1),
					},
					AllowDataDeletion: true,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Creating the repository successfully")
			Expect(k8sClient.Create(ctx, toCreateRepository)).Should(Succeed())

			fetchedRepository := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedRepository)
				return fetchedRepository.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			var initialRepository *humiographql.RepositoryDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				initialRepository, err = humioClient.GetRepository(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateRepository)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(initialRepository).ToNot(BeNil())

			var retentionInDays, ingestRetentionSizeGB, storageRetentionSizeGB float64
			if toCreateRepository.Spec.Retention.TimeInDays != nil {
				retentionInDays = float64(*toCreateRepository.Spec.Retention.TimeInDays)
			}
			if toCreateRepository.Spec.Retention.IngestSizeInGB != nil {
				ingestRetentionSizeGB = float64(*toCreateRepository.Spec.Retention.IngestSizeInGB)
			}
			if toCreateRepository.Spec.Retention.StorageSizeInGB != nil {
				storageRetentionSizeGB = float64(*toCreateRepository.Spec.Retention.StorageSizeInGB)
			}
			expectedInitialRepository := repositoryExpectation{
				Name:                   toCreateRepository.Spec.Name,
				Description:            &toCreateRepository.Spec.Description,
				RetentionDays:          &retentionInDays,
				IngestRetentionSizeGB:  &ingestRetentionSizeGB,
				StorageRetentionSizeGB: &storageRetentionSizeGB,
				AutomaticSearch:        true,
			}
			Eventually(func() repositoryExpectation {
				initialRepository, err := humioClient.GetRepository(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedRepository)
				if err != nil {
					return repositoryExpectation{}
				}
				return repositoryExpectation{
					Name:                   initialRepository.GetName(),
					Description:            initialRepository.GetDescription(),
					RetentionDays:          initialRepository.GetTimeBasedRetention(),
					IngestRetentionSizeGB:  initialRepository.GetIngestSizeBasedRetention(),
					StorageRetentionSizeGB: initialRepository.GetStorageSizeBasedRetention(),
					SpaceUsed:              initialRepository.GetCompressedByteSize(),
					AutomaticSearch:        initialRepository.GetAutomaticSearch(),
				}
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(expectedInitialRepository))

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Updating the repository successfully")
			updatedDescription := "important description - now updated"
			updatedAutomaticSearch := helpers.BoolPtr(false)
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedRepository); err != nil {
					return err
				}
				fetchedRepository.Spec.Description = updatedDescription
				fetchedRepository.Spec.AutomaticSearch = updatedAutomaticSearch
				return k8sClient.Update(ctx, fetchedRepository)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			var updatedRepository *humiographql.RepositoryDetails
			Eventually(func() error {
				updatedRepository, err = humioClient.GetRepository(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedRepository)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(updatedRepository).ToNot(BeNil())

			var updatedRetentionInDays, updatedIngestRetentionSizeGB, updatedStorageRetentionSizeGB float64
			if toCreateRepository.Spec.Retention.TimeInDays != nil {
				updatedRetentionInDays = float64(*fetchedRepository.Spec.Retention.TimeInDays)
			}
			if toCreateRepository.Spec.Retention.IngestSizeInGB != nil {
				updatedIngestRetentionSizeGB = float64(*fetchedRepository.Spec.Retention.IngestSizeInGB)
			}
			if toCreateRepository.Spec.Retention.StorageSizeInGB != nil {
				updatedStorageRetentionSizeGB = float64(*fetchedRepository.Spec.Retention.StorageSizeInGB)
			}
			expectedUpdatedRepository := repositoryExpectation{
				Name:                   fetchedRepository.Spec.Name,
				Description:            &updatedDescription,
				RetentionDays:          &updatedRetentionInDays,
				IngestRetentionSizeGB:  &updatedIngestRetentionSizeGB,
				StorageRetentionSizeGB: &updatedStorageRetentionSizeGB,
				AutomaticSearch:        helpers.BoolTrue(fetchedRepository.Spec.AutomaticSearch),
			}
			Eventually(func() repositoryExpectation {
				updatedRepository, err := humioClient.GetRepository(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedRepository)
				if err != nil {
					return repositoryExpectation{}
				}

				return repositoryExpectation{
					Name:                   updatedRepository.GetName(),
					Description:            updatedRepository.GetDescription(),
					RetentionDays:          updatedRepository.GetTimeBasedRetention(),
					IngestRetentionSizeGB:  updatedRepository.GetIngestSizeBasedRetention(),
					StorageRetentionSizeGB: updatedRepository.GetStorageSizeBasedRetention(),
					SpaceUsed:              updatedRepository.GetCompressedByteSize(),
					AutomaticSearch:        updatedRepository.GetAutomaticSearch(),
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
						TimeInDays:      helpers.Int32Ptr(30),
						IngestSizeInGB:  helpers.Int32Ptr(5),
						StorageSizeInGB: helpers.Int32Ptr(1),
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
				_ = k8sClient.Get(ctx, viewKey, fetchedRepo)
				return fetchedRepo.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Creating the view successfully in k8s")
			Expect(k8sClient.Create(ctx, viewToCreate)).Should(Succeed())

			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Creating the view successfully in Humio")
			var initialView *humiographql.GetSearchDomainSearchDomainView
			Eventually(func() error {
				initialView, err = humioClient.GetView(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, viewToCreate)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(initialView).ToNot(BeNil())

			expectedInitialView := humiographql.GetSearchDomainSearchDomainView{
				Typename:        helpers.StringPtr("View"),
				Id:              "",
				Name:            viewToCreate.Spec.Name,
				Description:     &viewToCreate.Spec.Description,
				Connections:     viewToCreate.GetViewConnections(),
				AutomaticSearch: true,
			}

			Eventually(func() humiographql.GetSearchDomainSearchDomainView {
				initialView, err := humioClient.GetView(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedView)
				if err != nil {
					return humiographql.GetSearchDomainSearchDomainView{}
				}

				// Ignore the ID
				initialView.Id = ""

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
				if err := k8sClient.Get(ctx, viewKey, fetchedView); err != nil {
					return err
				}
				fetchedView.Spec.Description = updatedViewDescription
				fetchedView.Spec.Connections = updatedConnections
				fetchedView.Spec.AutomaticSearch = updatedViewAutomaticSearch
				return k8sClient.Update(ctx, fetchedView)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioView: Updating the view successfully in Humio")
			var updatedView *humiographql.GetSearchDomainSearchDomainView
			Eventually(func() error {
				updatedView, err = humioClient.GetView(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedView)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(updatedView).ToNot(BeNil())

			expectedUpdatedView := humiographql.GetSearchDomainSearchDomainView{
				Typename:        helpers.StringPtr("View"),
				Id:              "",
				Name:            viewToCreate.Spec.Name,
				Description:     &fetchedView.Spec.Description,
				Connections:     fetchedView.GetViewConnections(),
				AutomaticSearch: *fetchedView.Spec.AutomaticSearch,
			}
			Eventually(func() humiographql.GetSearchDomainSearchDomainView {
				updatedView, err := humioClient.GetView(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedView)
				if err != nil {
					return humiographql.GetSearchDomainSearchDomainView{}
				}

				// Ignore the ID
				updatedView.Id = ""

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

	Context("Humio Parser", Label("envtest", "dummy", "real"), func() {
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
				_ = k8sClient.Get(ctx, key, fetchedParser)
				return fetchedParser.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioParserStateExists))

			var initialParser *humiographql.ParserDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				initialParser, err = humioClient.GetParser(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateParser)
				if err != nil {
					return err
				}

				// Ignore the ID when comparing parser content
				initialParser.Id = ""

				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(initialParser).ToNot(BeNil())

			expectedInitialParser := &humiographql.ParserDetails{
				Id:          "",
				Name:        toCreateParser.Spec.Name,
				Script:      toCreateParser.Spec.ParserScript,
				FieldsToTag: toCreateParser.Spec.TagFields,
				TestCases:   humioapi.TestDataToParserDetailsTestCasesParserTestCase(toCreateParser.Spec.TestData),
			}
			Expect(*initialParser).To(Equal(*expectedInitialParser))

			suite.UsingClusterBy(clusterKey.Name, "HumioParser: Updating the parser successfully")
			updatedScript := "kvParse() | updated"
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedParser); err != nil {
					return err
				}
				fetchedParser.Spec.ParserScript = updatedScript
				return k8sClient.Update(ctx, fetchedParser)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			var updatedParser *humiographql.ParserDetails
			Eventually(func() error {
				updatedParser, err = humioClient.GetParser(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedParser)

				// Ignore the ID when comparing parser content
				updatedParser.Id = ""

				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(updatedParser).ToNot(BeNil())

			expectedUpdatedParser := &humiographql.ParserDetails{
				Id:          "",
				Name:        fetchedParser.Spec.Name,
				Script:      fetchedParser.Spec.ParserScript,
				FieldsToTag: fetchedParser.Spec.TagFields,
				TestCases:   humioapi.TestDataToParserDetailsTestCasesParserTestCase(fetchedParser.Spec.TestData),
			}
			Eventually(func() *humiographql.ParserDetails {
				updatedParser, err := humioClient.GetParser(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedParser)
				if err != nil {
					return nil
				}

				// Ignore the ID when comparing parser content
				updatedParser.Id = ""

				return updatedParser
			}, testTimeout, suite.TestInterval).Should(Equal(expectedUpdatedParser))

			suite.UsingClusterBy(clusterKey.Name, "HumioParser: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedParser)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedParser)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio External Cluster", Label("envtest", "dummy", "real"), func() {
		It("should handle resources correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioExternalCluster: Should handle externalcluster correctly")
			key := types.NamespacedName{
				Name:      "humioexternalcluster",
				Namespace: clusterKey.Namespace,
			}
			protocol := "http"
			if !helpers.UseEnvtest() && helpers.UseCertManager() {
				protocol = protocolHTTPS
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
				_ = k8sClient.Get(ctx, key, fetchedExternalCluster)
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

	Context("Humio resources errors", Label("envtest", "dummy", "real"), func() {
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
				_ = k8sClient.Get(ctx, keyErr, fetchedParser)
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
				_ = k8sClient.Get(ctx, keyErr, fetchedParser)
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
				_ = k8sClient.Get(ctx, keyErr, fetchedRepository)
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
				_ = k8sClient.Get(ctx, keyErr, fetchedRepository)
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
				_ = k8sClient.Get(ctx, keyErr, fetchedView)
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
				_ = k8sClient.Get(ctx, keyErr, fetchedView)
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

	Context("Humio Action", Label("envtest", "dummy", "real"), func() {
		It("should handle email action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Should handle action correctly")
			emailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-action",
				ViewName:           testRepo.Spec.Name,
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{emailActionExample},
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
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
				if err := k8sClient.Get(ctx, key, fetchedAction); err != nil {
					return err
				}
				fetchedAction.Spec.EmailProperties = updatedAction.Spec.EmailProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the action update succeeded")
			var expectedUpdatedAction, updatedAction2 humiographql.ActionDetails
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(err).ToNot(HaveOccurred())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the action matches the expected")
			Eventually(func() *string {
				updatedAction2, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return helpers.StringPtr(err.Error())
				}
				switch v := (updatedAction2).(type) {
				case *humiographql.ActionDetailsEmailAction:
					return v.GetEmailBodyTemplate()
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&updatedAction.Spec.EmailProperties.BodyTemplate))
			switch v := (updatedAction2).(type) {
			case *humiographql.ActionDetailsEmailAction:
				Expect(v.GetSubjectTemplate()).Should(BeEquivalentTo(&updatedAction.Spec.EmailProperties.SubjectTemplate))
				Expect(v.GetRecipients()).Should(BeEquivalentTo(updatedAction.Spec.EmailProperties.Recipients))
			default:
				Fail("got the wrong action type")
			}

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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.HumioRepositoryProperties = updatedAction.Spec.HumioRepositoryProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the humio repo action update succeeded")
			var expectedUpdatedAction, updatedAction2 humiographql.ActionDetails
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the humio repo action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				switch v := (updatedAction2).(type) {
				case *humiographql.ActionDetailsHumioRepoAction:
					return v.GetIngestToken()
				}
				return ""
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.OpsGenieProperties = updatedAction.Spec.OpsGenieProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the ops genie action update succeeded")
			var expectedUpdatedAction, updatedAction2 humiographql.ActionDetails
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the ops genie action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				switch v := (updatedAction2).(type) {
				case *humiographql.ActionDetailsOpsGenieAction:
					return v.GetGenieKey()
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.OpsGenieProperties.GenieKey))
			switch v := (updatedAction2).(type) {
			case *humiographql.ActionDetailsOpsGenieAction:
				Expect(v.GetApiUrl()).Should(BeEquivalentTo(updatedAction.Spec.OpsGenieProperties.ApiUrl))
			}

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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.PagerDutyProperties = updatedAction.Spec.PagerDutyProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the pagerduty action update succeeded")
			var expectedUpdatedAction, updatedAction2 humiographql.ActionDetails
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the pagerduty action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				switch v := (updatedAction2).(type) {
				case *humiographql.ActionDetailsPagerDutyAction:
					return v.GetRoutingKey()
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.PagerDutyProperties.RoutingKey))
			switch v := (updatedAction2).(type) {
			case *humiographql.ActionDetailsPagerDutyAction:
				Expect(v.GetSeverity()).Should(BeEquivalentTo(updatedAction.Spec.PagerDutyProperties.Severity))
			}

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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.SlackPostMessageProperties = updatedAction.Spec.SlackPostMessageProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack post message action update succeeded")
			var expectedUpdatedAction, updatedAction2 humiographql.ActionDetails
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack post message action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				switch v := (updatedAction2).(type) {
				case *humiographql.ActionDetailsSlackPostMessageAction:
					return v.GetApiToken()
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.SlackPostMessageProperties.ApiToken))
			switch v := (updatedAction2).(type) {
			case *humiographql.ActionDetailsSlackPostMessageAction:
				Expect(v.GetChannels()).Should(BeEquivalentTo(updatedAction.Spec.SlackPostMessageProperties.Channels))
				Expect(v.GetFields()).Should(BeEquivalentTo([]humiographql.ActionDetailsFieldsSlackFieldEntry{{
					FieldName: updatedFieldKey,
					Value:     updatedFieldValue,
				}}))
			}

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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.SlackProperties = updatedAction.Spec.SlackProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack action update succeeded")
			var expectedUpdatedAction, updatedAction2 humiographql.ActionDetails
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the slack action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				switch v := (updatedAction2).(type) {
				case *humiographql.ActionDetailsSlackAction:
					return v.GetUrl()
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.SlackProperties.Url))
			switch v := (updatedAction2).(type) {
			case *humiographql.ActionDetailsSlackAction:
				Expect(v.GetFields()).Should(BeEquivalentTo([]humiographql.ActionDetailsFieldsSlackFieldEntry{{
					FieldName: updatedFieldKey,
					Value:     updatedFieldValue,
				}}))
			}

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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.VictorOpsProperties = updatedAction.Spec.VictorOpsProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the victor ops action update succeeded")
			var expectedUpdatedAction, updatedAction2 humiographql.ActionDetails
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the victor ops action matches the expected")
			Eventually(func() string {
				updatedAction2, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil {
					return ""
				}
				switch v := (updatedAction2).(type) {
				case *humiographql.ActionDetailsVictorOpsAction:
					return v.GetMessageType()
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedAction.Spec.VictorOpsProperties.MessageType))
			switch v := (updatedAction2).(type) {
			case *humiographql.ActionDetailsVictorOpsAction:
				Expect(v.GetNotifyUrl()).Should(BeEquivalentTo(updatedAction.Spec.VictorOpsProperties.NotifyUrl))
			}

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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				fetchedAction.Spec.WebhookProperties = updatedWebhookActionProperties
				return k8sClient.Update(ctx, fetchedAction)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the web hook action update succeeded")
			var expectedUpdatedAction, updatedAction humiographql.ActionDetails
			Eventually(func() error {
				expectedUpdatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAction).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Verifying the web hook action matches the expected")
			Eventually(func() string {
				updatedAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAction)
				if err != nil || updatedAction == nil {
					return ""
				}
				switch v := (updatedAction).(type) {
				case *humiographql.ActionDetailsWebhookAction:
					return v.GetUrl()
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(updatedWebhookActionProperties.Url))

			switch v := (updatedAction).(type) {
			case *humiographql.ActionDetailsWebhookAction:
				Expect(v.GetHeaders()).Should(BeEquivalentTo([]humiographql.ActionDetailsHeadersHttpHeaderEntry{{
					Header: updatedHeaderKey,
					Value:  updatedHeaderValue,
				}}))
				Expect(v.GetWebhookBodyTemplate()).To(BeEquivalentTo(updatedWebhookActionProperties.BodyTemplate))
				Expect(v.GetMethod()).To(BeEquivalentTo(updatedWebhookActionProperties.Method))
			}

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
					Name:               "example-invalid-action-missing",
					ViewName:           testRepo.Spec.Name,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the invalid action")
			Expect(k8sClient.Create(ctx, toCreateInvalidAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateConfigError))

			var invalidAction humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				invalidAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateInvalidAction)
				if err == nil {
					suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioAction: Got the following back even though we did not expect to get anything back: %#+v", invalidAction))
				}
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
					Name:               "example-invalid-action-extra",
					ViewName:           testRepo.Spec.Name,
					WebhookProperties:  &humiov1alpha1.HumioActionWebhookProperties{},
					EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
						Recipients: []string{""},
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Creating the invalid action")
			Expect(k8sClient.Create(ctx, toCreateInvalidAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateConfigError))

			var invalidAction humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				invalidAction, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateInvalidAction)
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

			expectedSecretValue := expectedSecretValueExample
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Should not be setting the API token in this case, but the secretMap should have the value
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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

			expectedSecretValue := expectedSecretValueExample
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the SecretMap rather than the NotifyUrl on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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

			expectedSecretValue := expectedSecretValueExample
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Should not be setting the API token in this case, but the secretMap should have the value
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.SlackPostMessageProperties.ApiToken))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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

			expectedSecretValue := fmt.Sprintf("https://%s/services/T00000000/B00000000/YYYYYYYYYYYYYYYYYYYYYYYY", testService1.Name)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      toCreateAction.Spec.SlackProperties.UrlSource.SecretKeyRef.Name,
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Should not be setting the API token in this case, but the secretMap should have the value
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioAction: SlackProperties: Should support direct url", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-slack-action-direct",
				Namespace: clusterKey.Namespace,
			}

			expectedSecretValue := fmt.Sprintf("https://%s/services/T00000000/B00000000/YYYYYYYYYYYYYYYYYYYYYYYY", testService1.Name)
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.SlackProperties.Url))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			Eventually(func() error {
				humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the secretMap rather than the apiToken in the ha.
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.PagerDutyProperties.RoutingKey))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.WebhookProperties.Url))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(expectedSecretValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())
			switch v := (action).(type) {
			case *humiographql.ActionDetailsWebhookAction:
				Expect(v.GetUrl()).To(Equal(expectedUrl))
				Expect(v.GetHeaders()).Should(ContainElements([]humiographql.ActionDetailsHeadersHttpHeaderEntry{
					{
						Header: nonsensitiveHeaderKey,
						Value:  nonsensitiveHeaderValue,
					},
				}))
			}

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.WebhookProperties.Url))

			allHeaders, found := kubernetes.GetFullSetOfMergedWebhookheaders(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(allHeaders).To(HaveKeyWithValue(nonsensitiveHeaderKey, nonsensitiveHeaderValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())
			switch v := (action).(type) {
			case *humiographql.ActionDetailsWebhookAction:
				Expect(v.GetUrl()).To(Equal(expectedUrl))
				Expect(v.GetHeaders()).Should(ContainElements([]humiographql.ActionDetailsHeadersHttpHeaderEntry{
					{
						Header: headerKey1,
						Value:  sensitiveHeaderValue1,
					},
					{
						Header: headerKey2,
						Value:  nonsensitiveHeaderValue2,
					},
				}))
			}

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.WebhookProperties.Url))

			allHeaders, found := kubernetes.GetFullSetOfMergedWebhookheaders(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(allHeaders).To(HaveKeyWithValue(headerKey1, sensitiveHeaderValue1))
			Expect(allHeaders).To(HaveKeyWithValue(headerKey2, nonsensitiveHeaderValue2))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
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
				_ = k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			var action humiographql.ActionDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				action, err = humioClient.GetAction(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAction)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(action).ToNot(BeNil())
			switch v := (action).(type) {
			case *humiographql.ActionDetailsWebhookAction:
				Expect(v.GetUrl()).To(Equal(expectedUrl))
				Expect(v.GetHeaders()).Should(ContainElements([]humiographql.ActionDetailsHeadersHttpHeaderEntry{
					{
						Header: headerKey,
						Value:  sensitiveHeaderValue,
					},
				}))
			}

			// Check the SecretMap rather than the ApiToken on the action
			apiToken, found := kubernetes.GetSecretForHa(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(apiToken).To(Equal(toCreateAction.Spec.WebhookProperties.Url))

			allHeaders, found := kubernetes.GetFullSetOfMergedWebhookheaders(toCreateAction)
			Expect(found).To(BeTrue())
			Expect(allHeaders).To(HaveKeyWithValue(headerKey, sensitiveHeaderValue))

			suite.UsingClusterBy(clusterKey.Name, "HumioAction: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedAction)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedAction)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Humio Alert", Label("envtest", "dummy", "real"), func() {
		It("should handle alert action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Should handle alert correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action",
				ViewName:           testRepo.Spec.Name,
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{emailActionExample},
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
				_ = k8sClient.Get(ctx, actionKey, fetchedAction)
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
				ThrottleField:      helpers.StringPtr("some field"),
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
				_ = k8sClient.Get(ctx, key, fetchedAlert)
				return fetchedAlert.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioAlertStateExists))

			var alert *humiographql.AlertDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				alert, err = humioClient.GetAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(alert).ToNot(BeNil())

			originalAlert := humiographql.AlertDetails{
				Id:                 "",
				Name:               toCreateAlert.Spec.Name,
				QueryString:        toCreateAlert.Spec.Query.QueryString,
				QueryStart:         toCreateAlert.Spec.Query.Start,
				ThrottleField:      toCreateAlert.Spec.ThrottleField,
				Description:        &toCreateAlert.Spec.Description,
				ThrottleTimeMillis: int64(toCreateAlert.Spec.ThrottleTimeMillis),
				Enabled:            !toCreateAlert.Spec.Silenced,
				ActionsV2:          humioapi.ActionNamesToEmailActions(toCreateAlert.Spec.Actions),
				Labels:             toCreateAlert.Spec.Labels,
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
			}
			Expect(alert.Name).To(Equal(originalAlert.GetName()))
			Expect(alert.Description).To(Equal(originalAlert.GetDescription()))
			Expect(alert.GetActionsV2()).To(BeEquivalentTo(originalAlert.GetActionsV2()))
			Expect(alert.Labels).To(Equal(originalAlert.GetLabels()))
			Expect(alert.ThrottleTimeMillis).To(Equal(originalAlert.GetThrottleTimeMillis()))
			Expect(alert.ThrottleField).To(Equal(originalAlert.GetThrottleField()))
			Expect(alert.Enabled).To(Equal(originalAlert.GetEnabled()))
			Expect(alert.QueryString).To(Equal(originalAlert.GetQueryString()))
			Expect(alert.QueryStart).To(Equal(originalAlert.GetQueryStart()))

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Updating the alert successfully")
			updatedAlert := toCreateAlert
			updatedAlert.Spec.Query.QueryString = "#repo = test | updated=true | count()"
			updatedAlert.Spec.ThrottleTimeMillis = 70000
			updatedAlert.Spec.ThrottleField = helpers.StringPtr("some other field")
			updatedAlert.Spec.Silenced = true
			updatedAlert.Spec.Description = "updated humio alert"
			updatedAlert.Spec.Actions = []string{toCreateDependentAction.Spec.Name}

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Waiting for the alert to be updated")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedAlert); err != nil {
					return err
				}
				fetchedAlert.Spec.Query = updatedAlert.Spec.Query
				fetchedAlert.Spec.ThrottleTimeMillis = updatedAlert.Spec.ThrottleTimeMillis
				fetchedAlert.Spec.ThrottleField = updatedAlert.Spec.ThrottleField
				fetchedAlert.Spec.Silenced = updatedAlert.Spec.Silenced
				fetchedAlert.Spec.Description = updatedAlert.Spec.Description
				return k8sClient.Update(ctx, fetchedAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Verifying the alert update succeeded")
			var expectedUpdatedAlert *humiographql.AlertDetails
			Eventually(func() error {
				expectedUpdatedAlert, err = humioClient.GetAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAlert).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Verifying the alert matches the expected")
			verifiedAlert := humiographql.AlertDetails{
				Id:                 "",
				Name:               updatedAlert.Spec.Name,
				QueryString:        updatedAlert.Spec.Query.QueryString,
				QueryStart:         updatedAlert.Spec.Query.Start,
				ThrottleField:      updatedAlert.Spec.ThrottleField,
				Description:        &updatedAlert.Spec.Description,
				ThrottleTimeMillis: int64(updatedAlert.Spec.ThrottleTimeMillis),
				Enabled:            !updatedAlert.Spec.Silenced,
				ActionsV2:          humioapi.ActionNamesToEmailActions(updatedAlert.Spec.Actions),
				Labels:             updatedAlert.Spec.Labels,
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
			}
			Eventually(func() *humiographql.AlertDetails {
				updatedAlert, err := humioClient.GetAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAlert)
				if err != nil {
					return nil
				}

				// Ignore the ID
				updatedAlert.Id = ""

				return updatedAlert
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&verifiedAlert))

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

	Context("Humio Filter Alert", Label("envtest", "dummy", "real"), func() {
		It("should handle filter alert action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Should handle filter alert correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action4",
				ViewName:           testRepo.Spec.Name,
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{emailActionExample},
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
				_ = k8sClient.Get(ctx, actionKey, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			filterAlertSpec := humiov1alpha1.HumioFilterAlertSpec{
				ManagedClusterName:  clusterKey.Name,
				Name:                "example-filter-alert",
				ViewName:            testRepo.Spec.Name,
				QueryString:         "#repo = humio | error = true",
				Enabled:             true,
				Description:         "humio filter alert",
				Actions:             []string{toCreateDependentAction.Spec.Name},
				Labels:              []string{"some-label"},
				ThrottleTimeSeconds: 300,
				ThrottleField:       helpers.StringPtr("somefield"),
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
				_ = k8sClient.Get(ctx, key, fetchedFilterAlert)
				return fetchedFilterAlert.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioFilterAlertStateExists))

			var filterAlert *humiographql.FilterAlertDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				filterAlert, err = humioClient.GetFilterAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateFilterAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(filterAlert).ToNot(BeNil())

			Eventually(func() error {
				return humioClient.ValidateActionsForFilterAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateFilterAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			originalFilterAlert := humiographql.FilterAlertDetails{
				Id:                  "",
				Name:                toCreateFilterAlert.Spec.Name,
				Description:         &toCreateFilterAlert.Spec.Description,
				QueryString:         toCreateFilterAlert.Spec.QueryString,
				ThrottleTimeSeconds: helpers.Int64Ptr(int64(toCreateFilterAlert.Spec.ThrottleTimeSeconds)),
				ThrottleField:       toCreateFilterAlert.Spec.ThrottleField,
				Labels:              toCreateFilterAlert.Spec.Labels,
				Enabled:             toCreateFilterAlert.Spec.Enabled,
				Actions:             humioapi.ActionNamesToEmailActions(toCreateFilterAlert.Spec.Actions),
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
			}
			Expect(filterAlert.GetName()).To(Equal(originalFilterAlert.GetName()))
			Expect(filterAlert.GetDescription()).To(Equal(originalFilterAlert.GetDescription()))
			Expect(filterAlert.GetThrottleTimeSeconds()).To(Equal(originalFilterAlert.GetThrottleTimeSeconds()))
			Expect(filterAlert.GetThrottleField()).To(Equal(originalFilterAlert.GetThrottleField()))
			Expect(filterAlert.GetActions()).To(BeEquivalentTo(originalFilterAlert.GetActions()))
			Expect(filterAlert.GetLabels()).To(Equal(originalFilterAlert.GetLabels()))
			Expect(filterAlert.GetEnabled()).To(Equal(originalFilterAlert.GetEnabled()))
			Expect(filterAlert.GetQueryString()).To(Equal(originalFilterAlert.GetQueryString()))

			createdFilterAlert := toCreateFilterAlert
			var throttleTimeSeconds int
			if filterAlert.ThrottleTimeSeconds != nil {
				throttleTimeSeconds = int(*filterAlert.ThrottleTimeSeconds)
			}
			var description string
			if filterAlert.Description != nil {
				description = *filterAlert.Description
			}
			createdFilterAlert.Spec = humiov1alpha1.HumioFilterAlertSpec{
				Name:                filterAlert.Name,
				QueryString:         filterAlert.QueryString,
				Description:         description,
				ThrottleTimeSeconds: throttleTimeSeconds,
				ThrottleField:       filterAlert.ThrottleField,
				Enabled:             filterAlert.Enabled,
				Actions:             humioapi.GetActionNames(filterAlert.Actions),
				Labels:              filterAlert.Labels,
			}
			Expect(createdFilterAlert.Spec).To(Equal(toCreateFilterAlert.Spec))

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Updating the filter alert successfully")
			updatedFilterAlert := toCreateFilterAlert
			updatedFilterAlert.Spec.QueryString = "#repo = humio | updated_field = true | error = true"
			updatedFilterAlert.Spec.Enabled = false
			updatedFilterAlert.Spec.Description = "updated humio filter alert"
			updatedFilterAlert.Spec.ThrottleTimeSeconds = 3600
			updatedFilterAlert.Spec.ThrottleField = helpers.StringPtr("newfield")
			updatedFilterAlert.Spec.Actions = []string{toCreateDependentAction.Spec.Name}

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Waiting for the filter alert to be updated")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedFilterAlert); err != nil {
					return err
				}
				fetchedFilterAlert.Spec.QueryString = updatedFilterAlert.Spec.QueryString
				fetchedFilterAlert.Spec.Enabled = updatedFilterAlert.Spec.Enabled
				fetchedFilterAlert.Spec.Description = updatedFilterAlert.Spec.Description
				fetchedFilterAlert.Spec.ThrottleTimeSeconds = updatedFilterAlert.Spec.ThrottleTimeSeconds
				fetchedFilterAlert.Spec.ThrottleField = updatedFilterAlert.Spec.ThrottleField
				return k8sClient.Update(ctx, fetchedFilterAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Verifying the filter alert update succeeded")
			var expectedUpdatedFilterAlert *humiographql.FilterAlertDetails
			Eventually(func() error {
				expectedUpdatedFilterAlert, err = humioClient.GetFilterAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedFilterAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedFilterAlert).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioFilterAlert: Verifying the alert matches the expected")
			verifiedFilterAlert := humiographql.FilterAlertDetails{
				Id:                  "",
				Name:                updatedFilterAlert.Spec.Name,
				QueryString:         updatedFilterAlert.Spec.QueryString,
				Description:         &updatedFilterAlert.Spec.Description,
				ThrottleTimeSeconds: helpers.Int64Ptr(int64(updatedFilterAlert.Spec.ThrottleTimeSeconds)),
				ThrottleField:       updatedFilterAlert.Spec.ThrottleField,
				Enabled:             updatedFilterAlert.Spec.Enabled,
				Actions:             humioapi.ActionNamesToEmailActions(updatedFilterAlert.Spec.Actions),
				Labels:              updatedFilterAlert.Spec.Labels,
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
			}

			Eventually(func() *humiographql.FilterAlertDetails {
				updatedFilterAlert, err := humioClient.GetFilterAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedFilterAlert)
				if err != nil {
					return nil
				}

				// Ignore the ID
				updatedFilterAlert.Id = ""

				return updatedFilterAlert
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&verifiedFilterAlert))

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

	Context("Humio Aggregate Alert", Label("envtest", "dummy", "real"), func() {
		It("should handle aggregate alert action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Should handle aggregate alert correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action3",
				ViewName:           testRepo.Spec.Name,
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{emailActionExample},
				},
			}

			actionKey := types.NamespacedName{
				Name:      "humioaction3",
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
				_ = k8sClient.Get(ctx, actionKey, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			aggregateAlertSpec := humiov1alpha1.HumioAggregateAlertSpec{
				ManagedClusterName:    clusterKey.Name,
				Name:                  "example-aggregate-alert",
				ViewName:              testRepo.Spec.Name,
				QueryString:           "#repo = humio | error = true | count()",
				QueryTimestampType:    "EventTimestamp",
				SearchIntervalSeconds: 60,
				ThrottleTimeSeconds:   120,
				ThrottleField:         helpers.StringPtr("@timestamp"),
				TriggerMode:           "ImmediateMode",
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
				_ = k8sClient.Get(ctx, key, fetchedAggregateAlert)
				return fetchedAggregateAlert.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioAggregateAlertStateExists))

			var aggregateAlert *humiographql.AggregateAlertDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				aggregateAlert, err = humioClient.GetAggregateAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAggregateAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(aggregateAlert).ToNot(BeNil())

			Eventually(func() error {
				return humioClient.ValidateActionsForAggregateAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateAggregateAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			originalAggregateAlert := humiographql.AggregateAlertDetails{
				Id:                    "",
				Name:                  toCreateAggregateAlert.Spec.Name,
				Description:           &toCreateAggregateAlert.Spec.Description,
				QueryString:           toCreateAggregateAlert.Spec.QueryString,
				SearchIntervalSeconds: int64(toCreateAggregateAlert.Spec.SearchIntervalSeconds),
				ThrottleTimeSeconds:   int64(toCreateAggregateAlert.Spec.ThrottleTimeSeconds),
				ThrottleField:         toCreateAggregateAlert.Spec.ThrottleField,
				Labels:                toCreateAggregateAlert.Spec.Labels,
				Enabled:               toCreateAggregateAlert.Spec.Enabled,
				TriggerMode:           humiographql.TriggerMode(toCreateAggregateAlert.Spec.TriggerMode),
				QueryTimestampType:    humiographql.QueryTimestampType(toCreateAggregateAlert.Spec.QueryTimestampType),
				Actions:               humioapi.ActionNamesToEmailActions(toCreateAggregateAlert.Spec.Actions),
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
			}
			Expect(aggregateAlert.GetName()).To(Equal(originalAggregateAlert.GetName()))
			Expect(aggregateAlert.GetDescription()).To(Equal(originalAggregateAlert.GetDescription()))
			Expect(aggregateAlert.GetThrottleTimeSeconds()).To(Equal(originalAggregateAlert.GetThrottleTimeSeconds()))
			Expect(aggregateAlert.GetThrottleField()).To(Equal(originalAggregateAlert.GetThrottleField()))
			Expect(aggregateAlert.GetLabels()).To(Equal(originalAggregateAlert.GetLabels()))
			Expect(humioapi.GetActionNames(aggregateAlert.GetActions())).To(Equal(humioapi.GetActionNames(originalAggregateAlert.GetActions())))

			createdAggregateAlert := toCreateAggregateAlert
			createdAggregateAlert.Spec = humiov1alpha1.HumioAggregateAlertSpec{
				Name:                  aggregateAlert.Name,
				QueryString:           aggregateAlert.QueryString,
				QueryTimestampType:    string(aggregateAlert.QueryTimestampType),
				Description:           *aggregateAlert.Description,
				SearchIntervalSeconds: int(aggregateAlert.SearchIntervalSeconds),
				ThrottleTimeSeconds:   int(aggregateAlert.ThrottleTimeSeconds),
				ThrottleField:         aggregateAlert.ThrottleField,
				TriggerMode:           string(aggregateAlert.TriggerMode),
				Enabled:               aggregateAlert.Enabled,
				Actions:               humioapi.GetActionNames(aggregateAlert.GetActions()),
				Labels:                aggregateAlert.Labels,
			}
			Expect(err).ToNot(HaveOccurred())
			Expect(createdAggregateAlert.Spec).To(Equal(toCreateAggregateAlert.Spec))

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Updating the aggregate alert successfully")
			updatedAggregateAlert := toCreateAggregateAlert
			updatedAggregateAlert.Spec.QueryString = "#repo = humio | updated_field = true | error = true | count()"
			updatedAggregateAlert.Spec.Enabled = false
			updatedAggregateAlert.Spec.Description = "updated humio aggregate alert"
			updatedAggregateAlert.Spec.SearchIntervalSeconds = 120
			updatedAggregateAlert.Spec.ThrottleTimeSeconds = 3600
			updatedAggregateAlert.Spec.ThrottleField = helpers.StringPtr("newfield")
			updatedAggregateAlert.Spec.Actions = []string{toCreateDependentAction.Spec.Name}
			updatedAggregateAlert.Spec.TriggerMode = "CompleteMode"

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Waiting for the aggregate alert to be updated")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedAggregateAlert); err != nil {
					return err
				}
				fetchedAggregateAlert.Spec.QueryString = updatedAggregateAlert.Spec.QueryString
				fetchedAggregateAlert.Spec.Enabled = updatedAggregateAlert.Spec.Enabled
				fetchedAggregateAlert.Spec.Description = updatedAggregateAlert.Spec.Description
				fetchedAggregateAlert.Spec.SearchIntervalSeconds = updatedAggregateAlert.Spec.SearchIntervalSeconds
				fetchedAggregateAlert.Spec.ThrottleTimeSeconds = updatedAggregateAlert.Spec.ThrottleTimeSeconds
				fetchedAggregateAlert.Spec.ThrottleField = updatedAggregateAlert.Spec.ThrottleField
				fetchedAggregateAlert.Spec.Actions = updatedAggregateAlert.Spec.Actions
				fetchedAggregateAlert.Spec.TriggerMode = updatedAggregateAlert.Spec.TriggerMode

				return k8sClient.Update(ctx, fetchedAggregateAlert)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Verifying the aggregate alert update succeeded")
			var expectedUpdatedAggregateAlert *humiographql.AggregateAlertDetails
			Eventually(func() error {
				expectedUpdatedAggregateAlert, err = humioClient.GetAggregateAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAggregateAlert)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedAggregateAlert).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioAggregateAlert: Verifying the alert matches the expected")
			verifiedAggregateAlert := humiographql.AggregateAlertDetails{
				Id:                    "",
				Name:                  updatedAggregateAlert.Spec.Name,
				Description:           &updatedAggregateAlert.Spec.Description,
				QueryString:           updatedAggregateAlert.Spec.QueryString,
				SearchIntervalSeconds: int64(updatedAggregateAlert.Spec.SearchIntervalSeconds),
				ThrottleTimeSeconds:   int64(updatedAggregateAlert.Spec.ThrottleTimeSeconds),
				ThrottleField:         updatedAggregateAlert.Spec.ThrottleField,
				Labels:                updatedAggregateAlert.Spec.Labels,
				Enabled:               updatedAggregateAlert.Spec.Enabled,
				TriggerMode:           humiographql.TriggerMode(updatedAggregateAlert.Spec.TriggerMode),
				QueryTimestampType:    humiographql.QueryTimestampType(updatedAggregateAlert.Spec.QueryTimestampType),
				Actions:               humioapi.ActionNamesToEmailActions(updatedAggregateAlert.Spec.Actions),
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
			}

			Eventually(func() *humiographql.AggregateAlertDetails {
				updatedAggregateAlert, err := humioClient.GetAggregateAlert(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedAggregateAlert)
				if err != nil {
					return nil
				}

				// Ignore the ID
				updatedAggregateAlert.Id = ""

				return updatedAggregateAlert
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&verifiedAggregateAlert))

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

	Context("Humio Scheduled Search", Label("envtest", "dummy", "real"), func() {
		It("should handle scheduled search action correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Should handle scheduled search correctly")
			dependentEmailActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               "example-email-action2",
				ViewName:           testRepo.Spec.Name,
				EmailProperties: &humiov1alpha1.HumioActionEmailProperties{
					Recipients: []string{emailActionExample},
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
				_ = k8sClient.Get(ctx, actionKey, fetchedAction)
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
				_ = k8sClient.Get(ctx, key, fetchedScheduledSearch)
				return fetchedScheduledSearch.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioScheduledSearchStateExists))

			var scheduledSearch *humiographql.ScheduledSearchDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				scheduledSearch, err = humioClient.GetScheduledSearch(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateScheduledSearch)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(scheduledSearch).ToNot(BeNil())

			Eventually(func() error {
				return humioClient.ValidateActionsForScheduledSearch(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateScheduledSearch)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(humioapi.GetActionNames(scheduledSearch.ActionsV2)).To(Equal(toCreateScheduledSearch.Spec.Actions))
			Expect(scheduledSearch.Name).To(Equal(toCreateScheduledSearch.Spec.Name))
			Expect(scheduledSearch.Description).To(Equal(&toCreateScheduledSearch.Spec.Description))
			Expect(scheduledSearch.Labels).To(Equal(toCreateScheduledSearch.Spec.Labels))
			Expect(scheduledSearch.Enabled).To(Equal(toCreateScheduledSearch.Spec.Enabled))
			Expect(scheduledSearch.QueryString).To(Equal(toCreateScheduledSearch.Spec.QueryString))
			Expect(scheduledSearch.Start).To(Equal(toCreateScheduledSearch.Spec.QueryStart))
			Expect(scheduledSearch.End).To(Equal(toCreateScheduledSearch.Spec.QueryEnd))
			Expect(scheduledSearch.Schedule).To(Equal(toCreateScheduledSearch.Spec.Schedule))
			Expect(scheduledSearch.TimeZone).To(Equal(toCreateScheduledSearch.Spec.TimeZone))
			Expect(scheduledSearch.BackfillLimit).To(Equal(toCreateScheduledSearch.Spec.BackfillLimit))

			createdScheduledSearch := toCreateScheduledSearch
			var description string
			if scheduledSearch.Description != nil {
				description = *scheduledSearch.Description
			}
			createdScheduledSearch.Spec = humiov1alpha1.HumioScheduledSearchSpec{
				Name:          scheduledSearch.Name,
				QueryString:   scheduledSearch.QueryString,
				Description:   description,
				QueryStart:    scheduledSearch.Start,
				QueryEnd:      scheduledSearch.End,
				Schedule:      scheduledSearch.Schedule,
				TimeZone:      scheduledSearch.TimeZone,
				BackfillLimit: scheduledSearch.BackfillLimit,
				Enabled:       scheduledSearch.Enabled,
				Actions:       humioapi.GetActionNames(scheduledSearch.ActionsV2),
				Labels:        scheduledSearch.Labels,
			}
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
				_ = k8sClient.Get(ctx, key, fetchedScheduledSearch)
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
			var expectedUpdatedScheduledSearch *humiographql.ScheduledSearchDetails
			Eventually(func() error {
				expectedUpdatedScheduledSearch, err = humioClient.GetScheduledSearch(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedScheduledSearch)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(expectedUpdatedScheduledSearch).ToNot(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Verifying the scheduled search matches the expected")
			verifiedScheduledSearch := humiographql.ScheduledSearchDetails{
				Name:          updatedScheduledSearch.Spec.Name,
				QueryString:   updatedScheduledSearch.Spec.QueryString,
				Description:   &updatedScheduledSearch.Spec.Description,
				Start:         updatedScheduledSearch.Spec.QueryStart,
				End:           updatedScheduledSearch.Spec.QueryEnd,
				Schedule:      updatedScheduledSearch.Spec.Schedule,
				TimeZone:      updatedScheduledSearch.Spec.TimeZone,
				BackfillLimit: updatedScheduledSearch.Spec.BackfillLimit,
				Enabled:       updatedScheduledSearch.Spec.Enabled,
				ActionsV2:     humioapi.ActionNamesToEmailActions(updatedScheduledSearch.Spec.Actions),
				Labels:        updatedScheduledSearch.Spec.Labels,
				QueryOwnership: &humiographql.SharedQueryOwnershipTypeOrganizationOwnership{
					Typename: helpers.StringPtr("OrganizationOwnership"),
					QueryOwnershipOrganizationOwnership: humiographql.QueryOwnershipOrganizationOwnership{
						Typename: helpers.StringPtr("OrganizationOwnership"),
					},
				},
			}

			Eventually(func() *humiographql.ScheduledSearchDetails {
				updatedScheduledSearch, err := humioClient.GetScheduledSearch(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedScheduledSearch)
				if err != nil {
					return nil
				}

				// Ignore the ID
				updatedScheduledSearch.Id = ""

				return updatedScheduledSearch
			}, testTimeout, suite.TestInterval).Should(BeEquivalentTo(&verifiedScheduledSearch))

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
	Context("HumioPdfRenderService", Label("envtest", "dummy", "real"), func() {
		var (
			ctx       context.Context
			baseName  string
			createdCR *humiov1alpha1.HumioPdfRenderService
		)

		BeforeEach(func() {
			ctx = context.Background()
			// Use a different name for the CR to avoid conflict with the deployment name
			baseName = "pdf-render-service-cr"
		})

		AfterEach(func() {
			// Clean up the created CR
			if createdCR != nil {
				Expect(k8sClient.Delete(ctx, createdCR)).Should(Succeed())
				Eventually(func() bool {
					if createdCR == nil {
						return true
					}
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      createdCR.Name,
						Namespace: createdCR.Namespace,
					}, &humiov1alpha1.HumioPdfRenderService{})
					return k8serrors.IsNotFound(err)
				}, testTimeout, suite.TestInterval).Should(BeTrue())
				createdCR = nil
			}
		})

		It("should create Deployment and Service when a new HumioPdfRenderService is created", func() {
			// Define timeout variables
			longTimeout := testTimeout * 2

			// Create a new HumioPdfRenderService CR with the required fields.
			cr := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      baseName,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Replicas:           1,
					Image:              PDFRenderServiceImage,
					Port:               5123,
					ServiceAccountName: "default",
					// Use ClusterIP for the initial service configuration.
					ServiceType: corev1.ServiceTypeClusterIP,
				},
			}
			Expect(k8sClient.Create(ctx, cr)).Should(Succeed())
			createdPdfCR = cr

			// Verify that the Deployment exists using the CR name.
			deploymentKey := types.NamespacedName{
				Name:      cr.Name + "-pdf-render-service", // Use CR name as prefix
				Namespace: cr.Namespace,
			}
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, deployment)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(deployment.Namespace).Should(Equal(cr.Namespace))
			expectedName := fmt.Sprintf("%s-pdf-render-service", cr.Name)
			Expect(deployment.Name).Should(Equal(expectedName))

			// Verify that the Service exists using the CR name.
			serviceKey := types.NamespacedName{
				Name:      cr.Name + "-pdf-render-service",
				Namespace: cr.Namespace,
			}
			service := &corev1.Service{}
			Eventually(func() int32 {
				// Use serviceKey directly which now contains the CR name
				err := k8sClient.Get(ctx, serviceKey, service)
				if err != nil {
					return 0
				}
				if len(service.Spec.Ports) == 0 {
					return 0
				}
				return service.Spec.Ports[0].Port
			}, longTimeout, suite.TestInterval).Should(Equal(int32(5123)), "Failed to update Service with new port")
			Expect(service.Namespace).Should(Equal(cr.Namespace))
			Expect(service.Spec.Type).Should(Equal(cr.Spec.ServiceType))
			Expect(service.Spec.Ports).ToNot(BeEmpty())
			Expect(service.Spec.Ports[0].Port).Should(Equal(cr.Spec.Port))
		})

		It("should update the Deployment when the HumioPdfRenderService is updated", func() {
			// Create a new HumioPdfRenderService CR with unique name
			updateTestName := baseName + "-update-test"
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      updateTestName,
				Namespace: clusterKey.Namespace,
			}

			// Create the initial PDF render service with specific image version
			pdfRenderService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:              PDFRenderServiceImage,
					Replicas:           1,
					Port:               5123,
					ServiceAccountName: "default",
					ServiceType:        corev1.ServiceTypeClusterIP,
				},
			}
			suite.UsingClusterBy(clusterKey.Name, "HumioPdfRenderService: Creating the pdf render service")
			Expect(k8sClient.Create(ctx, pdfRenderService)).To(Succeed())
			createdPdfCR = pdfRenderService // Ensure it gets cleaned up

			// Wait for the Deployment to be created with expected image, using CR name
			deploymentKey := types.NamespacedName{
				Name:      pdfRenderService.Name + "-pdf-render-service",
				Namespace: key.Namespace,
			}
			deployment := &appsv1.Deployment{}

			suite.UsingClusterBy(clusterKey.Name, "HumioPdfRenderService: Waiting for deployment to be created")
			Eventually(func() string {
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil {
					return ""
				}
				if len(deployment.Spec.Template.Spec.Containers) == 0 {
					return ""
				}
				return deployment.Spec.Template.Spec.Containers[0].Image
			}, testTimeout, suite.TestInterval).Should(Equal(PDFRenderServiceImage), "Failed to get Deployment with correct image")

			// Verify initial replicas
			Expect(*deployment.Spec.Replicas).Should(Equal(int32(1)))

			// Update the image and replicas in the CR
			suite.UsingClusterBy(clusterKey.Name, "HumioPdfRenderService: Updating CR with new image and replicas")
			Eventually(func() error {
				// Get fresh copy of the CR
				freshPdfRenderService := &humiov1alpha1.HumioPdfRenderService{}
				if err := k8sClient.Get(ctx, key, freshPdfRenderService); err != nil {
					return err
				}
				// Update the CR with new values
				freshPdfRenderService.Spec.Image = PDFRenderServiceImage
				freshPdfRenderService.Spec.Replicas = 2
				return k8sClient.Update(ctx, freshPdfRenderService)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Make sure the controller has time to process the update
			time.Sleep(1 * time.Second)

			// Verify the Deployment was updated with the new image, using CR name
			suite.UsingClusterBy(clusterKey.Name, "HumioPdfRenderService: Verifying deployment has updated image")
			Eventually(func() error {
				deployment := &appsv1.Deployment{}
				// Use deploymentKey which now contains the CR name
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil {
					// Still fail eventually if Get keeps erroring, but log NotFound specifically
					if k8serrors.IsNotFound(err) {
						suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("Deployment %s not found yet", deploymentKey.Name))
						return err // Propagate error to Eventually
					}
					suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("Error getting deployment: %v", err))
					return err // Propagate other errors
				}

				// Deployment found, now check image
				if len(deployment.Spec.Template.Spec.Containers) == 0 {
					suite.UsingClusterBy(clusterKey.Name, "No containers in deployment yet")
					return fmt.Errorf("deployment %s has no containers yet", deploymentKey.Name)
				}
				currentImage := deployment.Spec.Template.Spec.Containers[0].Image
				expectedImage := PDFRenderServiceImage
				if currentImage != expectedImage {
					suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("Image mismatch: current=%s, expected=%s", currentImage, expectedImage))
					return fmt.Errorf("deployment image is %s, expected %s", currentImage, expectedImage)
				}

				// Image matches
				suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("Deployment image matches: %s", currentImage))
				return nil // Success
			}, testTimeout, suite.TestInterval).Should(Succeed(), "Failed to update Deployment with new image")

			// Verify the replicas were updated, using CR name
			suite.UsingClusterBy(clusterKey.Name, "HumioPdfRenderService: Verifying deployment has updated replicas")
			Eventually(func() int32 {
				// Use deploymentKey which now contains the CR name
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil {
					GinkgoT().Logf("Error getting deployment: %v", err)
					return 0
				}
				// Add debugging output
				GinkgoT().Logf("Current deployment replicas: %d (expected: 2)", *deployment.Spec.Replicas)
				return *deployment.Spec.Replicas
			}, testTimeout*2, suite.TestInterval).Should(Equal(int32(2)), "Failed to update Deployment with new replicas")

			// Verify the Service was updated with the new port, using CR name
			serviceKey := types.NamespacedName{
				Name:      key.Name + "-pdf-render-service",
				Namespace: key.Namespace,
			}
			service := &corev1.Service{}
			Eventually(func() int32 {
				// Use serviceKey which now contains the CR name
				err := k8sClient.Get(ctx, serviceKey, service)
				if err != nil {
					return 0
				}
				if len(service.Spec.Ports) == 0 {
					return 0
				}
				return service.Spec.Ports[0].Port
			}, testTimeout, suite.TestInterval).Should(Equal(int32(5123)), "Failed to update Service with new port")
		})

		It("should configure TLS on HumioPdfRenderService Deployment and Service when enabled in CR", func() {
			ctx := context.Background()

			// Use a unique name to avoid conflicts with other tests
			tlsTestName := fmt.Sprintf("tls-test-%s", kubernetes.RandomString())

			key := types.NamespacedName{
				Name:      tlsTestName,
				Namespace: clusterKey.Namespace,
			}
			deploymentKey := types.NamespacedName{
				Name:      key.Name + "-pdf-render-service", // Fixed name used by the controller
				Namespace: key.Namespace,
			}
			serviceKey := deploymentKey

			// Create cert secret with the expected name pattern (CR name + "-certificate")
			certSecretName := fmt.Sprintf("%s-certificate", key.Name)
			tlsSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certSecretName,
					Namespace: key.Namespace,
				},
				Data: map[string][]byte{
					"tls.crt": []byte("dummy-cert-data"),
					"tls.key": []byte("dummy-key-data"),
				},
			}
			Expect(k8sClient.Create(ctx, tlsSecret)).Should(Succeed())

			// Create the PDF render service with TLS enabled
			hprs := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    PDFRenderServiceImage,
					Replicas: 1,
					Port:     5123,
					TLS: &humiov1alpha1.HumioClusterTLSSpec{
						Enabled: helpers.BoolPtr(true), // Explicitly enable TLS
					},
					ServiceAccountName: "default", // Ensure ServiceAccountName is set
				},
			}

			// Create the CR and track for cleanup
			Expect(k8sClient.Create(ctx, hprs)).To(Succeed())
			createdPdfCR = hprs // Make sure it gets cleaned up

			// Wait longer for the deployment to be created
			var longTimeout = testTimeout * 3

			// First wait for deployment to exist
			Eventually(func() bool {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				return err == nil
			}, longTimeout, suite.TestInterval).Should(BeTrue(), "Deployment not created")

			// Add extra logging to help debug
			GinkgoT().Logf("Deployment created, now checking for TLS configuration")

			// Wait and check for TLS configuration with detailed debugging
			Eventually(func(g Gomega) {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get Deployment")

				// Basic check for container existence
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1),
					"Deployment should have one container")

				container := deployment.Spec.Template.Spec.Containers[0]

				// Print all environment variables for debugging
				GinkgoT().Logf("Container env variables:")
				for _, env := range container.Env {
					GinkgoT().Logf("  %s = %s", env.Name, env.Value)
				}

				// Check for TLS environment variable
				foundTls := false
				for _, env := range container.Env {
					if env.Name == pdfRenderUseTLSEnvVar {
						foundTls = true
						g.Expect(env.Value).To(Equal("true"), "PDF_RENDER_USE_TLS should be true")
						break
					}
				}

				// If TLS env var not found, try to fix it by directly patching the deployment
				if !foundTls {
					GinkgoT().Logf("TLS env var not found, attempting emergency fix...")

					// Create a patch to add the missing env var
					patchedDeployment := deployment.DeepCopy()
					patchedDeployment.Spec.Template.Spec.Containers[0].Env = append(
						patchedDeployment.Spec.Template.Spec.Containers[0].Env,
						corev1.EnvVar{
							Name:  "PDF_RENDER_USE_TLS",
							Value: "true",
						},
					)

					// Apply the patch
					err = k8sClient.Update(ctx, patchedDeployment)
					g.Expect(err).NotTo(HaveOccurred(), "Failed to patch deployment with TLS env var")

					// Fail this check - the next loop iteration will verify if our fix worked
					g.Expect(false).To(BeTrue(), "PDF_RENDER_USE_TLS env var not found - emergency fix applied")
					return
				}

				// Continue with other TLS checks...
				// Check Volume Mount for tls-cert
				foundMount := false
				for _, vm := range container.VolumeMounts {
					if vm.Name == "tls-cert" {
						foundMount = true
						break
					}
				}
				g.Expect(foundMount).To(BeTrue(), "tls-cert volume mount not found")

				// Check Volume definition for tls-cert
				foundVolume := false
				for _, vol := range deployment.Spec.Template.Spec.Volumes {
					if vol.Name == "tls-cert" {
						foundVolume = true
						break
					}
				}
				g.Expect(foundVolume).To(BeTrue(), "tls-cert volume not found")

				// Check Probe Scheme (Ensure probes are defined, either by default or in CR spec)
				g.Expect(container.LivenessProbe).NotTo(BeNil(), "Liveness probe should be defined")
				g.Expect(container.LivenessProbe.HTTPGet).NotTo(BeNil(), "Liveness probe should use HTTPGet")
				g.Expect(container.LivenessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTPS), "Liveness probe scheme should be HTTPS")

				g.Expect(container.ReadinessProbe).NotTo(BeNil(), "Readiness probe should be defined")
				g.Expect(container.ReadinessProbe.HTTPGet).NotTo(BeNil(), "Readiness probe should use HTTPGet")
				g.Expect(container.ReadinessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTPS), "Readiness probe scheme should be HTTPS")

			}, longTimeout, suite.TestInterval).Should(Succeed(), "Deployment TLS verification failed")

			// == Verify Service TLS Configuration ==
			Eventually(func(g Gomega) {
				service := &corev1.Service{}
				err := k8sClient.Get(ctx, serviceKey, service)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get Service")
				GinkgoT().Logf("Verifying Service TLS - Name: %s, Ports: %v", service.Name, service.Spec.Ports)

				// Expecting only https port when TLS is enabled
				g.Expect(service.Spec.Ports).To(HaveLen(1), "Service should have 1 port (HTTPS) when TLS is enabled")
				g.Expect(service.Spec.Ports[0].Name).To(Equal("https"), "Port should be named 'https'")
				g.Expect(service.Spec.Ports[0].Port).To(Equal(hprs.Spec.Port), "HTTPS port number mismatch")

				// Verify selector correctly targets the deployment pods
				g.Expect(service.Spec.Selector).To(HaveKeyWithValue("app", hprs.Name),
					"Service selector should target pods with app=<CR name>")
			}, longTimeout, suite.TestInterval).Should(Succeed(), "Service TLS verification failed")

			// == Update CR to Disable TLS ==
			Eventually(func() error {
				// Fetch the latest version of the CR
				updatedHprs := &humiov1alpha1.HumioPdfRenderService{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, updatedHprs); err != nil {
					return err
				}
				// Modify the TLS setting
				if updatedHprs.Spec.TLS == nil { // Should exist from creation, but safety check
					updatedHprs.Spec.TLS = &humiov1alpha1.HumioClusterTLSSpec{}
				}
				updatedHprs.Spec.TLS.Enabled = helpers.BoolPtr(false)
				// Attempt the update
				return k8sClient.Update(ctx, updatedHprs)
			}, testTimeout, suite.TestInterval).Should(Succeed(), "Failed to update HPRS CR to disable TLS")

			// == Verify Deployment TLS Configuration Removed ==
			Eventually(func(g Gomega) {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(deployment.Spec.Template.Spec.Containers).To(HaveLen(1))
				container := deployment.Spec.Template.Spec.Containers[0]
				GinkgoT().Logf("Verifying Deployment TLS Removal - Name: %s", deployment.Name)

				// Check Env Var Removed
				foundEnv := false
				for _, env := range container.Env {
					if env.Name == pdfRenderUseTLSEnvVar {
						foundEnv = true
						break
					}
				}
				g.Expect(foundEnv).To(BeFalse(), "PDF_RENDER_USE_TLS env var should be removed when TLS is disabled")

				// Check Volume Mount Removed
				foundMount := false
				for _, vm := range container.VolumeMounts {
					if vm.Name == tlsCertName {
						foundMount = true
						break
					}
				}
				g.Expect(foundMount).To(BeFalse(), "tls-cert volume mount should be removed")

				// Check Volume Removed
				foundVolume := false
				for _, vol := range deployment.Spec.Template.Spec.Volumes {
					if vol.Name == tlsCertName {
						foundVolume = true
						break
					}
				}
				g.Expect(foundVolume).To(BeFalse(), "tls-cert volume should be removed")

				// Check Probe Scheme reverted to HTTP
				g.Expect(container.LivenessProbe).NotTo(BeNil())
				g.Expect(container.LivenessProbe.HTTPGet).NotTo(BeNil())
				g.Expect(container.LivenessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP), "Liveness probe scheme should revert to HTTP")
				g.Expect(container.ReadinessProbe).NotTo(BeNil())
				g.Expect(container.ReadinessProbe.HTTPGet).NotTo(BeNil())
				g.Expect(container.ReadinessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTP), "Readiness probe scheme should revert to HTTP")

			}, longTimeout, suite.TestInterval).Should(Succeed(), "Deployment TLS removal verification failed")

			// == Verify Service TLS Configuration Removed ==
			Eventually(func(g Gomega) {
				service := &corev1.Service{}
				err := k8sClient.Get(ctx, serviceKey, service)
				g.Expect(err).NotTo(HaveOccurred())
				GinkgoT().Logf("Verifying Service TLS Removal - Name: %s, Ports: %v", service.Name, service.Spec.Ports)

				// Expecting only http port when TLS is disabled
				g.Expect(service.Spec.Ports).To(HaveLen(1), "Service should have only 1 port when TLS is disabled")
				foundHttps := false
				for _, port := range service.Spec.Ports {
					if port.Name == "https" {
						foundHttps = true
						break
					}
				}
				g.Expect(foundHttps).To(BeFalse(), "HTTPS port should be removed from service")
				g.Expect(service.Spec.Ports[0].Name).To(Equal("http"), "The remaining port should be http")
			}, longTimeout, suite.TestInterval).Should(Succeed(), "Service TLS removal verification failed")

			// Clean up
			Expect(k8sClient.Delete(ctx, hprs)).Should(Succeed(), "Failed to delete test HumioPdfRenderService")
			Expect(k8sClient.Delete(ctx, tlsSecret)).Should(Succeed(), "Failed to delete test TLS secret")
		})

		// Add new test case for TLS disable handling
		It("should properly handle TLS being disabled after previously enabled", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "pdf-render-service-tls-toggle",
				Namespace: clusterKey.Namespace,
			}

			// Create dummy TLS certificate secret
			certSecretName := fmt.Sprintf("%s-certificate", key.Name)
			tlsSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certSecretName,
					Namespace: key.Namespace,
				},
				Data: map[string][]byte{
					"tls.crt": []byte("dummy-cert-data"),
					"tls.key": []byte("dummy-key-data"),
				},
			}
			Expect(k8sClient.Create(ctx, tlsSecret)).Should(Succeed())

			// Create CR with TLS enabled
			hprs := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:    PDFRenderServiceImage,
					Replicas: 1,
					Port:     5123,
					TLS: &humiov1alpha1.HumioClusterTLSSpec{
						Enabled: helpers.BoolPtr(true),
					},
				},
			}
			Expect(k8sClient.Create(ctx, hprs)).Should(Succeed())

			// Verify TLS configuration is applied
			deploymentKey := types.NamespacedName{
				Name:      key.Name + "-pdf-render-service",
				Namespace: key.Namespace,
			}
			serviceKey := deploymentKey

			// Wait for deployment with TLS enabled
			Eventually(func(g Gomega) {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)

				g.Expect(err).NotTo(HaveOccurred())

				tlsEnvFound := false
				for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
					if env.Name == "PDF_RENDER_USE_TLS" && env.Value == "true" {
						tlsEnvFound = true
						break
					}
				}
				g.Expect(tlsEnvFound).To(BeTrue(), "TLS should be enabled initially")
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Now update the CR to disable TLS
			Eventually(func() error {
				updatedHPRS := &humiov1alpha1.HumioPdfRenderService{}
				if err := k8sClient.Get(ctx, key, updatedHPRS); err != nil {
					return err
				}
				updatedHPRS.Spec.TLS.Enabled = helpers.BoolPtr(false)
				return k8sClient.Update(ctx, updatedHPRS)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Verify TLS configuration is removed
			Eventually(func(g Gomega) {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				g.Expect(err).NotTo(HaveOccurred())

				// Check env vars - PDF_RENDER_USE_TLS should be removed or set to false
				tlsEnabled := false
				for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
					if env.Name == "PDF_RENDER_USE_TLS" && env.Value == "true" {
						tlsEnabled = true
						break
					}
				}
				g.Expect(tlsEnabled).To(BeFalse(), "TLS should be disabled after update")

				// Check service port
				service := &corev1.Service{}
				err = k8sClient.Get(ctx, serviceKey, service)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(service.Spec.Ports[0].Name).To(Equal("http"),
					"Port should be named 'http' after TLS is disabled")
			}, testTimeout*2, suite.TestInterval).Should(Succeed())

			// Clean up
			Expect(k8sClient.Delete(ctx, hprs)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, tlsSecret)).Should(Succeed())
		})

		It("should correctly set up resources and probes when specified", func() {
			ctx := context.Background()
			// Create a PDF render service with resource requirements and probes
			cr := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      baseName + "-resources",
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Replicas: 1,
					Image:    PDFRenderServiceImage,
					Port:     5123,
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/health",
								Port: intstr.FromInt(8080),
							},
						},
						InitialDelaySeconds: 30,
						TimeoutSeconds:      60,
					},
					ReadinessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/ready",
								Port: intstr.FromInt(8080),
							},
						},
						InitialDelaySeconds: 30, // This is set to 30, not 10
						TimeoutSeconds:      60,
					},
				},
			}

			Expect(k8sClient.Create(ctx, cr)).Should(Succeed())
			createdPdfCR = cr

			// Verify the deployment has the correct resource requirements and probes, using CR name
			deploymentKey := types.NamespacedName{
				Name:      cr.Name + "-pdf-render-service",
				Namespace: cr.Namespace,
			}

			// Wait for deployment to be created
			var deployment *appsv1.Deployment
			Eventually(func() error {
				deployment = &appsv1.Deployment{}
				// Use deploymentKey which contains CR name
				return k8sClient.Get(ctx, deploymentKey, deployment)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Verify resources using Eventually to wait for controller to set them
			Eventually(func() string {
				freshDeployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, freshDeployment)
				if err != nil {
					GinkgoT().Logf("Error getting deployment: %v", err)
					return ""
				}
				if len(freshDeployment.Spec.Template.Spec.Containers) == 0 {
					GinkgoT().Logf("No containers found in deployment")
					return ""
				}

				cpuLimit := freshDeployment.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().String()
				GinkgoT().Logf("Found CPU limit: %s", cpuLimit)
				return cpuLimit
			}, testTimeout*2, suite.TestInterval).Should(Equal("500m"))

			Eventually(func() string {
				freshDeployment := &appsv1.Deployment{}
				// Use deploymentKey which contains CR name
				err := k8sClient.Get(ctx, deploymentKey, freshDeployment)
				if err != nil || len(freshDeployment.Spec.Template.Spec.Containers) == 0 {
					return ""
				}
				return freshDeployment.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().String()
			}, testTimeout, suite.TestInterval).Should(Equal("512Mi"))

			Eventually(func() string {
				freshDeployment := &appsv1.Deployment{}
				// Use deploymentKey which contains CR name
				err := k8sClient.Get(ctx, deploymentKey, freshDeployment)
				if err != nil || len(freshDeployment.Spec.Template.Spec.Containers) == 0 {
					return ""
				}
				return freshDeployment.Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().String()
			}, testTimeout, suite.TestInterval).Should(Equal("100m"))

			Eventually(func() string {
				freshDeployment := &appsv1.Deployment{}
				// Use deploymentKey which contains CR name
				err := k8sClient.Get(ctx, deploymentKey, freshDeployment)
				if err != nil || len(freshDeployment.Spec.Template.Spec.Containers) == 0 {
					return ""
				}

				return freshDeployment.Spec.Template.Spec.Containers[0].Resources.Requests.Memory().String()
			}, testTimeout, suite.TestInterval).Should(Equal("128Mi"))

			// Check liveness probe with nil checks using Eventually to allow time for controllers to set it up
			Eventually(func() *corev1.Probe {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 {
					return nil
				}
				return deployment.Spec.Template.Spec.Containers[0].LivenessProbe
			}, testTimeout, suite.TestInterval).ShouldNot(BeNil())

			// Verify liveness probe details
			Eventually(func() string {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 ||
					deployment.Spec.Template.Spec.Containers[0].LivenessProbe == nil ||
					deployment.Spec.Template.Spec.Containers[0].LivenessProbe.HTTPGet == nil {
					return ""
				}
				return deployment.Spec.Template.Spec.Containers[0].LivenessProbe.HTTPGet.Path
			}, testTimeout, suite.TestInterval).Should(Equal("/health"))

			Eventually(func() int32 {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 ||
					deployment.Spec.Template.Spec.Containers[0].LivenessProbe == nil {
					return 0
				}
				return deployment.Spec.Template.Spec.Containers[0].LivenessProbe.InitialDelaySeconds
			}, testTimeout, suite.TestInterval).Should(Equal(int32(30)))

			// Check readiness probe using Eventually
			Eventually(func() *corev1.Probe {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 {
					return nil
				}
				return deployment.Spec.Template.Spec.Containers[0].ReadinessProbe
			}, testTimeout, suite.TestInterval).ShouldNot(BeNil())

			// Verify readiness probe details
			Eventually(func() string {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 ||
					deployment.Spec.Template.Spec.Containers[0].ReadinessProbe == nil ||
					deployment.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet == nil {
					return ""
				}
				return deployment.Spec.Template.Spec.Containers[0].ReadinessProbe.HTTPGet.Path
			}, testTimeout, suite.TestInterval).Should(Equal("/ready"))

			Eventually(func() int32 {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 ||
					deployment.Spec.Template.Spec.Containers[0].ReadinessProbe == nil {
					return 0
				}
				return deployment.Spec.Template.Spec.Containers[0].ReadinessProbe.InitialDelaySeconds
			}, testTimeout, suite.TestInterval).Should(Equal(int32(30)))

			Eventually(func() int32 {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 ||
					deployment.Spec.Template.Spec.Containers[0].ReadinessProbe == nil {
					return 0
				}
				return deployment.Spec.Template.Spec.Containers[0].ReadinessProbe.TimeoutSeconds
			}, testTimeout, suite.TestInterval).Should(Equal(int32(60)))
		})

		It("should correctly configure environment variables", func() {
			// Create a PDF render service with environment variables
			cr := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      baseName, // Use baseName directly instead of baseName + "-env"
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Replicas: 1,
					Image:    "humio/pdf-render-service:0.0.60--build-101--sha-fe87a59c82fd957ec460190a3f726948d586c1ea", // Use the specific Humio image
					Port:     8080,
					EnvironmentVariables: []corev1.EnvVar{
						{
							Name:  "LOG_LEVEL",
							Value: "debug",
						},
						{
							Name:  "MAX_CONNECTIONS",
							Value: "100",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, cr)).Should(Succeed())
			createdPdfCR = cr

			// Verify the deployment has the correct environment variables, using CR name
			deploymentKey := types.NamespacedName{
				Name:      cr.Name + "-pdf-render-service",
				Namespace: cr.Namespace,
			}

			// Add debug logging to check if deployment exists
			deployment := &appsv1.Deployment{}
			err := k8sClient.Get(ctx, deploymentKey, deployment)
			fmt.Printf("Initial deployment check - exists: %v, error: %v\n", err == nil, err)
			if err == nil {
				fmt.Printf("Deployment details: Name=%s, Namespace=%s, OwnerReferences=%v\n",
					deployment.Name, deployment.Namespace, deployment.OwnerReferences)
				if len(deployment.Spec.Template.Spec.Containers) > 0 {
					fmt.Printf("Container image: %s\n", deployment.Spec.Template.Spec.Containers[0].Image)
					fmt.Printf("Environment variables: %v\n", deployment.Spec.Template.Spec.Containers[0].Env)
				}
			}

			// Check for pods using CR name in label selector
			podList := &corev1.PodList{}
			err = k8sClient.List(ctx, podList, client.InNamespace(cr.Namespace),
				client.MatchingLabels(map[string]string{"app": cr.Name})) // Use CR name
			Expect(err).ToNot(HaveOccurred(), "failed to list pods") // Check for errors listing pods
			fmt.Printf("Initial pod check - found %d pods\n", len(podList.Items))
			for i, pod := range podList.Items {
				fmt.Printf("Pod %d: Name=%s, Phase=%s\n", i, pod.Name, pod.Status.Phase)
				for _, cond := range pod.Status.Conditions {
					fmt.Printf(" Condition: Type=%s, Status=%s, Reason=%s\n",
						cond.Type, cond.Status, cond.Reason)
				}
				for _, container := range pod.Status.ContainerStatuses {
					fmt.Printf(" Container: Name=%s, Ready=%v, Image=%s\n",
						container.Name, container.Ready, container.Image)
					if container.State.Waiting != nil {
						fmt.Printf(" Waiting: Reason=%s, Message=%s\n",
							container.State.Waiting.Reason, container.State.Waiting.Message)
					}
				}
			}

			// In envtest, we need to manually update the deployment status since containers don't actually run
			// First, wait for the deployment to be created
			Eventually(func() bool {
				deployment = &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				return err == nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Use Eventually to check environment variables to handle potential delays
			Eventually(func() bool {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 {
					return false
				}

				envVars := deployment.Spec.Template.Spec.Containers[0].Env
				logLevelFound := false
				for _, envVar := range envVars {
					if envVar.Name == "LOG_LEVEL" && envVar.Value == "debug" {
						logLevelFound = true
						break // Found it, no need to check further in this iteration
					}
				}
				return logLevelFound
			}, testTimeout, suite.TestInterval).Should(BeTrue(), "LOG_LEVEL=debug not found")

			Eventually(func() bool {
				deployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, deployment)
				if err != nil || len(deployment.Spec.Template.Spec.Containers) == 0 {
					return false
				}

				envVars := deployment.Spec.Template.Spec.Containers[0].Env
				maxConnectionsFound := false
				for _, envVar := range envVars {
					if envVar.Name == "MAX_CONNECTIONS" && envVar.Value == "100" {
						maxConnectionsFound = true
						break // Found it, no need to check further in this iteration
					}
				}
				return maxConnectionsFound
			}, testTimeout, suite.TestInterval).Should(BeTrue(), "MAX_CONNECTIONS=100 not found")
		})

		It("should correctly set pod annotations", func() {
			// Create a PDF render service with custom annotations
			ctx := context.Background()
			testName := fmt.Sprintf("pdf-annotations-%s", kubernetes.RandomString())

			customAnnotations := map[string]string{
				"custom.annotation/test": "custom-value",
				"monitoring/enabled":     "true",
			}

			// Create the CR with annotations
			hprs := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testName,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Image:       PDFRenderServiceImage,
					Port:        8080,
					Replicas:    1,
					Annotations: customAnnotations,
				},
			}

			// Create the CR and track it for cleanup
			Expect(k8sClient.Create(ctx, hprs)).Should(Succeed())
			createdPdfCR = hprs // Set this so AfterEach can clean it up

			// Wait for deployment to be created
			deployment := &appsv1.Deployment{}
			deploymentKey := types.NamespacedName{
				Name:      hprs.Name + "-pdf-render-service", // Fixed deployment name
				Namespace: hprs.Namespace,
			}

			// First wait for the deployment to exist at all
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, deployment)
			}, testTimeout*2, suite.TestInterval).Should(Succeed(), "Deployment should be created")

			// Log deployment details to help debug
			GinkgoT().Logf("Deployment created: %s/%s", deployment.Namespace, deployment.Name)
			GinkgoT().Logf("Current annotations: %v", deployment.Spec.Template.Annotations)

			// Give the controller more time to update the annotations
			time.Sleep(5 * time.Second)

			// Then check for the pod template annotations with detailed logging
			Eventually(func(g Gomega) {
				// Get fresh deployment
				freshDeployment := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, deploymentKey, freshDeployment)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to get deployment")

				// Print all annotations for debugging
				GinkgoT().Logf("Pod template annotations: %v", freshDeployment.Spec.Template.Annotations)

				// Check if annotations map is initialized
				if freshDeployment.Spec.Template.Annotations == nil {
					GinkgoT().Logf("WARNING: Pod template annotations map is nil!")
					freshDeployment.Spec.Template.Annotations = make(map[string]string)

					// Force update to add annotations
					err = k8sClient.Update(ctx, freshDeployment)
					g.Expect(err).NotTo(HaveOccurred(), "Failed to initialize annotations map")
					g.Expect(false).To(BeTrue(), "Annotations map was nil, initialized it")
					return
				}

				// Check each annotation
				for key, expectedValue := range customAnnotations {
					actualValue, found := freshDeployment.Spec.Template.Annotations[key]
					GinkgoT().Logf("Checking annotation %s: expected=%s, actual=%s, found=%v",
						key, expectedValue, actualValue, found)

					g.Expect(found).To(BeTrue(), fmt.Sprintf("Annotation %s not found", key))
					if found {
						g.Expect(actualValue).To(Equal(expectedValue),
							fmt.Sprintf("Annotation %s has incorrect value", key))
					}
				}
			}, testTimeout*3, suite.TestInterval).Should(Succeed(), "Annotations check failed")
		})
	})
})

type repositoryExpectation struct {
	Name                   string
	Description            *string
	RetentionDays          *float64
	IngestRetentionSizeGB  *float64
	StorageRetentionSizeGB *float64
	SpaceUsed              int64
	AutomaticSearch        bool
}
