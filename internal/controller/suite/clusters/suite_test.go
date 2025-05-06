/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clusters

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	uberzap "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var testEnv *envtest.Environment
var k8sManager ctrl.Manager
var testHumioClient humio.Client
var testTimeout time.Duration
var testProcessNamespace string
var err error

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "HumioCluster Controller Suite")
}

var _ = BeforeSuite(func() {
	var log logr.Logger
	zapLog, _ := helpers.NewLogger()
	defer func(zapLog *uberzap.Logger) {
		_ = zapLog.Sync()
	}(zapLog)
	log = zapr.NewLogger(zapLog).WithSink(GinkgoLogr.GetSink())
	logf.SetLogger(log)

	By("bootstrapping test environment")
	useExistingCluster := true
	testProcessNamespace = fmt.Sprintf("e2e-clusters-%d", GinkgoParallelProcess())
	if !helpers.UseEnvtest() {
		testEnv = &envtest.Environment{
			UseExistingCluster: &useExistingCluster,
		}
		if helpers.UseDummyImage() {
			// We use kind with dummy images instead of the real humio/humio-core container images
			testTimeout = time.Second * 180
			testHumioClient = humio.NewMockClient()
		} else {
			// We use kind with real humio/humio-core container images
			testTimeout = time.Second * 900
			testHumioClient = humio.NewClient(log, "")
			By("Verifying we have a valid license, as tests will require starting up real LogScale containers")
			Expect(helpers.GetE2ELicenseFromEnvVar()).NotTo(BeEmpty())
		}
	} else {
		// We use envtest to run tests
		testTimeout = time.Second * 30
		testEnv = &envtest.Environment{
			// TODO: If we want to add support for TLS-functionality, we need to install cert-manager's CRD's
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing: true,
		}
		testHumioClient = humio.NewMockClient()
	}

	var cfg *rest.Config

	Eventually(func() error {
		// testEnv.Start() sporadically fails with "unable to grab random port for serving webhooks on", so let's
		// retry a couple of times
		cfg, err = testEnv.Start()
		if err != nil {
			By(fmt.Sprintf("Got error trying to start testEnv, retrying... err=%v", err))
		}
		return err
	}, 30*time.Second, 5*time.Second).Should(Succeed())
	Expect(cfg).NotTo(BeNil())

	if helpers.UseCertManager() {
		err = cmapi.AddToScheme(scheme.Scheme)
		Expect(err).NotTo(HaveOccurred())
	}

	err = humiov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Logger:  log,
	})
	Expect(err).NotTo(HaveOccurred())

	var requeuePeriod time.Duration

	err = (&controller.HumioClusterReconciler{
		Client: k8sManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: testHumioClient,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioExternalClusterReconciler{
		Client: k8sManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: testHumioClient,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioPdfRenderServiceReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		BaseLogger: log,
		Namespace:  testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).NotTo(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).NotTo(BeNil())

	By(fmt.Sprintf("Creating test namespace: %s", testProcessNamespace))
	testNamespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testProcessNamespace,
		},
	}
	err = k8sClient.Create(context.TODO(), &testNamespace)
	Expect(err).ToNot(HaveOccurred())

	suite.CreateDockerRegredSecret(context.TODO(), testNamespace, k8sClient)
})

var _ = AfterSuite(func() {
	if testProcessNamespace != "" && k8sClient != nil {
		By(fmt.Sprintf("Removing regcred secret for namespace: %s", testProcessNamespace))
		_ = k8sClient.Delete(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      suite.DockerRegistryCredentialsSecretName,
				Namespace: testProcessNamespace,
			},
		})

		By(fmt.Sprintf("Removing test namespace: %s", testProcessNamespace))
		err := k8sClient.Delete(context.TODO(),
			&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testProcessNamespace,
				},
			},
		)
		Expect(err).ToNot(HaveOccurred())
	}
	By("Tearing down the test environment")
	_ = testEnv.Stop()
})

