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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/humio/humio-operator/controllers"
	"github.com/humio/humio-operator/controllers/suite"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"github.com/humio/humio-operator/pkg/kubernetes"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	humioapi "github.com/humio/cli/api"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	openshiftsecurityv1 "github.com/openshift/api/security/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/openshift"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var testEnv *envtest.Environment
var k8sManager ctrl.Manager
var humioClientForHumioAction humio.Client
var humioClientForHumioAlert humio.Client
var humioClientForHumioCluster humio.Client
var humioClientForHumioExternalCluster humio.Client
var humioClientForHumioIngestToken humio.Client
var humioClientForHumioParser humio.Client
var humioClientForHumioRepository humio.Client
var humioClientForHumioView humio.Client
var humioClientForTestSuite humio.Client
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
	defer zapLog.Sync()
	log = zapr.NewLogger(zapLog)
	logf.SetLogger(log)

	Expect(os.Getenv("HUMIO_E2E_LICENSE")).NotTo(BeEmpty())

	By("bootstrapping test environment")
	useExistingCluster := true
	testProcessNamespace = fmt.Sprintf("e2e-clusters-%d", GinkgoParallelProcess())
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		testTimeout = time.Second * 300
		testEnv = &envtest.Environment{
			UseExistingCluster: &useExistingCluster,
		}
		humioClientForTestSuite = humio.NewClient(log, &humioapi.Config{}, "")
		humioClientForHumioAction = humio.NewClient(log, &humioapi.Config{}, "")
		humioClientForHumioAlert = humio.NewClient(log, &humioapi.Config{}, "")
		humioClientForHumioCluster = humio.NewClient(log, &humioapi.Config{}, "")
		humioClientForHumioExternalCluster = humio.NewClient(log, &humioapi.Config{}, "")
		humioClientForHumioIngestToken = humio.NewClient(log, &humioapi.Config{}, "")
		humioClientForHumioParser = humio.NewClient(log, &humioapi.Config{}, "")
		humioClientForHumioRepository = humio.NewClient(log, &humioapi.Config{}, "")
		humioClientForHumioView = humio.NewClient(log, &humioapi.Config{}, "")
	} else {
		testTimeout = time.Second * 30
		testEnv = &envtest.Environment{
			// TODO: If we want to add support for TLS-functionality, we need to install cert-manager's CRD's
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing: true,
		}
		humioClientForTestSuite = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
		humioClientForHumioAction = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
		humioClientForHumioAlert = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
		humioClientForHumioCluster = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
		humioClientForHumioExternalCluster = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
		humioClientForHumioIngestToken = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
		humioClientForHumioParser = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
		humioClientForHumioRepository = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
		humioClientForHumioView = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
	}

	var cfg *rest.Config

	Eventually(func() error {
		// testEnv.Start() sporadically fails with "unable to grab random port for serving webhooks on", so let's
		// retry a couple of times
		cfg, err = testEnv.Start()
		return err
	}, 30*time.Second, 5*time.Second).Should(Succeed())
	Expect(cfg).NotTo(BeNil())

	if helpers.IsOpenShift() {
		err = openshiftsecurityv1.AddToScheme(scheme.Scheme)
		Expect(err).NotTo(HaveOccurred())
	}

	if helpers.UseCertManager() {
		err = cmapi.AddToScheme(scheme.Scheme)
		Expect(err).NotTo(HaveOccurred())
	}

	err = humiov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	watchNamespace, _ := helpers.GetWatchNamespace()

	options := ctrl.Options{
		Scheme:             scheme.Scheme,
		MetricsBindAddress: "0",
		Namespace:          watchNamespace,
		Logger:             log,
	}

	// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
	if strings.Contains(watchNamespace, ",") {
		log.Info(fmt.Sprintf("manager will be watching namespace %q", watchNamespace))
		// configure cluster-scoped with MultiNamespacedCacheBuilder
		options.Namespace = ""
		options.NewCache = cache.MultiNamespacedCacheBuilder(strings.Split(watchNamespace, ","))
		// TODO: Get rid of Namespace property on Reconciler objects and instead use a custom cache implementation as this cache doesn't support watching a subset of namespace while still allowing to watch cluster-scoped resources. https://github.com/kubernetes-sigs/controller-runtime/issues/934
	}

	k8sManager, err = ctrl.NewManager(cfg, options)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioActionReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClientForHumioAction,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioAlertReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClientForHumioAlert,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioClusterReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClientForHumioCluster,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioExternalClusterReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClientForHumioExternalCluster,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioClusterGroupReconciler{
		Client:     k8sManager.GetClient(),
		BaseLogger: log,
		Namespace:  testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioIngestTokenReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClientForHumioIngestToken,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioParserReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClientForHumioParser,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioRepositoryReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClientForHumioRepository,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioViewReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClientForHumioView,
		BaseLogger:  log,
		Namespace:   testProcessNamespace,
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

	if helpers.IsOpenShift() {
		var err error
		ctx := context.Background()
		Eventually(func() bool {
			_, err = openshift.GetSecurityContextConstraints(ctx, k8sClient)
			if k8serrors.IsNotFound(err) {
				// Object has not been created yet
				return true
			}
			if err != nil {
				// Some other error happened. Typically:
				//   <*cache.ErrCacheNotStarted | 0x31fc738>: {}
				//         the cache is not started, can not read objects occurred
				return false
			}
			// At this point we know the object already exists.
			return true
		}, testTimeout, suite.TestInterval).Should(BeTrue())
		if k8serrors.IsNotFound(err) {
			By("Simulating helm chart installation of the SecurityContextConstraints object")
			sccName := os.Getenv("OPENSHIFT_SCC_NAME")
			priority := int32(0)
			scc := openshiftsecurityv1.SecurityContextConstraints{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sccName,
					Namespace: testProcessNamespace,
				},
				Priority:                 &priority,
				AllowPrivilegedContainer: true,
				DefaultAddCapabilities:   []corev1.Capability{},
				RequiredDropCapabilities: []corev1.Capability{
					"KILL",
					"MKNOD",
					"SETUID",
					"SETGID",
				},
				AllowedCapabilities: []corev1.Capability{
					"SYS_NICE",
				},
				AllowHostDirVolumePlugin: true,
				Volumes: []openshiftsecurityv1.FSType{
					openshiftsecurityv1.FSTypeConfigMap,
					openshiftsecurityv1.FSTypeDownwardAPI,
					openshiftsecurityv1.FSTypeEmptyDir,
					openshiftsecurityv1.FSTypeHostPath,
					openshiftsecurityv1.FSTypePersistentVolumeClaim,
					openshiftsecurityv1.FSProjected,
					openshiftsecurityv1.FSTypeSecret,
				},
				AllowedFlexVolumes: nil,
				AllowHostNetwork:   false,
				AllowHostPorts:     false,
				AllowHostPID:       false,
				AllowHostIPC:       false,
				SELinuxContext: openshiftsecurityv1.SELinuxContextStrategyOptions{
					Type: openshiftsecurityv1.SELinuxStrategyMustRunAs,
				},
				RunAsUser: openshiftsecurityv1.RunAsUserStrategyOptions{
					Type: openshiftsecurityv1.RunAsUserStrategyRunAsAny,
				},
				SupplementalGroups: openshiftsecurityv1.SupplementalGroupsStrategyOptions{
					Type: openshiftsecurityv1.SupplementalGroupsStrategyRunAsAny,
				},
				FSGroup: openshiftsecurityv1.FSGroupStrategyOptions{
					Type: openshiftsecurityv1.FSGroupStrategyRunAsAny,
				},
				ReadOnlyRootFilesystem: false,
				Users:                  []string{},
				Groups:                 nil,
				SeccompProfiles:        nil,
			}
			Expect(k8sClient.Create(ctx, &scc)).To(Succeed())
		}
	}
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
		//suite.PrintLinesWithRunID(testRunID, strings.Split(r.CapturedGinkgoWriterOutput, "\n"), r.State)
		//suite.PrintLinesWithRunID(testRunID, strings.Split(r.CapturedStdOutErr, "\n"), r.State)

		r.CapturedGinkgoWriterOutput = testRunID
		r.CapturedStdOutErr = testRunID

		u, _ := json.Marshal(r)
		fmt.Println(string(u))
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
	//suite.PrintLinesWithRunID(testRunID, strings.Split(specReport.CapturedGinkgoWriterOutput, "\n"), specReport.State)
	//suite.PrintLinesWithRunID(testRunID, strings.Split(specReport.CapturedStdOutErr, "\n"), specReport.State)

	specReport.CapturedGinkgoWriterOutput = testRunID
	specReport.CapturedStdOutErr = testRunID

	u, _ := json.Marshal(specReport)
	fmt.Println(string(u))
})

