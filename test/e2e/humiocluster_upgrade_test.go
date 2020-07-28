package e2e

import (
	goctx "context"
	"fmt"
	"time"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type upgradeTest struct {
	cluster   *corev1alpha1.HumioCluster
	bootstrap testState
	upgrade   testState
}

func newHumioClusterWithUpgradeTest(clusterName string, namespace string) humioClusterTest {
	return &upgradeTest{
		cluster: &corev1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace,
			},
			Spec: corev1alpha1.HumioClusterSpec{
				NodeCount: 2,
				EnvironmentVariables: []corev1.EnvVar{
					{
						Name:  "ZOOKEEPER_URL",
						Value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless.default:2181",
					},
					{
						Name:  "KAFKA_SERVERS",
						Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
					},
				},
			},
		},
	}
}

func (b *upgradeTest) Start(f *framework.Framework, ctx *framework.Context) error {
	b.bootstrap.initiated = true
	return f.Client.Create(goctx.TODO(), b.cluster, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
}

func (b *upgradeTest) Wait(f *framework.Framework) error {
	var gotUpgraded bool
	for start := time.Now(); time.Since(start) < timeout; {
		// return after all tests have completed
		if b.bootstrap.passed && b.upgrade.passed {
			return nil
		}

		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: b.cluster.ObjectMeta.Name, Namespace: b.cluster.ObjectMeta.Namespace}, b.cluster)
		if err != nil {
			fmt.Printf("could not get humio cluster: %s", err)
		}

		clusterState := b.cluster.Status.State
		clusterPodRevision := b.cluster.Annotations[podRevisionAnnotation]

		if clusterState == corev1alpha1.HumioClusterStateRunning {
			b.bootstrap.passed = true
		}

		foundPodList, err := kubernetes.ListPods(
			f.Client.Client,
			b.cluster.Namespace,
			kubernetes.MatchingLabelsForHumio(b.cluster.Name),
		)
		if err != nil {
			for _, pod := range foundPodList {
				fmt.Println(fmt.Sprintf("pod %s status: %#v", pod.Name, pod.Status))
			}
		}

		if b.upgrade.initiated {
			if !b.upgrade.passed {
				if clusterState == corev1alpha1.HumioClusterStateUpgrading {
					gotUpgraded = true
				}
				if clusterState == corev1alpha1.HumioClusterStateRunning {
					if !gotUpgraded {
						return fmt.Errorf("never went into upgrading state")
					}
					if clusterPodRevision != "2" {
						return fmt.Errorf("got wrong cluster pod revision when upgrading: expected: 2 got: %s", clusterPodRevision)
					}
					for _, pod := range foundPodList {
						if pod.Annotations[podRevisionAnnotation] != clusterPodRevision {
							if pod.Annotations[podRevisionAnnotation] != clusterPodRevision {
								return fmt.Errorf("got wrong pod revision when upgrading: expected: %s got: %s", clusterPodRevision, pod.Annotations[podRevisionAnnotation])
							}
						}
					}
					b.upgrade.passed = true
				}
			}
		} else {
			if b.bootstrap.passed {
				if clusterPodRevision != "1" {
					return fmt.Errorf("got wrong cluster pod revision before upgrading: expected: 1 got: %s", clusterPodRevision)
				}

				b.cluster.Spec.Image = "humio/humio-core:1.13.0"
				f.Client.Update(goctx.TODO(), b.cluster)
				b.upgrade.initiated = true
			}
		}

		time.Sleep(time.Second * 10)
	}
	if !b.bootstrap.passed {
		return fmt.Errorf("timed out waiting for cluster state to become: %s", corev1alpha1.HumioClusterStateRunning)
	}
	return fmt.Errorf("timed out waiting for cluster to upgrade")
}