var _ = ReportAfterSuite("HumioCluster Controller Suite", func(suiteReport ginkgotypes.Report) {
	for _, r := range suiteReport.SpecReports {
		testRunID := fmt.Sprintf("ReportAfterSuite-%s", kubernetes.RandomString())

		// Don't print CapturedGinkgoWriterOutput and CapturedStdOutErr for now as they end up being logged 3 times.
		// Ginkgo captures the stdout of anything it spawns and populates that into the reports, which results in stdout
		// being logged from these locations:
		// 1. regular container stdout
		// 2. ReportAfterEach
		// 3. ReportAfterSuite
		// suite.PrintLinesWithRunID(testRunID, strings.Split(r.CapturedGinkgoWriterOutput, "\n"), r.State)
		// suite.PrintLinesWithRunID(testRunID, strings.Split(r.CapturedStdOutErr, "\n"), r.State)

		r.CapturedGinkgoWriterOutput = testRunID
		r.CapturedStdOutErr = testRunID

		u, _ := json.Marshal(r)
		fmt.Println(string(u))
	}
	if len(suiteReport.SpecialSuiteFailureReasons) > 0 {
		fmt.Printf("SpecialSuiteFailureReasons: %+v", suiteReport.SpecialSuiteFailureReasons)
	}
})

var _ = ReportAfterEach(func(specReport ginkgotypes.SpecReport) {
	testRunID := fmt.Sprintf("ReportAfterEach-%s", kubernetes.RandomString())

	// Don't print CapturedGinkgoWriterOutput and CapturedStdOutErr for now as they end up being logged 3 times.
	// Ginkgo captures the stdout of anything it spawns and populates that into the reports, which results in stdout
	// being logged from these locations:
	// 1. regular container stdout
	// 2. ReportAfterEach
	// 3. ReportAfterSuite
	// suite.PrintLinesWithRunID(testRunID, strings.Split(specReport.CapturedGinkgoWriterOutput, "\n"), specReport.State)
	// suite.PrintLinesWithRunID(testRunID, strings.Split(specReport.CapturedStdOutErr, "\n"), specReport.State)

	specReport.CapturedGinkgoWriterOutput = testRunID
	specReport.CapturedStdOutErr = testRunID

	u, _ := json.Marshal(specReport)
	fmt.Println(string(u))
})

func createAndBootstrapMultiNodePoolCluster(ctx context.Context, k8sClient client.Client, humioClient humio.Client, cluster *humiov1alpha1.HumioCluster) {
	suite.CreateAndBootstrapCluster(ctx, k8sClient, humioClient, cluster, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)

	key := types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      cluster.Name,
	}

	suite.UsingClusterBy(key.Name, "Confirming each node pool enters expected state")
	var updatedHumioCluster humiov1alpha1.HumioCluster
	Eventually(func() string {
		err := k8sClient.Get(ctx, key, &updatedHumioCluster)
		if err != nil && !k8serrors.IsNotFound(err) {
			Expect(err).Should(Succeed())
		}
		for _, pool := range updatedHumioCluster.Status.NodePoolStatus {
			if pool.State != humiov1alpha1.HumioClusterStateRunning {
				return pool.State
			}
		}
		return humiov1alpha1.HumioClusterStateRunning
	}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))
}

func constructBasicMultiNodePoolHumioCluster(key types.NamespacedName, numberOfAdditionalNodePools int) *humiov1alpha1.HumioCluster {
	toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)

	nodeSpec := suite.ConstructBasicNodeSpecForHumioCluster(key)
	for i := 1; i <= numberOfAdditionalNodePools; i++ {
		toCreate.Spec.NodePools = append(toCreate.Spec.NodePools, humiov1alpha1.HumioNodePoolSpec{
			Name:          fmt.Sprintf("np-%d", i),
			HumioNodeSpec: nodeSpec,
		})
	}

	return toCreate
}

func markPodAsPendingUnschedulableIfUsingEnvtest(ctx context.Context, client client.Client, pod corev1.Pod, clusterName string) error {
	if !helpers.UseEnvtest() {
		return nil
	}

	suite.UsingClusterBy(clusterName, fmt.Sprintf("Simulating Humio pod is marked Pending Unschedulable (podName %s, pod phase %s)", pod.Name, pod.Status.Phase))
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodScheduled,
			Status: corev1.ConditionFalse,
			Reason: controller.PodConditionReasonUnschedulable,
		},
	}
	pod.Status.Phase = corev1.PodPending
	return client.Status().Update(ctx, &pod)
}

