package e2e

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/humio/humio-operator/pkg/apis"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	retryInterval        = time.Second * 5
	timeout              = time.Second * 300
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5
)

type humioClusterTest interface {
	Start(f *framework.Framework, ctx *framework.Context) error
	Wait(f *framework.Framework) error
}

func TestHumioCluster(t *testing.T) {
	schemes := []runtime.Object{
		&corev1alpha1.HumioClusterList{},
		&corev1alpha1.HumioIngestTokenList{},
		&corev1alpha1.HumioParserList{},
		&corev1alpha1.HumioRepositoryList{},
	}

	for _, scheme := range schemes {
		err := framework.AddToFrameworkScheme(apis.AddToScheme, scheme)
		if err != nil {
			t.Fatalf("failed to add custom resource scheme to framework: %v", err)
		}
	}

	t.Run("humiocluster-group", func(t *testing.T) {
		t.Run("cluster", HumioCluster)
		t.Run("pvc-cluster", HumioClusterWithPVCs)
	})
}

func HumioCluster(t *testing.T) {
	t.Parallel()
	ctx := framework.NewContext(t)
	defer ctx.Cleanup()
	err := ctx.InitializeClusterResources(&framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		t.Fatalf("failed to initialize cluster resources: %v", err)
	}
	t.Log("Initialized cluster resources")

	// GetNamespace creates a namespace if it doesn't exist
	namespace, _ := ctx.GetOperatorNamespace()

	// get global framework variables
	f := framework.Global

	// wait for humio-operator to be ready
	err = e2eutil.WaitForOperatorDeployment(t, f.KubeClient, namespace, "humio-operator", 1, retryInterval, timeout)
	if err != nil {
		t.Fatal(err)
	}

	// run the tests
	clusterName := "example-humiocluster"
	tests := []humioClusterTest{
		newBootstrapTest(clusterName, namespace),
		newIngestTokenTest(clusterName, namespace),
		newParserTest(clusterName, namespace),
		newRepositoryTest(clusterName, namespace),
	}

	go printKubectlcommands(t, namespace)

	for _, test := range tests {
		if err = test.Start(f, ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Wait(f); err != nil {
			t.Fatal(err)
		}
	}
}

// TODO: Run this in the HumioCluster function once we support multiple namespaces
func HumioClusterWithPVCs(t *testing.T) {
	t.Parallel()
	ctx := framework.NewContext(t)
	defer ctx.Cleanup()
	err := ctx.InitializeClusterResources(&framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		t.Fatalf("failed to initialize cluster resources: %v", err)
	}
	t.Log("Initialized cluster resources")

	// GetNamespace creates a namespace if it doesn't exist
	namespace, _ := ctx.GetOperatorNamespace()

	// get global framework variables
	f := framework.Global

	// wait for humio-operator to be ready
	err = e2eutil.WaitForOperatorDeployment(t, f.KubeClient, namespace, "humio-operator", 1, retryInterval, timeout)
	if err != nil {
		t.Fatal(err)
	}

	// run the tests
	clusterName := "example-humiocluster-pvc"
	tests := []humioClusterTest{
		newHumioClusterWithPVCsTest(clusterName, namespace),
	}

	go printKubectlcommands(t, namespace)

	for _, test := range tests {
		if err = test.Start(f, ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Wait(f); err != nil {
			t.Fatal(err)
		}
	}
}

func printKubectlcommands(t *testing.T, namespace string) {
	commands := []string{
		"kubectl get pods -A",
		fmt.Sprintf("kubectl describe pods -n %s", namespace),
		fmt.Sprintf("kubectl logs deploy/humio-operator -n %s", namespace),
	}

	ticker := time.NewTicker(time.Second * 5)
	for range ticker.C {
		for _, command := range commands {
			cmd := exec.Command("bash", "-c", command)
			stdoutStderr, err := cmd.CombinedOutput()
			t.Log(fmt.Sprintf("%s, %s\n", stdoutStderr, err))
		}
	}
}
