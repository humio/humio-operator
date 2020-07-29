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

type parserTest struct {
	test   *testing.T
	parser *corev1alpha1.HumioParser
}

func newParserTest(test *testing.T, clusterName string, namespace string) humioClusterTest {
	return &parserTest{
		test: test,
		parser: &corev1alpha1.HumioParser{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "example-parser",
				Namespace: namespace,
			},
			Spec: corev1alpha1.HumioParserSpec{
				ManagedClusterName: clusterName,
				Name:               "example-parser",
				RepositoryName:     "humio",
				ParserScript:       "kvParse()",
				TagFields:          []string{"@somefield"},
				TestData:           []string{"testdata"},
			},
		},
	}
}

func (p *parserTest) Start(f *framework.Framework, ctx *framework.Context) error {
	return f.Client.Create(goctx.TODO(), p.parser, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
}

func (p *parserTest) Update(_ *framework.Framework) error {
	return nil
}

func (p *parserTest) Teardown(f *framework.Framework) error {
	return f.Client.Delete(goctx.TODO(), p.parser)
}

func (p *parserTest) Wait(f *framework.Framework) error {
	for start := time.Now(); time.Since(start) < timeout; {
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: p.parser.ObjectMeta.Name, Namespace: p.parser.ObjectMeta.Namespace}, p.parser)
		if err != nil {
			p.test.Logf("could not get humio parser: %s", err)
		}
		if p.parser.Status.State == corev1alpha1.HumioParserStateExists {
			return nil
		}
		time.Sleep(time.Second * 2)
	}
	return fmt.Errorf("timed out waiting for parser state to become: %s", corev1alpha1.HumioParserStateExists)
}
