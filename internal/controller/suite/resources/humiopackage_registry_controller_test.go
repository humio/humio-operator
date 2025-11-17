package resources

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/registries"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const gitlabResponseString = `{"version": "15.8.0", "revision": "123456"}`

func getMockHTTPClient() *registries.MockHTTPClient {
	return humioPackageRegistryReconciler.HTTPClient.(*registries.MockHTTPClient)
}

var _ = Describe("Humio Registry and Package", Ordered, Label("envtest", "dummy", "real"), func() {
	var mockHTTPClient *registries.MockHTTPClient
	ctx := context.Background()
	processId := GinkgoParallelProcess()
	hprName := fmt.Sprintf("example-hpr-%d", processId)
	BeforeAll(func() {
		mockHTTPClient = getMockHTTPClient()
		mockHTTPClient.Calls = nil
		mockHTTPClient.ExpectedCalls = nil
	})

	Describe("Humio Registry success", func() {
		BeforeEach(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil
		})
		It("Create HumioPackageRegistry should succeed for type marketplace", func() {
			setupMockHTTPResponses(mockHTTPClient, "GetWithContext", 200, "", 2, 2, mock.Anything, mock.Anything)

			name := hprName + "-marketplace"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "marketplace",
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			}

			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}

			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			// status.state should be set to Active
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hss
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			// check its gone
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("Create HumioPackageRegistry should succeed for type gitlab", func() {
			secretName := fmt.Sprintf("gitlab-token-%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			gitlabSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-gitlab-token",
				},
			}
			Expect(k8sClient.Create(ctx, gitlabSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, gitlabSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, gitlabResponseString, 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			name := hprName + "-gitlab"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "gitlab",
				Gitlab: &humiov1alpha1.RegistryConnectionGitlab{
					URL:      "https://gitlab.example.com/api/v4",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Project:  "myproject",
				},
			}

			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("Create HumioPackageRegistry should succeed for type github", func() {
			secretName := fmt.Sprintf("github-token-%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			githubSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-github-token",
				},
			}
			Expect(k8sClient.Create(ctx, githubSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, githubSecret)
				return err == nil
			}).Should(BeTrue())

			responseString := ""
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, responseString, 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			name := hprName + "-github"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "github",
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL:      "https://api.github.com",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Owner:    "myorg",
				},
			}

			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}

			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			// status.state should be set to Active
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Create HumioPackageRegistry should succeed for type artifactory", func() {
			secretName := fmt.Sprintf("artifactory-token-%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			artifactorySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-artifactory-token",
				},
			}
			Expect(k8sClient.Create(ctx, artifactorySecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, artifactorySecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, "", 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			name := hprName + "-artifactory"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "artifactory",
				Artifactory: &humiov1alpha1.RegistryConnectionArtifactory{
					URL:        "https://mycompany.jfrog.io/artifactory",
					TokenRef:   humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Repository: "generic-repo",
				},
			}

			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}
			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			// status.state should be set to Active
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("Create HumioPackageRegistry should succeed for type aws", func() {
			accessKeySecretName := fmt.Sprintf("aws-access-key-%d", GinkgoRandomSeed())
			accessSecretSecretName := fmt.Sprintf("aws-access-secret-%d", GinkgoRandomSeed())
			secretAccessKeyRef := types.NamespacedName{
				Name:      accessKeySecretName,
				Namespace: clusterKey.Namespace,
			}
			secretSecretKey := types.NamespacedName{
				Name:      accessSecretSecretName,
				Namespace: clusterKey.Namespace,
			}

			accessKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretAccessKeyRef.Name,
					Namespace: secretAccessKeyRef.Namespace,
				},
				StringData: map[string]string{
					"access-key": "AKIAIOSFODNN7EXAMPLE",
				},
			}
			accessSecretSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretSecretKey.Name,
					Namespace: secretSecretKey.Namespace,
				},
				StringData: map[string]string{
					"secret-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				},
			}
			Expect(k8sClient.Create(ctx, accessKeySecret)).Should(Succeed())
			Expect(k8sClient.Create(ctx, accessSecretSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretAccessKeyRef, accessKeySecret)
				return err == nil
			}).Should(BeTrue())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretSecretKey, accessSecretSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, "", 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			name := hprName + "-aws"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "aws",
				Aws: &humiov1alpha1.RegistryConnectionAws{
					Region:     "us-east-1",
					Domain:     "my-domain",
					Repository: "my-repo",
					AccessKeyRef: humiov1alpha1.SecretKeyRef{
						Name: accessKeySecretName,
						Key:  "access-key",
					},
					AccessSecretRef: humiov1alpha1.SecretKeyRef{
						Name: accessSecretSecretName,
						Key:  "secret-key",
					},
				},
			}
			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			// status.state should be set to Active
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// Verify HTTP call was made
			Expect(mockHTTPClient.AssertExpectations(GinkgoT())).To(BeTrue())

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			// check its gone
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Create HumioPackageRegistry should succeed for type gcloud", func() {
			secretName := fmt.Sprintf("gcloud-service-account-%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			gcloudSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"service-account.json": `{
								"type": "service_account",
								"project_id": "my-project",
								"private_key_id": "key-id",
								"private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC91clQXElK2hh9\nxkYmWkC2aUP0SpUc2lUqYf0enn+C0YBztnRdXalUeq26wRyIHVSAX9YX5lix0Gt3\nk2vSjshTcoH/7gnhrxY3yM/L00agD3FG8PXJcRAwJ1NqRJyqTkeyVqsgCMrbRteh\nkc0WhmjX4C/WSQi+EFMFzsZVe1AKNUW6EZtq41iIhI5NMjK/nbguUxa8lmUWojSb\nuCxg9NOrkdqRzIAmFAy1ak37GeuxI3NVqf2PLpM+Ko7RJ/PGOOQKJSObPSoy5a+6\nhk54L+OQ39ch8MaeHNn/X3XWPc2mdRdZKAN+TEhyWVKQBqqAJRrW/InEUc6hODZS\nG44DoQcFAgMBAAECggEAB+8Oadhhi8pPqboGpoWxHK6Lk4Mmdj09v/a2cHgpVhtR\nZgSjGl/WutwhtKNrgNjQ9kiLFxaecFgIlcfIgtVK1An+Guck7JS3tf8jiB49XmUm\n09MwQooCJjEOkGtrrMZ2wqJSppUXfVCZpHwGeUGG0jbhaPBGeEMQZTa+HUZ5EuQS\no7e6xehtbO4ngn7HJYpQef8/FfaUN7GGbeNVXyXyPFeL5IANySdhQYOkdjErj7Mn\nQ7mXrJgbRM3fcS5JwNdnUqGYkU3EQm1hFSBt6DnI8YvyOQbKGYjAnydpFVsKsovH\nPLkQAK79T3x4xs8KQejP9w9ciisjkTSULq30gDrsxQKBgQDlrUofPJddz4H9OtOm\ndk5vaXI1nrVvZtTaMZrix4blySEEMO8nntxkFHAbJlOYGNn6Rqr2UaRt6XdV3urM\nZIzE9unKCmtPMn07Loj2dGuJ9fJXQrkhxs0FUctbXibNdowWKeWVPipq3UQSzk1c\nnkGVRnExVOZhKtFw0PshwdaaowKBgQDTl4o4RA4rArMUnAQbicHe2Ch3M5gKUSV6\nnwmiuD458BtjgX0k0qDErZrF9c1cBNOGD8Hdcbal2m9YEKPgtcamaKqN6lPn4bEf\nikJpPpXJ6zlqGOJMfJJFCAymmDri4LN4Nj3Id76bRXirxpnk1SvIH5u9O/NZnaSb\n43BRgj3aNwKBgQClvl4lGJarLhpCYfdmwy1rHQ88PqH0GKM2KmH5kb95h6F54s5T\nK0MkPdOA5DGjKxvyjpjFVLlyT+68WzfZ9B3Z7c1c7hPufSL+WGCiafVJA+G0swPi\nqhI96n70Goep8gi53dY90zTNFYwQfiw50ELHtKPu07PFHx8xaL4x6C40PQKBgEmg\nwNsla1yyGsjAJXnDrO+zfhlEndJxPD54Gu1BeX3FvHIavAZVONZXprTd/LDZiRVs\nZER/blQ2N2qIl8340wBTCY5KjRnyYiUcglGHEq5pqNfvgsekzW0yCNzrugn6sNjS\n3xrj+DKlsQDtId4MA6kmvpXRx7NWdNI+CXaDgKxvAoGAWiXnOqzCqILyWaiIGGpg\nS1Hcnvn7juzpIwseEefizdIkSs9fVnbeC9X/kUnXWDhtbB2+B5QeXKOd1Vi/eXPS\nxQx236rChudS34+RmHB/WMiRWc3MfxDNquJdspq1vpSi80oMM9pzAbJEvLoOVIjz\ngie6cMddBXHxbQK3iDVGYzc=\n-----END PRIVATE KEY-----\n",
								"client_email": "test@my-project.iam.gserviceaccount.com",
								"client_id": "123456789012345678901",
								"auth_uri": "https://accounts.google.com/o/oauth2/auth",
								"token_uri": "https://oauth2.googleapis.com/token",
								"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
								"client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/test%40my-project.iam.gserviceaccount.com"
							}`,
				},
			}
			Expect(k8sClient.Create(ctx, gcloudSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, gcloudSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, "", 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))
			setupMockResponses(mockHTTPClient, "GetGcloudAccessToken", "mock-access-token", nil, 2, 2, mock.Anything, mock.AnythingOfType("[]uint8"))

			name := hprName + "-gcloud"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "gcloud",
				Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
					URL:        "https://artifactregistry.googleapis.com",
					ProjectID:  "my-project",
					Location:   "us-central1",
					Repository: "my-repo",
					ServiceAccountKeyRef: humiov1alpha1.SecretKeyRef{
						Name: secretName,
						Key:  "service-account.json",
					},
				},
			}
			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			// status.state should be set to Active since we're properly mocking OAuth2
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Describe("Humio Registry failure", func() {
		BeforeEach(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil
		})
		It("Create HumioPackageRegistry should fail for type marketplace", func() {
			setupMockHTTPResponses(mockHTTPClient, "GetWithContext", 404, "", 2, 2, mock.Anything, mock.Anything)

			name := hprName + "failed-marketplace"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "marketplace",
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			}
			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateConfigError))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hss
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			// check its gone
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("Create HumioPackageRegistry should fail for type gitlab", func() {
			secretName := fmt.Sprintf("gitlab-token-failed%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			gitlabSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-gitlab-token",
				},
			}
			Expect(k8sClient.Create(ctx, gitlabSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, gitlabSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 404, gitlabResponseString, 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			name := hprName + "failed-gitlab"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "gitlab",
				Gitlab: &humiov1alpha1.RegistryConnectionGitlab{
					URL:      "https://gitlab.example.com/api/v4",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Project:  "myproject",
				},
			}

			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateConfigError))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("Create HumioPackageRegistry should fail for type github", func() {
			secretName := fmt.Sprintf("github-token-failed%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			githubSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-github-token",
				},
			}
			Expect(k8sClient.Create(ctx, githubSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, githubSecret)
				return err == nil
			}).Should(BeTrue())

			responseString := ""
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 404, responseString, 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			name := hprName + "failed-github"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "github",
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL:      "https://api.github.com",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Owner:    "myorg",
				},
			}

			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}

			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateConfigError))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Create HumioPackageRegistry should fail for type artifactory", func() {
			secretName := fmt.Sprintf("artifactory-token-failed%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			artifactorySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-artifactory-token",
				},
			}
			Expect(k8sClient.Create(ctx, artifactorySecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, artifactorySecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 404, "", 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			name := hprName + "failed-artifactory"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "artifactory",
				Artifactory: &humiov1alpha1.RegistryConnectionArtifactory{
					URL:        "https://mycompany.jfrog.io/artifactory",
					TokenRef:   humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Repository: "generic-repo",
				},
			}

			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}
			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateConfigError))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("Create HumioPackageRegistry should fail for type aws", func() {
			accessKeySecretName := fmt.Sprintf("aws-access-key-failed%d", GinkgoRandomSeed())
			accessSecretSecretName := fmt.Sprintf("aws-access-secret-failed%d", GinkgoRandomSeed())
			secretAccessKeyRef := types.NamespacedName{
				Name:      accessKeySecretName,
				Namespace: clusterKey.Namespace,
			}
			secretSecretKey := types.NamespacedName{
				Name:      accessSecretSecretName,
				Namespace: clusterKey.Namespace,
			}

			accessKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretAccessKeyRef.Name,
					Namespace: secretAccessKeyRef.Namespace,
				},
				StringData: map[string]string{
					"access-key": "AKIAIOSFODNN7EXAMPLE",
				},
			}
			accessSecretSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretSecretKey.Name,
					Namespace: secretSecretKey.Namespace,
				},
				StringData: map[string]string{
					"secret-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				},
			}
			Expect(k8sClient.Create(ctx, accessKeySecret)).Should(Succeed())
			Expect(k8sClient.Create(ctx, accessSecretSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretAccessKeyRef, accessKeySecret)
				return err == nil
			}).Should(BeTrue())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretSecretKey, accessSecretSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 404, "", 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			name := hprName + "failed-aws"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "aws",
				Aws: &humiov1alpha1.RegistryConnectionAws{
					Region:     "us-east-1",
					Domain:     "my-domain",
					Repository: "my-repo",
					AccessKeyRef: humiov1alpha1.SecretKeyRef{
						Name: accessKeySecretName,
						Key:  "access-key",
					},
					AccessSecretRef: humiov1alpha1.SecretKeyRef{
						Name: accessSecretSecretName,
						Key:  "secret-key",
					},
				},
			}
			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateConfigError))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// Verify HTTP call was made
			Expect(mockHTTPClient.AssertExpectations(GinkgoT())).To(BeTrue())

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Create HumioPackageRegistry should fail for type gcloud", func() {
			secretName := fmt.Sprintf("gcloud-service-account-failed%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			gcloudSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"service-account.json": `{
								"type": "service_account",
								"project_id": "my-project",
								"private_key_id": "key-id",
								"private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC91clQXElK2hh9\nxkYmWkC2aUP0SpUc2lUqYf0enn+C0YBztnRdXalUeq26wRyIHVSAX9YX5lix0Gt3\nk2vSjshTcoH/7gnhrxY3yM/L00agD3FG8PXJcRAwJ1NqRJyqTkeyVqsgCMrbRteh\nkc0WhmjX4C/WSQi+EFMFzsZVe1AKNUW6EZtq41iIhI5NMjK/nbguUxa8lmUWojSb\nuCxg9NOrkdqRzIAmFAy1ak37GeuxI3NVqf2PLpM+Ko7RJ/PGOOQKJSObPSoy5a+6\nhk54L+OQ39ch8MaeHNn/X3XWPc2mdRdZKAN+TEhyWVKQBqqAJRrW/InEUc6hODZS\nG44DoQcFAgMBAAECggEAB+8Oadhhi8pPqboGpoWxHK6Lk4Mmdj09v/a2cHgpVhtR\nZgSjGl/WutwhtKNrgNjQ9kiLFxaecFgIlcfIgtVK1An+Guck7JS3tf8jiB49XmUm\n09MwQooCJjEOkGtrrMZ2wqJSppUXfVCZpHwGeUGG0jbhaPBGeEMQZTa+HUZ5EuQS\no7e6xehtbO4ngn7HJYpQef8/FfaUN7GGbeNVXyXyPFeL5IANySdhQYOkdjErj7Mn\nQ7mXrJgbRM3fcS5JwNdnUqGYkU3EQm1hFSBt6DnI8YvyOQbKGYjAnydpFVsKsovH\nPLkQAK79T3x4xs8KQejP9w9ciisjkTSULq30gDrsxQKBgQDlrUofPJddz4H9OtOm\ndk5vaXI1nrVvZtTaMZrix4blySEEMO8nntxkFHAbJlOYGNn6Rqr2UaRt6XdV3urM\nZIzE9unKCmtPMn07Loj2dGuJ9fJXQrkhxs0FUctbXibNdowWKeWVPipq3UQSzk1c\nnkGVRnExVOZhKtFw0PshwdaaowKBgQDTl4o4RA4rArMUnAQbicHe2Ch3M5gKUSV6\nnwmiuD458BtjgX0k0qDErZrF9c1cBNOGD8Hdcbal2m9YEKPgtcamaKqN6lPn4bEf\nikJpPpXJ6zlqGOJMfJJFCAymmDri4LN4Nj3Id76bRXirxpnk1SvIH5u9O/NZnaSb\n43BRgj3aNwKBgQClvl4lGJarLhpCYfdmwy1rHQ88PqH0GKM2KmH5kb95h6F54s5T\nK0MkPdOA5DGjKxvyjpjFVLlyT+68WzfZ9B3Z7c1c7hPufSL+WGCiafVJA+G0swPi\nqhI96n70Goep8gi53dY90zTNFYwQfiw50ELHtKPu07PFHx8xaL4x6C40PQKBgEmg\nwNsla1yyGsjAJXnDrO+zfhlEndJxPD54Gu1BeX3FvHIavAZVONZXprTd/LDZiRVs\nZER/blQ2N2qIl8340wBTCY5KjRnyYiUcglGHEq5pqNfvgsekzW0yCNzrugn6sNjS\n3xrj+DKlsQDtId4MA6kmvpXRx7NWdNI+CXaDgKxvAoGAWiXnOqzCqILyWaiIGGpg\nS1Hcnvn7juzpIwseEefizdIkSs9fVnbeC9X/kUnXWDhtbB2+B5QeXKOd1Vi/eXPS\nxQx236rChudS34+RmHB/WMiRWc3MfxDNquJdspq1vpSi80oMM9pzAbJEvLoOVIjz\ngie6cMddBXHxbQK3iDVGYzc=\n-----END PRIVATE KEY-----\n",
								"client_email": "test@my-project.iam.gserviceaccount.com",
								"client_id": "123456789012345678901",
								"auth_uri": "https://accounts.google.com/o/oauth2/auth",
								"token_uri": "https://oauth2.googleapis.com/token",
								"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
								"client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/test%40my-project.iam.gserviceaccount.com"
							}`,
				},
			}
			Expect(k8sClient.Create(ctx, gcloudSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, gcloudSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockResponses(mockHTTPClient, "GetGcloudAccessToken", "mock-access-token", fmt.Errorf("oauth2 token error"), 2, 2, mock.Anything, mock.AnythingOfType("[]uint8"))

			name := hprName + "failed-gcloud"
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "gcloud",
				Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
					URL:        "https://artifactregistry.googleapis.com",
					ProjectID:  "my-project",
					Location:   "us-central1",
					Repository: "my-repo",
					ServiceAccountKeyRef: humiov1alpha1.SecretKeyRef{
						Name: secretName,
						Key:  "service-account.json",
					},
				},
			}
			key := types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: hprSpec,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateConfigError))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// delete hpr
			Expect(k8sClient.Delete(ctx, k8sHpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, k8sHpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Describe("Humio Package Marketplace", Ordered, func() {
		var hpr *humiov1alpha1.HumioPackageRegistry
		var hprKey types.NamespacedName
		var view *humiov1alpha1.HumioView
		var viewKey types.NamespacedName
		var hp *humiov1alpha1.HumioPackage
		var packageKey types.NamespacedName

		name := hprName + "-marketplace-package"
		packageName := fmt.Sprintf("test-marketplace-package-%d", GinkgoRandomSeed())
		viewName := "marketplace-package"

		// this ensures the registry is available for all nested tests
		BeforeAll(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			// create the registry first
			setupMockHTTPResponses(mockHTTPClient, "GetWithContext", 200, "", 2, 2, mock.Anything, "https://packages.humio.com")
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "marketplace",
				Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
					URL: "https://packages.humio.com",
				},
			}
			hprKey = types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr = &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hprKey.Name,
					Namespace: hprKey.Namespace,
				},
				Spec: hprSpec,
			}

			packageKey = types.NamespacedName{
				Name:      packageName,
				Namespace: clusterKey.Namespace,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, hprKey, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// create the view
			viewKey = types.NamespacedName{
				Name:      viewName,
				Namespace: clusterKey.Namespace,
			}
			connections := make([]humiov1alpha1.HumioViewConnection, 0)
			connections = append(connections, humiov1alpha1.HumioViewConnection{
				RepositoryName: "humio",
				Filter:         "*",
			})
			view = &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               viewName,
					Description:        "important description",
					Connections:        connections,
				},
			}
			Expect(k8sClient.Create(ctx, view)).Should(Succeed())
			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))
		})
		// cleanup once tests are done
		AfterAll(func() {
			Expect(k8sClient.Delete(ctx, hpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hprKey, hpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
			Expect(k8sClient.Delete(ctx, view)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, viewKey, view)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		// reset mocks before each test
		BeforeEach(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			hp = &humiov1alpha1.HumioPackage{
				ObjectMeta: metav1.ObjectMeta{
					Name:      packageName,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPackageSpec{
					ManagedClusterName: clusterKey.Name,
					PackageName:        "crowdstrike/fdr",
					PackageVersion:     "1.1.4",
					PackageChecksum:    "sha256:bf6d5929ae79f9b43dc5dd378a20a39e2325e4d4c54bdffef248e8d129886453",
					RegistryRef: humiov1alpha1.RegistryReference{
						Name: hpr.Name,
					},
					PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
						{ViewNames: []string{viewName}},
					},
					Marketplace: &humiov1alpha1.MarketplacePackageInfo{
						Scope:   "crowdstrike",
						Package: "fdr",
					},
				},
			}

			// ensure package is not installed
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			pkg := &humiov1alpha1.HumioPackage{
				Status: humiov1alpha1.HumioPackageStatus{
					HumioPackageName: "crowdstrike/fdr",
				},
			}
			Eventually(func() string {
				_, err = humioClient.UninstallPackage(ctx, humioHttpClient, pkg, viewName)
				if err == nil {
					return ""
				}
				return err.Error()
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("is not installed"))
		})

		It("Successfully install package from marketplace", func() {
			zipFilePath := filepath.Join("assets", "crowdstrike-fdr-1.1.4.zip")
			zipContent, err := os.ReadFile(zipFilePath) // #nosec G304 - zipFilePath is constructed from test constants
			Expect(err).NotTo(HaveOccurred(), "Failed to read test zip file")

			mockVersionsResponse := `[{"version":"1.1.4","minHumioVersion":"1.30.0"},{"version":"1.1.3","minHumioVersion":"1.30.0"}]`
			setupMockHTTPResponses(mockHTTPClient, "GetWithContext", 200, mockVersionsResponse, 30, 0, mock.Anything, "https://packages.humio.com/packages/crowdstrike/fdr/versions")
			setupMockHTTPResponses(mockHTTPClient, "GetWithContext", 200, string(zipContent), 30, 0, mock.Anything, "https://packages.humio.com/packages/crowdstrike/fdr/1.1.4/download")

			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, packageKey, foundHp); err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateExists))

			// delete k8s package
			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Fail to install package from marketplace", func() {
			// hide requested version {"version":"1.1.4","minHumioVersion":"1.30.0"},
			mockVersionsResponse := `[{"version":"1.1.3","minHumioVersion":"1.30.0"}]`
			setupMockHTTPResponses(mockHTTPClient, "GetWithContext", 200, mockVersionsResponse, 1, 10, mock.Anything, "https://packages.humio.com/packages/crowdstrike/fdr/versions")

			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				if err := k8sClient.Get(ctx, packageKey, foundHp); err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateNotFound))

			// cleanup
			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Describe("Humio Package Gitlab", Ordered, func() {
		var hpr *humiov1alpha1.HumioPackageRegistry
		var hprKey types.NamespacedName
		var view *humiov1alpha1.HumioView
		var viewKey types.NamespacedName
		var hp *humiov1alpha1.HumioPackage
		var packageKey types.NamespacedName

		name := hprName + "-gitlab-package"
		packageName := fmt.Sprintf("test-gitlab-package-%d", GinkgoRandomSeed())
		viewName := "gitlab-package"

		BeforeAll(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			secretName := fmt.Sprintf("gitlab-package-token-%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			gitlabSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-gitlab-token",
				},
			}
			Expect(k8sClient.Create(ctx, gitlabSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, gitlabSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, gitlabResponseString, 2, 2, mock.Anything, mock.Anything, mock.AnythingOfType("map[string]string"))

			// create the registry
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "gitlab",
				Gitlab: &humiov1alpha1.RegistryConnectionGitlab{
					URL:      "https://gitlab.example.com/api/v4",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Project:  "myproject",
				},
			}
			hprKey = types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr = &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hprKey.Name,
					Namespace: hprKey.Namespace,
				},
				Spec: hprSpec,
			}

			packageKey = types.NamespacedName{
				Name:      packageName,
				Namespace: clusterKey.Namespace,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, hprKey, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// create the view
			viewKey = types.NamespacedName{
				Name:      viewName,
				Namespace: clusterKey.Namespace,
			}
			connections := make([]humiov1alpha1.HumioViewConnection, 0)
			connections = append(connections, humiov1alpha1.HumioViewConnection{
				RepositoryName: "humio",
				Filter:         "*",
			})
			view = &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               viewName,
					Description:        "important description",
					Connections:        connections,
				},
			}
			Expect(k8sClient.Create(ctx, view)).Should(Succeed())
			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))
		})

		// cleanup once tests are done
		AfterAll(func() {
			Expect(k8sClient.Delete(ctx, hpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hprKey, hpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, view)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, viewKey, view)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		BeforeEach(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			hp = &humiov1alpha1.HumioPackage{
				ObjectMeta: metav1.ObjectMeta{
					Name:      packageName,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPackageSpec{
					ManagedClusterName: clusterKey.Name,
					PackageName:        "crowdstrike/fdr",
					PackageVersion:     "1.1.4",
					PackageChecksum:    "sha256:bf6d5929ae79f9b43dc5dd378a20a39e2325e4d4c54bdffef248e8d129886453",
					RegistryRef: humiov1alpha1.RegistryReference{
						Name: hpr.Name,
					},
					PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
						{ViewNames: []string{viewName}},
					},
					Gitlab: &humiov1alpha1.GitlabPackageInfo{
						Package:   "fdr",
						AssetName: "crowdstrike-fdr-1.1.4.zip",
					},
				},
			}

			// ensure package is not installed
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			pkg := &humiov1alpha1.HumioPackage{
				Status: humiov1alpha1.HumioPackageStatus{
					HumioPackageName: "crowdstrike/fdr",
				},
			}
			Eventually(func() string {
				_, err = humioClient.UninstallPackage(ctx, humioHttpClient, pkg, viewName)
				if err == nil {
					return ""
				}
				return err.Error()
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("is not installed"))
		})

		It("Successfully install package from gitlab", func() {
			zipFilePath := filepath.Join("assets", "crowdstrike-fdr-1.1.4.zip")
			zipContent, err := os.ReadFile(zipFilePath) // #nosec G304 - zipFilePath is constructed from test constants
			Expect(err).NotTo(HaveOccurred(), "Failed to read test zip file")

			// Debug logging for binary data handling
			fmt.Printf("Original zip size: %d bytes\n", len(zipContent))
			fmt.Printf("Mock body size: %d bytes\n", len(string(zipContent)))
			reconstructed := []byte(string(zipContent))
			fmt.Printf("Zip content matches after string conversion: %v\n", bytes.Equal(zipContent, reconstructed))
			if !bytes.Equal(zipContent, reconstructed) {
				fmt.Printf("First 50 bytes original: %v\n", zipContent[:min(50, len(zipContent))])
				fmt.Printf("First 50 bytes reconstructed: %v\n", reconstructed[:min(50, len(reconstructed))])
			}

			// Mock the package existence check call
			setupMockHTTPResponses(mockHTTPClient, "HeadWithHeadersAndContext", 200, "", 30, 0,
				mock.Anything, "https://gitlab.example.com/api/v4/projects/myproject/packages/generic/fdr/1.1.4/crowdstrike-fdr-1.1.4.zip", mock.Anything)

			// Mock the package download call
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, string(zipContent), 30, 0,
				mock.Anything, "https://gitlab.example.com/api/v4/projects/myproject/packages/generic/fdr/1.1.4/crowdstrike-fdr-1.1.4.zip", mock.Anything)

			// Create the HumioPackage
			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateExists))

			// cleanup
			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Fail to install package from gitlab", func() {
			setupMockHTTPResponses(mockHTTPClient, "HeadWithHeadersAndContext", 404, "", 1, 1,
				mock.Anything, "https://gitlab.example.com/api/v4/projects/myproject/packages/generic/fdr/1.1.4/crowdstrike-fdr-1.1.4.zip", mock.Anything)

			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateNotFound))

			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Describe("Humio Package Github", Ordered, func() {
		var hpr *humiov1alpha1.HumioPackageRegistry
		var hprKey types.NamespacedName
		var view *humiov1alpha1.HumioView
		var viewKey types.NamespacedName
		var hp *humiov1alpha1.HumioPackage
		var packageKey types.NamespacedName

		name := hprName + "-github-package"
		packageName := fmt.Sprintf("test-github-package-%d", GinkgoRandomSeed())
		viewName := "github-package"

		BeforeAll(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			secretName := fmt.Sprintf("github-package-token-%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			githubSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-github-token",
				},
			}
			Expect(k8sClient.Create(ctx, githubSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, githubSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, "", 2, 2, mock.Anything, "https://api.github.com/", mock.Anything)

			// create the registry
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "github",
				Github: &humiov1alpha1.RegistryConnectionGithub{
					URL:      "https://api.github.com",
					TokenRef: humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Owner:    "myorg",
				},
			}
			hprKey = types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr = &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hprKey.Name,
					Namespace: hprKey.Namespace,
				},
				Spec: hprSpec,
			}

			packageKey = types.NamespacedName{
				Name:      packageName,
				Namespace: clusterKey.Namespace,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, hprKey, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// create the view
			viewKey = types.NamespacedName{
				Name:      viewName,
				Namespace: clusterKey.Namespace,
			}
			connections := make([]humiov1alpha1.HumioViewConnection, 0)
			connections = append(connections, humiov1alpha1.HumioViewConnection{
				RepositoryName: "humio",
				Filter:         "*",
			})
			view = &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               viewName,
					Description:        "important description",
					Connections:        connections,
				},
			}
			Expect(k8sClient.Create(ctx, view)).Should(Succeed())
			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))
		})

		// cleanup once tests are done
		AfterAll(func() {
			Expect(k8sClient.Delete(ctx, hpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hprKey, hpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, view)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, viewKey, view)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		BeforeEach(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			hp = &humiov1alpha1.HumioPackage{
				ObjectMeta: metav1.ObjectMeta{
					Name:      packageName,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPackageSpec{
					ManagedClusterName: clusterKey.Name,
					PackageName:        "crowdstrike/fdr",
					PackageVersion:     "1.1.4",
					PackageChecksum:    "sha256:bf6d5929ae79f9b43dc5dd378a20a39e2325e4d4c54bdffef248e8d129886453",
					RegistryRef: humiov1alpha1.RegistryReference{
						Name: hpr.Name,
					},
					PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
						{ViewNames: []string{viewName}},
					},
					Github: &humiov1alpha1.GithubPackageInfo{
						Repository: "fdr-package",
						AssetName:  "crowdstrike-fdr-1.1.4.zip",
						Tag:        "1.1.4",
					},
				},
			}

			// ensure package is not installed
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			pkg := &humiov1alpha1.HumioPackage{
				Status: humiov1alpha1.HumioPackageStatus{
					HumioPackageName: "crowdstrike/fdr",
				},
			}
			Eventually(func() string {
				_, err = humioClient.UninstallPackage(ctx, humioHttpClient, pkg, viewName)
				if err == nil {
					return ""
				}
				return err.Error()
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("is not installed"))
		})

		It("Successfully install package from github", func() {
			zipFilePath := filepath.Join("assets", "crowdstrike-fdr-1.1.4.zip")
			zipContent, err := os.ReadFile(zipFilePath) // #nosec G304 - zipFilePath is constructed from test constants
			Expect(err).NotTo(HaveOccurred(), "Failed to read test zip file")

			// Mock the release information call for both CheckPackageExists and DownloadPackage
			githubReleaseResponse := `{
						"tag_name": "1.1.4",
						"zipball_url": "https://api.github.com/repos/myorg/fdr-package/zipball/1.1.4",
						"assets": [
							{
								"name": "crowdstrike-fdr-1.1.4.zip",
								"browser_download_url": "https://github.com/myorg/fdr-package/releases/download/1.1.4/crowdstrike-fdr-1.1.4.zip"
							}
						]
					}`

			// Mock CheckPackageExists - increase maybe count to handle multiple reconciles
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, githubReleaseResponse, 30, 0, mock.Anything, "https://api.github.com/repos/myorg/fdr-package/releases/tags/1.1.4",
				mock.Anything)

			// Mock the actual asset download call
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, string(zipContent), 30, 0, mock.Anything, "https://github.com/myorg/fdr-package/releases/download/1.1.4/crowdstrike-fdr-1.1.4.zip",
				mock.Anything)

			// Create the HumioPackage
			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateExists))

			// cleanup
			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Fail to install package from github", func() {
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 404, "", 1, 1, mock.Anything, "https://api.github.com/repos/myorg/fdr-package/releases/tags/1.1.4",
				mock.Anything)
			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateNotFound))

			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})
	Describe("Humio Package Artifactory", Ordered, func() {
		var hpr *humiov1alpha1.HumioPackageRegistry
		var hprKey types.NamespacedName
		var view *humiov1alpha1.HumioView
		var viewKey types.NamespacedName
		var hp *humiov1alpha1.HumioPackage
		var packageKey types.NamespacedName

		name := hprName + "-artifactory-package"
		packageName := fmt.Sprintf("test-artifactory-package-%d", GinkgoRandomSeed())
		viewName := "artifactory-package"

		BeforeAll(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			secretName := fmt.Sprintf("artifactory-package-token-%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			artifactorySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"token": "test-artifactory-token",
				},
			}
			Expect(k8sClient.Create(ctx, artifactorySecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, artifactorySecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, "", 2, 2, mock.Anything, "https://mycompany.jfrog.io/artifactory/api/system/ping",
				mock.Anything)
			// create the registry
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "artifactory",
				Artifactory: &humiov1alpha1.RegistryConnectionArtifactory{
					URL:        "https://mycompany.jfrog.io/artifactory",
					TokenRef:   humiov1alpha1.SecretKeyRef{Name: secretName, Key: "token"},
					Repository: "generic-repo",
				},
			}
			hprKey = types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr = &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hprKey.Name,
					Namespace: hprKey.Namespace,
				},
				Spec: hprSpec,
			}

			packageKey = types.NamespacedName{
				Name:      packageName,
				Namespace: clusterKey.Namespace,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, hprKey, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// create the view
			viewKey = types.NamespacedName{
				Name:      viewName,
				Namespace: clusterKey.Namespace,
			}
			connections := make([]humiov1alpha1.HumioViewConnection, 0)
			connections = append(connections, humiov1alpha1.HumioViewConnection{
				RepositoryName: "humio",
				Filter:         "*",
			})
			view = &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               viewName,
					Description:        "important description",
					Connections:        connections,
				},
			}
			Expect(k8sClient.Create(ctx, view)).Should(Succeed())
			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))
		})

		// cleanup once tests are done
		AfterAll(func() {
			Expect(k8sClient.Delete(ctx, hpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hprKey, hpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, view)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, viewKey, view)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		BeforeEach(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			hp = &humiov1alpha1.HumioPackage{
				ObjectMeta: metav1.ObjectMeta{
					Name:      packageName,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPackageSpec{
					ManagedClusterName: clusterKey.Name,
					PackageName:        "crowdstrike/fdr",
					PackageVersion:     "1.1.4",
					PackageChecksum:    "sha256:bf6d5929ae79f9b43dc5dd378a20a39e2325e4d4c54bdffef248e8d129886453",
					RegistryRef: humiov1alpha1.RegistryReference{
						Name: hpr.Name,
					},
					PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
						{ViewNames: []string{viewName}},
					},
					Artifactory: &humiov1alpha1.ArtifactoryPackageInfo{
						FilePath: "packages/crowdstrike/fdr/1.1.4/crowdstrike-fdr-1.1.4.zip",
					},
				},
			}

			// ensure package is not installed
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			pkg := &humiov1alpha1.HumioPackage{
				Status: humiov1alpha1.HumioPackageStatus{
					HumioPackageName: "crowdstrike/fdr",
				},
			}
			Eventually(func() string {
				_, err = humioClient.UninstallPackage(ctx, humioHttpClient, pkg, viewName)
				if err == nil {
					return ""
				}
				return err.Error()
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("is not installed"))
		})

		It("Successfully install package from artifactory", func() {
			zipFilePath := filepath.Join("assets", "crowdstrike-fdr-1.1.4.zip")
			zipContent, err := os.ReadFile(zipFilePath) // #nosec G304 - zipFilePath is constructed from test constants
			Expect(err).NotTo(HaveOccurred(), "Failed to read test zip file")

			// Cluster / humio is unstable so we set a high number of mocked calls as the reconciller will eventually succeed
			// Mock the package existence check call (HEAD request)
			setupMockHTTPResponses(mockHTTPClient, "HeadWithHeadersAndContext", 200, "", 30, 0, mock.Anything, "https://mycompany.jfrog.io/artifactory/generic-repo/packages/crowdstrike/fdr/1.1.4/crowdstrike-fdr-1.1.4.zip",
				mock.Anything)

			// Mock the Artifactory package download call
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, string(zipContent), 30, 0, mock.Anything, "https://mycompany.jfrog.io/artifactory/generic-repo/packages/crowdstrike/fdr/1.1.4/crowdstrike-fdr-1.1.4.zip",
				mock.Anything)

			// Create the HumioPackage
			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateExists))

			// cleanup
			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Fail to install package from artifactory", func() {
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 404, "", 1, 1, mock.Anything, "https://mycompany.jfrog.io/artifactory/generic-repo/packages/crowdstrike/fdr/1.1.4/crowdstrike-fdr-1.1.4.zip",
				mock.Anything)

			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateNotFound))

			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})
	Describe("Humio Package Aws", Ordered, func() {
		var hpr *humiov1alpha1.HumioPackageRegistry
		var hprKey types.NamespacedName
		var view *humiov1alpha1.HumioView
		var viewKey types.NamespacedName
		var hp *humiov1alpha1.HumioPackage
		var packageKey types.NamespacedName

		name := hprName + "-aws-package"
		packageName := fmt.Sprintf("test-aws-package-%d", GinkgoRandomSeed())
		viewName := "aws-package"

		BeforeAll(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			accessKeySecretName := fmt.Sprintf("aws-access-key-package-%d", GinkgoRandomSeed())
			accessSecretSecretName := fmt.Sprintf("aws-access-secret-package-%d", GinkgoRandomSeed())
			secretAccessKeyRef := types.NamespacedName{
				Name:      accessKeySecretName,
				Namespace: clusterKey.Namespace,
			}
			secretSecretKey := types.NamespacedName{
				Name:      accessSecretSecretName,
				Namespace: clusterKey.Namespace,
			}

			accessKeySecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretAccessKeyRef.Name,
					Namespace: secretAccessKeyRef.Namespace,
				},
				StringData: map[string]string{
					"access-key": "AKIAIOSFODNN7EXAMPLE",
				},
			}
			accessSecretSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretSecretKey.Name,
					Namespace: secretSecretKey.Namespace,
				},
				StringData: map[string]string{
					"secret-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				},
			}
			Expect(k8sClient.Create(ctx, accessKeySecret)).Should(Succeed())
			Expect(k8sClient.Create(ctx, accessSecretSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretAccessKeyRef, accessKeySecret)
				return err == nil
			}).Should(BeTrue())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretSecretKey, accessSecretSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, "", 2, 2, mock.Anything, mock.Anything, mock.Anything)
			// create the registry
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "aws",
				Aws: &humiov1alpha1.RegistryConnectionAws{
					Region:     "us-east-1",
					Domain:     "my-domain",
					Repository: "my-repo",
					AccessKeyRef: humiov1alpha1.SecretKeyRef{
						Name: accessKeySecretName,
						Key:  "access-key",
					},
					AccessSecretRef: humiov1alpha1.SecretKeyRef{
						Name: accessSecretSecretName,
						Key:  "secret-key",
					},
				},
			}
			hprKey = types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr = &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hprKey.Name,
					Namespace: hprKey.Namespace,
				},
				Spec: hprSpec,
			}

			packageKey = types.NamespacedName{
				Name:      packageName,
				Namespace: clusterKey.Namespace,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, hprKey, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// create the view
			viewKey := types.NamespacedName{
				Name:      viewName,
				Namespace: clusterKey.Namespace,
			}
			connections := make([]humiov1alpha1.HumioViewConnection, 0)
			connections = append(connections, humiov1alpha1.HumioViewConnection{
				RepositoryName: "humio",
				Filter:         "*",
			})
			view = &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               viewName,
					Description:        "important description",
					Connections:        connections,
				},
			}
			Expect(k8sClient.Create(ctx, view)).Should(Succeed())
			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))
		})

		// cleanup once tests are done
		AfterAll(func() {
			Expect(k8sClient.Delete(ctx, hpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hprKey, hpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, view)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, viewKey, view)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		BeforeEach(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			hp = &humiov1alpha1.HumioPackage{
				ObjectMeta: metav1.ObjectMeta{
					Name:      packageName,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPackageSpec{
					ManagedClusterName: clusterKey.Name,
					PackageName:        "crowdstrike/fdr",
					PackageVersion:     "1.1.4",
					PackageChecksum:    "sha256:bf6d5929ae79f9b43dc5dd378a20a39e2325e4d4c54bdffef248e8d129886453",
					RegistryRef: humiov1alpha1.RegistryReference{
						Name: hpr.Name,
					},
					PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
						{ViewNames: []string{viewName}},
					},
					Aws: &humiov1alpha1.AwsPackageInfo{
						Namespace: "crowdstrike",
						Package:   "fdr",
						Filename:  "crowdstrike-fdr-1.1.4.zip",
					},
				},
			}

			// ensure package is not installed
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			pkg := &humiov1alpha1.HumioPackage{
				Status: humiov1alpha1.HumioPackageStatus{
					HumioPackageName: "crowdstrike/fdr",
				},
			}
			Eventually(func() string {
				_, err = humioClient.UninstallPackage(ctx, humioHttpClient, pkg, viewName)
				if err == nil {
					return ""
				}
				return err.Error()
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("is not installed"))
		})

		It("Successfully install package from aws", func() {
			zipFilePath := filepath.Join("assets", "crowdstrike-fdr-1.1.4.zip")
			zipContent, err := os.ReadFile(zipFilePath) // #nosec G304 - zipFilePath is constructed from test constants
			Expect(err).NotTo(HaveOccurred(), "Failed to read test zip file")

			// Mock the package assets check call (POST request) - return assets list
			assetsResponse := `{
				"assets": [
					{
						"name": "crowdstrike-fdr-1.1.4.zip",
						"size": 12345
					}
				],
				"package": "fdr",
				"format": "generic",
				"version": "1.1.4"
			}`
			// Mock the AWS CheckConnection call
			setupMockHTTPResponses(mockHTTPClient, "PostWithHeadersAndContext", 200, assetsResponse, 30, 0, mock.Anything, mock.MatchedBy(func(url string) bool {
				return strings.Contains(url, "codeartifact") && strings.Contains(url, "/v1/package/version/assets")
			}), mock.AnythingOfType("map[string]string"), mock.AnythingOfType("[]uint8"))

			// Mock the AWS package download call (GET request) - return zip content
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, string(zipContent), 30, 0, mock.Anything, mock.MatchedBy(func(url string) bool {
				return strings.Contains(url, "codeartifact") && strings.Contains(url, "/v1/package/version/asset")
			}), mock.AnythingOfType("map[string]string"))

			// Create the HumioPackage
			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateExists))

			// cleanup
			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Fail to install package from aws", func() {
			assetsResponse := `{
						"assets": [],
						"package": "fdr",
						"format": "generic",
						"version": "1.1.4"
					}`

			setupMockHTTPResponses(mockHTTPClient, "PostWithHeadersAndContext", 200, assetsResponse, 1, 1, mock.Anything, mock.MatchedBy(func(url string) bool {
				return strings.Contains(url, "codeartifact") && strings.Contains(url, "/v1/package/version/assets")
			}), mock.Anything, mock.Anything)
			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateNotFound))

			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})
	Describe("Humio Package Gcloud", Ordered, func() {
		var hpr *humiov1alpha1.HumioPackageRegistry
		var hprKey types.NamespacedName
		var view *humiov1alpha1.HumioView
		var viewKey types.NamespacedName
		var hp *humiov1alpha1.HumioPackage
		var packageKey types.NamespacedName

		name := hprName + "-gcloud-package"
		packageName := fmt.Sprintf("test-gcloud-package-%d", GinkgoRandomSeed())
		viewName := "gcloud-package"

		BeforeAll(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			secretName := fmt.Sprintf("gcloud-service-account-package-%d", GinkgoRandomSeed())
			secretKey := types.NamespacedName{
				Name:      secretName,
				Namespace: clusterKey.Namespace,
			}
			gcloudSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"service-account.json": `{
								"type": "service_account",
								"project_id": "my-project",
								"private_key_id": "key-id",
								"private_key": "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC91clQXElK2hh9\nxkYmWkC2aUP0SpUc2lUqYf0enn+C0YBztnRdXalUeq26wRyIHVSAX9YX5lix0Gt3\nk2vSjshTcoH/7gnhrxY3yM/L00agD3FG8PXJcRAwJ1NqRJyqTkeyVqsgCMrbRteh\nkc0WhmjX4C/WSQi+EFMFzsZVe1AKNUW6EZtq41iIhI5NMjK/nbguUxa8lmUWojSb\nuCxg9NOrkdqRzIAmFAy1ak37GeuxI3NVqf2PLpM+Ko7RJ/PGOOQKJSObPSoy5a+6\nhk54L+OQ39ch8MaeHNn/X3XWPc2mdRdZKAN+TEhyWVKQBqqAJRrW/InEUc6hODZS\nG44DoQcFAgMBAAECggEAB+8Oadhhi8pPqboGpoWxHK6Lk4Mmdj09v/a2cHgpVhtR\nZgSjGl/WutwhtKNrgNjQ9kiLFxaecFgIlcfIgtVK1An+Guck7JS3tf8jiB49XmUm\n09MwQooCJjEOkGtrrMZ2wqJSppUXfVCZpHwGeUGG0jbhaPBGeEMQZTa+HUZ5EuQS\no7e6xehtbO4ngn7HJYpQef8/FfaUN7GGbeNVXyXyPFeL5IANySdhQYOkdjErj7Mn\nQ7mXrJgbRM3fcS5JwNdnUqGYkU3EQm1hFSBt6DnI8YvyOQbKGYjAnydpFVsKsovH\nPLkQAK79T3x4xs8KQejP9w9ciisjkTSULq30gDrsxQKBgQDlrUofPJddz4H9OtOm\ndk5vaXI1nrVvZtTaMZrix4blySEEMO8nntxkFHAbJlOYGNn6Rqr2UaRt6XdV3urM\nZIzE9unKCmtPMn07Loj2dGuJ9fJXQrkhxs0FUctbXibNdowWKeWVPipq3UQSzk1c\nnkGVRnExVOZhKtFw0PshwdaaowKBgQDTl4o4RA4rArMUnAQbicHe2Ch3M5gKUSV6\nnwmiuD458BtjgX0k0qDErZrF9c1cBNOGD8Hdcbal2m9YEKPgtcamaKqN6lPn4bEf\nikJpPpXJ6zlqGOJMfJJFCAymmDri4LN4Nj3Id76bRXirxpnk1SvIH5u9O/NZnaSb\n43BRgj3aNwKBgQClvl4lGJarLhpCYfdmwy1rHQ88PqH0GKM2KmH5kb95h6F54s5T\nK0MkPdOA5DGjKxvyjpjFVLlyT+68WzfZ9B3Z7c1c7hPufSL+WGCiafVJA+G0swPi\nqhI96n70Goep8gi53dY90zTNFYwQfiw50ELHtKPu07PFHx8xaL4x6C40PQKBgEmg\nwNsla1yyGsjAJXnDrO+zfhlEndJxPD54Gu1BeX3FvHIavAZVONZXprTd/LDZiRVs\nZER/blQ2N2qIl8340wBTCY5KjRnyYiUcglGHEq5pqNfvgsekzW0yCNzrugn6sNjS\n3xrj+DKlsQDtId4MA6kmvpXRx7NWdNI+CXaDgKxvAoGAWiXnOqzCqILyWaiIGGpg\nS1Hcnvn7juzpIwseEefizdIkSs9fVnbeC9X/kUnXWDhtbB2+B5QeXKOd1Vi/eXPS\nxQx236rChudS34+RmHB/WMiRWc3MfxDNquJdspq1vpSi80oMM9pzAbJEvLoOVIjz\ngie6cMddBXHxbQK3iDVGYzc=\n-----END PRIVATE KEY-----\n",
								"client_email": "test@my-project.iam.gserviceaccount.com",
								"client_id": "123456789012345678901",
								"auth_uri": "https://accounts.google.com/o/oauth2/auth",
								"token_uri": "https://oauth2.googleapis.com/token",
								"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
								"client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/test%40my-project.iam.gserviceaccount.com"
							}`,
				},
			}
			Expect(k8sClient.Create(ctx, gcloudSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, gcloudSecret)
				return err == nil
			}).Should(BeTrue())

			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, "", 2, 2, mock.Anything, mock.Anything, mock.Anything)
			setupMockResponses(mockHTTPClient, "GetGcloudAccessToken", "mock-access-token", nil, 2, 2, mock.Anything, mock.Anything)
			// create the registry
			hprSpec := humiov1alpha1.HumioPackageRegistrySpec{
				ManagedClusterName: clusterKey.Name,
				DisplayName:        name,
				Enabled:            true,
				RegistryType:       "gcloud",
				Gcloud: &humiov1alpha1.RegistryConnectionGcloud{
					URL:        "https://artifactregistry.googleapis.com",
					ProjectID:  "my-project",
					Location:   "us-central1",
					Repository: "my-repo",
					ServiceAccountKeyRef: humiov1alpha1.SecretKeyRef{
						Name: secretName,
						Key:  "service-account.json",
					},
				},
			}
			hprKey = types.NamespacedName{
				Name:      name,
				Namespace: clusterKey.Namespace,
			}
			hpr = &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hprKey.Name,
					Namespace: hprKey.Namespace,
				},
				Spec: hprSpec,
			}

			packageKey = types.NamespacedName{
				Name:      packageName,
				Namespace: clusterKey.Namespace,
			}

			Expect(k8sClient.Create(ctx, hpr)).Should(Succeed())

			k8sHpr := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, hprKey, k8sHpr)
				return k8sHpr.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))
			Expect(k8sHpr.Spec).Should(Equal(hprSpec))

			// create the view
			viewKey := types.NamespacedName{
				Name:      viewName,
				Namespace: clusterKey.Namespace,
			}
			connections := make([]humiov1alpha1.HumioViewConnection, 0)
			connections = append(connections, humiov1alpha1.HumioViewConnection{
				RepositoryName: "humio",
				Filter:         "*",
			})
			view = &humiov1alpha1.HumioView{
				ObjectMeta: metav1.ObjectMeta{
					Name:      viewKey.Name,
					Namespace: viewKey.Namespace,
				},
				Spec: humiov1alpha1.HumioViewSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               viewName,
					Description:        "important description",
					Connections:        connections,
				},
			}
			Expect(k8sClient.Create(ctx, view)).Should(Succeed())
			fetchedView := &humiov1alpha1.HumioView{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, viewKey, fetchedView)
				return fetchedView.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))
		})

		// cleanup once tests are done
		AfterAll(func() {
			Expect(k8sClient.Delete(ctx, hpr)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, hprKey, hpr)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, view)).To(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, viewKey, view)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		BeforeEach(func() {
			mockHTTPClient = getMockHTTPClient()
			mockHTTPClient.Calls = nil
			mockHTTPClient.ExpectedCalls = nil

			hp = &humiov1alpha1.HumioPackage{
				ObjectMeta: metav1.ObjectMeta{
					Name:      packageName,
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioPackageSpec{
					ManagedClusterName: clusterKey.Name,
					PackageName:        "crowdstrike/fdr",
					PackageVersion:     "1.1.4",
					PackageChecksum:    "sha256:bf6d5929ae79f9b43dc5dd378a20a39e2325e4d4c54bdffef248e8d129886453",
					RegistryRef: humiov1alpha1.RegistryReference{
						Name: hpr.Name,
					},
					PackageInstallTargets: []humiov1alpha1.PackageInstallTarget{
						{ViewNames: []string{viewName}},
					},
					Gcloud: &humiov1alpha1.GcloudPackageInfo{
						Package:  "fdr",
						Filename: "crowdstrike-fdr-1.1.4.zip",
					},
				},
			}

			// ensure package is not installed
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
			pkg := &humiov1alpha1.HumioPackage{
				Status: humiov1alpha1.HumioPackageStatus{
					HumioPackageName: "crowdstrike/fdr",
				},
			}
			Eventually(func() string {
				_, err = humioClient.UninstallPackage(ctx, humioHttpClient, pkg, viewName)
				if err == nil {
					return ""
				}
				return err.Error()
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("is not installed"))
		})

		It("Successfully install package from gcloud", func() {
			zipFilePath := filepath.Join("assets", "crowdstrike-fdr-1.1.4.zip")
			zipContent, err := os.ReadFile(zipFilePath) // #nosec G304 - zipFilePath is constructed from test constants
			Expect(err).NotTo(HaveOccurred(), "Failed to read test zip file")

			setupMockResponses(mockHTTPClient, "GetGcloudAccessToken", "mock-access-token", nil, 30, 0, mock.Anything, mock.AnythingOfType("[]uint8"))
			// Mock the package existence check call (GET with Range header) - return 206 Partial Content
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 206, "", 20, 0, mock.Anything, "https://artifactregistry.googleapis.com/download/v1/projects/my-project/locations/us-central1/repositories/my-repo/files/fdr:1.1.4:crowdstrike-fdr-1.1.4.zip:download?alt=media",
				mock.MatchedBy(func(headers map[string]string) bool {
					_, hasRange := headers["Range"]
					return hasRange
				}))

			// Mock the GCloud package download call (GET without Range header) - return zip content
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 200, string(zipContent), 30, 0, mock.Anything, "https://artifactregistry.googleapis.com/download/v1/projects/my-project/locations/us-central1/repositories/my-repo/files/fdr:1.1.4:crowdstrike-fdr-1.1.4.zip:download?alt=media",
				mock.Anything)

			// Create the HumioPackage
			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateExists))

			// cleanup
			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
		It("Fail to install package from aws", func() {
			// Mock the package existence check call (GET with Range header)
			setupMockResponses(mockHTTPClient, "GetGcloudAccessToken", "mock-access-token", nil, 1, 1, mock.Anything, mock.Anything)
			setupMockHTTPResponses(mockHTTPClient, "GetWithHeadersAndContext", 404, "", 1, 1, mock.Anything, "https://artifactregistry.googleapis.com/download/v1/projects/my-project/locations/us-central1/repositories/my-repo/files/fdr:1.1.4:crowdstrike-fdr-1.1.4.zip:download?alt=media",
				mock.MatchedBy(func(headers map[string]string) bool {
					_, hasRange := headers["Range"]
					return hasRange
				}))

			// Create the HumioPackage
			Expect(k8sClient.Create(ctx, hp)).Should(Succeed())
			foundHp := &humiov1alpha1.HumioPackage{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				if err != nil {
					return ""
				}
				return foundHp.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageStateNotFound))

			Expect(k8sClient.Delete(ctx, hp)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, packageKey, foundHp)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})
})

// Helper function to set up multiple mock HTTP responses
func setupMockHTTPResponses(client *registries.MockHTTPClient, method string, statusCode int, body string, onceCount int, maybeCount int, args ...any) {
	// Set up the specified number of Once() calls
	for range onceCount {
		response := &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(body)),
		}
		client.On(method, args...).Return(response, nil).Once()
	}
	// Set up the specified number of Maybe() calls
	for range maybeCount {
		response := &http.Response{
			StatusCode: statusCode,
			Body:       io.NopCloser(strings.NewReader(body)),
		}
		client.On(method, args...).Return(response, nil).Maybe()
	}
}

//nolint:unparam
func setupMockResponses(client *registries.MockHTTPClient, method, response string, err error, onceCount int, maybeCount int, args ...any) {
	// Set up the specified number of Once() calls
	for range onceCount {
		client.On(method, args...).Return(response, err).Once()
	}
	// Set up the specified number of Maybe() calls
	for range maybeCount {
		client.On(method, args...).Return(response, err).Maybe()
	}
}
