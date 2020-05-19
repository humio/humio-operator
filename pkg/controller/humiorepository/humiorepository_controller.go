package humiorepository

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

// Add creates a new HumioRepository Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	return &ReconcileHumioRepository{
		client:      mgr.GetClient(),
		scheme:      mgr.GetScheme(),
		humioClient: humio.NewClient(logger.Sugar(), &humioapi.Config{}),
		logger:      logger.Sugar(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("humiorepository-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource HumioRepository
	err = c.Watch(&source.Kind{Type: &corev1alpha1.HumioRepository{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileHumioRepository implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileHumioRepository{}

// ReconcileHumioRepository reconciles a HumioRepository object
type ReconcileHumioRepository struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client      client.Client
	scheme      *runtime.Scheme
	humioClient humio.Client
	logger      *zap.SugaredLogger
}

// Reconcile reads that state of the cluster for a HumioRepository object and makes changes based on the state read
// and what is in the HumioRepository.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileHumioRepository) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	r.logger = logger.Sugar().With("Request.Namespace", request.Namespace, "Request.Name", request.Name, "Request.Type", helpers.GetTypeName(r))
	r.logger.Info("Reconciling HumioRepository")
	// TODO: Add back controllerutil.SetControllerReference everywhere we create k8s objects

	// Fetch the HumioRepository instance
	hr := &corev1alpha1.HumioRepository{}
	err := r.client.Get(context.TODO(), request.NamespacedName, hr)
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

	cluster, err := helpers.NewCluster(hr.Spec.ManagedClusterName, hr.Spec.ExternalClusterName, hr.Namespace)
	if err != nil {
		r.logger.Error("repository must have one of ManagedClusterName and ExternalClusterName set: %s", err)
		return reconcile.Result{}, err
	}

	secret, err := kubernetes.GetSecret(context.TODO(), r.client, kubernetes.ServiceTokenSecretName, hr.Namespace)
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

	defer func(ctx context.Context, humioClient humio.Client, hr *corev1alpha1.HumioRepository) {
		curRepository, err := humioClient.GetRepository(hr)
		if err != nil {
			r.setState(ctx, corev1alpha1.HumioRepositoryStateUnknown, hr)
			return
		}
		emptyRepository := humioapi.Parser{}
		if reflect.DeepEqual(emptyRepository, *curRepository) {
			r.setState(ctx, corev1alpha1.HumioRepositoryStateNotFound, hr)
			return
		}
		r.setState(ctx, corev1alpha1.HumioRepositoryStateExists, hr)
	}(context.TODO(), r.humioClient, hr)

	r.logger.Info("Checking if repository is marked to be deleted")
	// Check if the HumioRepository instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioRepositoryMarkedToBeDeleted := hr.GetDeletionTimestamp() != nil
	if isHumioRepositoryMarkedToBeDeleted {
		r.logger.Info("Repository marked to be deleted")
		if helpers.ContainsElement(hr.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.logger.Info("Repository contains finalizer so run finalizer method")
			if err := r.finalize(hr); err != nil {
				r.logger.Infof("Finalizer method returned error: %v", err)
				return reconcile.Result{}, err
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.logger.Info("Finalizer done. Removing finalizer")
			hr.SetFinalizers(helpers.RemoveElement(hr.GetFinalizers(), humioFinalizer))
			err := r.client.Update(context.TODO(), hr)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.logger.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hr.GetFinalizers(), humioFinalizer) {
		r.logger.Info("Finalizer not present, adding finalizer to repository")
		if err := r.addFinalizer(hr); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Get current repository
	r.logger.Info("get current repository")
	curRepository, err := r.humioClient.GetRepository(hr)
	if err != nil {
		r.logger.Infof("could not check if repository exists: %s", err)
		return reconcile.Result{}, fmt.Errorf("could not check if repository exists: %s", err)
	}

	emptyRepository := humioapi.Repository{}
	if reflect.DeepEqual(emptyRepository, *curRepository) {
		r.logger.Info("repository doesn't exist. Now adding repository")
		// create repository
		_, err := r.humioClient.AddRepository(hr)
		if err != nil {
			r.logger.Infof("could not create repository: %s", err)
			return reconcile.Result{}, fmt.Errorf("could not create repository: %s", err)
		}
		r.logger.Infof("created repository: %s", hr.Spec.Name)
		return reconcile.Result{Requeue: true}, nil
	}

	if (curRepository.Description != hr.Spec.Description) || (curRepository.RetentionDays != float64(hr.Spec.Retention.TimeInDays)) || (curRepository.IngestRetentionSizeGB != float64(hr.Spec.Retention.IngestSizeInGB)) || (curRepository.StorageRetentionSizeGB != float64(hr.Spec.Retention.StorageSizeInGB)) {
		r.logger.Info("repository information differs, triggering update")
		_, err = r.humioClient.UpdateRepository(hr)
		if err != nil {
			r.logger.Infof("could not update repository: %s", err)
			return reconcile.Result{}, fmt.Errorf("could not update repository: %s", err)
		}
	}

	// TODO: handle updates to repositoryName. Right now we just create the new repository,
	// and "leak/leave behind" the old repository.
	// A solution could be to add an annotation that includes the "old name" so we can see if it was changed.
	// A workaround for now is to delete the repository CR and create it again.

	// All done, requeue every 30 seconds even if no changes were made
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
}

func (r *ReconcileHumioRepository) finalize(hr *corev1alpha1.HumioRepository) error {
	return r.humioClient.DeleteRepository(hr)
}

func (r *ReconcileHumioRepository) addFinalizer(hr *corev1alpha1.HumioRepository) error {
	r.logger.Info("Adding Finalizer for the HumioRepository")
	hr.SetFinalizers(append(hr.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.client.Update(context.TODO(), hr)
	if err != nil {
		r.logger.Error(err, "Failed to update HumioRepository with finalizer")
		return err
	}
	return nil
}

func (r *ReconcileHumioRepository) setState(ctx context.Context, state string, hr *corev1alpha1.HumioRepository) error {
	hr.Status.State = state
	return r.client.Status().Update(ctx, hr)
}
