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

package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	uberzap "go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
)

var ownsNodePoolTestEnv *envtest.Environment
var ownsNodePoolK8sClient client.Client
var ownsNodePoolK8sManager ctrl.Manager
var ownsNodePoolTestNamespace string
var ownsNodePoolCtx context.Context
var ownsNodePoolCancel context.CancelFunc

func TestOwnsNodePoolPredicate(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HumioCluster Owns HumioNodePool Suite")
}

var _ = BeforeSuite(func() {
	var log logr.Logger
	zapLog, _ := helpers.NewLogger("info")
	defer func(zapLog *uberzap.Logger) {
		_ = zapLog.Sync()
	}(zapLog)
	log = zapr.NewLogger(zapLog).WithSink(GinkgoLogr.GetSink())
	logf.SetLogger(log)

	By("bootstrapping test environment")
	ownsNodePoolTestEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	var cfg *rest.Config
	var err error

	Eventually(func() error {
		cfg, err = ownsNodePoolTestEnv.Start()
		return err
	}, time.Second*30, time.Second*1).Should(Succeed())

	Expect(cfg).NotTo(BeNil())

	err = humiov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	ownsNodePoolK8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&HumioClusterReconciler{
		Client:       ownsNodePoolK8sManager.GetClient(),
		CommonConfig: CommonConfig{},
		BaseLogger:   log,
		HumioClient:  humio.NewClient(log, "test-user-agent"),
	}).SetupWithManager(ownsNodePoolK8sManager)
	Expect(err).ToNot(HaveOccurred())

	ownsNodePoolCtx, ownsNodePoolCancel = context.WithCancel(context.Background())
	go func() {
		defer GinkgoRecover()
		err = ownsNodePoolK8sManager.Start(ownsNodePoolCtx)
		Expect(err).ToNot(HaveOccurred())
	}()

	ownsNodePoolK8sClient = ownsNodePoolK8sManager.GetClient()
	Expect(ownsNodePoolK8sClient).ToNot(BeNil())

	ownsNodePoolTestNamespace = fmt.Sprintf("owns-nodepool-test-%d", GinkgoParallelProcess())

	By(fmt.Sprintf("Creating test namespace: %s", ownsNodePoolTestNamespace))
	testNamespace := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ownsNodePoolTestNamespace,
		},
	}
	err = ownsNodePoolK8sClient.Create(context.Background(), &testNamespace)
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if ownsNodePoolTestNamespace != "" && ownsNodePoolK8sClient != nil {
		By(fmt.Sprintf("Removing test namespace: %s", ownsNodePoolTestNamespace))
		err := ownsNodePoolK8sClient.Delete(context.Background(),
			&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ownsNodePoolTestNamespace,
				},
			},
		)
		Expect(err).ToNot(HaveOccurred())
	}
	ownsNodePoolCancel()
	if ownsNodePoolTestEnv != nil {
		err := ownsNodePoolTestEnv.Stop()
		Expect(err).NotTo(HaveOccurred())
	}
})

var _ = Describe("HumioCluster Owns HumioNodePool with GenerationChangedPredicate", Label("envtest"), func() {
	var hc *humiov1alpha1.HumioCluster
	var shadowCR *humiov1alpha1.HumioNodePool

	BeforeEach(func() {
		ctx := context.Background()

		hc = &humiov1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("test-cluster-%d", time.Now().UnixNano()),
				Namespace: ownsNodePoolTestNamespace,
			},
			Spec: humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					Image: "humio/humio-core:1.200.0",
				},
			},
		}

		err := ownsNodePoolK8sClient.Create(ctx, hc)
		Expect(err).ToNot(HaveOccurred())

		shadowCR = &humiov1alpha1.HumioNodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-test-pool", hc.Name),
				Namespace: hc.Namespace,
				Annotations: map[string]string{
					"humio.com/managed-by": "humiocluster-shadow",
				},
			},
			Spec: humiov1alpha1.HumioNodePoolSpec{
				Name:        "test-pool",
				ClusterName: hc.Name,
				NodeCount:   3,
			},
		}

		err = ctrl.SetControllerReference(hc, shadowCR, scheme.Scheme)
		Expect(err).ToNot(HaveOccurred())

		err = ownsNodePoolK8sClient.Create(ctx, shadowCR)
		Expect(err).ToNot(HaveOccurred())

		initialGeneration := shadowCR.Generation
		Expect(initialGeneration).To(BeNumerically(">", 0))
	})

	AfterEach(func() {
		ctx := context.Background()
		if shadowCR != nil {
			_ = ownsNodePoolK8sClient.Delete(ctx, shadowCR)
		}
		if hc != nil {
			_ = ownsNodePoolK8sClient.Delete(ctx, hc)
		}
	})

	Context("when shadow CR .spec.nodeCount is patched", func() {
		It("should trigger reconcile of parent HumioCluster (generation increments)", func() {
			ctx := context.Background()

			initialObservedGen := hc.GetObservedGeneration()

			err := ownsNodePoolK8sClient.Get(ctx, client.ObjectKeyFromObject(shadowCR), shadowCR)
			Expect(err).ToNot(HaveOccurred())

			shadowCR.Spec.NodeCount = 5
			err = ownsNodePoolK8sClient.Update(ctx, shadowCR)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() int64 {
				var updatedCR humiov1alpha1.HumioNodePool
				_ = ownsNodePoolK8sClient.Get(ctx, client.ObjectKeyFromObject(shadowCR), &updatedCR)
				return updatedCR.Generation
			}, time.Second*10, time.Millisecond*500).Should(BeNumerically(">", 1))

			Eventually(func() int64 {
				var updatedHC humiov1alpha1.HumioCluster
				_ = ownsNodePoolK8sClient.Get(ctx, client.ObjectKeyFromObject(hc), &updatedHC)
				return updatedHC.GetObservedGeneration()
			}, time.Second*15, time.Second*1).Should(BeNumerically(">", initialObservedGen))
		})
	})

	Context("when shadow CR .status.currentReplicas is patched", func() {
		It("should NOT trigger reconcile (status changes do not increment generation)", func() {
			ctx := context.Background()

			err := ownsNodePoolK8sClient.Get(ctx, client.ObjectKeyFromObject(shadowCR), shadowCR)
			Expect(err).ToNot(HaveOccurred())

			initialGeneration := shadowCR.Generation
			initialObservedGen := hc.GetObservedGeneration()

			shadowCR.Status.CurrentReplicas = 42
			err = ownsNodePoolK8sClient.Status().Update(ctx, shadowCR)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func() int32 {
				var updatedCR humiov1alpha1.HumioNodePool
				_ = ownsNodePoolK8sClient.Get(ctx, client.ObjectKeyFromObject(shadowCR), &updatedCR)
				return updatedCR.Status.CurrentReplicas
			}, time.Second*10, time.Millisecond*500).Should(Equal(int32(42)))

			err = ownsNodePoolK8sClient.Get(ctx, client.ObjectKeyFromObject(shadowCR), shadowCR)
			Expect(err).ToNot(HaveOccurred())
			Expect(shadowCR.Generation).To(Equal(initialGeneration))

			Consistently(func() int64 {
				var updatedHC humiov1alpha1.HumioCluster
				_ = ownsNodePoolK8sClient.Get(ctx, client.ObjectKeyFromObject(hc), &updatedHC)
				return updatedHC.GetObservedGeneration()
			}, time.Second*5, time.Second*1).Should(Equal(initialObservedGen))
		})
	})
})
