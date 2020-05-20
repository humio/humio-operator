package e2e

import (
	goctx "context"
	"fmt"
	"time"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type ingestTokenTest struct {
	ingestToken *corev1alpha1.HumioIngestToken
}

func newIngestTokenTest(clusterName string, namespace string) humioClusterTest {
	return &ingestTokenTest{
		ingestToken: &corev1alpha1.HumioIngestToken{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "example-humioingesttoken",
				Namespace: namespace,
			},
			Spec: corev1alpha1.HumioIngestTokenSpec{
				ManagedClusterName: clusterName,
				Name:               "example-humioingesttoken",
				RepositoryName:     "humio",
				TokenSecretName:    "ingest-token-secret",
			},
		},
	}
}

func (i *ingestTokenTest) Start(f *framework.Framework, ctx *framework.Context) error {
	return f.Client.Create(goctx.TODO(), i.ingestToken, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
}

func (i *ingestTokenTest) Wait(f *framework.Framework) error {
	for start := time.Now(); time.Since(start) < timeout; {
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: i.ingestToken.ObjectMeta.Name, Namespace: i.ingestToken.ObjectMeta.Namespace}, i.ingestToken)
		if err != nil {
			fmt.Printf("could not get humio ingest token: %s", err)
		}
		if i.ingestToken.Status.State == corev1alpha1.HumioIngestTokenStateExists {
			return nil
		}
		time.Sleep(time.Second * 2)
	}

	return fmt.Errorf("timed out waiting for ingest token state to become: %s", corev1alpha1.HumioIngestTokenStateExists)
}
