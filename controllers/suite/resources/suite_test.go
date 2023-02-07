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

package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/humio/humio-operator/pkg/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/humio/humio-operator/controllers"
	"github.com/humio/humio-operator/controllers/suite"
	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	"k8s.io/apimachinery/pkg/types"

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
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var testEnv *envtest.Environment
var k8sManager ctrl.Manager
var humioClient humio.Client
var testTimeout time.Duration
var testNamespace corev1.Namespace
var testRepo corev1alpha1.HumioRepository
var testService1 corev1.Service
var testService2 corev1.Service
var clusterKey types.NamespacedName
var cluster = &corev1alpha1.HumioCluster{}
var sharedCluster helpers.ClusterInterface
var err error

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "HumioResources Controller Suite")
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
	clusterKey = types.NamespacedName{
		Name:      fmt.Sprintf("humiocluster-shared-%d", GinkgoParallelProcess()),
		Namespace: fmt.Sprintf("e2e-resources-%d", GinkgoParallelProcess()),
	}

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		testTimeout = time.Second * 300
		testEnv = &envtest.Environment{
			UseExistingCluster: &useExistingCluster,
		}
		humioClient = humio.NewClient(log, &humioapi.Config{}, "")
	} else {
		testTimeout = time.Second * 30
		testEnv = &envtest.Environment{
			// TODO: If we want to add support for TLS-functionality, we need to install cert-manager's CRD's
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing: true,
		}
		humioClient = humio.NewMockClient(humioapi.Cluster{}, nil, nil, nil)
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

	err = corev1alpha1.AddToScheme(scheme.Scheme)
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
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioAlertReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioClusterReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioExternalClusterReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioIngestTokenReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioParserReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioRepositoryReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controllers.HumioViewReconciler{
		Client:      k8sManager.GetClient(),
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sManager)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).NotTo(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).NotTo(BeNil())

	By(fmt.Sprintf("Creating test namespace: %s", clusterKey.Namespace))
	testNamespace = corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterKey.Namespace,
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
					Namespace: clusterKey.Namespace,
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

	suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioCluster: Creating shared test cluster in namespace %s", clusterKey.Namespace))
	cluster = suite.ConstructBasicSingleNodeHumioCluster(clusterKey, true)
	suite.CreateAndBootstrapCluster(context.TODO(), k8sClient, humioClient, cluster, true, corev1alpha1.HumioClusterStateRunning, testTimeout)

	sharedCluster, err = helpers.NewCluster(context.TODO(), k8sClient, clusterKey.Name, "", clusterKey.Namespace, helpers.UseCertManager(), true)
	Expect(err).To(BeNil())
	Expect(sharedCluster).ToNot(BeNil())
	Expect(sharedCluster.Config()).ToNot(BeNil())

	testRepo = corev1alpha1.HumioRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: clusterKey.Namespace,
		},
		Spec: corev1alpha1.HumioRepositorySpec{
			ManagedClusterName: clusterKey.Name,
			Name:               "test-repo",
		},
	}
	Expect(k8sClient.Create(context.TODO(), &testRepo)).To(Succeed())

	testService1 = corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service1",
			Namespace: clusterKey.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone,
		},
	}
	testEndpoint1 := corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testService1.Name,
			Namespace: testService1.Namespace,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: "100.64.1.1",
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(context.TODO(), &testService1)).To(Succeed())
	Expect(k8sClient.Create(context.TODO(), &testEndpoint1)).To(Succeed())

	testService2 = corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service2",
			Namespace: clusterKey.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone,
		},
	}
	testEndpoint2 := corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testService2.Name,
			Namespace: testService2.Namespace,
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: "100.64.1.1",
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(context.TODO(), &testService2)).To(Succeed())
	Expect(k8sClient.Create(context.TODO(), &testEndpoint2)).To(Succeed())
})

var _ = AfterSuite(func() {
	if k8sClient != nil {
		suite.UsingClusterBy(clusterKey.Name, "HumioCluster: Confirming resource generation wasn't updated excessively")
		Expect(k8sClient.Get(context.Background(), clusterKey, cluster)).Should(Succeed())
		Expect(cluster.GetGeneration()).ShouldNot(BeNumerically(">", 100))

		suite.CleanupCluster(context.TODO(), k8sClient, cluster)

		By(fmt.Sprintf("Removing regcred secret for namespace: %s", testNamespace.Name))
		_ = k8sClient.Delete(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      suite.DockerRegistryCredentialsSecretName,
				Namespace: clusterKey.Namespace,
			},
		})

		if testNamespace.ObjectMeta.Name != "" {
			By(fmt.Sprintf("Removing test namespace: %s", clusterKey.Namespace))
			err := k8sClient.Delete(context.TODO(), &testNamespace)
			Expect(err).ToNot(HaveOccurred())
		}
	}

	By("Tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
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
