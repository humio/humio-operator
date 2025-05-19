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

package bootstraptokens

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	uberzap "go.uber.org/zap"
	"k8s.io/client-go/rest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	corev1 "k8s.io/api/core/v1"
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
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var k8sClient client.Client
var testEnv *envtest.Environment
var k8sManager ctrl.Manager
var testTimeout time.Duration
var testProcessNamespace string
var err error

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "HumioBootstrapToken Controller Suite")
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
	testProcessNamespace = fmt.Sprintf("e2e-bootstrap-tokens-%d", GinkgoParallelProcess())
	if !helpers.UseEnvtest() {
		testTimeout = time.Second * 300
		testEnv = &envtest.Environment{
			UseExistingCluster: &useExistingCluster,
		}
	} else {
		// We use envtest to run tests
		testTimeout = time.Second * 30
		testEnv = &envtest.Environment{
			// TODO: If we want to add support for TLS-functionality, we need to install cert-manager's CRD's
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing: true,
		}
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

	err = (&controller.HumioBootstrapTokenReconciler{
		Client: k8sManager.GetClient(),
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

var _ = ReportAfterSuite("HumioBootstrapToken Controller Suite", func(suiteReport ginkgotypes.Report) {
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
