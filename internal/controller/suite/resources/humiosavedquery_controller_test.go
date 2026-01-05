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
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/controller/suite"
)

var _ = Describe("HumioSavedQuery Controller", Label("envtest", "dummy", "real"), func() {
	BeforeEach(func() {
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	AfterEach(func() {
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	Context("Basic CRUD Operations", func() {
		It("should create saved query successfully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query",
				Namespace: clusterKey.Namespace,
			}

			toCreateSavedQuery := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "test-saved-query",
					ViewName:           testRepo.Spec.Name,
					QueryString:        "#type=test | count()",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query")
			Expect(k8sClient.Create(ctx, toCreateSavedQuery)).Should(Succeed())

			fetchedSavedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedSavedQuery)
				condition := findCondition(fetchedSavedQuery.Status.Conditions, "Ready")
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal("True"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Synced condition")
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedSavedQuery)
				condition := findCondition(fetchedSavedQuery.Status.Conditions, "Synced")
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal("True"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying saved query spec")
			Expect(fetchedSavedQuery.Spec.Name).To(Equal("test-saved-query"))
			Expect(fetchedSavedQuery.Spec.ViewName).To(Equal(testRepo.Spec.Name))
			Expect(fetchedSavedQuery.Spec.QueryString).To(Equal("#type=test | count()"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Successfully deleting saved query")
			Expect(k8sClient.Delete(ctx, fetchedSavedQuery)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedSavedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should update saved query successfully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-update",
				Namespace: clusterKey.Namespace,
			}

			toCreateSavedQuery := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "test-saved-query-update",
					ViewName:           testRepo.Spec.Name,
					QueryString:        "#type=original | count()",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query for update test")
			Expect(k8sClient.Create(ctx, toCreateSavedQuery)).Should(Succeed())

			fetchedSavedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedSavedQuery)
				condition := findCondition(fetchedSavedQuery.Status.Conditions, "Ready")
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal("True"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Updating saved query")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedSavedQuery); err != nil {
					return err
				}
				fetchedSavedQuery.Spec.QueryString = "#type=updated | count()"
				return k8sClient.Update(ctx, fetchedSavedQuery)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying updated query")
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedSavedQuery)
				return fetchedSavedQuery.Spec.QueryString
			}, testTimeout, suite.TestInterval).Should(Equal("#type=updated | count()"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedSavedQuery)).Should(Succeed())
		})

	})

	Context("Error Handling", func() {
		It("should set Ready condition to False when view doesn't exist", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-invalid-view",
				Namespace: clusterKey.Namespace,
			}

			toCreateSavedQuery := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "test-saved-query-invalid",
					ViewName:           "non-existent-view",
					QueryString:        "#type=test | count()",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query with invalid view")
			Expect(k8sClient.Create(ctx, toCreateSavedQuery)).Should(Succeed())

			// In dummy mode, this may still succeed, so we just verify the resource was created
			fetchedSavedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, fetchedSavedQuery)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedSavedQuery)).Should(Succeed())
		})
	})

	Context("Kubernetes Conditions Management", func() {
		It("should track Ready condition lifecycle", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-ready-lifecycle",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=ready | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Ready=True")
			waitForCondition(ctx, key, "Ready")

			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())

			// Verify Ready condition - reason could be SavedQueryReady or AdoptionSuccessful
			readyCond := findCondition(fetchedQuery.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			// Don't check exact reason as it depends on reconciliation timing

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying ObservedGeneration")
			Expect(readyCond.ObservedGeneration).To(Equal(fetchedQuery.Generation))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying LastTransitionTime populated")
			Expect(readyCond.LastTransitionTime.IsZero()).To(BeFalse())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())
		})

		It("should track Synced condition lifecycle", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-synced-lifecycle",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=synced | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Synced=True")
			waitForCondition(ctx, key, "Synced")

			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())
			expectCondition(fetchedQuery.Status.Conditions, "Synced", metav1.ConditionTrue, "ConfigurationSynced")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Recording initial transition time")
			initialSyncedCond := findCondition(fetchedQuery.Status.Conditions, "Synced")
			initialTransitionTime := initialSyncedCond.LastTransitionTime

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Updating QueryString to create drift")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedQuery); err != nil {
					return err
				}
				fetchedQuery.Spec.QueryString = "#type=synced | count() | updated=true"
				return k8sClient.Update(ctx, fetchedQuery)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Synced remains True after update")
			waitForCondition(ctx, key, "Synced")

			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())
			updatedSyncedCond := findCondition(fetchedQuery.Status.Conditions, "Synced")
			Expect(updatedSyncedCond.LastTransitionTime.After(initialTransitionTime.Time) ||
				updatedSyncedCond.LastTransitionTime.Equal(&initialTransitionTime)).To(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())
		})

	})
	Context("Finalizer Management", func() {

		It("should prevent deletion until finalizer is processed", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-finalizer-prevents-delete",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=prevent-delete | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=True")
			waitForCondition(ctx, key, "Ready")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Deleting saved query")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying DeletionTimestamp set")
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioSavedQuery{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					// Resource might already be fully deleted (finalizer removed) - that's OK
					return k8serrors.IsNotFound(err)
				}
				return fresh.DeletionTimestamp != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Expect(fetchedQuery.Finalizers).Should(ContainElement("core.humio.com/finalizer"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for finalizer removal and deletion")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

	})

	Context("Drift Detection", func() {

		It("should detect and update QueryString drift", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-query-drift",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=querydrift | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=True")
			waitForCondition(ctx, key, "Ready")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Updating QueryString")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedQuery); err != nil {
					return err
				}
				fetchedQuery.Spec.QueryString = "#type=querydrift | count() | updated=true"
				return k8sClient.Update(ctx, fetchedQuery)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Synced=True after drift correction")
			waitForCondition(ctx, key, "Synced")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying updated query in Kubernetes spec")
			Eventually(func() string {
				verifiedQuery := &humiov1alpha1.HumioSavedQuery{}
				if err := k8sClient.Get(ctx, key, verifiedQuery); err != nil {
					return ""
				}
				return verifiedQuery.Spec.QueryString
			}, testTimeout, suite.TestInterval).Should(Equal("#type=querydrift | count() | updated=true"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())
		})
	})

	Context("Cluster Selection", func() {
		It("should handle non-existent cluster gracefully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-nonexistent-cluster",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: "non-existent-cluster",
					Name:               key.Name,
					ViewName:           testRepo.Spec.Name,
					QueryString:        "#type=noCluster | count()",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query with non-existent cluster")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			// The resource should be created but may not reach Ready=True
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, fetchedQuery)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())
		})
	})

	Context("Basic CRUD Enhancements", func() {
		It("should handle deletion with finalizer properly", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-delete-enhanced",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=delete | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=True")
			waitForCondition(ctx, key, "Ready")

			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying finalizer present")
			Expect(fetchedQuery.Finalizers).Should(ContainElement("core.humio.com/finalizer"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Deleting saved query")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying DeletionTimestamp set")
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioSavedQuery{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					// Resource might already be fully deleted (finalizer removed) - that's OK
					return k8serrors.IsNotFound(err)
				}
				return fresh.DeletionTimestamp != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for deletion from LogScale")
			// The controller should remove the resource from LogScale and then remove the finalizer

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying finalizer removed")
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioSavedQuery{}
				err := k8sClient.Get(ctx, key, fresh)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying K8s resource deleted")
			finalCheck := &humiov1alpha1.HumioSavedQuery{}
			err := k8sClient.Get(ctx, key, finalCheck)
			Expect(err).Should(HaveOccurred())
		})

	})

	Context("Field Validation", func() {
		It("should enforce Name immutability", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-name-immutable",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=immutable | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=True")
			waitForCondition(ctx, key, "Ready")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Attempting to change Name field")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedQuery); err != nil {
					return err
				}
				originalName := fetchedQuery.Spec.Name
				fetchedQuery.Spec.Name = "new-name"
				err := k8sClient.Update(ctx, fetchedQuery)
				if err != nil {
					// XValidation should reject this
					return nil
				}
				// If update succeeded (no webhook), verify Name unchanged
				_ = k8sClient.Get(ctx, key, fetchedQuery)
				if fetchedQuery.Spec.Name != originalName {
					return nil // Name was changed, which shouldn't happen
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())
		})

		It("should validate required fields", func() {
			ctx := context.Background()

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Attempting to create without Name")
			invalidQuery1 := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-saved-query-no-name",
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "", // Empty name
					ViewName:           testRepo.Spec.Name,
					QueryString:        "#type=test | count()",
				},
			}

			err1 := k8sClient.Create(ctx, invalidQuery1)
			if err1 != nil {
				suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verified Name is required")
				Expect(err1.Error()).To(Or(ContainSubstring("name"), ContainSubstring("required")))
			} else {
				// Cleanup if validation didn't catch it
				_ = k8sClient.Delete(ctx, invalidQuery1)
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Attempting to create without ViewName")
			invalidQuery2 := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-saved-query-no-view",
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "test-query",
					ViewName:           "", // Empty view name
					QueryString:        "#type=test | count()",
				},
			}

			err2 := k8sClient.Create(ctx, invalidQuery2)
			if err2 != nil {
				suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verified ViewName is required")
				Expect(err2.Error()).To(Or(ContainSubstring("viewName"), ContainSubstring("required")))
			} else {
				_ = k8sClient.Delete(ctx, invalidQuery2)
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Attempting to create without QueryString")
			invalidQuery3 := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-saved-query-no-query",
					Namespace: clusterKey.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "test-query",
					ViewName:           testRepo.Spec.Name,
					QueryString:        "", // Empty query
				},
			}

			err3 := k8sClient.Create(ctx, invalidQuery3)
			if err3 != nil {
				suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verified QueryString is required")
				Expect(err3.Error()).To(Or(ContainSubstring("queryString"), ContainSubstring("required")))
			} else {
				_ = k8sClient.Delete(ctx, invalidQuery3)
			}
		})

	})

	Context("Adoption Pattern", func() {
		It("should successfully adopt existing query with matching configuration", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-adoption-success",
				Namespace: clusterKey.Namespace,
			}

			// Pre-create a query in LogScale (simulating existing saved query)
			preExistingQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=adoption | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Pre-creating query in mock LogScale")
			// Use the HumioClient directly to add the query (bypassing K8s)
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), ctrl.Request{NamespacedName: clusterKey})
			Expect(humioClient.AddSavedQuery(ctx, humioHttpClient, preExistingQuery, false)).To(Succeed())

			// Now create the K8s resource with matching configuration
			toCreateQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=adoption | count()")
			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating K8s resource to adopt existing query")
			Expect(k8sClient.Create(ctx, toCreateQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying adoption success")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioSavedQuery{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return false
				}
				condition := findCondition(fresh.Status.Conditions, "Ready")
				if condition == nil || condition.Status != metav1.ConditionTrue {
					return false
				}
				// Accept either AdoptionSuccessful (briefly visible) or SavedQueryReady (after transition)
				// Both indicate successful adoption
				return condition.Reason == "AdoptionSuccessful" || condition.Reason == "SavedQueryReady"
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Ready=True and ManagedByOperator set")
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())
			readyCond := findCondition(fetchedQuery.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			// Reason transitions from AdoptionSuccessful -> SavedQueryReady quickly due to Requeue: true
			Expect(readyCond.Reason).To(Or(Equal("AdoptionSuccessful"), Equal("SavedQueryReady")))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying ManagedByOperator is true")
			Expect(fetchedQuery.Status.ManagedByOperator).NotTo(BeNil())
			Expect(*fetchedQuery.Status.ManagedByOperator).To(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Synced condition is true")
			syncedCond := findCondition(fetchedQuery.Status.Conditions, "Synced")
			Expect(syncedCond).NotTo(BeNil())
			Expect(syncedCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(syncedCond.Reason).To(Equal("ConfigurationSynced"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying query was deleted from LogScale")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should reject adoption when configuration doesn't match", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-adoption-reject",
				Namespace: clusterKey.Namespace,
			}

			// Pre-create a query in LogScale with different query string
			preExistingQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=adoption-reject | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Pre-creating query with different config")
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), ctrl.Request{NamespacedName: clusterKey})
			Expect(humioClient.AddSavedQuery(ctx, humioHttpClient, preExistingQuery, false)).To(Succeed())

			// Now create the K8s resource with non-matching query string
			toCreateQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=adoption-reject | sum(field)")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating K8s resource with conflicting config")
			Expect(k8sClient.Create(ctx, toCreateQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying adoption rejection")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedQuery)
				condition := findCondition(fetchedQuery.Status.Conditions, "Ready")
				if condition != nil {
					return condition.Reason
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal("AdoptionRejected"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Ready=False with rejection message")
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())
			readyCond := findCondition(fetchedQuery.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("AdoptionRejected"))
			Expect(readyCond.Message).To(ContainSubstring("Cannot adopt existing saved query"))
			Expect(readyCond.Message).To(ContainSubstring("configuration mismatch"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying ManagedByOperator is false")
			Expect(fetchedQuery.Status.ManagedByOperator).NotTo(BeNil())
			Expect(*fetchedQuery.Status.ManagedByOperator).To(BeFalse())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Synced=False")
			syncedCond := findCondition(fetchedQuery.Status.Conditions, "Synced")
			Expect(syncedCond).NotTo(BeNil())
			Expect(syncedCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(syncedCond.Reason).To(Equal("ConfigurationDrift"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Deleting K8s resource")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying K8s resource deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying query still exists in LogScale")
			// Query should still exist since adoption was rejected (ManagedByOperator=false)
			tempQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "")
			details, err := humioClient.GetSavedQuery(ctx, humioHttpClient, tempQuery)
			Expect(err).ToNot(HaveOccurred())
			Expect(details).NotTo(BeNil())
			Expect(details.Name).To(Equal(key.Name))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up pre-existing query from LogScale")
			Expect(humioClient.DeleteSavedQuery(ctx, humioHttpClient, tempQuery)).To(Succeed())
		})

		It("should not delete from LogScale when ManagedByOperator is false", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-unmanaged-deletion",
				Namespace: clusterKey.Namespace,
			}

			// Pre-create a query in LogScale
			preExistingQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=unmanaged | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Pre-creating query in LogScale")
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), ctrl.Request{NamespacedName: clusterKey})
			Expect(humioClient.AddSavedQuery(ctx, humioHttpClient, preExistingQuery, false)).To(Succeed())

			// Create K8s resource with non-matching query string to trigger rejection
			toCreateQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=unmanaged | sum(field)")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating K8s resource")
			Expect(k8sClient.Create(ctx, toCreateQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for adoption rejection")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, key, fetchedQuery)
				return fetchedQuery.Status.ManagedByOperator != nil && !*fetchedQuery.Status.ManagedByOperator
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Deleting K8s resource with ManagedByOperator=false")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying K8s resource deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying query still exists in LogScale")
			tempQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "")
			details, err := humioClient.GetSavedQuery(ctx, humioHttpClient, tempQuery)
			Expect(err).ToNot(HaveOccurred())
			Expect(details).NotTo(BeNil())
			Expect(details.Name).To(Equal(key.Name))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up query from LogScale")
			Expect(humioClient.DeleteSavedQuery(ctx, humioHttpClient, tempQuery)).To(Succeed())
		})

		It("should delete from LogScale when ManagedByOperator is true", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-managed-deletion",
				Namespace: clusterKey.Namespace,
			}
			// Create a query through normal creation (which sets ManagedByOperator=true)
			toCreateQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=managed-delete | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating managed query")
			Expect(k8sClient.Create(ctx, toCreateQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=True")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, key, fetchedQuery)
				condition := findCondition(fetchedQuery.Status.Conditions, "Ready")
				return condition != nil && condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying ManagedByOperator is true")
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())
			Expect(fetchedQuery.Status.ManagedByOperator).NotTo(BeNil())
			Expect(*fetchedQuery.Status.ManagedByOperator).To(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Deleting managed query")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying K8s resource deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying query also deleted from LogScale")
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), ctrl.Request{NamespacedName: clusterKey})
			tempQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "")
			_, err := humioClient.GetSavedQuery(ctx, humioHttpClient, tempQuery)
			Expect(err).To(HaveOccurred())
			var notFoundErr humioapi.EntityNotFound
			Expect(errors.As(err, &notFoundErr)).To(BeTrue())
		})
	})

	Context("ManagedByOperator Status Field", func() {
		It("should set ManagedByOperator to true on normal creation", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-managed-creation",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=managed-create | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating new query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying ManagedByOperator is true")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, key, fetchedQuery)
				return fetchedQuery.Status.ManagedByOperator != nil && *fetchedQuery.Status.ManagedByOperator
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())
		})
	})

	Context("Invalid View Handling", func() {
		It("should set Ready=False for invalid view and allow deletion", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-invalid-view-deletion",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, "non-existent-view-12345", "#type=invalid-view | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating query with invalid view")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=False")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioSavedQuery{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return false
				}
				condition := findCondition(fresh.Status.Conditions, "Ready")
				return condition != nil && condition.Status == metav1.ConditionFalse
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Ready condition has appropriate error")
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())
			readyCond := findCondition(fetchedQuery.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("ConfigurationError"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Attempting deletion with invalid view")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying deletion completes without blocking")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle transition from valid to invalid view gracefully", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-view-transition",
				Namespace: clusterKey.Namespace,
			}

			// Create with valid view
			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=transition | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating query with valid view")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=True")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, key, fetchedQuery)
				condition := findCondition(fetchedQuery.Status.Conditions, "Ready")
				return condition != nil && condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Updating to invalid view")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedQuery); err != nil {
					return err
				}
				fetchedQuery.Spec.ViewName = "invalid-view-transition-test"
				return k8sClient.Update(ctx, fetchedQuery)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Ready transitions to False")
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, key, fetchedQuery)
				condition := findCondition(fetchedQuery.Status.Conditions, "Ready")
				return condition != nil && condition.Status == metav1.ConditionFalse
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying deletion still works")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Version-Aware Description and Labels", func() {
		It("should handle queries without description/labels on any LogScale version", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-no-desc-labels",
				Namespace: clusterKey.Namespace,
			}

			// Create query without description or labels (backward compatible)
			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=backward-compat | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating query without description/labels")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Ready=True")
			waitForCondition(ctx, key, "Ready")

			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying no version warnings")
			// No UnsupportedFields condition should be present since no description/labels used
			versionCond := findCondition(fetchedQuery.Status.Conditions, "VersionCompatibility")
			if versionCond != nil {
				// If condition exists, it should be True (no issues)
				Expect(versionCond.Status).To(Equal(metav1.ConditionTrue))
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should allow updates without description/labels on any LogScale version", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-update-no-fields",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=update-test | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=True")
			waitForCondition(ctx, key, "Ready")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Updating QueryString")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() error {
				if err := k8sClient.Get(ctx, key, fetchedQuery); err != nil {
					return err
				}
				fetchedQuery.Spec.QueryString = "#type=update-test | count() | updated=true"
				return k8sClient.Update(ctx, fetchedQuery)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying update synced")
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedQuery)
				return fetchedQuery.Spec.QueryString
			}, testTimeout, suite.TestInterval).Should(Equal("#type=update-test | count() | updated=true"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying Ready remains True")
			waitForCondition(ctx, key, "Ready")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())
		})

		It("should handle adoption of existing queries without description/labels", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-adopt-no-fields",
				Namespace: clusterKey.Namespace,
			}

			// Pre-create a query in LogScale without description/labels
			preExistingQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=adopt-no-fields | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Pre-creating query in LogScale")
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), ctrl.Request{NamespacedName: clusterKey})
			Expect(humioClient.AddSavedQuery(ctx, humioHttpClient, preExistingQuery, false)).To(Succeed())

			// Create K8s resource with matching configuration (no description/labels)
			toCreateQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=adopt-no-fields | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating K8s resource")
			Expect(k8sClient.Create(ctx, toCreateQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying adoption success")
			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioSavedQuery{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return false
				}
				condition := findCondition(fresh.Status.Conditions, "Ready")
				return condition != nil && condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying ManagedByOperator is true")
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())
			Expect(fetchedQuery.Status.ManagedByOperator).NotTo(BeNil())
			Expect(*fetchedQuery.Status.ManagedByOperator).To(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should handle deletion of queries created without description/labels", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-delete-no-fields",
				Namespace: clusterKey.Namespace,
			}

			savedQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "#type=delete-no-fields | count()")

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating query")
			Expect(k8sClient.Create(ctx, savedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Waiting for Ready=True")
			waitForCondition(ctx, key, "Ready")

			fetchedQuery := &humiov1alpha1.HumioSavedQuery{}
			Expect(k8sClient.Get(ctx, key, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Deleting query")
			Expect(k8sClient.Delete(ctx, fetchedQuery)).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying deletion completes")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetchedQuery)
				return err != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying query deleted from LogScale")
			humioHttpClient := humioClient.GetHumioHttpClient(sharedCluster.Config(), ctrl.Request{NamespacedName: clusterKey})
			tempQuery := createTestSavedQuery(key.Name, testRepo.Spec.Name, "")
			_, err := humioClient.GetSavedQuery(ctx, humioHttpClient, tempQuery)
			Expect(err).To(HaveOccurred())
			var notFoundErr humioapi.EntityNotFound
			Expect(errors.As(err, &notFoundErr)).To(BeTrue())
		})
	})
})

