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

package controllers

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/zapr"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"github.com/humio/humio-operator/pkg/openshift"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1beta1"
	uberzap "go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/api/networking/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"github.com/humio/humio-operator/pkg/humio"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioClusterReconciler reconciles a HumioCluster object
type HumioClusterReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	HumioClient humio.Client
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=services,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=services/finalizers,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=endpoints,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingress,verbs=create;delete;get;list;patch;update;watch

func (r *HumioClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	zapLog, _ := uberzap.NewProduction(uberzap.AddCaller(), uberzap.AddCallerSkip(1))
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioCluster")

	// Fetch the HumioCluster
	hc := &humiov1alpha1.HumioCluster{}
	err := r.Get(context.TODO(), req.NamespacedName, hc)
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
		r.Log.Error(err, "could not ensure we have a valid CA secret")
		return reconcile.Result{}, err
	}

	// Ensure pods that does not run the desired version are deleted.
	result, err := r.ensureMismatchedPodsAreDeleted(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	_, err = constructPod(hc, "", &podAttachments{})
	if err != nil {
		err = r.setState(context.TODO(), humiov1alpha1.HumioClusterStateConfigError, hc)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
		}
		return reconcile.Result{}, err
	}

	// Assume we are bootstrapping if no cluster state is set.
	// TODO: this is a workaround for the issue where humio pods cannot start up at the same time during the first boot
	if hc.Status.State == "" {
		err := r.setState(context.TODO(), humiov1alpha1.HumioClusterStateBootstrapping, hc)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
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
		r.Log.Error(err, "could not ensure we clean up users in SecurityContextConstraints")
		return reconcile.Result{}, err
	}

	// Ensure the CA Issuer is valid/ready
	err = r.ensureValidCAIssuer(context.TODO(), hc)
	if err != nil {
		r.Log.Error(err, "could not ensure we have a valid CA issuer")
		return reconcile.Result{}, err
	}
	// Ensure we have a k8s secret holding the ca.crt
	// This can be used in reverse proxies talking to Humio.
	err = r.ensureHumioClusterCACertBundle(context.TODO(), hc)
	if err != nil {
		r.Log.Error(err, "could not ensure we have a CA cert bundle")
		return reconcile.Result{}, err
	}

	err = r.ensureHumioClusterKeystoreSecret(context.TODO(), hc)
	if err != nil {
		r.Log.Error(err, "could not ensure we have a secret holding keystore encryption key")
		return reconcile.Result{}, err
	}

	err = r.ensureHumioNodeCertificates(context.TODO(), hc)
	if err != nil {
		r.Log.Error(err, "could not ensure we have certificates ready for Humio nodes")
		return reconcile.Result{}, err
	}

	err = r.ensureExtraKafkaConfigsConfigMap(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.ensureViewGroupPermissionsConfigMap(context.TODO(), hc)
	if err != nil {
		return reconcile.Result{}, err
	}

	result, err = r.ensurePersistentVolumeClaimsExist(context.TODO(), hc)
	if result != emptyResult || err != nil {
		return result, err
	}

	// Ensure pods exist. Will requeue if not all pods are created and ready
	if hc.Status.State == humiov1alpha1.HumioClusterStateBootstrapping {
		result, err = r.ensurePodsBootstrapped(context.TODO(), hc)
		if result != emptyResult || err != nil {
			return result, err
		}
	}

	// Wait for the sidecar to create the secret which contains the token used to authenticate with humio and then authenticate with it
	result, err = r.authWithSidecarToken(context.TODO(), hc, r.HumioClient.GetBaseURL(hc))
	if result != emptyResult || err != nil {
		return result, err
	}

	if hc.Status.State == humiov1alpha1.HumioClusterStateBootstrapping {
		err = r.setState(context.TODO(), humiov1alpha1.HumioClusterStateRunning, hc)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
	}

	defer func(ctx context.Context, hc *humiov1alpha1.HumioCluster) {
		pods, _ := kubernetes.ListPods(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
		r.setNodeCount(ctx, len(pods), hc)
	}(context.TODO(), hc)

	defer func(ctx context.Context, humioClient humio.Client, hc *humiov1alpha1.HumioCluster) {
		status, err := humioClient.Status()
		if err != nil {
			r.Log.Error(err, "unable to get status")
		}
		r.setVersion(ctx, status.Version, hc)
		r.setPod(ctx, hc)

	}(context.TODO(), r.HumioClient, hc)

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
	clusterController := humio.NewClusterController(r.Log, r.HumioClient)
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

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
}

func (r *HumioClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioCluster{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&v1beta1.Ingress{}).
		Complete(r)
}

// ensureExtraKafkaConfigsConfigMap creates a configmap containing configs specified in extraKafkaConfigs which will be mounted
// into the Humio container and pointed to by Humio's configuration option EXTRA_KAFKA_CONFIGS_FILE
func (r *HumioClusterReconciler) ensureExtraKafkaConfigsConfigMap(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	extraKafkaConfigsConfigMapData := extraKafkaConfigsOrDefault(hc)
	if extraKafkaConfigsConfigMapData == "" {
		return nil
	}
	_, err := kubernetes.GetConfigMap(ctx, r, extraKafkaConfigsConfigMapName(hc), hc.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			configMap := kubernetes.ConstructExtraKafkaConfigsConfigMap(
				extraKafkaConfigsConfigMapName(hc),
				extraKafkaPropertiesFilename,
				extraKafkaConfigsConfigMapData,
				hc.Name,
				hc.Namespace,
			)
			if err := controllerutil.SetControllerReference(hc, configMap, r.Scheme); err != nil {
				r.Log.Error(err, "could not set controller reference")
				return err
			}
			err = r.Create(ctx, configMap)
			if err != nil {
				r.Log.Error(err, "unable to create extra kafka configs configmap")
				return err
			}
			r.Log.Info(fmt.Sprintf("successfully created extra kafka configs configmap name %s", configMap.Name))
			humioClusterPrometheusMetrics.Counters.ConfigMapsCreated.Inc()
		}
	}
	return nil
}

