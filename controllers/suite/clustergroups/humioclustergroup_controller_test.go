package clustergroups

import (
	"context"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/controllers/suite"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("HumioClusterGroup Controller", func() {
	Context("Humio ClusterGroup GC", func() {
		It("Should cleanup HumioClusterGroup locks correctly", func() {
			key := types.NamespacedName{
				Name:      "humioclustergroup-gc",
				Namespace: testProcessNamespace,
			}
			toCreate := &humiov1alpha1.HumioClusterGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterGroupSpec{},
			}

			suite.UsingClusterBy(key.Name, "Creating the clustergroup successfully")
			ctx := context.Background()
			humioClusterKey1 := types.NamespacedName{
				Name:      "test-cluster-1",
				Namespace: testProcessNamespace,
			}
			humioClusterKey2 := types.NamespacedName{
				Name:      "test-cluster-2",
				Namespace: testProcessNamespace,
			}
			humioCluster1 := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      humioClusterKey1.Name,
					Namespace: humioClusterKey1.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{},
			}
			humioCluster2 := &humiov1alpha1.HumioCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      humioClusterKey2.Name,
					Namespace: humioClusterKey2.Namespace,
				},
				Spec: humiov1alpha1.HumioClusterSpec{},
			}

			suite.UsingClusterBy(key.Name, "Creating the clustergroup successfully")
			defer k8sClient.Delete(ctx, toCreate)
			Expect(k8sClient.Create(ctx, toCreate)).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Creating the humioclusters successfully")
			defer k8sClient.Delete(ctx, humioCluster1)
			defer k8sClient.Delete(ctx, humioCluster2)
			Expect(k8sClient.Create(ctx, humioCluster1)).Should(Succeed())
			Expect(k8sClient.Create(ctx, humioCluster2)).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Waiting for the humioclusters to exist")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, humioClusterKey1, humioCluster1); err != nil {
					return err
				}
				if err := k8sClient.Get(ctx, humioClusterKey2, humioCluster2); err != nil {
					return err
				}
				return nil
			}, testTimeout, suite.TestInterval).Should(BeNil())

			suite.UsingClusterBy(key.Name, "Updating the clustergroup with the humiocluster status successfully")
			updatedHumioClusterGroup := toCreate
			updatedHumioClusterGroup.Status = []humiov1alpha1.HumioClusterGroupStatusHumioClusterStatusAggregate{
				{
					Type:            humiov1alpha1.HumioClusterStateRestarting,
					InProgressCount: 1,
					ClusterList: []humiov1alpha1.HumioClusterGroupStatusHumioClusterStatus{
						{
							ClusterName:    humioCluster1.Name,
							ClusterState:   humiov1alpha1.HumioClusterStateRestarting,
							LastUpdateTime: &metav1.Time{Time: time.Now()},
						},
					},
				},
				{
					Type:            humiov1alpha1.HumioClusterStateUpgrading,
					InProgressCount: 1,
					ClusterList: []humiov1alpha1.HumioClusterGroupStatusHumioClusterStatus{
						{
							ClusterName:    humioCluster2.Name,
							ClusterState:   humiov1alpha1.HumioClusterStateUpgrading,
							LastUpdateTime: &metav1.Time{Time: time.Now()},
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, updatedHumioClusterGroup)).Should(Succeed())

			suite.UsingClusterBy(key.Name, "Verifying the clustergroup contains the humiocluster status")
			Eventually(func() int {
				if err := k8sClient.Get(ctx, key, updatedHumioClusterGroup); err != nil {
					return -1
				}
				var numClustersLocked int
				for _, clusterStatusAggregate := range updatedHumioClusterGroup.Status {
					numClustersLocked += len(clusterStatusAggregate.ClusterList)
				}
				return numClustersLocked
			}, testTimeout, suite.TestInterval).Should(Equal(2))

			suite.UsingClusterBy(key.Name, "Deleting the humioclusters")

			Expect(k8sClient.Delete(ctx, humioCluster1)).Should(Succeed())
			Eventually(func() interface{} {
				if err := k8sClient.Get(ctx, humioClusterKey1, humioCluster1); err != nil {
					if k8serrors.IsNotFound(err) {
						return true
					}
				}
				return false
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			Expect(k8sClient.Delete(ctx, humioCluster2)).Should(Succeed())
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, humioClusterKey2, humioCluster2); err != nil {
					if k8serrors.IsNotFound(err) {
						return true
					}
				}
				return false
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Ensuring the humioclustergroup releases the lock for the removed humioclusters")
			Eventually(func() interface{} {
				if err := k8sClient.Get(ctx, key, updatedHumioClusterGroup); err != nil {
					return -1
				}
				var numClustersLocked int
				for _, clusterStatusAggregate := range updatedHumioClusterGroup.Status {
					numClustersLocked += len(clusterStatusAggregate.ClusterList)
				}
				return numClustersLocked
			}, testTimeout, suite.TestInterval).Should(Equal(0))
		})
	})
})
