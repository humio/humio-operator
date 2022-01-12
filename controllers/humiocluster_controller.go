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

	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"github.com/humio/humio-operator/pkg/openshift"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"github.com/humio/humio-operator/pkg/humio"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioClusterReconciler reconciles a HumioCluster object
type HumioClusterReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioclusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioclusters/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=pods,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=core,resources=services,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=core,resources=services/finalizers,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=core,resources=endpoints,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=create;delete;get;list;patch;update;watch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingress,verbs=create;delete;get;list;patch;update;watch

func (r *HumioClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioCluster")

	// Fetch the HumioCluster
	hc := &humiov1alpha1.HumioCluster{}
	if err := r.Get(ctx, req.NamespacedName, hc); err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	var humioNodePools []*HumioNodePool
	humioNodePools = append(humioNodePools, NewHumioNodeManagerFromHumioCluster(hc))
	for _, nodePool := range hc.Spec.NodePools {
		humioNodePools = append(humioNodePools, NewHumioNodeManagerFromHumioNodePool(hc, &nodePool))
	}

	emptyResult := reconcile.Result{}

	defer func(ctx context.Context, humioClient humio.Client, hc *humiov1alpha1.HumioCluster) {
		_, _ = r.updateStatus(r.Client.Status(), hc, statusOptions().
			withObservedGeneration(hc.GetGeneration()))
	}(ctx, r.HumioClient, hc)

	for _, pool := range humioNodePools {
		if err := r.setImageFromSource(context.TODO(), pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withNodePoolState(humiov1alpha1.HumioClusterStateConfigError, pool.GetNodePoolName()))
		}
	}

	for _, pool := range humioNodePools {
		if err := r.ensureValidHumioVersion(pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withNodePoolState(humiov1alpha1.HumioClusterStateConfigError, pool.GetNodePoolName()))
		}
	}

	if err := r.ensureValidStorageConfiguration(hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}

	// Ensure we have a valid CA certificate to configure intra-cluster communication.
	// Because generating the CA can take a while, we do this before we start tearing down mismatching pods
	if err := r.ensureValidCASecret(ctx, hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}

	if err := r.ensureHeadlessServiceExists(ctx, hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}

	if err := r.ensureNodePoolSpecificResourcesHaveLabelWithNodePoolName(ctx, humioNodePools[0]); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}

	for _, pool := range humioNodePools {
		if r.nodePoolAllowsMaintenanceOperations(hc, pool, humioNodePools) {
			// TODO: result should be controlled and returned by the status
			// Ensure pods that does not run the desired version are deleted.
			result, err := r.ensureMismatchedPodsAreDeleted(ctx, hc, pool)
			if result != emptyResult || err != nil {
				return result, err
			}
		}
	}

	if allServiceAccountsExists, err := r.validateUserDefinedServiceAccountsExists(ctx, hc); err != nil {
		if !allServiceAccountsExists {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withState(humiov1alpha1.HumioClusterStateConfigError))
		}
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}

	for _, pool := range humioNodePools {
		if err := r.validateInitialPodSpec(pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withNodePoolState(humiov1alpha1.HumioClusterStateConfigError, pool.GetNodePoolName()))
		}
	}

	if err := r.validateNodeCount(hc, humioNodePools); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}

	if err := r.ensureLicenseIsValid(hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}

	if hc.Status.State == "" {
		// TODO: migrate to updateStatus()
		err := r.setState(ctx, humiov1alpha1.HumioClusterStateRunning, hc)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set cluster state")
		}
	}

	for _, pool := range humioNodePools {
		if clusterState, err := r.ensurePodRevisionAnnotation(hc, pool); err != nil || clusterState != hc.Status.State {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withNodePoolState(clusterState, pool.GetNodePoolName()))
		}
	}

	for _, pool := range humioNodePools {
		if issueRestart, err := r.ensureHumioServiceAccountAnnotations(ctx, pool); err != nil || issueRestart {
			opts := statusOptions()
			if issueRestart {
				_, err = r.incrementHumioClusterPodRevision(ctx, hc, pool)
			}
			if err != nil {
				opts.withMessage(err.Error())
			}
			return r.updateStatus(r.Client.Status(), hc, opts.withState(hc.Status.State))
		}
	}

	for _, pool := range humioNodePools {
		if err := r.ensureService(ctx, hc, pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}

	for _, pool := range humioNodePools {
		if err := r.ensureHumioPodPermissions(ctx, hc, pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}

	for _, pool := range humioNodePools {
		if err := r.ensureInitContainerPermissions(ctx, hc, pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}

	for _, pool := range humioNodePools {
		if err := r.ensureAuthContainerPermissions(ctx, hc, pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}

	// Ensure the users in the SCC are cleaned up.
	// This cleanup is only called as part of reconciling HumioCluster objects,
	// this means that you can end up with the SCC listing the service accounts
	// used for the last cluster to be deleted, in the case that all HumioCluster's are removed.
	// TODO: Determine if we should move this to a finalizer to fix the situation described above.
	if err := r.ensureCleanupUsersInSecurityContextConstraints(ctx); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}

	// Ensure the CA Issuer is valid/ready
	if err := r.ensureValidCAIssuer(ctx, hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}
	// Ensure we have a k8s secret holding the ca.crt
	// This can be used in reverse proxies talking to Humio.
	if err := r.ensureHumioClusterCACertBundle(ctx, hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}

	if err := r.ensureHumioClusterKeystoreSecret(ctx, hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}

	for _, pool := range humioNodePools {
		if err := r.ensureHumioNodeCertificates(ctx, hc, pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}

	for _, pool := range humioNodePools {
		if err := r.ensureExtraKafkaConfigsConfigMap(ctx, hc, pool); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}

	if err := r.ensureViewGroupPermissionsConfigMap(ctx, hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}

	for _, pool := range humioNodePools {
		if err := r.ensurePersistentVolumeClaimsExist(ctx, hc, pool); err != nil {
			opts := statusOptions()
			if hc.Status.State != humiov1alpha1.HumioClusterStateRestarting && hc.Status.State != humiov1alpha1.HumioClusterStateUpgrading {
				opts.withNodePoolState(humiov1alpha1.HumioClusterStatePending, pool.GetNodePoolName())
			}
			return r.updateStatus(r.Client.Status(), hc, opts.
				withMessage(err.Error()))
		}
	}

	// TODO: result should be controlled and returned by the status
	for _, pool := range humioNodePools {
		if result, err := r.ensurePodsExist(ctx, hc, pool); result != emptyResult || err != nil {
			if err != nil {
				_, _ = r.updateStatus(r.Client.Status(), hc, statusOptions().
					withMessage(err.Error()))
			}
			return result, err
		}
	}

	// TODO: result should be controlled and returned by the status
	if len(r.nodePoolsInMaintenance(hc, humioNodePools)) == 0 {
		if result, err := r.ensureLicense(ctx, hc, req); result != emptyResult || err != nil {
			if err != nil {
				_, _ = r.updateStatus(r.Client.Status(), hc, statusOptions().
					withMessage(err.Error()))
			}
			// Usually if we fail to get the license, that means the cluster is not up. So wait a bit longer than usual to retry
			return reconcile.Result{RequeueAfter: time.Second * 15}, nil
		}
	}

	cluster, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(r.logErrorAndReturn(err, "unable to obtain humio client config").Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}

	defer func(ctx context.Context, humioClient humio.Client, hc *humiov1alpha1.HumioCluster) {
		opts := statusOptions()
		if hc.Status.State == humiov1alpha1.HumioClusterStateRunning {
			status, err := humioClient.Status(cluster.Config(), req)
			if err != nil {
				r.Log.Error(err, "unable to get cluster status")
			}
			opts.withVersion(status.Version)
		}
		podStatusList, err := r.getPodStatusList(ctx, humioNodePools)
		if err != nil {
			r.Log.Error(err, "unable to get pod status list")
		}
		_, _ = r.updateStatus(r.Client.Status(), hc, opts.
			withPods(podStatusList).
			withNodeCount(len(podStatusList)))
	}(ctx, r.HumioClient, hc)

	if len(r.nodePoolsInMaintenance(hc, humioNodePools)) == 0 {
		for _, pool := range humioNodePools {
			if err = r.ensureLabels(ctx, cluster.Config(), req, pool); err != nil {
				return r.updateStatus(r.Client.Status(), hc, statusOptions().
					withMessage(err.Error()))
			}
		}
	}

	// Ensure ingress objects are deleted if ingress is disabled.
	if err = r.ensureNoIngressesIfIngressNotEnabled(ctx, hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}

	if err = r.ensureIngress(ctx, hc); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}

	for _, pool := range humioNodePools {
		if podsReady, err := r.nodePoolPodsReady(hc, pool); !podsReady || err != nil {
			msg := "waiting on all pods to be ready"
			if err != nil {
				msg = err.Error()
			}
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withState(hc.Status.State).
				withMessage(msg))
		}
	}

	if err = r.ensurePartitionsAreBalanced(hc, cluster.Config(), req); err != nil {
		return r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()))
	}

	// TODO: result should be controlled and returned by the status
	if result, err := r.cleanupUnusedTLSCertificates(ctx, hc); result != emptyResult || err != nil {
		if err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
		return result, err
	}

	// TODO: cleanup of unused TLS secrets only removes those that are related to the current HumioCluster,
	//       which means we end up with orphaned secrets when deleting a HumioCluster.
	// TODO: result should be controlled and returned by the status
	if result, err := r.cleanupUnusedTLSSecrets(ctx, hc); result != emptyResult || err != nil {
		if err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
		return result, err
	}

	// TODO: result should be controlled and returned by the status
	if result, err := r.cleanupUnusedCAIssuer(ctx, hc); result != emptyResult || err != nil {
		if err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
		return result, err
	}

	r.Log.Info("done reconciling")
	return r.updateStatus(r.Client.Status(), hc, statusOptions().withState(hc.Status.State).withMessage(""))
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioCluster{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&networkingv1.Ingress{}).
		Complete(r)
}

func (r *HumioClusterReconciler) nodePoolPodsReady(hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) (bool, error) {
	foundPodList, err := kubernetes.ListPods(context.TODO(), r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return false, r.logErrorAndReturn(err, "failed to list pods")
	}
	podsStatus, err := r.getPodsStatus(hnp, foundPodList)
	if err != nil {
		return false, r.logErrorAndReturn(err, "failed to get pod status")
	}
	if podsStatus.waitingOnPods() {
		r.Log.Info("waiting on pods, refusing to continue with reconciliation until all pods are ready")
		r.Log.Info(fmt.Sprintf("cluster state is %s. waitingOnPods=%v, "+
			"revisionsInSync=%v, podRevisisons=%v, podDeletionTimestampSet=%v, podNames=%v, expectedRunningPods=%v, "+
			"podsReady=%v, podsNotReady=%v",
			hc.Status.State, podsStatus.waitingOnPods(), podsStatus.podRevisionsInSync(),
			podsStatus.podRevisions, podsStatus.podDeletionTimestampSet, podsStatus.podNames,
			podsStatus.expectedRunningPods, podsStatus.readyCount, podsStatus.notReadyCount))
		return false, nil
	}
	return true, nil
}

