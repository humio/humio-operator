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

const (
	podRevisionAnnotation = "humio.com/pod-revision"
)

type restartTest struct {
	cluster   *corev1alpha1.HumioCluster
	bootstrap testState
	restart   testState
}

type testState struct {
	initiated bool
	passed    bool
}

func newHumioClusterWithRestartTest(clusterName string, namespace string) humioClusterTest {
	return &restartTest{
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

func (b *restartTest) Start(f *framework.Framework, ctx *framework.Context) error {
	b.bootstrap.initiated = true
	return f.Client.Create(goctx.TODO(), b.cluster, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
}

func (b *restartTest) Wait(f *framework.Framework) error {
	var gotRestarted bool
	for start := time.Now(); time.Since(start) < timeout; {
		// return after all tests have completed
		if b.bootstrap.passed && b.restart.passed {
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

		if b.restart.initiated {
			if !b.restart.passed {
				if clusterState == corev1alpha1.HumioClusterStateRestarting {
					gotRestarted = true
				}
				if clusterState == corev1alpha1.HumioClusterStateRunning {
					if !gotRestarted {
						return fmt.Errorf("error never went into restarting state when restarting: %+v", b.cluster)
					}
					if clusterPodRevision != "2" {
						return fmt.Errorf("got wrong cluster pod revision when restarting: expected: 2 got: %s", clusterPodRevision)
					}
					for _, pod := range foundPodList {
						if pod.Annotations[podRevisionAnnotation] != clusterPodRevision {
							if pod.Annotations[podRevisionAnnotation] != clusterPodRevision {
								return fmt.Errorf("got wrong pod revision when restarting: expected: %s got: %s", clusterPodRevision, pod.Annotations[podRevisionAnnotation])
							}
						}
					}
					b.restart.passed = true
				}
			}
		} else {
			if b.bootstrap.passed {
				if clusterPodRevision != "1" {
					return fmt.Errorf("got wrong cluster pod revision before restarting: expected: 1 got: %s", clusterPodRevision)
				}

				b.cluster.Spec.EnvironmentVariables = append(b.cluster.Spec.EnvironmentVariables, corev1.EnvVar{
					Name:  "SOME_ENV_VAR",
					Value: "some value",
				})
				f.Client.Update(goctx.TODO(), b.cluster)
				b.restart.initiated = true
			}
		}

		time.Sleep(time.Second * 10)
	}
	if !b.bootstrap.passed {
		return fmt.Errorf("timed out waiting for cluster state to become: %s", corev1alpha1.HumioClusterStateRunning)
	}
	return fmt.Errorf("timed out waiting for cluster to upgrade")
}
