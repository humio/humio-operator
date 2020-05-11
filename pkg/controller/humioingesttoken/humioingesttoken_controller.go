package humioingesttoken

import (
	"context"
	"fmt"
	"time"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const humioFinalizer = "finalizer.humio.com"

// Add creates a new HumioIngestToken Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	return &ReconcileHumioIngestToken{
		client:      mgr.GetClient(),
		scheme:      mgr.GetScheme(),
		humioClient: humio.NewClient(logger.Sugar(), &humioapi.Config{}),
		logger:      logger.Sugar(),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("humioingesttoken-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource HumioIngestToken
	err = c.Watch(&source.Kind{Type: &corev1alpha1.HumioIngestToken{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Secrets and requeue the owner HumioIngestToken
	var watchTypes []runtime.Object
	watchTypes = append(watchTypes, &corev1.Secret{})

	for _, watchType := range watchTypes {
		err = c.Watch(&source.Kind{Type: watchType}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &corev1alpha1.HumioIngestToken{},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// blank assignment to verify that ReconcileHumioIngestToken implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileHumioIngestToken{}

// ReconcileHumioIngestToken reconciles a HumioIngestToken object
type ReconcileHumioIngestToken struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client      client.Client
	scheme      *runtime.Scheme
	humioClient humio.Client
	logger      *zap.SugaredLogger
}

// Reconcile reads that state of the cluster for a HumioIngestToken object and makes changes based on the state read
// and what is in the HumioIngestToken.Spec
func (r *ReconcileHumioIngestToken) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	r.logger = logger.Sugar().With("Request.Namespace", request.Namespace, "Request.Name", request.Name, "Request.Type", helpers.GetTypeName(r))
	r.logger.Info("Reconciling HumioIngestToken")
	// TODO: Add back controllerutil.SetControllerReference everywhere we create k8s objects

	// Fetch the HumioIngestToken instance
	hit := &corev1alpha1.HumioIngestToken{}
	err := r.client.Get(context.TODO(), request.NamespacedName, hit)
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

	cluster, err := helpers.NewCluster(hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName, hit.Namespace)
	if err != nil {
		r.logger.Error("ingest token must have one of ManagedClusterName and ExternalClusterName set: %s", err)
		return reconcile.Result{}, err
	}

	secret, err := kubernetes.GetSecret(context.TODO(), r.client, kubernetes.ServiceTokenSecretName, hit.Namespace)
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

	r.logger.Info("Checking if ingest token is marked to be deleted")
	// Check if the HumioIngestToken instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioIngestTokenMarkedToBeDeleted := hit.GetDeletionTimestamp() != nil
	if isHumioIngestTokenMarkedToBeDeleted {
		r.logger.Info("Ingest token marked to be deleted")
		if helpers.ContainsElement(hit.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.logger.Info("Ingest token contains finalizer so run finalizer method")
			if err := r.finalize(hit); err != nil {
				r.logger.Infof("Finalizer method returned error: %v", err)
				return reconcile.Result{}, err
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.logger.Info("Finalizer done. Removing finalizer")
			hit.SetFinalizers(helpers.RemoveElement(hit.GetFinalizers(), humioFinalizer))
			err := r.client.Update(context.TODO(), hit)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.logger.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hit.GetFinalizers(), humioFinalizer) {
		r.logger.Info("Finalizer not present, adding finalizer to ingest token")
		if err := r.addFinalizer(hit); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Get current ingest token
	r.logger.Info("get current ingest token")
	curToken, err := r.humioClient.GetIngestToken(hit)
	if err != nil {
		r.logger.Infof("could not check if ingest token exists in repo %s: %+v", hit.Spec.RepositoryName, err)
		return reconcile.Result{}, fmt.Errorf("could not check if ingest token exists: %s", err)
	}
	// If token doesn't exist, the Get returns: nil, err.
	// How do we distinguish between "doesn't exist" and "error while executing get"?
	// TODO: change the way we do errors from the API so we can get rid of this hack
	emptyToken := humioapi.IngestToken{}
	if emptyToken == *curToken {
		r.logger.Info("ingest token doesn't exist. Now adding ingest token")
		// create token
		_, err := r.humioClient.AddIngestToken(hit)
		if err != nil {
			r.logger.Info("could not create ingest token: %s", err)
			return reconcile.Result{}, fmt.Errorf("could not create ingest token: %s", err)
		}
		r.logger.Infof("created ingest token: %s", hit.Spec.Name)
		return reconcile.Result{Requeue: true}, nil
	}

	// Trigger update if parser name changed
	if curToken.AssignedParser != hit.Spec.ParserName {
		r.logger.Info("token name or parser name differs, triggering update")
		_, updateErr := r.humioClient.UpdateIngestToken(hit)
		if updateErr != nil {
			return reconcile.Result{}, fmt.Errorf("could not update ingest token: %s", updateErr)
		}
	}

	err = r.ensureTokenSecretExists(context.TODO(), hit, cluster)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("could not ensure token secret exists: %s", err)
	}

	// TODO: handle updates to ingest token name and repositoryName. Right now we just create the new ingest token,
	// and "leak/leave behind" the old token.
	// A workaround for now is to delete the ingest token CR and create it again.

	// All done, requeue every 30 seconds even if no changes were made
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
}

func (r *ReconcileHumioIngestToken) finalize(hit *corev1alpha1.HumioIngestToken) error {
	return r.humioClient.DeleteIngestToken(hit)
}

func (r *ReconcileHumioIngestToken) addFinalizer(hit *corev1alpha1.HumioIngestToken) error {
	r.logger.Info("Adding Finalizer for the HumioIngestToken")
	hit.SetFinalizers(append(hit.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.client.Update(context.TODO(), hit)
	if err != nil {
		r.logger.Error(err, "Failed to update HumioIngestToken with finalizer")
		return err
	}
	return nil
}

func (r *ReconcileHumioIngestToken) ensureTokenSecretExists(ctx context.Context, hit *corev1alpha1.HumioIngestToken, cluster helpers.ClusterInterface) error {
	if hit.Spec.TokenSecretName == "" {
		return nil
	}

	ingestToken, err := r.humioClient.GetIngestToken(hit)
	if err != nil {
		return fmt.Errorf("failed to get ingest token: %s", err)
	}

	secretData := map[string][]byte{"token": []byte(ingestToken.Token)}
	desiredSecret := kubernetes.ConstructSecret(cluster.Name(), hit.Namespace, hit.Spec.TokenSecretName, secretData)
	if err := controllerutil.SetControllerReference(hit, desiredSecret, r.scheme); err != nil {
		return fmt.Errorf("could not set controller reference: %s", err)
	}

	existingSecret, err := kubernetes.GetSecret(ctx, r.client, hit.Spec.TokenSecretName, hit.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			err = r.client.Create(ctx, desiredSecret)
			if err != nil {
				return fmt.Errorf("unable to create ingest token secret for HumioIngestToken: %s", err)
			}
			r.logger.Infof("successfully created ingest token secret %s for HumioIngestToken %s", hit.Spec.TokenSecretName, hit.Name)
			prometheusMetrics.Counters.ServiceAccountSecretsCreated.Inc()
		}
	} else {
		// kubernetes secret exists, check if we need to update it
		r.logger.Infof("ingest token secret %s already exists for HumioIngestToken %s", hit.Spec.TokenSecretName, hit.Name)
		if string(existingSecret.Data["token"]) != string(desiredSecret.Data["token"]) {
			r.logger.Infof("ingest token %s stored in secret %s does not match the token in Humio. Updating token for %s.", hit.Name, hit.Spec.TokenSecretName)
			r.client.Update(ctx, desiredSecret)
		}
	}
	return nil
}