func (r *HumioClusterReconciler) nodePoolAllowsMaintenanceOperations(hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, hnps []*HumioNodePool) bool {
	poolsInMaintenance := r.nodePoolsInMaintenance(hc, hnps)
	if len(poolsInMaintenance) == 0 {
		return true
	}
	for _, poolInMaintenance := range poolsInMaintenance {
		if hnp.GetNodePoolName() == poolInMaintenance.GetNodePoolName() {
			return true
		}
	}
	return false
}

func (r *HumioClusterReconciler) nodePoolsInMaintenance(hc *humiov1alpha1.HumioCluster, hnps []*HumioNodePool) []*HumioNodePool {
	var poolsInMaintenance []*HumioNodePool
	for _, pool := range hnps {
		for _, poolStatus := range hc.Status.NodePoolStatus {
			if poolStatus.Name == pool.GetNodePoolName() && poolStatus.State != humiov1alpha1.HumioClusterStateRunning {
				poolsInMaintenance = append(poolsInMaintenance, pool)
			}
		}
	}
	return poolsInMaintenance
}

func (r *HumioClusterReconciler) ensurePodRevisionAnnotation(hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) (string, error) {
	revisionKey, revisionValue := hnp.GetHumioClusterNodePoolRevisionAnnotation()
	if revisionValue == 0 {
		revisionValue = 1
		r.Log.Info(fmt.Sprintf("setting cluster pod revision %s=%d", revisionKey, revisionValue))
		if hc.Annotations == nil {
			hc.Annotations = map[string]string{}
		}
		hc.Annotations[revisionKey] = strconv.Itoa(revisionValue)
		hnp.SetHumioClusterNodePoolRevisionAnnotation(revisionValue)

		if err := r.Update(context.TODO(), hc); err != nil {
			return humiov1alpha1.HumioClusterStatePending, r.logErrorAndReturn(err, fmt.Sprintf("unable to set pod revision annotation %s", revisionKey))
		}
	}
	return hc.Status.State, nil
}

func (r *HumioClusterReconciler) validateInitialPodSpec(hnp *HumioNodePool) error {
	if _, err := constructPod(hnp, "", &podAttachments{}); err != nil {
		return r.logErrorAndReturn(err, "failed to validate pod spec")
	}
	return nil
}

func (r *HumioClusterReconciler) validateNodeCount(hc *humiov1alpha1.HumioCluster, hnps []*HumioNodePool) error {
	totalNodeCount := 0
	for _, pool := range hnps {
		totalNodeCount += pool.GetNodeCount()
	}

	if totalNodeCount < NewHumioNodeManagerFromHumioCluster(hc).GetTargetReplicationFactor() {
		return r.logErrorAndReturn(fmt.Errorf("nodeCount is too low"), "node count must be equal to or greater than the target replication factor")
	}
	return nil
}

// ensureExtraKafkaConfigsConfigMap creates a configmap containing configs specified in extraKafkaConfigs which will be mounted
// into the Humio container and pointed to by Humio's configuration option EXTRA_KAFKA_CONFIGS_FILE
func (r *HumioClusterReconciler) ensureExtraKafkaConfigsConfigMap(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	extraKafkaConfigsConfigMapData := hnp.GetExtraKafkaConfigs()
	if extraKafkaConfigsConfigMapData == "" {
		return nil
	}
	_, err := kubernetes.GetConfigMap(ctx, r, hnp.GetExtraKafkaConfigsConfigMapName(), hnp.GetNamespace())
	if err != nil {
		if k8serrors.IsNotFound(err) {
			configMap := kubernetes.ConstructExtraKafkaConfigsConfigMap(
				hnp.GetExtraKafkaConfigsConfigMapName(),
				extraKafkaPropertiesFilename,
				extraKafkaConfigsConfigMapData,
				hnp.GetClusterName(),
				hnp.GetNamespace(),
			)
			if err := controllerutil.SetControllerReference(hc, configMap, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			r.Log.Info(fmt.Sprintf("creating configMap: %s", configMap.Name))
			if err = r.Create(ctx, configMap); err != nil {
				return r.logErrorAndReturn(err, "unable to create extra kafka configs configmap")
			}
			r.Log.Info(fmt.Sprintf("successfully created extra kafka configs configmap name %s", configMap.Name))
			humioClusterPrometheusMetrics.Counters.ConfigMapsCreated.Inc()
			return nil
		}
		return r.logErrorAndReturn(err, "unable to get extra kakfa configs configmap")
	}
	return nil
}

// getEnvVarSource returns the environment variables from either the configMap or secret that is referenced by envVarSource
func (r *HumioClusterReconciler) getEnvVarSource(ctx context.Context, hnp *HumioNodePool) (*map[string]string, error) {
	var envVarConfigMapName string
	var envVarSecretName string
	for _, envVarSource := range hnp.GetEnvironmentVariablesSource() {
		if envVarSource.ConfigMapRef != nil {
			envVarConfigMapName = envVarSource.ConfigMapRef.Name
			configMap, err := kubernetes.GetConfigMap(ctx, r, envVarConfigMapName, hnp.GetNamespace())
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return nil, fmt.Errorf("environmentVariablesSource was set but no configMap exists by name %s in namespace %s", envVarConfigMapName, hnp.GetNamespace())
				}
				return nil, fmt.Errorf("unable to get configMap with name %s in namespace %s", envVarConfigMapName, hnp.GetNamespace())
			}
			return &configMap.Data, nil
		}
		if envVarSource.SecretRef != nil {
			envVarSecretName = envVarSource.SecretRef.Name
			secretData := map[string]string{}
			secret, err := kubernetes.GetSecret(ctx, r, envVarSecretName, hnp.GetNamespace())
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return nil, fmt.Errorf("environmentVariablesSource was set but no secret exists by name %s in namespace %s", envVarSecretName, hnp.GetNamespace())
				}
				return nil, fmt.Errorf("unable to get secret with name %s in namespace %s", envVarSecretName, hnp.GetNamespace())
			}
			for k, v := range secret.Data {
				secretData[k] = string(v)
			}
			return &secretData, nil
		}
	}
	return nil, nil
}

