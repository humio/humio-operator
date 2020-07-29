package e2e

import (
	"fmt"
	"os/exec"
	"sync"
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
	timeout              = time.Second * 600
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5
)

type humioClusterTest interface {
	Start(*framework.Framework, *framework.Context) error
	Update(*framework.Framework) error
	Teardown(*framework.Framework) error
	Wait(*framework.Framework) error
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
		t.Run("cluster-restart", HumioClusterRestart)
		t.Run("cluster-upgrade", HumioClusterUpgrade)
		t.Run("tls-cluster", HumioClusterWithTLS)
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
		newBootstrapTest(t, clusterName, namespace), // we cannot tear this down until the other 3 tests are done.

		// The 3 tests below depends on the cluster from "newBootstrapTest" running.
		// TODO: Fix the race between tearing down the operator and waiting for it to run the finalizers for the CR's.
		//       If the operator goes away too early, the CR's will be stuck due to CR's finalizers not being run.
		newIngestTokenTest(t, clusterName, namespace),
		newParserTest(t, clusterName, namespace),
		newRepositoryTest(t, clusterName, namespace),
	}

	// print kubectl commands until the tests are complete. ensure we wait for the last kubectl command to complete
	// before exiting to avoid trying to exec a kubectl command after the test has shut down
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan bool, 1)
	go printKubectlcommands(t, namespace, &wg, done)

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
	for _, test := range tests {
		if err = test.Update(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Wait(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Teardown(f); err != nil {
			t.Fatal(err)
		}
	}

	done <- true
	wg.Wait()
}

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
		newHumioClusterWithPVCsTest(t, fmt.Sprintf("%s-tls-disabled", clusterName), namespace, false),
		newHumioClusterWithPVCsTest(t, fmt.Sprintf("%s-tls-enabled", clusterName), namespace, true),
	}

	// print kubectl commands until the tests are complete. ensure we wait for the last kubectl command to complete
	// before exiting to avoid trying to exec a kubectl command after the test has shut down
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan bool, 1)
	go printKubectlcommands(t, namespace, &wg, done)

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
	for _, test := range tests {
		if err = test.Update(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Wait(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Teardown(f); err != nil {
			t.Fatal(err)
		}
	}

	done <- true
	wg.Wait()
}

func HumioClusterWithTLS(t *testing.T) {
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
	clusterName := "example-humiocluster-tls"
	tests := []humioClusterTest{
		newHumioClusterWithTLSTest(t, fmt.Sprintf("%s-enabled-to-disabled", clusterName), namespace, true, false), // OK, runtime 205 seconds
		newHumioClusterWithTLSTest(t, fmt.Sprintf("%s-disabled-to-enabled", clusterName), namespace, false, true), // TODO: Validate if this works by itself
	}

	// print kubectl commands until the tests are complete. ensure we wait for the last kubectl command to complete
	// before exiting to avoid trying to exec a kubectl command after the test has shut down
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan bool, 1)
	go printKubectlcommands(t, namespace, &wg, done)

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
	for _, test := range tests {
		if err = test.Update(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Wait(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Teardown(f); err != nil {
			t.Fatal(err)
		}
	}

	done <- true
	wg.Wait()
}

func HumioClusterRestart(t *testing.T) {
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
	clusterName := "example-humiocluster-restart"
	tests := []humioClusterTest{
		newHumioClusterWithRestartTest(fmt.Sprintf("%s-tls-disabled", clusterName), namespace, false),
		newHumioClusterWithRestartTest(fmt.Sprintf("%s-tls-enabled", clusterName), namespace, true),
	}

	// print kubectl commands until the tests are complete. ensure we wait for the last kubectl command to complete
	// before exiting to avoid trying to exec a kubectl command after the test has shut down
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan bool, 1)
	go printKubectlcommands(t, namespace, &wg, done)

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
	for _, test := range tests {
		if err = test.Update(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Wait(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Teardown(f); err != nil {
			t.Fatal(err)
		}
	}

	done <- true
	wg.Wait()
}

func HumioClusterUpgrade(t *testing.T) {
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
	clusterName := "example-humiocluster-upgrade"
	tests := []humioClusterTest{
		newHumioClusterWithUpgradeTest(fmt.Sprintf("%s-tls-disabled", clusterName), namespace, false),
		newHumioClusterWithUpgradeTest(fmt.Sprintf("%s-tls-enabled", clusterName), namespace, true),
	}

	// print kubectl commands until the tests are complete. ensure we wait for the last kubectl command to complete
	// before exiting to avoid trying to exec a kubectl command after the test has shut down
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan bool, 1)
	go printKubectlcommands(t, namespace, &wg, done)

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
	for _, test := range tests {
		if err = test.Update(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Wait(f); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range tests {
		if err = test.Teardown(f); err != nil {
			t.Fatal(err)
		}
	}

	done <- true
	wg.Wait()
}

func printKubectlcommands(t *testing.T, namespace string, wg *sync.WaitGroup, done <-chan bool) {
	defer wg.Done()

	commands := []string{
		"kubectl get pods -A",
		fmt.Sprintf("kubectl describe pods -n %s", namespace),
		fmt.Sprintf("kubectl describe persistentvolumeclaims -n %s", namespace),
		fmt.Sprintf("kubectl logs deploy/humio-operator -n %s", namespace),
	}

	ticker := time.NewTicker(time.Second * 5)
	for range ticker.C {
		select {
		case <-done:
			return
		default:
		}

		for _, command := range commands {
			cmd := exec.Command("bash", "-c", command)
			stdoutStderr, err := cmd.CombinedOutput()
			t.Log(fmt.Sprintf("%s, %s\n", stdoutStderr, err))
		}
	}
}
