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

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	uberzap "go.uber.org/zap"

	"github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/registries"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1beta1 "github.com/humio/humio-operator/api/v1beta1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme = runtime.NewScheme()
	// We override these using ldflags when running "go build"
	commit  = "none"
	date    = "unknown"
	version = "master"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))

	utilruntime.Must(corev1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1beta1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var logLevel string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	var requeuePeriod time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.DurationVar(&requeuePeriod, "requeue-period", 15*time.Second,
		"The default reconciliation requeue period for all Humio* resources.")
	flag.StringVar(&logLevel, "loglevel", "INFO", "The level at which to log output. "+
		"Possible values: DEBUG, INFO, WARN, ERROR, DPANIC, PANIC, FATAL.")
	flag.Parse()

	logLevel = strings.Trim(logLevel, "\" ")

	var log logr.Logger
	zapLog, _ := helpers.NewLogger(logLevel)
	defer func(zapLog *uberzap.Logger) {
		_ = zapLog.Sync()
	}(zapLog)
	log = zapr.NewLogger(zapLog).WithValues("Operator.Commit", commit, "Operator.Date", date, "Operator.Version", version)
	ctrl.SetLogger(log)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		ctrl.Log.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create watchers for metrics and webhooks certificates
	var metricsCertWatcher *certwatcher.CertWatcher
	var err error

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.4/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.4/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		ctrl.Log.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		var err error
		metricsCertWatcher, err = certwatcher.New(
			filepath.Join(metricsCertPath, metricsCertName),
			filepath.Join(metricsCertPath, metricsCertKey),
		)
		if err != nil {
			ctrl.Log.Error(err, "to initialize metrics certificate watcher", "error", err)
			os.Exit(1)
		}

		metricsServerOptions.TLSOpts = append(metricsServerOptions.TLSOpts, func(config *tls.Config) {
			config.GetCertificate = metricsCertWatcher.GetCertificate
		})
	}

	cacheOptions, err := helpers.GetCacheOptionsWithWatchNamespace()
	if err != nil {
		ctrl.Log.Info("unable to get WatchNamespace: the manager will watch and manage resources in all namespaces")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          nil,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "d7845218.humio.com",
		Logger:                 log,
		Cache:                  cacheOptions,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	watchedNamespaces := []string{}
	for namespace := range cacheOptions.DefaultNamespaces {
		watchedNamespaces = append(watchedNamespaces, namespace)
	}
	if len(watchedNamespaces) > 0 {
		log.Info("Watching specific namespaces", "namespaces", strings.Join(watchedNamespaces, ", "))
	} else {
		log.Info("Watching all namespaces")
	}

	if helpers.UseCertManager() {
		if err = cmapi.AddToScheme(mgr.GetScheme()); err != nil {
			ctrl.Log.Error(err, "unable to add cert-manager to scheme")
			os.Exit(2)
		}
	}

	setupControllers(mgr, log, requeuePeriod)

	if metricsCertWatcher != nil {
		ctrl.Log.Info("Adding metrics certificate watcher to manager")
		if err := mgr.Add(metricsCertWatcher); err != nil {
			ctrl.Log.Error(err, "unable to add metrics certificate watcher to manager")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	ctrl.Log.Info(fmt.Sprintf("starting manager for humio-operator %s (%s on %s)", version, commit, date))
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// setupController is a helper function that sets up a controller and exits on error
func setupController(name string, setupFunc func() error) {
	if err := setupFunc(); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", name)
		os.Exit(1)
	}
}

// setupControllerNoExit is a helper function that sets up a controller and logs errors without exiting
func setupControllerNoExit(name string, setupFunc func() error) {
	if err := setupFunc(); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", name)
	}
}

func setupControllers(mgr ctrl.Manager, log logr.Logger, requeuePeriod time.Duration) {
	userAgent := fmt.Sprintf("humio-operator/%s (%s on %s)", version, commit, date)

	setupController("HumioAction", func() error {
		return (&controller.HumioActionReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioAggregateAlert", func() error {
		return (&controller.HumioAggregateAlertReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioAlert", func() error {
		return (&controller.HumioAlertReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioBootstrapToken", func() error {
		return (&controller.HumioBootstrapTokenReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			BaseLogger: log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioCluster", func() error {
		return (&controller.HumioClusterReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioExternalCluster", func() error {
		return (&controller.HumioExternalClusterReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioEventForwardingRule", func() error {
		return (&controller.HumioEventForwardingRuleReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioEventForwarder", func() error {
		return (&controller.HumioEventForwarderReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupControllerNoExit("HumioFilterAlert", func() error {
		return (&controller.HumioFilterAlertReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupControllerNoExit("HumioFeatureFlag", func() error {
		return (&controller.HumioFeatureFlagReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioIngestToken", func() error {
		return (&controller.HumioIngestTokenReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioParser", func() error {
		return (&controller.HumioParserReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioRepository", func() error {
		return (&controller.HumioRepositoryReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioScheduledSearch", func() error {
		return (&controller.HumioScheduledSearchReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioSavedQuery", func() error {
		return (&controller.HumioSavedQueryReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioView", func() error {
		return (&controller.HumioViewReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioUser", func() error {
		return (&controller.HumioUserReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioGroup", func() error {
		return (&controller.HumioGroupReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioViewPermissionRole", func() error {
		return (&controller.HumioViewPermissionRoleReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioSystemPermissionRole", func() error {
		return (&controller.HumioSystemPermissionRoleReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioOrganizationPermissionRole", func() error {
		return (&controller.HumioOrganizationPermissionRoleReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioMultiClusterSearchView", func() error {
		return (&controller.HumioMultiClusterSearchViewReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioIPFilter", func() error {
		return (&controller.HumioIPFilterReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioViewToken", func() error {
		return (&controller.HumioViewTokenReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioSystemToken", func() error {
		return (&controller.HumioSystemTokenReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioOrganizationToken", func() error {
		return (&controller.HumioOrganizationTokenReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	setupController("HumioPdfRenderService", func() error {
		return (&controller.HumioPdfRenderServiceReconciler{
			Client:     mgr.GetClient(),
			Scheme:     mgr.GetScheme(),
			BaseLogger: log,
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
		}).SetupWithManager(mgr)
	})
	setupController("HumioTelemetry", func() error {
		return (&controller.HumioTelemetryReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	httpClient := registries.NewHTTPClient(api.Config{Insecure: false})
	setupController("HumioPackageRegistry", func() error {
		return (&controller.HumioPackageRegistryReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HTTPClient: httpClient,
			BaseLogger: log,
		}).SetupWithManager(mgr)
	})

	setupController("HumioPackage", func() error {
		return (&controller.HumioPackageReconciler{
			Client: mgr.GetClient(),
			CommonConfig: controller.CommonConfig{
				RequeuePeriod: requeuePeriod,
			},
			HumioClient: humio.NewClient(log, userAgent),
			HTTPClient:  httpClient,
			BaseLogger:  log,
		}).SetupWithManager(mgr)
	})
	// +kubebuilder:scaffold:builder
}