// setImageFromSource will check if imageSource is defined and if it is, it will update spec.Image with the image value
func (r *HumioClusterReconciler) setImageFromSource(ctx context.Context, hnp *HumioNodePool) error {
	if hnp.GetImageSource() != nil {
		configMap, err := kubernetes.GetConfigMap(ctx, r, hnp.GetImageSource().ConfigMapRef.Name, hnp.GetNamespace())
		if err != nil {
			return r.logErrorAndReturn(err, "failed to set imageFromSource")
		}
		if imageValue, ok := configMap.Data[hnp.GetImageSource().ConfigMapRef.Key]; ok {
			hnp.SetImage(imageValue)
		} else {
			return r.logErrorAndReturn(err, fmt.Sprintf("imageSource was set but key %s was not found for configmap %s in namespace %s", hnp.GetImageSource().ConfigMapRef.Key, hnp.GetImageSource().ConfigMapRef.Name, hnp.GetNamespace()))
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
			if err = r.Delete(ctx, viewGroupPermissionsConfigMap); err != nil {
				r.Log.Error(err, "unable to delete view group permissions config map")
			}
		}
		return nil
	}
	_, err := kubernetes.GetConfigMap(ctx, r, viewGroupPermissionsConfigMapName(hc), hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			configMap := kubernetes.ConstructViewGroupPermissionsConfigMap(
				viewGroupPermissionsConfigMapName(hc),
				viewGroupPermissionsFilename,
				viewGroupPermissionsConfigMapData,
				hc.Name,
				hc.Namespace,
			)
			if err := controllerutil.SetControllerReference(hc, configMap, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}

			r.Log.Info(fmt.Sprintf("creating configMap: %s", configMap.Name))
			if err = r.Create(ctx, configMap); err != nil {
				return r.logErrorAndReturn(err, "unable to create view group permissions configmap")
			}
			r.Log.Info(fmt.Sprintf("successfully created view group permissions configmap name %s", configMap.Name))
			humioClusterPrometheusMetrics.Counters.ConfigMapsCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureNoIngressesIfIngressNotEnabled(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if hc.Spec.Ingress.Enabled {
		return nil
	}

	foundIngressList, err := kubernetes.ListIngresses(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return r.logErrorAndReturn(err, "could not list ingress")
	}
	// if we do not have any ingress objects we have nothing to clean up
	if len(foundIngressList) == 0 {
		return nil
	}

	for idx, ingress := range foundIngressList {
		// only consider ingresses not already being deleted
		if ingress.DeletionTimestamp == nil {
			r.Log.Info(fmt.Sprintf("deleting ingress with name %s", ingress.Name))
			if err = r.Delete(ctx, &foundIngressList[idx]); err != nil {
				return r.logErrorAndReturn(err, "could not delete ingress")
			}
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureIngress(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !hc.Spec.Ingress.Enabled {
		return nil
	}
	if len(hc.Spec.NodePools) > 0 {
		return fmt.Errorf("ingress only supported if pods belong to HumioCluster.Spec.NodeCount")
	}
	if len(hc.Spec.Ingress.Controller) == 0 {
		return r.logErrorAndReturn(fmt.Errorf("ingress enabled but no controller specified"), "could not ensure ingress")
	}

	switch hc.Spec.Ingress.Controller {
	case "nginx":
		if err := r.ensureNginxIngress(ctx, hc); err != nil {
			return r.logErrorAndReturn(err, "could not ensure nginx ingress")
		}
	default:
		return r.logErrorAndReturn(fmt.Errorf("ingress controller '%s' not supported", hc.Spec.Ingress.Controller), "could not ensure ingress")
	}

	return nil
}

func (r *HumioClusterReconciler) humioHostnames(ctx context.Context, hc *humiov1alpha1.HumioCluster) (string, string, error) {
	var hostname string
	var esHostname string

	if hc.Spec.Hostname != "" {
		hostname = hc.Spec.Hostname
	}
	if hc.Spec.ESHostname != "" {
		esHostname = hc.Spec.ESHostname
	}

	if hc.Spec.HostnameSource.SecretKeyRef != nil {
		if hostname != "" {
			return "", "", fmt.Errorf("conflicting fields: both hostname and hostnameSource.secretKeyRef are defined")
		}

		hostnameSecret, err := kubernetes.GetSecret(ctx, r, hc.Spec.HostnameSource.SecretKeyRef.Name, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return "", "", fmt.Errorf("hostnameSource.secretKeyRef was set but no secret exists by name %s in namespace %s", hc.Spec.HostnameSource.SecretKeyRef.Name, hc.Namespace)

			}
			return "", "", fmt.Errorf("unable to get secret with name %s in namespace %s", hc.Spec.HostnameSource.SecretKeyRef.Name, hc.Namespace)
		}
		if _, ok := hostnameSecret.Data[hc.Spec.HostnameSource.SecretKeyRef.Key]; !ok {
			return "", "", fmt.Errorf("hostnameSource.secretKeyRef was found but it does not contain the key %s", hc.Spec.HostnameSource.SecretKeyRef.Key)
		}
		hostname = string(hostnameSecret.Data[hc.Spec.HostnameSource.SecretKeyRef.Key])

	}
	if hc.Spec.ESHostnameSource.SecretKeyRef != nil {
		if esHostname != "" {
			return "", "", fmt.Errorf("conflicting fields: both esHostname and esHostnameSource.secretKeyRef are defined")
		}

		esHostnameSecret, err := kubernetes.GetSecret(ctx, r, hc.Spec.ESHostnameSource.SecretKeyRef.Name, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return "", "", fmt.Errorf("esHostnameSource.secretKeyRef was set but no secret exists by name %s in namespace %s", hc.Spec.ESHostnameSource.SecretKeyRef.Name, hc.Namespace)

			}
			return "", "", fmt.Errorf("unable to get secret with name %s in namespace %s", hc.Spec.ESHostnameSource.SecretKeyRef.Name, hc.Namespace)
		}
		if _, ok := esHostnameSecret.Data[hc.Spec.ESHostnameSource.SecretKeyRef.Key]; !ok {
			return "", "", fmt.Errorf("esHostnameSource.secretKeyRef was found but it does not contain the key %s", hc.Spec.ESHostnameSource.SecretKeyRef.Key)
		}
		esHostname = string(esHostnameSecret.Data[hc.Spec.ESHostnameSource.SecretKeyRef.Key])
	}

	if hostname == "" && esHostname == "" {
		return "", "", fmt.Errorf("one of the following must be set to enable ingress: hostname, esHostname, " +
			"hostnameSource, esHostnameSource")
	}

	return hostname, esHostname, nil
}

// ensureNginxIngress creates the necessary ingress objects to expose the Humio cluster
// through NGINX ingress controller (https://kubernetes.github.io/ingress-nginx/).
func (r *HumioClusterReconciler) ensureNginxIngress(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring ingress")

	hostname, esHostname, err := r.humioHostnames(ctx, hc)
	if err != nil {
		return r.logErrorAndReturn(err, "could not managed ingress")
	}

	// Due to ingress-ingress relying on ingress object annotations to enable/disable/adjust certain features we create multiple ingress objects.
	ingresses := []*networkingv1.Ingress{
		constructGeneralIngress(hc, hostname),
		constructStreamingQueryIngress(hc, hostname),
		constructIngestIngress(hc, hostname),
		constructESIngestIngress(hc, esHostname),
	}
	for _, desiredIngress := range ingresses {
		// After constructing ingress objects, the rule's host attribute should be set to that which is defined in
		// the humiocluster spec. If the rule host is not set, then it means the hostname or esHostname was not set in
		// the spec, so we do not create the ingress resource
		var createIngress bool
		for _, rule := range desiredIngress.Spec.Rules {
			if rule.Host != "" {
				createIngress = true
			}
		}

		existingIngress, err := kubernetes.GetIngress(ctx, r, desiredIngress.Name, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				if err := controllerutil.SetControllerReference(hc, desiredIngress, r.Scheme()); err != nil {
					return r.logErrorAndReturn(err, "could not set controller reference")
				}
				if createIngress {
					r.Log.Info(fmt.Sprintf("creating ingress: %s", desiredIngress.Name))
					err = r.Create(ctx, desiredIngress)
					if err != nil {
						return r.logErrorAndReturn(err, "unable to create ingress")
					}
					r.Log.Info(fmt.Sprintf("successfully created ingress with name %s", desiredIngress.Name))
					humioClusterPrometheusMetrics.Counters.IngressesCreated.Inc()
				}
				continue
			}
		}

		if !createIngress {
			r.Log.Info(fmt.Sprintf("hostname not defined for ingress object, deleting ingress object with name %s", existingIngress.Name))
			err = r.Delete(ctx, existingIngress)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to delete ingress object")
			}
			r.Log.Info(fmt.Sprintf("successfully deleted ingress %+#v", desiredIngress))
			continue
		}

		if !r.ingressesMatch(existingIngress, desiredIngress) {
			r.Log.Info(fmt.Sprintf("ingress object already exists, there is a difference between expected vs existing, updating ingress object with name %s", desiredIngress.Name))
			existingIngress.Annotations = desiredIngress.Annotations
			existingIngress.Labels = desiredIngress.Labels
			existingIngress.Spec = desiredIngress.Spec
			err = r.Update(ctx, existingIngress)
			if err != nil {
				return r.logErrorAndReturn(err, "could not update ingress")
			}
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureHumioPodPermissions(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	// Do not manage these resources if the HumioServiceAccountName is supplied. This implies the service account is managed
	// outside of the operator
	if hnp.HumioServiceAccountIsSetByUser() {
		return nil
	}

	r.Log.Info("ensuring pod permissions")
	if err := r.ensureServiceAccountExists(ctx, hc, hnp, hnp.GetHumioServiceAccountName(), hnp.GetHumioServiceAccountAnnotations()); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure humio service account exists")
	}

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		if err := r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hnp.GetNamespace(), hnp.GetInitServiceAccountName()); err != nil {
			return r.logErrorAndReturn(err, "could not ensure SecurityContextConstraints contains ServiceAccount")
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureInitContainerPermissions(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	if hnp.InitContainerDisabled() {
		return nil
	}

	// Only add the service account secret if the initServiceAccountName is supplied. This implies the service account,
	// cluster role and cluster role binding are managed outside of the operator, so we skip the remaining tasks.
	if hnp.InitServiceAccountIsSetByUser() {
		// We do not want to attach the init service account to the humio pod. Instead, only the init container should use this
		// service account. To do this, we can attach the service account directly to the init container as per
		// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
		if err := r.ensureServiceAccountSecretExists(ctx, hc, hnp, hnp.GetInitServiceAccountSecretName(), hnp.GetInitServiceAccountName()); err != nil {
			return r.logErrorAndReturn(err, "unable to ensure init service account secret exists for HumioCluster")
		}
		return nil
	}

	// The service account is used by the init container attached to the humio pods to get the availability zone
	// from the node on which the pod is scheduled. We cannot pre determine the zone from the controller because we cannot
	// assume that the nodes are running. Additionally, if we pre allocate the zones to the humio pods, we would be required
	// to have an autoscaling group per zone.

	if err := r.ensureServiceAccountExists(ctx, hc, hnp, hnp.GetInitServiceAccountName(), map[string]string{}); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure init service account exists")
	}

	// We do not want to attach the init service account to the humio pod. Instead, only the init container should use this
	// service account. To do this, we can attach the service account directly to the init container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	if err := r.ensureServiceAccountSecretExists(ctx, hc, hnp, hnp.GetInitServiceAccountSecretName(), hnp.GetInitServiceAccountName()); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure init service account secret exists for HumioCluster")
	}

	// This should be namespaced by the name, e.g. clustername-namespace-name
	// Required until https://github.com/kubernetes/kubernetes/issues/40610 is fixed

	if err := r.ensureInitClusterRole(ctx, hnp); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure init cluster role exists")
	}

	// This should be namespaced by the name, e.g. clustername-namespace-name
	// Required until https://github.com/kubernetes/kubernetes/issues/40610 is fixed
	if err := r.ensureInitClusterRoleBinding(ctx, hnp); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure init cluster role binding exists")
	}

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		if err := r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hnp.GetNamespace(), hnp.GetInitServiceAccountName()); err != nil {
			return r.logErrorAndReturn(err, "could not ensure SecurityContextConstraints contains ServiceAccount")
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureAuthContainerPermissions(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	// Only add the service account secret if the authServiceAccountName is supplied. This implies the service account,
	// cluster role and cluster role binding are managed outside of the operator, so we skip the remaining tasks.
	if hnp.AuthServiceAccountIsSetByUser() {
		// We do not want to attach the auth service account to the humio pod. Instead, only the auth container should use this
		// service account. To do this, we can attach the service account directly to the auth container as per
		// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
		if err := r.ensureServiceAccountSecretExists(ctx, hc, hnp, hnp.GetAuthServiceAccountSecretName(), hnp.GetAuthServiceAccountName()); err != nil {
			return r.logErrorAndReturn(err, "unable to ensure auth service account secret exists")
		}
		return nil
	}

	// The service account is used by the auth container attached to the humio pods.
	if err := r.ensureServiceAccountExists(ctx, hc, hnp, hnp.GetAuthServiceAccountName(), map[string]string{}); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure auth service account exists")
	}

	// We do not want to attach the auth service account to the humio pod. Instead, only the auth container should use this
	// service account. To do this, we can attach the service account directly to the auth container as per
	// https://github.com/kubernetes/kubernetes/issues/66020#issuecomment-590413238
	if err := r.ensureServiceAccountSecretExists(ctx, hc, hnp, hnp.GetAuthServiceAccountSecretName(), hnp.GetAuthServiceAccountName()); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure auth service account secret exists")
	}

	if err := r.ensureAuthRole(ctx, hc, hnp); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure auth role exists")
	}

	if err := r.ensureAuthRoleBinding(ctx, hc, hnp); err != nil {
		return r.logErrorAndReturn(err, "unable to ensure auth role binding exists")
	}

	// In cases with OpenShift, we must ensure our ServiceAccount has access to the SecurityContextConstraint
	if helpers.IsOpenShift() {
		if err := r.ensureSecurityContextConstraintsContainsServiceAccount(ctx, hnp.GetNamespace(), hnp.GetAuthServiceAccountName()); err != nil {
			return r.logErrorAndReturn(err, "could not ensure SecurityContextConstraints contains ServiceAccount")
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureSecurityContextConstraintsContainsServiceAccount(ctx context.Context, namespace, serviceAccountName string) error {
	// TODO: Write unit/e2e test for this

	if !helpers.IsOpenShift() {
		return fmt.Errorf("updating SecurityContextConstraints are only suppoted when running on OpenShift")
	}

	// Get current SCC
	scc, err := openshift.GetSecurityContextConstraints(ctx, r)
	if err != nil {
		return r.logErrorAndReturn(err, "unable to get details about SecurityContextConstraints")
	}

	// Give ServiceAccount access to SecurityContextConstraints if not already present
	usersEntry := fmt.Sprintf("system:serviceaccount:%s:%s", namespace, serviceAccountName)
	if !helpers.ContainsElement(scc.Users, usersEntry) {
		scc.Users = append(scc.Users, usersEntry)
		err = r.Update(ctx, scc)
		if err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("could not update SecurityContextConstraints %s to add ServiceAccount %s", scc.Name, serviceAccountName))
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureCleanupUsersInSecurityContextConstraints(ctx context.Context) error {
	if !helpers.IsOpenShift() {
		return nil
	}

	scc, err := openshift.GetSecurityContextConstraints(ctx, r)
	if err != nil {
		return r.logErrorAndReturn(err, "unable to get details about SecurityContextConstraints")
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
		if k8serrors.IsNotFound(err) {
			// If we have an error and it reflects that the service account does not exist, we remove the entry from the list.
			scc.Users = helpers.RemoveElement(scc.Users, fmt.Sprintf("system:serviceaccount:%s:%s", sccUserNamespace, sccUserName))
			if err = r.Update(ctx, scc); err != nil {
				return r.logErrorAndReturn(err, "unable to update SecurityContextConstraints")
			}
		} else {
			return r.logErrorAndReturn(err, "unable to get existing service account")
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureValidCAIssuer(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		return nil
	}

	r.Log.Info("checking for an existing valid CA Issuer")
	validCAIssuer, err := validCAIssuer(ctx, r, hc.Namespace, hc.Name)
	if err != nil && !k8serrors.IsNotFound(err) {
		return r.logErrorAndReturn(err, "could not validate CA Issuer")
	}
	if validCAIssuer {
		r.Log.Info("found valid CA Issuer")
		return nil
	}

	var existingCAIssuer cmapi.Issuer
	if err = r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, &existingCAIssuer); err != nil {
		if k8serrors.IsNotFound(err) {
			caIssuer := constructCAIssuer(hc)
			if err := controllerutil.SetControllerReference(hc, &caIssuer, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			// should only create it if it doesn't exist
			r.Log.Info(fmt.Sprintf("creating CA Issuer: %s", caIssuer.Name))
			if err = r.Create(ctx, &caIssuer); err != nil {
				return r.logErrorAndReturn(err, "could not create CA Issuer")
			}
			return nil
		}
		return r.logErrorAndReturn(err, "ccould not get CA Issuer")
	}

	return nil
}

func (r *HumioClusterReconciler) ensureValidCASecret(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		return nil
	}

	r.Log.Info("checking for an existing CA secret")
	validCASecret, err := validCASecret(ctx, r, hc.Namespace, getCASecretName(hc))
	if validCASecret {
		r.Log.Info("found valid CA secret")
		return nil
	}
	if err != nil && !k8serrors.IsNotFound(err) {
		return r.logErrorAndReturn(err, "could not validate CA secret")
	}

	if useExistingCA(hc) {
		return r.logErrorAndReturn(fmt.Errorf("configured to use existing CA secret, but the CA secret invalid"), "specified CA secret invalid")
	}

	r.Log.Info("generating new CA certificate")
	ca, err := generateCACertificate()
	if err != nil {
		return r.logErrorAndReturn(err, "could not generate new CA certificate")
	}

	r.Log.Info("persisting new CA certificate")
	caSecretData := map[string][]byte{
		"tls.crt": ca.Certificate,
		"tls.key": ca.Key,
	}
	caSecret := kubernetes.ConstructSecret(hc.Name, hc.Namespace, getCASecretName(hc), caSecretData, nil)
	if err := controllerutil.SetControllerReference(hc, caSecret, r.Scheme()); err != nil {
		return r.logErrorAndReturn(err, "could not set controller reference")
	}
	r.Log.Info(fmt.Sprintf("creating CA secret: %s", caSecret.Name))
	err = r.Create(ctx, caSecret)
	if err != nil {
		return r.logErrorAndReturn(err, "could not create secret with CA")
	}

	return nil
}

func (r *HumioClusterReconciler) ensureHumioClusterKeystoreSecret(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		return nil
	}

	existingSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      fmt.Sprintf("%s-keystore-passphrase", hc.Name),
	}, existingSecret); err != nil {
		if k8serrors.IsNotFound(err) {
			randomPass := kubernetes.RandomString()
			secretData := map[string][]byte{
				"passphrase": []byte(randomPass), // TODO: do we need separate passwords for different aspects?
			}
			secret := kubernetes.ConstructSecret(hc.Name, hc.Namespace, fmt.Sprintf("%s-keystore-passphrase", hc.Name), secretData, nil)
			if err := controllerutil.SetControllerReference(hc, secret, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			r.Log.Info(fmt.Sprintf("creating secret: %s", secret.Name))
			if err := r.Create(ctx, secret); err != nil {
				return r.logErrorAndReturn(err, "could not create secret")
			}
			return nil
		} else {
			return r.logErrorAndReturn(err, "could not get secret")
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureHumioClusterCACertBundle(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		return nil
	}

	r.Log.Info("ensuring we have a CA cert bundle")
	existingCertificate := &cmapi.Certificate{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, existingCertificate)

	if k8serrors.IsNotFound(err) {
		r.Log.Info("CA cert bundle doesn't exist, creating it now")
		cert := constructClusterCACertificateBundle(hc)
		if err := controllerutil.SetControllerReference(hc, &cert, r.Scheme()); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		r.Log.Info(fmt.Sprintf("creating certificate: %s", cert.Name))
		if err := r.Create(ctx, &cert); err != nil {
			return r.logErrorAndReturn(err, "could not create certificate")
		}
		return nil
	}

	if err != nil {
		return r.logErrorAndReturn(err, "could not get certificate")
	}
	return nil
}

func (r *HumioClusterReconciler) ensureHumioNodeCertificates(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	if !hnp.TLSEnabled() {
		return nil
	}

	existingNodeCertCount, err := r.updateNodeCertificates(ctx, hc, hnp)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to get node certificate count")
	}
	for i := existingNodeCertCount; i < hnp.GetNodeCount(); i++ {
		certificate := constructNodeCertificate(hc, hnp, kubernetes.RandomString())

		certForHash := constructNodeCertificate(hc, hnp, "")
		// Keystores will always contain a new pointer when constructing a certificate.
		// To work around this, we override it to nil before calculating the hash,
		// if we do not do this, the hash will always be different.
		certForHash.Spec.Keystores = nil

		certificateHash := helpers.AsSHA256(certForHash)
		certificate.Annotations[certHashAnnotation] = certificateHash
		r.Log.Info(fmt.Sprintf("creating node TLS certificate with name %s", certificate.Name))
		if err = controllerutil.SetControllerReference(hc, &certificate, r.Scheme()); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		r.Log.Info(fmt.Sprintf("creating node certificate: %s", certificate.Name))
		if err = r.Create(ctx, &certificate); err != nil {
			return r.logErrorAndReturn(err, "could create node certificate")
		}

		if err = r.waitForNewNodeCertificate(ctx, hc, hnp, existingNodeCertCount+1); err != nil {
			return r.logErrorAndReturn(err, "new node certificate not ready as expected")
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureInitClusterRole(ctx context.Context, hnp *HumioNodePool) error {
	clusterRoleName := hnp.GetInitClusterRoleName()
	_, err := kubernetes.GetClusterRole(ctx, r, clusterRoleName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			clusterRole := kubernetes.ConstructInitClusterRole(clusterRoleName, hnp.GetNodePoolLabels())
			// TODO: We cannot use controllerutil.SetControllerReference() as ClusterRole is cluster-wide and owner is namespaced.
			// We probably need another way to ensure we clean them up. Perhaps we can use finalizers?
			r.Log.Info(fmt.Sprintf("creating cluster role: %s", clusterRole.Name))
			err = r.Create(ctx, clusterRole)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to create init cluster role")
			}
			r.Log.Info(fmt.Sprintf("successfully created init cluster role %s", clusterRoleName))
			humioClusterPrometheusMetrics.Counters.ClusterRolesCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureAuthRole(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	roleName := hnp.GetAuthRoleName()
	_, err := kubernetes.GetRole(ctx, r, roleName, hnp.GetNamespace())
	if err != nil {
		if k8serrors.IsNotFound(err) {
			role := kubernetes.ConstructAuthRole(roleName, hnp.GetNamespace(), hnp.GetNodePoolLabels())
			if err := controllerutil.SetControllerReference(hc, role, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			r.Log.Info(fmt.Sprintf("creating role: %s", role.Name))
			err = r.Create(ctx, role)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to create auth role")
			}
			r.Log.Info(fmt.Sprintf("successfully created auth role %s", roleName))
			humioClusterPrometheusMetrics.Counters.RolesCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureInitClusterRoleBinding(ctx context.Context, hnp *HumioNodePool) error {
	clusterRoleBindingName := hnp.GetInitClusterRoleBindingName()
	_, err := kubernetes.GetClusterRoleBinding(ctx, r, clusterRoleBindingName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			clusterRole := kubernetes.ConstructClusterRoleBinding(
				clusterRoleBindingName,
				hnp.GetInitClusterRoleName(),
				hnp.GetNamespace(),
				hnp.GetInitServiceAccountName(),
				hnp.GetNodePoolLabels(),
			)
			// TODO: We cannot use controllerutil.SetControllerReference() as ClusterRoleBinding is cluster-wide and owner is namespaced.
			// We probably need another way to ensure we clean them up. Perhaps we can use finalizers?
			r.Log.Info(fmt.Sprintf("creating cluster role: %s", clusterRole.Name))
			err = r.Create(ctx, clusterRole)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to create init cluster role binding")
			}
			r.Log.Info(fmt.Sprintf("successfully created init cluster role binding %s", clusterRoleBindingName))
			humioClusterPrometheusMetrics.Counters.ClusterRoleBindingsCreated.Inc()
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureAuthRoleBinding(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	roleBindingName := hnp.GetAuthRoleBindingName()
	_, err := kubernetes.GetRoleBinding(ctx, r, roleBindingName, hnp.GetNamespace())
	if err != nil {
		if k8serrors.IsNotFound(err) {
			roleBinding := kubernetes.ConstructRoleBinding(
				roleBindingName,
				hnp.GetAuthRoleName(),
				hnp.GetNamespace(),
				hnp.GetAuthServiceAccountName(),
				hnp.GetNodePoolLabels(),
			)
			if err := controllerutil.SetControllerReference(hc, roleBinding, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			r.Log.Info(fmt.Sprintf("creating role binding: %s", roleBinding.Name))
			err = r.Create(ctx, roleBinding)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to create auth role binding")
			}
			r.Log.Info(fmt.Sprintf("successfully created auth role binding %s", roleBindingName))
			humioClusterPrometheusMetrics.Counters.RoleBindingsCreated.Inc()
		}
	}
	return nil
}

// validateUserDefinedServiceAccountsExists confirms that the user-defined service accounts all exist as they should.
// If any of the service account names explicitly set does not exist, or that we get an error, we return false and the error.
// In case the user does not define any service accounts or that all user-defined service accounts already exists, we return true.
func (r *HumioClusterReconciler) validateUserDefinedServiceAccountsExists(ctx context.Context, hc *humiov1alpha1.HumioCluster) (bool, error) {
	if hc.Spec.HumioServiceAccountName != "" {
		_, err := kubernetes.GetServiceAccount(ctx, r, hc.Spec.HumioServiceAccountName, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, r.logErrorAndReturn(err, "not all referenced service accounts exists")
			}
			return true, r.logErrorAndReturn(err, "could not get service accounts")
		}
	}
	if hc.Spec.InitServiceAccountName != "" {
		_, err := kubernetes.GetServiceAccount(ctx, r, hc.Spec.InitServiceAccountName, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, r.logErrorAndReturn(err, "not all referenced service accounts exists")
			}
			return true, r.logErrorAndReturn(err, "could not get service accounts")
		}
	}
	if hc.Spec.AuthServiceAccountName != "" {
		_, err := kubernetes.GetServiceAccount(ctx, r, hc.Spec.AuthServiceAccountName, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, r.logErrorAndReturn(err, "not all referenced service accounts exists")
			}
			return true, r.logErrorAndReturn(err, "could not get service accounts")
		}
	}
	return true, nil
}

func (r *HumioClusterReconciler) ensureServiceAccountExists(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, serviceAccountName string, serviceAccountAnnotations map[string]string) error {
	serviceAccountExists, err := r.serviceAccountExists(ctx, hnp.GetNamespace(), serviceAccountName)
	if err != nil {
		return r.logErrorAndReturn(err, fmt.Sprintf("could not check existence of service account %q", serviceAccountName))
	}
	if !serviceAccountExists {
		serviceAccount := kubernetes.ConstructServiceAccount(serviceAccountName, hnp.GetNamespace(), serviceAccountAnnotations, hnp.GetNodePoolLabels())
		if err := controllerutil.SetControllerReference(hc, serviceAccount, r.Scheme()); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		r.Log.Info(fmt.Sprintf("creating service account: %s", serviceAccount.Name))
		err = r.Create(ctx, serviceAccount)
		if err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("unable to create service account %s", serviceAccount.Name))
		}
		r.Log.Info(fmt.Sprintf("successfully created service account %s", serviceAccount.Name))
		humioClusterPrometheusMetrics.Counters.ServiceAccountsCreated.Inc()
	}
	return nil
}

func (r *HumioClusterReconciler) ensureServiceAccountSecretExists(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, serviceAccountSecretName, serviceAccountName string) error {
	serviceAccountExists, err := r.serviceAccountExists(ctx, hnp.GetNamespace(), serviceAccountName)
	if err != nil {
		return r.logErrorAndReturn(err, fmt.Sprintf("could not check existence of service account %q", serviceAccountName))
	}
	if !serviceAccountExists {
		return r.logErrorAndReturn(err, fmt.Sprintf("service account %q must exist before the service account secret can be created", serviceAccountName))
	}

	foundServiceAccountSecretsList, err := kubernetes.ListSecrets(ctx, r, hnp.GetNamespace(), hnp.GetLabelsForSecret(serviceAccountSecretName))
	if err != nil {
		return r.logErrorAndReturn(err, "unable to list secrets")
	}

	if len(foundServiceAccountSecretsList) == 0 {
		secret := kubernetes.ConstructServiceAccountSecret(hnp.GetClusterName(), hnp.GetNamespace(), serviceAccountSecretName, serviceAccountName)
		if err := controllerutil.SetControllerReference(hc, secret, r.Scheme()); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		r.Log.Info(fmt.Sprintf("creating secret: %s", secret.Name))
		err = r.Create(ctx, secret)
		if err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("unable to create service account secret %s", secret.Name))
		}
		// check that we can list the new secret
		// this is to avoid issues where the requeue is faster than kubernetes
		if err := r.waitForNewSecret(ctx, hnp, foundServiceAccountSecretsList, serviceAccountSecretName); err != nil {
			return r.logErrorAndReturn(err, "failed to validate new secret")
		}
		r.Log.Info(fmt.Sprintf("successfully created service account secret %s for service account %s", secret.Name, serviceAccountName))
		humioClusterPrometheusMetrics.Counters.ServiceAccountSecretsCreated.Inc()
	}

	return nil
}

func (r *HumioClusterReconciler) serviceAccountExists(ctx context.Context, namespace, serviceAccountName string) (bool, error) {
	if _, err := kubernetes.GetServiceAccount(ctx, r, serviceAccountName, namespace); err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *HumioClusterReconciler) ensureLabels(ctx context.Context, config *humioapi.Config, req reconcile.Request, hnp *HumioNodePool) error {
	r.Log.Info("ensuring labels")
	cluster, err := r.HumioClient.GetClusters(config, req)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to get clusters")
	}

	foundPodList, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return r.logErrorAndReturn(err, "failed to list pods")
	}

	pvcList, err := r.pvcList(ctx, hnp)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to list pvcs to assign labels")
	}

	r.Log.Info(fmt.Sprintf("cluster node details: %#+v", cluster.Nodes))
	for idx, pod := range foundPodList {
		// Skip pods that already have a label. Check that the pvc also has the label if applicable
		if kubernetes.LabelListContainsLabel(pod.GetLabels(), kubernetes.NodeIdLabelName) {
			if hnp.PVCsEnabled() {
				if err := r.ensurePvcLabels(ctx, hnp, pod, pvcList); err != nil {
					return r.logErrorAndReturn(err, "could not ensure pvc labels")
				}
			}
			continue
		}
		// If pod does not have an IP yet, so it is probably pending
		if pod.Status.PodIP == "" {
			r.Log.Info(fmt.Sprintf("not setting labels for pod %s because it is in state %s", pod.Name, pod.Status.Phase))
			continue
		}
		for _, node := range cluster.Nodes {
			if node.Uri == fmt.Sprintf("http://%s:%d", pod.Status.PodIP, humioPort) {
				labels := hnp.GetNodePoolLabels()
				labels[kubernetes.NodeIdLabelName] = strconv.Itoa(node.Id)
				r.Log.Info(fmt.Sprintf("setting labels for pod %s, labels=%v", pod.Name, labels))
				pod.SetLabels(labels)
				if err := r.Update(ctx, &foundPodList[idx]); err != nil {
					return r.logErrorAndReturn(err, fmt.Sprintf("failed to update labels on pod %s", pod.Name))
				}
				if hnp.PVCsEnabled() {
					if err = r.ensurePvcLabels(ctx, hnp, pod, pvcList); err != nil {
						return r.logErrorAndReturn(err, "could not ensure pvc labels")
					}
				}
			}
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensurePvcLabels(ctx context.Context, hnp *HumioNodePool, pod corev1.Pod, pvcList []corev1.PersistentVolumeClaim) error {
	pvc, err := findPvcForPod(pvcList, pod)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to get pvc for pod to assign labels")
	}
	if kubernetes.LabelListContainsLabel(pvc.GetLabels(), kubernetes.NodeIdLabelName) {
		return nil
	}
	nodeId, err := strconv.Atoi(pod.Labels[kubernetes.NodeIdLabelName])
	if err != nil {
		return r.logErrorAndReturn(err, fmt.Sprintf("unable to set label on pvc, nodeid %v is invalid", pod.Labels[kubernetes.NodeIdLabelName]))
	}
	labels := hnp.GetNodePoolLabels()
	labels[kubernetes.NodeIdLabelName] = strconv.Itoa(nodeId)
	r.Log.Info(fmt.Sprintf("setting labels for pvc %s, labels=%v", pvc.Name, labels))
	pvc.SetLabels(labels)
	if err := r.Update(ctx, &pvc); err != nil {
		return r.logErrorAndReturn(err, fmt.Sprintf("failed to update labels on pvc %s", pod.Name))
	}
	return nil
}

func (r *HumioClusterReconciler) ensureLicenseIsValid(hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring license is valid")

	licenseSecretKeySelector := licenseSecretKeyRefOrDefault(hc)
	if licenseSecretKeySelector == nil {
		return fmt.Errorf("no license secret key selector provided")
	}

	licenseSecret, err := kubernetes.GetSecret(context.TODO(), r, licenseSecretKeySelector.Name, hc.Namespace)
	if err != nil {
		return err
	}
	if _, ok := licenseSecret.Data[licenseSecretKeySelector.Key]; !ok {
		return r.logErrorAndReturn(fmt.Errorf("could not read the license"),
			fmt.Sprintf("key %s does not exist for secret %s", licenseSecretKeySelector.Key, licenseSecretKeySelector.Name))
	}

	licenseStr := string(licenseSecret.Data[licenseSecretKeySelector.Key])
	if _, err = humio.ParseLicense(licenseStr); err != nil {
		return r.logErrorAndReturn(err,
			"unable to parse license")
	}

	return nil
}

func (r *HumioClusterReconciler) ensureLicense(ctx context.Context, hc *humiov1alpha1.HumioCluster, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info("ensuring license")

	// Configure a Humio client without an API token which we can use to check the current license on the cluster
	noLicense := humioapi.OnPremLicense{}
	cluster, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), false)
	if err != nil {
		return reconcile.Result{}, err
	}

	existingLicense, err := r.HumioClient.GetLicense(cluster.Config(), req)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get license: %w", err)
	}

	defer func(ctx context.Context, hc *humiov1alpha1.HumioCluster) {
		if existingLicense != nil {
			licenseStatus := humiov1alpha1.HumioLicenseStatus{
				Type:       "onprem",
				Expiration: existingLicense.ExpiresAt(),
			}
			_, _ = r.updateStatus(r.Client.Status(), hc, statusOptions().
				withLicense(licenseStatus))
		}
	}(ctx, hc)

	licenseStr, err := r.getLicenseString(ctx, hc)
	if err != nil {
		_, _ = r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
		return reconcile.Result{}, err
	}

	// Confirm we can parse the license provided in the HumioCluster resource
	desiredLicense, err := humio.ParseLicense(licenseStr)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "license was supplied but could not be parsed")
	}

	// At this point we know a non-empty license has been returned by the Humio API,
	// so we can continue to parse the license and issue a license update if needed.
	if existingLicense == nil || existingLicense == noLicense {
		if err = r.HumioClient.InstallLicense(cluster.Config(), req, licenseStr); err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not install initial license")
		}

		r.Log.Info(fmt.Sprintf("successfully installed initial license: issued: %s, expires: %s",
			desiredLicense.IssuedAt(), desiredLicense.ExpiresAt()))
		return reconcile.Result{Requeue: true}, nil
	}

	cluster, err = helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true)
	if err != nil {
		return reconcile.Result{}, err
	}

	if existingLicense.IssuedAt() != desiredLicense.IssuedAt() ||
		existingLicense.ExpiresAt() != desiredLicense.ExpiresAt() {
		r.Log.Info(fmt.Sprintf("updating license because of: existingLicense.IssuedAt(%s) != desiredLicense.IssuedAt(%s) || existingLicense.ExpiresAt(%s) != desiredLicense.ExpiresAt(%s)", existingLicense.IssuedAt(), desiredLicense.IssuedAt(), existingLicense.ExpiresAt(), desiredLicense.ExpiresAt()))
		if err = r.HumioClient.InstallLicense(cluster.Config(), req, licenseStr); err != nil {
			return reconcile.Result{}, fmt.Errorf("could not install license: %w", err)
		}

		r.Log.Info(fmt.Sprintf("successfully installed license: issued: %s, expires: %s",
			desiredLicense.IssuedAt(), desiredLicense.ExpiresAt()))

		// refresh the existing license for the status update
		existingLicense, err = r.HumioClient.GetLicense(cluster.Config(), req)
		if err != nil {
			r.Log.Error(err, "failed to get updated license: %w", err)
		}
		return reconcile.Result{}, nil
	}

	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) ensurePartitionsAreBalanced(hc *humiov1alpha1.HumioCluster, config *humioapi.Config, req reconcile.Request) error {
	if !hc.Spec.AutoRebalancePartitions {
		r.Log.Info("partition auto-rebalancing not enabled, skipping")
		return nil
	}

	currentClusterInfo, err := r.HumioClient.GetClusters(config, req)
	if err != nil {
		return r.logErrorAndReturn(err, "could not get cluster info")
	}

	suggestedStorageLayout, err := r.HumioClient.SuggestedStoragePartitions(config, req)
	if err != nil {
		return r.logErrorAndReturn(err, "could not get suggested storage layout")
	}
	currentStorageLayoutInput := helpers.MapStoragePartition(currentClusterInfo.StoragePartitions, helpers.ToStoragePartitionInput)
	if !reflect.DeepEqual(currentStorageLayoutInput, suggestedStorageLayout) {
		r.Log.Info(fmt.Sprintf("triggering update of storage partitions to use suggested layout, current: %#+v, suggested: %#+v", currentClusterInfo.StoragePartitions, suggestedStorageLayout))
		if err = r.HumioClient.UpdateStoragePartitionScheme(config, req, suggestedStorageLayout); err != nil {
			return r.logErrorAndReturn(err, "could not update storage partition scheme")
		}
	}

	suggestedIngestLayout, err := r.HumioClient.SuggestedIngestPartitions(config, req)
	if err != nil {
		return r.logErrorAndReturn(err, "could not get suggested ingest layout")
	}
	currentIngestLayoutInput := helpers.MapIngestPartition(currentClusterInfo.IngestPartitions, helpers.ToIngestPartitionInput)
	if !reflect.DeepEqual(currentIngestLayoutInput, suggestedIngestLayout) {
		r.Log.Info(fmt.Sprintf("triggering update of ingest partitions to use suggested layout, current: %#+v, suggested: %#+v", currentClusterInfo.IngestPartitions, suggestedIngestLayout))
		if err = r.HumioClient.UpdateIngestPartitionScheme(config, req, suggestedIngestLayout); err != nil {
			return r.logErrorAndReturn(err, "could not update ingest partition scheme")
		}
	}

	return nil
}

