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
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller/suite"
)

// Constants for PDF render service TLS configuration
//
//nolint:unused
const (
	emailActionExample         string = "example@example.com"
	expectedSecretValueExample string = "secret-token"
	PDFRenderServiceImage      string = "humio/pdf-render-service:0.0.60--build-102--sha-c8eb95329236ba5fc65659b83af1d84b4703cb1e"
	protocolHTTPS              string = "https"
	protocolHTTP               string = "http"
	tlsCertName                string = "tls-cert"                 // Unrelated to PDF service TLS, likely for HumioCluster itself
	hprsFinalizer              string = "core.humio.com/finalizer" // Match controller constant
	updatedParserScript        string = "kvParse() | updated"
)

var _ = Describe("Humio Resources Controllers", func() {

	BeforeEach(func() {
		// failed test runs that don't clean up leave resources behind.
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
		humioClient.ClearHumioClientConnections(testRepoName)
	})

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
			updatedScript := updatedParserScript
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
			protocol := protocolHTTP
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

			if protocol == protocolHTTPS {
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
			updatedScript := updatedParserScript
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
			protocol := protocolHTTP
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

			if protocol == protocolHTTPS {
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
			updatedScript := updatedParserScript
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
			protocol := protocolHTTP
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

			if protocol == protocolHTTPS {
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
			updatedScript := updatedParserScript
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
			protocol := protocolHTTP
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

			if protocol == protocolHTTPS {
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
			updatedScript := updatedParserScript
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
			protocol := protocolHTTP
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

			if protocol == protocolHTTPS {
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

	Context("Humio External Cluster", Label("envtest", "dummy", "real"), func() {
		It("should handle resources correctly", func() {
			ctx := context.Background()
			suite.UsingClusterBy(clusterKey.Name, "HumioExternalCluster: Should handle externalcluster correctly")
			key := types.NamespacedName{
				Name:      "humioexternalcluster",
				Namespace: clusterKey.Namespace,
			}
			protocol := protocolHTTP
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

			if protocol == protocolHTTPS {
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
			updatedScript := updatedParserScript
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
			protocol := protocolHTTP
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

			if protocol == protocolHTTPS {
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
			updatedScript := updatedParserScript
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
			protocol := protocolHTTP
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

			if protocol == protocolHTTPS {
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

	Context("Humio Feature Flag", Label("envtest", "dummy", "real"), func() {
		It("HumioFeatureFlag: Should enable and disable feature successfully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "array-functions",
				Namespace: clusterKey.Namespace,
			}

			toSetFeatureFlag := &humiov1alpha1.HumioFeatureFlag{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioFeatureFlagSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "ArrayFunctions",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioFeatureFlag: Enabling feature flag")
			Expect(k8sClient.Create(ctx, toSetFeatureFlag)).Should(Succeed())

			fetchedFeatureFlag := &humiov1alpha1.HumioFeatureFlag{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedFeatureFlag)
				return fetchedFeatureFlag.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioFeatureFlagStateExists))

			var isFeatureFlagEnabled bool
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				isFeatureFlagEnabled, err = humioClient.IsFeatureFlagEnabled(ctx, humioHttpClient, toSetFeatureFlag)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(isFeatureFlagEnabled).To(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioFeatureFlag: Disabling feature flag")
			Expect(k8sClient.Delete(ctx, fetchedFeatureFlag)).To(Succeed())
			Eventually(func() bool {
				isFeatureFlagEnabled, err = humioClient.IsFeatureFlagEnabled(ctx, humioHttpClient, toSetFeatureFlag)
				objErr := k8sClient.Get(ctx, key, fetchedFeatureFlag)

				return k8serrors.IsNotFound(objErr) && !isFeatureFlagEnabled
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("HumioFeatureFlag: Should deny improperly configured feature flag with missing required values", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "example-invalid-feature-flag",
				Namespace: clusterKey.Namespace,
			}
			toCreateInvalidFeatureFlag := &humiov1alpha1.HumioFeatureFlag{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioFeatureFlagSpec{
					ManagedClusterName: clusterKey.Name,
					//Name: key.Name,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioFeatureFlag: Trying to create an invalid feature flag")
			Expect(k8sClient.Create(ctx, toCreateInvalidFeatureFlag)).Should(Not(Succeed()))
		})

		It("HumioFeatureFlag: Should deny feature flag which is not available in LogScale", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "example-invalid-feature-flag",
				Namespace: clusterKey.Namespace,
			}
			toCreateInvalidFeatureFlag := &humiov1alpha1.HumioFeatureFlag{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioFeatureFlagSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               key.Name,
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioFeatureFlag: Trying to create a feature flag with an invalid name")
			Expect(k8sClient.Create(ctx, toCreateInvalidFeatureFlag)).Should(Succeed())
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, toCreateInvalidFeatureFlag)
				return toCreateInvalidFeatureFlag.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioFeatureFlagStateConfigError))
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

			suite.UsingClusterBy(clusterKey.Name, "HumioScheduledSearch: Succeswith customsfully deleting the action")
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

	Context("Humio User", Label("envtest", "dummy", "real"), func() {
		It("HumioUser: Should handle user correctly", func() {
			ctx := context.Background()
			spec := humiov1alpha1.HumioUserSpec{
				ManagedClusterName: clusterKey.Name,
				UserName:           "example-user",
				IsRoot:             nil,
			}

			key := types.NamespacedName{
				Name:      "humiouser",
				Namespace: clusterKey.Namespace,
			}

			toCreateUser := &humiov1alpha1.HumioUser{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: spec,
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioUser: Creating the user successfully with isRoot=nil")
			Expect(k8sClient.Create(ctx, toCreateUser)).Should(Succeed())

			fetchedUser := &humiov1alpha1.HumioUser{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedUser)
				return fetchedUser.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioUserStateExists))

			var initialUser *humiographql.UserDetails
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			Eventually(func() error {
				initialUser, err = humioClient.GetUser(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, toCreateUser)
				if err != nil {
					return err
				}

				// Ignore the ID when comparing content
				initialUser.Id = ""

				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(initialUser).ToNot(BeNil())

			expectedInitialUser := &humiographql.UserDetails{
				Id:       "",
				Username: toCreateUser.Spec.UserName,
				IsRoot:   false,
			}
			Expect(*initialUser).To(Equal(*expectedInitialUser))

			suite.UsingClusterBy(clusterKey.Name, "HumioUser: Updating the user successfully to set isRoot=true")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedUser); err != nil {
					return err
				}
				fetchedUser.Spec.IsRoot = helpers.BoolPtr(true)
				return k8sClient.Update(ctx, fetchedUser)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			expectedUpdatedUser := &humiographql.UserDetails{
				Id:       "",
				Username: toCreateUser.Spec.UserName,
				IsRoot:   true,
			}
			Eventually(func() *humiographql.UserDetails {
				updatedUser, err := humioClient.GetUser(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedUser)
				if err != nil {
					return nil
				}

				// Ignore the ID when comparing content
				updatedUser.Id = ""

				return updatedUser
			}, testTimeout, suite.TestInterval).Should(Equal(expectedUpdatedUser))

			suite.UsingClusterBy(clusterKey.Name, "HumioUser: Updating the user successfully to set isRoot=false")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedUser); err != nil {
					return err
				}
				fetchedUser.Spec.IsRoot = helpers.BoolPtr(false)
				return k8sClient.Update(ctx, fetchedUser)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			expectedUpdatedUser = &humiographql.UserDetails{
				Id:       "",
				Username: toCreateUser.Spec.UserName,
				IsRoot:   false,
			}
			Eventually(func() *humiographql.UserDetails {
				updatedUser, err := humioClient.GetUser(ctx, humioHttpClient, reconcile.Request{NamespacedName: clusterKey}, fetchedUser)
				if err != nil {
					return nil
				}

				// Ignore the ID when comparing content
				updatedUser.Id = ""

				return updatedUser
			}, testTimeout, suite.TestInterval).Should(Equal(expectedUpdatedUser))

			suite.UsingClusterBy(clusterKey.Name, "HumioUser: Successfully deleting it")
			Expect(k8sClient.Delete(ctx, fetchedUser)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedUser)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Required Spec Validation", Label("envtest", "dummy", "real"), func() {
		It("should reject with missing spec", func() {
			// Verify the scheme was initialized before we continue
			Expect(testScheme).ToNot(BeNil())

			// Dynamically fetch all Humio CRD types from the scheme
			var resources []runtime.Object

			// Get all types registered in the scheme
			for gvk := range testScheme.AllKnownTypes() {
				// Filter for types in the humiov1alpha1 group/version that start with "Humio"
				if gvk.Group == humiov1alpha1.GroupVersion.Group &&
					gvk.Version == humiov1alpha1.GroupVersion.Version &&
					strings.HasPrefix(gvk.Kind, "Humio") {

					// Skip any list types
					if strings.HasSuffix(gvk.Kind, "List") {
						continue
					}

					// Create a new instance of this type
					obj, err := testScheme.New(gvk)
					if err == nil {
						resources = append(resources, obj)
					}
				}
			}

			// Verify we validate this for all our CRD's
			Expect(resources).To(HaveLen(15)) // Bump this as we introduce new CRD's

			for i := range resources {
				// Get the GVK information
				obj := resources[i].DeepCopyObject()

				// Get the type information
				objType := reflect.TypeOf(obj).Elem()
				kind := objType.Name()

				// Fetch API group and version
				apiGroup := humiov1alpha1.GroupVersion.Group
				apiVersion := humiov1alpha1.GroupVersion.Version

				// Create a raw JSON representation without spec
				rawObj := fmt.Sprintf(`{
            "apiVersion": "%s/%s",
            "kind": "%s",
            "metadata": {
                "name": "%s-sample",
                "namespace": "default"
            }
        }`, apiGroup, apiVersion, kind, strings.ToLower(kind))

				// Convert to unstructured
				unstructuredObj := &unstructured.Unstructured{}
				err := json.Unmarshal([]byte(rawObj), unstructuredObj)
				Expect(err).NotTo(HaveOccurred())

				// Verify the GVK is set correctly
				gvk := unstructuredObj.GetObjectKind().GroupVersionKind()
				Expect(gvk.Kind).To(Equal(kind))
				Expect(gvk.Group).To(Equal(apiGroup))
				Expect(gvk.Version).To(Equal(apiVersion))

				// Attempt to create the resource with no spec field
				err = k8sClient.Create(context.Background(), unstructuredObj)

				// Expect an error because spec is required
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("spec: Required value"))

			}
		})
	})

	// Common constants and setup for PDF Render Service tests
	const (
		shortTimeout  = time.Second * 10
		mediumTimeout = time.Second * 30
		longTimeout   = time.Second * 60
	)

	// Test Case 1: PDF Render Service Ownership
	// --------------------------------------------------------------------
	Context("PDF Render Service Owner References", Label("envtest", "dummy", "real"), func() {

		var (
			ctx           = context.Background()
			mediumTimeout = time.Second * 30
			longTimeout   = time.Second * 60
		)
		It("should set owner references correctly on child Deployment and Service", func() {

			hprsKey := types.NamespacedName{
				Name:      "humio-pdf-render-service",
				Namespace: clusterKey.Namespace,
			}
			depKey := types.NamespacedName{
				Name:      hprsKey.Name + "-pdf-render-service",
				Namespace: hprsKey.Namespace,
			}

			suite.UsingClusterBy(clusterKey.Name, "Creating HumioPdfRenderService CR")
			hprs := suite.CreatePdfRenderServiceCR(ctx, k8sClient, hprsKey, false)

			suite.UsingClusterBy(clusterKey.Name, "Waiting for observedGeneration to catch up")
			suite.WaitForObservedGeneration(ctx, k8sClient, hprs, longTimeout, suite.TestInterval)

			suite.UsingClusterBy(clusterKey.Name, "Ensuring PDF Render Deployment is ready")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, depKey)

			suite.UsingClusterBy(clusterKey.Name, "Verifying Deployment has correct owner reference")
			Eventually(func() []metav1.OwnerReference {
				dep := &appsv1.Deployment{}
				err := k8sClient.Get(ctx, depKey, dep)
				if err != nil {
					return nil
				}
				return dep.OwnerReferences
			}, mediumTimeout, suite.TestInterval).Should(ContainElement(HaveField("UID", hprs.UID)))

			suite.UsingClusterBy(clusterKey.Name, "Verifying Service has correct owner reference")
			Eventually(func() []metav1.OwnerReference {
				svc := &corev1.Service{}
				err := k8sClient.Get(ctx, depKey, svc)
				if err != nil {
					return nil
				}
				return svc.OwnerReferences
			}, mediumTimeout, suite.TestInterval).Should(ContainElement(HaveField("UID", hprs.UID)))

			suite.UsingClusterBy(clusterKey.Name, "Cleaning up HumioPdfRenderService CR")
			Expect(k8sClient.Delete(ctx, hprs)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hprsKey, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	// Test Case 2: PDF Render Service Creation
	Context("PDF Render Service Creation", Label("envtest", "dummy", "real"), func() {
		var (
			ctx = context.Background()
		)

		It("should create Deployment and Service when a new HumioPdfRenderService is created", func() {
			// Use the same namespace for all test cases
			pdfKey := types.NamespacedName{
				Name:      "humio-pdf-render-service-creation",
				Namespace: clusterKey.Namespace,
			}

			deploymentKey := types.NamespacedName{
				Name:      pdfKey.Name + "-pdf-render-service",
				Namespace: pdfKey.Namespace,
			}

			serviceKey := types.NamespacedName{
				Name:      pdfKey.Name + "-pdf-render-service",
				Namespace: pdfKey.Namespace,
			}

			suite.UsingClusterBy(clusterKey.Name, "Creating HumioPdfRenderService CR")
			hprs := suite.CreatePdfRenderServiceCR(ctx, k8sClient, pdfKey, false)
			Expect(hprs).NotTo(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "Waiting for observedGeneration to catch up")
			suite.WaitForObservedGeneration(ctx, k8sClient, hprs, testTimeout*2, suite.TestInterval)

			suite.UsingClusterBy(clusterKey.Name, "Ensuring PDF Render Deployment is ready")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			suite.UsingClusterBy(clusterKey.Name, "Verifying Deployment exists with correct properties")
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, deployment)
			}, testTimeout*2, suite.TestInterval).Should(Succeed())
			Expect(deployment.Namespace).Should(Equal(pdfKey.Namespace))
			expectedName := fmt.Sprintf("%s-pdf-render-service", pdfKey.Name)
			Expect(deployment.Name).Should(Equal(expectedName))

			suite.UsingClusterBy(clusterKey.Name, "Verifying Service exists with correct properties")
			service := &corev1.Service{}
			Eventually(func() int32 {
				err := k8sClient.Get(ctx, serviceKey, service)
				if err != nil {
					return 0
				}
				if len(service.Spec.Ports) == 0 {
					return 0
				}
				return service.Spec.Ports[0].Port
			}, testTimeout*2, suite.TestInterval).Should(Equal(int32(controller.DefaultPdfRenderServicePort)), "Failed to update Service with new port")
			Expect(service.Namespace).Should(Equal(pdfKey.Namespace))
			Expect(service.Spec.Type).Should(Equal(corev1.ServiceTypeClusterIP))
			Expect(service.Spec.Ports).ToNot(BeEmpty())
			Expect(service.Spec.Ports[0].Port).Should(Equal(int32(controller.DefaultPdfRenderServicePort)))

			suite.UsingClusterBy(clusterKey.Name, "Cleaning up HumioPdfRenderService CR")
			Expect(k8sClient.Delete(ctx, hprs)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pdfKey, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	// Test Case 3: PDF Render Service Update
	Context("PDF Render Service Update", Label("envtest", "dummy", "real"), func() {
		var (
			ctx = context.Background()
		)

		It("should update the Deployment when the HumioPdfRenderService is updated", func() {
			// Generate a unique name with random suffix to avoid conflicts
			randomSuffix := kubernetes.RandomString()[0:6]
			key := types.NamespacedName{
				Name:      fmt.Sprintf("humio-pdf-update-%s", randomSuffix),
				Namespace: clusterKey.Namespace,
			}

			deploymentKey := types.NamespacedName{
				Name:      key.Name + "-pdf-render-service",
				Namespace: key.Namespace,
			}

			serviceKey := types.NamespacedName{
				Name:      key.Name + "-pdf-render-service",
				Namespace: key.Namespace,
			}

			suite.UsingClusterBy(clusterKey.Name, "Cleaning up any existing resources from previous test runs")
			existingResource := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
			}
			err := k8sClient.Delete(ctx, existingResource)
			if err != nil && !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, mediumTimeout, suite.TestInterval).Should(BeTrue(), "Existing resources should be cleaned up")

			suite.UsingClusterBy(clusterKey.Name, "Creating the HumioPdfRenderService CR")
			hprs := suite.CreatePdfRenderServiceCR(ctx, k8sClient, key, false)
			Expect(hprs).NotTo(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "Waiting for observedGeneration to catch up")
			suite.WaitForObservedGeneration(ctx, k8sClient, hprs, testTimeout*2, suite.TestInterval)

			suite.UsingClusterBy(clusterKey.Name, "Ensuring the PDF render deployment is ready")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			suite.UsingClusterBy(clusterKey.Name, "Verifying the HumioPdfRenderService is in Running state")
			Eventually(func() string {
				updated := &humiov1alpha1.HumioPdfRenderService{}
				if err := k8sClient.Get(ctx, key, updated); err != nil {
					return fmt.Sprintf("Error getting HPRS: %v", err)
				}
				return updated.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

			// Verify the Deployment is created with the correct default PDF render image and replicas
			suite.UsingClusterBy(clusterKey.Name, "Verifying the initial Deployment configuration")
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, deployment)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(deployment.Spec.Template.Spec.Containers[0].Image).To(Equal(PDFRenderServiceImage))
			Expect(*deployment.Spec.Replicas).To(Equal(int32(1)))

			// --- Update the CR ---
			updatedImage := "updated/image:v2"
			updatedReplicas := int32(2)
			updatedPort := int32(5123)

			suite.UsingClusterBy(clusterKey.Name, "Updating the HumioPdfRenderService spec")
			var freshHprs *humiov1alpha1.HumioPdfRenderService
			Eventually(func() error {
				freshHprs = &humiov1alpha1.HumioPdfRenderService{}
				if err := k8sClient.Get(ctx, key, freshHprs); err != nil {
					return err
				}
				freshHprs.Spec.Image = updatedImage
				freshHprs.Spec.Replicas = updatedReplicas
				freshHprs.Spec.Port = updatedPort
				return k8sClient.Update(ctx, freshHprs)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "Waiting for observedGeneration to catch up after update")
			suite.WaitForObservedGeneration(ctx, k8sClient, freshHprs, longTimeout, suite.TestInterval)

			suite.UsingClusterBy(clusterKey.Name, "Ensuring the PDF render deployment is ready after update")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			suite.UsingClusterBy(clusterKey.Name, "Verifying the Deployment is updated with new image")
			Eventually(func() string {
				if err := k8sClient.Get(ctx, deploymentKey, deployment); err != nil {
					return ""
				}
				if len(deployment.Spec.Template.Spec.Containers) == 0 {
					return ""
				}
				return deployment.Spec.Template.Spec.Containers[0].Image
			}, testTimeout, suite.TestInterval).Should(Equal(updatedImage))

			suite.UsingClusterBy(clusterKey.Name, "Verifying the Deployment is updated with new replicas")
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, deploymentKey, deployment); err != nil || deployment.Spec.Replicas == nil {
					return -1
				}
				return *deployment.Spec.Replicas
			}, testTimeout, suite.TestInterval).Should(Equal(updatedReplicas))

			suite.UsingClusterBy(clusterKey.Name, "Verifying the Service is updated with the new port")
			service := &corev1.Service{}
			Eventually(func() int32 {
				if err := k8sClient.Get(ctx, serviceKey, service); err != nil || len(service.Spec.Ports) == 0 {
					return -1
				}
				return service.Spec.Ports[0].Port
			}, testTimeout, suite.TestInterval).Should(Equal(updatedPort))

			suite.UsingClusterBy(clusterKey.Name, "Verifying the Service is updated with the new type")
			Eventually(func() corev1.ServiceType {
				if err := k8sClient.Get(ctx, serviceKey, service); err != nil {
					return ""
				}
				return service.Spec.Type
			}, testTimeout, suite.TestInterval).Should(Equal(corev1.ServiceTypeClusterIP))

			DeferCleanup(func() {
				suite.UsingClusterBy(clusterKey.Name, "Cleaning up test resources")
				_ = k8sClient.Delete(ctx, hprs)
				Eventually(func() bool {
					err := k8sClient.Get(ctx, key, &humiov1alpha1.HumioPdfRenderService{})
					return k8serrors.IsNotFound(err)
				}, mediumTimeout, suite.TestInterval).Should(BeTrue(), "PDF render service should be cleaned up")
			})
		})

	})

	// Test Case 4: PDF Render Service Resources and Probes
	Context("PDF Render Service Resources and Probes", Label("envtest", "dummy", "real"), func() {
		It("should correctly set up resources and probes when specified", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humio-pdf-resources-test",
				Namespace: clusterKey.Namespace,
			}

			deploymentKey := types.NamespacedName{
				Name:      key.Name + "-pdf-render-service",
				Namespace: key.Namespace,
			}

			// Clean up any existing resources from previous test runs
			suite.UsingClusterBy(clusterKey.Name, "Cleaning up any existing resources from previous test runs")
			existingResource := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
			}
			err := k8sClient.Delete(ctx, existingResource)
			if err != nil && !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, mediumTimeout, suite.TestInterval).Should(BeTrue(), "Existing resources should be cleaned up")

			suite.UsingClusterBy(clusterKey.Name, "Creating a HumioPdfRenderService with resource requirements and probes")
			hprs := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
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
								Port: intstr.FromInt(3152),
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
						InitialDelaySeconds: 30,
						TimeoutSeconds:      60,
					},
				},
			}
			Expect(k8sClient.Create(ctx, hprs)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "Waiting for observedGeneration to catch up")
			suite.WaitForObservedGeneration(ctx, k8sClient, hprs, longTimeout, suite.TestInterval)

			suite.UsingClusterBy(clusterKey.Name, "Ensuring PDF render deployment is ready")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			suite.UsingClusterBy(clusterKey.Name, "Verifying the Deployment has correct resource requirements")
			// First, get and log the actual values for debugging
			var dep appsv1.Deployment
			Eventually(func() error {
				if err := k8sClient.Get(ctx, deploymentKey, &dep); err != nil {
					return err
				}
				if len(dep.Spec.Template.Spec.Containers) == 0 {
					return fmt.Errorf("no containers found in deployment")
				}

				c := dep.Spec.Template.Spec.Containers[0]
				fmt.Printf("Actual resources in deployment - CPU Limits: %v, Memory Limits: %v, CPU Requests: %v, Memory Requests: %v\n",
					c.Resources.Limits.Cpu(),
					c.Resources.Limits.Memory(),
					c.Resources.Requests.Cpu(),
					c.Resources.Requests.Memory())

				return nil
			}, longTimeout, suite.TestInterval).Should(Succeed())

			// Now check each resource value individually with better error messages
			container := dep.Spec.Template.Spec.Containers[0]

			suite.UsingClusterBy(clusterKey.Name, "Checking CPU limit")
			Expect(container.Resources.Limits).To(HaveKey(corev1.ResourceCPU))
			Expect(container.Resources.Limits.Cpu().String()).To(Equal("500m"))

			suite.UsingClusterBy(clusterKey.Name, "Checking Memory limit")
			Expect(container.Resources.Limits).To(HaveKey(corev1.ResourceMemory))
			Expect(container.Resources.Limits.Memory().String()).To(Equal("512Mi"))

			suite.UsingClusterBy(clusterKey.Name, "Checking CPU request")
			Expect(container.Resources.Requests).To(HaveKey(corev1.ResourceCPU))
			Expect(container.Resources.Requests.Cpu().String()).To(Equal("100m"))

			suite.UsingClusterBy(clusterKey.Name, "Checking Memory request")
			Expect(container.Resources.Requests).To(HaveKey(corev1.ResourceMemory))
			Expect(container.Resources.Requests.Memory().String()).To(Equal("128Mi"))

			suite.UsingClusterBy(clusterKey.Name, "Verifying the Deployment has correct liveness probe")
			Expect(container.LivenessProbe).NotTo(BeNil())
			Expect(container.LivenessProbe.HTTPGet).NotTo(BeNil())
			Expect(container.LivenessProbe.HTTPGet.Path).To(Equal("/health"))
			Expect(container.LivenessProbe.InitialDelaySeconds).To(Equal(int32(30)))
			Expect(container.LivenessProbe.TimeoutSeconds).To(Equal(int32(60)))

			suite.UsingClusterBy(clusterKey.Name, "Verifying the Deployment has correct readiness probe")
			Expect(container.ReadinessProbe).NotTo(BeNil())
			Expect(container.ReadinessProbe.HTTPGet).NotTo(BeNil())
			Expect(container.ReadinessProbe.HTTPGet.Path).To(Equal("/ready"))
			Expect(container.ReadinessProbe.InitialDelaySeconds).To(Equal(int32(30)))
			Expect(container.ReadinessProbe.TimeoutSeconds).To(Equal(int32(60)))

			// Clean up resources
			suite.UsingClusterBy(clusterKey.Name, "Cleaning up test resources")
			Expect(k8sClient.Delete(ctx, hprs)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, mediumTimeout, suite.TestInterval).Should(BeTrue(), "PDF render service should be cleaned up")
		})
	})

	// Test Case 5: PDF Render Service Environment Variables
	Context("PDF Render Service Environment Variables", Label("envtest", "dummy", "real"), func() {
		It("should correctly configure environment variables (create and update)", func() {
			ctx := context.Background()
			key := types.NamespacedName{

				Name:      "humio-pdf-update-test",
				Namespace: clusterKey.Namespace,
			}

			deploymentKey := types.NamespacedName{
				Name:      key.Name + "-pdf-render-service",
				Namespace: key.Namespace,
			}

			// Clean up any existing resources from previous test runs
			suite.UsingClusterBy(clusterKey.Name, "Cleaning up any existing resources from previous test runs")
			existingResource := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
			}
			err := k8sClient.Delete(ctx, existingResource)
			if err != nil && !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, mediumTimeout, suite.TestInterval).Should(BeTrue(), "Existing resources should be cleaned up")

			suite.UsingClusterBy(clusterKey.Name, "Creating a HumioPdfRenderService with custom environment variables")
			hprs := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					Replicas: 1,
					Image:    PDFRenderServiceImage,
					Port:     8080,
					EnvironmentVariables: []corev1.EnvVar{
						{Name: "LOG_LEVEL", Value: "debug"},
						{Name: "MAX_CONNECTIONS", Value: "100"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, hprs)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "Waiting for observedGeneration to catch up")
			suite.WaitForObservedGeneration(ctx, k8sClient, hprs, testTimeout*2, suite.TestInterval)

			suite.UsingClusterBy(clusterKey.Name, "Ensuring PDF render deployment is ready")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			dep := &appsv1.Deployment{}
			suite.UsingClusterBy(clusterKey.Name, "Waiting for the Deployment to exist")
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, dep)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "Verifying the Deployment has the correct environment variables")
			Eventually(func() []corev1.EnvVar {
				_ = k8sClient.Get(ctx, deploymentKey, dep)
				if len(dep.Spec.Template.Spec.Containers) == 0 {
					return nil
				}
				return dep.Spec.Template.Spec.Containers[0].Env
			}, testTimeout, suite.TestInterval).Should(ContainElements(
				corev1.EnvVar{Name: "LOG_LEVEL", Value: "debug"},
				corev1.EnvVar{Name: "MAX_CONNECTIONS", Value: "100"},
			))

			suite.UsingClusterBy(clusterKey.Name, "Updating environment variables")
			var freshHprs *humiov1alpha1.HumioPdfRenderService
			Eventually(func() error {
				freshHprs = &humiov1alpha1.HumioPdfRenderService{}
				if err := k8sClient.Get(ctx, key, freshHprs); err != nil {
					return err
				}
				freshHprs.Spec.EnvironmentVariables = []corev1.EnvVar{
					{Name: "LOG_LEVEL", Value: "info"},
					{Name: "MAX_CONNECTIONS", Value: "200"},
					{Name: "NEW_VAR", Value: "value"},
				}
				return k8sClient.Update(ctx, freshHprs)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "Waiting for observedGeneration to catch up after update")
			suite.WaitForObservedGeneration(ctx, k8sClient, freshHprs, testTimeout*2, suite.TestInterval)

			suite.UsingClusterBy(clusterKey.Name, "Ensuring PDF render deployment is ready after update")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			suite.UsingClusterBy(clusterKey.Name, "Verifying the updated environment variables")
			Eventually(func() []corev1.EnvVar {
				_ = k8sClient.Get(ctx, deploymentKey, dep)
				if len(dep.Spec.Template.Spec.Containers) == 0 {
					return nil
				}
				return dep.Spec.Template.Spec.Containers[0].Env
			}, testTimeout, suite.TestInterval).Should(ContainElements(
				corev1.EnvVar{Name: "LOG_LEVEL", Value: "info"},
				corev1.EnvVar{Name: "MAX_CONNECTIONS", Value: "200"},
				corev1.EnvVar{Name: "NEW_VAR", Value: "value"},
			))

			// Clean up resources
			suite.UsingClusterBy(clusterKey.Name, "Cleaning up test resources")
			Expect(k8sClient.Delete(ctx, hprs)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, mediumTimeout, suite.TestInterval).Should(BeTrue(), "PDF render service should be cleaned up")
		})
	})

	// Test Case 7: PDF Render Service Finalizer
	Context("PDF Render Service Finalizer", Label("envtest", "dummy", "real"), func() {
		It("should add a finalizer and clean up resources on deletion", func() {
			ctx := context.Background()
			hprsKey := types.NamespacedName{Name: "humio-pdf", Namespace: clusterKey.Namespace}

			deploymentKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-pdf-render-service", hprsKey.Name),
				Namespace: hprsKey.Namespace,
			}

			serviceKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-pdf-render-service", hprsKey.Name),
				Namespace: hprsKey.Namespace,
			}

			// Clean up any existing resources from previous test runs
			suite.UsingClusterBy(clusterKey.Name, "Cleaning up any existing resources from previous test runs")
			existingResource := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hprsKey.Name,
					Namespace: hprsKey.Namespace,
				},
			}
			err := k8sClient.Delete(ctx, existingResource)
			if err != nil && !k8serrors.IsNotFound(err) {
				Expect(err).NotTo(HaveOccurred())
			}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hprsKey, &humiov1alpha1.HumioPdfRenderService{})
				return k8serrors.IsNotFound(err)
			}, mediumTimeout, suite.TestInterval).Should(BeTrue(), "Existing resources should be cleaned up")

			suite.UsingClusterBy(clusterKey.Name, "Creating the HumioPdfRenderService")
			hprs := suite.CreatePdfRenderServiceCR(ctx, k8sClient, hprsKey, false)
			Expect(hprs).NotTo(BeNil())

			suite.UsingClusterBy(clusterKey.Name, "Waiting for observedGeneration to catch up")
			suite.WaitForObservedGeneration(ctx, k8sClient, hprs, longTimeout, suite.TestInterval)

			suite.UsingClusterBy(clusterKey.Name, "Ensuring PDF render deployment is ready")
			suite.EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

			// Verify the deployment exists
			suite.UsingClusterBy(clusterKey.Name, "Verifying deployment exists")
			deployment := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, deploymentKey, deployment)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Verify the service exists
			suite.UsingClusterBy(clusterKey.Name, "Verifying service exists")
			service := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, serviceKey, service)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Verify the finalizer is added
			suite.UsingClusterBy(clusterKey.Name, "Verifying finalizer is added")
			Eventually(func() []string {
				fresh := &humiov1alpha1.HumioPdfRenderService{}
				if err := k8sClient.Get(ctx, hprsKey, fresh); err != nil {
					return nil
				}
				return fresh.Finalizers
			}, testTimeout, suite.TestInterval).ShouldNot(BeEmpty())

			suite.UsingClusterBy(clusterKey.Name, "Deleting the HumioPdfRenderService")
			Eventually(func() error {
				fresh := &humiov1alpha1.HumioPdfRenderService{}
				if err := k8sClient.Get(ctx, hprsKey, fresh); err != nil {
					return err
				}
				return k8sClient.Delete(ctx, fresh)
			}, mediumTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "Ensuring HumioPdfRenderService CR is removed")
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioPdfRenderService{}
				err := k8sClient.Get(ctx, hprsKey, fresh)
				return k8serrors.IsNotFound(err)
			}, longTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "Ensuring deployment is cleaned up")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, deploymentKey, &appsv1.Deployment{})
				return k8serrors.IsNotFound(err)
			}, longTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "Ensuring service is cleaned up")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, &corev1.Service{})
				return k8serrors.IsNotFound(err)
			}, longTimeout, suite.TestInterval).Should(BeTrue())
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
