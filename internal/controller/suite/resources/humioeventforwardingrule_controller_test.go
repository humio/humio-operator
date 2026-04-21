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

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Helper function to find a specific condition in the conditions list
func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

var _ = Describe("HumioEventForwardingRule Controller", Ordered, Label("envtest", "dummy", "real"), func() {
	var (
		ctx                context.Context
		cancel             context.CancelFunc
		humioHttpClient    *api.Client
		sharedForwarderID  string
		sharedForwarderKey types.NamespacedName
		sharedForwarder    *humiov1alpha1.HumioEventForwarder
	)

	BeforeAll(func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create a shared event forwarder for all tests
		sharedForwarderKey = types.NamespacedName{
			Name:      fmt.Sprintf("test-forwarder-shared-%d", GinkgoParallelProcess()),
			Namespace: clusterKey.Namespace,
		}

		sharedForwarder = &humiov1alpha1.HumioEventForwarder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sharedForwarderKey.Name,
				Namespace: sharedForwarderKey.Namespace,
			},
			Spec: humiov1alpha1.HumioEventForwarderSpec{
				ManagedClusterName: clusterKey.Name,
				Name:               fmt.Sprintf("test-forwarder-shared-%d", GinkgoParallelProcess()),
				Description:        "Shared test forwarder for event forwarding rule tests",
				ForwarderType:      "kafka",
				Enabled:            true,
				AllowDataDeletion:  true, // Allow cleanup in AfterAll
				KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
					Topic:      "test-topic",
					Properties: "bootstrap.servers=localhost:9092",
				},
			},
		}

		suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating shared forwarder")
		Expect(k8sClient.Create(ctx, sharedForwarder)).Should(Succeed())

		// Wait for forwarder to be ready and get its ID
		fetchedForwarder := &humiov1alpha1.HumioEventForwarder{}
		Eventually(func() bool {
			_ = k8sClient.Get(ctx, sharedForwarderKey, fetchedForwarder)
			condition := meta.FindStatusCondition(fetchedForwarder.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
			return condition != nil && condition.Status == metav1.ConditionTrue
		}, testTimeout, suite.TestInterval).Should(BeTrue())

		Expect(k8sClient.Get(ctx, sharedForwarderKey, fetchedForwarder)).Should(Succeed())
		Expect(fetchedForwarder.Status.EventForwarderID).ShouldNot(BeEmpty())
		sharedForwarderID = fetchedForwarder.Status.EventForwarderID
	})

	AfterAll(func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Clean up the shared forwarder
		suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting shared forwarder")
		fetchedForwarder := &humiov1alpha1.HumioEventForwarder{}
		if err := k8sClient.Get(ctx, sharedForwarderKey, fetchedForwarder); err == nil {
			Expect(k8sClient.Delete(ctx, fetchedForwarder)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, sharedForwarderKey, fetchedForwarder)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		}
	})

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		humioClient.ClearHumioClientConnections(testRepoName)
		humioHttpClient = humioClient.GetHumioHttpClient(sharedCluster.Config(), reconcile.Request{NamespacedName: clusterKey})
	})

	AfterEach(func() {
		cancel()
		humioClient.ClearHumioClientConnections(testRepoName)
	})

	Context("Basic CRUD Operations", func() {
		It("should create event forwarding rule successfully", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-create-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarding-rule-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
					AllowDataDeletion:  true, // Allow test cleanup
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating Event Forwarding Rule")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Verify Synced condition
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeSynced)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Successfully deleting")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should delete event forwarding rule", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-delete-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarding-rule-delete-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
					AllowDataDeletion:  true, // Allow test cleanup
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating event forwarding rule for deletion test")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Delete
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting event forwarding rule")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Drift Detection", func() {
		It("should detect query string drift", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-drift-query-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarding-rule-drift-query-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
					AllowDataDeletion:  true, // Allow test cleanup
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating event forwarding rule")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Update spec to create drift
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating drift by updating query")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			fetched.Spec.QueryString = "#type=test | field=value"
			Expect(k8sClient.Update(ctx, fetched)).Should(Succeed())

			// Verify drift is detected and corrected
			Eventually(func() string {
				rule, err := humioClient.GetEventForwardingRule(ctx, humioHttpClient, fetched)
				if err != nil {
					return ""
				}
				return rule.GetQueryString()
			}, testTimeout, suite.TestInterval).Should(Equal("#type=test | field=value"))

			// Cleanup rule
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should detect event forwarder ID drift", func() {
			// Create a second forwarder for drift testing
			forwarder2Key := types.NamespacedName{
				Name:      fmt.Sprintf("test-forwarder-drift-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			forwarder2 := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      forwarder2Key.Name,
					Namespace: forwarder2Key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarder-drift-%d", GinkgoParallelProcess()),
					Description:        "Second forwarder for drift testing",
					ForwarderType:      "kafka",
					Enabled:            true,
					AllowDataDeletion:  true, // Allow test cleanup
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "test-topic-2",
						Properties: "bootstrap.servers=localhost:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating second forwarder for drift test")
			Expect(k8sClient.Create(ctx, forwarder2)).Should(Succeed())

			// Wait for second forwarder to be ready and get its ID
			fetchedForwarder2 := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, forwarder2Key, fetchedForwarder2)
				condition := meta.FindStatusCondition(fetchedForwarder2.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				return condition != nil && condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Expect(k8sClient.Get(ctx, forwarder2Key, fetchedForwarder2)).Should(Succeed())
			Expect(fetchedForwarder2.Status.EventForwarderID).ShouldNot(BeEmpty())
			forwarder2ID := fetchedForwarder2.Status.EventForwarderID

			// Create forwarding rule with first forwarder
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-drift-fwd-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarding-rule-drift-fwd-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
					AllowDataDeletion:  true, // Allow test cleanup
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating event forwarding rule")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Update spec to create drift - switch to second forwarder
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating drift by updating forwarder ID")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			fetched.Spec.EventForwarderID = forwarder2ID
			Expect(k8sClient.Update(ctx, fetched)).Should(Succeed())

			// Verify drift is detected and corrected
			Eventually(func() string {
				rule, err := humioClient.GetEventForwardingRule(ctx, humioHttpClient, fetched)
				if err != nil {
					return ""
				}
				return rule.GetEventForwarderId()
			}, testTimeout, suite.TestInterval).Should(Equal(forwarder2ID))

			// Cleanup forwarding rule
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Cleanup second forwarder
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting second forwarder")
			Expect(k8sClient.Delete(ctx, forwarder2)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, forwarder2Key, fetchedForwarder2)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Error Handling", func() {
		It("should handle missing managed cluster", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-no-cluster-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: "non-existent-cluster",
					Name:               fmt.Sprintf("test-forwarding-rule-no-cluster-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
					AllowDataDeletion:  true, // Allow test cleanup
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating with missing cluster")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition == nil {
					return false
				}
				return condition.Status == metav1.ConditionFalse &&
					condition.Reason == humiov1alpha1.EventForwardingRuleReasonConfigError
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Rename Tests", func() {
		It("should block Name changes without annotation", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-rename-blocked-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarding-rule-original-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
					AllowDataDeletion:  true, // Allow test cleanup
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating event forwarding rule")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Get the original rule ID annotation
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			oldRuleIDAnnotation := fetched.Annotations["core.humio.com/event-forwarding-rule-id"]
			Expect(oldRuleIDAnnotation).ShouldNot(BeEmpty())

			// Try to change Name field without annotation - should be blocked by controller
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Attempting Name change without annotation")
			fetched.Spec.Name = "changed-name-blocked"
			Expect(k8sClient.Update(ctx, fetched)).Should(Succeed())

			// Controller should detect rename without annotation and set error condition
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil && condition.Reason == humiov1alpha1.EventForwardingRuleReasonConfigError {
					return condition.Message
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("humio.com/allow-rename"))

			// Verify rule ID hasn't changed in LogScale (still has same annotation)
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(fetched.Annotations["core.humio.com/event-forwarding-rule-id"]).Should(Equal(oldRuleIDAnnotation))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting")
			freshFetch := &humiov1alpha1.HumioEventForwardingRule{}
			Expect(k8sClient.Get(ctx, key, freshFetch)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, freshFetch)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, freshFetch)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should delete-recreate rule with annotation", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-rename-allowed-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarding-rule-original-allowed-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
					AllowDataDeletion:  true, // Allow test cleanup
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating event forwarding rule")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Get original rule ID before rename
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			oldRuleIDAnnotation := fetched.Annotations["core.humio.com/event-forwarding-rule-id"]
			Expect(oldRuleIDAnnotation).ShouldNot(BeEmpty())

			// Add annotation and change Name
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Renaming with annotation")
			if fetched.Annotations == nil {
				fetched.Annotations = make(map[string]string)
			}
			fetched.Annotations["humio.com/allow-rename"] = controller.AllowRenameAnnotationValue
			fetched.Spec.Name = fmt.Sprintf("test-forwarding-rule-renamed-allowed-%d", GinkgoParallelProcess())
			Expect(k8sClient.Update(ctx, fetched)).Should(Succeed())

			// Wait for delete-recreate to complete - rule ID annotation should change
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, key, fetched)
				newRuleIDAnnotation := fetched.Annotations["core.humio.com/event-forwarding-rule-id"]
				return newRuleIDAnnotation != "" && newRuleIDAnnotation != oldRuleIDAnnotation
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify rule is ready with new name
			Eventually(func() string {
				_ = k8sClient.Get(ctx, key, fetched)
				condition := findCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting")
			freshFetch := &humiov1alpha1.HumioEventForwardingRule{}
			Expect(k8sClient.Get(ctx, key, freshFetch)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, freshFetch)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, freshFetch)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("GraphQL Integration", func() {
		It("should verify GraphQL response mapped to status", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-status-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarding-rule-status-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
					AllowDataDeletion:  true, // Allow test cleanup
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating event forwarding rule")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() int {
				_ = k8sClient.Get(ctx, key, fetched)
				return len(fetched.Status.Conditions)
			}, testTimeout, suite.TestInterval).Should(BeNumerically(">=", 2))

			// Verify both Ready and Synced conditions exist
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			readyCondition := meta.FindStatusCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
			Expect(readyCondition).ToNot(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))

			syncedCondition := meta.FindStatusCondition(fetched.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeSynced)
			Expect(syncedCondition).ToNot(BeNil())
			Expect(syncedCondition.Status).To(Equal(metav1.ConditionTrue))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Force-Finalize", Label("envtest", "dummy", "real"), func() {
		It("should force-finalize when annotation present", func() {
			// Create a LOCAL forwarder for this test (not using shared forwarder)
			// so we can properly clean up the orphaned LogScale resources after force-finalize
			forwarderKey := types.NamespacedName{
				Name:      fmt.Sprintf("forwarder-force-finalize-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			forwarder := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      forwarderKey.Name,
					Namespace: forwarderKey.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarder-force-finalize-%d", GinkgoParallelProcess()),
					Description:        "Forwarder for force-finalize test",
					ForwarderType:      "kafka",
					Enabled:            true,
					AllowDataDeletion:  true, // Allow test cleanup
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "test-topic",
						Properties: "bootstrap.servers=localhost:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating forwarder for force-finalize test")
			Expect(k8sClient.Create(ctx, forwarder)).Should(Succeed())

			// Wait for forwarder to be ready
			fetchedForwarder := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, forwarderKey, fetchedForwarder)
				condition := meta.FindStatusCondition(fetchedForwarder.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				return condition != nil && condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Get the forwarder ID
			Expect(k8sClient.Get(ctx, forwarderKey, fetchedForwarder)).Should(Succeed())
			Expect(fetchedForwarder.Status.EventForwarderID).ShouldNot(BeEmpty())
			forwarderID := fetchedForwarder.Status.EventForwarderID

			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwardingrule-force-finalize-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("rule-force-finalize-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   forwarderID,
					AllowDataDeletion:  false, // Block deletion
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule Force-Finalize: Creating rule with allowDataDeletion=false")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwardingRule{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Verify finalizer present
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(fetched.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))

			// Capture the LogScale rule ID before force-finalize (we'll need it for cleanup)
			ruleID := ""
			Eventually(func() error {
				ruleDetails, err := humioClient.GetEventForwardingRule(ctx, humioHttpClient, fetched)
				if err != nil {
					return err
				}
				ruleID = ruleDetails.GetId()
				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(ruleID).ShouldNot(BeEmpty())

			// Attempt deletion (will be blocked by allowDataDeletion=false)
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule Force-Finalize: Triggering deletion (should block)")
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())

			// Verify resource stuck in deletion
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule Force-Finalize: Verifying deletion is blocked")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return err == nil && fetched.GetDeletionTimestamp() != nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify finalizer still present (blocked)
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(fetched.GetFinalizers()).To(ContainElement(controller.HumioFinalizer))

			// Add force-finalize annotation
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule Force-Finalize: Adding force-finalize annotation")
			Eventually(func() error {
				fresh := &humiov1alpha1.HumioEventForwardingRule{}
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
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule Force-Finalize: Verifying force-finalize removes finalizer")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, fetched)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue(), "Resource should be deleted after force-finalize")

			// Cleanup: Manually delete the orphaned LogScale rule since force-finalize skipped it
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule Force-Finalize: Manually deleting orphaned LogScale rule")
			_, err := humiographql.DeleteEventForwardingRule(ctx, humioHttpClient, testRepo.Spec.Name, ruleID)
			if err != nil {
				// Log the error but don't fail the test - the rule might already be gone
				suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("Warning: Failed to delete orphaned rule: %v", err))
			}

			// Cleanup: Delete the forwarder
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting forwarder for force-finalize test")
			Expect(k8sClient.Delete(ctx, fetchedForwarder)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, forwarderKey, fetchedForwarder)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})

	Context("Forwarder Reference Resolution", Label("envtest", "dummy", "real"), func() {
		It("should resolve eventForwarderRef from same namespace", func() {
			forwarderKey := types.NamespacedName{
				Name:      fmt.Sprintf("test-forwarder-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}
			ruleKey := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-ref-same-ns-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			// Create a HumioEventForwarder first
			forwarder := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      forwarderKey.Name,
					Namespace: forwarderKey.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarder-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder for reference resolution",
					ForwarderType:      "kafka",
					Enabled:            true,
					AllowDataDeletion:  true, // Allow test cleanup
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "test-topic",
						Properties: "bootstrap.servers=localhost:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating forwarder")
			Expect(k8sClient.Create(ctx, forwarder)).Should(Succeed())

			// Wait for forwarder to be ready
			fetchedForwarder := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, forwarderKey, fetchedForwarder)
				condition := meta.FindStatusCondition(fetchedForwarder.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				return condition != nil && condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Ensure the forwarder has an ID in status
			Expect(k8sClient.Get(ctx, forwarderKey, fetchedForwarder)).Should(Succeed())
			Expect(fetchedForwarder.Status.EventForwarderID).ShouldNot(BeEmpty())
			forwarderID := fetchedForwarder.Status.EventForwarderID

			// Create a HumioEventForwardingRule that references the forwarder
			rule := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ruleKey.Name,
					Namespace: ruleKey.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-rule-ref-same-ns-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					AllowDataDeletion:  true, // Allow test cleanup
					EventForwarderRef: &humiov1alpha1.EventForwarderReference{
						Name: forwarderKey.Name,
						// Namespace is omitted, should default to same namespace
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating rule with forwarder reference")
			Expect(k8sClient.Create(ctx, rule)).Should(Succeed())

			// Wait for rule to be ready
			fetchedRule := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, ruleKey, fetchedRule)
				condition := findCondition(fetchedRule.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				return condition != nil && condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify the resolved forwarder ID in status matches the forwarder's ID
			Expect(k8sClient.Get(ctx, ruleKey, fetchedRule)).Should(Succeed())
			Expect(fetchedRule.Status.ResolvedEventForwarderID).Should(Equal(forwarderID))

			// Verify the rule was created successfully in LogScale
			var ruleDetails *humiographql.EventForwardingRuleDetails
			Eventually(func() error {
				ruleDetails, err = humioClient.GetEventForwardingRule(ctx, humioHttpClient, fetchedRule)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(ruleDetails).ToNot(BeNil())
			Expect(ruleDetails.GetEventForwarderId()).To(Equal(forwarderID))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting rule")
			Expect(k8sClient.Delete(ctx, fetchedRule)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, ruleKey, fetchedRule)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting forwarder")
			Expect(k8sClient.Delete(ctx, fetchedForwarder)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, forwarderKey, fetchedForwarder)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should fail with missing forwarder reference", func() {
			ruleKey := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-missing-ref-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			// Create a HumioEventForwardingRule that references a non-existent forwarder
			rule := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ruleKey.Name,
					Namespace: ruleKey.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-rule-missing-ref-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					AllowDataDeletion:  true, // Allow test cleanup
					EventForwarderRef: &humiov1alpha1.EventForwarderReference{
						Name: "non-existent-forwarder",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating rule with missing forwarder reference")
			Expect(k8sClient.Create(ctx, rule)).Should(Succeed())

			// Wait for rule to have Ready condition set to False
			fetchedRule := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, ruleKey, fetchedRule)
				condition := findCondition(fetchedRule.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				return condition != nil && condition.Status == metav1.ConditionFalse
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify the reason is InvalidForwarder
			Expect(k8sClient.Get(ctx, ruleKey, fetchedRule)).Should(Succeed())
			condition := findCondition(fetchedRule.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
			Expect(condition.Reason).Should(Equal(humiov1alpha1.EventForwardingRuleReasonInvalidForwarder))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting rule")
			Expect(k8sClient.Delete(ctx, fetchedRule)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, ruleKey, fetchedRule)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should show specific error for non-existent repository", func() {
			// First create a forwarder
			forwarderKey := types.NamespacedName{
				Name:      fmt.Sprintf("forwarder-for-repo-test-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			forwarder := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      forwarderKey.Name,
					Namespace: forwarderKey.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarder-repo-%d", GinkgoParallelProcess()),
					Description:        "Forwarder for testing repository not found error",
					ForwarderType:      "kafka",
					Enabled:            true,
					AllowDataDeletion:  true, // Allow test cleanup
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "test-topic",
						Properties: "bootstrap.servers=kafka:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarding: Creating forwarder for repo test")
			Expect(k8sClient.Create(ctx, forwarder)).Should(Succeed())

			// Wait for forwarder to be ready
			fetchedForwarder := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, forwarderKey, fetchedForwarder)
				condition := meta.FindStatusCondition(fetchedForwarder.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				return condition != nil && condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Create rule with NON-EXISTENT repository
			ruleKey := types.NamespacedName{
				Name:      fmt.Sprintf("rule-missing-repo-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			rule := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ruleKey.Name,
					Namespace: ruleKey.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-rule-missing-repo-%d", GinkgoParallelProcess()),
					RepositoryName:     "nonexistent-repository", // DOES NOT EXIST
					QueryString:        "#type=test",
					AllowDataDeletion:  true, // Allow test cleanup
					EventForwarderRef: &humiov1alpha1.EventForwarderReference{
						Name: forwarderKey.Name,
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Creating rule with non-existent repository")
			Expect(k8sClient.Create(ctx, rule)).Should(Succeed())

			// Verify specific error message about repository
			fetchedRule := &humiov1alpha1.HumioEventForwardingRule{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, ruleKey, fetchedRule)
				condition := findCondition(fetchedRule.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
				if condition != nil && condition.Status == metav1.ConditionFalse {
					return condition.Message
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(And(
				ContainSubstring("Repository"),
				ContainSubstring("nonexistent-repository"),
				ContainSubstring("not found"),
			))

			// The message should NOT be generic "Event Forwarding Rule not found"
			_ = k8sClient.Get(ctx, ruleKey, fetchedRule)
			condition := findCondition(fetchedRule.Status.Conditions, humiov1alpha1.EventForwardingRuleConditionTypeReady)
			Expect(condition).ShouldNot(BeNil())
			Expect(condition.Message).ShouldNot(Equal("Event Forwarding Rule not found in LogScale"))
			Expect(condition.Message).Should(ContainSubstring("Repository"))
			Expect(condition.Reason).Should(Equal(humiov1alpha1.EventForwardingRuleReasonReconcileError))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Deleting rule")
			Expect(k8sClient.Delete(ctx, fetchedRule)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, ruleKey, fetchedRule)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting forwarder")
			Expect(k8sClient.Delete(ctx, fetchedForwarder)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, forwarderKey, fetchedForwarder)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})
	})
})
