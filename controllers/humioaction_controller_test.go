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

package controllers

import (
	"context"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Humio Action Controller", func() {
	BeforeEach(func() {
		clusterKey := types.NamespacedName{
			Name:      "humiocluster-shared",
			Namespace: "default",
		}
		cluster := constructBasicSingleNodeHumioCluster(clusterKey, true)
		ctx := context.Background()
		createAndBootstrapCluster(ctx, cluster, true)
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
		var existingClusters humiov1alpha1.HumioClusterList
		ctx := context.Background()
		k8sClient.List(ctx, &existingClusters)
		for _, cluster := range existingClusters.Items {
			if val, ok := cluster.Annotations[autoCleanupAfterTestAnnotationName]; ok {
				if val == testProcessID {
					_ = k8sClient.Delete(ctx, &cluster)
				}
			}
		}
	})

	Context("SlackPostMessageProperties", func() {

		It("should support referencing secrets", func() {
			slackPostMessageActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: "humiocluster-shared",
				Name:               "example-slack-post-message-action",
				ViewName:           "humio",
				SlackPostMessageProperties: &humiov1alpha1.HumioActionSlackPostMessageProperties{
					ApiTokenSource: humiov1alpha1.VarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "slack-secret",
							},
							Key: "key",
						},
					},
					Channels: []string{"#some-channel"},
					Fields: map[string]string{
						"some": "key",
					},
				},
			}

			key := types.NamespacedName{
				Name:      "humio-slack-post-message-action",
				Namespace: "default",
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: slackPostMessageActionSpec,
			}

			slackSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "slack-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"key": []byte("secret-token"),
				},
			}

			ctx := context.Background()

			Expect(k8sClient.Create(ctx, slackSecret)).Should(Succeed())
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			notifier, err := humioClient.GetNotifier(toCreateAction)
			Expect(err).To(BeNil())
			Expect(notifier).ToNot(BeNil())

			createdAction, err := humio.ActionFromNotifier(notifier)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.SlackPostMessageProperties.ApiToken).To(Equal("secret-token"))
		})

		It("should support direct api token", func() {
			slackPostMessageActionSpec := humiov1alpha1.HumioActionSpec{
				ManagedClusterName: "humiocluster-shared",
				Name:               "example-slack-post-message-action",
				ViewName:           "humio",
				SlackPostMessageProperties: &humiov1alpha1.HumioActionSlackPostMessageProperties{
					ApiToken: "direct-token",
					Channels: []string{"#some-channel"},
					Fields: map[string]string{
						"some": "key",
					},
				},
			}

			key := types.NamespacedName{
				Name:      "humio-slack-post-message-action",
				Namespace: "default",
			}

			toCreateAction := &humiov1alpha1.HumioAction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: slackPostMessageActionSpec,
			}

			ctx := context.Background()
			Expect(k8sClient.Create(ctx, toCreateAction)).Should(Succeed())

			fetchedAction := &humiov1alpha1.HumioAction{}
			Eventually(func() string {
				k8sClient.Get(ctx, key, fetchedAction)
				return fetchedAction.Status.State
			}, testTimeout, testInterval).Should(Equal(humiov1alpha1.HumioActionStateExists))

			notifier, err := humioClient.GetNotifier(toCreateAction)
			Expect(err).To(BeNil())
			Expect(notifier).ToNot(BeNil())

			createdAction, err := humio.ActionFromNotifier(notifier)
			Expect(err).To(BeNil())
			Expect(createdAction.Spec.Name).To(Equal(toCreateAction.Spec.Name))
			Expect(createdAction.Spec.SlackPostMessageProperties.ApiToken).To(Equal("direct-token"))
		})
	})
})