func (r *HumioClusterReconciler) ensureService(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	r.Log.Info("ensuring service")
	existingService, err := kubernetes.GetService(ctx, r, hnp.GetNodePoolName(), hnp.GetNamespace())
	service := constructService(hnp)
	if k8serrors.IsNotFound(err) {
		if err := controllerutil.SetControllerReference(hc, service, r.Scheme()); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		r.Log.Info(fmt.Sprintf("creating service %s of type %s with Humio port %d and ES port %d", service.Name, service.Spec.Type, hnp.GetHumioServicePort(), hnp.GetHumioESServicePort()))
		if err = r.Create(ctx, service); err != nil {
			return r.logErrorAndReturn(err, "unable to create service for HumioCluster")
		}
		return nil
	}

	if servicesMatchTest, err := servicesMatch(existingService, service); !servicesMatchTest || err != nil {
		r.Log.Info(fmt.Sprintf("service %s requires update: %s", existingService.Name, err))
		updateService(existingService, service)
		if err = r.Update(ctx, existingService); err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("could not update service %s", service.Name))
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureHeadlessServiceExists(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring headless service")
	existingService, err := kubernetes.GetService(ctx, r, headlessServiceName(hc.Name), hc.Namespace)
	service := constructHeadlessService(hc)
	if k8serrors.IsNotFound(err) {
		if err := controllerutil.SetControllerReference(hc, service, r.Scheme()); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		err = r.Create(ctx, service)
		if err != nil {
			return r.logErrorAndReturn(err, "unable to create headless service for HumioCluster")
		}
		return nil
	}
	if servicesMatchTest, err := servicesMatch(existingService, service); !servicesMatchTest || err != nil {
		r.Log.Info(fmt.Sprintf("service %s requires update: %s", existingService.Name, err))
		updateService(existingService, service)
		if err = r.Update(ctx, existingService); err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("could not update service %s", service.Name))
		}
	}
	return nil
}

