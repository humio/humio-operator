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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	uberzap "go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/humio/humio-operator/internal/controller/suite"
	ginkgotypes "github.com/onsi/ginkgo/v2/types"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1beta1 "github.com/humio/humio-operator/api/v1beta1"
	webhooks "github.com/humio/humio-operator/internal/controller/webhooks"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cancel context.CancelFunc
var ctx context.Context
var testScheme *runtime.Scheme
var k8sClient client.Client
var testEnv *envtest.Environment
var k8sOperatorManager ctrl.Manager
var k8sWebhookManager ctrl.Manager
var humioClient humio.Client
var testTimeout time.Duration
var testNamespace corev1.Namespace
var testRepoName = "test-repo"
var testRepo corev1alpha1.HumioRepository
var testService1 corev1.Service
var testService2 corev1.Service
var clusterKey types.NamespacedName
var cluster = &corev1alpha1.HumioCluster{}
var sharedCluster helpers.ClusterInterface
var err error
var webhookCertGenerator *helpers.WebhookCertGenerator
var webhookListenHost string = "127.0.0.1"
var webhookServiceHost string = "127.0.0.1"
var webhookNamespace string = "e2e-resources-1"
var webhookSetupReconciler *controller.WebhookSetupReconciler
var webhookCertWatcher *certwatcher.CertWatcher

const (
	webhookPort     int           = 9443
	webhookCertPath string        = "/tmp/k8s-webhook-server/serving-certs"
	webhookCertName               = "tls.crt"
	webhookCertKey                = "tls.key"
	requeuePeriod   time.Duration = time.Second * 15
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HumioResources Controller Suite")
}

