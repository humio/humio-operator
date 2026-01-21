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

	Context("Immutability Tests", func() {
		It("should reject Name field changes", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarding-immutable-name-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwardingRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwardingRuleSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-forwarding-rule-immut-name-%d", GinkgoParallelProcess()),
					RepositoryName:     testRepo.Spec.Name,
					QueryString:        "#type=test",
					EventForwarderID:   sharedForwarderID,
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

			// Try to change immutable Name field
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwardingRule: Attempting to change immutable Name field")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			fetched.Spec.Name = "changed-name"
			Eventually(func() error {
				return k8sClient.Update(ctx, fetched)
			}, testTimeout, suite.TestInterval).Should(MatchError(ContainSubstring("Value is immutable")))

			// Cleanup (need to refetch to avoid update conflict)
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
