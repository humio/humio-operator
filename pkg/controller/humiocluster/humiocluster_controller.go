package humiocluster

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1beta1"
	"k8s.io/apimachinery/pkg/types"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"github.com/humio/humio-operator/pkg/openshift"
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
	watchTypes = append(watchTypes, &corev1.ServiceAccount{})
	watchTypes = append(watchTypes, &corev1.PersistentVolumeClaim{})
	// TODO: figure out if we need to watch SecurityContextConstraints?

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
	emptyResult := reconcile.Result{}

	// Ensure we have a valid CA certificate to configure intra-cluster communication.
	// Because generating the CA can take a while, we do this before we start tearing down mismatching pods
	err = r.ensureValidCASecret(context.TODO(), hc)
	if err != nil {
		r.logger.Errorf("could not ensure we have a valid CA secret: %s", err)
		return reconcile.Result{}, err
	}

	// Ensure pods that does not run the desired version are deleted.
	result, err := r.ensureMismatchedPodsAreDeleted(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	// Assume we are bootstrapping if no cluster state is set.
	// TODO: this is a workaround for the issue where humio pods cannot start up at the same time during the first boot
	if hc.Status.State == "" {
		err := r.setState(context.TODO(), corev1alpha1.HumioClusterStateBootstrapping, hc)
		if err != nil {
			r.logger.Infof("unable to set cluster state: %s", err)
			return reconcile.Result{}, err
		}
		r.incrementHumioClusterPodRevision(context.TODO(), hc, PodRestartPolicyRolling)
	}

	result, err = r.ensureHumioServiceAccountAnnotations(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

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

	// Ensure the users in the SCC are cleaned up.
	// This cleanup is only called as part of reconciling HumioCluster objects,
	// this means that you can end up with the SCC listing the service accounts
	// used for the last cluster to be deleted, in the case that all HumioCluster's are removed.
	// TODO: Determine if we should move this to a finalizer to fix the situation described above.
	err = r.ensureCleanupUsersInSecurityContextConstraints(context.TODO(), hc)
	if err != nil {
		r.logger.Errorf("could not ensure we clean up users in SecurityContextConstraints: %s", err)
		return reconcile.Result{}, err
	}

	// Ensure the CA Issuer is valid/ready
	err = r.ensureValidCAIssuer(context.TODO(), hc)
	if err != nil {
		r.logger.Errorf("could not ensure we have a valid CA issuer: %s", err)
		return reconcile.Result{}, err
	}
	// Ensure we have a k8s secret holding the ca.crt
	// This can be used in reverse proxies talking to Humio.
	err = r.ensureHumioClusterCACertBundle(context.TODO(), hc)
	if err != nil {
		r.logger.Errorf("could not ensure we have a CA cert bundle for the cluster: %s", err)
		return reconcile.Result{}, err
	}

	err = r.ensureHumioClusterKeystoreSecret(context.TODO(), hc)
	if err != nil {
		r.logger.Errorf("could not ensure we have a secret holding encryption key for keystore: %s", err)
		return reconcile.Result{}, err
	}

	err = r.ensureHumioNodeCertificates(context.TODO(), hc)
	if err != nil {
		r.logger.Errorf("could not ensure we have certificates ready for Humio nodes: %s", err)
		return reconcile.Result{}, err
	}

	err = r.ensureKafkaConfigConfigMap(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	result, err = r.ensurePersistentVolumeClaimsExist(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	// Ensure pods exist. Will requeue if not all pods are created and ready
	if hc.Status.State == corev1alpha1.HumioClusterStateBootstrapping {
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

	if hc.Status.State == corev1alpha1.HumioClusterStateBootstrapping {
		err = r.setState(context.TODO(), corev1alpha1.HumioClusterStateRunning, hc)
		if err != nil {
			r.logger.Infof("unable to set cluster state: %s", err)
			return reconcile.Result{}, err
		}
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
		r.setPod(ctx, hc)

	}(context.TODO(), r.humioClient, hc)

	result, err = r.ensurePodsExist(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	err = r.ensureLabels(context.TODO(), hc)
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

	result, err = r.cleanupUnusedTLSCertificates(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	// TODO: cleanup of unused TLS secrets only removes those that are related to the current HumioCluster,
	//       which means we end up with orphaned secrets when deleting a HumioCluster.
	result, err = r.cleanupUnusedTLSSecrets(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	// All done, requeue every 30 seconds even if no changes were made
	r.logger.Info("done reconciling, will requeue after 30 seconds")
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
}

// ensureKafkaConfigConfigMap creates a configmap containing configs specified in extraKafkaConfigs which will be mounted
// into the Humio container and pointed to by Humio's configuration option EXTRA_KAFKA_CONFIGS_FILE
func (r *ReconcileHumioCluster) ensureKafkaConfigConfigMap(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	extraKafkaConfigsConfigMapData := extraKafkaConfigsOrDefault(hc)
	if extraKafkaConfigsConfigMapData == "" {
		return nil
	}
	_, err := kubernetes.GetConfigMap(ctx, r.client, extraKafkaConfigsConfigMapName(hc), hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			configMap := kubernetes.ConstructExtraKafkaConfigsConfigMap(
				extraKafkaConfigsConfigMapName(hc),
				extraKafkaPropertiesFilename,
				extraKafkaConfigsConfigMapData,
				hc.Name,
				hc.Namespace,
			)
			if err := controllerutil.SetControllerReference(hc, configMap, r.scheme); err != nil {
				r.logger.Errorf("could not set controller reference: %s", err)
				return err
			}
			err = r.client.Create(ctx, configMap)
			if err != nil {
				r.logger.Errorf("unable to create extra kafka configs configmap for HumioCluster: %s", err)
				return err
			}
			r.logger.Infof("successfully created extra kafka configs configmap %s for HumioCluster %s", configMap, hc.Name)
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

	r.logger.Info("ensuring pod permissions")
	err := r.ensureServiceAccountExists(ctx, hc, humioServiceAccountNameOrDefault(hc), humioServiceAccountAnnotationsOrDefault(hc))
	if err != nil {
		r.logger.Errorf("unable to ensure humio service account exists for HumioCluster: %s", err)
		return err
	}

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		err = r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hc, humioServiceAccountNameOrDefault(hc))
		if err != nil {
			r.logger.Errorf("could not ensure SecurityContextConstraints contains ServiceAccount: %s", err)
			return err
		}
	}

	return nil
}

func (r *ReconcileHumioCluster) ensureInitContainerPermissions(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	// We do not want to attach the init service account to the humio pod. Instead, only the init container should use this
	// service account. To do this, we can attach the service account directly to the init container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	err := r.ensureServiceAccountSecretExists(ctx, hc, initServiceAccountSecretName(hc), initServiceAccountNameOrDefault(hc))
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

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		err = r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hc, initServiceAccountNameOrDefault(hc))
		if err != nil {
			r.logger.Errorf("could not ensure SecurityContextConstraints contains ServiceAccount: %s", err)
			return err
		}
	}

	return nil
}

func (r *ReconcileHumioCluster) ensureAuthContainerPermissions(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	// We do not want to attach the auth service account to the humio pod. Instead, only the auth container should use this
	// service account. To do this, we can attach the service account directly to the auth container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	err := r.ensureServiceAccountSecretExists(ctx, hc, authServiceAccountSecretName(hc), authServiceAccountNameOrDefault(hc))
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

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		err = r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hc, authServiceAccountNameOrDefault(hc))
		if err != nil {
			r.logger.Errorf("could not ensure SecurityContextConstraints contains ServiceAccount: %s", err)
			return err
		}
	}

	return nil
}

func (r *ReconcileHumioCluster) ensureSecurityContextConstraintsContainsServiceAccount(ctx context.Context, hc *corev1alpha1.HumioCluster, serviceAccountName string) error {
	// TODO: Write unit/e2e test for this

	if !helpers.IsOpenShift() {
		return fmt.Errorf("updating SecurityContextConstraints are only suppoted when running on OpenShift")
	}

	// Get current SCC
	scc, err := openshift.GetSecurityContextConstraints(ctx, r.client)
	if err != nil {
		r.logger.Errorf("unable to get details about SecurityContextConstraints: %s", err)
		return err
	}

	// Give ServiceAccount access to SecurityContextConstraints if not already present
	usersEntry := fmt.Sprintf("system:serviceaccount:%s:%s", hc.Namespace, serviceAccountName)
	if !helpers.ContainsElement(scc.Users, usersEntry) {
		scc.Users = append(scc.Users, usersEntry)
		err = r.client.Update(ctx, scc)
		if err != nil {
			r.logger.Errorf("could not update SecurityContextConstraints %s to add ServiceAccount %s: %s", scc.Name, serviceAccountName, err)
			return err
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensureCleanupUsersInSecurityContextConstraints(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	if !helpers.IsOpenShift() {
		return nil
	}

	scc, err := openshift.GetSecurityContextConstraints(ctx, r.client)
	if err != nil {
		r.logger.Errorf("unable to get details about SecurityContextConstraints: %s", err)
		return err
	}

	for _, userEntry := range scc.Users {
		sccUserData := strings.Split(userEntry, ":")
		sccUserNamespace := sccUserData[2]
		sccUserName := sccUserData[3]

		_, err := kubernetes.GetServiceAccount(ctx, r.client, sccUserName, sccUserNamespace)
		if err == nil {
			// We found an existing service account
			continue
		}
		if k8serrors.IsNotFound(err) {
			// If we have an error and it reflects that the service account does not exist, we remove the entry from the list.
			scc.Users = helpers.RemoveElement(scc.Users, fmt.Sprintf("system:serviceaccount:%s:%s", sccUserNamespace, sccUserName))
			err = r.client.Update(ctx, scc)
			if err != nil {
				r.logger.Errorf("unable to update SecurityContextConstraints: %s", err)
				return err
			}
		} else {
			r.logger.Errorf("unable to get existing service account: %s", err)
			return err
		}
	}

	return nil
}

func (r *ReconcileHumioCluster) ensureValidCAIssuer(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.logger.Debugf("cluster not configured to run with tls, skipping")
		return nil
	}

	r.logger.Debugf("checking for an existing valid CA Issuer")
	validCAIssuer, err := validCAIssuer(ctx, r.client, hc.Namespace, hc.Name)
	if err != nil && !k8serrors.IsNotFound(err) {
		r.logger.Warnf("could not validate CA Issuer: %s", err)
		return err
	}
	if validCAIssuer {
		r.logger.Debugf("found valid CA Issuer")
		return nil
	}

	var existingCAIssuer cmapi.Issuer
	err = r.client.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, &existingCAIssuer)
	if err != nil {
		if errors.IsNotFound(err) {
			caIssuer := constructCAIssuer(hc)
			if err := controllerutil.SetControllerReference(hc, &caIssuer, r.scheme); err != nil {
				r.logger.Errorf("could not set controller reference: %s", err)
				return err
			}
			// should only create it if it doesn't exist
			err = r.client.Create(ctx, &caIssuer)
			if err != nil {
				r.logger.Errorf("could not create CA Issuer: %s", err)
				return err
			}
			return nil
		}
		return err
	}

	return nil
}

func (r *ReconcileHumioCluster) ensureValidCASecret(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.logger.Debugf("cluster not configured to run with tls, skipping")
		return nil
	}

	r.logger.Debugf("checking for an existing CA secret")
	validCASecret, err := validCASecret(ctx, r.client, hc.Namespace, getCASecretName(hc))
	if validCASecret {
		r.logger.Infof("found valid CA secret")
		return nil
	}
	if err != nil && !k8serrors.IsNotFound(err) {
		r.logger.Warnf("could not validate CA secret")
		return err
	}

	if useExistingCA(hc) {
		r.logger.Errorf("specified CA secret invalid")
		return fmt.Errorf("configured to use existing CA secret, but the CA secret invalid")
	}

	r.logger.Debugf("generating new CA certificate")
	ca, err := generateCACertificate()
	if err != nil {
		r.logger.Errorf("could not generate new CA certificate: %s", err)
		return err
	}

	r.logger.Debugf("persisting new CA certificate")
	caSecretData := map[string][]byte{
		"tls.crt": ca.Certificate,
		"tls.key": ca.Key,
	}
	caSecret := kubernetes.ConstructSecret(hc.Name, hc.Namespace, getCASecretName(hc), caSecretData)
	if err := controllerutil.SetControllerReference(hc, caSecret, r.scheme); err != nil {
		r.logger.Errorf("could not set controller reference: %s", err)
		return err
	}
	err = r.client.Create(ctx, caSecret)
	if err != nil {
		r.logger.Errorf("could not create secret with CA: %s", err)
		return err
	}

	return nil
}

func (r *ReconcileHumioCluster) ensureHumioClusterKeystoreSecret(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.logger.Debugf("cluster not configured to run with tls, skipping")
		return nil
	}

	existingSecret := &corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      fmt.Sprintf("%s-keystore-passphrase", hc.Name),
	}, existingSecret)

	if k8serrors.IsNotFound(err) {
		randomPass := kubernetes.RandomString()
		secretData := map[string][]byte{
			"passphrase": []byte(randomPass), // TODO: do we need separate passwords for different aspects?
		}
		secret := kubernetes.ConstructSecret(hc.Name, hc.Namespace, fmt.Sprintf("%s-keystore-passphrase", hc.Name), secretData)
		if err := controllerutil.SetControllerReference(hc, secret, r.scheme); err != nil {
			r.logger.Errorf("could not set controller reference: %s", err)
			return err
		}
		err := r.client.Create(ctx, secret)
		if err != nil {
			r.logger.Errorf("could not create secret: %s", err)
			return err
		}
		return nil
	}

	return err
}