// ensureNodePoolSpecificResourcesHaveLabelWithNodePoolName updates resources that were created prior to the introduction of node pools.
// We need this because multiple resources now includes an additional label containing the name of the node pool a given resource belongs to.
func (r *HumioClusterReconciler) ensureNodePoolSpecificResourcesHaveLabelWithNodePoolName(ctx context.Context, hnp *HumioNodePool) error {
	allPods, err := kubernetes.ListPods(ctx, r.Client, hnp.GetNamespace(), hnp.GetCommonClusterLabels())
	if err != nil {
		return r.logErrorAndReturn(err, "unable to list pods")
	}
	for _, pod := range allPods {
		if _, found := pod.Labels[kubernetes.NodePoolLabelName]; !found {
			pod.SetLabels(hnp.GetPodLabels())
			err = r.Client.Update(ctx, &pod)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to update pod")
			}
		}
	}

	if hnp.TLSEnabled() {
		allNodeCertificates, err := kubernetes.ListCertificates(ctx, r.Client, hnp.GetNamespace(), hnp.GetCommonClusterLabels())
		if err != nil {
			return err
		}
		for _, cert := range allNodeCertificates {
			if _, found := cert.Labels[kubernetes.NodePoolLabelName]; !found {
				cert.SetLabels(hnp.GetNodePoolLabels())
				err = r.Client.Update(ctx, &cert)
				if err != nil {
					return r.logErrorAndReturn(err, "unable to update node certificate")
				}
			}
		}
	}

	if hnp.PVCsEnabled() {
		allPVCs, err := kubernetes.ListPersistentVolumeClaims(ctx, r.Client, hnp.GetNamespace(), hnp.GetCommonClusterLabels())
		if err != nil {
			return err
		}
		for _, pvc := range allPVCs {
			if _, found := pvc.Labels[kubernetes.NodePoolLabelName]; !found {
				pvc.SetLabels(hnp.GetNodePoolLabels())
				err = r.Client.Update(ctx, &pvc)
				if err != nil {
					return r.logErrorAndReturn(err, "unable to update pvc")
				}
			}
		}
	}

	if !hnp.HumioServiceAccountIsSetByUser() {
		serviceAccount, err := kubernetes.GetServiceAccount(ctx, r.Client, hnp.GetHumioServiceAccountName(), hnp.GetNamespace())
		if err == nil {
			serviceAccount.SetLabels(hnp.GetNodePoolLabels())
			err = r.Client.Update(ctx, serviceAccount)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to update humio service account")
			}
		}
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "unable to get humio service account")
			}
		}
	}

	if !hnp.InitServiceAccountIsSetByUser() {
		serviceAccount, err := kubernetes.GetServiceAccount(ctx, r.Client, hnp.GetInitServiceAccountName(), hnp.GetNamespace())
		if err == nil {
			serviceAccount.SetLabels(hnp.GetNodePoolLabels())
			err = r.Client.Update(ctx, serviceAccount)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to update init service account")
			}
		}
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "unable to get init service account")
			}
		}

		clusterRole, err := kubernetes.GetClusterRole(ctx, r.Client, hnp.GetInitClusterRoleName())
		if err == nil {
			clusterRole.SetLabels(hnp.GetNodePoolLabels())
			err = r.Client.Update(ctx, clusterRole)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to update init cluster role")
			}
		}
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "unable to get init cluster role")
			}
		}

		clusterRoleBinding, err := kubernetes.GetClusterRoleBinding(ctx, r.Client, hnp.GetInitClusterRoleBindingName())
		if err == nil {
			clusterRoleBinding.SetLabels(hnp.GetNodePoolLabels())
			err = r.Client.Update(ctx, clusterRoleBinding)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to update init cluster role binding")
			}
		}
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "unable to get init cluster role binding")
			}
		}
	}

	if !hnp.AuthServiceAccountIsSetByUser() {
		serviceAccount, err := kubernetes.GetServiceAccount(ctx, r.Client, hnp.GetAuthServiceAccountName(), hnp.GetNamespace())
		if err == nil {
			serviceAccount.SetLabels(hnp.GetNodePoolLabels())
			err = r.Client.Update(ctx, serviceAccount)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to update auth service account")
			}
		}
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "unable to get auth service account")
			}
		}

		role, err := kubernetes.GetRole(ctx, r.Client, hnp.GetAuthRoleName(), hnp.GetNamespace())
		if err == nil {
			role.SetLabels(hnp.GetNodePoolLabels())
			err = r.Client.Update(ctx, role)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to update auth role")
			}
		}
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "unable to get auth role")
			}
		}

		roleBinding, err := kubernetes.GetRoleBinding(ctx, r.Client, hnp.GetAuthRoleBindingName(), hnp.GetNamespace())
		if err == nil {
			roleBinding.SetLabels(hnp.GetNodePoolLabels())
			err = r.Client.Update(ctx, roleBinding)
			if err != nil {
				return r.logErrorAndReturn(err, "unable to update auth role binding")
			}
		}
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "unable to get auth role binding")
			}
		}
	}

	return nil
}

