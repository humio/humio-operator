package e2e

import (
	goctx "context"
	"fmt"
	"testing"
	"time"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type repositoryTest struct {
	test       *testing.T
	repository *corev1alpha1.HumioRepository
}

func newRepositoryTest(test *testing.T, clusterName string, namespace string) humioClusterTest {
	return &repositoryTest{
		test: test,
		repository: &corev1alpha1.HumioRepository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "example-repository",
				Namespace: namespace,
			},
			Spec: corev1alpha1.HumioRepositorySpec{
				ManagedClusterName: clusterName,
				Name:               "example-repository",
				Description:        "this is an important message",
				Retention: corev1alpha1.HumioRetention{
					IngestSizeInGB:  5,
					StorageSizeInGB: 1,
					TimeInDays:      7,
				},
			},
		},
	}
}

func (r *repositoryTest) Start(f *framework.Framework, ctx *framework.Context) error {
	return f.Client.Create(goctx.TODO(), r.repository, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
}

func (r *repositoryTest) Update(_ *framework.Framework) error {
	return nil
}

func (r *repositoryTest) Teardown(f *framework.Framework) error {
	return f.Client.Delete(goctx.TODO(), r.repository)
}

func (r *repositoryTest) Wait(f *framework.Framework) error {
	for start := time.Now(); time.Since(start) < timeout; {
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: r.repository.ObjectMeta.Name, Namespace: r.repository.ObjectMeta.Namespace}, r.repository)
		if err != nil {
			r.test.Logf("could not get humio repository: %s", err)
		}
		if r.repository.Status.State == corev1alpha1.HumioRepositoryStateExists {
			return nil
		}
		time.Sleep(time.Second * 2)
	}
	return fmt.Errorf("timed out waiting for repository state to become: %s", corev1alpha1.HumioRepositoryStateExists)
}
