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
	"fmt"
	"sort"
	"strings"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/controller/suite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// normalizeProperties sorts Kafka properties by key for order-independent comparison
func normalizeProperties(props string) string {
	if props == "" {
		return ""
	}
	lines := strings.Split(props, "\n")
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

var _ = Describe("HumioEventForwarder Controller", Label("envtest", "dummy", "real"), func() {
	var (
		ctx             context.Context
		cancel          context.CancelFunc
		humioHttpClient *api.Client
	)

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
		It("should create event forwarder with Kafka configuration successfully", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-create-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-%d", GinkgoParallelProcess()),
					Description:        "Test Kafka event forwarder for CRUD operations",
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "test-events",
						Properties: "bootstrap.servers=kafka:9092\nsecurity.protocol=PLAINTEXT",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating Kafka Event Forwarder")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Verify EventForwarderID is populated
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				return fresh.Status.EventForwarderID
			}, testTimeout, suite.TestInterval).ShouldNot(BeEmpty())

			// Verify ManagedByOperator is set to true on creation
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Verifying ManagedByOperator=true")
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return false
				}
				return fresh.Status.ManagedByOperator != nil && *fresh.Status.ManagedByOperator
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify Synced condition
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return false
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeSynced)
				if condition == nil {
					return false
				}
				return condition.Status == metav1.ConditionTrue &&
					condition.Reason == humiov1alpha1.EventForwarderReasonConfigurationSynced
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify forwarder exists in LogScale
			var forwarder *humiographql.KafkaEventForwarderDetails
			Eventually(func() error {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return err
				}
				forwarder, err = humioClient.GetEventForwarder(ctx, humioHttpClient, fresh)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(forwarder).ToNot(BeNil())
			Expect(forwarder.GetName()).To(Equal(toCreate.Spec.Name))
			Expect(forwarder.GetTopic()).To(Equal(toCreate.Spec.KafkaConfig.Topic))
			Expect(forwarder.GetEnabled()).To(Equal(toCreate.Spec.Enabled))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Successfully deleting")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should delete event forwarder and remove finalizer", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-delete-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-delete-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder for deletion",
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "test-delete-events",
						Properties: "bootstrap.servers=kafka:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating forwarder for deletion test")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Verify finalizer was added
			Eventually(func() []string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return nil
				}
				return fresh.GetFinalizers()
			}, testTimeout, suite.TestInterval).ShouldNot(BeEmpty())

			// Delete
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting event forwarder")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())

			// Verify ManagedByOperator is true before deletion
			Expect(fetched.Status.ManagedByOperator).ToNot(BeNil())
			Expect(*fetched.Status.ManagedByOperator).To(BeTrue())

			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())

			// Verify finalizer is removed and resource is deleted
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify forwarder was deleted from LogScale (ManagedByOperator=true triggers deletion)
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Verifying forwarder deleted from LogScale")
			_, err := humioClient.GetEventForwarder(ctx, humioHttpClient, fetched)
			Expect(err).Should(HaveOccurred())
			var notFound api.EntityNotFound
			Expect(errors.As(err, &notFound)).To(BeTrue())
		})
	})

	Context("Update Operations", func() {
		It("should update enabled state", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-update-enabled-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-enabled-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder for enabled state update",
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "test-events",
						Properties: "bootstrap.servers=kafka:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating event forwarder")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Verify initial enabled state
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return false
				}
				forwarder, err := humioClient.GetEventForwarder(ctx, humioHttpClient, fresh)
				if err != nil {
					return false
				}
				return forwarder.GetEnabled()
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Disable the forwarder
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Disabling forwarder")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			fetched.Spec.Enabled = false
			Expect(k8sClient.Update(ctx, fetched)).Should(Succeed())

			// Verify forwarder is disabled in LogScale
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return true // return opposite to trigger retry
				}
				forwarder, err := humioClient.GetEventForwarder(ctx, humioHttpClient, fresh)
				if err != nil {
					return true // return opposite to trigger retry
				}
				return forwarder.GetEnabled()
			}, testTimeout, suite.TestInterval).Should(BeFalse())

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should update Kafka topic", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-update-topic-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-topic-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder for topic update",
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "initial-topic",
						Properties: "bootstrap.servers=kafka:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating event forwarder")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Update topic
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Updating Kafka topic")
			updatedTopic := "updated-topic"
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			fetched.Spec.KafkaConfig.Topic = updatedTopic
			Expect(k8sClient.Update(ctx, fetched)).Should(Succeed())

			// Verify update in LogScale
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				forwarder, err := humioClient.GetEventForwarder(ctx, humioHttpClient, fresh)
				if err != nil {
					return ""
				}
				return forwarder.GetTopic()
			}, testTimeout, suite.TestInterval).Should(Equal(updatedTopic))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should update Kafka properties", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-update-props-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-props-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder for properties update",
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "test-events",
						Properties: "bootstrap.servers=kafka:9092\nsecurity.protocol=PLAINTEXT",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating event forwarder")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			fetched := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Update properties
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Updating Kafka properties")
			updatedProperties := "bootstrap.servers=kafka:9092\nsecurity.protocol=SASL_SSL\nsasl.mechanism=PLAIN"
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			fetched.Spec.KafkaConfig.Properties = updatedProperties
			Expect(k8sClient.Update(ctx, fetched)).Should(Succeed())

			// Verify update in LogScale
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return false
				}
				forwarder, err := humioClient.GetEventForwarder(ctx, humioHttpClient, fresh)
				if err != nil {
					return false
				}
				// Compare properties as key-value pairs (order-independent)
				return normalizeProperties(forwarder.GetProperties()) == normalizeProperties(updatedProperties)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

	})

	Context("Validation Tests", func() {
		It("should reject kafka forwarder without kafkaConfig", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-no-kafkaconfig-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-no-config-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder without kafkaConfig",
					ForwarderType:      "kafka",
					Enabled:            true,
					// Intentionally omitting KafkaConfig
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Attempting to create kafka forwarder without kafkaConfig")
			err := k8sClient.Create(ctx, toCreate)
			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("kafkaConfig is required when forwarderType is kafka"))
		})
	})

	Context("PropertiesSecretRef", func() {
		It("should create event forwarder with PropertiesSecretRef", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-secret-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}
			secretKey := types.NamespacedName{
				Name:      fmt.Sprintf("kafka-credentials-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			// Create Secret with sensitive Kafka properties
			kafkaSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"kafka.properties": "sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"test-user\" password=\"test-password\";\nssl.truststore.password=truststore-pass",
				},
			}
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating Secret for Kafka credentials")
			Expect(k8sClient.Create(ctx, kafkaSecret)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, secretKey, kafkaSecret)
				return err == nil
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Create forwarder with PropertiesSecretRef
			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-secret-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder with secret-based credentials",
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic: "test-secret-events",
						PropertiesSecretRef: &humiov1alpha1.SecretKeyReference{
							Name: secretKey.Name,
							Key:  "kafka.properties",
						},
						Properties: "bootstrap.servers=kafka:9092\ncompression.type=snappy",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating forwarder with PropertiesSecretRef")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			// Verify forwarder becomes ready
			fetched := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Verify EventForwarderID is populated
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				return fresh.Status.EventForwarderID
			}, testTimeout, suite.TestInterval).ShouldNot(BeEmpty())

			// Verify Synced condition
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return false
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeSynced)
				if condition == nil {
					return false
				}
				return condition.Status == metav1.ConditionTrue
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify forwarder exists in LogScale with merged properties
			var forwarder *humiographql.KafkaEventForwarderDetails
			Eventually(func() error {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return err
				}
				forwarder, err = humioClient.GetEventForwarder(ctx, humioHttpClient, fresh)
				return err
			}, testTimeout, suite.TestInterval).Should(Succeed())
			Expect(forwarder).ToNot(BeNil())
			Expect(forwarder.GetName()).To(Equal(toCreate.Spec.Name))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Cleanup Secret
			Expect(k8sClient.Delete(ctx, kafkaSecret)).Should(Succeed())
		})

		It("should handle missing Secret gracefully", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-missing-secret-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			// Create forwarder referencing non-existent secret
			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-missing-secret-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder with missing secret reference",
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic: "test-missing-secret-events",
						PropertiesSecretRef: &humiov1alpha1.SecretKeyReference{
							Name: "non-existent-secret",
							Key:  "kafka.properties",
						},
						Properties: "bootstrap.servers=kafka:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating forwarder with missing secret")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			// Verify forwarder does not become ready AND has specific error message
			fetched := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				if condition != nil && condition.Status == metav1.ConditionFalse {
					return condition.Message
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("failed to fetch secret"))

			// Verify error condition is set
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			condition := meta.FindStatusCondition(fetched.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
			Expect(condition).ShouldNot(BeNil())
			Expect(condition.Reason).Should(Equal(humiov1alpha1.EventForwarderReasonReconcileError))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should reject cross-namespace secret references", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("eventforwarder-cross-ns-secret-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			// Create secret in a different namespace
			differentNamespace := fmt.Sprintf("different-ns-%d", GinkgoParallelProcess())
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: differentNamespace,
				},
			}
			suite.UsingClusterBy(clusterKey.Name, "Creating different namespace for secret")
			Expect(k8sClient.Create(ctx, ns)).Should(Succeed())

			secretKey := types.NamespacedName{
				Name:      fmt.Sprintf("cross-ns-kafka-secret-%d", GinkgoParallelProcess()),
				Namespace: differentNamespace,
			}

			kafkaSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
				},
				StringData: map[string]string{
					"kafka.properties": "sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"user\" password=\"secret\"",
				},
			}
			suite.UsingClusterBy(clusterKey.Name, "Creating secret in different namespace")
			Expect(k8sClient.Create(ctx, kafkaSecret)).Should(Succeed())

			// Create forwarder trying to reference secret from different namespace
			toCreate := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               fmt.Sprintf("test-kafka-forwarder-cross-ns-%d", GinkgoParallelProcess()),
					Description:        "Test forwarder attempting cross-namespace secret access",
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic: "test-cross-ns-events",
						PropertiesSecretRef: &humiov1alpha1.SecretKeyReference{
							Name:      secretKey.Name,
							Key:       "kafka.properties",
							Namespace: differentNamespace, // Cross-namespace reference
						},
						Properties: "bootstrap.servers=kafka:9092",
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating forwarder with cross-namespace secret")
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			// Verify forwarder does not become ready AND has security error message
			fetched := &humiov1alpha1.HumioEventForwarder{}
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(fresh.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
				if condition != nil && condition.Status == metav1.ConditionFalse {
					return condition.Message
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(ContainSubstring("are not allowed"))

			// Verify error message contains the enforced namespace
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			condition := meta.FindStatusCondition(fetched.Status.Conditions, humiov1alpha1.EventForwarderConditionTypeReady)
			Expect(condition).ShouldNot(BeNil())
			Expect(condition.Message).Should(ContainSubstring(clusterKey.Namespace))
			Expect(condition.Reason).Should(Equal(humiov1alpha1.EventForwarderReasonReconcileError))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Deleting")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Cleanup secret and namespace
			Expect(k8sClient.Delete(ctx, kafkaSecret)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ns)).Should(Succeed())
		})

	})

	Context("Adoption Pattern", Label("envtest", "dummy", "real"), func() {
		It("should adopt existing event forwarder when properties match", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("humioeventforwarder-adoption-match-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			// Step 1: Create a forwarder directly in mock client (simulating an unmanaged forwarder)
			// This simulates a forwarder that was created manually in LogScale UI or via API
			suite.UsingClusterBy(clusterKey.Name, "Creating unmanaged forwarder directly in LogScale")

			unmanagedForwarderName := fmt.Sprintf("unmanaged-kafka-forwarder-%d", GinkgoParallelProcess())
			testProperties := "bootstrap.servers=kafka.example.com:9092\nsecurity.protocol=SASL_SSL"

			// Create forwarder directly in mock client
			unmanagedForwarder := &humiov1alpha1.HumioEventForwarder{
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					Name:          unmanagedForwarderName,
					Description:   "Unmanaged forwarder for adoption test",
					ForwarderType: "kafka",
					Enabled:       true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "adoption-test-topic",
						Properties: testProperties,
					},
				},
			}
			unmanagedForwarder.Spec.ManagedClusterName = clusterKey.Name

			err := humioClient.AddEventForwarder(ctx, humioHttpClient, unmanagedForwarder)
			Expect(err).Should(Succeed())

			// Get the forwarder to retrieve its ID
			addedForwarder, err := humioClient.GetEventForwarder(ctx, humioHttpClient, unmanagedForwarder)
			Expect(err).Should(Succeed())
			unmanagedForwarderID := addedForwarder.GetId()
			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("Created unmanaged forwarder with ID: %s", unmanagedForwarderID))

			// Step 2: Create a CRD with the SAME name and SAME properties
			// The operator should adopt the existing forwarder instead of trying to create a duplicate
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating CRD with matching properties")
			toCreateEventForwarder := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               unmanagedForwarderName,                  // Same name as unmanaged forwarder
					Description:        "Unmanaged forwarder for adoption test", // Same description
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "adoption-test-topic", // Same topic
						Properties: testProperties,        // Same properties
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating CRD")
			Expect(k8sClient.Create(ctx, toCreateEventForwarder)).Should(Succeed())

			// Step 3: Verify adoption succeeded
			// The forwarder should become Ready and the status should contain the adopted ID
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Verifying successful adoption")
			fetched := &humiov1alpha1.HumioEventForwarder{}

			// Wait for Ready condition
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(
					fresh.Status.Conditions,
					humiov1alpha1.EventForwarderConditionTypeReady,
				)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Verify the adopted ID matches the unmanaged forwarder ID
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(fetched.Status.EventForwarderID).Should(Equal(unmanagedForwarderID))

			// Verify ManagedByOperator is set to true on successful adoption
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Verifying ManagedByOperator=true")
			Expect(fetched.Status.ManagedByOperator).ToNot(BeNil())
			Expect(*fetched.Status.ManagedByOperator).To(BeTrue())

			// Verify Synced condition is also True (properties match)
			Eventually(func() string {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				if err := k8sClient.Get(ctx, key, fresh); err != nil {
					return ""
				}
				condition := meta.FindStatusCondition(
					fresh.Status.Conditions,
					humiov1alpha1.EventForwarderConditionTypeSynced,
				)
				if condition != nil {
					return string(condition.Status)
				}
				return ""
			}, testTimeout, suite.TestInterval).Should(Equal(string(metav1.ConditionTrue)))

			// Verify forwarder still exists in LogScale with correct ID
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			forwarder, err := humioClient.GetEventForwarder(ctx, humioHttpClient, fetched)
			Expect(err).Should(Succeed())
			Expect(forwarder.GetId()).Should(Equal(unmanagedForwarderID))

			// Cleanup
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Cleaning up adopted forwarder")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		})

		It("should reject adoption when properties don't match", func() {
			key := types.NamespacedName{
				Name:      fmt.Sprintf("humioeventforwarder-adoption-reject-%d", GinkgoParallelProcess()),
				Namespace: clusterKey.Namespace,
			}

			// Step 1: Create a forwarder directly in mock client with specific properties
			suite.UsingClusterBy(clusterKey.Name, "Creating unmanaged forwarder with specific properties")

			unmanagedForwarderName := fmt.Sprintf("unmanaged-kafka-forwarder-reject-%d", GinkgoParallelProcess())
			unmanagedProperties := "bootstrap.servers=kafka.example.com:9092\nsecurity.protocol=SASL_SSL\ncompression.type=snappy"

			// Create forwarder directly in mock client
			unmanagedForwarder := &humiov1alpha1.HumioEventForwarder{
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					Name:          unmanagedForwarderName,
					Description:   "Unmanaged forwarder with different properties",
					ForwarderType: "kafka",
					Enabled:       true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "rejection-test-topic",
						Properties: unmanagedProperties,
					},
				},
			}
			unmanagedForwarder.Spec.ManagedClusterName = clusterKey.Name

			err := humioClient.AddEventForwarder(ctx, humioHttpClient, unmanagedForwarder)
			Expect(err).Should(Succeed())

			// Get the forwarder to retrieve its ID
			addedForwarder, err := humioClient.GetEventForwarder(ctx, humioHttpClient, unmanagedForwarder)
			Expect(err).Should(Succeed())
			unmanagedForwarderID := addedForwarder.GetId()
			suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("Created unmanaged forwarder with ID: %s", unmanagedForwarderID))

			// Step 2: Create a CRD with the SAME name but DIFFERENT properties
			// The operator should REJECT this and NOT adopt the forwarder
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating CRD with different properties")
			differentProperties := "bootstrap.servers=different-kafka.example.com:9093\nsecurity.protocol=PLAINTEXT" // Different!

			toCreateEventForwarder := &humiov1alpha1.HumioEventForwarder{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioEventForwarderSpec{
					ManagedClusterName: clusterKey.Name,
					Name:               unmanagedForwarderName,          // Same name
					Description:        "CRD with different properties", // Different description
					ForwarderType:      "kafka",
					Enabled:            true,
					KafkaConfig: &humiov1alpha1.KafkaEventForwarderConfig{
						Topic:      "rejection-test-topic", // Same topic
						Properties: differentProperties,    // Different properties!
					},
				},
			}

			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Creating CRD")
			Expect(k8sClient.Create(ctx, toCreateEventForwarder)).Should(Succeed())

			// Step 3: Verify adoption was REJECTED
			// The forwarder should NOT become Ready because adoption was rejected
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Verifying adoption was rejected")
			fetched := &humiov1alpha1.HumioEventForwarder{}

			// Wait for reconciliation to process and reject adoption
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, key, fetched); err != nil {
					return false
				}
				condition := meta.FindStatusCondition(
					fetched.Status.Conditions,
					humiov1alpha1.EventForwarderConditionTypeReady,
				)
				// We're waiting for a condition to appear with status False or error message
				if condition != nil && condition.Status != metav1.ConditionTrue {
					return true
				}
				return false
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify the EventForwarderID is NOT set (adoption was rejected)
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(fetched.Status.EventForwarderID).Should(BeEmpty())

			// Verify Ready condition is NOT True and has error message
			condition := meta.FindStatusCondition(
				fetched.Status.Conditions,
				humiov1alpha1.EventForwarderConditionTypeReady,
			)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).ShouldNot(Equal(metav1.ConditionTrue))
			Expect(condition.Message).Should(ContainSubstring("configuration mismatch"))

			// Verify ManagedByOperator is set to false on adoption rejection
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Verifying ManagedByOperator=false")
			Expect(fetched.Status.ManagedByOperator).ToNot(BeNil())
			Expect(*fetched.Status.ManagedByOperator).To(BeFalse())

			// Verify the unmanaged forwarder still exists in LogScale unchanged
			// Use the mock client's GetEventForwarder method to verify the original forwarder
			originalForwarder, err := humioClient.GetEventForwarder(ctx, humioHttpClient, unmanagedForwarder)
			Expect(err).Should(Succeed())
			Expect(originalForwarder.GetId()).Should(Equal(unmanagedForwarderID))
			Expect(normalizeProperties(originalForwarder.GetProperties())).Should(Equal(normalizeProperties(unmanagedProperties)))

			// Cleanup CRD (forwarder remains in LogScale as it was never adopted)
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Cleaning up rejected CRD")
			Expect(k8sClient.Get(ctx, key, fetched)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, fetched)).Should(Succeed())
			Eventually(func() bool {
				fresh := &humiov1alpha1.HumioEventForwarder{}
				err := k8sClient.Get(ctx, key, fresh)
				return k8serrors.IsNotFound(err)
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			// Verify forwarder was NOT deleted from LogScale (ManagedByOperator=false prevents deletion)
			suite.UsingClusterBy(clusterKey.Name, "HumioEventForwarder: Verifying forwarder NOT deleted from LogScale")
			stillExists, err := humioClient.GetEventForwarder(ctx, humioHttpClient, unmanagedForwarder)
			Expect(err).Should(Succeed())
			Expect(stillExists).ToNot(BeNil())
			Expect(stillExists.GetId()).Should(Equal(unmanagedForwarderID))

			// Cleanup the unmanaged forwarder from LogScale (use mock client)
			suite.UsingClusterBy(clusterKey.Name, "Cleaning up unmanaged forwarder from LogScale")
			err = humioClient.DeleteEventForwarder(ctx, humioHttpClient, unmanagedForwarder)
			Expect(err).Should(Succeed())
		})
	})
})
