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
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	humioapi "github.com/humio/cli/api"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1beta1"
	openshiftsecurityv1 "github.com/openshift/api/security/v1"
	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/controllers"
	//+kubebuilder:scaffold:imports
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

	utilruntime.Must(humiov1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	var log logr.Logger
	zapLog, _ := helpers.NewLogger()
	defer zapLog.Sync()
	log = zapr.NewLogger(zapLog).WithValues("Operator.Commit", commit, "Operator.Date", date, "Operator.Version", version)
	ctrl.SetLogger(log)

	ctrl.Log.Info("starting humio-operator")

	watchNamespace, err := getWatchNamespace()
	if err != nil {
		ctrl.Log.Error(err, "unable to get WatchNamespace, "+
			"the manager will watch and manage resources in all namespaces")
	}

	options := ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "d7845218.humio.com",
		Namespace:              watchNamespace,
	}

	// Add support for MultiNamespace set in WATCH_NAMESPACE (e.g ns1,ns2)
	if strings.Contains(watchNamespace, ",") {
		ctrl.Log.Info(fmt.Sprintf("manager will be watching namespace %q", watchNamespace))
		// configure cluster-scoped with MultiNamespacedCacheBuilder
		options.Namespace = ""
		options.NewCache = cache.MultiNamespacedCacheBuilder(strings.Split(watchNamespace, ","))
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), options)
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if helpers.IsOpenShift() {
		if err = openshiftsecurityv1.AddToScheme(mgr.GetScheme()); err != nil {
			ctrl.Log.Error(err, "unable to add cert-manager to scheme")
			os.Exit(2)
		}
	}

	if helpers.UseCertManager() {
		if err = cmapi.AddToScheme(mgr.GetScheme()); err != nil {
			ctrl.Log.Error(err, "unable to add cert-manager to scheme")
			os.Exit(2)
		}
	}

	userAgent := fmt.Sprintf("humio-operator/%s (%s on %s)", version, commit, date)

	if err = (&controllers.HumioExternalClusterReconciler{
		Client:      mgr.GetClient(),
		HumioClient: humio.NewClient(log, &humioapi.Config{}, userAgent),
		BaseLogger:  log,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "HumioExternalCluster")
		os.Exit(1)
	}
	if err = (&controllers.HumioClusterReconciler{
		Client:      mgr.GetClient(),
		HumioClient: humio.NewClient(log, &humioapi.Config{}, userAgent),
		BaseLogger:  log,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "HumioCluster")
		os.Exit(1)
	}
	if err = (&controllers.HumioIngestTokenReconciler{
		Client:      mgr.GetClient(),
		HumioClient: humio.NewClient(log, &humioapi.Config{}, userAgent),
		BaseLogger:  log,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "HumioIngestToken")
		os.Exit(1)
	}
	if err = (&controllers.HumioParserReconciler{
		Client:      mgr.GetClient(),
		HumioClient: humio.NewClient(log, &humioapi.Config{}, userAgent),
		BaseLogger:  log,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "HumioParser")
		os.Exit(1)
	}
	if err = (&controllers.HumioRepositoryReconciler{
		Client:      mgr.GetClient(),
		HumioClient: humio.NewClient(log, &humioapi.Config{}, userAgent),
		BaseLogger:  log,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "HumioRepository")
		os.Exit(1)
	}
	if err = (&controllers.HumioViewReconciler{
		Client:      mgr.GetClient(),
		HumioClient: humio.NewClient(log, &humioapi.Config{}, userAgent),
		BaseLogger:  log,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "HumioView")
		os.Exit(1)
	}
	if err = (&controllers.HumioActionReconciler{
		Client:      mgr.GetClient(),
		HumioClient: humio.NewClient(log, &humioapi.Config{}, userAgent),
		BaseLogger:  log,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "HumioAction")
		os.Exit(1)
	}
	if err = (&controllers.HumioAlertReconciler{
		Client:      mgr.GetClient(),
		HumioClient: humio.NewClient(log, &humioapi.Config{}, userAgent),
		BaseLogger:  log,
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create controller", "controller", "HumioAlert")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

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