func createAndBootstrapMultiNodePoolCluster(ctx context.Context, k8sClient client.Client, humioClient humio.Client, cluster *humiov1alpha1.HumioCluster, autoCreateLicense bool, expectedState string) {
	suite.CreateAndBootstrapCluster(ctx, k8sClient, humioClient, cluster, autoCreateLicense, expectedState, testTimeout)

	if expectedState != humiov1alpha1.HumioClusterStateRunning {
		return
	}

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
			if pool.State != expectedState {
				return pool.State
			}
		}
		return expectedState
	}, testTimeout, suite.TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))
}

func constructBasicMultiNodePoolHumioCluster(key types.NamespacedName, useAutoCreatedLicense bool, numberOfAdditionalNodePools int) *humiov1alpha1.HumioCluster {
	toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, useAutoCreatedLicense)
	nodeSpec := suite.ConstructBasicNodeSpecForHumioCluster(key)

	for i := 1; i <= numberOfAdditionalNodePools; i++ {
		toCreate.Spec.NodePools = append(toCreate.Spec.NodePools, humiov1alpha1.HumioNodePoolSpec{
			Name:          fmt.Sprintf("np-%d", i),
			HumioNodeSpec: nodeSpec,
		})
	}

	return toCreate
}

func markPodAsPending(ctx context.Context, client client.Client, nodeID int, pod corev1.Pod, clusterName string) error {
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		return nil
	}

	suite.UsingClusterBy(clusterName, fmt.Sprintf("Simulating Humio pod is marked Pending (node %d, pod phase %s)", nodeID, pod.Status.Phase))
	pod.Status.PodIP = fmt.Sprintf("192.168.0.%d", nodeID)

	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodScheduled,
			Status: corev1.ConditionFalse,
			Reason: controllers.PodConditionReasonUnschedulable,
		},
	}
	pod.Status.Phase = corev1.PodPending
	return client.Status().Update(ctx, &pod)
}

