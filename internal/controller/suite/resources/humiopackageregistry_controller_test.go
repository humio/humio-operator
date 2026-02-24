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
	"io"
	"net/http"
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/registries"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("HumioPackageRegistry Controller", Label("envtest", "dummy", "real"), func() {
	var mockHTTPClient *registries.MockHTTPClient

	BeforeEach(func() {
		humioClient.ClearHumioClientConnections(testRepoName)
		// Setup mock HTTP client for envtest
		mockHTTPClient = humioPackageRegistryReconciler.HTTPClient.(*registries.MockHTTPClient)
		mockHTTPClient.Calls = nil
		mockHTTPClient.ExpectedCalls = nil
	})

	AfterEach(func() {
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	Context("Basic CRUD Operations", func() {
		It("should create and delete package registry successfully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-package-registry",
				Namespace: clusterKey.Namespace,
			}

			// Setup mock HTTP responses for registry communication
			for range 2 {
				response := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader("")),
				}
				mockHTTPClient.On("GetWithContext", mock.Anything, mock.Anything).Return(response, nil).Once()
			}
			for range 2 {
				response := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader("")),
				}
				mockHTTPClient.On("GetWithContext", mock.Anything, mock.Anything).Return(response, nil).Maybe()
			}

			toCreate := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPackageRegistrySpec{
					ManagedClusterName: clusterKey.Name,
					RegistryType:       "marketplace",
					Enabled:            true, // Explicitly enable to reach Active state
					AllowDataDeletion:  true,
					Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
						URL: "https://test-marketplace.example.com",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioPackageRegistry: Creating package registry")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				return fetched.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateExists))

			// Verify finalizer added
			Expect(fetched.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioPackageRegistry: Deleting package registry")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Force-Finalize", Label("envtest", "dummy", "real"), func() {
		It("should force-finalize when annotation present", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "packageregistry-force-finalize",
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioPackageRegistry{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioPackageRegistrySpec{
					ManagedClusterName: clusterKey.Name,
					RegistryType:       "marketplace",
					Enabled:            false, // Disabled to avoid HTTP mock setup, still tests force-finalize
					AllowDataDeletion:  false, // Block deletion
					Marketplace: &humiov1alpha1.RegistryConnectionMarketplace{
						URL: "https://test-marketplace.example.com",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioPackageRegistry Force-Finalize: Creating registry with allowDataDeletion=false")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioPackageRegistry{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				return fetched.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioPackageRegistryStateDisabled))

			// Verify finalizer present
			Expect(fetched.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))

			// Attempt deletion (will be blocked by allowDataDeletion=false)
			suite.UsingClusterBy(clusterKey.Name, "HumioPackageRegistry Force-Finalize: Triggering deletion (should block)")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())

			// Verify resource stuck in deletion
			suite.UsingClusterBy(clusterKey.Name, "HumioPackageRegistry Force-Finalize: Verifying deletion is blocked")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return err == nil && fetched.GetDeletionTimestamp() != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify finalizer still present (blocked)
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(fetched.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))

			// Add force-finalize annotation
			suite.UsingClusterBy(clusterKey.Name, "HumioPackageRegistry Force-Finalize: Adding force-finalize annotation")
			Eventually(func() error {
				fresh := &humiov1alpha1.HumioPackageRegistry{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return err
				}
				if fresh.Annotations == nil {
					fresh.Annotations = make(map[string]string)
				}
				fresh.Annotations[controller.ForceFinalizerAnnotation] = controller.ForceFinalizerAnnotationValue
				return k8sClient.Update(ctx, fresh)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			// Verify finalizer removed and resource deleted
			suite.UsingClusterBy(clusterKey.Name, "HumioPackageRegistry Force-Finalize: Verifying force-finalize removes finalizer")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue(), "Resource should be deleted after force-finalize")
		})
	})
})