func markPodAsPendingImagePullBackOffIfUsingEnvtest(ctx context.Context, client client.Client, pod corev1.Pod, clusterName string) error {
	if !helpers.UseEnvtest() {
		return nil
	}

	suite.UsingClusterBy(clusterName, fmt.Sprintf("Simulating Humio pod is marked Pending ImagePullBackOff (podName %s, pod phase %s)", pod.Name, pod.Status.Phase))
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodScheduled,
			Status: corev1.ConditionTrue,
		},
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{
			Name: controller.HumioContainerName,
			State: corev1.ContainerState{
				Waiting: &corev1.ContainerStateWaiting{
					Reason: "ImagePullBackOff",
				},
			},
		},
	}
	pod.Status.Phase = corev1.PodPending
	return client.Status().Update(ctx, &pod)
}

func markPodsWithRevisionAsReadyIfUsingEnvTest(ctx context.Context, hnp *controller.HumioNodePool, podRevision int, desiredReadyPodCount int) {
	if !helpers.UseEnvtest() {
		return
	}
	foundPodList, _ := kubernetes.ListPods(ctx, k8sClient, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("Found %d pods", len(foundPodList)))
	podListWithRevision := []corev1.Pod{}
	for i := range foundPodList {
		foundPodRevisionValue := foundPodList[i].Annotations[controller.PodRevisionAnnotation]
		foundPodHash := foundPodList[i].Annotations[controller.PodHashAnnotation]
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("Pod=%s revision=%s podHash=%s podIP=%s podPhase=%s podStatusConditions=%+v",
			foundPodList[i].Name, foundPodRevisionValue, foundPodHash, foundPodList[i].Status.PodIP, foundPodList[i].Status.Phase, foundPodList[i].Status.Conditions))
		foundPodRevisionValueInt, _ := strconv.Atoi(foundPodRevisionValue)
		if foundPodRevisionValueInt == podRevision {
			podListWithRevision = append(podListWithRevision, foundPodList[i])
		}
	}
	suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("revision=%d, count=%d pods", podRevision, len(podListWithRevision)))

	readyWithRevision := 0
	for i := range podListWithRevision {
		if podListWithRevision[i].Status.PodIP != "" {
			readyWithRevision++
		}
	}
	suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("revision=%d, count=%d pods, readyWithRevision=%d", podRevision, len(podListWithRevision), readyWithRevision))

	if readyWithRevision == desiredReadyPodCount {
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("Got expected pod count %d with revision %d", readyWithRevision, podRevision))
		return
	}

	for i := range podListWithRevision {
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("Considering pod %s with podIP %s", podListWithRevision[i].Name, podListWithRevision[i].Status.PodIP))
		if podListWithRevision[i].Status.PodIP == "" {
			err := suite.MarkPodAsRunningIfUsingEnvtest(ctx, k8sClient, podListWithRevision[i], hnp.GetClusterName())
			if err != nil {
				suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("Got error while marking pod %s as running: %v", podListWithRevision[i].Name, err))
			}
			break
		}
	}
}

func podReadyCountByRevision(ctx context.Context, hnp *controller.HumioNodePool, expectedPodRevision int) map[int]int {
	revisionToReadyCount := map[int]int{}
	clusterPods, err := kubernetes.ListPods(ctx, k8sClient, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		suite.UsingClusterBy(hnp.GetClusterName(), "podReadyCountByRevision | Got error when listing pods")
	}

	for _, pod := range clusterPods {
		value, found := pod.Annotations[controller.PodRevisionAnnotation]
		if !found {
			suite.UsingClusterBy(hnp.GetClusterName(), "podReadyCountByRevision | ERROR, pod found without revision annotation")
		}
		revision, _ := strconv.Atoi(value)
		if pod.DeletionTimestamp == nil {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady {
					if condition.Status == corev1.ConditionTrue {
						revisionToReadyCount[revision]++
					}
				}
			}
		}
	}

	maxRevision := expectedPodRevision
	for revision := range revisionToReadyCount {
		if revision > maxRevision {
			maxRevision = revision
		}
	}

	for revision := 0; revision <= maxRevision; revision++ {
		if _, ok := revisionToReadyCount[revision]; !ok {
			revisionToReadyCount[revision] = 0
		}
	}

	return revisionToReadyCount
}

