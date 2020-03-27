package humiocluster

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	log                      = logf.Log.WithName("controller_humiocluster")
	serviceAccountSecretName = "developer"
	serviceTokenSecretName   = "developer-token"
	metricPodsCreated        = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "humio_controller_pods_created_total",
		Help: "Total number of pod objects created by controller",
	})
	metricPodsDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "humio_controller_pods_deleted_total",
		Help: "Total number of pod objects deleted by controller",
	})
	metricSecretsCreated = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "humio_controller_secrets_created_total",
		Help: "Total number of secret objects created by controller",
	})
)

// Add creates a new HumioCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileHumioCluster{
		client:      mgr.GetClient(),
		scheme:      mgr.GetScheme(),
		humioClient: humio.NewClient(&humioapi.Config{}),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("humiocluster-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource HumioCluster
	err = c.Watch(&source.Kind{Type: &corev1alpha1.HumioCluster{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Pods and requeue the owner HumioCluster
	var watchTypes []runtime.Object
	watchTypes = append(watchTypes, &corev1.Pod{})
	watchTypes = append(watchTypes, &corev1.Secret{})
	watchTypes = append(watchTypes, &corev1.Service{})

	for _, watchType := range watchTypes {
		err = c.Watch(&source.Kind{Type: watchType}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &corev1alpha1.HumioCluster{},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// blank assignment to verify that ReconcileHumioCluster implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileHumioCluster{}

// ReconcileHumioCluster reconciles a HumioCluster object
type ReconcileHumioCluster struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client      client.Client
	humioClient humio.Client
	scheme      *runtime.Scheme
	logger      logr.Logger
}

// Reconcile reads that state of the cluster for a HumioCluster object and makes changes based on the state read
// and what is in the HumioCluster.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileHumioCluster) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	r.logger = log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	r.logger.Info("Reconciling HumioCluster")

	// Fetch the HumioCluster
	humioCluster := &corev1alpha1.HumioCluster{}
	err := r.client.Get(context.TODO(), request.NamespacedName, humioCluster)
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

	// Set defaults
	setDefaults(humioCluster)

	// Assume we are bootstrapping if no cluster state is set.
	// TODO: this is a workaround for the issue where humio pods cannot start up at the same time during the first boot
	if humioCluster.Status.ClusterState == "" {
		r.setClusterStatus(context.TODO(), corev1alpha1.HumioClusterStateBoostrapping, humioCluster)
	}

	// Ensure developer password is a k8s secret
	err = r.ensureDeveloperUserPasswordExists(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	emptyResult := reconcile.Result{}

	// Ensure pods that does not run the desired version are deleted.
	result, err := r.ensureMismatchedPodVersionsAreDeleted(context.TODO(), humioCluster)
	if result != emptyResult || err != nil {
		return result, err
	}

	// Ensure pods exist. Will requeue if not all pods are created and ready
	if humioCluster.Status.ClusterState == corev1alpha1.HumioClusterStateBoostrapping {
		result, err = r.ensurePodsBootstrapped(context.TODO(), humioCluster)
		if result != emptyResult || err != nil {
			return result, err
		}
	}

	r.setClusterStatus(context.TODO(), corev1alpha1.HumioClusterStateRunning, humioCluster)

	result, err = r.ensurePodsExist(context.TODO(), humioCluster)
	if result != emptyResult || err != nil {
		return result, err
	}

	// Ensure service exists
	err = r.ensureServiceExists(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Ensure persistent token is a k8s secret, authenticate the humio client with the persistent token
	err = r.ensurePersistentTokenExists(context.TODO(), humioCluster, r.humioClient.GetBaseURL(humioCluster))
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.ensurePodLabels(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// TODO: wait until all pods are ready before continuing
	clusterController := humio.NewClusterController(r.humioClient)
	err = r.ensurePartitionsAreBalanced(*clusterController, humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// All done, requeue every 30 seconds even if no changes were made
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
}

// setClusterStatus is used to change the cluster status
// TODO: we use this to determine if we should have a delay between startup of humio pods during bootstrap vs starting up pods during an image update
func (r *ReconcileHumioCluster) setClusterStatus(context context.Context, clusterState string, humioCluster *corev1alpha1.HumioCluster) error {
	humioCluster.Status.ClusterState = clusterState
	return r.client.Status().Update(context, humioCluster)
}

func (r *ReconcileHumioCluster) ensurePodLabels(context context.Context, hc *corev1alpha1.HumioCluster) error {
	r.logger.Info("ensuring pod labels")
	cluster, err := r.humioClient.GetClusters()
	if err != nil {
		return fmt.Errorf("failed to get clusters: %s", err)
	}

	foundPodList, err := ListPods(r.client, hc)

	for _, pod := range foundPodList {
		// Skip pods that already have a label
		if labelListContainsLabel(pod.GetLabels(), "node_id") {
			continue
		}
		// If pod does not have an IP yet it is probably pending
		if pod.Status.PodIP == "" {
			r.logger.Info(fmt.Sprintf("not setting labels for pod %s because it is in state %s", pod.Name, pod.Status.Phase))
			continue
		}
		r.logger.Info(fmt.Sprintf("setting labels for nodes: %v", cluster.Nodes))
		for _, node := range cluster.Nodes {
			if node.Uri == fmt.Sprintf("http://%s:%d", pod.Status.PodIP, humioPort) {
				labels := labelsForPod(hc.Name, node.Id)
				r.logger.Info(fmt.Sprintf("setting labels for pod %s, labels=%v", pod.Name, labels))
				pod.SetLabels(labels)
				if err := r.client.Update(context, &pod); err != nil {
					return fmt.Errorf("failed to update labels on pod %s: %s", pod.Name, err)
				}
			}
		}
	}

	return nil
}

func (r *ReconcileHumioCluster) ensurePartitionsAreBalanced(humioClusterController humio.ClusterController, hc *corev1alpha1.HumioCluster) error {
	partitionsBalanced, err := humioClusterController.AreStoragePartitionsBalanced(hc)
	if err != nil {
		return fmt.Errorf("unable to check if storage partitions are balanced:%s", err)
	}
	if !partitionsBalanced {
		r.logger.Info("storage partitions are not balanced. Balancing now")
		err = humioClusterController.RebalanceStoragePartitions(hc)
		if err != nil {
			return fmt.Errorf("failed to balance storage partitions :%s", err)
		}
	}
	partitionsBalanced, err = humioClusterController.AreIngestPartitionsBalanced(hc)
	if err != nil {
		return fmt.Errorf("unable to check if ingest partitions are balanced:%s", err)
	}
	if !partitionsBalanced {
		r.logger.Info("ingest partitions are not balanced. Balancing now")
		err = humioClusterController.RebalanceIngestPartitions(hc)
		if err != nil {
			return fmt.Errorf("failed to balance ingest partitions :%s", err)
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureServiceExists(context context.Context, hc *corev1alpha1.HumioCluster) error {
	_, err := r.GetService(context, hc)
	if k8serrors.IsNotFound(err) {
		service, err := r.constructService(hc)
		if err != nil {
			return fmt.Errorf("unable to construct service for HumioCluster: %v", err)
		}
		err = r.client.Create(context, service)
		if err != nil {
			return fmt.Errorf("unable to create service for HumioCluster: %v", err)
		}
	}
	return nil
}

// ensureMismatchedPodVersionsAreDeleted is used to delete pods which container image does not match the desired image from the HumioCluster.
// If a pod is deleted, this will requeue immediately and rely on the next reconciliation to delete the next pod.
// The method only returns an empty result and no error if all pods are running the desired version,
// and no pod is currently being deleted.
func (r *ReconcileHumioCluster) ensureMismatchedPodVersionsAreDeleted(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	foundPodList, err := ListPods(r.client, humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// if we do not have any pods running we have nothing to clean up, or wait until they have been deleted
	if len(foundPodList) == 0 {
		return reconcile.Result{}, nil
	}

	podBeingDeleted := false
	for _, pod := range foundPodList {
		// TODO: can we assume we always only have one pod?
		// Probably not if running in a service mesh with sidecars injected.
		// Should have a container name variable and match this here.

		// only consider pods not already being deleted
		if pod.DeletionTimestamp == nil {

			// if container image versions of a pod differs, we want to delete it
			if pod.Spec.Containers[0].Image != humioCluster.Spec.Image {
				// TODO: figure out if we should only allow upgrades and not downgrades
				r.logger.Info(fmt.Sprintf("deleting pod %s", pod.Name))
				err = DeletePod(r.client, pod)
				if err != nil {
					return reconcile.Result{}, fmt.Errorf("could not delete pod %s, got err: %v", pod.Name, err)
				}
				return reconcile.Result{Requeue: true}, nil
			}
		} else {
			podBeingDeleted = true
		}

	}
	// if we have pods being deleted, requeue after a short delay
	if podBeingDeleted {
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 10}, nil
	}
	// return empty result and no error indicating that everything was in the state we wanted it to be
	return reconcile.Result{}, nil
}

// TODO: change to create 1 pod at a time, return Requeue=true and RequeueAfter.
// check that other pods, if they exist, are in a ready state
func (r *ReconcileHumioCluster) ensurePodsBootstrapped(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	foundPodList, err := ListPods(r.client, humioCluster)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to list pods: %s", err)
	}

	var podsReadyCount int
	var podsNotReadyCount int
	for _, pod := range foundPodList {
		podsNotReadyCount++
		for _, condition := range pod.Status.Conditions {
			if condition.Type == "Ready" {
				if condition.Status == "True" {
					podsReadyCount++
					podsNotReadyCount--
				}
			}
		}
	}
	if podsReadyCount == humioCluster.Spec.NodeCount {
		r.logger.Info("all humio pods are reporting ready")
		return reconcile.Result{}, nil
	}

	if podsNotReadyCount > 0 {
		r.logger.Info(fmt.Sprintf("there are %d humio pods that are not ready. all humio pods must report ready before reconciliation can continue", podsNotReadyCount))
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, nil
	}

	if podsReadyCount < humioCluster.Spec.NodeCount {
		pod, err := r.constructPod(humioCluster)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("unable to construct pod for HumioCluster: %v", err)
		}

		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			log.Info(fmt.Sprintf("unable to create pod: %v", err))
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, fmt.Errorf("unable to create Pod for HumioCluster: %v", err)
		}
		log.Info(fmt.Sprintf("successfully created pod %s for HumioCluster %s", pod.Name, humioCluster.Name))
		metricPodsCreated.Inc()
		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) ensurePodsExist(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	foundPodList, err := ListPods(r.client, humioCluster)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to list pods: %s", err)
	}

	if len(foundPodList) < humioCluster.Spec.NodeCount {
		pod, err := r.constructPod(humioCluster)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("unable to construct pod for HumioCluster: %v", err)
		}

		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			log.Info(fmt.Sprintf("unable to create pod: %v", err))
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, fmt.Errorf("unable to create Pod for HumioCluster: %v", err)
		}
		log.Info(fmt.Sprintf("successfully created pod %s for HumioCluster %s", pod.Name, humioCluster.Name))
		metricPodsCreated.Inc()
		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

// TODO: extend this (or create separate method) to take this password and perform a login, get the jwt token and then call the api to get the persistent api token and also store that as a secret
// this functionality should perhaps go into humio.cluster_auth.go
func (r *ReconcileHumioCluster) ensureDeveloperUserPasswordExists(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) error {
	secretData := map[string][]byte{"password": []byte(generatePassword())}
	_, err := r.GetSecret(conetext, humioCluster, serviceAccountSecretName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			secret, err := r.constructSecret(humioCluster, serviceAccountSecretName, secretData)
			if err != nil {
				return fmt.Errorf("unable to construct service for HumioCluster: %v", err)
			}
			err = r.client.Create(context.TODO(), secret)
			if err != nil {
				log.Info(fmt.Sprintf("unable to create secret: %v", err))
				return fmt.Errorf("unable to create service account secret for HumioCluster: %v", err)
			}
			log.Info(fmt.Sprintf("successfully created service account secret %s for HumioCluster %s", serviceAccountSecretName, humioCluster.Name))
			metricSecretsCreated.Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensurePersistentTokenExists(conetext context.Context, humioCluster *corev1alpha1.HumioCluster, url string) error {
	existingSecret, err := r.GetSecret(conetext, humioCluster, serviceTokenSecretName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			password, err := r.getDeveloperUserPassword(humioCluster)
			if err != nil {
				return fmt.Errorf("failed to get password: %v", err)
			}
			persistentToken, err := GetPersistentToken(humioCluster, url, password, r.humioClient)
			if err != nil {
				return fmt.Errorf("failed to get persistent token: %s", err)
			}

			secretData := map[string][]byte{"token": []byte(persistentToken)}
			secret, err := r.constructSecret(humioCluster, serviceTokenSecretName, secretData)
			if err != nil {
				return fmt.Errorf("unable to construct service for HumioCluster: %v", err)
			}

			err = r.client.Create(context.TODO(), secret)
			if err != nil {
				log.Info(fmt.Sprintf("unable to create secret: %v", err))
				return fmt.Errorf("unable to create persistent token secret for HumioCluster: %v", err)
			}
			log.Info(fmt.Sprintf("successfully created persistent token secret %s for HumioCluster %s", serviceTokenSecretName, humioCluster.Name))
			metricSecretsCreated.Inc()
		}
	} else {
		log.Info(fmt.Sprintf("persistent token secret %s already exists for HumioCluster %s", serviceTokenSecretName, humioCluster.Name))
	}

	// Either authenticate or re-authenticate with the persistent token
	r.humioClient.Authenticate(
		&humioapi.Config{
			Address: url,
			Token:   string(existingSecret.Data["token"]),
		},
	)

	return nil
}

func (r *ReconcileHumioCluster) getDeveloperUserPassword(hc *corev1alpha1.HumioCluster) (string, error) {
	secret, err := r.GetSecret(context.TODO(), hc, serviceAccountSecretName)
	if err != nil {
		return "", fmt.Errorf("could not get secret with password: %v", err)
	}
	if string(secret.Data["password"]) == "" {
		return "", fmt.Errorf("secret %s expected content to not be empty, but it was", serviceAccountSecretName)
	}
	return string(secret.Data["password"]), nil
}

func envVarList(humioCluster *corev1alpha1.HumioCluster) []corev1.EnvVar {
	setEnvironmentVariableDefaults(humioCluster)
	return humioCluster.Spec.EnvironmentVariables
}

func init() {
	metrics.Registry.MustRegister(metricPodsCreated)
	metrics.Registry.MustRegister(metricPodsDeleted)
}
