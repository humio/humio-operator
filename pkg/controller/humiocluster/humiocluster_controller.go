package humiocluster

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
	"github.com/prometheus/client_golang/prometheus"
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
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	prometheusMetrics = map[string]prometheus.Counter{
		"podsCreated": prometheus.NewCounter(prometheus.CounterOpts{
			Name: "humiocluster_controller_pods_created_total",
			Help: "Total number of pod objects created by controller",
		}),
		"podsDeleted": prometheus.NewCounter(prometheus.CounterOpts{
			Name: "humiocluster_controller_pods_deleted_total",
			Help: "Total number of pod objects deleted by controller",
		}),
		"secretsCreated": prometheus.NewCounter(prometheus.CounterOpts{
			Name: "humiocluster_controller_secrets_created_total",
			Help: "Total number of secret objects created by controller",
		}),
		"clusterRolesCreated": prometheus.NewCounter(prometheus.CounterOpts{
			Name: "humiocluster_controller_cluster_roles_created_total",
			Help: "Total number of cluster roles objects created by controller",
		}),
		"clusterRoleBindingsCreated": prometheus.NewCounter(prometheus.CounterOpts{
			Name: "humiocluster_controller_cluster_role_bindings_created_total",
			Help: "Total number of cluster role bindings objects created by controller",
		}),
		"serviceAccountsCreated": prometheus.NewCounter(prometheus.CounterOpts{
			Name: "humiocluster_controller_service_accounts_created_total",
			Help: "Total number of service accounts objects created by controller",
		}),
		"serviceAccountSecretsCreated": prometheus.NewCounter(prometheus.CounterOpts{
			Name: "humiocluster_controller_service_account_secrets_created_total",
			Help: "Total number of service account secrets objects created by controller",
		}),
	}
)

// Add creates a new HumioCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	return &ReconcileHumioCluster{
		client:      mgr.GetClient(),
		scheme:      mgr.GetScheme(),
		humioClient: humio.NewClient(logger.Sugar(), &humioapi.Config{}),
		logger:      logger.Sugar(),
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
	scheme      *runtime.Scheme
	humioClient humio.Client
	logger      *zap.SugaredLogger
}