func podPendingCountByRevision(ctx context.Context, hnp *controller.HumioNodePool, expectedPodRevision int, expectedPendingCount int) map[int]int {
	revisionToPendingCount := map[int]int{}
	clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	for nodeID, pod := range clusterPods {
		revision, _ := strconv.Atoi(pod.Annotations[controller.PodRevisionAnnotation])
		if !helpers.UseEnvtest() {
			if pod.DeletionTimestamp == nil {
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodScheduled {
						if condition.Status == corev1.ConditionFalse && condition.Reason == controller.PodConditionReasonUnschedulable {
							revisionToPendingCount[revision]++
						}
					}
				}
			}
		} else {
			if nodeID+1 <= expectedPendingCount {
				_ = markPodAsPendingUnschedulableIfUsingEnvtest(ctx, k8sClient, pod, hnp.GetClusterName())
				revisionToPendingCount[revision]++
			}
		}
	}

	maxRevision := expectedPodRevision
	for revision := range revisionToPendingCount {
		if revision > maxRevision {
			maxRevision = revision
		}
	}

	for revision := 0; revision <= maxRevision; revision++ {
		if _, ok := revisionToPendingCount[revision]; !ok {
			revisionToPendingCount[revision] = 0
		}
	}

	return revisionToPendingCount
}

func ensurePodsRollingRestart(ctx context.Context, hnp *controller.HumioNodePool, expectedPodRevision int, numPodsPerIteration int) {
	suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("ensurePodsRollingRestart Ensuring replacement pods are ready %d at a time", numPodsPerIteration))

	// Each iteration we mark up to some expectedReady count in bulks of numPodsPerIteration, up to at most hnp.GetNodeCount()
	for expectedReadyCount := numPodsPerIteration; expectedReadyCount < hnp.GetNodeCount()+numPodsPerIteration; expectedReadyCount = expectedReadyCount + numPodsPerIteration {
		cappedExpectedReadyCount := min(hnp.GetNodeCount(), expectedReadyCount)
		Eventually(func() map[int]int {
			suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("ensurePodsRollingRestart Ensuring replacement pods are ready %d at a time expectedReadyCount=%d", numPodsPerIteration, cappedExpectedReadyCount))
			markPodsWithRevisionAsReadyIfUsingEnvTest(ctx, hnp, expectedPodRevision, cappedExpectedReadyCount)
			return podReadyCountByRevision(ctx, hnp, expectedPodRevision)
		}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision, cappedExpectedReadyCount))
	}
}

func ensurePodsGoPending(ctx context.Context, hnp *controller.HumioNodePool, expectedPodRevision int, expectedPendingCount int) {
	suite.UsingClusterBy(hnp.GetClusterName(), "Ensuring replacement pods are Pending")

	Eventually(func() map[int]int {
		return podPendingCountByRevision(ctx, hnp, expectedPodRevision, expectedPendingCount)
	}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision, expectedPendingCount))

}

func ensurePodsTerminate(ctx context.Context, hnp *controller.HumioNodePool, expectedPodRevision int) {
	suite.UsingClusterBy(hnp.GetClusterName(), "ensurePodsTerminate Ensuring all existing pods are terminated at the same time")
	Eventually(func() map[int]int {
		markPodsWithRevisionAsReadyIfUsingEnvTest(ctx, hnp, expectedPodRevision, 0)
		numPodsReadyByRevision := podReadyCountByRevision(ctx, hnp, expectedPodRevision)
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("podsReadyCountByRevision() = %#+v", numPodsReadyByRevision))
		return numPodsReadyByRevision
	}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision-1, 0))

	suite.UsingClusterBy(hnp.GetClusterName(), "ensurePodsTerminate Ensuring replacement pods are not ready at the same time")
	Eventually(func() map[int]int {
		markPodsWithRevisionAsReadyIfUsingEnvTest(ctx, hnp, expectedPodRevision, 0)
		numPodsReadyByRevision := podReadyCountByRevision(ctx, hnp, expectedPodRevision)
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("ensurePodsTerminate podsReadyCountByRevision() = %#+v", numPodsReadyByRevision))
		return numPodsReadyByRevision
	}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision, 0))

}

func ensurePodsSimultaneousRestart(ctx context.Context, hnp *controller.HumioNodePool, expectedPodRevision int) {
	ensurePodsTerminate(ctx, hnp, expectedPodRevision)

	suite.UsingClusterBy(hnp.GetClusterName(), "ensurePodsSimultaneousRestart Ensuring all pods come back up after terminating")
	Eventually(func() map[int]int {
		markPodsWithRevisionAsReadyIfUsingEnvTest(ctx, hnp, expectedPodRevision, hnp.GetNodeCount())
		numPodsReadyByRevision := podReadyCountByRevision(ctx, hnp, expectedPodRevision)
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("ensurePodsSimultaneousRestart podsReadyCountByRevision() = %#+v", numPodsReadyByRevision))
		return numPodsReadyByRevision
	}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision, hnp.GetNodeCount()))
}

