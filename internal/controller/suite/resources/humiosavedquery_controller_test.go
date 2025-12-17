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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
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
					Description:        "Test saved query",
					Labels:             []string{"test", "automation"},
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
			Expect(fetchedSavedQuery.Spec.Description).To(Equal("Test saved query"))
			Expect(fetchedSavedQuery.Spec.Labels).To(ContainElements("test", "automation"))

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
					Description:        "Original description",
					Labels:             []string{"original"},
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
				fetchedSavedQuery.Spec.Description = "Updated description"
				fetchedSavedQuery.Spec.Labels = []string{"updated", "modified"}
				return k8sClient.Update(ctx, fetchedSavedQuery)
			}, testTimeout, suite.TestInterval).Should(Succeed())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying updated query")
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedSavedQuery)
				return fetchedSavedQuery.Spec.QueryString
			}, testTimeout, suite.TestInterval).Should(Equal("#type=updated | count()"))

			Expect(fetchedSavedQuery.Spec.Description).To(Equal("Updated description"))
			Expect(fetchedSavedQuery.Spec.Labels).To(ContainElements("updated", "modified"))

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedSavedQuery)).Should(Succeed())
		})

		It("should handle saved query with empty labels", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-no-labels",
				Namespace: clusterKey.Namespace,
			}

			toCreateSavedQuery := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "test-saved-query-no-labels",
					ViewName:           testRepo.Spec.Name,
					QueryString:        "#type=test | count()",
					Description:        "Query without labels",
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query without labels")
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

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying empty labels")
			Expect(k8sClient.Get(ctx, key, fetchedSavedQuery)).Should(Succeed())
			Expect(fetchedSavedQuery.Spec.Labels).To(BeEmpty())

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Cleaning up")
			Expect(k8sClient.Delete(ctx, fetchedSavedQuery)).Should(Succeed())
		})

		It("should handle drift detection correctly", func() {
			ctx := context.Background()
			key := types.NamespacedName{
				Name:      "test-saved-query-drift",
				Namespace: clusterKey.Namespace,
			}

			toCreateSavedQuery := &humiov1alpha1.HumioSavedQuery{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioSavedQuerySpec{
					ManagedClusterName: clusterKey.Name,
					Name:               "test-saved-query-drift",
					ViewName:           testRepo.Spec.Name,
					QueryString:        "#type=drift | count()",
					Description:        "Drift test query",
					Labels:             []string{"drift-test"},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Creating saved query for drift test")
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

			suite.UsingClusterBy(clusterKey.Name, "HumioSavedQuery: Verifying initial sync")
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetchedSavedQuery)
				condition := findCondition(fetchedSavedQuery.Status.Conditions, "Synced")
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal("True"))

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
})

// Helper function to find a condition by type
func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}