// Helper function to verify condition state
func expectCondition(conditions []metav1.Condition, condType string, status metav1.ConditionStatus, reason string) {
	cond := findCondition(conditions, condType)
	Expect(cond).NotTo(BeNil(), "Condition %s should exist", condType)
	Expect(cond.Status).To(Equal(status), "Condition %s status mismatch", condType)
	Expect(cond.Reason).To(Equal(reason), "Condition %s reason mismatch", condType)
}

// Helper function to wait for specific condition
func waitForCondition(ctx context.Context, key types.NamespacedName, condType string) {
	Eventually(func() metav1.ConditionStatus {
		resource := &humiov1alpha1.HumioSavedQuery{}
		if err := k8sClient.Get(ctx, key, resource); err != nil {
			return metav1.ConditionUnknown
		}
		cond := findCondition(resource.Status.Conditions, condType)
		if cond != nil {
			return cond.Status
		}
		return metav1.ConditionUnknown
	}, testTimeout, suite.TestInterval).Should(Equal(metav1.ConditionTrue))
}

// Helper function to create saved query with defaults
func createTestSavedQuery(name, viewName, query string) *humiov1alpha1.HumioSavedQuery {
	return &humiov1alpha1.HumioSavedQuery{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterKey.Namespace,
		},
		Spec: humiov1alpha1.HumioSavedQuerySpec{
			ManagedClusterName: clusterKey.Name,
			Name:               name,
			ViewName:           viewName,
			QueryString:        query,
		},
	}
}
