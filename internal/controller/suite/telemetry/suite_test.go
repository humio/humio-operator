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

package telemetry

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg                  *rest.Config
	k8sClient            client.Client
	testEnv              *envtest.Environment
	testTimeout          time.Duration
	testHumioClient      humio.Client
	testK8sManager       ctrl.Manager
	testProcessNamespace string
	log                  logr.Logger
)

func TestHumioTelemetry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HumioTelemetry Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Initialize logger
	zapLog, err := helpers.NewLogger(suite.LogLevel)
	Expect(err).ToNot(HaveOccurred())
	log = zapr.NewLogger(zapLog).WithSink(GinkgoLogr.GetSink())

	By("bootstrapping test environment")

	useExistingCluster := true
	testProcessNamespace = fmt.Sprintf("e2e-telemetry-%d", GinkgoParallelProcess())
	if !helpers.UseEnvtest() {
		testEnv = &envtest.Environment{
			UseExistingCluster: &useExistingCluster,
		}
		if helpers.UseDummyImage() {
			testTimeout = time.Second * 300
			testHumioClient = humio.NewMockClient()
		} else {
			testTimeout = time.Second * 900
			testHumioClient = humio.NewClient(log, "")
		}
	} else {
		testTimeout = time.Second * 30
		testEnv = &envtest.Environment{
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing: true,
		}
		testHumioClient = humio.NewMockClient()
	}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = humiov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	cacheOptions, err := helpers.GetCacheOptionsWithWatchNamespace()
	if err != nil {
		ctrl.Log.Info("unable to get WatchNamespace: the manager will watch and manage resources in all namespaces")
	}

	// +kubebuilder:scaffold:scheme

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Logger:  log,
		Cache:   cacheOptions,
	})
	Expect(err).ToNot(HaveOccurred())

	var requeuePeriod time.Duration

	err = (&controller.HumioTelemetryReconciler{
		Client: k8sManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: testHumioClient,
		BaseLogger:  log,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&controller.HumioClusterReconciler{
		Client: k8sManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: testHumioClient,
		BaseLogger:  log,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).NotTo(BeNil())

	testK8sManager = k8sManager

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
	By("tearing down the test environment")
	if testEnv != nil {
		_ = testEnv.Stop()
	}
})
