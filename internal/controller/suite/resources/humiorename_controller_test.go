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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
)

var _ = Describe("Resource Renaming", Label("envtest", "dummy", "real"), func() {
	BeforeEach(func() {
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	AfterEach(func() {
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	Context("Cascade Updates", func() {
		Context("HumioRepository Rename with Dependencies", func() {
			It("should rename repository and cascade update parser", func() {
				ctx := context.Background()
				repoKey := types.NamespacedName{
					Name:      "test-repo-cascade",
					Namespace: clusterKey.Namespace,
				}
				parserKey := types.NamespacedName{
					Name:      "test-parser-cascade",
					Namespace: clusterKey.Namespace,
				}

				// Create repository
				toCreateRepo := &humiov1alpha1.HumioRepository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      repoKey.Name,
						Namespace: repoKey.Namespace,
					},
					Spec: humiov1alpha1.HumioRepositorySpec{
						ManagedClusterName: clusterKey.Name,
						Name:               "original-repo",
						CascadeRenames:     true, // Enable cascade renaming for this test
					},
				}

				suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Creating repository")
				Expect(k8sClient.Create(ctx, toCreateRepo)).Should(Succeed())

				fetchedRepo := &humiov1alpha1.HumioRepository{}
				suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Waiting for LastSyncedName to be set")
				Eventually(func() string {
					_ = k8sClient.Get(ctx, repoKey, fetchedRepo)
					return fetchedRepo.Status.LastSyncedName
				}, testTimeout, suite.TestInterval).Should(Equal("original-repo"))

				// Create parser dependent on repository
				toCreateParser := &humiov1alpha1.HumioParser{
					ObjectMeta: metav1.ObjectMeta{
						Name:      parserKey.Name,
						Namespace: parserKey.Namespace,
					},
					Spec: humiov1alpha1.HumioParserSpec{
						ManagedClusterName: clusterKey.Name,
						Name:               "cascade-parser",
						RepositoryName:     "original-repo",
						ParserScript:       "kvParse()",
						TagFields:          []string{"@testfield"},
						TestData:           []string{"test data"},
					},
				}

				suite.UsingClusterBy(clusterKey.Name, "HumioParser: Creating dependent parser")
				Expect(k8sClient.Create(ctx, toCreateParser)).Should(Succeed())

				fetchedParser := &humiov1alpha1.HumioParser{}
				suite.UsingClusterBy(clusterKey.Name, "HumioParser: Waiting for parser to exist")
				Eventually(func() string {
					_ = k8sClient.Get(ctx, parserKey, fetchedParser)
					return fetchedParser.Status.State
				}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioParserStateExists))

				suite.UsingClusterBy(clusterKey.Name, "HumioParser: Verifying parser references original repo")
				Expect(fetchedParser.Spec.RepositoryName).To(Equal("original-repo"))

				// Rename repository
				suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Adding allow-rename annotation")
				Eventually(func() error {
					if err := k8sClient.Get(ctx, repoKey, fetchedRepo); err != nil {
						return err
					}
					if fetchedRepo.Annotations == nil {
						fetchedRepo.Annotations = make(map[string]string)
					}
					fetchedRepo.Annotations["humio.com/allow-rename"] = controller.AllowRenameAnnotationValue
					return k8sClient.Update(ctx, fetchedRepo)
				}, testTimeout, suite.TestInterval).Should(Succeed())

				suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Renaming repository")
				Eventually(func() error {
					if err := k8sClient.Get(ctx, repoKey, fetchedRepo); err != nil {
						return err
					}
					fetchedRepo.Spec.Name = "renamed-repo"
					return k8sClient.Update(ctx, fetchedRepo)
				}, testTimeout, suite.TestInterval).Should(Succeed())

				suite.UsingClusterBy(clusterKey.Name, "HumioRepository: Verifying repository renamed")
				Eventually(func() string {
					_ = k8sClient.Get(ctx, repoKey, fetchedRepo)
					return fetchedRepo.Status.LastSyncedName
				}, testTimeout, suite.TestInterval).Should(Equal("renamed-repo"))

				// Verify parser was cascade updated
				suite.UsingClusterBy(clusterKey.Name, "HumioParser: Verifying parser cascade updated")
				Eventually(func() string {
					_ = k8sClient.Get(ctx, parserKey, fetchedParser)
					return fetchedParser.Spec.RepositoryName
				}, testTimeout*2, suite.TestInterval).Should(Equal("renamed-repo"))

				suite.UsingClusterBy(clusterKey.Name, "HumioParser: Verifying cascade annotations")
				Eventually(func() bool {
					_ = k8sClient.Get(ctx, parserKey, fetchedParser)
					_, hasUpdate := fetchedParser.Annotations["humio.com/last-cascade-update"]
					_, hasReason := fetchedParser.Annotations["humio.com/cascade-reason"]
					return hasUpdate && hasReason
				}, testTimeout, suite.TestInterval).Should(BeTrue())

				suite.UsingClusterBy(clusterKey.Name, "Cleanup: Deleting resources")
				Expect(k8sClient.Delete(ctx, fetchedParser)).Should(Succeed())
				Expect(k8sClient.Delete(ctx, fetchedRepo)).Should(Succeed())
			})
		})

		Context("HumioView Rename with Dependencies", func() {
			It("should rename view and cascade update alert", func() {
				ctx := context.Background()
				viewKey := types.NamespacedName{
					Name:      "test-view-cascade",
					Namespace: clusterKey.Namespace,
				}
				alertKey := types.NamespacedName{
					Name:      "test-alert-cascade",
					Namespace: clusterKey.Namespace,
				}

				// Create view
				toCreateView := &humiov1alpha1.HumioView{
					ObjectMeta: metav1.ObjectMeta{
						Name:      viewKey.Name,
						Namespace: viewKey.Namespace,
					},
					Spec: humiov1alpha1.HumioViewSpec{
						ManagedClusterName: clusterKey.Name,
						Name:               "original-view",
						CascadeRenames:     true, // Enable cascade renaming for this test
						Connections: []humiov1alpha1.HumioViewConnection{
							{
								RepositoryName: testRepo.Spec.Name,
								Filter:         "*",
							},
						},
					},
				}

				suite.UsingClusterBy(clusterKey.Name, "HumioView: Creating view")
				Expect(k8sClient.Create(ctx, toCreateView)).Should(Succeed())

				fetchedView := &humiov1alpha1.HumioView{}
				suite.UsingClusterBy(clusterKey.Name, "HumioView: Waiting for view to exist")
				Eventually(func() string {
					_ = k8sClient.Get(ctx, viewKey, fetchedView)
					return fetchedView.Status.State
				}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioViewStateExists))

				// Create alert dependent on view
				toCreateAlert := &humiov1alpha1.HumioAlert{
					ObjectMeta: metav1.ObjectMeta{
						Name:      alertKey.Name,
						Namespace: alertKey.Namespace,
					},
					Spec: humiov1alpha1.HumioAlertSpec{
						ManagedClusterName: clusterKey.Name,
						Name:               "cascade-alert",
						ViewName:           "original-view",
						AllowDataDeletion:  true,
						Query: humiov1alpha1.HumioQuery{
							QueryString: "#type=test | count()",
							Start:       "5m",
						},
						ThrottleTimeMillis: 60000,
						Actions:            []string{},
						Labels:             []string{},
					},
				}

				suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Creating dependent alert")
				Expect(k8sClient.Create(ctx, toCreateAlert)).Should(Succeed())

				fetchedAlert := &humiov1alpha1.HumioAlert{}
				suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Waiting for alert to exist")
				Eventually(func() string {
					_ = k8sClient.Get(ctx, alertKey, fetchedAlert)
					return fetchedAlert.Status.State
				}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioAlertStateExists))

				suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Verifying alert references original view")
				Expect(fetchedAlert.Spec.ViewName).To(Equal("original-view"))

				// Rename view
				suite.UsingClusterBy(clusterKey.Name, "HumioView: Adding allow-rename annotation")
				Eventually(func() error {
					if err := k8sClient.Get(ctx, viewKey, fetchedView); err != nil {
						return err
					}
					if fetchedView.Annotations == nil {
						fetchedView.Annotations = make(map[string]string)
					}
					fetchedView.Annotations["humio.com/allow-rename"] = controller.AllowRenameAnnotationValue
					return k8sClient.Update(ctx, fetchedView)
				}, testTimeout, suite.TestInterval).Should(Succeed())

				suite.UsingClusterBy(clusterKey.Name, "HumioView: Renaming view")
				Eventually(func() error {
					if err := k8sClient.Get(ctx, viewKey, fetchedView); err != nil {
						return err
					}
					fetchedView.Spec.Name = "renamed-view"
					return k8sClient.Update(ctx, fetchedView)
				}, testTimeout, suite.TestInterval).Should(Succeed())

				suite.UsingClusterBy(clusterKey.Name, "HumioView: Verifying view renamed")
				Eventually(func() string {
					_ = k8sClient.Get(ctx, viewKey, fetchedView)
					return fetchedView.Status.LastSyncedName
				}, testTimeout, suite.TestInterval).Should(Equal("renamed-view"))

				// Verify alert was cascade updated
				suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Verifying alert cascade updated")
				Eventually(func() string {
					_ = k8sClient.Get(ctx, alertKey, fetchedAlert)
					return fetchedAlert.Spec.ViewName
				}, testTimeout*2, suite.TestInterval).Should(Equal("renamed-view"))

				suite.UsingClusterBy(clusterKey.Name, "HumioAlert: Verifying cascade annotations")
				Eventually(func() bool {
					_ = k8sClient.Get(ctx, alertKey, fetchedAlert)
					_, hasUpdate := fetchedAlert.Annotations["humio.com/last-cascade-update"]
					_, hasReason := fetchedAlert.Annotations["humio.com/cascade-reason"]
					return hasUpdate && hasReason
				}, testTimeout, suite.TestInterval).Should(BeTrue())

				suite.UsingClusterBy(clusterKey.Name, "Cleanup: Deleting resources")
				Expect(k8sClient.Delete(ctx, fetchedAlert)).Should(Succeed())
				Expect(k8sClient.Delete(ctx, fetchedView)).Should(Succeed())
			})
		})
	})

	Context("HumioRepository - Force Finalize", Label("envtest", "dummy", "real"), func() {
		It("should force-finalize when annotation present", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "humiorepository-force-finalize",
				Namespace: clusterKey.Namespace,
			}

			toCreateRepository := &humiov1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioRepositorySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "repository-force-finalize",
					AllowDataDeletion:  false, // Block deletion
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioRepository Force-Finalize: Creating repository with allowDataDeletion=false")
			Expect(k8sClient.Create(ctx, toCreateRepository)).Should(Succeed())

			fetchedRepository := &humiov1alpha1.HumioRepository{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedRepository)
				return fetchedRepository.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioRepositoryStateExists))

			// Verify finalizer present
			Expect(fetchedRepository.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))

			// Attempt deletion (will be blocked by allowDataDeletion=false)
			suite.UsingClusterBy(clusterKey.Name, "HumioRepository Force-Finalize: Triggering deletion (should block)")
			Expect(k8sClient.Delete(ctx, fetchedRepository)).Should(Succeed())

			// Verify resource stuck in deletion
			suite.UsingClusterBy(clusterKey.Name, "HumioRepository Force-Finalize: Verifying deletion is blocked")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedRepository)
				return err == nil && fetchedRepository.GetDeletionTimestamp() != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify finalizer still present (blocked)
			Expect(k8sClient.Get(ctx, key, fetchedRepository)).Should(Succeed())
			Expect(fetchedRepository.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))

			// Add force-finalize annotation
			suite.UsingClusterBy(clusterKey.Name, "HumioRepository Force-Finalize: Adding force-finalize annotation")
			Eventually(func() error {
				fresh := &humiov1alpha1.HumioRepository{}
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
			suite.UsingClusterBy(clusterKey.Name, "HumioRepository Force-Finalize: Verifying force-finalize removes finalizer")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedRepository)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue(), "Resource should be deleted after force-finalize")
		})
	})
})
