package bootstraptokens

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/helpers"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/humio/humio-operator/internal/controller/suite"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("HumioBootstrapToken Controller", func() {
	Context("Humio BootstrapToken Create", Label("envtest", "dummy", "real"), func() {
		It("Should correctly create bootstrap token", func() {
			key := types.NamespacedName{
				Name:      "humiobootstraptoken-create",
				Namespace: testProcessNamespace,
			}
			toCreate := &humiov1alpha1.HumioBootstrapToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioBootstrapTokenSpec{
					ManagedClusterName: key.Name,
				},
			}
			toCreateHumioCluster := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{
					NodePools: []humiov1alpha1.HumioNodePoolSpec{
						{
							Name: "node-pool-1",
							HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
								NodeCount: 1,
								Affinity: corev1.Affinity{
									NodeAffinity: &corev1.NodeAffinity{
										RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
											NodeSelectorTerms: []corev1.NodeSelectorTerm{
												{
													MatchExpressions: []corev1.NodeSelectorRequirement{
														{
															Key:      "kubernetes.io/os",
															Operator: corev1.NodeSelectorOpIn,
															Values:   []string{"linux"},
														},
													},
												},
											},
										},
									},
									PodAntiAffinity: &corev1.PodAntiAffinity{
										PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
											{
												Weight: 100,
												PodAffinityTerm: corev1.PodAffinityTerm{
													LabelSelector: &metav1.LabelSelector{
														MatchExpressions: []metav1.LabelSelectorRequirement{
															{
																Key:      "app.kubernetes.io/name",
																Operator: metav1.LabelSelectorOpIn,
																Values:   []string{"humio"},
															},
														},
													},
													TopologyKey: "kubernetes.io/hostname",
												},
											},
										},
									},
								},
								Tolerations: []corev1.Toleration{
									{
										Key:      "dedicated",
										Operator: corev1.TolerationOpEqual,
										Value:    "humio",
										Effect:   corev1.TaintEffectNoSchedule,
									},
									{
										Key:      "humio.com/exclusive",
										Operator: corev1.TolerationOpExists,
										Effect:   corev1.TaintEffectNoExecute,
									},
								},
							},
						},
					},
				},
			}
			ctx := context.Background()

			suite.UsingClusterBy(key.Name, "Creating the cluster successfully")
			defer suite.CleanupBootstrapToken(ctx, k8sClient, toCreate)

			bootstrapTokenConfig := controller.NewHumioBootstrapTokenConfig(toCreate, &humiov1alpha1.HumioCluster{})
			bootstrapTokenOneTimePod := &corev1.Pod{}

			Expect(k8sClient.Create(ctx, toCreateHumioCluster)).To(Succeed())
			Expect(k8sClient.Create(ctx, toCreate)).To(Succeed())

			Expect(bootstrapTokenConfig.PodName()).To(Equal("humiobootstraptoken-create-bootstrap-token-onetime"))
			Expect(bootstrapTokenConfig.Namespace()).To(Equal(testProcessNamespace))

			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      bootstrapTokenConfig.PodName(),
					Namespace: bootstrapTokenConfig.Namespace(),
				}, bootstrapTokenOneTimePod)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				}
				if k8serrors.IsNotFound(err) {
					return err
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(Succeed())

			Expect(bootstrapTokenOneTimePod.Name).To(Equal(bootstrapTokenConfig.PodName()))

			// Verify node affinity matches
			Expect(bootstrapTokenOneTimePod.Spec.Affinity).ToNot(BeNil())
			Expect(bootstrapTokenOneTimePod.Spec.Affinity.NodeAffinity).ToNot(BeNil())
			Expect(bootstrapTokenOneTimePod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution).ToNot(BeNil())
			clusterNodeAffinity := toCreateHumioCluster.Spec.NodePools[0].Affinity.NodeAffinity
			podNodeAffinity := bootstrapTokenOneTimePod.Spec.Affinity.NodeAffinity
			Expect(podNodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms).To(Equal(
				clusterNodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms))

			// Verify pod anti-affinity matches
			Expect(bootstrapTokenOneTimePod.Spec.Affinity.PodAntiAffinity).ToNot(BeNil())
			clusterPodAntiAffinity := toCreateHumioCluster.Spec.NodePools[0].Affinity.PodAntiAffinity
			podPodAntiAffinity := bootstrapTokenOneTimePod.Spec.Affinity.PodAntiAffinity
			Expect(podPodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(Equal(
				clusterPodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution))

			// Verify tolerations match
			for i, toleration := range toCreateHumioCluster.Spec.NodePools[0].Tolerations {
				found := false
				for _, podToleration := range bootstrapTokenOneTimePod.Spec.Tolerations {
					if podToleration.Key == toleration.Key &&
						podToleration.Operator == toleration.Operator &&
						podToleration.Value == toleration.Value &&
						podToleration.Effect == toleration.Effect {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue(), "Missing expected toleration at index %d: %v", i, toleration)
			}
		})

		It("Should create custom bootstrap token and have operator generate hashedToken", Label("envtest", "dummy", "real"), func() {
			key := types.NamespacedName{
				Name:      "humiobootstraptoken-custom",
				Namespace: testProcessNamespace,
			}

			// Generate a random 32-byte token and base64 encode it
			tokenBytes := make([]byte, 32)
			_, err := rand.Read(tokenBytes)
			Expect(err).ToNot(HaveOccurred())
			customToken := base64.StdEncoding.EncodeToString(tokenBytes)

			// Create custom bootstrap token secret with only the plain token
			customSecretKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-custom-bootstrap-secret", key.Name),
				Namespace: key.Namespace,
			}
			customHashedSecretKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-custom-hashed-secret", key.Name),
				Namespace: key.Namespace,
			}

			suite.UsingClusterBy(key.Name, "Creating custom bootstrap token secret with plain token only")
			customSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      customSecretKey.Name,
					Namespace: customSecretKey.Namespace,
					Labels: map[string]string{
						"app.kubernetes.io/instance":   key.Name,
						"app.kubernetes.io/managed-by": "humio-operator",
						"app.kubernetes.io/name":       "humio",
						"humio.com/secret-identifier":  fmt.Sprintf("%s-bootstrap-token", key.Name),
					},
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"secret": []byte(customToken),
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, customSecret)).To(Succeed())

			// Create separate hashed token secret for envtest/dummy environments
			if helpers.UseEnvtest() {
				suite.UsingClusterBy(key.Name, "Creating custom hashed token secret for envtest")
				hashedTokenValue := base64.StdEncoding.EncodeToString([]byte("hashed-" + customToken))
				customHashedSecret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      customHashedSecretKey.Name,
						Namespace: customHashedSecretKey.Namespace,
						Labels: map[string]string{
							"app.kubernetes.io/instance":   key.Name,
							"app.kubernetes.io/managed-by": "humio-operator",
							"app.kubernetes.io/name":       "humio",
						},
					},
					Type: corev1.SecretTypeOpaque,
					Data: map[string][]byte{
						"hashedToken": []byte(hashedTokenValue),
					},
				}
				Expect(k8sClient.Create(ctx, customHashedSecret)).To(Succeed())
			}

			// Create custom HumioBootstrapToken that references our secret
			// Note: We don't need a cluster to exist - the controller will process the token anyway
			suite.UsingClusterBy(key.Name, "Creating custom HumioBootstrapToken resource")
			customBootstrapToken := &humiov1alpha1.HumioBootstrapToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
					Labels: map[string]string{
						"app.kubernetes.io/instance":   key.Name,
						"app.kubernetes.io/managed-by": "humio-operator",
						"app.kubernetes.io/name":       "humio",
						"managed-cluster-name":         key.Name,
					},
				},
				Spec: humiov1alpha1.HumioBootstrapTokenSpec{
					ManagedClusterName: key.Name, // This cluster doesn't need to exist
					TokenSecret: humiov1alpha1.HumioTokenSecretSpec{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: customSecretKey.Name,
							},
							Key: "secret",
						},
					},
					HashedTokenSecret: humiov1alpha1.HumioHashedTokenSecretSpec{
						SecretKeyRef: func() *corev1.SecretKeySelector {
							if helpers.UseEnvtest() {
								return &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: customHashedSecretKey.Name,
									},
									Key: "hashedToken",
								}
							}
							return nil
						}(),
					},
				},
			}
			Expect(k8sClient.Create(ctx, customBootstrapToken)).To(Succeed())
			defer suite.CleanupBootstrapToken(ctx, k8sClient, customBootstrapToken)

			// Wait for HumioBootstrapToken to be Ready (even without cluster existing)
			suite.UsingClusterBy(key.Name, "Waiting for HumioBootstrapToken to reach Ready state")
			Eventually(func() string {
				var bootstrapToken humiov1alpha1.HumioBootstrapToken
				if err := k8sClient.Get(ctx, key, &bootstrapToken); err != nil {
					return ""
				}
				return bootstrapToken.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioBootstrapTokenStateReady))

			// Verify the original token value was preserved in token secret
			suite.UsingClusterBy(key.Name, "Verifying original token was preserved")
			var tokenSecret corev1.Secret
			Expect(k8sClient.Get(ctx, customSecretKey, &tokenSecret)).To(Succeed())
			Expect(string(tokenSecret.Data["secret"])).To(Equal(customToken))

			// Verify the bootstrap token status contains correct secret references
			suite.UsingClusterBy(key.Name, "Verifying bootstrap token status")
			var finalBootstrapToken humiov1alpha1.HumioBootstrapToken
			Expect(k8sClient.Get(ctx, key, &finalBootstrapToken)).To(Succeed())
			Expect(finalBootstrapToken.Status.State).To(Equal(humiov1alpha1.HumioBootstrapTokenStateReady))
			Expect(finalBootstrapToken.Status.TokenSecretKeyRef).NotTo(BeNil())
			Expect(finalBootstrapToken.Status.HashedTokenSecretKeyRef).NotTo(BeNil())
		})

		It("Should use existing hashedToken when both tokenSecret and hashedTokenSecret point to same secret with pre-populated hashedToken", Label("envtest", "dummy", "real"), func() {
			key := types.NamespacedName{
				Name:      "humiobootstraptoken-existing-hashed",
				Namespace: testProcessNamespace,
			}

			// Generate a random 32-byte token and base64 encode it
			tokenBytes := make([]byte, 32)
			_, err := rand.Read(tokenBytes)
			Expect(err).ToNot(HaveOccurred())
			customToken := base64.StdEncoding.EncodeToString(tokenBytes)

			// Generate a mock hashed token value
			hashedTokenValue := base64.StdEncoding.EncodeToString([]byte("pre-hashed-" + customToken))

			// Create secret with both plain token and pre-computed hashed token
			secretKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-bootstrap-secret", key.Name),
				Namespace: key.Namespace,
			}

			suite.UsingClusterBy(key.Name, "Creating bootstrap token secret with both plain and hashed tokens")
			combinedSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretKey.Name,
					Namespace: secretKey.Namespace,
					Labels: map[string]string{
						"app.kubernetes.io/instance":   key.Name,
						"app.kubernetes.io/managed-by": "humio-operator",
						"app.kubernetes.io/name":       "humio",
						"humio.com/secret-identifier":  fmt.Sprintf("%s-bootstrap-token", key.Name),
					},
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"secret":      []byte(customToken),
					"hashedToken": []byte(hashedTokenValue),
				},
			}
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, combinedSecret)).To(Succeed())

			// Create HumioBootstrapToken that references the same secret for both plain and hashed tokens
			suite.UsingClusterBy(key.Name, "Creating HumioBootstrapToken resource pointing to same secret")
			bootstrapToken := &humiov1alpha1.HumioBootstrapToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
					Labels: map[string]string{
						"app.kubernetes.io/instance":   key.Name,
						"app.kubernetes.io/managed-by": "humio-operator",
						"app.kubernetes.io/name":       "humio",
						"managed-cluster-name":         key.Name,
					},
				},
				Spec: humiov1alpha1.HumioBootstrapTokenSpec{
					ManagedClusterName: key.Name, // This cluster doesn't need to exist
					TokenSecret: humiov1alpha1.HumioTokenSecretSpec{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: secretKey.Name,
							},
							Key: "secret",
						},
					},
					HashedTokenSecret: humiov1alpha1.HumioHashedTokenSecretSpec{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: secretKey.Name,
							},
							Key: "hashedToken",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, bootstrapToken)).To(Succeed())
			defer suite.CleanupBootstrapToken(ctx, k8sClient, bootstrapToken)

			// Wait for HumioBootstrapToken to reach Ready state
			suite.UsingClusterBy(key.Name, "Waiting for HumioBootstrapToken to reach Ready state")
			Eventually(func() string {
				var bt humiov1alpha1.HumioBootstrapToken
				if err := k8sClient.Get(ctx, key, &bt); err != nil {
					return ""
				}
				return bt.Status.State
			}, testTimeout, suite.TestInterval).Should(Equal(humiov1alpha1.HumioBootstrapTokenStateReady))

			// Verify the original token value was preserved
			suite.UsingClusterBy(key.Name, "Verifying original token was preserved")
			var finalSecret corev1.Secret
			Expect(k8sClient.Get(ctx, secretKey, &finalSecret)).To(Succeed())
			Expect(string(finalSecret.Data["secret"])).To(Equal(customToken))

			// Verify the original hashed token value was preserved (not regenerated)
			suite.UsingClusterBy(key.Name, "Verifying original hashed token was preserved")
			Expect(string(finalSecret.Data["hashedToken"])).To(Equal(hashedTokenValue))

			// Verify the bootstrap token status contains correct secret references
			suite.UsingClusterBy(key.Name, "Verifying bootstrap token status")
			var finalBootstrapToken humiov1alpha1.HumioBootstrapToken
			Expect(k8sClient.Get(ctx, key, &finalBootstrapToken)).To(Succeed())
			Expect(finalBootstrapToken.Status.State).To(Equal(humiov1alpha1.HumioBootstrapTokenStateReady))
			Expect(finalBootstrapToken.Status.TokenSecretKeyRef).NotTo(BeNil())
			Expect(finalBootstrapToken.Status.HashedTokenSecretKeyRef).NotTo(BeNil())
		})
	})
})