// ensureViewGroupPermissionsConfigMap creates a configmap containing configs specified in viewGroupPermissions which will be mounted
// into the Humio container and used by Humio's configuration option READ_GROUP_PERMISSIONS_FROM_FILE
func (r *HumioClusterReconciler) ensureViewGroupPermissionsConfigMap(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	viewGroupPermissionsConfigMapData := viewGroupPermissionsOrDefault(hc)
	if viewGroupPermissionsConfigMapData == "" {
		viewGroupPermissionsConfigMap, err := kubernetes.GetConfigMap(ctx, r, viewGroupPermissionsConfigMapName(hc), hc.Namespace)
		if err == nil {
			err = r.Delete(ctx, viewGroupPermissionsConfigMap)
			if err != nil {
				r.Log.Error(err, "unable to delete view group permissions config map")
			}
		}
		return nil
	}
	_, err := kubernetes.GetConfigMap(ctx, r, viewGroupPermissionsConfigMapName(hc), hc.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			configMap := kubernetes.ConstructViewGroupPermissionsConfigMap(
				viewGroupPermissionsConfigMapName(hc),
				viewGroupPermissionsFilename,
				viewGroupPermissionsConfigMapData,
				hc.Name,
				hc.Namespace,
			)
			if err := controllerutil.SetControllerReference(hc, configMap, r.Scheme); err != nil {
				r.Log.Error(err, "could not set controller reference")
				return err
			}
			err = r.Create(ctx, configMap)
			if err != nil {
				r.Log.Error(err, "unable to create view group permissions configmap")
				return err
			}
			r.Log.Info(fmt.Sprintf("successfully created view group permissions configmap name %s", configMap.Name))
			humioClusterPrometheusMetrics.Counters.ConfigMapsCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureNoIngressesIfIngressNotEnabled(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	if hc.Spec.Ingress.Enabled {
		return reconcile.Result{}, nil
	}

	foundIngressList, err := kubernetes.ListIngresses(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
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
			r.Log.Info(fmt.Sprintf("deleting ingress with name %s", ingress.Name))
			err = r.Delete(ctx, &ingress)
			if err != nil {
				r.Log.Error(err, "could not delete ingress")
				return reconcile.Result{}, err
			}
		}
	}
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) ensureIngress(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
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
			r.Log.Error(err, "could not ensure nginx ingress")
			return err
		}
	default:
		return fmt.Errorf("ingress controller '%s' not supported", hc.Spec.Ingress.Controller)
	}

	return nil
}

