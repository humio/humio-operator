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
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Humio ViewToken Controller", Label("envtest", "dummy", "real"), func() {
	var (
		ctx             context.Context
		cancel          context.CancelFunc
		humioHttpClient *api.Client
		k8sIPFilter     *humiov1alpha1.HumioIPFilter
		k8sView         *humiov1alpha1.HumioView
		crViewToken     *humiov1alpha1.HumioViewToken
		keyView         types.NamespacedName
		keyIPFilter     types.NamespacedName
		keyViewToken    types.NamespacedName
		specViewToken   humiov1alpha1.HumioViewTokenSpec
		k8sViewToken    *humiov1alpha1.HumioViewToken
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		humioClient.ClearHumioClientConnections(testRepoName)
		// dependencies
		humioHttpClient = humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})

		// enable token permissions updates
		_ = humioClient.EnableTokenUpdatePermissionsForTests(ctx, humioHttpClient)

		// create IPFilter dependency
		keyIPFilter = types.NamespacedName{
			Name:      "viewtoken-filter-cr",
			Namespace: clusterKey.Namespace,
		}
		specIPFilter := humiov1alpha1.HumioIPFilterSpec{
			ManagedClusterName: clusterKey.Name,
			Name:               "viewtoken-filter",
			IPFilter: []humiov1alpha1.FirewallRule{
				{Action: "allow", Address: "127.0.0.1"},
				{Action: "allow", Address: "10.0.0.0/8"},
			},
		}
		crIPFilter := &humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{
				Name:      keyIPFilter.Name,
				Namespace: keyIPFilter.Namespace,
			},
			Spec: specIPFilter,
		}
		// wait for IPFilter to be ready
		k8sIPFilter = &humiov1alpha1.HumioIPFilter{}
		suite.UsingClusterBy(clusterKey.Name, "HumioIPFilter: Creating the IPFilter successfully")
		Expect(k8sClient.Create(ctx, crIPFilter)).Should(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, keyIPFilter, k8sIPFilter)
			return k8sIPFilter.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioIPFilterStateExists))

		// view dependency
		keyView = types.NamespacedName{
			Name:      "viewtoken-view-cr",
			Namespace: clusterKey.Namespace,
		}
		specView := humiov1alpha1.HumioViewSpec{
			ManagedClusterName: clusterKey.Name,
			Name:               "viewtoken-view",
			Connections: []humiov1alpha1.HumioViewConnection{
				{
					RepositoryName: testRepo.Spec.Name,
				},
			},
		}
		crView := &humiov1alpha1.HumioView{
			ObjectMeta: metav1.ObjectMeta{
				Name:      keyView.Name,
				Namespace: keyView.Namespace,
			},
			Spec: specView,
		}
		Expect(k8sClient.Create(ctx, crView)).Should(Succeed())
		// wait for View to be ready
		k8sView = &humiov1alpha1.HumioView{}
		Eventually(func() string {
			_ = k8sClient.Get(ctx, keyView, k8sView)
			return k8sView.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))
	})

	AfterEach(func() {
		// wait for View to be purged
		Expect(k8sClient.Delete(ctx, k8sView)).Should(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, keyView, k8sView)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())
		// wait for IPFilter to be purged
		Expect(k8sClient.Delete(ctx, k8sIPFilter)).Should(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, keyIPFilter, k8sIPFilter)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())
		cancel()
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	Context("When creating a HumioViewToken CR instance with valid input", func() {
		BeforeEach(func() {
			permissionNames := []string{"ChangeFiles"}
			expireAt := metav1.NewTime(helpers.GetCurrentDay().AddDate(0, 0, 10))

			keyViewToken = types.NamespacedName{
				Name:      "viewtoken-cr",
				Namespace: clusterKey.Namespace,
			}
			specViewToken = humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "viewtoken",
					IPFilterName:       k8sIPFilter.Spec.Name,
					Permissions:        permissionNames,
					TokenSecretName:    "viewtoken-secret",
					ExpiresAt:          &expireAt,
				},
				ViewNames: []string{k8sView.Spec.Name},
			}
			crViewToken = &humiov1alpha1.HumioViewToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyViewToken.Name,
					Namespace: keyViewToken.Namespace,
				},
				Spec: specViewToken,
			}
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, k8sViewToken)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyViewToken, k8sViewToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should create the k8s HumioViewToken cr", func() {
			Expect(k8sClient.Create(ctx, crViewToken)).To(Succeed())
			k8sViewToken = &humiov1alpha1.HumioViewToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, k8sViewToken)
				return k8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		It("should create the humio view token", func() {
			Expect(k8sClient.Create(ctx, crViewToken)).To(Succeed())
			k8sViewToken = &humiov1alpha1.HumioViewToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, k8sViewToken)
				return k8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))

			var humioViewToken *humiographql.ViewTokenDetailsViewPermissionsToken
			Eventually(func() error {
				humioViewToken, err = humioClient.GetViewToken(ctx, humioHttpClient, k8sViewToken)
				if err != nil {
					return err
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(humioViewToken).ToNot(BeNil())
			Expect(humioViewToken.Id).ToNot(BeEmpty())
			Expect(k8sViewToken.Status.HumioID).To(Equal(humioViewToken.Id))
			Expect(k8sViewToken.Spec.ExpiresAt).To(Equal(specViewToken.ExpiresAt))
			Expect(k8sViewToken.Spec.ExpiresAt.UnixMilli()).To(Equal(*humioViewToken.ExpireAt))
		})

		It("should create the k8s HumioViewToken associated secret", func() {
			Expect(k8sClient.Create(ctx, crViewToken)).To(Succeed())
			k8sViewToken = &humiov1alpha1.HumioViewToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, k8sViewToken)
				return k8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))

			secretKey := types.NamespacedName{
				Name:      k8sViewToken.Spec.TokenSecretName,
				Namespace: clusterKey.Namespace,
			}
			secret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, secret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldID))
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldName))
			Expect(secret.Data).To(HaveKey(controller.TokenFieldName))
			Expect(string(secret.Data[controller.ResourceFieldID])).To(Equal(k8sViewToken.Status.HumioID))
			Expect(string(secret.Data[controller.ResourceFieldName])).To(Equal(k8sViewToken.Spec.Name))
			tokenParts := strings.Split(string(secret.Data[controller.TokenFieldName]), "~")
			Expect(tokenParts[0]).To(Equal(k8sViewToken.Status.HumioID))
			Expect(secret.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))
		})

		It("should ConfigError on missing view", func() {
			crViewToken.Spec.ViewNames = append(crViewToken.Spec.ViewNames, "missing-view")
			Expect(k8sClient.Create(ctx, crViewToken)).To(Succeed())
			k8sViewToken = &humiov1alpha1.HumioViewToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, k8sViewToken)
				return k8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenConfigError))
		})

		It("should ConfigError on bad IPFilterName", func() {
			crViewToken.Spec.IPFilterName = "missing-ipfilter-viewtoken"
			Expect(k8sClient.Create(ctx, crViewToken)).To(Succeed())
			k8sViewToken = &humiov1alpha1.HumioViewToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, k8sViewToken)
				return k8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenConfigError))
		})
	})

	Context("When updating a HumioViewToken CR instance", func() {
		BeforeEach(func() {
			permissionNames := []string{"ChangeFiles"}
			expireAt := metav1.NewTime(helpers.GetCurrentDay().AddDate(0, 0, 10))

			keyViewToken = types.NamespacedName{
				Name:      "viewtoken-cr",
				Namespace: clusterKey.Namespace,
			}
			specViewToken = humiov1alpha1.HumioViewTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "viewtoken",
					IPFilterName:       k8sIPFilter.Spec.Name,
					Permissions:        permissionNames,
					TokenSecretName:    "viewtoken-secret",
					ExpiresAt:          &expireAt,
				},
				ViewNames: []string{k8sView.Spec.Name},
			}
			crViewToken = &humiov1alpha1.HumioViewToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyViewToken.Name,
					Namespace: keyViewToken.Namespace,
				},
				Spec: specViewToken,
			}
			Expect(k8sClient.Create(ctx, crViewToken)).To(Succeed())
			k8sViewToken = &humiov1alpha1.HumioViewToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, k8sViewToken)
				return k8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, k8sViewToken)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyViewToken, k8sViewToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should allow permissions update", func() {
			updatedPermissions := []string{"ReadAccess"}
			k8sViewToken.Spec.Permissions = updatedPermissions
			// update
			Eventually(func() error {
				return k8sClient.Update(ctx, k8sViewToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// fetch humio token
			var humioViewToken *humiographql.ViewTokenDetailsViewPermissionsToken
			Eventually(func() []string {
				humioViewToken, err = humioClient.GetViewToken(ctx, humioHttpClient, k8sViewToken)
				return humio.FixPermissions(humioViewToken.Permissions)
			}, testTimeout, suite.TestInterval).Should(ContainElements(humio.FixPermissions(updatedPermissions)))
		})

		It("should fail with immutable error on ViewNames change attempt", func() {
			k8sViewToken.Spec.ViewNames = append(k8sViewToken.Spec.ViewNames, "missing-view")
			Eventually(func() error {
				return k8sClient.Update(ctx, k8sViewToken)
			}, testTimeout, suite.TestInterval).Should(MatchError(ContainSubstring("Value is immutable")))
		})

		It("should fail with immutable error on IPFilterName change attempt", func() {
			k8sViewToken.Spec.IPFilterName = "missing-ipfilter-viewtoken"
			Eventually(func() error {
				return k8sClient.Update(ctx, k8sViewToken)
			}, testTimeout, suite.TestInterval).Should(MatchError(ContainSubstring("Value is immutable")))
		})

		It("should transition Status.State Exists->ConfigError->Exists on permissions updates", func() {
			// initial state
			localk8sViewToken := &humiov1alpha1.HumioViewToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, localk8sViewToken)
				return localk8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
			// update with bad permissions
			updatedPermissions := []string{"bad-permission"}
			localk8sViewToken.Spec.Permissions = updatedPermissions
			Eventually(func() error {
				return k8sClient.Update(ctx, localk8sViewToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// check state
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, localk8sViewToken)
				return localk8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenConfigError))
			// revert
			updatedPermissions = []string{"DeleteDataSources"}
			localk8sViewToken.Spec.Permissions = updatedPermissions
			// update
			Eventually(func() error {
				return k8sClient.Update(ctx, localk8sViewToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, localk8sViewToken)
				return localk8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		It("should recreate k8s secret if missing", func() {
			// initial state
			localk8sViewToken := &humiov1alpha1.HumioViewToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, localk8sViewToken)
				return localk8sViewToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
			// check current secret
			secretKey := types.NamespacedName{
				Name:      localk8sViewToken.Spec.TokenSecretName,
				Namespace: clusterKey.Namespace,
			}
			secret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, secret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldID))
			Expect(string(secret.Data[controller.ResourceFieldID])).To(Equal(localk8sViewToken.Status.HumioID))
			oldTokenId := string(secret.Data[controller.ResourceFieldID])
			// remove finalizer from secret and delete
			controllerutil.RemoveFinalizer(secret, controller.HumioFinalizer)
			Expect(k8sClient.Update(ctx, secret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).Should(Succeed())
			// check new secret was created
			newSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, newSecret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// secret field for HumioID should be different now
			Expect(string(newSecret.Data[controller.ResourceFieldID])).ToNot(Equal(oldTokenId))
			// refetch HumioViewToken check new HumioID
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyViewToken, localk8sViewToken)
				return localk8sViewToken.Status.HumioID
			}, testTimeout, suite.TestInterval).Should(Equal(string(newSecret.Data[controller.ResourceFieldID])))
		})
	})
})