// cleanupUnusedTLSCertificates finds all existing per-node certificates for a specific HumioCluster
// and cleans them up if we have no use for them anymore.
func (r *HumioClusterReconciler) cleanupUnusedTLSSecrets(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	if !helpers.UseCertManager() {
		return reconcile.Result{}, nil
	}

	// because these secrets are created by cert-manager we cannot use our typical label selector
	foundSecretList, err := kubernetes.ListSecrets(ctx, r, hc.Namespace, client.MatchingLabels{})
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "unable to list secrets")
	}
	if len(foundSecretList) == 0 {
		return reconcile.Result{}, nil
	}

	for idx, secret := range foundSecretList {
		if !helpers.TLSEnabled(hc) {
			if secret.Type == corev1.SecretTypeOpaque {
				if secret.Name == fmt.Sprintf("%s-%s", hc.Name, "ca-keypair") ||
					secret.Name == fmt.Sprintf("%s-%s", hc.Name, "keystore-passphrase") {
					r.Log.Info(fmt.Sprintf("TLS is not enabled for cluster, removing unused secret: %s", secret.Name))
					if err := r.Delete(ctx, &foundSecretList[idx]); err != nil {
						return reconcile.Result{}, r.logErrorAndReturn(err, "could not delete TLS secret")
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
						if !*hc.Spec.TLS.Enabled {
							inUse = false
						}
					}
				}
			} else {
				// this is the per-node secret
				inUse, err = r.tlsCertSecretInUse(ctx, secret.Namespace, secret.Name)
				if err != nil {
					return reconcile.Result{}, r.logErrorAndReturn(err, "unable to determine if secret is in use")
				}
			}
			if !inUse {
				r.Log.Info(fmt.Sprintf("deleting secret %s", secret.Name))
				if err = r.Delete(ctx, &foundSecretList[idx]); err != nil {
					return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("could not delete secret %s", secret.Name))

				}
				return reconcile.Result{Requeue: true}, nil
			}
		}
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return reconcile.Result{}, nil
}