// ensureNginxIngress creates the necessary ingress objects to expose the Humio cluster
// through NGINX ingress controller (https://kubernetes.github.io/ingress-nginx/).
func (r *HumioClusterReconciler) ensureNginxIngress(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	// Due to ingress-ngress relying on ingress object annotations to enable/disable/adjust certain features we create multiple ingress objects.
	ingresses := []*v1beta1.Ingress{
		constructGeneralIngress(hc),
		constructStreamingQueryIngress(hc),
		constructIngestIngress(hc),
		constructESIngestIngress(hc),
	}
	for _, desiredIngress := range ingresses {
		existingIngress, err := kubernetes.GetIngress(ctx, r, desiredIngress.Name, hc.Namespace)
		if err != nil {
			if errors.IsNotFound(err) {
				if err := controllerutil.SetControllerReference(hc, desiredIngress, r.Scheme); err != nil {
					r.Log.Error(err, "could not set controller reference")
					return err
				}
				for _, rule := range desiredIngress.Spec.Rules {
					if rule.Host == "" {
						continue
					}
				}
				err = r.Create(ctx, desiredIngress)
				if err != nil {
					r.Log.Error(err, "unable to create ingress")
					return err
				}
				r.Log.Info(fmt.Sprintf("successfully created ingress with name %s", desiredIngress.Name))
				humioClusterPrometheusMetrics.Counters.IngressesCreated.Inc()
				continue
			}
		}
		if !r.ingressesMatch(existingIngress, desiredIngress) {
			for _, rule := range desiredIngress.Spec.Rules {
				if rule.Host == "" {
					r.Log.Info(fmt.Sprintf("hostname not defined for ingress object, deleting ingress object with name %s", existingIngress.Name))
					err = r.Delete(ctx, existingIngress)
				}
			}
			r.Log.Info(fmt.Sprintf("ingress object already exists, there is a difference between expected vs existing, updating ingress object with name %s", desiredIngress.Name))
			existingIngress.Annotations = desiredIngress.Annotations
			existingIngress.Labels = desiredIngress.Labels
			existingIngress.Spec = desiredIngress.Spec
			err = r.Update(ctx, existingIngress)
			if err != nil {
				r.Log.Error(err, "could not update ingress")
				return err
			}
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureHumioPodPermissions(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	// Do not manage these resources if the HumioServiceAccountName is supplied. This implies the service account is managed
	// outside of the operator
	if hc.Spec.HumioServiceAccountName != "" {
		return nil
	}

	r.Log.Info("ensuring pod permissions")
	err := r.ensureServiceAccountExists(ctx, hc, humioServiceAccountNameOrDefault(hc), humioServiceAccountAnnotationsOrDefault(hc))
	if err != nil {
		r.Log.Error(err, "unable to ensure humio service account exists")
		return err
	}

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		err = r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hc, humioServiceAccountNameOrDefault(hc))
		if err != nil {
			r.Log.Error(err, "could not ensure SecurityContextConstraints contains ServiceAccount")
			return err
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureInitContainerPermissions(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	// We do not want to attach the init service account to the humio pod. Instead, only the init container should use this
	// service account. To do this, we can attach the service account directly to the init container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	err := r.ensureServiceAccountSecretExists(ctx, hc, initServiceAccountSecretName(hc), initServiceAccountNameOrDefault(hc))
	if err != nil {
		r.Log.Error(err, "unable to ensure init service account secret exists for HumioCluster")
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
		r.Log.Error(err, "unable to ensure init service account exists")
		return err
	}

	// This should be namespaced by the name, e.g. clustername-namespace-name
	// Required until https://github.com/kubernetes/kubernetes/issues/40610 is fixed
	err = r.ensureInitClusterRole(ctx, hc)
	if err != nil {
		r.Log.Error(err, "unable to ensure init cluster role exists")
		return err
	}

	// This should be namespaced by the name, e.g. clustername-namespace-name
	// Required until https://github.com/kubernetes/kubernetes/issues/40610 is fixed
	err = r.ensureInitClusterRoleBinding(ctx, hc)
	if err != nil {
		r.Log.Error(err, "unable to ensure init cluster role binding exists")
		return err
	}

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		err = r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hc, initServiceAccountNameOrDefault(hc))
		if err != nil {
			r.Log.Error(err, "could not ensure SecurityContextConstraints contains ServiceAccount")
			return err
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureAuthContainerPermissions(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	// We do not want to attach the auth service account to the humio pod. Instead, only the auth container should use this
	// service account. To do this, we can attach the service account directly to the auth container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	err := r.ensureServiceAccountSecretExists(ctx, hc, authServiceAccountSecretName(hc), authServiceAccountNameOrDefault(hc))
	if err != nil {
		r.Log.Error(err, "unable to ensure auth service account secret exists")
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
		r.Log.Error(err, "unable to ensure auth service account exists")
		return err
	}

	err = r.ensureAuthRole(ctx, hc)
	if err != nil {
		r.Log.Error(err, "unable to ensure auth role exists")
		return err
	}

	err = r.ensureAuthRoleBinding(ctx, hc)
	if err != nil {
		r.Log.Error(err, "unable to ensure auth role binding exists")
		return err
	}

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		err = r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hc, authServiceAccountNameOrDefault(hc))
		if err != nil {
			r.Log.Error(err, "could not ensure SecurityContextConstraints contains ServiceAccount")
			return err
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureSecurityContextConstraintsContainsServiceAccount(ctx context.Context, hc *humiov1alpha1.HumioCluster, serviceAccountName string) error {
	// TODO: Write unit/e2e test for this

	if !helpers.IsOpenShift() {
		return fmt.Errorf("updating SecurityContextConstraints are only suppoted when running on OpenShift")
	}

	// Get current SCC
	scc, err := openshift.GetSecurityContextConstraints(ctx, r)
	if err != nil {
		r.Log.Error(err, "unable to get details about SecurityContextConstraints")
		return err
	}

	// Give ServiceAccount access to SecurityContextConstraints if not already present
	usersEntry := fmt.Sprintf("system:serviceaccount:%s:%s", hc.Namespace, serviceAccountName)
	if !helpers.ContainsElement(scc.Users, usersEntry) {
		scc.Users = append(scc.Users, usersEntry)
		err = r.Update(ctx, scc)
		if err != nil {
			r.Log.Error(err, fmt.Sprintf("could not update SecurityContextConstraints %s to add ServiceAccount %s", scc.Name, serviceAccountName))
			return err
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureCleanupUsersInSecurityContextConstraints(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.IsOpenShift() {
		return nil
	}

	scc, err := openshift.GetSecurityContextConstraints(ctx, r)
	if err != nil {
		r.Log.Error(err, "unable to get details about SecurityContextConstraints")
		return err
	}

	for _, userEntry := range scc.Users {
		sccUserData := strings.Split(userEntry, ":")
		sccUserNamespace := sccUserData[2]
		sccUserName := sccUserData[3]

		_, err := kubernetes.GetServiceAccount(ctx, r, sccUserName, sccUserNamespace)
		if err == nil {
			// We found an existing service account
			continue
		}
		if errors.IsNotFound(err) {
			// If we have an error and it reflects that the service account does not exist, we remove the entry from the list.
			scc.Users = helpers.RemoveElement(scc.Users, fmt.Sprintf("system:serviceaccount:%s:%s", sccUserNamespace, sccUserName))
			err = r.Update(ctx, scc)
			if err != nil {
				r.Log.Error(err, "unable to update SecurityContextConstraints")
				return err
			}
		} else {
			r.Log.Error(err, "unable to get existing service account")
			return err
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureValidCAIssuer(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.Log.Info("cluster not configured to run with TLS, skipping")
		return nil
	}

	r.Log.Info("checking for an existing valid CA Issuer")
	validCAIssuer, err := validCAIssuer(ctx, r, hc.Namespace, hc.Name)
	if err != nil && !errors.IsNotFound(err) {
		r.Log.Error(err, "could not validate CA Issuer")
		return err
	}
	if validCAIssuer {
		r.Log.Info("found valid CA Issuer")
		return nil
	}

	var existingCAIssuer cmapi.Issuer
	err = r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, &existingCAIssuer)
	if err != nil {
		if errors.IsNotFound(err) {
			caIssuer := constructCAIssuer(hc)
			if err := controllerutil.SetControllerReference(hc, &caIssuer, r.Scheme); err != nil {
				r.Log.Error(err, "could not set controller reference")
				return err
			}
			// should only create it if it doesn't exist
			err = r.Create(ctx, &caIssuer)
			if err != nil {
				r.Log.Error(err, "could not create CA Issuer")
				return err
			}
			return nil
		}
		return err
	}

	return nil
}

func (r *HumioClusterReconciler) ensureValidCASecret(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.Log.Info("cluster not configured to run with TLS, skipping")
		return nil
	}

	r.Log.Info("checking for an existing CA secret")
	validCASecret, err := validCASecret(ctx, r, hc.Namespace, getCASecretName(hc))
	if validCASecret {
		r.Log.Info("found valid CA secret")
		return nil
	}
	if err != nil && !errors.IsNotFound(err) {
		r.Log.Error(err, "could not validate CA secret")
		return err
	}

	if useExistingCA(hc) {
		r.Log.Info("specified CA secret invalid")
		return fmt.Errorf("configured to use existing CA secret, but the CA secret invalid")
	}

	r.Log.Info("generating new CA certificate")
	ca, err := generateCACertificate()
	if err != nil {
		r.Log.Error(err, "could not generate new CA certificate")
		return err
	}

	r.Log.Info("persisting new CA certificate")
	caSecretData := map[string][]byte{
		"tls.crt": ca.Certificate,
		"tls.key": ca.Key,
	}
	caSecret := kubernetes.ConstructSecret(hc.Name, hc.Namespace, getCASecretName(hc), caSecretData)
	if err := controllerutil.SetControllerReference(hc, caSecret, r.Scheme); err != nil {
		r.Log.Error(err, "could not set controller reference")
		return err
	}
	err = r.Create(ctx, caSecret)
	if err != nil {
		r.Log.Error(err, "could not create secret with CA")
		return err
	}

	return nil
}

func (r *HumioClusterReconciler) ensureHumioClusterKeystoreSecret(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.Log.Info("cluster not configured to run with TLS, skipping")
		return nil
	}

	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      fmt.Sprintf("%s-keystore-passphrase", hc.Name),
	}, existingSecret)

	if errors.IsNotFound(err) {
		randomPass := kubernetes.RandomString()
		secretData := map[string][]byte{
			"passphrase": []byte(randomPass), // TODO: do we need separate passwords for different aspects?
		}
		secret := kubernetes.ConstructSecret(hc.Name, hc.Namespace, fmt.Sprintf("%s-keystore-passphrase", hc.Name), secretData)
		if err := controllerutil.SetControllerReference(hc, secret, r.Scheme); err != nil {
			r.Log.Error(err, "could not set controller reference")
			return err
		}
		err := r.Create(ctx, secret)
		if err != nil {
			r.Log.Error(err, "could not create secret")
			return err
		}
		return nil
	}

	return err
}

func (r *HumioClusterReconciler) ensureHumioClusterCACertBundle(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.Log.Info("cluster not configured to run with TLS, skipping")
		return nil
	}

	r.Log.Info("ensuring we have a CA cert bundle")
	existingCertificate := &cmapi.Certificate{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, existingCertificate)
	if errors.IsNotFound(err) {
		r.Log.Info("CA cert bundle doesn't exist, creating it now")
		cert := constructClusterCACertificateBundle(hc)
		if err := controllerutil.SetControllerReference(hc, &cert, r.Scheme); err != nil {
			r.Log.Error(err, "could not set controller reference")
			return err
		}
		err := r.Create(ctx, &cert)
		if err != nil {
			r.Log.Error(err, "could not create certificate")
			return err
		}
		return nil

	}

	return err
}

func (r *HumioClusterReconciler) ensureHumioNodeCertificates(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		r.Log.Info("cluster not configured to run with TLS, skipping")
		return nil
	}
	certificates, err := kubernetes.ListCertificates(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return err
	}
	existingNodeCertCount := 0
	for _, cert := range certificates {
		if strings.HasPrefix(cert.Name, fmt.Sprintf("%s-core", hc.Name)) {
			existingNodeCertCount++

			// Check if we should update the existing certificate
			certForHash := constructNodeCertificate(hc, "")

			// Keystores will always contain a new pointer when constructing a certificate.
			// To work around this, we override it to nil before calculating the hash,
			// if we do not do this, the hash will always be different.
			certForHash.Spec.Keystores = nil

			desiredCertificateHash := helpers.AsSHA256(certForHash)
			currentCertificateHash, _ := cert.Annotations[certHashAnnotation]
			if currentCertificateHash != desiredCertificateHash {
				r.Log.Info(fmt.Sprintf("node certificate %s doesn't have expected hash, got: %s, expected: %s",
					cert.Name, currentCertificateHash, desiredCertificateHash))
				currentCertificateNameSubstrings := strings.Split(cert.Name, "-")
				currentCertificateSuffix := currentCertificateNameSubstrings[len(currentCertificateNameSubstrings)-1]

				desiredCertificate := constructNodeCertificate(hc, currentCertificateSuffix)
				desiredCertificate.ResourceVersion = cert.ResourceVersion
				desiredCertificate.Annotations[certHashAnnotation] = desiredCertificateHash
				r.Log.Info(fmt.Sprintf("updating node TLS certificate with name %s", desiredCertificate.Name))
				if err := controllerutil.SetControllerReference(hc, &desiredCertificate, r.Scheme); err != nil {
					r.Log.Error(err, "could not set controller reference")
					return err
				}
				err = r.Update(ctx, &desiredCertificate)
				if err != nil {
					return err
				}
			}
		}
	}
	for i := existingNodeCertCount; i < nodeCountOrDefault(hc); i++ {
		certificate := constructNodeCertificate(hc, kubernetes.RandomString())

		certForHash := constructNodeCertificate(hc, "")
		// Keystores will always contain a new pointer when constructing a certificate.
		// To work around this, we override it to nil before calculating the hash,
		// if we do not do this, the hash will always be different.
		certForHash.Spec.Keystores = nil

		certificateHash := helpers.AsSHA256(certForHash)
		certificate.Annotations[certHashAnnotation] = certificateHash
		r.Log.Info(fmt.Sprintf("creating node TLS certificate with name %s", certificate.Name))
		if err := controllerutil.SetControllerReference(hc, &certificate, r.Scheme); err != nil {
			r.Log.Error(err, "could not set controller reference")
			return err
		}
		err := r.Create(ctx, &certificate)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureInitClusterRole(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	clusterRoleName := initClusterRoleName(hc)
	_, err := kubernetes.GetClusterRole(ctx, r, clusterRoleName)
	if err != nil {
		if errors.IsNotFound(err) {
			clusterRole := kubernetes.ConstructInitClusterRole(clusterRoleName, hc.Name)
			// TODO: We cannot use controllerutil.SetControllerReference() as ClusterRole is cluster-wide and owner is namespaced.
			// We probably need another way to ensure we clean them up. Perhaps we can use finalizers?
			err = r.Create(ctx, clusterRole)
			if err != nil {
				r.Log.Error(err, "unable to create init cluster role")
				return err
			}
			r.Log.Info(fmt.Sprintf("successfully created init cluster role %s", clusterRoleName))
			humioClusterPrometheusMetrics.Counters.ClusterRolesCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureAuthRole(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	roleName := authRoleName(hc)
	_, err := kubernetes.GetRole(ctx, r, roleName, hc.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			role := kubernetes.ConstructAuthRole(roleName, hc.Name, hc.Namespace)
			if err := controllerutil.SetControllerReference(hc, role, r.Scheme); err != nil {
				r.Log.Error(err, "could not set controller reference")
				return err
			}
			err = r.Create(ctx, role)
			if err != nil {
				r.Log.Error(err, "unable to create auth role")
				return err
			}
			r.Log.Info(fmt.Sprintf("successfully created auth role %s", roleName))
			humioClusterPrometheusMetrics.Counters.RolesCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureInitClusterRoleBinding(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	clusterRoleBindingName := initClusterRoleBindingName(hc)
	_, err := kubernetes.GetClusterRoleBinding(ctx, r, clusterRoleBindingName)
	if err != nil {
		if errors.IsNotFound(err) {
			clusterRole := kubernetes.ConstructClusterRoleBinding(
				clusterRoleBindingName,
				initClusterRoleName(hc),
				hc.Name,
				hc.Namespace,
				initServiceAccountNameOrDefault(hc),
			)
			// TODO: We cannot use controllerutil.SetControllerReference() as ClusterRoleBinding is cluster-wide and owner is namespaced.
			// We probably need another way to ensure we clean them up. Perhaps we can use finalizers?
			err = r.Create(ctx, clusterRole)
			if err != nil {
				r.Log.Error(err, "unable to create init cluster role binding")
				return err
			}
			r.Log.Info(fmt.Sprintf("successfully created init cluster role binding %s", clusterRoleBindingName))
			humioClusterPrometheusMetrics.Counters.ClusterRoleBindingsCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureAuthRoleBinding(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	roleBindingName := authRoleBindingName(hc)
	_, err := kubernetes.GetRoleBinding(ctx, r, roleBindingName, hc.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			roleBinding := kubernetes.ConstructRoleBinding(
				roleBindingName,
				authRoleName(hc),
				hc.Name,
				hc.Namespace,
				authServiceAccountNameOrDefault(hc),
			)
			if err := controllerutil.SetControllerReference(hc, roleBinding, r.Scheme); err != nil {
				r.Log.Error(err, "could not set controller reference")
				return err
			}
			err = r.Create(ctx, roleBinding)
			if err != nil {
				r.Log.Error(err, "unable to create auth role binding")
				return err
			}
			r.Log.Info(fmt.Sprintf("successfully created auth role binding %s", roleBindingName))
			humioClusterPrometheusMetrics.Counters.RoleBindingsCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureServiceAccountExists(ctx context.Context, hc *humiov1alpha1.HumioCluster, serviceAccountName string, serviceAccountAnnotations map[string]string) error {
	_, err := kubernetes.GetServiceAccount(ctx, r, serviceAccountName, hc.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			serviceAccount := kubernetes.ConstructServiceAccount(serviceAccountName, hc.Name, hc.Namespace, serviceAccountAnnotations)
			if err := controllerutil.SetControllerReference(hc, serviceAccount, r.Scheme); err != nil {
				r.Log.Error(err, "could not set controller reference")
				return err
			}
			err = r.Create(ctx, serviceAccount)
			if err != nil {
				r.Log.Error(err, fmt.Sprintf("unable to create service account %s", serviceAccountName))
				return err
			}
			r.Log.Info(fmt.Sprintf("successfully created service account %s", serviceAccountName))
			humioClusterPrometheusMetrics.Counters.ServiceAccountsCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureServiceAccountSecretExists(ctx context.Context, hc *humiov1alpha1.HumioCluster, serviceAccountSecretName, serviceAccountName string) error {
	foundServiceAccountSecretsList, err := kubernetes.ListSecrets(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForSecret(hc.Name, serviceAccountSecretName))
	if err != nil {
		r.Log.Error(err, "unable list secrets")
		return err
	}

	if len(foundServiceAccountSecretsList) == 0 {
		secret := kubernetes.ConstructServiceAccountSecret(hc.Name, hc.Namespace, serviceAccountSecretName, serviceAccountName)
		if err := controllerutil.SetControllerReference(hc, secret, r.Scheme); err != nil {
			r.Log.Error(err, "could not set controller reference")
			return err
		}
		err = r.Create(ctx, secret)
		if err != nil {
			r.Log.Error(err, fmt.Sprintf("unable to create service account secret %s", serviceAccountSecretName))
			return err
		}
		r.Log.Info(fmt.Sprintf("successfully created service account secret %s", serviceAccountSecretName))
		humioClusterPrometheusMetrics.Counters.ServiceAccountSecretsCreated.Inc()
	}

	return nil
}

func (r *HumioClusterReconciler) ensureLabels(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring labels")
	cluster, err := r.HumioClient.GetClusters()
	if err != nil {
		r.Log.Error(err, "failed to get clusters")
		return err
	}

	foundPodList, err := kubernetes.ListPods(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.Log.Error(err, "failed to list pods")
		return err
	}

	pvcList, err := r.pvcList(hc)
	if err != nil {
		r.Log.Error(err, "failed to list pvcs to assign labels")
		return err
	}

	for _, pod := range foundPodList {
		// Skip pods that already have a label. Check that the pvc also has the label if applicable
		if kubernetes.LabelListContainsLabel(pod.GetLabels(), kubernetes.NodeIdLabelName) {
			if pvcsEnabled(hc) {
				err := r.ensurePvcLabels(ctx, hc, pod, pvcList)
				if err != nil {
					r.Log.Error(err, "could not ensure pvc labels")
					return err
				}
			}
			continue
		}
		// If pod does not have an IP yet it is probably pending
		if pod.Status.PodIP == "" {
			r.Log.Info(fmt.Sprintf("not setting labels for pod %s because it is in state %s", pod.Name, pod.Status.Phase))
			continue
		}
		r.Log.Info(fmt.Sprintf("setting labels for nodes: %#+v", cluster.Nodes))
		for _, node := range cluster.Nodes {
			if node.Uri == fmt.Sprintf("http://%s:%d", pod.Status.PodIP, humioPort) {
				labels := kubernetes.LabelsForPod(hc.Name, node.Id)
				r.Log.Info(fmt.Sprintf("setting labels for pod %s, labels=%v", pod.Name, labels))
				pod.SetLabels(labels)
				if err := r.Update(ctx, &pod); err != nil {
					r.Log.Error(err, fmt.Sprintf("failed to update labels on pod %s", pod.Name))
					return err
				}
				if pvcsEnabled(hc) {
					err = r.ensurePvcLabels(ctx, hc, pod, pvcList)
					if err != nil {
						r.Log.Error(err, "could not ensure pvc labels")
						return err
					}
				}
			}
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensurePvcLabels(ctx context.Context, hc *humiov1alpha1.HumioCluster, pod corev1.Pod, pvcList []corev1.PersistentVolumeClaim) error {
	pvc, err := findPvcForPod(pvcList, pod)
	if err != nil {
		r.Log.Error(err, "failed to get pvc for pod to assign labels")
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
	r.Log.Info(fmt.Sprintf("setting labels for pvc %s, labels=%v", pvc.Name, labels))
	pvc.SetLabels(labels)
	if err := r.Update(ctx, &pvc); err != nil {
		r.Log.Error(err, fmt.Sprintf("failed to update labels on pvc %s", pod.Name))
		return err
	}
	return nil
}

func (r *HumioClusterReconciler) ensurePartitionsAreBalanced(humioClusterController humio.ClusterController, hc *humiov1alpha1.HumioCluster) error {
	if !hc.Spec.AutoRebalancePartitions {
		r.Log.Info("partition auto-rebalancing not enabled, skipping")
		return nil
	}
	partitionsBalanced, err := humioClusterController.AreStoragePartitionsBalanced(hc)
	if err != nil {
		r.Log.Error(err, "unable to check if storage partitions are balanced")
		return err
	}
	if !partitionsBalanced {
		r.Log.Info("storage partitions are not balanced. Balancing now")
		err = humioClusterController.RebalanceStoragePartitions(hc)
		if err != nil {
			r.Log.Error(err, "failed to balance storage partitions")
			return err
		}
	}
	partitionsBalanced, err = humioClusterController.AreIngestPartitionsBalanced(hc)
	if err != nil {
		r.Log.Error(err, "unable to check if ingest partitions are balanced")
		return err
	}
	if !partitionsBalanced {
		r.Log.Info("ingest partitions are not balanced. Balancing now")
		err = humioClusterController.RebalanceIngestPartitions(hc)
		if err != nil {
			r.Log.Error(err, "failed to balance ingest partitions")
			return err
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureServiceExists(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring service")
	_, err := kubernetes.GetService(ctx, r, hc.Name, hc.Namespace)
	if errors.IsNotFound(err) {
		service := constructService(hc)
		if err := controllerutil.SetControllerReference(hc, service, r.Scheme); err != nil {
			r.Log.Error(err, "could not set controller reference")
			return err
		}
		err = r.Create(ctx, service)
		if err != nil {
			r.Log.Error(err, "unable to create service for HumioCluster")
			return err
		}
	}
	return nil
}

// cleanupUnusedTLSCertificates finds all existing per-node certificates for a specific HumioCluster
// and cleans them up if we have no use for them anymore.
func (r *HumioClusterReconciler) cleanupUnusedTLSSecrets(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	if !helpers.UseCertManager() {
		r.Log.Info("cert-manager not available, skipping")
		return reconcile.Result{}, nil
	}

	// because these secrets are created by cert-manager we cannot use our typical label selector
	foundSecretList, err := kubernetes.ListSecrets(ctx, r, hc.Namespace, client.MatchingLabels{})
	if err != nil {
		r.Log.Error(err, "unable to list secrets")
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
					r.Log.Info(fmt.Sprintf("TLS is not enabled for cluster, removing unused secret: %s", secret.Name))
					err := r.Delete(ctx, &secret)
					if err != nil {
						return reconcile.Result{}, err
					}
				}
			}
		}

		commonName, found := secret.Annotations[cmapi.CommonNameAnnotationKey]
		if !found || commonName != "" {
			continue
		}
		issuerKind, found := secret.Annotations[cmapi.IssuerKindAnnotationKey]
		if !found || issuerKind != cmapi.IssuerKind {
			continue
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
					r.Log.Error(err, "unable to determine if secret is in use")
					return reconcile.Result{}, err
				}
			}
			if !inUse {
				r.Log.Info(fmt.Sprintf("deleting secret %s", secret.Name))
				err = r.Delete(ctx, &secret)
				if err != nil {
					r.Log.Error(err, fmt.Sprintf("could not delete secret %s", secret.Name))
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
func (r *HumioClusterReconciler) cleanupUnusedTLSCertificates(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	if !helpers.UseCertManager() {
		r.Log.Info("cert-manager not available, skipping")
		return reconcile.Result{}, nil
	}

	foundCertificateList, err := kubernetes.ListCertificates(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.Log.Error(err, "unable to list certificates")
		return reconcile.Result{}, err
	}
	if len(foundCertificateList) == 0 {
		return reconcile.Result{}, nil
	}

	for _, certificate := range foundCertificateList {
		// only consider secrets not already being deleted
		if certificate.DeletionTimestamp == nil {
			if len(certificate.OwnerReferences) == 0 {
				continue
			}
			if certificate.OwnerReferences[0].Kind != "HumioCluster" {
				continue
			}
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
					r.Log.Error(err, "unable to determine if certificate is in use")
					return reconcile.Result{}, err
				}
			}
			if !inUse {
				r.Log.Info(fmt.Sprintf("deleting certificate %s", certificate.Name))
				err = r.Delete(ctx, &certificate)
				if err != nil {
					r.Log.Error(err, fmt.Sprintf("could not delete certificate %s", certificate.Name))
					return reconcile.Result{}, err
				}
				return reconcile.Result{Requeue: true}, nil
			}
		}
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) tlsCertSecretInUse(ctx context.Context, secretNamespace, secretName string) (bool, error) {
	pod := &corev1.Pod{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: secretNamespace,
		Name:      secretName,
	}, pod)

	if errors.IsNotFound(err) {
		return false, nil
	}
	return true, err
}

func (r *HumioClusterReconciler) getInitServiceAccountSecretName(ctx context.Context, hc *humiov1alpha1.HumioCluster) (string, error) {
	foundInitServiceAccountSecretsList, err := kubernetes.ListSecrets(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForSecret(hc.Name, initServiceAccountSecretName(hc)))
	if err != nil {
		return "", err
	}
	if len(foundInitServiceAccountSecretsList) == 0 {
		return "", nil
	}
	if len(foundInitServiceAccountSecretsList) > 1 {
		return "", fmt.Errorf("found more than one init service account secret")
	}
	return foundInitServiceAccountSecretsList[0].Name, nil
}

func (r *HumioClusterReconciler) getAuthServiceAccountSecretName(ctx context.Context, hc *humiov1alpha1.HumioCluster) (string, error) {
	foundAuthServiceAccountNameSecretsList, err := kubernetes.ListSecrets(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForSecret(hc.Name, authServiceAccountSecretName(hc)))
	if err != nil {
		return "", err
	}
	if len(foundAuthServiceAccountNameSecretsList) == 0 {
		return "", nil
	}
	if len(foundAuthServiceAccountNameSecretsList) > 1 {
		return "", fmt.Errorf("found more than one auth service account secret")
	}
	return foundAuthServiceAccountNameSecretsList[0].Name, nil
}

func (r *HumioClusterReconciler) ensureHumioServiceAccountAnnotations(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	// Don't change the service account annotations if the service account is not managed by the operator
	if hc.Spec.HumioServiceAccountName != "" {
		return reconcile.Result{}, nil
	}
	serviceAccountName := humioServiceAccountNameOrDefault(hc)
	serviceAccountAnnotations := humioServiceAccountAnnotationsOrDefault(hc)

	r.Log.Info(fmt.Sprintf("ensuring service account %s annotations", serviceAccountName))
	existingServiceAccount, err := kubernetes.GetServiceAccount(ctx, r, serviceAccountName, hc.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		r.Log.Error(err, fmt.Sprintf("failed to get service account %s", serviceAccountName))
		return reconcile.Result{}, err
	}

	serviceAccount := kubernetes.ConstructServiceAccount(serviceAccountName, hc.Name, hc.Namespace, serviceAccountAnnotations)
	if !reflect.DeepEqual(existingServiceAccount.Annotations, serviceAccount.Annotations) {
		r.Log.Info(fmt.Sprintf("service account annotations do not match: annotations %s, got %s. updating service account %s",
			helpers.MapToString(serviceAccount.Annotations), helpers.MapToString(existingServiceAccount.Annotations), existingServiceAccount.Name))
		existingServiceAccount.Annotations = serviceAccount.Annotations
		err = r.Update(ctx, existingServiceAccount)
		if err != nil {
			r.Log.Error(err, fmt.Sprintf("could not update service account %s", existingServiceAccount.Name))
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
func (r *HumioClusterReconciler) ensureMismatchedPodsAreDeleted(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	foundPodList, err := kubernetes.ListPods(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return reconcile.Result{}, err
	}

	// if we do not have any pods running we have nothing to delete
	if len(foundPodList) == 0 {
		return reconcile.Result{}, nil
	}

	var podBeingDeleted bool
	var waitingOnReadyPods bool
	r.Log.Info("ensuring mismatching pods are deleted")

	// It's not necessary to have real attachments here since we are only using them to get the desired state of the pod
	// which sanitizes the attachments in podSpecAsSHA256().
	attachments := &podAttachments{}

	// If we allow a rolling update, then don't take down more than one pod at a time.
	// Check the number of ready pods. if we have already deleted a pod, then the ready count will less than expected,
	// but we must continue with reconciliation so the pod may be created later in the reconciliation.
	// If we're doing a non-rolling update (recreate), then we can take down all the pods without waiting, but we will
	// wait until all the pods are ready before changing the cluster state back to Running.
	podsReadyCount, podsNotReadyCount := r.podsReady(foundPodList)
	if podsReadyCount < nodeCountOrDefault(hc) || podsNotReadyCount > 0 {
		waitingOnReadyPods = true
		r.Log.Info(fmt.Sprintf("there are %d/%d humio pods that are ready", podsReadyCount, nodeCountOrDefault(hc)))
	}

	if (r.getHumioClusterPodRestartPolicy(hc) == PodRestartPolicyRolling && !waitingOnReadyPods) ||
		r.getHumioClusterPodRestartPolicy(hc) == PodRestartPolicyRecreate {
		desiredLifecycleState, err := r.getPodDesiredLifecycleState(hc, foundPodList, attachments)
		if err != nil {
			r.Log.Error(err, "got error when getting pod desired lifecycle")
			return reconcile.Result{}, err
		}
		// If we are currently deleting pods, then check if the cluster state is Running. If it is, then change to an
		// appropriate state depending on the restart policy.
		// If the cluster state is set as per the restart policy:
		// 	 PodRestartPolicyRecreate == HumioClusterStateUpgrading
		// 	 PodRestartPolicyRolling == HumioClusterStateRestarting
		if desiredLifecycleState.delete {
			if hc.Status.State == humiov1alpha1.HumioClusterStateRunning {
				if desiredLifecycleState.restartPolicy == PodRestartPolicyRecreate {
					if err = r.setState(ctx, humiov1alpha1.HumioClusterStateUpgrading, hc); err != nil {
						r.Log.Error(err, fmt.Sprintf("failed to set state to %s", humiov1alpha1.HumioClusterStateUpgrading))
					}
					if revision, err := r.incrementHumioClusterPodRevision(ctx, hc, PodRestartPolicyRecreate); err != nil {
						r.Log.Error(err, fmt.Sprintf("failed to increment pod revision to %d", revision))
					}
				}
				if desiredLifecycleState.restartPolicy == PodRestartPolicyRolling {
					if err = r.setState(ctx, humiov1alpha1.HumioClusterStateRestarting, hc); err != nil {
						r.Log.Error(err, fmt.Sprintf("failed to set state to %s", humiov1alpha1.HumioClusterStateRestarting))
					}
					if revision, err := r.incrementHumioClusterPodRevision(ctx, hc, PodRestartPolicyRolling); err != nil {
						r.Log.Error(err, fmt.Sprintf("failed to increment pod revision to %d", revision))
					}
				}
			}
			r.Log.Info(fmt.Sprintf("deleting pod %s", desiredLifecycleState.pod.Name))
			podBeingDeleted = true
			err = r.Delete(ctx, &desiredLifecycleState.pod)
			if err != nil {
				r.Log.Error(err, fmt.Sprintf("could not delete pod %s", desiredLifecycleState.pod.Name))
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
		if hc.Status.State == humiov1alpha1.HumioClusterStateRestarting || hc.Status.State == humiov1alpha1.HumioClusterStateUpgrading {
			r.Log.Info(fmt.Sprintf("no longer deleting pods. changing cluster state from %s to %s", hc.Status.State, humiov1alpha1.HumioClusterStateRunning))
			if err = r.setState(ctx, humiov1alpha1.HumioClusterStateRunning, hc); err != nil {
				r.Log.Error(err, fmt.Sprintf("failed to set state to %s", humiov1alpha1.HumioClusterStateRunning))
			}
		}
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) ingressesMatch(ingress *v1beta1.Ingress, desiredIngress *v1beta1.Ingress) bool {
	// Kubernetes 1.18 introduced a new field, PathType. For older versions PathType is returned as nil,
	// so we explicitly set the value before comparing ingress objects.
	// When minimum supported Kubernetes version is 1.18, we can drop this.
	pathTypeImplementationSpecific := v1beta1.PathTypeImplementationSpecific
	for ruleIdx, rule := range ingress.Spec.Rules {
		for pathIdx := range rule.HTTP.Paths {
			if ingress.Spec.Rules[ruleIdx].HTTP.Paths[pathIdx].PathType == nil {
				ingress.Spec.Rules[ruleIdx].HTTP.Paths[pathIdx].PathType = &pathTypeImplementationSpecific
			}
		}
	}

	if !reflect.DeepEqual(ingress.Spec, desiredIngress.Spec) {
		r.Log.Info(fmt.Sprintf("ingress specs do not match: got %+v, wanted %+v", ingress.Spec, desiredIngress.Spec))
		return false
	}

	if !reflect.DeepEqual(ingress.Annotations, desiredIngress.Annotations) {
		r.Log.Info(fmt.Sprintf("ingress annotations do not match: got %+v, wanted %+v", ingress.Annotations, desiredIngress.Annotations))
		return false
	}
	return true
}

// check that other pods, if they exist, are in a ready state
func (r *HumioClusterReconciler) ensurePodsBootstrapped(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	r.Log.Info("ensuring pods are bootstrapped")
	foundPodList, err := kubernetes.ListPods(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.Log.Error(err, "failed to list pods")
		return reconcile.Result{}, err
	}
	r.Log.Info(fmt.Sprintf("found %d pods", len(foundPodList)))

	podsReadyCount, podsNotReadyCount := r.podsReady(foundPodList)
	if podsReadyCount == nodeCountOrDefault(hc) {
		r.Log.Info("all humio pods are reporting ready")
		return reconcile.Result{}, nil
	}

	if podsNotReadyCount > 0 {
		r.Log.Info(fmt.Sprintf("there are %d humio pods that are not ready. all humio pods must report ready before reconciliation can continue", podsNotReadyCount))
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, nil
	}

	r.Log.Info(fmt.Sprintf("pod ready count is %d, while desired node count is %d", podsReadyCount, nodeCountOrDefault(hc)))
	if podsReadyCount < nodeCountOrDefault(hc) {
		attachments, err := r.newPodAttachments(ctx, hc, foundPodList)
		if err != nil {
			r.Log.Error(err, "failed to get pod attachments")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		err = r.createPod(ctx, hc, attachments)
		if err != nil {
			r.Log.Error(err, "unable to create pod")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		humioClusterPrometheusMetrics.Counters.PodsCreated.Inc()

		// check that we can list the new pod
		// this is to avoid issues where the requeue is faster than kubernetes
		if err := r.waitForNewPod(hc, len(foundPodList)+1); err != nil {
			r.Log.Error(err, "failed to validate new pod")
			return reconcile.Result{}, err
		}

		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) ensurePodsExist(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	foundPodList, err := kubernetes.ListPods(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.Log.Error(err, "failed to list pods")
		return reconcile.Result{}, err
	}

	if len(foundPodList) < nodeCountOrDefault(hc) {
		attachments, err := r.newPodAttachments(ctx, hc, foundPodList)
		if err != nil {
			r.Log.Error(err, "failed to get pod attachments")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		err = r.createPod(ctx, hc, attachments)
		if err != nil {
			r.Log.Error(err, "unable to create pod")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		humioClusterPrometheusMetrics.Counters.PodsCreated.Inc()

		// check that we can list the new pod
		// this is to avoid issues where the requeue is faster than kubernetes
		if err := r.waitForNewPod(hc, len(foundPodList)+1); err != nil { // TODO: We often end in situations where we expect one more than we have, causing this to timeout after 30 seconds. This doesn't happen during bootstrapping.
			r.Log.Error(err, "failed to validate new pod")
			return reconcile.Result{}, err
		}

		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) ensurePersistentVolumeClaimsExist(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	if !pvcsEnabled(hc) {
		r.Log.Info("pvcs are disabled. skipping")
		return reconcile.Result{}, nil
	}

	r.Log.Info("ensuring pvcs")
	foundPersistentVolumeClaims, err := kubernetes.ListPersistentVolumeClaims(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	r.Log.Info(fmt.Sprintf("found %d pvcs", len(foundPersistentVolumeClaims)))

	if err != nil {
		r.Log.Error(err, "failed to list pvcs")
		return reconcile.Result{}, err
	}

	if len(foundPersistentVolumeClaims) < nodeCountOrDefault(hc) {
		r.Log.Info(fmt.Sprintf("pvc count of %d is less than %d. adding more", len(foundPersistentVolumeClaims), nodeCountOrDefault(hc)))
		pvc := constructPersistentVolumeClaim(hc)
		pvc.Annotations[pvcHashAnnotation] = helpers.AsSHA256(pvc.Spec)
		if err := controllerutil.SetControllerReference(hc, pvc, r.Scheme); err != nil {
			r.Log.Error(err, "could not set controller reference")
			return reconcile.Result{}, err
		}
		err = r.Create(ctx, pvc)
		if err != nil {
			r.Log.Error(err, "unable to create pvc")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
		}
		r.Log.Info(fmt.Sprintf("successfully created pvc %s for HumioCluster %s", pvc.Name, hc.Name))
		humioClusterPrometheusMetrics.Counters.PvcsCreated.Inc()

		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, nil
	}

	// TODO: what should happen if we have more pvcs than are expected?
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) authWithSidecarToken(ctx context.Context, hc *humiov1alpha1.HumioCluster, url string) (reconcile.Result, error) {
	adminTokenSecretName := fmt.Sprintf("%s-%s", hc.Name, kubernetes.ServiceTokenSecretNameSuffix)
	existingSecret, err := kubernetes.GetSecret(ctx, r, adminTokenSecretName, hc.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info(fmt.Sprintf("waiting for sidecar to populate secret %s", adminTokenSecretName))
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 10}, nil
		}
	}

	humioAPIConfig := &humioapi.Config{
		Address: url,
		Token:   string(existingSecret.Data["token"]),
	}

	// Get CA
	if helpers.TLSEnabled(hc) {
		existingCABundle, err := kubernetes.GetSecret(ctx, r, constructClusterCACertificateBundle(hc).Spec.SecretName, hc.Namespace)
		if errors.IsNotFound(err) {
			r.Log.Info("waiting for secret with CA bundle")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 10}, nil
		}
		if err != nil {
			r.Log.Error(err, "unable to obtain CA certificate")
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 10}, err
		}
		humioAPIConfig.CACertificate = existingCABundle.Data["ca.crt"]
	}

	// Either authenticate or re-authenticate with the persistent token
	return reconcile.Result{}, r.HumioClient.Authenticate(humioAPIConfig)
}

// TODO: there is no need for this. We should instead change this to a get method where we return the list of env vars
// including the defaults
func envVarList(hc *humiov1alpha1.HumioCluster) []corev1.EnvVar {
	setEnvironmentVariableDefaults(hc)
	return hc.Spec.EnvironmentVariables
}

func (r *HumioClusterReconciler) pvcList(hc *humiov1alpha1.HumioCluster) ([]corev1.PersistentVolumeClaim, error) {
	if pvcsEnabled(hc) {
		return kubernetes.ListPersistentVolumeClaims(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	}
	return []corev1.PersistentVolumeClaim{}, nil
}