var _ = SynchronizedBeforeSuite(func() {
	// running just once on process 1 - setup webhook server
	var log logr.Logger
	var cfg *rest.Config
	var err error

	zapLog, _ := helpers.NewLogger()
	defer func(zapLog *uberzap.Logger) {
		_ = zapLog.Sync()
	}(zapLog)
	log = zapr.NewLogger(zapLog).WithSink(GinkgoLogr.GetSink())
	logf.SetLogger(log)

	useExistingCluster := true
	processID := GinkgoParallelProcess()

	clusterKey = types.NamespacedName{
		Name:      fmt.Sprintf("humiocluster-shared-%d", processID),
		Namespace: fmt.Sprintf("e2e-resources-%d", processID),
	}

	// register schemes
	testScheme = runtime.NewScheme()
	registerSchemes(testScheme)

	// initiatialize testenv and humioClient
	if !helpers.UseEnvtest() {
		testTimeout = time.Second * 240
		testEnv = &envtest.Environment{
			UseExistingCluster: &useExistingCluster,
			CRDInstallOptions: envtest.CRDInstallOptions{
				Scheme: testScheme,
			},
			ControlPlaneStartTimeout: 10 * time.Second,
			ControlPlaneStopTimeout:  10 * time.Second,
		}
		if helpers.UseDummyImage() {
			humioClient = humio.NewMockClient()
		} else {
			humioClient = humio.NewClient(log, "")
			By("Verifying we have a valid license, as tests will require starting up real LogScale containers")
			Expect(helpers.GetE2ELicenseFromEnvVar()).NotTo(BeEmpty())
		}
	} else {
		testTimeout = time.Second * 30
		testEnv = &envtest.Environment{
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing: true,
			CRDInstallOptions: envtest.CRDInstallOptions{
				Scheme: testScheme,
			},
			ControlPlaneStartTimeout: 10 * time.Second,
			ControlPlaneStopTimeout:  10 * time.Second,
		}
		humioClient = humio.NewMockClient()
	}

	// Setup k8s client config
	Eventually(func() error {
		cfg, err = testEnv.Start()
		return err
	}, 30*time.Second, 5*time.Second).Should(Succeed())
	Expect(cfg).NotTo(BeNil())

	var tlsOpts []func(*tls.Config)
	tlsVersion := func(c *tls.Config) {
		c.MinVersion = tls.VersionTLS12
	}
	tlsOpts = append(tlsOpts, tlsVersion)

	var webhookServer webhook.Server

	// Generate locally stored TLS certificate; shared across processes when running in envTest
	if !helpers.UseEnvtest() {
		webhookListenHost = "0.0.0.0"
		webhookServiceHost = helpers.GetOperatorWebhookServiceName()
		webhookNamespace = "default"
	}

	webhookCertGenerator = helpers.NewCertGenerator(webhookCertPath, webhookCertName, webhookCertKey,
		webhookServiceHost, webhookNamespace,
	)
	utilruntime.Must(webhookCertGenerator.GenerateIfNotExists())

	ctrl.Log.Info("Initializing webhook certificate watcher using provided certificates",
		"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)
	webhookCertWatcher, err = certwatcher.New(
		filepath.Join(webhookCertPath, webhookCertName),
		filepath.Join(webhookCertPath, webhookCertKey),
	)
	if err != nil {
		ctrl.Log.Error(err, "Failed to initialize webhook certificate watcher")
		os.Exit(1)
	}

	webhookTLSOpts := append(tlsOpts, func(config *tls.Config) {
		config.GetCertificate = webhookCertWatcher.GetCertificate
	})

	webhookServer = webhook.NewServer(webhook.Options{
		TLSOpts: webhookTLSOpts,
		Port:    webhookPort,
		Host:    webhookListenHost,
	})

	// Initiate k8s Operator Manager
	k8sOperatorManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  testScheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Logger:  log,
	})
	Expect(err).NotTo(HaveOccurred())

	// Initiate k8s Webhook Manager
	k8sWebhookManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme:        testScheme,
		WebhookServer: webhookServer,
		Metrics:       metricsserver.Options{BindAddress: "0"},
		Logger:        log,
	})
	Expect(err).NotTo(HaveOccurred())

	// Setup webhooks and controllers
	registerWebhooks(k8sWebhookManager, log)

	if webhookCertWatcher != nil {
		utilruntime.Must(k8sWebhookManager.Add(webhookCertWatcher))
	}

	// register controllers
	registerControllers(k8sOperatorManager, log)

	// start Operator Manager
	ctx, cancel = context.WithCancel(context.TODO())
	go func() {
		managerErr := k8sOperatorManager.Start(ctx)
		Expect(managerErr).NotTo(HaveOccurred())
	}()

	// Wait for the manager to be ready before getting the client
	Eventually(func() bool {
		return k8sOperatorManager.GetCache().WaitForCacheSync(ctx)
	}, 30*time.Second, time.Second).Should(BeTrue())

	// wait for namespace to be created
	testNamespace = corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterKey.Namespace,
		},
	}

	k8sClient = k8sOperatorManager.GetClient()
	Expect(k8sClient).NotTo(BeNil())

	err = k8sClient.Create(context.TODO(), &testNamespace)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	// wait until namespace is confirmed
	Eventually(func() string {
		ns := &corev1.Namespace{}
		_ = k8sClient.Get(context.TODO(), types.NamespacedName{Name: clusterKey.Namespace}, ns)
		return ns.Name
	}, 30*time.Second, 1*time.Second).Should(Equal(testNamespace.Name))

	// start Webhook Manager
	go func() {
		webhookErr := k8sWebhookManager.Start(ctx)
		Expect(webhookErr).NotTo(HaveOccurred())
	}()

	// Wait for webhook server to be ready
	if helpers.UseEnvtest() {
		Eventually(func() error {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", webhookListenHost, webhookPort), time.Second)
			if err != nil {
				return err
			}
			_ = conn.Close()
			return nil
		}, 30*time.Second, 1*time.Second).Should(Succeed())
		fmt.Printf("DEBUG: Webhook server is now listening on %s:%d\n", webhookListenHost, webhookPort)
	} else {
		Eventually(func() error {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s.default.svc:%d", helpers.GetOperatorWebhookServiceName(), 443), time.Second)
			if err != nil {
				return err
			}
			_ = conn.Close()
			return nil
		}, 30*time.Second, 1*time.Second).Should(Succeed())
		fmt.Printf("DEBUG: Webhook server is now listening on %s.default.svc:%d\n", helpers.GetOperatorWebhookServiceName(), 443)
	}

}, func() {
	var log logr.Logger
	var err error

	zapLog, _ := helpers.NewLogger()
	defer func(zapLog *uberzap.Logger) {
		_ = zapLog.Sync()
	}(zapLog)
	log = zapr.NewLogger(zapLog).WithSink(GinkgoLogr.GetSink())
	logf.SetLogger(log)

	By("bootstrapping test environment for all processes")
	useExistingCluster := true
	processID := GinkgoParallelProcess()

	if processID > 1 {
		clusterKey = types.NamespacedName{
			Name:      fmt.Sprintf("humiocluster-shared-%d", processID),
			Namespace: fmt.Sprintf("e2e-resources-%d", processID),
		}
		// register schemes
		testScheme = runtime.NewScheme()
		registerSchemes(testScheme)

		// initiatialize testenv and humioClient
		if !helpers.UseEnvtest() {
			testTimeout = time.Second * 300
			testEnv = &envtest.Environment{
				UseExistingCluster: &useExistingCluster,
			}
			if helpers.UseDummyImage() {
				humioClient = humio.NewMockClient()
			} else {
				humioClient = humio.NewClient(log, "")
				By("Verifying we have a valid license, as tests will require starting up real LogScale containers")
				Expect(helpers.GetE2ELicenseFromEnvVar()).NotTo(BeEmpty())
			}
		} else {
			testTimeout = time.Second * 30
			testEnv = &envtest.Environment{
				CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
				ErrorIfCRDPathMissing: true,
			}
			humioClient = humio.NewMockClient()
		}
	}

	// Setup k8s client configuration
	var cfg *rest.Config
	if processID > 1 {
		Eventually(func() error {
			cfg, err = testEnv.Start()
			return err
		}, 30*time.Second, 5*time.Second).Should(Succeed())
	} else {
		cfg = k8sOperatorManager.GetConfig()
		// Initialize k8sClient for process 1 if not already set
		if k8sClient == nil {
			k8sClient = k8sOperatorManager.GetClient()
			Expect(k8sClient).NotTo(BeNil())
		}
	}
	Expect(cfg).NotTo(BeNil())

	// when running locally we need to use local CABundle except process 1 that already has it
	if helpers.UseEnvtest() && processID > 1 {
		webhookCertGenerator = helpers.NewCertGenerator(webhookCertPath, webhookCertName, webhookCertKey,
			webhookServiceHost, clusterKey.Namespace,
		)
	}

	if processID > 1 {
		k8sOperatorManager, err = ctrl.NewManager(cfg, ctrl.Options{
			Scheme:  testScheme,
			Metrics: metricsserver.Options{BindAddress: "0"},
			Logger:  log,
		})
		Expect(err).NotTo(HaveOccurred())

		k8sClient = k8sOperatorManager.GetClient()
		Expect(k8sClient).NotTo(BeNil())
	}

	// we want to sync local CABundle to k8s only if running locally or in process 1
	// for 1 it is already set and started
	if processID > 1 {
		// register controllers
		registerControllers(k8sOperatorManager, log)

		if helpers.UseEnvtest() {
			// register webhook reconciler
			webhookSetupReconciler = controller.NewTestWebhookSetupReconciler(
				k8sOperatorManager.GetClient(),
				k8sOperatorManager.GetCache(),
				log,
				webhookCertGenerator,
				helpers.GetOperatorWebhookServiceName(),
				webhookNamespace,
				requeuePeriod,
				webhookPort,
				"127.0.0.1",
			)
			utilruntime.Must(k8sOperatorManager.Add(webhookSetupReconciler))

			if webhookCertWatcher != nil {
				utilruntime.Must(k8sOperatorManager.Add(webhookCertWatcher))
			}
		}
	}

	// Start manager
	if processID > 1 {
		ctx, cancel = context.WithCancel(context.TODO())
		go func() {
			err = k8sOperatorManager.Start(ctx)
			Expect(err).NotTo(HaveOccurred())
		}()
	}

	// Start testing
	By(fmt.Sprintf("Creating test namespace: %s", clusterKey.Namespace))
	testNamespace = corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterKey.Namespace,
		},
	}
	err = k8sClient.Create(context.TODO(), &testNamespace)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}

	suite.CreateDockerRegredSecret(context.TODO(), testNamespace, k8sClient)
	suite.UsingClusterBy(clusterKey.Name, fmt.Sprintf("HumioCluster: Creating shared test cluster in namespace %s", clusterKey.Namespace))
	cluster = suite.ConstructBasicSingleNodeHumioCluster(clusterKey, true)
	suite.CreateAndBootstrapCluster(context.TODO(), k8sClient, humioClient, cluster, true, corev1alpha1.HumioClusterStateRunning, testTimeout)

	// Update cluster status version
	if helpers.UseEnvtest() || helpers.UseDummyImage() {
		Eventually(func() error {
			if err := k8sClient.Get(context.TODO(), clusterKey, cluster); err != nil {
				return err
			}
			cluster.Status.Version = humio.WebhookHumioVersion
			return k8sClient.Status().Update(context.TODO(), cluster)
		}, testTimeout, suite.TestInterval).Should(Succeed())
	}

	// Start some basic initial tests
	sharedCluster, err = helpers.NewCluster(context.TODO(), k8sClient, clusterKey.Name, "", clusterKey.Namespace, helpers.UseCertManager(), true, false)
	Expect(err).ToNot(HaveOccurred())
	Expect(sharedCluster).ToNot(BeNil())
	Expect(sharedCluster.Config()).ToNot(BeNil())

	testRepo = corev1alpha1.HumioRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testRepoName,
			Namespace: clusterKey.Namespace,
		},
		Spec: corev1alpha1.HumioRepositorySpec{
			ManagedClusterName: clusterKey.Name,
			Name:               testRepoName,
			AllowDataDeletion:  true,
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
		if testRepo.Name != "" {
			Expect(k8sClient.Delete(context.TODO(), &corev1alpha1.HumioRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testRepo.Name,
					Namespace: testRepo.Namespace,
				},
			})).To(Succeed())
			Eventually(func() bool {
				return k8serrors.IsNotFound(
					k8sClient.Get(ctx, types.NamespacedName{
						Name:      testRepo.Name,
						Namespace: testRepo.Namespace,
					}, &corev1alpha1.HumioRepository{}),
				)
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		}

		if testService1.Name != "" {
			Expect(k8sClient.Delete(context.TODO(), &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testService1.Name,
					Namespace: testService1.Namespace,
				},
			})).To(Succeed())
		}
		if testService2.Name != "" {
			Expect(k8sClient.Delete(context.TODO(), &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testService2.Name,
					Namespace: testService2.Namespace,
				},
			})).To(Succeed())
		}

		suite.UsingClusterBy(clusterKey.Name, "HumioCluster: Confirming resource generation wasn't updated excessively")
		Expect(k8sClient.Get(context.Background(), clusterKey, cluster)).Should(Succeed())
		Expect(cluster.GetGeneration()).ShouldNot(BeNumerically(">", 100))

		suite.CleanupCluster(context.TODO(), k8sClient, cluster)

		if suite.UseDockerCredentials() {
			By(fmt.Sprintf("Removing regcred secret for namespace: %s", testNamespace.Name))
			Expect(k8sClient.Delete(context.TODO(), &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      suite.DockerRegistryCredentialsSecretName,
					Namespace: clusterKey.Namespace,
				},
			})).To(Succeed())
		}

		if testNamespace.Name != "" && !helpers.UseEnvtest() && helpers.PreserveKindCluster() {
			By(fmt.Sprintf("Removing test namespace: %s", clusterKey.Namespace))
			err := k8sClient.Delete(context.TODO(), &testNamespace)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() bool {
				return k8serrors.IsNotFound(k8sClient.Get(context.TODO(), types.NamespacedName{Name: clusterKey.Namespace}, &testNamespace))
			}, testTimeout, suite.TestInterval).Should(BeTrue())
		}
	}

	if cancel != nil {
		cancel()
	}
	By("Tearing down the test environment")
	if testEnv != nil {
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
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

func registerSchemes(scheme *runtime.Scheme) {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1beta1.AddToScheme(scheme))
	if helpers.UseCertManager() {
		utilruntime.Must(cmapi.AddToScheme(scheme))
	}
}