// Reconcile reads that state of the cluster for a HumioCluster object and makes changes based on the state read
// and what is in the HumioCluster.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileHumioCluster) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	r.logger = logger.Sugar().With("Request.Namespace", request.Namespace, "Request.Name", request.Name, "Request.Type", helpers.GetTypeName(r))
	r.logger.Info("Reconciling HumioCluster")
	// TODO: Add back controllerutil.SetControllerReference everywhere we create k8s objects

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
		r.setClusterState(context.TODO(), corev1alpha1.HumioClusterStateBoostrapping, humioCluster)
	}

	err = r.ensureInitContainerPermissions(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Ensure developer password is a k8s secret
	err = r.ensureDeveloperUserPasswordExists(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Ensure extra kafka configs configmap if specified
	err = r.ensureKafkaConfigConfigmap(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	emptyResult := reconcile.Result{}

	// Ensure pods that does not run the desired version are deleted.
	result, err := r.ensureMismatchedPodsAreDeleted(context.TODO(), humioCluster)
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

	r.setClusterState(context.TODO(), corev1alpha1.HumioClusterStateRunning, humioCluster)

	defer func(context context.Context, humioCluster *corev1alpha1.HumioCluster) {
		pods, _ := kubernetes.ListPods(r.client, humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(humioCluster.Name))
		r.setClusterNodeCount(context, len(pods), humioCluster)
	}(context.TODO(), humioCluster)

	// TODO: get cluster version from humio api
	defer func(context context.Context, humioClient humio.Client, humioCluster *corev1alpha1.HumioCluster) {
		status, err := humioClient.Status()
		if err != nil {
			r.logger.Infof("unable to get status: %s", err)
		}
		r.setClusterVersion(context, status.Version, humioCluster)
	}(context.TODO(), r.humioClient, humioCluster)

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
	clusterController := humio.NewClusterController(r.logger, r.humioClient)
	err = r.ensurePartitionsAreBalanced(*clusterController, humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// All done, requeue every 30 seconds even if no changes were made
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
}

// setClusterState is used to change the cluster state
// TODO: we use this to determine if we should have a delay between startup of humio pods during bootstrap vs starting up pods during an image update
func (r *ReconcileHumioCluster) setClusterState(context context.Context, clusterState string, humioCluster *corev1alpha1.HumioCluster) error {
	humioCluster.Status.ClusterState = clusterState
	return r.client.Status().Update(context, humioCluster)
}

func (r *ReconcileHumioCluster) setClusterVersion(context context.Context, clusterVersion string, humioCluster *corev1alpha1.HumioCluster) error {
	humioCluster.Status.ClusterVersion = clusterVersion
	return r.client.Status().Update(context, humioCluster)
}

func (r *ReconcileHumioCluster) setClusterNodeCount(context context.Context, clusterNodeCount int, humioCluster *corev1alpha1.HumioCluster) error {
	humioCluster.Status.ClusterNodeCount = clusterNodeCount
	return r.client.Status().Update(context, humioCluster)
}

func (r *ReconcileHumioCluster) ensureKafkaConfigConfigmap(context context.Context, humioCluster *corev1alpha1.HumioCluster) error {
	extraKafkaConfigsConfigmapData := extraKafkaConfigsOrDefault(humioCluster)
	if extraKafkaConfigsConfigmapData == "" {
		return nil
	}
	_, err := kubernetes.GetConfigmap(r.client, context, extraKafkaConfigsConfigmapName, humioCluster.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			configmap := kubernetes.ConstructExtraKafkaConfigsConfigmap(
				extraKafkaConfigsConfigmapName,
				extraKafkaPropertiesFilename,
				extraKafkaConfigsConfigmapData,
				humioCluster.Name,
				humioCluster.Namespace,
			)
			if err := controllerutil.SetControllerReference(humioCluster, configmap, r.scheme); err != nil {
				return fmt.Errorf("could not set controller reference: %s", err)
			}
			err = r.client.Create(context, configmap)
			if err != nil {
				return fmt.Errorf("unable to create extra kafka configs configmap for HumioCluster: %s", err)
			}
			r.logger.Infof("successfully created extra kafka configs configmap %s for HumioCluster %s", configmap, humioCluster.Name)
			prometheusMetrics["clusterRolesCreated"].Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureInitContainerPermissions(context context.Context, humioCluster *corev1alpha1.HumioCluster) error {
	// We do not want to attach the init service account to the humio pod. Instead, only the init container should use this
	// service account. To do this, we can attach the service account directly to the init container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	err := r.ensureInitServiceAccountSecretExists(context, humioCluster)
	if err != nil {
		return err
	}

	// Do not manage these resources if the InitServiceAccountName is supplied. This implies the service account, cluster role and cluster
	// role binding are managed outside of the operator
	if humioCluster.Spec.InitServiceAccountName != "" {
		return nil
	}

	// The service account is used by the init container attached to the humio pods to get the availability zone
	// from the node on which the pod is scheduled. We cannot pre determine the zone from the controller because we cannot
	// assume that the nodes are running. Additionally, if we pre allocate the zones to the humio pods, we would be required
	// to have an autoscaling group per zone.
	err = r.ensureInitServiceAccountExists(context, humioCluster)
	if err != nil {
		return err
	}

	// This should be namespaced by the name, e.g. clustername-namespace-name
	// Required until https://github.com/kubernetes/kubernetes/issues/40610 is fixed
	err = r.ensureInitClusterRole(context, humioCluster)
	if err != nil {
		return err
	}

	// This should be namespaced by the name, e.g. clustername-namespace-name
	// Required until https://github.com/kubernetes/kubernetes/issues/40610 is fixed
	err = r.ensureInitClusterRoleBinding(context, humioCluster)
	if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureInitClusterRole(context context.Context, hc *corev1alpha1.HumioCluster) error {
	clusterRoleName := initClusterRoleName(hc)
	_, err := kubernetes.GetClusterRole(r.client, context, clusterRoleName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			clusterRole := kubernetes.ConstructInitClusterRole(clusterRoleName, hc.Name)
			// TODO: We cannot use controllerutil.SetControllerReference() as ClusterRole is cluster-wide and owner is namespaced.
			// We probably need another way to ensure we clean them up. Perhaps we can use finalizers?
			err = r.client.Create(context, clusterRole)
			if err != nil {
				return fmt.Errorf("unable to create init cluster role for HumioCluster: %s", err)
			}
			r.logger.Infof("successfully created init cluster role %s for HumioCluster %s", clusterRoleName, hc.Name)
			prometheusMetrics["clusterRolesCreated"].Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureInitClusterRoleBinding(context context.Context, hc *corev1alpha1.HumioCluster) error {
	clusterRoleBindingName := initClusterRoleBindingName(hc)
	_, err := kubernetes.GetClusterRoleBinding(r.client, context, clusterRoleBindingName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			clusterRole := kubernetes.ConstructInitClusterRoleBinding(
				clusterRoleBindingName,
				initClusterRoleName(hc),
				hc.Name,
				hc.Namespace,
				initServiceAccountNameOrDefault(hc),
			)
			// TODO: We cannot use controllerutil.SetControllerReference() as ClusterRoleBinding is cluster-wide and owner is namespaced.
			// We probably need another way to ensure we clean them up. Perhaps we can use finalizers?
			err = r.client.Create(context, clusterRole)
			if err != nil {
				return fmt.Errorf("unable to create init cluster role binding for HumioCluster: %s", err)
			}
			r.logger.Infof("successfully created init cluster role binding %s for HumioCluster %s", clusterRoleBindingName, hc.Name)
			prometheusMetrics["clusterRoleBindingsCreated"].Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureInitServiceAccountExists(context context.Context, hc *corev1alpha1.HumioCluster) error {
	serviceAccountName := initServiceAccountNameOrDefault(hc)
	_, err := kubernetes.GetServiceAccount(r.client, context, serviceAccountName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			serviceAccount := kubernetes.ConstructInitServiceAccount(serviceAccountName, hc.Name, hc.Namespace)
			if err := controllerutil.SetControllerReference(hc, serviceAccount, r.scheme); err != nil {
				return fmt.Errorf("could not set controller reference: %s", err)
			}
			err = r.client.Create(context, serviceAccount)
			if err != nil {
				return fmt.Errorf("unable to create init service account for HumioCluster: %s", err)
			}
			r.logger.Infof("successfully created init service account %s for HumioCluster %s", serviceAccountName, hc.Name)
			prometheusMetrics["serviceAccountsCreated"].Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureInitServiceAccountSecretExists(context context.Context, hc *corev1alpha1.HumioCluster) error {
	_, err := kubernetes.GetSecret(r.client, context, initServiceAccountSecretName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			secret := kubernetes.ConstructServiceAccountSecret(hc.Name, hc.Namespace, initServiceAccountSecretName, initServiceAccountName)
			if err := controllerutil.SetControllerReference(hc, secret, r.scheme); err != nil {
				return fmt.Errorf("could not set controller reference: %s", err)
			}
			err = r.client.Create(context, secret)
			if err != nil {
				return fmt.Errorf("unable to create init service account secret for HumioCluster: %s", err)
			}
			r.logger.Infof("successfully created init service account secret %s for HumioCluster %s", initServiceAccountSecretName, hc.Name)
			prometheusMetrics["serviceAccountSecretsCreated"].Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensurePodLabels(context context.Context, hc *corev1alpha1.HumioCluster) error {
	r.logger.Info("ensuring pod labels")
	cluster, err := r.humioClient.GetClusters()
	if err != nil {
		return fmt.Errorf("failed to get clusters: %s", err)
	}

	foundPodList, err := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))

	for _, pod := range foundPodList {
		// Skip pods that already have a label
		if kubernetes.LabelListContainsLabel(pod.GetLabels(), "node_id") {
			continue
		}
		// If pod does not have an IP yet it is probably pending
		if pod.Status.PodIP == "" {
			r.logger.Infof("not setting labels for pod %s because it is in state %s", pod.Name, pod.Status.Phase)
			continue
		}
		r.logger.Infof("setting labels for nodes: %v", cluster.Nodes)
		for _, node := range cluster.Nodes {
			if node.Uri == fmt.Sprintf("http://%s:%d", pod.Status.PodIP, humioPort) {
				labels := kubernetes.LabelsForPod(hc.Name, node.Id)
				r.logger.Infof("setting labels for pod %s, labels=%v", pod.Name, labels)
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
		return fmt.Errorf("unable to check if storage partitions are balanced: %s", err)
	}
	if !partitionsBalanced {
		r.logger.Info("storage partitions are not balanced. Balancing now")
		err = humioClusterController.RebalanceStoragePartitions(hc)
		if err != nil {
			return fmt.Errorf("failed to balance storage partitions: %s", err)
		}
	}
	partitionsBalanced, err = humioClusterController.AreIngestPartitionsBalanced(hc)
	if err != nil {
		return fmt.Errorf("unable to check if ingest partitions are balanced: %s", err)
	}
	if !partitionsBalanced {
		r.logger.Info("ingest partitions are not balanced. Balancing now")
		err = humioClusterController.RebalanceIngestPartitions(hc)
		if err != nil {
			return fmt.Errorf("failed to balance ingest partitions: %s", err)
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureServiceExists(context context.Context, hc *corev1alpha1.HumioCluster) error {
	_, err := kubernetes.GetService(r.client, context, hc.Name, hc.Namespace)
	if k8serrors.IsNotFound(err) {
		service := kubernetes.ConstructService(hc.Name, hc.Namespace)
		if err := controllerutil.SetControllerReference(hc, service, r.scheme); err != nil {
			return fmt.Errorf("could not set controller reference: %s", err)
		}
		err = r.client.Create(context, service)
		if err != nil {
			return fmt.Errorf("unable to create service for HumioCluster: %s", err)
		}
	}
	return nil
}

// ensureMismatchedPodsAreDeleted is used to delete pods which container spec does not match that which is desired.
// If a pod is deleted, this will requeue immediately and rely on the next reconciliation to delete the next pod.
// The method only returns an empty result and no error if all pods are running the desired version,
// and no pod is currently being deleted.
func (r *ReconcileHumioCluster) ensureMismatchedPodsAreDeleted(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	foundPodList, err := kubernetes.ListPods(r.client, humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(humioCluster.Name))
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

			// if pod spec differs, we want to delete it
			desiredPod, err := constructPod(humioCluster)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("could not construct pod: %s", err)
			}

			if !r.podsMatch(pod, *desiredPod) {
				// TODO: figure out if we should only allow upgrades and not downgrades
				r.logger.Infof("deleting pod %s", pod.Name)
				err = kubernetes.DeletePod(r.client, pod)
				if err != nil {
					return reconcile.Result{}, fmt.Errorf("could not delete pod %s, got err: %s", pod.Name, err)
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

func (r *ReconcileHumioCluster) podsMatch(pod corev1.Pod, desiredPod corev1.Pod) bool {
	if pod.Spec.Containers[0].Image != desiredPod.Spec.Containers[0].Image {
		r.logger.Infof("pod image does not match: got %s, wanted %s", pod.Spec.Containers[0].Image, desiredPod.Spec.Containers[0].Image)
		return false
	}
	if !reflect.DeepEqual(pod.Spec.Containers[0].Env, desiredPod.Spec.Containers[0].Env) {
		r.logger.Infof("pod env vars do not match: got %+v, wanted %+v", pod.Spec.Containers[0].Env, desiredPod.Spec.Containers[0].Env)
		return false
	}
	if !reflect.DeepEqual(pod.Spec.Affinity, desiredPod.Spec.Affinity) {
		r.logger.Infof("pod affinity do not match: got %+v, wanted %+v", pod.Spec.Affinity, desiredPod.Spec.Affinity)
		return false
	}
	if !reflect.DeepEqual(pod.Spec.ImagePullSecrets, desiredPod.Spec.ImagePullSecrets) {
		r.logger.Infof("pod image pull secrets do not match: got %+v, wanted %+v", pod.Spec.ImagePullSecrets, desiredPod.Spec.ImagePullSecrets)
		return false
	}
	if pod.Spec.ServiceAccountName != desiredPod.Spec.ServiceAccountName {
		r.logger.Infof("pod service account name does not match: got %s, wanted %s", pod.Spec.ServiceAccountName, desiredPod.Spec.ServiceAccountName)
		return false
	}
	var knownVolumes []corev1.Volume
	for _, volume := range pod.Spec.Volumes {
		for _, knownVolume := range desiredPod.Spec.Volumes {
			if volume.Name == knownVolume.Name {
				knownVolumes = append(knownVolumes, volume)
			}
		}
	}
	if !reflect.DeepEqual(knownVolumes, desiredPod.Spec.Volumes) {
		r.logger.Infof("pod volumes do not match: got %+v, wanted %+v", pod.Spec.Volumes, desiredPod.Spec.Volumes)
		return false
	}
	return true
}

// TODO: change to create 1 pod at a time, return Requeue=true and RequeueAfter.
// check that other pods, if they exist, are in a ready state
func (r *ReconcileHumioCluster) ensurePodsBootstrapped(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	foundPodList, err := kubernetes.ListPods(r.client, humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(humioCluster.Name))
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
		r.logger.Infof("there are %d humio pods that are not ready. all humio pods must report ready before reconciliation can continue", podsNotReadyCount)
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, nil
	}

	if podsReadyCount < humioCluster.Spec.NodeCount {
		pod, err := constructPod(humioCluster)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("unable to construct pod for HumioCluster: %s", err)
		}
		if err := controllerutil.SetControllerReference(humioCluster, pod, r.scheme); err != nil {
			return reconcile.Result{}, fmt.Errorf("could not set controller reference: %s", err)
		}
		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, fmt.Errorf("unable to create Pod for HumioCluster: %s", err)
		}
		r.logger.Infof("successfully created pod %s for HumioCluster %s", pod.Name, humioCluster.Name)
		prometheusMetrics["podsCreated"].Inc()
		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) ensurePodsExist(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	foundPodList, err := kubernetes.ListPods(r.client, humioCluster.Namespace, kubernetes.MatchingLabelsForHumio(humioCluster.Name))
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to list pods: %s", err)
	}

	if len(foundPodList) < humioCluster.Spec.NodeCount {
		pod, err := constructPod(humioCluster)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("unable to construct pod for HumioCluster: %s", err)
		}
		if err := controllerutil.SetControllerReference(humioCluster, pod, r.scheme); err != nil {
			return reconcile.Result{}, fmt.Errorf("could not set controller reference: %s", err)
		}
		err = r.client.Create(context.TODO(), pod)
		if err != nil {
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, fmt.Errorf("unable to create Pod for HumioCluster: %s", err)
		}
		r.logger.Infof("successfully created pod %s for HumioCluster %s", pod.Name, humioCluster.Name)
		prometheusMetrics["podsCreated"].Inc()
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
	_, err := kubernetes.GetSecret(r.client, conetext, kubernetes.ServiceAccountSecretName, humioCluster.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			secret := kubernetes.ConstructSecret(humioCluster.Name, humioCluster.Namespace, kubernetes.ServiceAccountSecretName, secretData)
			if err := controllerutil.SetControllerReference(humioCluster, secret, r.scheme); err != nil {
				return fmt.Errorf("could not set controller reference: %s", err)
			}
			err = r.client.Create(context.TODO(), secret)
			if err != nil {
				return fmt.Errorf("unable to create service account secret for HumioCluster: %s", err)
			}
			r.logger.Infof("successfully created service account secret %s for HumioCluster %s", kubernetes.ServiceAccountSecretName, humioCluster.Name)
			prometheusMetrics["secretsCreated"].Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensurePersistentTokenExists(conetext context.Context, humioCluster *corev1alpha1.HumioCluster, url string) error {
	existingSecret, err := kubernetes.GetSecret(r.client, conetext, kubernetes.ServiceTokenSecretName, humioCluster.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			password, err := r.getDeveloperUserPassword(humioCluster)
			if err != nil {
				return fmt.Errorf("failed to get password: %s", err)
			}
			persistentToken, err := GetPersistentToken(humioCluster, url, password, r.humioClient)
			if err != nil {
				return fmt.Errorf("failed to get persistent token: %s", err)
			}

			secretData := map[string][]byte{"token": []byte(persistentToken)}
			secret := kubernetes.ConstructSecret(humioCluster.Name, humioCluster.Namespace, kubernetes.ServiceTokenSecretName, secretData)
			if err := controllerutil.SetControllerReference(humioCluster, secret, r.scheme); err != nil {
				return fmt.Errorf("could not set controller reference: %s", err)
			}
			err = r.client.Create(context.TODO(), secret)
			if err != nil {
				return fmt.Errorf("unable to create persistent token secret for HumioCluster: %s", err)
			}
			r.logger.Infof("successfully created persistent token secret %s for HumioCluster %s", kubernetes.ServiceTokenSecretName, humioCluster.Name)
			prometheusMetrics["secretsCreated"].Inc()
		}
	} else {
		r.logger.Infof("persistent token secret %s already exists for HumioCluster %s", kubernetes.ServiceTokenSecretName, humioCluster.Name)
	}

	// Either authenticate or re-authenticate with the persistent token
	return r.humioClient.Authenticate(
		&humioapi.Config{
			Address: url,
			Token:   string(existingSecret.Data["token"]),
		},
	)
}

func (r *ReconcileHumioCluster) getDeveloperUserPassword(hc *corev1alpha1.HumioCluster) (string, error) {
	secret, err := kubernetes.GetSecret(r.client, context.TODO(), kubernetes.ServiceAccountSecretName, hc.Namespace)
	if err != nil {
		return "", fmt.Errorf("could not get secret with password: %s", err)
	}
	if string(secret.Data["password"]) == "" {
		return "", fmt.Errorf("secret %s expected content to not be empty, but it was", kubernetes.ServiceAccountSecretName)
	}
	return string(secret.Data["password"]), nil
}

// TODO: there is no need for this. We should instead change this to a get method where we return the list of env vars
// including the defaults
func envVarList(humioCluster *corev1alpha1.HumioCluster) []corev1.EnvVar {
	setEnvironmentVariableDefaults(humioCluster)
	return humioCluster.Spec.EnvironmentVariables
}

func init() {
	for _, m := range prometheusMetrics {
		metrics.Registry.MustRegister(m)

	}
}
