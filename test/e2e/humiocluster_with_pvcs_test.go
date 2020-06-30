package e2e

import (
	goctx "context"
	"fmt"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type humioClusterWithPVCsTest struct {
	cluster *corev1alpha1.HumioCluster
}

func newHumioClusterWithPVCsTest(clusterName string, namespace string) humioClusterTest {
	return &humioClusterWithPVCsTest{
		cluster: &corev1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace,
			},
			Spec: corev1alpha1.HumioClusterSpec{
				NodeCount: 1,
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
				DataVolumePersistentVolumeClaimSpecTemplate: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
	}
}

func (h *humioClusterWithPVCsTest) Start(f *framework.Framework, ctx *framework.Context) error {
	return f.Client.Create(goctx.TODO(), h.cluster, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
}

func (h *humioClusterWithPVCsTest) Wait(f *framework.Framework) error {
	for start := time.Now(); time.Since(start) < timeout; {
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: h.cluster.ObjectMeta.Name, Namespace: h.cluster.ObjectMeta.Namespace}, h.cluster)
		if err != nil {
			fmt.Printf("could not get humio cluster: %s", err)
		}
		if h.cluster.Status.State == corev1alpha1.HumioClusterStateRunning {
			foundPodList, err := kubernetes.ListPods(
				f.Client.Client,
				h.cluster.Namespace,
				kubernetes.MatchingLabelsForHumio(h.cluster.Name),
			)
			if err != nil {
				return fmt.Errorf("got error listing pods after cluster became running: %s", err)
			}

			emptyPersistentVolumeClaim := corev1.PersistentVolumeClaimVolumeSource{}
			var pvcCount int
			for _, pod := range foundPodList {
				for _, volume := range pod.Spec.Volumes {
					if volume.Name == "humio-data" {
						if !reflect.DeepEqual(volume.PersistentVolumeClaim, emptyPersistentVolumeClaim) {
							pvcCount++
						} else {
							return fmt.Errorf("expected pod %s to have a pvc but instead got %+v", pod.Name, volume)
						}
					}
				}
			}

			if pvcCount < h.cluster.Spec.NodeCount {
				return fmt.Errorf("expected to find %d pods with attached pvcs but instead got %d", h.cluster.Spec.NodeCount, pvcCount)
			}
			return nil
		}

		if foundPodList, err := kubernetes.ListPods(
			f.Client.Client,
			h.cluster.Namespace,
			kubernetes.MatchingLabelsForHumio(h.cluster.Name),
		); err != nil {
			for _, pod := range foundPodList {
				fmt.Println(fmt.Sprintf("pod %s status: %#v", pod.Name, pod.Status))
			}
		}

		time.Sleep(time.Second * 10)
	}

	return fmt.Errorf("timed out waiting for cluster state to become: %s", corev1alpha1.HumioClusterStateRunning)
}
