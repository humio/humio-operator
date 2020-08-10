package e2e

import (
	goctx "context"
	"fmt"
	"testing"
	"time"

	"github.com/humio/humio-operator/pkg/helpers"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type bootstrapTest struct {
	test    *testing.T
	cluster *corev1alpha1.HumioCluster
}

func newBootstrapTest(test *testing.T, clusterName string, namespace string) humioClusterTest {
	return &bootstrapTest{
		test: test,
		cluster: &corev1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace,
			},
			Spec: corev1alpha1.HumioClusterSpec{
				NodeCount: helpers.IntPtr(1),
				TLS: &corev1alpha1.HumioClusterTLSSpec{
					Enabled: helpers.BoolPtr(false),
				},
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

func (b *bootstrapTest) Start(f *framework.Framework, ctx *framework.Context) error {
	b.cluster.Spec.EnvironmentVariables = append(b.cluster.Spec.EnvironmentVariables,
		corev1.EnvVar{
			Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
			Value: b.cluster.Name,
		},
	)
	return f.Client.Create(goctx.TODO(), b.cluster, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
}

func (b *bootstrapTest) Update(_ *framework.Framework) error {
	return nil
}

func (b *bootstrapTest) Teardown(_ *framework.Framework) error {
	// we have to keep this cluster running as other tests depend on this cluster being available. Tests that validate parsers, ingest tokens, repositories.
	return nil
}

func (b *bootstrapTest) Wait(f *framework.Framework) error {
	for start := time.Now(); time.Since(start) < timeout; {
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: b.cluster.ObjectMeta.Name, Namespace: b.cluster.ObjectMeta.Namespace}, b.cluster)
		if err != nil {
			b.test.Logf("could not get humio cluster: %s", err)
		}
		if b.cluster.Status.State == corev1alpha1.HumioClusterStateRunning {
			return nil
		}

		if foundPodList, err := kubernetes.ListPods(
			f.Client.Client,
			b.cluster.Namespace,
			kubernetes.MatchingLabelsForHumio(b.cluster.Name),
		); err != nil {
			for _, pod := range foundPodList {
				b.test.Logf("pod %s status: %#v", pod.Name, pod.Status)
			}
		}

		time.Sleep(time.Second * 10)
	}

	return fmt.Errorf("timed out waiting for cluster state to become: %s", corev1alpha1.HumioClusterStateRunning)
}