// cleanupUnusedCAIssuer deletes the the CA Issuer for a cluster if TLS has been disabled
func (r *HumioClusterReconciler) cleanupUnusedCAIssuer(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	if helpers.TLSEnabled(hc) {
		return reconcile.Result{}, nil
	}

	if !helpers.UseCertManager() {
		return reconcile.Result{}, nil
	}

	var existingCAIssuer cmapi.Issuer
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, &existingCAIssuer)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{Requeue: true}, r.logErrorAndReturn(err, "could not get CA Issuer")
	}

	r.Log.Info("found existing CA Issuer but cluster is configured without TLS, deleting CA Issuer")
	if err = r.Delete(ctx, &existingCAIssuer); err != nil {
		return reconcile.Result{Requeue: true}, r.logErrorAndReturn(err, "unable to delete CA Issuer")
	}

	return reconcile.Result{}, nil
}

// cleanupUnusedTLSCertificates finds all existing per-node certificates and cleans them up if we have no matching pod for them
func (r *HumioClusterReconciler) cleanupUnusedTLSCertificates(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	if !helpers.UseCertManager() {
		return reconcile.Result{}, nil
	}

	foundCertificateList, err := kubernetes.ListCertificates(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "unable to list certificates")
	}
	if len(foundCertificateList) == 0 {
		return reconcile.Result{}, nil
	}

	for idx, certificate := range foundCertificateList {
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
						if !*hc.Spec.TLS.Enabled {
							inUse = false
						}
					}
				}
			} else {
				// this is the per-node secret
				inUse, err = r.tlsCertSecretInUse(ctx, certificate.Namespace, certificate.Name)
				if err != nil {
					return reconcile.Result{}, r.logErrorAndReturn(err, "unable to determine if certificate is in use")
				}
			}
			if !inUse {
				r.Log.Info(fmt.Sprintf("deleting certificate %s", certificate.Name))
				if err = r.Delete(ctx, &foundCertificateList[idx]); err != nil {
					return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("could not delete certificate %s", certificate.Name))
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

	if k8serrors.IsNotFound(err) {
		return false, nil
	}
	return true, err
}

func (r *HumioClusterReconciler) getInitServiceAccountSecretName(ctx context.Context, hnp *HumioNodePool) (string, error) {
	foundInitServiceAccountSecretsList, err := kubernetes.ListSecrets(ctx, r, hnp.GetNamespace(), hnp.GetLabelsForSecret(hnp.GetInitServiceAccountSecretName()))
	if err != nil {
		return "", err
	}
	if len(foundInitServiceAccountSecretsList) == 0 {
		return "", nil
	}
	if len(foundInitServiceAccountSecretsList) > 1 {
		var secretNames []string
		for _, secret := range foundInitServiceAccountSecretsList {
			secretNames = append(secretNames, secret.Name)
		}
		return "", fmt.Errorf("found more than one init service account secret: %s", strings.Join(secretNames, ", "))
	}
	return foundInitServiceAccountSecretsList[0].Name, nil
}

func (r *HumioClusterReconciler) getAuthServiceAccountSecretName(ctx context.Context, hnp *HumioNodePool) (string, error) {
	foundAuthServiceAccountNameSecretsList, err := kubernetes.ListSecrets(ctx, r, hnp.GetNamespace(), hnp.GetLabelsForSecret(hnp.GetAuthServiceAccountSecretName()))
	if err != nil {
		return "", err
	}
	if len(foundAuthServiceAccountNameSecretsList) == 0 {
		return "", nil
	}
	if len(foundAuthServiceAccountNameSecretsList) > 1 {
		var secretNames []string
		for _, secret := range foundAuthServiceAccountNameSecretsList {
			secretNames = append(secretNames, secret.Name)
		}
		return "", fmt.Errorf("found more than one auth service account secret: %s", strings.Join(secretNames, ", "))
	}
	return foundAuthServiceAccountNameSecretsList[0].Name, nil
}

func (r *HumioClusterReconciler) ensureHumioServiceAccountAnnotations(ctx context.Context, hnp *HumioNodePool) (bool, error) {
	// Don't change the service account annotations if the service account is not managed by the operator
	if hnp.HumioServiceAccountIsSetByUser() {
		return false, nil
	}
	serviceAccountName := hnp.GetHumioServiceAccountName()
	serviceAccountAnnotations := hnp.GetHumioServiceAccountAnnotations()

	r.Log.Info(fmt.Sprintf("ensuring service account %s annotations", serviceAccountName))
	existingServiceAccount, err := kubernetes.GetServiceAccount(ctx, r, serviceAccountName, hnp.GetNamespace())
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, r.logErrorAndReturn(err, fmt.Sprintf("failed to get service account %s", serviceAccountName))
	}

	serviceAccount := kubernetes.ConstructServiceAccount(serviceAccountName, hnp.GetNamespace(), serviceAccountAnnotations, hnp.GetNodePoolLabels())
	serviceAccountAnnotationsString := helpers.MapToSortedString(serviceAccountAnnotations)
	existingServiceAccountAnnotationsString := helpers.MapToSortedString(existingServiceAccount.Annotations)
	if serviceAccountAnnotationsString != existingServiceAccountAnnotationsString {
		r.Log.Info(fmt.Sprintf("service account annotations do not match: annotations %s, got %s. updating service account %s",
			serviceAccountAnnotationsString, existingServiceAccountAnnotationsString, existingServiceAccount.Name))
		existingServiceAccount.Annotations = serviceAccount.Annotations
		if err = r.Update(ctx, existingServiceAccount); err != nil {
			return false, r.logErrorAndReturn(err, fmt.Sprintf("could not update service account %s", existingServiceAccount.Name))
		}

		// Trigger restart of humio to pick up the updated service account
		return true, nil

	}
	return false, nil
}

// ensureMismatchedPodsAreDeleted is used to delete pods which container spec does not match that which is desired.
// The behavior of this depends on what, if anything, was changed in the pod. If there are changes that fall under a
// rolling update, then the pod restart policy is set to PodRestartPolicyRolling and the reconciliation will continue if
// there are any pods not in a ready state. This is so replacement pods may be created.
// If there are changes that fall under a recreate update, the the pod restart policy is set to PodRestartPolicyRecreate
// and the reconciliation will requeue and the deletions will continue to be executed until all the pods have been
// removed.
func (r *HumioClusterReconciler) ensureMismatchedPodsAreDeleted(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) (reconcile.Result, error) {
	foundPodList, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to list pods")
	}

	// if we do not have any pods running we have nothing to delete
	if len(foundPodList) == 0 {
		return reconcile.Result{}, nil
	}

	r.Log.Info("ensuring mismatching pods are deleted")
	attachments := &podAttachments{}
	// In the case we are using PVCs, we cannot lookup the available PVCs since they may already be in use
	if hnp.DataVolumePersistentVolumeClaimSpecTemplateIsSetByUser() {
		attachments.dataVolumeSource = hnp.GetDataVolumePersistentVolumeClaimSpecTemplate("")
	}

	podsStatus, err := r.getPodsStatus(hnp, foundPodList)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to get pod status")
	}

	envVarSourceData, err := r.getEnvVarSource(ctx, hnp)
	if err != nil {
		result, _ := r.updateStatus(r.Client.Status(), hc, statusOptions().
			withMessage(r.logErrorAndReturn(err, "got error when getting pod envVarSource").Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
		return result, err
	}
	if envVarSourceData != nil {
		attachments.envVarSourceData = envVarSourceData
	}

	// prioritize deleting the pods with errors
	var podList []corev1.Pod
	if podsStatus.havePodsWithContainerStateWaitingErrors() {
		r.Log.Info(fmt.Sprintf("found %d humio pods with errors", len(podsStatus.podErrors)))
		podList = podsStatus.podErrors
	} else {
		podList = foundPodList
	}
	desiredLifecycleState, err := r.getPodDesiredLifecycleState(hnp, podList, attachments)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "got error when getting pod desired lifecycle")
	}

	// If we are currently deleting pods, then check if the cluster state is Running or in a ConfigError state. If it
	// is, then change to an appropriate state depending on the restart policy.
	// If the cluster state is set as per the restart policy:
	// 	 PodRestartPolicyRecreate == HumioClusterStateUpgrading
	// 	 PodRestartPolicyRolling == HumioClusterStateRestarting
	if hc.Status.State == humiov1alpha1.HumioClusterStateRunning || hc.Status.State == humiov1alpha1.HumioClusterStateConfigError {
		if desiredLifecycleState.WantsUpgrade() {
			r.Log.Info(fmt.Sprintf("changing cluster state from %s to %s", hc.Status.State, humiov1alpha1.HumioClusterStateUpgrading))
			if result, err := r.updateStatus(r.Client.Status(), hc, statusOptions().
				withNodePoolState(humiov1alpha1.HumioClusterStateUpgrading, hnp.GetNodePoolName())); err != nil {
				return result, err
			}
			if revision, err := r.incrementHumioClusterPodRevision(ctx, hc, hnp); err != nil {
				return r.updateStatus(r.Client.Status(), hc, statusOptions().
					withMessage(r.logErrorAndReturn(err, fmt.Sprintf("failed to increment pod revision to %d", revision)).Error()))
			}
		}
		if !desiredLifecycleState.WantsUpgrade() && desiredLifecycleState.WantsRestart() {
			if result, err := r.updateStatus(r.Client.Status(), hc, statusOptions().
				withNodePoolState(humiov1alpha1.HumioClusterStateRestarting, hnp.GetNodePoolName())); err != nil {
				return result, err
			}
			if revision, err := r.incrementHumioClusterPodRevision(ctx, hc, hnp); err != nil {
				return r.updateStatus(r.Client.Status(), hc, statusOptions().
					withMessage(r.logErrorAndReturn(err, fmt.Sprintf("failed to increment pod revision to %d", revision)).Error()))
			}
		}
	}
	if desiredLifecycleState.ShouldDeletePod() {
		if hc.Status.State == humiov1alpha1.HumioClusterStateRestarting && podsStatus.waitingOnPods() && desiredLifecycleState.ShouldRollingRestart() {
			r.Log.Info(fmt.Sprintf("pod %s should be deleted, but waiting because not all other pods are "+
				"ready. waitingOnPods=%v, clusterState=%s", desiredLifecycleState.pod.Name,
				podsStatus.waitingOnPods(), hc.Status.State))
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage("waiting for pods to become ready"))
		}

		if hc.Status.State == humiov1alpha1.HumioClusterStateUpgrading && podsStatus.waitingOnPods() && desiredLifecycleState.ShouldRollingRestart() {
			r.Log.Info(fmt.Sprintf("pod %s should be deleted, but waiting because not all other pods are "+
				"ready. waitingOnPods=%v, clusterState=%s", desiredLifecycleState.pod.Name,
				podsStatus.waitingOnPods(), hc.Status.State))
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage("waiting for pods to become ready"))
		}

		r.Log.Info(fmt.Sprintf("deleting pod %s", desiredLifecycleState.pod.Name))
		if err = r.Delete(ctx, &desiredLifecycleState.pod); err != nil {
			return r.updateStatus(r.Client.Status(), hc, statusOptions().
				withMessage(r.logErrorAndReturn(err, fmt.Sprintf("could not delete pod %s", desiredLifecycleState.pod.Name)).Error()))
		}
	} else {
		if desiredLifecycleState.WantsUpgrade() {
			r.Log.Info(fmt.Sprintf("pod %s should be deleted because cluster upgrade is wanted but refusing due to the configured upgrade strategy",
				desiredLifecycleState.pod.Name))
		} else if desiredLifecycleState.WantsRestart() {
			r.Log.Info(fmt.Sprintf("pod %s should be deleted because cluster restart is wanted but refusing due to the configured upgrade strategy",
				desiredLifecycleState.pod.Name))
		}
	}

	// If we allow a rolling update, then don't take down more than one pod at a time.
	// Check the number of ready pods. if we have already deleted a pod, then the ready count will less than expected,
	// but we must continue with reconciliation so the pod may be created later in the reconciliation.
	// If we're doing a non-rolling update (recreate), then we can take down all the pods without waiting, but we will
	// wait until all the pods are ready before changing the cluster state back to Running.
	// If we are no longer waiting on or deleting pods, and all the revisions are in sync, then we know the upgrade or
	// restart is complete and we can set the cluster state back to HumioClusterStateRunning.
	// It's possible we entered a ConfigError state during an upgrade or restart, and in this case, we should reset the
	// state to Running if the the pods are healthy but we're in a ConfigError state.
	if !podsStatus.waitingOnPods() && !desiredLifecycleState.WantsUpgrade() && !desiredLifecycleState.WantsRestart() && podsStatus.podRevisionsInSync() {
		if hc.Status.State == humiov1alpha1.HumioClusterStateRestarting || hc.Status.State == humiov1alpha1.HumioClusterStateUpgrading || hc.Status.State == humiov1alpha1.HumioClusterStateConfigError {
			r.Log.Info(fmt.Sprintf("no longer deleting pods. changing cluster state from %s to %s", hc.Status.State, humiov1alpha1.HumioClusterStateRunning))
			if result, err := r.updateStatus(r.Client.Status(), hc, statusOptions().
				withNodePoolState(humiov1alpha1.HumioClusterStateRunning, hnp.GetNodePoolName())); err != nil {
				return result, err
			}
		}
	}

	r.Log.Info(fmt.Sprintf("cluster state is still %s. waitingOnPods=%v, podBeingDeleted=%v, "+
		"revisionsInSync=%v, podRevisisons=%v, podDeletionTimestampSet=%v, podNames=%v, podHumioVersions=%v, expectedRunningPods=%v, podsReady=%v, podsNotReady=%v",
		hc.Status.State, podsStatus.waitingOnPods(), desiredLifecycleState.ShouldDeletePod(), podsStatus.podRevisionsInSync(),
		podsStatus.podRevisions, podsStatus.podDeletionTimestampSet, podsStatus.podNames, podsStatus.podImageVersions, podsStatus.expectedRunningPods, podsStatus.readyCount, podsStatus.notReadyCount))

	// If we have pods being deleted, requeue as long as we're not doing a rolling update. This will ensure all pods
	// are removed before creating the replacement pods.
	if hc.Status.State == humiov1alpha1.HumioClusterStateUpgrading && desiredLifecycleState.ShouldDeletePod() && !desiredLifecycleState.ShouldRollingRestart() {
		return reconcile.Result{RequeueAfter: time.Second + 1}, nil
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) ingressesMatch(ingress *networkingv1.Ingress, desiredIngress *networkingv1.Ingress) bool {
	// Kubernetes 1.18 introduced a new field, PathType. For older versions PathType is returned as nil,
	// so we explicitly set the value before comparing ingress objects.
	// When minimum supported Kubernetes version is 1.18, we can drop this.
	pathTypeImplementationSpecific := networkingv1.PathTypeImplementationSpecific
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

	ingressAnnotations := helpers.MapToSortedString(ingress.Annotations)
	desiredIngressAnnotations := helpers.MapToSortedString(desiredIngress.Annotations)
	if ingressAnnotations != desiredIngressAnnotations {
		r.Log.Info(fmt.Sprintf("ingress annotations do not match: got %s, wanted %s", ingressAnnotations, desiredIngressAnnotations))
		return false
	}
	return true
}

