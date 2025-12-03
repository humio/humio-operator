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
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1beta1 "github.com/humio/humio-operator/api/v1beta1"
	"github.com/humio/humio-operator/internal/controller"
	webhooks "github.com/humio/humio-operator/internal/controller/webhooks"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"

	uberzap "go.uber.org/zap"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
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
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var logLevel string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var requeuePeriod time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for webhook operator. "+
			"Enabling this will ensure there is only one active webhook operator.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "/tmp/k8s-webhook-server/serving-certs",
		"The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.DurationVar(&requeuePeriod, "requeue-period", 15*time.Second,
		"The default reconciliation requeue period for all Humio* resources.")
	flag.StringVar(&logLevel, "loglevel", "INFO", "The level at which to log output. "+
		"Possible values: DEBUG, INFO, WARN, ERROR, DPANIC, PANIC, FATAL.")
	flag.Parse()

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
		log.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Create webhook certificate watcher
	var webhookCertWatcher *certwatcher.CertWatcher
	var err error
	webhookTLSOpts := tlsOpts

	webhookCertGenerator := helpers.NewCertGenerator(webhookCertPath, webhookCertName, webhookCertKey,
		helpers.GetOperatorWebhookServiceName(), helpers.GetOperatorNamespace(),
	)
	err = webhookCertGenerator.GenerateIfNotExists()
	if err != nil {
		ctrl.Log.Error(err, "Failed to generate webhook certificate")
	}

	log.Info("Initializing webhook certificate watcher using provided certificates",
		"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

	webhookCertWatcher, err = certwatcher.New(
		filepath.Join(webhookCertPath, webhookCertName),
		filepath.Join(webhookCertPath, webhookCertKey),
	)
	if err != nil {
		log.Error(err, "Failed to initialize webhook certificate watcher")
		os.Exit(1)
	}

	webhookTLSOpts = append(webhookTLSOpts, func(config *tls.Config) {
		config.GetCertificate = webhookCertWatcher.GetCertificate
	})

	webhookServer := ctrlwebhook.NewServer(ctrlwebhook.Options{
		TLSOpts: webhookTLSOpts,
		Port:    9443,
		Host:    "0.0.0.0",
	})

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
			TLSOpts:       tlsOpts,
		},
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "webhook-operator.humio.com",
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
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if helpers.UseCertManager() {
		log.Info("cert-manager support enabled")
	}

	// Register webhooks with manager
	setupWebhooks(mgr, log, requeuePeriod, webhookCertGenerator)

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if webhookCertWatcher != nil {
		log.Info("Adding webhook certificate watcher to manager")
		if err := mgr.Add(webhookCertWatcher); err != nil {
			log.Error(err, "unable to add webhook certificate watcher to manager")
			os.Exit(1)
		}
	}

	log.Info("starting webhook operator")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "problem running webhook operator")
		os.Exit(1)
	}
}

func setupWebhooks(mgr ctrl.Manager, log logr.Logger, requeuePeriod time.Duration,
	CertGenerator *helpers.WebhookCertGenerator) {

	userAgent := fmt.Sprintf("humio-operator/%s (%s on %s)", version, commit, date)

	// Setup validation + conversion webhooks
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&corev1alpha1.HumioScheduledSearch{}).
		WithValidator(&webhooks.HumioScheduledSearchValidator{
			BaseLogger:  log,
			Client:      mgr.GetClient(),
			HumioClient: humio.NewClient(log, userAgent),
		}).
		WithDefaulter(nil).
		Complete(); err != nil {
		ctrl.Log.Error(err, "unable to create conversion webhook for corev1alpha1.HumioScheduledSearch")
		os.Exit(1)
	}
	if err := ctrl.NewWebhookManagedBy(mgr).
		For(&corev1beta1.HumioScheduledSearch{}).
		WithValidator(&webhooks.HumioScheduledSearchValidator{
			BaseLogger:  log,
			Client:      mgr.GetClient(),
			HumioClient: humio.NewClient(log, userAgent),
		}).
		WithDefaulter(nil).
		Complete(); err != nil {
		ctrl.Log.Error(err, "unable to create conversion webhook for corev1beta1.HumioScheduledSearch")
		os.Exit(1)
	}
	// webhook setup initial reconciliation on existing resources
	webhookSetupReconciler := controller.NewProductionWebhookSetupReconciler(
		mgr.GetClient(),
		mgr.GetCache(),
		log,
		CertGenerator,
		helpers.GetOperatorName(),
		helpers.GetOperatorNamespace(),
		requeuePeriod,
	)

	// webhookSetupReconciler is a startup-only component
	// runs Start to handle the initial creation or sync for existing resources
	if err := mgr.Add(webhookSetupReconciler); err != nil {
		ctrl.Log.Error(err, "unable to run initial sync for", "controller", "WebhookSetupReconciler")
		os.Exit(1)
	}
}