func registerControllers(k8sOperatorManager ctrl.Manager, log logr.Logger) {
	err = (&controller.HumioActionReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioAggregateAlertReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioAlertReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioBootstrapTokenReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		BaseLogger: log,
		Namespace:  clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioClusterReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioExternalClusterReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioFilterAlertReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioFeatureFlagReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioIngestTokenReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioOrganizationPermissionRoleReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioParserReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioRepositoryReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioScheduledSearchReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioSystemPermissionRoleReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioViewReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioUserReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioViewPermissionRoleReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioGroupReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioPdfRenderServiceReconciler{
		Client: k8sOperatorManager.GetClient(),
		Scheme: k8sOperatorManager.GetScheme(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		BaseLogger: log,
		Namespace:  clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioMultiClusterSearchViewReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioIPFilterReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioViewTokenReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod:              requeuePeriod,
			CriticalErrorRequeuePeriod: time.Second * 5,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioSystemTokenReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod:              requeuePeriod,
			CriticalErrorRequeuePeriod: time.Second * 5,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	err = (&controller.HumioOrganizationTokenReconciler{
		Client: k8sOperatorManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod:              requeuePeriod,
			CriticalErrorRequeuePeriod: time.Second * 5,
		},
		HumioClient: humioClient,
		BaseLogger:  log,
		Namespace:   clusterKey.Namespace,
	}).SetupWithManager(k8sOperatorManager)
	Expect(err).NotTo(HaveOccurred())

	// we create the namespace as other resources depend on it
	testScheme = k8sOperatorManager.GetScheme()
	k8sClient = k8sOperatorManager.GetClient()
	Expect(k8sClient).NotTo(BeNil())
}

func registerWebhooks(k8sWebhookManager ctrl.Manager, log logr.Logger) {
	if helpers.UseEnvtest() {
		webhookSetupReconciler = controller.NewTestWebhookSetupReconciler(
			k8sWebhookManager.GetClient(),
			k8sWebhookManager.GetCache(),
			log,
			webhookCertGenerator,
			helpers.GetOperatorWebhookServiceName(),
			webhookNamespace,
			requeuePeriod,
			webhookPort,
			"127.0.0.1",
		)
	} else {
		webhookSetupReconciler = controller.NewProductionWebhookSetupReconciler(
			k8sWebhookManager.GetClient(),
			k8sWebhookManager.GetCache(),
			log,
			webhookCertGenerator,
			helpers.GetOperatorName(),
			helpers.GetOperatorNamespace(),
			requeuePeriod,
		)
	}
	utilruntime.Must(k8sWebhookManager.Add(webhookSetupReconciler))

	if err := ctrl.NewWebhookManagedBy(k8sWebhookManager).
		For(&corev1alpha1.HumioScheduledSearch{}).
		WithValidator(&webhooks.HumioScheduledSearchValidator{
			BaseLogger:  log,
			Client:      k8sWebhookManager.GetClient(),
			HumioClient: humioClient,
		}).
		WithDefaulter(nil).
		Complete(); err != nil {
		ctrl.Log.Error(err, "unable to create conversion webhook for corev1alpha1.HumioScheduledSearch", "webhook", "HumioScheduledSearch")
		os.Exit(1)
	}
	if err := ctrl.NewWebhookManagedBy(k8sWebhookManager).
		For(&corev1beta1.HumioScheduledSearch{}).
		WithValidator(&webhooks.HumioScheduledSearchValidator{
			BaseLogger:  log,
			Client:      k8sWebhookManager.GetClient(),
			HumioClient: humioClient,
		}).
		WithDefaulter(nil).
		Complete(); err != nil {
		ctrl.Log.Error(err, "unable to create conversion webhook for corev1beta1.HumioScheduledSearch", "webhook", "HumioScheduledSearch")
		os.Exit(1)
	}
}