func podReadyCountByRevision(ctx context.Context, hnp *controllers.HumioNodePool, expectedPodRevision int, expectedReadyCount int) map[int]int {
	revisionToReadyCount := map[int]int{}
	clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	for nodeID, pod := range clusterPods {
		_, revision := hnp.GetHumioClusterNodePoolRevisionAnnotation()
		if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
			if pod.DeletionTimestamp == nil {
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady {
						if condition.Status == corev1.ConditionTrue {
							revisionToReadyCount[revision]++

						}
					}
				}
			}
		} else {
			if nodeID+1 <= expectedReadyCount {
				_ = suite.MarkPodAsRunning(ctx, k8sClient, nodeID, pod, hnp.GetClusterName())
				revisionToReadyCount[revision]++
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

func podPendingCountByRevision(ctx context.Context, hnp *controllers.HumioNodePool, expectedPodRevision int, expectedPendingCount int) map[int]int {
	revisionToPendingCount := map[int]int{}
	clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	for nodeID, pod := range clusterPods {
		revision, _ := strconv.Atoi(pod.Annotations[controllers.PodRevisionAnnotation])
		if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
			if pod.DeletionTimestamp == nil {
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodScheduled {
						if condition.Status == corev1.ConditionFalse && condition.Reason == controllers.PodConditionReasonUnschedulable {
							revisionToPendingCount[revision]++
						}
					}
				}
			}
		} else {
			if nodeID+1 <= expectedPendingCount {
				_ = markPodAsPending(ctx, k8sClient, nodeID, pod, hnp.GetClusterName())
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

func ensurePodsRollingRestart(ctx context.Context, hnp *controllers.HumioNodePool, expectedPodRevision int) {
	suite.UsingClusterBy(hnp.GetClusterName(), "Ensuring replacement pods are ready one at a time")

	for expectedReadyCount := 1; expectedReadyCount < hnp.GetNodeCount()+1; expectedReadyCount++ {
		Eventually(func() map[int]int {
			return podReadyCountByRevision(ctx, hnp, expectedPodRevision, expectedReadyCount)
		}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision, expectedReadyCount))
	}
}

func ensurePodsGoPending(ctx context.Context, hnp *controllers.HumioNodePool, expectedPodRevision int, expectedPendingCount int) {
	suite.UsingClusterBy(hnp.GetClusterName(), "Ensuring replacement pods are Pending")

	Eventually(func() map[int]int {
		return podPendingCountByRevision(ctx, hnp, expectedPodRevision, expectedPendingCount)
	}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision, expectedPendingCount))

}

func ensurePodsTerminate(ctx context.Context, hnp *controllers.HumioNodePool, expectedPodRevision int) {
	suite.UsingClusterBy(hnp.GetClusterName(), "Ensuring all existing pods are terminated at the same time")
	Eventually(func() map[int]int {
		numPodsReadyByRevision := podReadyCountByRevision(ctx, hnp, expectedPodRevision, 0)
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("podsReadyCountByRevision() = %#+v", numPodsReadyByRevision))
		return numPodsReadyByRevision
	}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision-1, 0))

	suite.UsingClusterBy(hnp.GetClusterName(), "Ensuring replacement pods are not ready at the same time")
	Eventually(func() map[int]int {
		numPodsReadyByRevision := podReadyCountByRevision(ctx, hnp, expectedPodRevision, 0)
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("podsReadyCountByRevision() = %#+v", numPodsReadyByRevision))
		return numPodsReadyByRevision
	}, testTimeout, suite.TestInterval).Should(HaveKeyWithValue(expectedPodRevision, 0))

}

func ensurePodsSimultaneousRestart(ctx context.Context, hnp *controllers.HumioNodePool, expectedPodRevision int) {
	ensurePodsTerminate(ctx, hnp, expectedPodRevision)

	suite.UsingClusterBy(hnp.GetClusterName(), "Ensuring all pods come back up after terminating")
	Eventually(func() map[int]int {
		numPodsReadyByRevision := podReadyCountByRevision(ctx, hnp, expectedPodRevision, hnp.GetNodeCount())
		suite.UsingClusterBy(hnp.GetClusterName(), fmt.Sprintf("podsReadyCountByRevision() = %#+v", numPodsReadyByRevision))
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