func podNames(pods []corev1.Pod) []string {
	var podNamesList []string
	for _, pod := range pods {
		if pod.Name != "" {
			podNamesList = append(podNamesList, pod.Name)
		}
	}
	sort.Strings(podNamesList)
	return podNamesList
}

func getProbeScheme(hc *humiov1alpha1.HumioCluster) corev1.URIScheme {
	if !helpers.TLSEnabled(hc) {
		return corev1.URISchemeHTTP
	}

	return corev1.URISchemeHTTPS
}

// ensurePdfRenderDeploymentReady creates (if missing) a dummy Deployment for the
// given HumioPdfRenderService and patches .status so the HumioCluster controller
// sees it as Ready when running in env‑test.
func ensurePdfRenderDeploymentReady(ctx context.Context, c client.Client, key types.NamespacedName) {
	if !helpers.UseEnvtest() {
		return // real controller will create & update it in live clusters
	}

	// Define standard timeouts for this function
	standardTimeout := 30 * time.Second
	quickInterval := 250 * time.Millisecond

	labels := map[string]string{
		"app.kubernetes.io/name":       key.Name,
		"app.kubernetes.io/component":  "pdf-render-service",
		"app.kubernetes.io/managed-by": "humio-operator",
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: helpers.Int32Ptr(1),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "pdf", Image: "dummy"}}},
			},
		},
	}

	// Create the deployment if it doesn't exist
	err := c.Create(ctx, dep)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		Fail(fmt.Sprintf("Failed to create deployment for PDF render service: %v", err))
	}

	// Wait for the deployment to exist
	Eventually(func() error {
		return c.Get(ctx, key, dep)
	}, standardTimeout, quickInterval).Should(Succeed(),
		"Deployment %s/%s should exist", key.Namespace, key.Name)

	// Update the deployment status
	Eventually(func() error {
		// Get the latest version before updating status
		if err := c.Get(ctx, key, dep); err != nil {
			return err
		}
		dep.Status.Replicas = 1
		dep.Status.ReadyReplicas = 1
		dep.Status.AvailableReplicas = 1
		dep.Status.UpdatedReplicas = 1
		dep.Status.ObservedGeneration = dep.Generation
		return c.Status().Update(ctx, dep)
	}, standardTimeout, quickInterval).Should(Succeed(),
		"Should be able to update deployment status")

	// Verify the status update was applied
	Eventually(func() int32 {
		err := c.Get(ctx, key, dep)
		if err != nil {
			return 0
		}
		return dep.Status.ReadyReplicas
	}, standardTimeout, quickInterval).Should(Equal(int32(1)),
		"Deployment %s/%s should have 1 ready replica", key.Namespace, key.Name)
}

// fetchHumioPodEnv scans the first Humio pod and returns its env map.
// Returns nil if no pods are found or if there's an error.
func fetchHumioPodEnv(ctx context.Context, c client.Client, hcName, ns string) map[string]string {
	// Define standard timeouts for this function
	standardTimeout := 30 * time.Second
	quickInterval := 250 * time.Millisecond

	var pods []corev1.Pod
	var err error

	// Wait for pods to be available with proper error handling
	Eventually(func() bool {
		pods, err = kubernetes.ListPods(
			ctx, c, ns, kubernetes.MatchingLabelsForHumio(hcName),
		)
		if err != nil {
			fmt.Fprintf(GinkgoWriter, "Error listing pods for %s in namespace %s: %v\n",
				hcName, ns, err)
			return false
		}
		return len(pods) > 0
	}, standardTimeout, quickInterval).Should(BeTrue(),
		"Should find at least one pod for HumioCluster %s in namespace %s", hcName, ns)

	// Verify pods are not empty
	if len(pods) == 0 {
		fmt.Fprintf(GinkgoWriter, "No pods found for HumioCluster %s in namespace %s\n",
			hcName, ns)
		return nil
	}

	// Verify pod has containers
	if len(pods[0].Spec.Containers) == 0 {
		fmt.Fprintf(GinkgoWriter, "Pod %s has no containers\n", pods[0].Name)
		return nil
	}

	// Extract environment variables
	envs := map[string]string{}
	for _, env := range pods[0].Spec.Containers[0].Env {
		envs[env.Name] = env.Value
	}

	return envs
}
