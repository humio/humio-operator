package humiocluster

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"time"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/networking/v1beta1"
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
	hc := &corev1alpha1.HumioCluster{}
	err := r.client.Get(context.TODO(), request.NamespacedName, hc)
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
	setDefaults(hc)

	// Assume we are bootstrapping if no cluster state is set.
	// TODO: this is a workaround for the issue where humio pods cannot start up at the same time during the first boot
	if hc.Status.State == "" {
		r.setState(context.TODO(), corev1alpha1.HumioClusterStateBoostrapping, hc)
	}

	// Ensure service exists
	err = r.ensureServiceExists(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.ensureHumioPodPermissions(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.ensureInitContainerPermissions(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.ensureAuthContainerPermissions(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Ensure extra kafka configs configmap if specified
	err = r.ensureKafkaConfigConfigmap(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	emptyResult := reconcile.Result{}

	// Ensure pods that does not run the desired version are deleted.
	result, err := r.ensureMismatchedPodsAreDeleted(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	// Ensure pods exist. Will requeue if not all pods are created and ready
	if hc.Status.State == corev1alpha1.HumioClusterStateBoostrapping {
		result, err = r.ensurePodsBootstrapped(context.TODO(), hc)
		if result != emptyResult || err != nil {
			return result, err
		}
	}

	// Wait for the sidecar to create the secret which contains the token used to authenticate with humio and then authenticate with it
	result, err = r.authWithSidecarToken(context.TODO(), hc, r.humioClient.GetBaseURL(hc))
	if result != emptyResult || err != nil {
		return result, err
	}

	err = r.setState(context.TODO(), corev1alpha1.HumioClusterStateRunning, hc)
	if err != nil {
		r.logger.Infof("unable to set cluster state: %s", err)
		return reconcile.Result{}, err
	}

	defer func(ctx context.Context, hc *corev1alpha1.HumioCluster) {
		pods, _ := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
		r.setNodeCount(ctx, len(pods), hc)
	}(context.TODO(), hc)

	defer func(ctx context.Context, humioClient humio.Client, hc *corev1alpha1.HumioCluster) {
		status, err := humioClient.Status()
		if err != nil {
			r.logger.Infof("unable to get status: %s", err)
		}
		r.setVersion(ctx, status.Version, hc)
	}(context.TODO(), r.humioClient, hc)

	result, err = r.ensurePodsExist(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	err = r.ensurePodLabels(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Ensure ingress objects are deleted if ingress is disabled.
	result, err = r.ensureNoIngressesIfIngressNotEnabled(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}
	err = r.ensureIngress(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	// TODO: wait until all pods are ready before continuing
	clusterController := humio.NewClusterController(r.logger, r.humioClient)
	err = r.ensurePartitionsAreBalanced(*clusterController, hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	// All done, requeue every 30 seconds even if no changes were made
	r.logger.Info("done reconciling, will requeue after 30 seconds")
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
}

// setState is used to change the cluster state
// TODO: we use this to determine if we should have a delay between startup of humio pods during bootstrap vs starting up pods during an image update
func (r *ReconcileHumioCluster) setState(ctx context.Context, state string, hc *corev1alpha1.HumioCluster) error {
	hc.Status.State = state
	return r.client.Status().Update(ctx, hc)
}

func (r *ReconcileHumioCluster) setVersion(ctx context.Context, version string, hc *corev1alpha1.HumioCluster) error {
	hc.Status.Version = version
	return r.client.Status().Update(ctx, hc)
}

func (r *ReconcileHumioCluster) setNodeCount(ctx context.Context, nodeCount int, hc *corev1alpha1.HumioCluster) error {
	hc.Status.NodeCount = nodeCount
	return r.client.Status().Update(ctx, hc)
}

// ensureKafkaConfigConfigmap creates a configmap containing configs specified in extraKafkaConfigs which will be mounted
// into the Humio container and pointed to by Humio's configuration option EXTRA_KAFKA_CONFIGS_FILE
func (r *ReconcileHumioCluster) ensureKafkaConfigConfigmap(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	extraKafkaConfigsConfigmapData := extraKafkaConfigsOrDefault(hc)
	if extraKafkaConfigsConfigmapData == "" {
		return nil
	}
	_, err := kubernetes.GetConfigmap(ctx, r.client, extraKafkaConfigsConfigmapName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			configmap := kubernetes.ConstructExtraKafkaConfigsConfigmap(
				extraKafkaConfigsConfigmapName,
				extraKafkaPropertiesFilename,
				extraKafkaConfigsConfigmapData,
				hc.Name,
				hc.Namespace,
			)
			if err := controllerutil.SetControllerReference(hc, configmap, r.scheme); err != nil {
				r.logger.Errorf("could not set controller reference: %s", err)
				return err
			}
			err = r.client.Create(ctx, configmap)
			if err != nil {
				r.logger.Errorf("unable to create extra kafka configs configmap for HumioCluster: %s", err)
				return err
			}
			r.logger.Infof("successfully created extra kafka configs configmap %s for HumioCluster %s", configmap, hc.Name)
			prometheusMetrics.Counters.ClusterRolesCreated.Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureNoIngressesIfIngressNotEnabled(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	if hc.Spec.Ingress.Enabled {
		return reconcile.Result{}, nil
	}

	foundIngressList, err := kubernetes.ListIngresses(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return reconcile.Result{}, err
	}
	// if we do not have any ingress objects we have nothing to clean up
	if len(foundIngressList) == 0 {
		return reconcile.Result{}, nil
	}

	for _, ingress := range foundIngressList {
		// only consider ingresses not already being deleted
		if ingress.DeletionTimestamp == nil {
			r.logger.Infof("deleting ingress %s", ingress.Name)
			err = r.client.Delete(ctx, &ingress)
			if err != nil {
				r.logger.Errorf("could not delete ingress %s, got err: %s", ingress.Name, err)
				return reconcile.Result{}, err
			}
		}
	}
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) ensureIngress(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	if !hc.Spec.Ingress.Enabled {
		return nil
	}
	if len(hc.Spec.Ingress.Controller) == 0 {
		return fmt.Errorf("ingress enabled but no controller specified")
	}

	switch hc.Spec.Ingress.Controller {
	case "nginx":
		err := r.ensureNginxIngress(ctx, hc)
		if err != nil {
			r.logger.Errorf("could not ensure nginx ingress")
			return err
		}
	default:
		return fmt.Errorf("ingress controller '%s' not supported", hc.Spec.Ingress.Controller)
	}

	return nil
}

// ensureNginxIngress creates the necessary ingress objects to expose the Humio cluster
// through NGINX ingress controller (https://kubernetes.github.io/ingress-nginx/).
func (r *ReconcileHumioCluster) ensureNginxIngress(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	// Due to ingress-ngress relying on ingress object annotations to enable/disable/adjust certain features we create multiple ingress objects.
	ingresses := []*v1beta1.Ingress{
		constructGeneralIngress(hc),
		constructStreamingQueryIngress(hc),
		constructIngestIngress(hc),
		constructESIngestIngress(hc),
	}
	for _, ingress := range ingresses {
		existingIngress, err := kubernetes.GetIngress(ctx, r.client, ingress.Name, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				if err := controllerutil.SetControllerReference(hc, ingress, r.scheme); err != nil {
					r.logger.Errorf("could not set controller reference: %s", err)
					return err
				}
				err = r.client.Create(ctx, ingress)
				if err != nil {
					r.logger.Errorf("unable to create ingress %s for HumioCluster: %s", ingress.Name, err)
					return err
				}
				r.logger.Infof("successfully created ingress %s for HumioCluster %s", ingress.Name, hc.Name)
				prometheusMetrics.Counters.IngressesCreated.Inc()
				continue
			}
		}
		if !r.ingressesMatch(existingIngress, ingress) {
			r.logger.Info("ingress object already exists, there is a difference between expected vs existing, updating ingress object %s", ingress.Name)
			err = r.client.Update(ctx, ingress)
			if err != nil {
				r.logger.Errorf("could not perform update of ingress %s: %v", ingress.Name, err)
				return err
			}
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureHumioPodPermissions(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	// Do not manage these resources if the HumioServiceAccountName is supplied. This implies the service account is managed
	// outside of the operator
	if hc.Spec.HumioServiceAccountName != "" {
		return nil
	}

	err := r.ensureServiceAccountExists(ctx, hc, humioServiceAccountNameOrDefault(hc), humioServiceAccountAnnotationsOrDefault(hc))
	if err != nil {
		r.logger.Errorf("unable to ensure humio service account exists for HumioCluster: %s", err)
		return err
	}

	return nil
}

func (r *ReconcileHumioCluster) ensureInitContainerPermissions(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	// We do not want to attach the init service account to the humio pod. Instead, only the init container should use this
	// service account. To do this, we can attach the service account directly to the init container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	err := r.ensureServiceAccountSecretExists(ctx, hc, initServiceAccountSecretName, initServiceAccountName)
	if err != nil {
		r.logger.Errorf("unable to ensure init service account secret exists for HumioCluster: %s", err)
		return err
	}

	// Do not manage these resources if the InitServiceAccountName is supplied. This implies the service account, cluster role and cluster
	// role binding are managed outside of the operator
	if hc.Spec.InitServiceAccountName != "" {
		return nil
	}

	// The service account is used by the init container attached to the humio pods to get the availability zone
	// from the node on which the pod is scheduled. We cannot pre determine the zone from the controller because we cannot
	// assume that the nodes are running. Additionally, if we pre allocate the zones to the humio pods, we would be required
	// to have an autoscaling group per zone.
	err = r.ensureServiceAccountExists(ctx, hc, initServiceAccountNameOrDefault(hc), map[string]string{})
	if err != nil {
		r.logger.Errorf("unable to ensure init service account exists for HumioCluster: %s", err)
		return err
	}

	// This should be namespaced by the name, e.g. clustername-namespace-name
	// Required until https://github.com/kubernetes/kubernetes/issues/40610 is fixed
	err = r.ensureInitClusterRole(ctx, hc)
	if err != nil {
		r.logger.Errorf("unable to ensure init cluster role exists for HumioCluster: %s", err)
		return err
	}

	// This should be namespaced by the name, e.g. clustername-namespace-name
	// Required until https://github.com/kubernetes/kubernetes/issues/40610 is fixed
	err = r.ensureInitClusterRoleBinding(ctx, hc)
	if err != nil {
		r.logger.Errorf("unable to ensure init cluster role binding exists for HumioCluster: %s", err)
		return err
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureAuthContainerPermissions(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	// We do not want to attach the auth service account to the humio pod. Instead, only the auth container should use this
	// service account. To do this, we can attach the service account directly to the auth container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	err := r.ensureServiceAccountSecretExists(ctx, hc, authServiceAccountSecretName, authServiceAccountName)
	if err != nil {
		r.logger.Errorf("unable to ensure auth service account secret exists for HumioCluster: %s", err)
		return err
	}

	// Do not manage these resources if the authServiceAccountName is supplied. This implies the service account, cluster role and cluster
	// role binding are managed outside of the operator
	if hc.Spec.AuthServiceAccountName != "" {
		return nil
	}

	// The service account is used by the auth container attached to the humio pods.
	err = r.ensureServiceAccountExists(ctx, hc, authServiceAccountNameOrDefault(hc), map[string]string{})
	if err != nil {
		r.logger.Errorf("unable to ensure auth service account exists for HumioCluster: %s", err)
		return err
	}

	err = r.ensureAuthRole(ctx, hc)
	if err != nil {
		r.logger.Errorf("unable to ensure auth role exists for HumioCluster: %s", err)
		return err
	}

	err = r.ensureAuthRoleBinding(ctx, hc)
	if err != nil {
		r.logger.Errorf("unable to ensure auth role binding exists for HumioCluster: %s", err)
		return err
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureInitClusterRole(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	clusterRoleName := initClusterRoleName(hc)
	_, err := kubernetes.GetClusterRole(ctx, r.client, clusterRoleName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			clusterRole := kubernetes.ConstructInitClusterRole(clusterRoleName, hc.Name)
			// TODO: We cannot use controllerutil.SetControllerReference() as ClusterRole is cluster-wide and owner is namespaced.
			// We probably need another way to ensure we clean them up. Perhaps we can use finalizers?
			err = r.client.Create(ctx, clusterRole)
			if err != nil {
				r.logger.Errorf("unable to create init cluster role for HumioCluster: %s", err)
				return err
			}
			r.logger.Infof("successfully created init cluster role %s for HumioCluster %s", clusterRoleName, hc.Name)
			prometheusMetrics.Counters.ClusterRolesCreated.Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureAuthRole(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	roleName := authRoleName(hc)
	_, err := kubernetes.GetRole(ctx, r.client, roleName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			role := kubernetes.ConstructAuthRole(roleName, hc.Name, hc.Namespace)
			err = r.client.Create(ctx, role)
			if err != nil {
				r.logger.Errorf("unable to create auth role for HumioCluster: %s", err)
				return err
			}
			r.logger.Infof("successfully created auth role %s for HumioCluster %s", roleName, hc.Name)
			prometheusMetrics.Counters.RolesCreated.Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureInitClusterRoleBinding(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	clusterRoleBindingName := initClusterRoleBindingName(hc)
	_, err := kubernetes.GetClusterRoleBinding(ctx, r.client, clusterRoleBindingName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			clusterRole := kubernetes.ConstructClusterRoleBinding(
				clusterRoleBindingName,
				initClusterRoleName(hc),
				hc.Name,
				hc.Namespace,
				initServiceAccountNameOrDefault(hc),
			)
			// TODO: We cannot use controllerutil.SetControllerReference() as ClusterRoleBinding is cluster-wide and owner is namespaced.
			// We probably need another way to ensure we clean them up. Perhaps we can use finalizers?
			err = r.client.Create(ctx, clusterRole)
			if err != nil {
				r.logger.Errorf("unable to create init cluster role binding for HumioCluster: %s", err)
				return err
			}
			r.logger.Infof("successfully created init cluster role binding %s for HumioCluster %s", clusterRoleBindingName, hc.Name)
			prometheusMetrics.Counters.ClusterRoleBindingsCreated.Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureAuthRoleBinding(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	roleBindingName := authRoleBindingName(hc)
	_, err := kubernetes.GetRoleBinding(ctx, r.client, roleBindingName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			role := kubernetes.ConstructRoleBinding(
				roleBindingName,
				authRoleName(hc),
				hc.Name,
				hc.Namespace,
				authServiceAccountNameOrDefault(hc),
			)
			err = r.client.Create(ctx, role)
			if err != nil {
				r.logger.Errorf("unable to create auth role binding for HumioCluster: %s", err)
				return err
			}
			r.logger.Infof("successfully created auth role binding %s for HumioCluster %s", roleBindingName, hc.Name)
			prometheusMetrics.Counters.RoleBindingsCreated.Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureServiceAccountExists(ctx context.Context, hc *corev1alpha1.HumioCluster, serviceAccountName string, serviceAccountAnnotations map[string]string) error {
	_, err := kubernetes.GetServiceAccount(ctx, r.client, serviceAccountName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			serviceAccount := kubernetes.ConstructServiceAccount(serviceAccountName, hc.Name, hc.Namespace, serviceAccountAnnotations)
			if err := controllerutil.SetControllerReference(hc, serviceAccount, r.scheme); err != nil {
				r.logger.Errorf("could not set controller reference: %s", err)
				return err
			}
			err = r.client.Create(ctx, serviceAccount)
			if err != nil {
				r.logger.Errorf("unable to create service account %s for HumioCluster: %s", serviceAccountName, err)
				return err
			}
			r.logger.Infof("successfully created service account %s for HumioCluster %s", serviceAccountName, hc.Name)
			prometheusMetrics.Counters.ServiceAccountsCreated.Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureServiceAccountSecretExists(ctx context.Context, hc *corev1alpha1.HumioCluster, serviceAccountSecretName string, serviceAccountName string) error {
	_, err := kubernetes.GetSecret(ctx, r.client, serviceAccountSecretName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			secret := kubernetes.ConstructServiceAccountSecret(hc.Name, hc.Namespace, serviceAccountSecretName, serviceAccountName)
			if err := controllerutil.SetControllerReference(hc, secret, r.scheme); err != nil {
				r.logger.Errorf("could not set controller reference: %s", err)
				return err
			}
			err = r.client.Create(ctx, secret)
			if err != nil {
				r.logger.Errorf("unable to create service account secret %s for HumioCluster: %s", serviceAccountSecretName, err)
				return err
			}
			r.logger.Infof("successfully created service account secret %s for HumioCluster %s", serviceAccountSecretName, hc.Name)
			prometheusMetrics.Counters.ServiceAccountSecretsCreated.Inc()
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensurePodLabels(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	r.logger.Info("ensuring pod labels")
	cluster, err := r.humioClient.GetClusters()
	if err != nil {
		r.logger.Errorf("failed to get clusters: %s", err)
		return err
	}

	foundPodList, err := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.logger.Errorf("failed to list pods: %s", err)
		return err
	}

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
				if err := r.client.Update(ctx, &pod); err != nil {
					r.logger.Errorf("failed to update labels on pod %s: %s", pod.Name, err)
					return err
				}
			}
		}
	}

	return nil
}

func (r *ReconcileHumioCluster) ensurePartitionsAreBalanced(humioClusterController humio.ClusterController, hc *corev1alpha1.HumioCluster) error {
	partitionsBalanced, err := humioClusterController.AreStoragePartitionsBalanced(hc)
	if err != nil {
		r.logger.Errorf("unable to check if storage partitions are balanced: %s", err)
		return err
	}
	if !partitionsBalanced {
		r.logger.Info("storage partitions are not balanced. Balancing now")
		err = humioClusterController.RebalanceStoragePartitions(hc)
		if err != nil {
			r.logger.Errorf("failed to balance storage partitions: %s", err)
			return err
		}
	}
	partitionsBalanced, err = humioClusterController.AreIngestPartitionsBalanced(hc)
	if err != nil {
		r.logger.Errorf("unable to check if ingest partitions are balanced: %s", err)
		return err
	}
	if !partitionsBalanced {
		r.logger.Info("ingest partitions are not balanced. Balancing now")
		err = humioClusterController.RebalanceIngestPartitions(hc)
		if err != nil {
			r.logger.Errorf("failed to balance ingest partitions: %s", err)
			return err
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureServiceExists(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	_, err := kubernetes.GetService(ctx, r.client, hc.Name, hc.Namespace)
	if k8serrors.IsNotFound(err) {
		service := kubernetes.ConstructService(hc.Name, hc.Namespace)
		if err := controllerutil.SetControllerReference(hc, service, r.scheme); err != nil {
			r.logger.Errorf("could not set controller reference: %s", err)
			return err
		}
		err = r.client.Create(ctx, service)
		if err != nil {
			r.logger.Errorf("unable to create service for HumioCluster: %s", err)
			return err
		}
	}
	return nil
}

// ensureMismatchedPodsAreDeleted is used to delete pods which container spec does not match that which is desired.
// If a pod is deleted, this will requeue immediately and rely on the next reconciliation to delete the next pod.
// The method only returns an empty result and no error if all pods are running the desired version,
// and no pod is currently being deleted.
func (r *ReconcileHumioCluster) ensureMismatchedPodsAreDeleted(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	foundPodList, err := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
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
			desiredPod, err := constructPod(hc)
			if err != nil {
				r.logger.Errorf("could not construct pod: %s", err)
				return reconcile.Result{}, err
			}

			podsMatchTest, err := r.podsMatch(pod, *desiredPod)
			if err != nil {
				r.logger.Errorf("failed to check if pods match %s", err)
			}
			if !podsMatchTest {
				// TODO: figure out if we should only allow upgrades and not downgrades
				r.logger.Infof("deleting pod %s", pod.Name)
				err = r.client.Delete(ctx, &pod)
				if err != nil {
					r.logger.Errorf("could not delete pod %s, got err: %s", pod.Name, err)
					return reconcile.Result{}, err
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

func (r *ReconcileHumioCluster) podsMatch(pod corev1.Pod, desiredPod corev1.Pod) (bool, error) {
	if _, ok := pod.Annotations[podHashAnnotation]; !ok {
		r.logger.Errorf("did not find annotation with pod hash")
		return false, fmt.Errorf("did not find annotation with pod hash")
	}
	desiredPodHash := asSHA256(desiredPod.Spec)
	if pod.Annotations[podHashAnnotation] == desiredPodHash {
		return true, nil
	}
	r.logger.Infof("pod hash annotation did does not match desired pod")
	return false, nil
}

func (r *ReconcileHumioCluster) ingressesMatch(ingress *v1beta1.Ingress, desiredIngress *v1beta1.Ingress) bool {
	if !reflect.DeepEqual(ingress.Spec, desiredIngress.Spec) {
		r.logger.Infof("ingress specs do not match: got %+v, wanted %+v", ingress.Spec, desiredIngress.Spec)
		return false
	}

	if !reflect.DeepEqual(ingress.Annotations, desiredIngress.Annotations) {
		r.logger.Infof("ingress annotations do not match: got %+v, wanted %+v", ingress.Annotations, desiredIngress.Annotations)
		return false
	}
	return true
}

// TODO: change to create 1 pod at a time, return Requeue=true and RequeueAfter.
// check that other pods, if they exist, are in a ready state
func (r *ReconcileHumioCluster) ensurePodsBootstrapped(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	foundPodList, err := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.logger.Errorf("failed to list pods: %s", err)
		return reconcile.Result{}, err
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
	if podsReadyCount == hc.Spec.NodeCount {
		r.logger.Info("all humio pods are reporting ready")
		return reconcile.Result{}, nil
	}

	if podsNotReadyCount > 0 {
		r.logger.Infof("there are %d humio pods that are not ready. all humio pods must report ready before reconciliation can continue", podsNotReadyCount)
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, nil
	}

	if podsReadyCount < hc.Spec.NodeCount {
		pod, err := constructPod(hc)
		if err != nil {
			r.logger.Errorf("unable to construct pod for HumioCluster: %s", err)
			return reconcile.Result{}, err
		}
		pod.Annotations["humio_pod_hash"] = asSHA256(pod.Spec)
		if err := controllerutil.SetControllerReference(hc, pod, r.scheme); err != nil {
			r.logger.Errorf("could not set controller reference: %s", err)
			return reconcile.Result{}, err
		}
		err = r.client.Create(ctx, pod)
		if err != nil {
			r.logger.Errorf("unable to create Pod for HumioCluster: %s", err)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		r.logger.Infof("successfully created pod %s for HumioCluster %s", pod.Name, hc.Name)
		prometheusMetrics.Counters.PodsCreated.Inc()
		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		// RequeueAfter is here to try to avoid issues where the requeue is faster than kubernetes
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) ensurePodsExist(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	foundPodList, err := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.logger.Errorf("failed to list pods: %s", err)
		return reconcile.Result{}, err
	}

	if len(foundPodList) < hc.Spec.NodeCount {
		pod, err := constructPod(hc)
		if err != nil {
			r.logger.Errorf("unable to construct pod for HumioCluster: %s", err)
			return reconcile.Result{}, err
		}
		pod.Annotations["humio_pod_hash"] = asSHA256(pod.Spec)
		if err := controllerutil.SetControllerReference(hc, pod, r.scheme); err != nil {
			r.logger.Errorf("could not set controller reference: %s", err)
			return reconcile.Result{}, err
		}
		err = r.client.Create(ctx, pod)
		if err != nil {
			r.logger.Errorf("unable to create Pod for HumioCluster: %s", err)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		r.logger.Infof("successfully created pod %s for HumioCluster %s", pod.Name, hc.Name)
		prometheusMetrics.Counters.PodsCreated.Inc()
		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) authWithSidecarToken(ctx context.Context, hc *corev1alpha1.HumioCluster, url string) (reconcile.Result, error) {
	existingSecret, err := kubernetes.GetSecret(ctx, r.client, kubernetes.ServiceTokenSecretName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.logger.Infof("waiting for sidecar to populate secret %s for HumioCluster %s", kubernetes.ServiceTokenSecretName, hc.Name)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 10}, nil
		}
	}

	// Either authenticate or re-authenticate with the persistent token
	return reconcile.Result{}, r.humioClient.Authenticate(
		&humioapi.Config{
			Address: url,
			Token:   string(existingSecret.Data["token"]),
		},
	)
}

// TODO: there is no need for this. We should instead change this to a get method where we return the list of env vars
// including the defaults
func envVarList(hc *corev1alpha1.HumioCluster) []corev1.EnvVar {
	setEnvironmentVariableDefaults(hc)
	return hc.Spec.EnvironmentVariables
}

// TODO: This is very generic, we may want to move this elsewhere in case we need to use it elsewhere.
func asSHA256(o interface{}) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%v", o)))
	return fmt.Sprintf("%x", h.Sum(nil))
}
