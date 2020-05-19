package e2e

import (
	goctx "context"
	"fmt"
	"testing"
	"time"

	"github.com/humio/humio-operator/pkg/apis"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var (
	retryInterval        = time.Second * 5
	timeout              = time.Second * 300
	cleanupRetryInterval = time.Second * 1
	cleanupTimeout       = time.Second * 5
)

func TestHumioCluster(t *testing.T) {
	HumioClusterList := &corev1alpha1.HumioClusterList{}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, HumioClusterList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}
	HumioIngestTokenList := &corev1alpha1.HumioIngestTokenList{}
	err = framework.AddToFrameworkScheme(apis.AddToScheme, HumioIngestTokenList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}
	HumioParserList := &corev1alpha1.HumioParserList{}
	err = framework.AddToFrameworkScheme(apis.AddToScheme, HumioParserList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}
	HumioRepositoryList := &corev1alpha1.HumioRepositoryList{}
	err = framework.AddToFrameworkScheme(apis.AddToScheme, HumioRepositoryList)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}
	// run subtests
	t.Run("humiocluster-group", func(t *testing.T) {
		t.Run("cluster", HumioCluster)
	})
}

func HumioCluster(t *testing.T) {
	t.Parallel()
	ctx := framework.NewTestCtx(t)
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
	clusterName := "example-humiocluster"
	if err = HumioClusterBootstrapTest(t, f, ctx, clusterName); err != nil {
		t.Fatal(err)
	}
	if err = HumioIngestTokenTest(t, f, ctx, clusterName); err != nil {
		t.Fatal(err)
	}
	if err = HumioParserTest(t, f, ctx, clusterName); err != nil {
		t.Fatal(err)
	}
	if err = HumioRepositoryTest(t, f, ctx, clusterName); err != nil {
		t.Fatal(err)
	}
}

func HumioClusterBootstrapTest(t *testing.T, f *framework.Framework, ctx *framework.TestCtx, clusterName string) error {
	namespace, _ := ctx.GetWatchNamespace()

	// create HumioCluster custom resource
	exampleHumioCluster := &corev1alpha1.HumioCluster{
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
		},
	}
	// use TestCtx's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(goctx.TODO(), exampleHumioCluster, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		return err
	}

	for i := 0; i < 30; i++ {
		err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: exampleHumioCluster.ObjectMeta.Name, Namespace: namespace}, exampleHumioCluster)
		if err != nil {
			fmt.Printf("could not get humio cluster: %s", err)
		}
		if exampleHumioCluster.Status.State == corev1alpha1.HumioClusterStateRunning {
			return nil
		}

		if foundPodList, err := kubernetes.ListPods(
			f.Client.Client,
			exampleHumioCluster.Namespace,
			kubernetes.MatchingLabelsForHumio(exampleHumioCluster.Name),
		); err != nil {
			for _, pod := range foundPodList {
				fmt.Println(fmt.Sprintf("pod %s status: %#v", pod.Name, pod.Status))
			}
		}

		time.Sleep(time.Second * 10)
	}

	return fmt.Errorf("timed out waiting for cluster state to become: %s", corev1alpha1.HumioClusterStateRunning)
}

func HumioIngestTokenTest(t *testing.T, f *framework.Framework, ctx *framework.TestCtx, clusterName string) error {
	namespace, _ := ctx.GetWatchNamespace()

	// create HumioIngestToken custom resource
	exampleHumioIngestToken := &corev1alpha1.HumioIngestToken{
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
	}
	// use TestCtx's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(goctx.TODO(), exampleHumioIngestToken, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		return err
	}

	for i := 0; i < 5; i++ {
		err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: exampleHumioIngestToken.ObjectMeta.Name, Namespace: namespace}, exampleHumioIngestToken)
		if err != nil {
			fmt.Printf("could not get humio ingest token: %s", err)
		}

		if exampleHumioIngestToken.Status.State == corev1alpha1.HumioIngestTokenStateExists {
			return nil
		}

		time.Sleep(time.Second * 2)
	}

	return fmt.Errorf("timed out waiting for ingest token state to become: %s", corev1alpha1.HumioIngestTokenStateExists)
}

func HumioParserTest(t *testing.T, f *framework.Framework, ctx *framework.TestCtx, clusterName string) error {
	namespace, _ := ctx.GetWatchNamespace()

	// create HumioParser custom resource
	exampleHumioParser := &corev1alpha1.HumioParser{
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
	}
	// use TestCtx's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(goctx.TODO(), exampleHumioParser, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		return err
	}

	for i := 0; i < 5; i++ {
		err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: exampleHumioParser.ObjectMeta.Name, Namespace: namespace}, exampleHumioParser)
		if err != nil {
			fmt.Printf("could not get humio parser: %s", err)
		}

		if exampleHumioParser.Status.State == corev1alpha1.HumioParserStateExists {
			return nil
		}

		time.Sleep(time.Second * 2)
	}

	return fmt.Errorf("timed out waiting for parser state to become: %s", corev1alpha1.HumioParserStateExists)
}

func HumioRepositoryTest(t *testing.T, f *framework.Framework, ctx *framework.TestCtx, clusterName string) error {
	namespace, _ := ctx.GetWatchNamespace()

	// create HumioParser custom resource
	exampleHumioRepository := &corev1alpha1.HumioRepository{
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
	}
	// use TestCtx's create helper to create the object and add a cleanup function for the new object
	err := f.Client.Create(goctx.TODO(), exampleHumioRepository, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
	if err != nil {
		return err
	}

	for i := 0; i < 5; i++ {
		err = f.Client.Get(goctx.TODO(), types.NamespacedName{Name: exampleHumioRepository.ObjectMeta.Name, Namespace: namespace}, exampleHumioRepository)
		if err != nil {
			fmt.Printf("could not get humio repository: %s", err)
		}

		if exampleHumioRepository.Status.State == corev1alpha1.HumioRepositoryStateExists {
			return nil
		}

		time.Sleep(time.Second * 2)
	}

	return fmt.Errorf("timed out waiting for repository state to become: %s", corev1alpha1.HumioRepositoryStateExists)
}
