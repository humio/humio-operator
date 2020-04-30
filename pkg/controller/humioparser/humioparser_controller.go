package humioparser

import (
	"context"
	"fmt"
	"reflect"
	"time"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
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

const humioFinalizer = "finalizer.humio.com"

// Add creates a new HumioParser Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	return &ReconcileHumioParser{
		client:      mgr.GetClient(),
		scheme:      mgr.GetScheme(),
		humioClient: humio.NewClient(logger.Sugar(), &humioapi.Config{}),
		logger:      logger.Sugar(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("humioparser-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource HumioParser
	err = c.Watch(&source.Kind{Type: &corev1alpha1.HumioParser{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileHumioParser implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileHumioParser{}

// ReconcileHumioParser reconciles a HumioParser object
type ReconcileHumioParser struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client      client.Client
	scheme      *runtime.Scheme
	humioClient humio.Client
	logger      *zap.SugaredLogger
}

// Reconcile reads that state of the cluster for a HumioParser object and makes changes based on the state read
// and what is in the HumioParser.Spec
func (r *ReconcileHumioParser) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	r.logger = logger.Sugar().With("Request.Namespace", request.Namespace, "Request.Name", request.Name, "Request.Type", helpers.GetTypeName(r))
	r.logger.Info("Reconciling HumioParser")
	// TODO: Add back controllerutil.SetControllerReference everywhere we create k8s objects

	// Fetch the HumioParser instance
	hp := &corev1alpha1.HumioParser{}
	err := r.client.Get(context.TODO(), request.NamespacedName, hp)
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

	cluster, err := helpers.NewCluster(hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace)
	if err != nil {
		r.logger.Error("parser must have one of ManagedClusterName and ExternalClusterName set: %s", err)
		return reconcile.Result{}, err
	}

	secret, err := kubernetes.GetSecret(context.TODO(), r.client, kubernetes.ServiceTokenSecretName, hp.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			r.logger.Infof("api token secret does not exist for cluster: %s", cluster.Name())
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	url, err := cluster.Url(r.client)
	if err != nil {
		return reconcile.Result{}, err
	}
	err = r.humioClient.Authenticate(&humioapi.Config{
		Token:   string(secret.Data["token"]),
		Address: url,
	})
	if err != nil {
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}

	defer func(ctx context.Context, humioClient humio.Client, hp *corev1alpha1.HumioParser) {
		curParser, err := humioClient.GetParser(hp)
		if err != nil {
			r.setState(ctx, corev1alpha1.HumioParserStateUnknown, hp)
			return
		}
		emptyParser := humioapi.Parser{}
		if reflect.DeepEqual(emptyParser, *curParser) {
			r.setState(ctx, corev1alpha1.HumioParserStateNotFound, hp)
			return
		}
		r.setState(ctx, corev1alpha1.HumioParserStateExists, hp)
	}(context.TODO(), r.humioClient, hp)

	r.logger.Info("Checking if parser is marked to be deleted")
	// Check if the HumioParser instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioParserMarkedToBeDeleted := hp.GetDeletionTimestamp() != nil
	if isHumioParserMarkedToBeDeleted {
		r.logger.Info("Parser marked to be deleted")
		if helpers.ContainsElement(hp.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.logger.Info("Parser contains finalizer so run finalizer method")
			if err := r.finalize(hp); err != nil {
				r.logger.Infof("Finalizer method returned error: %v", err)
				return reconcile.Result{}, err
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.logger.Info("Finalizer done. Removing finalizer")
			hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), humioFinalizer))
			err := r.client.Update(context.TODO(), hp)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.logger.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hp.GetFinalizers(), humioFinalizer) {
		r.logger.Info("Finalizer not present, adding finalizer to parser")
		if err := r.addFinalizer(hp); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Get current parser
	r.logger.Info("get current parser")
	curParser, err := r.humioClient.GetParser(hp)
	if err != nil {
		r.logger.Infof("could not check if parser exists in repo %s: %+v", hp.Spec.RepositoryName, err)
		return reconcile.Result{}, fmt.Errorf("could not check if parser exists: %s", err)
	}

	emptyParser := humioapi.Parser{Tests: []humioapi.ParserTestCase{}, TagFields: nil} // when using a real humio, we need to do this, ensure tests work the same way. tests currently set this to nil whereas it should be the empty list
	if reflect.DeepEqual(emptyParser, *curParser) {
		r.logger.Info("parser doesn't exist. Now adding parser")
		// create parser
		_, err := r.humioClient.AddParser(hp)
		if err != nil {
			r.logger.Infof("could not create parser: %s", err)
			return reconcile.Result{}, fmt.Errorf("could not create parser: %s", err)
		}
		r.logger.Infof("created parser: %s", hp.Spec.Name)
		return reconcile.Result{Requeue: true}, nil
	}

	if (curParser.Script != hp.Spec.ParserScript) || !reflect.DeepEqual(curParser.TagFields, hp.Spec.TagFields) || !reflect.DeepEqual(curParser.Tests, helpers.MapTests(hp.Spec.TestData, helpers.ToTestCase)) {
		r.logger.Info("parser information differs, triggering update")
		_, err = r.humioClient.UpdateParser(hp)
		if err != nil {
			r.logger.Infof("could not update parser: %s", err)
			return reconcile.Result{}, fmt.Errorf("could not update parser: %s", err)
		}
	}

	// TODO: handle updates to parser name and repositoryName. Right now we just create the new parser,
	// and "leak/leave behind" the old parser.
	// A workaround for now is to delete the parser CR and create it again.

	// All done, requeue every 30 seconds even if no changes were made
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
}

func (r *ReconcileHumioParser) finalize(hp *corev1alpha1.HumioParser) error {
	return r.humioClient.DeleteParser(hp)
}

func (r *ReconcileHumioParser) addFinalizer(hp *corev1alpha1.HumioParser) error {
	r.logger.Info("Adding Finalizer for the HumioParser")
	hp.SetFinalizers(append(hp.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.client.Update(context.TODO(), hp)
	if err != nil {
		r.logger.Error(err, "Failed to update HumioParser with finalizer")
		return err
	}
	return nil
}

func (r *ReconcileHumioParser) setState(ctx context.Context, state string, hp *corev1alpha1.HumioParser) error {
	hp.Status.State = state
	return r.client.Status().Update(ctx, hp)
}