var _ = Describe("Humio SystemToken Controller", Label("envtest", "dummy", "real"), func() {
	var (
		ctx             context.Context
		cancel          context.CancelFunc
		humioHttpClient *api.Client
		k8sIPFilter     *humiov1alpha1.HumioIPFilter
		crSystemToken   *humiov1alpha1.HumioSystemToken
		keySystemToken  types.NamespacedName
		keyIPFilter     types.NamespacedName
		specSystemToken humiov1alpha1.HumioSystemTokenSpec
		k8sSystemToken  *humiov1alpha1.HumioSystemToken
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		humioClient.ClearHumioClientConnections(testRepoName)
		// dependencies
		humioHttpClient = humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})

		// enable token permissions updates
		_ = humioClient.EnableTokenUpdatePermissionsForTests(ctx, humioHttpClient)

		// create IPFilter dependency
		keyIPFilter = types.NamespacedName{
			Name:      "systemtoken-filter-cr",
			Namespace: clusterKey.Namespace,
		}
		specIPFilter := humiov1alpha1.HumioIPFilterSpec{
			ManagedClusterName: clusterKey.Name,
			Name:               "systemtoken-filter",
			IPFilter: []humiov1alpha1.FirewallRule{
				{Action: "allow", Address: "127.0.0.1"},
				{Action: "allow", Address: "10.0.0.0/8"},
			},
		}
		crIPFilter := &humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{
				Name:      keyIPFilter.Name,
				Namespace: keyIPFilter.Namespace,
			},
			Spec: specIPFilter,
		}
		k8sIPFilter = &humiov1alpha1.HumioIPFilter{}
		suite.UsingClusterBy(clusterKey.Name, "HumioIPFilter: Creating the IPFilter successfully")
		Expect(k8sClient.Create(ctx, crIPFilter)).Should(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, keyIPFilter, k8sIPFilter)
			return k8sIPFilter.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioIPFilterStateExists))
	})

	AfterEach(func() {
		Expect(k8sClient.Delete(ctx, k8sIPFilter)).Should(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, keyIPFilter, k8sIPFilter)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())
		cancel()
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	Context("When creating a HumioSystemToken CR instance with valid input", func() {
		BeforeEach(func() {
			permissionNames := []string{"ManageOrganizations"}
			expireAt := metav1.NewTime(helpers.GetCurrentDay().AddDate(0, 0, 10))

			keySystemToken = types.NamespacedName{
				Name:      "systemtoken-cr",
				Namespace: clusterKey.Namespace,
			}
			specSystemToken = humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "systemtoken",
					IPFilterName:       k8sIPFilter.Spec.Name,
					Permissions:        permissionNames,
					TokenSecretName:    "systemtoken-secret",
					ExpiresAt:          &expireAt,
				},
			}
			crSystemToken = &humiov1alpha1.HumioSystemToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keySystemToken.Name,
					Namespace: keySystemToken.Namespace,
				},
				Spec: specSystemToken,
			}
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, k8sSystemToken)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keySystemToken, k8sSystemToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should create the k8s HumioSystemToken cr", func() {
			Expect(k8sClient.Create(ctx, crSystemToken)).To(Succeed())
			k8sSystemToken = &humiov1alpha1.HumioSystemToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, k8sSystemToken)
				return k8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		It("should create the humio system token", func() {
			Expect(k8sClient.Create(ctx, crSystemToken)).To(Succeed())
			k8sSystemToken = &humiov1alpha1.HumioSystemToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, k8sSystemToken)
				return k8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))

			var humioSystemToken *humiographql.SystemTokenDetailsSystemPermissionsToken
			Eventually(func() error {
				humioSystemToken, err = humioClient.GetSystemToken(ctx, humioHttpClient, k8sSystemToken)
				if err != nil {
					return err
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(humioSystemToken).ToNot(BeNil())
			Expect(humioSystemToken.Id).ToNot(BeEmpty())
			Expect(k8sSystemToken.Status.HumioID).To(Equal(humioSystemToken.Id))
			Expect(k8sSystemToken.Spec.ExpiresAt).To(Equal(specSystemToken.ExpiresAt))
			Expect(k8sSystemToken.Spec.ExpiresAt.UnixMilli()).To(Equal(*humioSystemToken.ExpireAt))
		})

		It("should create the k8s HumioSystemToken associated secret", func() {
			Expect(k8sClient.Create(ctx, crSystemToken)).To(Succeed())
			k8sSystemToken = &humiov1alpha1.HumioSystemToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, k8sSystemToken)
				return k8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))

			secretKey := types.NamespacedName{
				Name:      k8sSystemToken.Spec.TokenSecretName,
				Namespace: clusterKey.Namespace,
			}
			secret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, secret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldID))
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldName))
			Expect(secret.Data).To(HaveKey(controller.TokenFieldName))
			Expect(string(secret.Data[controller.ResourceFieldID])).To(Equal(k8sSystemToken.Status.HumioID))
			Expect(string(secret.Data[controller.ResourceFieldName])).To(Equal(k8sSystemToken.Spec.Name))
			tokenParts := strings.Split(string(secret.Data[controller.TokenFieldName]), "~")
			Expect(tokenParts[0]).To(Equal(k8sSystemToken.Status.HumioID))
			Expect(secret.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))
		})

		It("should ConfigError on bad IPFilterName", func() {
			crSystemToken.Spec.IPFilterName = "missing-ipfilter-systemtoken"
			Expect(k8sClient.Create(ctx, crSystemToken)).To(Succeed())
			k8sSystemToken = &humiov1alpha1.HumioSystemToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, k8sSystemToken)
				return k8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenConfigError))
		})
	})

	Context("When updating a HumioSystemToken CR instance", func() {
		BeforeEach(func() {
			permissionNames := []string{"PatchGlobal"}
			expireAt := metav1.NewTime(helpers.GetCurrentDay().AddDate(0, 0, 10))

			keySystemToken = types.NamespacedName{
				Name:      "systemtoken-cr",
				Namespace: clusterKey.Namespace,
			}
			specSystemToken = humiov1alpha1.HumioSystemTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "systemtoken",
					IPFilterName:       k8sIPFilter.Spec.Name,
					Permissions:        permissionNames,
					TokenSecretName:    "systemtoken-secret",
					ExpiresAt:          &expireAt,
				},
			}
			crSystemToken = &humiov1alpha1.HumioSystemToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keySystemToken.Name,
					Namespace: keySystemToken.Namespace,
				},
				Spec: specSystemToken,
			}
			Expect(k8sClient.Create(ctx, crSystemToken)).To(Succeed())
			k8sSystemToken = &humiov1alpha1.HumioSystemToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, k8sSystemToken)
				return k8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, k8sSystemToken)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keySystemToken, k8sSystemToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should allow permissions update", func() {
			updatedPermissions := []string{"ReadHealthCheck"}
			k8sSystemToken.Spec.Permissions = updatedPermissions
			// update
			Eventually(func() error {
				return k8sClient.Update(ctx, k8sSystemToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// fetch humio token
			var humioSystemToken *humiographql.SystemTokenDetailsSystemPermissionsToken
			Eventually(func() []string {
				humioSystemToken, err = humioClient.GetSystemToken(ctx, humioHttpClient, k8sSystemToken)
				return humio.FixPermissions(humioSystemToken.Permissions)
			}, testTimeout, suite.TestInterval).Should(ContainElements(updatedPermissions))
		})

		It("should fail with immutable error on IPFilterName change attempt", func() {
			k8sSystemToken.Spec.IPFilterName = "missing-ipfilte-viewtoken"
			Eventually(func() error {
				return k8sClient.Update(ctx, k8sSystemToken)
			}, testTimeout, suite.TestInterval).Should(MatchError(ContainSubstring("Value is immutable")))
		})

		It("should transition Status.State Exists->ConfigError->Exists on permissions updates", func() {
			// initial state
			localk8sSystemToken := &humiov1alpha1.HumioSystemToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, localk8sSystemToken)
				return localk8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
			// update with bad permissions
			updatedPermissions := []string{"bad-permission"}
			localk8sSystemToken.Spec.Permissions = updatedPermissions
			Eventually(func() error {
				return k8sClient.Update(ctx, localk8sSystemToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// check state
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, localk8sSystemToken)
				return localk8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenConfigError))
			// revert
			updatedPermissions = []string{"ListSubdomains"}
			localk8sSystemToken.Spec.Permissions = updatedPermissions
			// update
			Eventually(func() error {
				return k8sClient.Update(ctx, localk8sSystemToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, localk8sSystemToken)
				return localk8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		It("should recreate k8s secret if missing", func() {
			// initial state
			localk8sSystemToken := &humiov1alpha1.HumioSystemToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, localk8sSystemToken)
				return localk8sSystemToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
			// check current secret
			secretKey := types.NamespacedName{
				Name:      localk8sSystemToken.Spec.TokenSecretName,
				Namespace: clusterKey.Namespace,
			}
			secret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, secret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldID))
			Expect(string(secret.Data[controller.ResourceFieldID])).To(Equal(localk8sSystemToken.Status.HumioID))
			oldTokenId := string(secret.Data[controller.ResourceFieldID])
			// remove finalizer from secret and delete
			controllerutil.RemoveFinalizer(secret, controller.HumioFinalizer)
			Expect(k8sClient.Update(ctx, secret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).Should(Succeed())
			// check new secret was created
			newSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, newSecret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// secret field for HumioID should be different now
			Expect(string(newSecret.Data[controller.ResourceFieldID])).ToNot(Equal(oldTokenId))
			// refetch HumioViewToken check new HumioID
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keySystemToken, localk8sSystemToken)
				return localk8sSystemToken.Status.HumioID
			}, testTimeout, suite.TestInterval).Should(Equal(string(newSecret.Data[controller.ResourceFieldID])))
		})
	})
})