func (r *ReconcileHumioCluster) ensureHumioClusterCACertBundle(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.logger.Debugf("cluster not configured to run with tls, skipping")
		return nil
	}

	r.logger.Debugf("ensuring we have a CA cert bundle")
	existingCertificate := &cmapi.Certificate{}
	err := r.client.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, existingCertificate)
	if k8serrors.IsNotFound(err) {
		r.logger.Infof("CA cert bundle doesn't exist, creating it now")
		cert := constructClusterCACertificateBundle(hc)
		if err := controllerutil.SetControllerReference(hc, &cert, r.scheme); err != nil {
			r.logger.Errorf("could not set controller reference: %s", err)
			return err
		}
		err := r.client.Create(ctx, &cert)
		if err != nil {
			r.logger.Errorf("could not create certificate: %s", err)
			return err
		}
		return nil

	}

	return err
}

func (r *ReconcileHumioCluster) ensureHumioNodeCertificates(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.logger.Debugf("cluster not configured to run with tls, skipping")
		return nil
	}
	certificates, err := kubernetes.ListCertificates(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return err
	}
	existingNodeCertCount := 0
	for _, cert := range certificates {
		if strings.HasPrefix(cert.Name, fmt.Sprintf("%s-core", hc.Name)) {
			existingNodeCertCount++
		}
	}
	for i := existingNodeCertCount; i < hc.Spec.NodeCount; i++ {
		certificate := constructNodeCertificate(hc, kubernetes.RandomString())
		r.logger.Infof("creating node TLS certificate with name %s", certificate.Name)
		if err := controllerutil.SetControllerReference(hc, &certificate, r.scheme); err != nil {
			r.logger.Errorf("could not set controller reference: %s", err)
			return err
		}
		err := r.client.Create(ctx, &certificate)
		if err != nil {
			return err
		}
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
			if err := controllerutil.SetControllerReference(hc, role, r.scheme); err != nil {
				r.logger.Errorf("could not set controller reference: %s", err)
				return err
			}
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
			roleBinding := kubernetes.ConstructRoleBinding(
				roleBindingName,
				authRoleName(hc),
				hc.Name,
				hc.Namespace,
				authServiceAccountNameOrDefault(hc),
			)
			if err := controllerutil.SetControllerReference(hc, roleBinding, r.scheme); err != nil {
				r.logger.Errorf("could not set controller reference: %s", err)
				return err
			}
			err = r.client.Create(ctx, roleBinding)
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

func (r *ReconcileHumioCluster) ensureServiceAccountSecretExists(ctx context.Context, hc *corev1alpha1.HumioCluster, serviceAccountSecretName, serviceAccountName string) error {
	foundServiceAccountSecretsList, err := kubernetes.ListSecrets(ctx, r.client, hc.Namespace, kubernetes.MatchingLabelsForSecret(hc.Name, serviceAccountSecretName))
	if err != nil {
		r.logger.Errorf("unable list secrets for HumioCluster: %s", err)
		return err
	}

	if len(foundServiceAccountSecretsList) == 0 {
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

	return nil
}

func (r *ReconcileHumioCluster) ensureLabels(ctx context.Context, hc *corev1alpha1.HumioCluster) error {
	r.logger.Info("ensuring labels")
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

	pvcList, err := r.pvcList(hc)
	if err != nil {
		r.logger.Errorf("failed to list pvcs to assign labels: %s", err)
		return err
	}

	for _, pod := range foundPodList {
		// Skip pods that already have a label. Check that the pvc also has the label if applicable
		if kubernetes.LabelListContainsLabel(pod.GetLabels(), kubernetes.NodeIdLabelName) {
			if pvcsEnabled(hc) {
				err := r.ensurePvcLabels(ctx, hc, pod, pvcList)
				if err != nil {
					r.logger.Error(err)
					return err
				}
			}
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
				if pvcsEnabled(hc) {
					err = r.ensurePvcLabels(ctx, hc, pod, pvcList)
					if err != nil {
						r.logger.Error(err)
						return err
					}
				}
			}
		}
	}
	return nil
}

func (r *ReconcileHumioCluster) ensurePvcLabels(ctx context.Context, hc *corev1alpha1.HumioCluster, pod corev1.Pod, pvcList []corev1.PersistentVolumeClaim) error {
	pvc, err := findPvcForPod(pvcList, pod)
	if err != nil {
		r.logger.Errorf("failed to get pvc for pod to assign labels: %s", err)
		return err
	}
	if kubernetes.LabelListContainsLabel(pvc.GetLabels(), kubernetes.NodeIdLabelName) {
		return nil
	}
	nodeId, err := strconv.Atoi(pod.Labels[kubernetes.NodeIdLabelName])
	if err != nil {
		return fmt.Errorf("unable to set label on pvc, nodeid %v is invalid: %s", pod.Labels[kubernetes.NodeIdLabelName], err)
	}
	labels := kubernetes.LabelsForPersistentVolume(hc.Name, nodeId)
	r.logger.Infof("setting labels for pvc %s, labels=%v", pvc.Name, labels)
	pvc.SetLabels(labels)
	if err := r.client.Update(ctx, &pvc); err != nil {
		r.logger.Errorf("failed to update labels on pvc %s: %s", pod.Name, err)
		return err
	}
	return nil
}

func (r *ReconcileHumioCluster) ensurePartitionsAreBalanced(humioClusterController humio.ClusterController, hc *corev1alpha1.HumioCluster) error {
	if !hc.Spec.AutoRebalancePartitions {
		r.logger.Info("partition auto-rebalancing not enabled, skipping")
		return nil
	}
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
	r.logger.Info("ensuring service")
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

// cleanupUnusedTLSCertificates finds all existing per-node certificates for a specific HumioCluster
// and cleans them up if we have no use for them anymore.
func (r *ReconcileHumioCluster) cleanupUnusedTLSSecrets(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	if !helpers.UseCertManager() {
		r.logger.Debugf("cert-manager not available, skipping")
		return reconcile.Result{}, nil
	}

	// because these secrets are created by cert-manager we cannot use our typical label selector
	foundSecretList, err := kubernetes.ListSecrets(ctx, r.client, hc.Namespace, client.MatchingLabels{})
	if err != nil {
		r.logger.Warnf("unable to list secrets: %s", err)
		return reconcile.Result{}, err
	}
	if len(foundSecretList) == 0 {
		return reconcile.Result{}, nil
	}

	for _, secret := range foundSecretList {
		if !helpers.TLSEnabled(hc) {
			if secret.Type == corev1.SecretTypeOpaque {
				if secret.Name == fmt.Sprintf("%s-%s", hc.Name, "ca-keypair") ||
					secret.Name == fmt.Sprintf("%s-%s", hc.Name, "keystore-passphrase") {
					r.logger.Infof("TLS is not enabled for cluster, removing unused secret: %s", secret.Name)
					err := r.client.Delete(ctx, &secret)
					if err != nil {
						return reconcile.Result{}, err
					}
				}
			}
		}

		issuerName, found := secret.Annotations[cmapi.IssuerNameAnnotationKey]
		if !found || issuerName != hc.Name {
			continue
		}
		if secret.Type != corev1.SecretTypeTLS {
			continue
		}
		// only consider secrets not already being deleted
		if secret.DeletionTimestamp == nil {
			inUse := true // assume it is in use until we find out otherwise
			if !strings.HasPrefix(secret.Name, fmt.Sprintf("%s-core-", hc.Name)) {
				// this is the cluster-wide secret
				if hc.Spec.TLS != nil {
					if hc.Spec.TLS.Enabled != nil {
						if *hc.Spec.TLS.Enabled == false {
							inUse = false
						}
					}
				}
			} else {
				// this is the per-node secret
				inUse, err = r.tlsCertSecretInUse(ctx, secret.Namespace, secret.Name)
				if err != nil {
					r.logger.Warnf("unable to determine if secret is in use: %s", err)
					return reconcile.Result{}, err
				}
			}
			if !inUse {
				r.logger.Infof("deleting secret %s", secret.Name)
				err = r.client.Delete(ctx, &secret)
				if err != nil {
					r.logger.Errorf("could not delete secret %s, got err: %s", secret.Name, err)
					return reconcile.Result{}, err
				}
				return reconcile.Result{Requeue: true}, nil
			}
		}
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return reconcile.Result{}, nil
}

// cleanupUnusedTLSCertificates finds all existing per-node certificates and cleans them up if we have no matching pod for them
func (r *ReconcileHumioCluster) cleanupUnusedTLSCertificates(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	if !helpers.UseCertManager() {
		r.logger.Debugf("cert-manager not available, skipping")
		return reconcile.Result{}, nil
	}

	foundCertificateList, err := kubernetes.ListCertificates(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.logger.Warnf("unable to list certificates: %s", err)
		return reconcile.Result{}, err
	}
	if len(foundCertificateList) == 0 {
		return reconcile.Result{}, nil
	}

	for _, certificate := range foundCertificateList {
		// only consider secrets not already being deleted
		if certificate.DeletionTimestamp == nil {
			inUse := true // assume it is in use until we find out otherwise
			if !strings.HasPrefix(certificate.Name, fmt.Sprintf("%s-core-", hc.Name)) {
				// this is the cluster-wide secret
				if hc.Spec.TLS != nil {
					if hc.Spec.TLS.Enabled != nil {
						if *hc.Spec.TLS.Enabled == false {
							inUse = false
						}
					}
				}
			} else {
				// this is the per-node secret
				inUse, err = r.tlsCertSecretInUse(ctx, certificate.Namespace, certificate.Name)
				if err != nil {
					r.logger.Warnf("unable to determine if certificate is in use: %s", err)
					return reconcile.Result{}, err
				}
			}
			if !inUse {
				r.logger.Infof("deleting certificate %s", certificate.Name)
				err = r.client.Delete(ctx, &certificate)
				if err != nil {
					r.logger.Errorf("could not delete certificate %s, got err: %s", certificate.Name, err)
					return reconcile.Result{}, err
				}
				return reconcile.Result{Requeue: true}, nil
			}
		}
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) tlsCertSecretInUse(ctx context.Context, secretNamespace, secretName string) (bool, error) {
	pod := &corev1.Pod{}
	err := r.client.Get(ctx, types.NamespacedName{
		Namespace: secretNamespace,
		Name:      secretName,
	}, pod)

	if k8serrors.IsNotFound(err) {
		return false, nil
	}
	return true, err
}

func (r *ReconcileHumioCluster) getInitServiceAccountSecretName(ctx context.Context, hc *corev1alpha1.HumioCluster) (string, error) {
	if hc.Spec.InitServiceAccountName != "" {
		return hc.Spec.InitServiceAccountName, nil
	}
	foundInitServiceAccountSecretsList, err := kubernetes.ListSecrets(ctx, r.client, hc.Namespace, kubernetes.MatchingLabelsForSecret(hc.Name, initServiceAccountSecretName(hc)))
	if err != nil {
		return "", err
	}
	if len(foundInitServiceAccountSecretsList) == 0 {
		return "", nil
	}
	if len(foundInitServiceAccountSecretsList) > 1 {
		return "", fmt.Errorf("found more than one init service account")
	}
	return foundInitServiceAccountSecretsList[0].Name, nil
}

func (r *ReconcileHumioCluster) getAuthServiceAccountSecretName(ctx context.Context, hc *corev1alpha1.HumioCluster) (string, error) {
	if hc.Spec.AuthServiceAccountName != "" {
		return hc.Spec.AuthServiceAccountName, nil
	}
	foundAuthServiceAccountNameSecretsList, err := kubernetes.ListSecrets(ctx, r.client, hc.Namespace, kubernetes.MatchingLabelsForSecret(hc.Name, authServiceAccountSecretName(hc)))
	if err != nil {
		return "", err
	}
	if len(foundAuthServiceAccountNameSecretsList) == 0 {
		return "", nil
	}
	if len(foundAuthServiceAccountNameSecretsList) > 1 {
		return "", fmt.Errorf("found more than one init service account")
	}
	return foundAuthServiceAccountNameSecretsList[0].Name, nil
}

func (r *ReconcileHumioCluster) ensureHumioServiceAccountAnnotations(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	// Don't change the service account annotations if the service account is not managed by the operator
	if hc.Spec.HumioServiceAccountName != "" {
		return reconcile.Result{}, nil
	}
	serviceAccountName := humioServiceAccountNameOrDefault(hc)
	serviceAccountAnnotations := humioServiceAccountAnnotationsOrDefault(hc)

	r.logger.Infof("ensuring service account %s annotations", serviceAccountName)
	existingServiceAccount, err := kubernetes.GetServiceAccount(ctx, r.client, serviceAccountName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		r.logger.Errorf("failed to get service account %s: %s", serviceAccountName, err)
		return reconcile.Result{}, err
	}

	serviceAccount := kubernetes.ConstructServiceAccount(serviceAccountName, hc.Name, hc.Namespace, serviceAccountAnnotations)
	if !reflect.DeepEqual(existingServiceAccount.Annotations, serviceAccount.Annotations) {
		r.logger.Infof("service account annotations do not match: annotations %s, got %s. updating service account %s",
			helpers.MapToString(serviceAccount.Annotations), helpers.MapToString(existingServiceAccount.Annotations), existingServiceAccount.Name)
		existingServiceAccount.Annotations = serviceAccount.Annotations
		err = r.client.Update(ctx, existingServiceAccount)
		if err != nil {
			r.logger.Errorf("could not update service account %s, got err: %s", existingServiceAccount.Name, err)
			return reconcile.Result{}, err
		}

		// Trigger restart of humio to pick up the updated service account
		r.incrementHumioClusterPodRevision(ctx, hc, PodRestartPolicyRolling)

		return reconcile.Result{Requeue: true}, nil
	}
	return reconcile.Result{}, nil
}

// ensureMismatchedPodsAreDeleted is used to delete pods which container spec does not match that which is desired.
// The behavior of this depends on what, if anything, was changed in the pod. If there are changes that fall under a
// rolling update, then the pod restart policy is set to PodRestartPolicyRolling and the reconciliation will continue if
// there are any pods not in a ready state. This is so replacement pods may be created.
// If there are changes that fall under a recreate update, the the pod restart policy is set to PodRestartPolicyRecreate
// and the reconciliation will requeue and the deletions will continue to be executed until all the pods have been
// removed.
func (r *ReconcileHumioCluster) ensureMismatchedPodsAreDeleted(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	foundPodList, err := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return reconcile.Result{}, err
	}

	// if we do not have any pods running we have nothing to delete
	if len(foundPodList) == 0 {
		return reconcile.Result{}, nil
	}

	var podBeingDeleted bool
	var waitingOnReadyPods bool
	r.logger.Info("ensuring mismatching pods are deleted")

	attachments, err := r.newPodAttachments(ctx, hc, foundPodList)
	if err != nil {
		r.logger.Errorf("failed to get pod attachments: %s", err)
	}

	// If we allow a rolling update, then don't take down more than one pod at a time.
	// Check the number of ready pods. if we have already deleted a pod, then the ready count will less than expected,
	// but we must continue with reconciliation so the pod may be created later in the reconciliation.
	// If we're doing a non-rolling update (recreate), then we can take down all the pods without waiting, but we will
	// wait until all the pods are ready before changing the cluster state back to Running.
	podsReadyCount, podsNotReadyCount := r.podsReady(foundPodList)
	if podsReadyCount < hc.Spec.NodeCount || podsNotReadyCount > 0 {
		waitingOnReadyPods = true
		r.logger.Infof("there are %d/%d humio pods that are ready", podsReadyCount, hc.Spec.NodeCount)
	}

	if (r.getHumioClusterPodRestartPolicy(hc) == PodRestartPolicyRolling && !waitingOnReadyPods) ||
		r.getHumioClusterPodRestartPolicy(hc) == PodRestartPolicyRecreate {
		desiredLifecycleState, err := r.getPodDesiredLifecycleState(hc, foundPodList, attachments)
		if err != nil {
			r.logger.Errorf("got error when getting pod desired lifecycle: %s", err)
			return reconcile.Result{}, err
		}
		// If we are currently deleting pods, then check if the cluster state is Running. If it is, then change to an
		// appropriate state depending on the restart policy.
		// If the cluster state is set as per the restart policy:
		// 	 PodRestartPolicyRecreate == HumioClusterStateUpgrading
		// 	 PodRestartPolicyRolling == HumioClusterStateRestarting
		if desiredLifecycleState.delete {
			if hc.Status.State == corev1alpha1.HumioClusterStateRunning {
				if desiredLifecycleState.restartPolicy == PodRestartPolicyRecreate {
					if err = r.setState(ctx, corev1alpha1.HumioClusterStateUpgrading, hc); err != nil {
						r.logger.Errorf("failed to set state to %s: %s", corev1alpha1.HumioClusterStateUpgrading, err)
					}
					if revision, err := r.incrementHumioClusterPodRevision(ctx, hc, PodRestartPolicyRecreate); err != nil {
						r.logger.Errorf("failed to increment pod revision to %d: %s", revision, err)
					}
				}
				if desiredLifecycleState.restartPolicy == PodRestartPolicyRolling {
					if err = r.setState(ctx, corev1alpha1.HumioClusterStateRestarting, hc); err != nil {
						r.logger.Errorf("failed to set state to %s: %s", corev1alpha1.HumioClusterStateRestarting, err)
					}
					if revision, err := r.incrementHumioClusterPodRevision(ctx, hc, PodRestartPolicyRolling); err != nil {
						r.logger.Errorf("failed to increment pod revision to %d: %s", revision, err)
					}
				}
			}
			r.logger.Infof("deleting pod %s", desiredLifecycleState.pod.Name)
			podBeingDeleted = true
			err = r.client.Delete(ctx, &desiredLifecycleState.pod)
			if err != nil {
				r.logger.Errorf("could not delete pod %s, got err: %s", desiredLifecycleState.pod.Name, err)
				return reconcile.Result{}, err
			}
		}
	}

	// If we have pods being deleted, requeue as long as we're not doing a rolling update. This will ensure all pods
	// are removed before creating the replacement pods.
	if podBeingDeleted && (r.getHumioClusterPodRestartPolicy(hc) == PodRestartPolicyRecreate) {
		return reconcile.Result{Requeue: true}, nil
	}

	// Set the cluster state back to HumioClusterStateRunning to indicate we are no longer restarting. This can only
	// happen when we know that all of the pods are in a Ready state and that we are no longer deleting pods.
	if !waitingOnReadyPods && !podBeingDeleted {
		if hc.Status.State == corev1alpha1.HumioClusterStateRestarting || hc.Status.State == corev1alpha1.HumioClusterStateUpgrading {
			r.logger.Infof("no longer deleting pods. changing cluster state from %s to %s", hc.Status.State, corev1alpha1.HumioClusterStateRunning)
			if err = r.setState(ctx, corev1alpha1.HumioClusterStateRunning, hc); err != nil {
				r.logger.Errorf("failed to set state to %s: %s", corev1alpha1.HumioClusterStateRunning, err)
			}
		}
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return reconcile.Result{}, nil
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

// check that other pods, if they exist, are in a ready state
func (r *ReconcileHumioCluster) ensurePodsBootstrapped(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	r.logger.Info("ensuring pods are bootstrapped")
	foundPodList, err := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.logger.Errorf("failed to list pods: %s", err)
		return reconcile.Result{}, err
	}
	r.logger.Debugf("found %d pods", len(foundPodList))

	podsReadyCount, podsNotReadyCount := r.podsReady(foundPodList)
	if podsReadyCount == hc.Spec.NodeCount {
		r.logger.Info("all humio pods are reporting ready")
		return reconcile.Result{}, nil
	}

	if podsNotReadyCount > 0 {
		r.logger.Infof("there are %d humio pods that are not ready. all humio pods must report ready before reconciliation can continue", podsNotReadyCount)
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, nil
	}

	r.logger.Debugf("pod ready count is %d, while desired node count is %d", podsReadyCount, hc.Spec.NodeCount)
	if podsReadyCount < hc.Spec.NodeCount {
		attachments, err := r.newPodAttachments(ctx, hc, foundPodList)
		if err != nil {
			r.logger.Errorf("failed to get pod attachments: %s", err)
		}
		err = r.createPod(ctx, hc, attachments)
		if err != nil {
			r.logger.Errorf("unable to create Pod for HumioCluster: %s", err)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		prometheusMetrics.Counters.PodsCreated.Inc()

		// check that we can list the new pod
		// this is to avoid issues where the requeue is faster than kubernetes
		if err := r.waitForNewPod(hc, len(foundPodList)+1); err != nil {
			r.logger.Errorf("failed to validate new pod: %s", err)
			return reconcile.Result{}, err
		}

		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
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

	attachments, err := r.newPodAttachments(ctx, hc, foundPodList)
	if err != nil {
		r.logger.Errorf("failed to get pod attachments: %s", err)
	}

	if len(foundPodList) < hc.Spec.NodeCount {
		err = r.createPod(ctx, hc, attachments)
		if err != nil {
			r.logger.Errorf("unable to create Pod for HumioCluster: %s", err)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		prometheusMetrics.Counters.PodsCreated.Inc()

		// check that we can list the new pod
		// this is to avoid issues where the requeue is faster than kubernetes
		if err := r.waitForNewPod(hc, len(foundPodList)+1); err != nil {
			r.logger.Errorf("failed to validate new pod: %s", err)
			return reconcile.Result{}, err
		}

		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) ensurePersistentVolumeClaimsExist(ctx context.Context, hc *corev1alpha1.HumioCluster) (reconcile.Result, error) {
	if !pvcsEnabled(hc) {
		r.logger.Info("pvcs are disabled. skipping")
		return reconcile.Result{}, nil
	}

	r.logger.Info("ensuring pvcs")
	foundPersistentVolumeClaims, err := kubernetes.ListPersistentVolumeClaims(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	r.logger.Debugf("found %d pvcs", len(foundPersistentVolumeClaims))

	if err != nil {
		r.logger.Errorf("failed to list pvcs: %s", err)
		return reconcile.Result{}, err
	}

	if len(foundPersistentVolumeClaims) < hc.Spec.NodeCount {
		r.logger.Infof("pvc count of %d is less than %d. adding more", len(foundPersistentVolumeClaims), hc.Spec.NodeCount)
		pvc := constructPersistentVolumeClaim(hc)
		pvc.Annotations["humio_pvc_hash"] = helpers.AsSHA256(pvc.Spec)
		if err := controllerutil.SetControllerReference(hc, pvc, r.scheme); err != nil {
			r.logger.Errorf("could not set controller reference: %s", err)
			return reconcile.Result{}, err
		}
		err = r.client.Create(ctx, pvc)
		if err != nil {
			r.logger.Errorf("unable to create pvc for HumioCluster: %s", err)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		r.logger.Infof("successfully created pvc %s for HumioCluster %s", pvc.Name, hc.Name)
		prometheusMetrics.Counters.PvcsCreated.Inc()

		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pvcs than are expected?
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) authWithSidecarToken(ctx context.Context, hc *corev1alpha1.HumioCluster, url string) (reconcile.Result, error) {
	adminTokenSecretName := fmt.Sprintf("%s-%s", hc.Name, kubernetes.ServiceTokenSecretNameSuffix)
	existingSecret, err := kubernetes.GetSecret(ctx, r.client, adminTokenSecretName, hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.logger.Infof("waiting for sidecar to populate secret %s for HumioCluster %s", adminTokenSecretName, hc.Name)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 10}, nil
		}
	}

	humioAPIConfig := &humioapi.Config{
		Address: url,
		Token:   string(existingSecret.Data["token"]),
	}

	// Get CA
	if helpers.TLSEnabled(hc) {
		existingCABundle, err := kubernetes.GetSecret(ctx, r.client, constructClusterCACertificateBundle(hc).Spec.SecretName, hc.Namespace)
		if k8serrors.IsNotFound(err) {
			r.logger.Infof("waiting for secret with CA bundle")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 10}, nil
		}
		if err != nil {
			r.logger.Warnf("unable to obtain CA certificate: %s", err)
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 10}, err
		}
		humioAPIConfig.CACertificate = existingCABundle.Data["ca.crt"]
	}

	// Either authenticate or re-authenticate with the persistent token
	return reconcile.Result{}, r.humioClient.Authenticate(humioAPIConfig)
}

// TODO: there is no need for this. We should instead change this to a get method where we return the list of env vars
// including the defaults
func envVarList(hc *corev1alpha1.HumioCluster) []corev1.EnvVar {
	setEnvironmentVariableDefaults(hc)
	return hc.Spec.EnvironmentVariables
}

func (r *ReconcileHumioCluster) pvcList(hc *corev1alpha1.HumioCluster) ([]corev1.PersistentVolumeClaim, error) {
	if pvcsEnabled(hc) {
		return kubernetes.ListPersistentVolumeClaims(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	}
	return []corev1.PersistentVolumeClaim{}, nil
}
