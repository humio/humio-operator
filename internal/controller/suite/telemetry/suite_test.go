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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	managerCtx           context.Context
	managerCancel        context.CancelFunc
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

	// Wait for CRDs to be available in the test cluster
	if helpers.UseEnvtest() {
		By("waiting for CRDs to be available in test environment")
		Eventually(func() bool {
			// Create a discovery client to check if CRDs are ready
			discoveryClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
			if err != nil {
				return false
			}

			// Check if the CRD GroupVersionKinds are available
			gvks := []schema.GroupVersionKind{
				{Group: "core.humio.com", Version: "v1alpha1", Kind: "HumioTelemetryCollection"},
				{Group: "core.humio.com", Version: "v1alpha1", Kind: "HumioTelemetryExport"},
			}

			for _, gvk := range gvks {
				if !discoveryClient.Scheme().Recognizes(gvk) {
					return false
				}
			}

			return true
		}, time.Second*30, time.Millisecond*100).Should(BeTrue(), "CRDs should be available in test environment")
	}

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

	err = (&controller.HumioTelemetryCollectionReconciler{
		Client: k8sManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		HumioClient: testHumioClient,
		BaseLogger:  log,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&controller.HumioTelemetryExportReconciler{
		Client: k8sManager.GetClient(),
		CommonConfig: controller.CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		BaseLogger: log,
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

	// Create a context that can be cancelled to gracefully shut down the manager
	managerCtx, managerCancel = context.WithCancel(context.Background())
	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(managerCtx)
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
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		Expect(err).ToNot(HaveOccurred())
	}

	suite.CreateDockerRegredSecret(context.TODO(), testNamespace, k8sClient)
})

var _ = AfterSuite(func() {
	// Cancel the manager context to gracefully shut down controllers
	if managerCancel != nil {
		By("stopping controller manager")
		managerCancel()
		// Give the manager time to shut down gracefully
		time.Sleep(2 * time.Second)
	}

	if testProcessNamespace != "" && k8sClient != nil {
		By(fmt.Sprintf("Removing regcred secret for namespace: %s", testProcessNamespace))
		_ = k8sClient.Delete(context.TODO(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      suite.DockerRegistryCredentialsSecretName,
				Namespace: testProcessNamespace,
			},
		})

		By(fmt.Sprintf("Force cleaning telemetry resources in namespace: %s", testProcessNamespace))

		// Force delete telemetry collections by removing finalizers
		collections := &humiov1alpha1.HumioTelemetryCollectionList{}
		if err := k8sClient.List(context.TODO(), collections, client.InNamespace(testProcessNamespace)); err == nil {
			for i := range collections.Items {
				collection := &collections.Items[i]
				if len(collection.Finalizers) > 0 {
					collection.Finalizers = []string{}
					_ = k8sClient.Update(context.TODO(), collection)
				}
			}
		}

		// Force delete telemetry exports by removing finalizers
		exports := &humiov1alpha1.HumioTelemetryExportList{}
		if err := k8sClient.List(context.TODO(), exports, client.InNamespace(testProcessNamespace)); err == nil {
			for i := range exports.Items {
				export := &exports.Items[i]
				if len(export.Finalizers) > 0 {
					export.Finalizers = []string{}
					_ = k8sClient.Update(context.TODO(), export)
				}
			}
		}

		By(fmt.Sprintf("Removing test namespace: %s", testProcessNamespace))
		ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
		defer cancel()

		err := k8sClient.Delete(ctx,
			&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: testProcessNamespace,
				},
			},
		)
		if err != nil {
			By(fmt.Sprintf("Warning: Failed to delete namespace %s: %v", testProcessNamespace, err))
		}
	}
	By("tearing down the test environment")
	if testEnv != nil {
		_ = testEnv.Stop()
	}
})
