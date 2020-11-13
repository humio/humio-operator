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

package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	humioapi "github.com/humio/cli/api"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1beta1"
	openshiftsecurityv1 "github.com/openshift/api/security/v1"
	uberzap "go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/openshift"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"

	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var k8sManager ctrl.Manager
var humioClient humio.Client
var testTimeout time.Duration

const testInterval = time.Second * 1

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	var log logr.Logger
	zapLog, _ := uberzap.NewProduction(uberzap.AddCaller(), uberzap.AddCallerSkip(1))
	defer zapLog.Sync()
	log = zapr.NewLogger(zapLog)
	logf.SetLogger(log)

	By("bootstrapping test environment")
	useExistingCluster := true
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		testTimeout = time.Second * 300
		testEnv = &envtest.Environment{
			UseExistingCluster: &useExistingCluster,
		}
		humioClient = humio.NewClient(log, &humioapi.Config{})
	} else {
		testTimeout = time.Second * 30
		testEnv = &envtest.Environment{
			// TODO: If we want to add support for TLS-functionality, we need to install cert-manager's CRD's
			CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
		}
		humioClient = humio.NewMocklient(
			humioapi.Cluster{},
			nil,
			nil,
			nil,
			"",
		)
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	err = humiov1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

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

	// +kubebuilder:scaffold:scheme

	watchNamespace, _ := getWatchNamespace()

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
	}

	k8sManager, err = ctrl.NewManager(cfg, options)
	Expect(err).ToNot(HaveOccurred())

	err = (&HumioExternalClusterReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		HumioClient: humioClient,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&HumioClusterReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		HumioClient: humioClient,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&HumioIngestTokenReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		HumioClient: humioClient,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&HumioParserReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		HumioClient: humioClient,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&HumioRepositoryReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		HumioClient: humioClient,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&HumioViewReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		HumioClient: humioClient,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClient = k8sManager.GetClient()
	Expect(k8sClient).ToNot(BeNil())

	if helpers.IsOpenShift() {
		var err error
		Eventually(func() bool {
			_, err = openshift.GetSecurityContextConstraints(context.Background(), k8sClient)
			if errors.IsNotFound(err) {
				// Object has not been created yet
				return true
			}
			if err != nil {
				// Some other error happened. Typically:
				//   <*cache.ErrCacheNotStarted | 0x31fc738>: {}
				//	   the cache is not started, can not read objects occurred
				return false
			}
			// At this point we know the object already exists.
			return true
		}, testTimeout, testInterval).Should(BeTrue())

		if errors.IsNotFound(err) {
			By("Simulating helm chart installation of the SecurityContextConstraints object")
			sccName := os.Getenv("OPENSHIFT_SCC_NAME")
			priority := int32(0)
			scc := openshiftsecurityv1.SecurityContextConstraints{
				ObjectMeta: metav1.ObjectMeta{
					Name:      sccName,
					Namespace: "default",
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
					"NET_BIND_SERVICE",
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
			Expect(k8sClient.Create(context.Background(), &scc))
		}
	}

	close(done)
}, 120)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})

// getWatchNamespace returns the Namespace the operator should be watching for changes
func getWatchNamespace() (string, error) {
	// WatchNamespaceEnvVar is the constant for env variable WATCH_NAMESPACE
	// which specifies the Namespace to watch.
	// An empty value means the operator is running with cluster scope.
	var watchNamespaceEnvVar = "WATCH_NAMESPACE"

	ns, found := os.LookupEnv(watchNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", watchNamespaceEnvVar)
	}
	return ns, nil
}
