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
	cfg             *rest.Config
	k8sClient       client.Client
	testEnv         *envtest.Environment
	testTimeout     time.Duration
	testHumioClient humio.Client
	testK8sManager  ctrl.Manager
	log             logr.Logger
	cancelContext   context.CancelFunc
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

	ctx, cancel := context.WithCancel(context.TODO())
	cancelContext = cancel

	By("bootstrapping test environment")

	if helpers.UseEnvtest() {
		By("setting up envtest environment")
		testTimeout = time.Second * 30
		testEnv = &envtest.Environment{
			CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "config", "crd", "bases")},
			ErrorIfCRDPathMissing: true,
		}

		var err error
		cfg, err = testEnv.Start()
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg).NotTo(BeNil())

		testHumioClient = humio.NewMockClient()
	} else {
		By("setting up test with existing cluster")
		testHumioClient = humio.NewClient(log, "")
		if helpers.UseDummyImage() {
			testTimeout = time.Second * 300
		} else {
			testTimeout = time.Second * 900
		}

		// Get the current kubeconfig
		cfg, err = ctrl.GetConfig()
		Expect(err).NotTo(HaveOccurred())
	}

	err = humiov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	if helpers.UseEnvtest() {
		k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient).NotTo(BeNil())

		k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:  scheme.Scheme,
			Metrics: metricsserver.Options{BindAddress: "0"},
			Logger:  log,
		})
		Expect(err).ToNot(HaveOccurred())

		err = (&controller.HumioTelemetryReconciler{
			Client:       k8sManager.GetClient(),
			CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 5},
			BaseLogger:   log,
			HumioClient:  testHumioClient,
		}).SetupWithManager(k8sManager)
		Expect(err).ToNot(HaveOccurred())

		err = (&controller.HumioClusterReconciler{
			Client:       k8sManager.GetClient(),
			CommonConfig: controller.CommonConfig{RequeuePeriod: time.Second * 5},
			BaseLogger:   log,
			HumioClient:  testHumioClient,
		}).SetupWithManager(k8sManager)
		Expect(err).ToNot(HaveOccurred())

		testK8sManager = k8sManager

		go func() {
			defer GinkgoRecover()
			err = k8sManager.Start(ctx)
			Expect(err).ToNot(HaveOccurred(), "failed to run manager")
		}()
	} else {
		k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient).NotTo(BeNil())
	}
})

var _ = AfterSuite(func() {
	if cancelContext != nil {
		cancelContext()
	}

	By("tearing down the test environment")
	if testEnv != nil {
		err := testEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
})
