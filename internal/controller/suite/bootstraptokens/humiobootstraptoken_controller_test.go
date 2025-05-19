package bootstraptokens

import (
	"context"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
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
	})
})