func (r *HumioClusterReconciler) ensurePodsExist(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) (reconcile.Result, error) {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	foundPodList, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to list pods")
	}

	if len(foundPodList) < hnp.GetNodeCount() {
		attachments, err := r.newPodAttachments(ctx, hnp, foundPodList)
		if err != nil {
			return reconcile.Result{RequeueAfter: time.Second * 5}, r.logErrorAndReturn(err, "failed to get pod attachments")
		}
		pod, err := r.createPod(ctx, hc, hnp, attachments)
		if err != nil {
			return reconcile.Result{RequeueAfter: time.Second * 5}, r.logErrorAndReturn(err, "unable to create pod")
		}
		humioClusterPrometheusMetrics.Counters.PodsCreated.Inc()

		// check that we can list the new pod
		// this is to avoid issues where the requeue is faster than kubernetes
		if err := r.waitForNewPod(ctx, hnp, foundPodList, pod); err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "failed to validate new pod")
		}

		// We have created a pod. Requeue immediately even if the pod is not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	// TODO: what should happen if we have more pods than are expected?
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) ensurePersistentVolumeClaimsExist(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	if !hnp.PVCsEnabled() {
		r.Log.Info("pvcs are disabled. skipping")
		return nil
	}

	r.Log.Info("ensuring pvcs")
	foundPersistentVolumeClaims, err := kubernetes.ListPersistentVolumeClaims(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return r.logErrorAndReturn(err, "failed to list pvcs")
	}
	r.Log.Info(fmt.Sprintf("found %d pvcs", len(foundPersistentVolumeClaims)))

	if len(foundPersistentVolumeClaims) < hnp.GetNodeCount() {
		r.Log.Info(fmt.Sprintf("pvc count of %d is less than %d. adding more", len(foundPersistentVolumeClaims), hnp.GetNodeCount()))
		pvc := constructPersistentVolumeClaim(hnp)
		pvc.Annotations[pvcHashAnnotation] = helpers.AsSHA256(pvc.Spec)
		if err := controllerutil.SetControllerReference(hc, pvc, r.Scheme()); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		r.Log.Info(fmt.Sprintf("creating pvc: %s", pvc.Name))
		if err = r.Create(ctx, pvc); err != nil {
			return r.logErrorAndReturn(err, "unable to create pvc")
		}
		r.Log.Info(fmt.Sprintf("successfully created pvc %s for HumioCluster %s", pvc.Name, hnp.GetNodePoolName()))
		humioClusterPrometheusMetrics.Counters.PvcsCreated.Inc()

		if err = r.waitForNewPvc(ctx, hnp, pvc); err != nil {
			return r.logErrorAndReturn(err, "unable to create pvc")
		}
		return nil
	}

	// TODO: what should happen if we have more pvcs than are expected?
	return nil
}

func (r *HumioClusterReconciler) ensureValidHumioVersion(hnp *HumioNodePool) error {
	hv, err := HumioVersionFromString(hnp.GetImage())
	if ok, _ := hv.AtLeast(HumioVersionMinimumSupported); !ok {
		return r.logErrorAndReturn(fmt.Errorf("unsupported Humio version: %s", hv.version.String()), fmt.Sprintf("Humio version must be at least %s", HumioVersionMinimumSupported))
	}
	if err != nil {
		return r.logErrorAndReturn(err, fmt.Sprintf("detected invalid Humio version: %s", hv.version))

	}
	return nil
}

func (r *HumioClusterReconciler) ensureValidStorageConfiguration(hc *humiov1alpha1.HumioCluster) error {
	errInvalidStorageConfiguration := fmt.Errorf("exactly one of dataVolumeSource and dataVolumePersistentVolumeClaimSpecTemplate must be set")

	emptyVolumeSource := corev1.VolumeSource{}
	emptyDataVolumePersistentVolumeClaimSpecTemplate := corev1.PersistentVolumeClaimSpec{}

	if reflect.DeepEqual(hc.Spec.DataVolumeSource, emptyVolumeSource) &&
		reflect.DeepEqual(hc.Spec.DataVolumePersistentVolumeClaimSpecTemplate, emptyDataVolumePersistentVolumeClaimSpecTemplate) {
		return r.logErrorAndReturn(errInvalidStorageConfiguration, "no storage configuration provided")
	}

	if !reflect.DeepEqual(hc.Spec.DataVolumeSource, emptyVolumeSource) &&
		!reflect.DeepEqual(hc.Spec.DataVolumePersistentVolumeClaimSpecTemplate, emptyDataVolumePersistentVolumeClaimSpecTemplate) {
		return r.logErrorAndReturn(errInvalidStorageConfiguration, "conflicting storage configuration provided")
	}

	return nil
}

func (r *HumioClusterReconciler) pvcList(ctx context.Context, hnp *HumioNodePool) ([]corev1.PersistentVolumeClaim, error) {
	if hnp.PVCsEnabled() {
		return kubernetes.ListPersistentVolumeClaims(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	}
	return []corev1.PersistentVolumeClaim{}, nil
}

func (r *HumioClusterReconciler) getLicenseString(ctx context.Context, hc *humiov1alpha1.HumioCluster) (string, error) {
	licenseSecretKeySelector := licenseSecretKeyRefOrDefault(hc)
	if licenseSecretKeySelector == nil {
		return "", fmt.Errorf("no license secret key selector provided")
	}

	licenseSecret, err := kubernetes.GetSecret(ctx, r, licenseSecretKeySelector.Name, hc.Namespace)
	if err != nil {
		return "", r.logErrorAndReturn(err, "could not get license")
	}
	if _, ok := licenseSecret.Data[licenseSecretKeySelector.Key]; !ok {
		return "", r.logErrorAndReturn(err, "could not get license")
	}

	if hc.Status.State == humiov1alpha1.HumioClusterStateConfigError {
		if _, err := r.updateStatus(r.Client.Status(), hc, statusOptions().
			withState(humiov1alpha1.HumioClusterStateRunning)); err != nil {
			r.Log.Error(err, fmt.Sprintf("failed to set state to %s", humiov1alpha1.HumioClusterStateRunning))
		}
	}

	return string(licenseSecret.Data[licenseSecretKeySelector.Key]), nil
}

func (r *HumioClusterReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