var _ = Describe("Humio OrganizationToken Controller", Label("envtest", "dummy", "real"), func() {
	var (
		ctx             context.Context
		cancel          context.CancelFunc
		humioHttpClient *api.Client
		k8sIPFilter     *humiov1alpha1.HumioIPFilter
		crOrgToken      *humiov1alpha1.HumioOrganizationToken
		keyOrgToken     types.NamespacedName
		keyIPFilter     types.NamespacedName
		specOrgToken    humiov1alpha1.HumioOrganizationTokenSpec
		k8sOrgToken     *humiov1alpha1.HumioOrganizationToken
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		humioClient.ClearHumioClientConnections(testRepoName)
		// dependencies
		humioHttpClient = humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})

		// enable token permissions updates
		_ = humioClient.EnableTokenUpdatePermissionsForTests(ctx, humioHttpClient)

		// create IPFilter dependency
		keyIPFilter = types.NamespacedName{
			Name:      "systemtoken-filter-cr",
			Namespace: clusterKey.Namespace,
		}
		specIPFilter := humiov1alpha1.HumioIPFilterSpec{
			ManagedClusterName: clusterKey.Name,
			Name:               "systemtoken-filter",
			IPFilter: []humiov1alpha1.FirewallRule{
				{Action: "allow", Address: "127.0.0.1"},
				{Action: "allow", Address: "10.0.0.0/8"},
			},
		}
		crIPFilter := &humiov1alpha1.HumioIPFilter{
			ObjectMeta: metav1.ObjectMeta{
				Name:      keyIPFilter.Name,
				Namespace: keyIPFilter.Namespace,
			},
			Spec: specIPFilter,
		}
		k8sIPFilter = &humiov1alpha1.HumioIPFilter{}
		suite.UsingClusterBy(clusterKey.Name, "HumioIPFilter: Creating the IPFilter successfully")
		Expect(k8sClient.Create(ctx, crIPFilter)).Should(Succeed())
		Eventually(func() string {
			_ = k8sClient.Get(ctx, keyIPFilter, k8sIPFilter)
			return k8sIPFilter.Status.State
		}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioIPFilterStateExists))
	})

	AfterEach(func() {
		Expect(k8sClient.Delete(ctx, k8sIPFilter)).Should(Succeed())
		Eventually(func() bool {
			err := k8sClient.Get(ctx, keyIPFilter, k8sIPFilter)
			return k8serrors.IsNotFound(err)
		}, testTimeout, suite.TestInterval).Should(BeTrue())
		cancel()
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	Context("When creating a HumioOrganizationToken CR instance with valid input", func() {
		BeforeEach(func() {
			permissionNames := []string{"BlockQueries"}
			expireAt := metav1.NewTime(helpers.GetCurrentDay().AddDate(0, 0, 10))

			keyOrgToken = types.NamespacedName{
				Name:      "orgtoken-cr",
				Namespace: clusterKey.Namespace,
			}
			specOrgToken = humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "orgtoken",
					IPFilterName:       k8sIPFilter.Spec.Name,
					Permissions:        permissionNames,
					TokenSecretName:    "orgtoken-secret",
					ExpiresAt:          &expireAt,
				},
			}
			crOrgToken = &humiov1alpha1.HumioOrganizationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyOrgToken.Name,
					Namespace: keyOrgToken.Namespace,
				},
				Spec: specOrgToken,
			}
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, k8sOrgToken)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyOrgToken, k8sOrgToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should create the k8s HumioOrganizationToken cr", func() {
			Expect(k8sClient.Create(ctx, crOrgToken)).To(Succeed())
			k8sOrgToken = &humiov1alpha1.HumioOrganizationToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, k8sOrgToken)
				return k8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		It("should create the humio organization token", func() {
			Expect(k8sClient.Create(ctx, crOrgToken)).To(Succeed())
			k8sOrgToken = &humiov1alpha1.HumioOrganizationToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, k8sOrgToken)
				return k8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))

			var humioOrgToken *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken
			Eventually(func() error {
				humioOrgToken, err = humioClient.GetOrganizationToken(ctx, humioHttpClient, k8sOrgToken)
				if err != nil {
					return err
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(humioOrgToken).ToNot(BeNil())
			Expect(humioOrgToken.Id).ToNot(BeEmpty())
			Expect(k8sOrgToken.Status.HumioID).To(Equal(humioOrgToken.Id))
			Expect(k8sOrgToken.Spec.ExpiresAt).To(Equal(specOrgToken.ExpiresAt))
			Expect(k8sOrgToken.Spec.ExpiresAt.UnixMilli()).To(Equal(*humioOrgToken.ExpireAt))
		})

		It("should create the k8s HumioOrganizationToken associated secret", func() {
			Expect(k8sClient.Create(ctx, crOrgToken)).To(Succeed())
			k8sOrgToken = &humiov1alpha1.HumioOrganizationToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, k8sOrgToken)
				return k8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))

			secretKey := types.NamespacedName{
				Name:      k8sOrgToken.Spec.TokenSecretName,
				Namespace: clusterKey.Namespace,
			}
			secret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, secret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldID))
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldName))
			Expect(secret.Data).To(HaveKey(controller.TokenFieldName))
			Expect(string(secret.Data[controller.ResourceFieldID])).To(Equal(k8sOrgToken.Status.HumioID))
			Expect(string(secret.Data[controller.ResourceFieldName])).To(Equal(k8sOrgToken.Spec.Name))
			tokenParts := strings.Split(string(secret.Data[controller.TokenFieldName]), "~")
			Expect(tokenParts[0]).To(Equal(k8sOrgToken.Status.HumioID))
			Expect(secret.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))
		})

		It("should ConfigError on bad IPFilterName", func() {
			crOrgToken.Spec.IPFilterName = "missing-ipfilter-orgtoken"
			Expect(k8sClient.Create(ctx, crOrgToken)).To(Succeed())
			k8sOrgToken = &humiov1alpha1.HumioOrganizationToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, k8sOrgToken)
				return k8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenConfigError))
		})
	})

	Context("When updating a HumioOrganizationToken CR instance", func() {
		BeforeEach(func() {
			permissionNames := []string{"DeleteAllViews"}
			expireAt := metav1.NewTime(helpers.GetCurrentDay().AddDate(0, 0, 10))

			keyOrgToken = types.NamespacedName{
				Name:      "orgtoken-cr",
				Namespace: clusterKey.Namespace,
			}
			specOrgToken = humiov1alpha1.HumioOrganizationTokenSpec{
				HumioTokenSpec: humiov1alpha1.HumioTokenSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "orgtoken",
					IPFilterName:       k8sIPFilter.Spec.Name,
					Permissions:        permissionNames,
					TokenSecretName:    "orgtoken-secret",
					ExpiresAt:          &expireAt,
				},
			}
			crOrgToken = &humiov1alpha1.HumioOrganizationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyOrgToken.Name,
					Namespace: keyOrgToken.Namespace,
				},
				Spec: specOrgToken,
			}
			Expect(k8sClient.Create(ctx, crOrgToken)).To(Succeed())
			k8sOrgToken = &humiov1alpha1.HumioOrganizationToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, k8sOrgToken)
				return k8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, k8sOrgToken)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, keyOrgToken, k8sOrgToken)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should allow permissions update", func() {
			updatedPermissions := []string{"ChangeOrganizationSettings"}
			k8sOrgToken.Spec.Permissions = updatedPermissions
			// update
			Eventually(func() error {
				return k8sClient.Update(ctx, k8sOrgToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// fetch humio token
			var humioOrgToken *humiographql.OrganizationTokenDetailsOrganizationPermissionsToken
			Eventually(func() []string {
				humioOrgToken, err = humioClient.GetOrganizationToken(ctx, humioHttpClient, k8sOrgToken)
				return humioOrgToken.Permissions
			}, testTimeout, suite.TestInterval).Should(ContainElements(updatedPermissions))
		})

		It("should fail with immutable error on IPFilterName change attempt", func() {
			k8sOrgToken.Spec.IPFilterName = "missing-ipfilter-orgtoken"
			Eventually(func() error {
				return k8sClient.Update(ctx, k8sOrgToken)
			}, testTimeout, suite.TestInterval).Should(MatchError(ContainSubstring("Value is immutable")))
		})

		It("should transition Status.State Exists->ConfigError->Exists on permissions updates", func() {
			// initial state
			localk8sOrgToken := &humiov1alpha1.HumioOrganizationToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, localk8sOrgToken)
				return localk8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
			// update with bad permissions
			updatedPermissions := []string{"bad-permission"}
			localk8sOrgToken.Spec.Permissions = updatedPermissions
			Eventually(func() error {
				return k8sClient.Update(ctx, localk8sOrgToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// check state
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, localk8sOrgToken)
				return localk8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenConfigError))
			// revert
			updatedPermissions = []string{"ViewFleetManagement"}
			localk8sOrgToken.Spec.Permissions = updatedPermissions
			// update
			Eventually(func() error {
				return k8sClient.Update(ctx, localk8sOrgToken)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, localk8sOrgToken)
				return localk8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
		})

		It("should recreate k8s secret if missing", func() {
			// initial state
			localk8sOrgToken := &humiov1alpha1.HumioOrganizationToken{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, localk8sOrgToken)
				return localk8sOrgToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioTokenExists))
			// check current secret
			secretKey := types.NamespacedName{
				Name:      localk8sOrgToken.Spec.TokenSecretName,
				Namespace: clusterKey.Namespace,
			}
			secret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, secret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(secret.Data).To(HaveKey(controller.ResourceFieldID))
			Expect(string(secret.Data[controller.ResourceFieldID])).To(Equal(localk8sOrgToken.Status.HumioID))
			oldTokenId := string(secret.Data[controller.ResourceFieldID])
			// remove finalizer from secret and delete
			controllerutil.RemoveFinalizer(secret, controller.HumioFinalizer)
			Expect(k8sClient.Update(ctx, secret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).Should(Succeed())
			// check new secret was created
			newSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, secretKey, newSecret)
			}, testTimeout, suite.TestInterval).Should(Succeed())
			// secret field for HumioID should be different now
			Expect(string(newSecret.Data[controller.ResourceFieldID])).ToNot(Equal(oldTokenId))
			// refetch HumioOrganizationToken check new HumioID
			Eventually(func() string {
				_ = k8sClient.Get(ctx, keyOrgToken, localk8sOrgToken)
				return localk8sOrgToken.Status.HumioID
			}, testTimeout, suite.TestInterval).Should(Equal(string(newSecret.Data[controller.ResourceFieldID])))
		})
	})
})
