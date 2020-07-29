package humioexternalcluster

import (
	"context"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/humio"
	"time"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// Add creates a new HumioExternalCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	return &ReconcileHumioExternalCluster{
		client:      mgr.GetClient(),
		scheme:      mgr.GetScheme(),
		humioClient: humio.NewClient(logger.Sugar(), &humioapi.Config{}),
		logger:      logger.Sugar(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("humioexternalcluster-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource HumioExternalCluster
	err = c.Watch(&source.Kind{Type: &corev1alpha1.HumioExternalCluster{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileHumioExternalCluster implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileHumioExternalCluster{}

// ReconcileHumioExternalCluster reconciles a HumioExternalCluster object
type ReconcileHumioExternalCluster struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client      client.Client
	scheme      *runtime.Scheme
	humioClient humio.Client
	logger      *zap.SugaredLogger
}

// Reconcile reads that state of the cluster for a HumioExternalCluster object and makes changes based on the state read
// and what is in the HumioExternalCluster.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileHumioExternalCluster) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	r.logger = logger.Sugar().With("Request.Namespace", request.Namespace, "Request.Name", request.Name, "Request.Type", helpers.GetTypeName(r))
	r.logger.Info("Reconciling HumioExternalCluster")

	// Fetch the HumioExternalCluster instance
	hec := &corev1alpha1.HumioExternalCluster{}
	err := r.client.Get(context.TODO(), request.NamespacedName, hec)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	if hec.Status.State == "" {
		err := r.setState(context.TODO(), corev1alpha1.HumioExternalClusterStateUnknown, hec)
		if err != nil {
			r.logger.Infof("unable to set cluster state: %s", err)
			return reconcile.Result{}, err
		}
	}

	cluster, err := helpers.NewCluster(context.TODO(), r.client, "", hec.Name, hec.Namespace, helpers.UseCertManager())
	if err != nil || cluster.Config() == nil {
		r.logger.Error("unable to obtain humio client config: %s", err)
		return reconcile.Result{}, err
	}

	err = r.humioClient.Authenticate(cluster.Config())
	if err != nil {
		r.logger.Warnf("unable to authenticate humio client: %s", err)
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}

	err = r.humioClient.TestAPIToken()
	if err != nil {
		err := r.setState(context.TODO(), corev1alpha1.HumioExternalClusterStateUnknown, hec)
		if err != nil {
			r.logger.Infof("unable to set cluster state: %s", err)
			return reconcile.Result{}, err
		}
	}

	err = r.setState(context.TODO(), corev1alpha1.HumioExternalClusterStateReady, hec)
	if err != nil {
		r.logger.Infof("unable to set cluster state: %s", err)
		return reconcile.Result{}, err
	}

	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
}
