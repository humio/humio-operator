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

package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/controller/versions"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioClusterReconciler reconciles a HumioCluster object
type HumioClusterReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

type ctxHumioClusterPoolFunc func(context.Context, *humiov1alpha1.HumioCluster, *HumioNodePool) error
type ctxHumioClusterFunc func(context.Context, *humiov1alpha1.HumioCluster) error

const (
	// MaximumMinReadyRequeue The maximum requeue time to set for the MinReadySeconds functionality - this is to avoid a scenario where we
	// requeue for hours into the future.
	MaximumMinReadyRequeue = time.Second * 300

	// waitingOnPodsMessage is the message that is populated as the message in the cluster status when waiting on pods
	waitingOnPodsMessage = "waiting for pods to become ready"

	humioVersionMinimumForReliableDownscaling = "1.173.0"
)

// +kubebuilder:rbac:groups=core.humio.com,resources=humioclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioclusters/finalizers,verbs=update
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

// nolint:gocyclo
func (r *HumioClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// when running tests, ignore resources that are not in the correct namespace
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues(
		"Request.Namespace", req.Namespace,
		"Request.Name", req.Name,
		"Request.Type", helpers.GetTypeName(r),
		"Reconcile.ID", kubernetes.RandomString(),
	)
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

	r.Log = r.Log.WithValues("Request.UID", hc.UID)
	humioNodePools := getHumioNodePoolManagers(hc)
	emptyResult := reconcile.Result{}

	// update status with observed generation
	// TODO: Look into refactoring of the use of "defer func's" to update HumioCluster.Status.
	//       Right now we use StatusWriter to update the status multiple times, and rely on RetryOnConflict to retry
	//       on conflicts which they'll be on many of the status updates.
	//       We should be able to bundle all the options together and do a single update using StatusWriter.
	//       Bundling options in a single StatusWriter.Update() should help reduce the number of conflicts.
	defer func(ctx context.Context, hc *humiov1alpha1.HumioCluster) {
		_, _ = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withObservedGeneration(hc.GetGeneration()))
	}(ctx, hc)

	// validate details in HumioCluster resource is valid
	if result, err := r.verifyHumioClusterConfigurationIsValid(ctx, hc, humioNodePools); result != emptyResult || err != nil {
		return result, err
	}

	// if the state is not set yet, we know config is valid and mark it as Running
	if hc.Status.State == "" {
		err := r.setState(ctx, humiov1alpha1.HumioClusterStateRunning, hc)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "unable to set cluster state")
		}
	}

	// create HumioBootstrapToken and block until we have a hashed bootstrap token
	if result, err := r.ensureHumioClusterBootstrapToken(ctx, hc); result != emptyResult || err != nil {
		if err != nil {
			_, _ = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
		return result, err
	}

	// update status with pods and nodeCount based on podStatusList
	defer func(ctx context.Context, hc *humiov1alpha1.HumioCluster) {
		opts := statusOptions()
		podStatusList, err := r.getPodStatusList(ctx, hc, humioNodePools.Filter(NodePoolFilterHasNode))
		if err != nil {
			r.Log.Error(err, "unable to get pod status list")
		}
		_, _ = r.updateStatus(ctx, r.Client.Status(), hc, opts.
			withPods(podStatusList).
			withNodeCount(len(podStatusList)))
	}(ctx, hc)

	// remove unused node pool status entries
	// TODO: This should be moved to cleanupUnusedResources, but nodePoolAllowsMaintenanceOperations fails
	//       to indicate there's a node pool status in maintenance if the node pool is no longer configured
	//       by the user. When nodePoolAllowsMaintenanceOperations is updated to properly indicate something
	//       marked as under maintenance, even if no longer a node pool specified by the user, then we should
	//       move this to cleanupUnusedResources.
	if ok, idx := r.hasNoUnusedNodePoolStatus(hc, &humioNodePools); !ok {
		r.cleanupUnusedNodePoolStatus(hc, idx)
		if result, err := r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withNodePoolStatusList(hc.Status.NodePoolStatus)); err != nil {
			return result, r.logErrorAndReturn(err, "unable to set cluster state")
		}
	}

	// ensure pods that does not run the desired version or config gets deleted and update state accordingly
	for _, pool := range humioNodePools.Items {
		if r.nodePoolAllowsMaintenanceOperations(hc, pool, humioNodePools.Items) {
			result, err := r.ensureMismatchedPodsAreDeleted(ctx, hc, pool)
			if result != emptyResult || err != nil {
				return result, err
			}
		}
	}

	// create various k8s objects, e.g. Issuer, Certificate, ConfigMap, Ingress, Service, ServiceAccount, ClusterRole, ClusterRoleBinding
	for _, fun := range []ctxHumioClusterFunc{
		r.ensureValidCAIssuer,
		r.ensureHumioClusterCACertBundle,
		r.ensureHumioClusterKeystoreSecret,
		r.ensureNoIngressesIfIngressNotEnabled, // TODO: cleanupUnusedResources seems like a better place for this
		r.ensureIngress,
		r.ensurePdfRenderService,
	} {
		if err := fun(ctx, hc); err != nil {
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}
	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		for _, fun := range []ctxHumioClusterPoolFunc{
			r.ensureService,
			r.ensureHumioPodPermissions,
			r.ensureInitContainerPermissions,
			r.ensureHumioNodeCertificates,
			r.ensureExtraKafkaConfigsConfigMap,
			r.ensureViewGroupPermissionsConfigMap,
			r.ensureRolePermissionsConfigMap,
			r.reconcileSinglePDB,
		} {
			if err := fun(ctx, hc, pool); err != nil {
				return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
					withMessage(err.Error()))
			}
		}
	}

	// update annotations on ServiceAccount object and trigger pod restart if annotations were changed
	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if issueRestart, err := r.ensureHumioServiceAccountAnnotations(ctx, pool); err != nil || issueRestart {
			desiredPodRevision := pool.GetDesiredPodRevision()
			if issueRestart {
				// TODO: Code seems to only try to save the updated pod revision in the same reconcile as the annotations on the ServiceAccount was updated.
				//       We should ensure that even if we don't store it in the current reconcile, we'll still properly detect it next time and retry storing this updated pod revision.
				//       Looks like a candidate for storing a ServiceAccount annotation hash in node pool status, similar to pod hash, bootstrap token hash, etc.
				//       as this way we'd both store the updated hash *and* the updated pod revision in the same k8sClient.Update() API call.
				desiredPodRevision++
			}
			_, err = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withNodePoolState(hc.Status.State, pool.GetNodePoolName(), desiredPodRevision, pool.GetDesiredPodHash(), pool.GetDesiredBootstrapTokenHash(), ""))
			return reconcile.Result{Requeue: true}, err
		}
	}

	// create pvcs if needed
	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if err := r.ensurePersistentVolumeClaimsExist(ctx, hc, pool); err != nil {
			opts := statusOptions()
			if hc.Status.State != humiov1alpha1.HumioClusterStateRestarting && hc.Status.State != humiov1alpha1.HumioClusterStateUpgrading {
				opts.withNodePoolState(humiov1alpha1.HumioClusterStatePending, pool.GetNodePoolName(), pool.GetDesiredPodRevision(), pool.GetDesiredPodHash(), pool.GetDesiredBootstrapTokenHash(), pool.GetZoneUnderMaintenance())
			}
			return r.updateStatus(ctx, r.Client.Status(), hc, opts.
				withMessage(err.Error()))
		}
	}

	// create pods if needed
	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if r.nodePoolAllowsMaintenanceOperations(hc, pool, humioNodePools.Items) {
			if result, err := r.ensurePodsExist(ctx, hc, pool); result != emptyResult || err != nil {
				if err != nil {
					_, _ = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
						withMessage(err.Error()))
				}
				return result, err
			}
		}
	}

	// wait for pods to start up
	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if podsReady, err := r.nodePoolPodsReady(ctx, hc, pool); !podsReady || err != nil {
			msg := waitingOnPodsMessage
			if err != nil {
				msg = err.Error()
			}
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withState(hc.Status.State).
				withMessage(msg))
		}
	}

	// wait for license and admin token
	if len(r.currentlyConfiguredNodePoolsInMaintenance(hc, humioNodePools.Filter(NodePoolFilterHasNode))) == 0 {
		if result, err := r.ensureLicenseAndAdminToken(ctx, hc, req); result != emptyResult || err != nil {
			if err != nil {
				_, _ = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
					withMessage(r.logErrorAndReturn(err, "unable to ensure license is installed and admin token is created").Error()))
			}
			// Usually if we fail to get the license, that means the cluster is not up. So wait a bit longer than usual to retry
			return reconcile.Result{RequeueAfter: time.Second * 15}, nil
		}
	}

	// construct humioClient configured with the admin token
	cluster, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withMessage(r.logErrorAndReturn(err, "unable to obtain humio client config").Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// update status with version
	defer func(ctx context.Context, humioClient humio.Client, hc *humiov1alpha1.HumioCluster) {
		opts := statusOptions()
		if hc.Status.State == humiov1alpha1.HumioClusterStateRunning {
			status, err := humioClient.Status(ctx, humioHttpClient, req)
			if err != nil {
				r.Log.Error(err, "unable to get cluster status")
				return
			}
			_, _ = r.updateStatus(ctx, r.Client.Status(), hc, opts.withVersion(status.Version))
		}
	}(ctx, r.HumioClient, hc)

	// downscale cluster if needed
	// Feature is only available for LogScale versions >= v1.173.0
	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		// Check if downscaling feature flag is enabled
		if pool.IsDownscalingFeatureEnabled() && r.nodePoolAllowsMaintenanceOperations(hc, pool, humioNodePools.Items) {
			if result, err := r.processDownscaling(ctx, hc, pool, req); result != emptyResult || err != nil {
				if err != nil {
					_, _ = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
						withMessage(err.Error()))
				}
				return result, err
			}
		}
	}

	// clean up various k8s objects we no longer need
	if result, err := r.cleanupUnusedResources(ctx, hc, humioNodePools); result != emptyResult || err != nil {
		return result, err
	}

	r.Log.Info("done reconciling")
	return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().withState(hc.Status.State).withMessage(""))
}

// ensurePdfRenderService ensures the pdf-render-service resource exists
func (r *HumioClusterReconciler) ensurePdfRenderService(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	// Check if PDF render service is enabled (disabled by default)
	if hc.Spec.EnablePdfRenderService == nil || !*hc.Spec.EnablePdfRenderService {
		// If not explicitly enabled, ensure any existing PDF render service is removed
		return r.removePdfRenderServiceIfExists(ctx, hc)
	}

	pdfService := &humiov1alpha1.HumioPdfRenderService{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name + "-pdf-render-service",
	}, pdfService)

	// Use the helper function to get the image from environment variable
	pdfRenderServiceImage := versions.DefaultPDFRenderServiceImage()
	if hc.Spec.PdfRenderServiceImage != "" {
		pdfRenderServiceImage = hc.Spec.PdfRenderServiceImage
	}

	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Create new pdf-render-service with TLS copied from cluster
			newPdfService := &humiov1alpha1.HumioPdfRenderService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      hc.Name + "-pdf-render-service",
					Namespace: hc.Namespace,
					Labels:    map[string]string{"humio_cluster": hc.Name},
				},
				Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
					TLS:      hc.Spec.TLS,
					Image:    pdfRenderServiceImage,
					Replicas: 1,
				},
			}
			if err := controllerutil.SetControllerReference(hc, newPdfService, r.Scheme()); err != nil {
				return err
			}
			r.Log.Info("Creating HumioPdfRenderService with name",
				"name", hc.Name+"-pdf-render-service",
				"namespace", hc.Namespace)
			return r.Create(ctx, newPdfService)
		}
		return err
	}

	// Already exists, check if TLS matches
	if !reflect.DeepEqual(pdfService.Spec.TLS, hc.Spec.TLS) {
		pdfService.Spec.TLS = hc.Spec.TLS
		r.Log.Info("Updating HumioPdfRenderService TLS settings", "name", pdfService.Name)
		return r.Update(ctx, pdfService)
	}

	return nil
}

// removePdfRenderServiceIfExists removes any existing PDF render service for this cluster
func (r *HumioClusterReconciler) removePdfRenderServiceIfExists(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	pdfService := &humiov1alpha1.HumioPdfRenderService{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name + "-pdf-render-service",
	}, pdfService)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			// PDF render service doesn't exist, nothing to do
			return nil
		}
		return err
	}

	// PDF render service exists but is not enabled, so remove it
	r.Log.Info("Removing HumioPdfRenderService as it's not enabled", "name", pdfService.Name)
	return r.Delete(ctx, pdfService)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioCluster{}).
		Named("humiocluster").
		Owns(&corev1.Pod{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&networkingv1.Ingress{}).
		Complete(r)
}

func (r *HumioClusterReconciler) nodePoolPodsReady(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) (bool, error) {
	foundPodList, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return false, r.logErrorAndReturn(err, "failed to list pods")
	}
	podsStatus, err := r.getPodsStatus(ctx, hc, hnp, foundPodList)
	if err != nil {
		return false, r.logErrorAndReturn(err, "failed to get pod status")
	}
	if podsStatus.waitingOnPods() {
		r.Log.Info("waiting on pods, refusing to continue with reconciliation until all pods are ready")
		r.Log.Info(fmt.Sprintf("cluster state is %s. waitingOnPods=%v, "+
			"revisionsInSync=%v, podRevisions=%v, podDeletionTimestampSet=%v, podNames=%v, expectedRunningPods=%v, "+
			"podsReady=%v, podsNotReady=%v",
			hc.Status.State, podsStatus.waitingOnPods(), podsStatus.podRevisionCountMatchesNodeCountAndAllPodsHaveRevision(hnp.GetDesiredPodRevision()),
			podsStatus.podRevisions, podsStatus.podDeletionTimestampSet, podsStatus.podNames,
			podsStatus.nodeCount, podsStatus.readyCount, podsStatus.notReadyCount))
		return false, nil
	}
	return true, nil
}

// nodePoolAllowsMaintenanceOperations fetches which node pools that are still defined, that are marked as in
// maintenance, and returns true if hnp is present in that list.
func (r *HumioClusterReconciler) nodePoolAllowsMaintenanceOperations(hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, hnps []*HumioNodePool) bool {
	poolsInMaintenance := r.currentlyConfiguredNodePoolsInMaintenance(hc, hnps)
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

// currentlyConfiguredNodePoolsInMaintenance loops through the desired node pools, and returns all node pools with state not Running
func (r *HumioClusterReconciler) currentlyConfiguredNodePoolsInMaintenance(hc *humiov1alpha1.HumioCluster, hnps []*HumioNodePool) []*HumioNodePool {
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

func (r *HumioClusterReconciler) cleanupUnusedNodePoolStatus(hc *humiov1alpha1.HumioCluster, idx int) {
	r.Log.Info(fmt.Sprintf("removing node pool %s from node pool status list", hc.Status.NodePoolStatus[idx].Name))
	hc.Status.NodePoolStatus = append(hc.Status.NodePoolStatus[:idx], hc.Status.NodePoolStatus[idx+1:]...)
}

func (r *HumioClusterReconciler) hasNoUnusedNodePoolStatus(hc *humiov1alpha1.HumioCluster, hnps *HumioNodePoolList) (bool, int) {
	for idx, poolStatus := range hc.Status.NodePoolStatus {
		var validPool bool
		for _, pool := range hnps.Items {
			if poolStatus.Name == pool.GetNodePoolName() && pool.GetNodeCount() > 0 {
				validPool = true
			}
		}
		if !validPool {
			r.Log.Info(fmt.Sprintf("node pool %s is not valid", poolStatus.Name))
			return false, idx
		}
	}
	return true, 0
}

func (r *HumioClusterReconciler) ensureHumioClusterBootstrapToken(ctx context.Context, hc *humiov1alpha1.HumioCluster) (reconcile.Result, error) {
	r.Log.Info("ensuring humiobootstraptoken")
	hbtList, err := kubernetes.ListHumioBootstrapTokens(ctx, r.Client, hc.GetNamespace(), kubernetes.LabelsForHumioBootstrapToken(hc.GetName()))
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not list HumioBootstrapToken")
	}
	if len(hbtList) > 0 {
		r.Log.Info("humiobootstraptoken already exists, checking if HumioBootstrapTokenReconciler populated it")
		if hbtList[0].Status.State == humiov1alpha1.HumioBootstrapTokenStateReady {
			return reconcile.Result{}, nil
		}
		r.Log.Info("secret not populated yet, waiting on HumioBootstrapTokenReconciler")
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}

	hbt := kubernetes.ConstructHumioBootstrapToken(hc.GetName(), hc.GetNamespace())
	if err := controllerutil.SetControllerReference(hc, hbt, r.Scheme()); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not set controller reference")
	}
	r.Log.Info(fmt.Sprintf("creating humiobootstraptoken %s", hbt.Name))
	err = r.Create(ctx, hbt)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not create bootstrap token resource")
	}

	return reconcile.Result{Requeue: true}, nil
}

func (r *HumioClusterReconciler) validateInitialPodSpec(hnp *HumioNodePool) error {
	if _, err := ConstructPod(hnp, "", &podAttachments{}); err != nil {
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

	desiredConfigMap := kubernetes.ConstructExtraKafkaConfigsConfigMap(
		hnp.GetExtraKafkaConfigsConfigMapName(),
		ExtraKafkaPropertiesFilename,
		extraKafkaConfigsConfigMapData,
		hnp.GetClusterName(),
		hnp.GetNamespace(),
	)
	if err := controllerutil.SetControllerReference(hc, &desiredConfigMap, r.Scheme()); err != nil {
		return r.logErrorAndReturn(err, "could not set controller reference")
	}

	existingConfigMap, err := kubernetes.GetConfigMap(ctx, r, hnp.GetExtraKafkaConfigsConfigMapName(), hnp.GetNamespace())
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info(fmt.Sprintf("creating configMap: %s", desiredConfigMap.Name))
			if err = r.Create(ctx, &desiredConfigMap); err != nil {
				return r.logErrorAndReturn(err, "unable to create extra kafka configs configmap")
			}
			r.Log.Info(fmt.Sprintf("successfully created extra kafka configs configmap name %s", desiredConfigMap.Name))
			humioClusterPrometheusMetrics.Counters.ConfigMapsCreated.Inc()
			return nil
		}
		return r.logErrorAndReturn(err, "unable to fetch extra kafka configs configmap")
	}

	if !equality.Semantic.DeepEqual(existingConfigMap.Data, desiredConfigMap.Data) {
		existingConfigMap.Data = desiredConfigMap.Data
		if updateErr := r.Update(ctx, &existingConfigMap); updateErr != nil {
			return fmt.Errorf("unable to update extra kafka configs configmap: %w", updateErr)
		}
	}

	return nil
}

// getEnvVarSource returns the environment variables from either the configMap or secret that is referenced by envVarSource
func (r *HumioClusterReconciler) getEnvVarSource(ctx context.Context, hnp *HumioNodePool) (*map[string]string, error) {
	var envVarConfigMapName string
	var envVarSecretName string
	fullEnvVarKeyValues := map[string]string{}
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
			for k, v := range configMap.Data {
				fullEnvVarKeyValues[k] = v
			}
		}
		if envVarSource.SecretRef != nil {
			envVarSecretName = envVarSource.SecretRef.Name
			secret, err := kubernetes.GetSecret(ctx, r, envVarSecretName, hnp.GetNamespace())
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return nil, fmt.Errorf("environmentVariablesSource was set but no secret exists by name %s in namespace %s", envVarSecretName, hnp.GetNamespace())
				}
				return nil, fmt.Errorf("unable to get secret with name %s in namespace %s", envVarSecretName, hnp.GetNamespace())
			}
			for k, v := range secret.Data {
				fullEnvVarKeyValues[k] = string(v)
			}
		}
	}
	if len(fullEnvVarKeyValues) == 0 {
		return nil, nil
	}
	return &fullEnvVarKeyValues, nil
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
func (r *HumioClusterReconciler) ensureViewGroupPermissionsConfigMap(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	viewGroupPermissionsConfigMapData := hnp.GetViewGroupPermissions()
	if viewGroupPermissionsConfigMapData == "" {
		return nil
	}

	desiredConfigMap := kubernetes.ConstructViewGroupPermissionsConfigMap(
		hnp.GetViewGroupPermissionsConfigMapName(),
		ViewGroupPermissionsFilename,
		viewGroupPermissionsConfigMapData,
		hc.Name,
		hc.Namespace,
	)
	if err := controllerutil.SetControllerReference(hc, &desiredConfigMap, r.Scheme()); err != nil {
		return r.logErrorAndReturn(err, "could not set controller reference")
	}

	existingConfigMap, err := kubernetes.GetConfigMap(ctx, r, hnp.GetViewGroupPermissionsConfigMapName(), hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info(fmt.Sprintf("creating configMap: %s", desiredConfigMap.Name))
			if err = r.Create(ctx, &desiredConfigMap); err != nil {
				return r.logErrorAndReturn(err, "unable to create view group permissions configmap")
			}
			r.Log.Info(fmt.Sprintf("successfully created view group permissions configmap name %s", desiredConfigMap.Name))
			humioClusterPrometheusMetrics.Counters.ConfigMapsCreated.Inc()
			return nil
		}
		return fmt.Errorf("unable to fetch view group permissions configmap: %w", err)
	}

	if !equality.Semantic.DeepEqual(existingConfigMap.Data, desiredConfigMap.Data) {
		existingConfigMap.Data = desiredConfigMap.Data
		if updateErr := r.Update(ctx, &existingConfigMap); updateErr != nil {
			return fmt.Errorf("unable to update view group permissions configmap: %w", updateErr)
		}
	}

	return nil
}

// ensureRolePermissionsConfigMap creates a configmap containing configs specified in rolePermissions which will be mounted
// into the Humio container and used by Humio's configuration option READ_GROUP_PERMISSIONS_FROM_FILE
func (r *HumioClusterReconciler) ensureRolePermissionsConfigMap(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	rolePermissionsConfigMapData := hnp.GetRolePermissions()
	if rolePermissionsConfigMapData == "" {
		return nil
	}

	desiredConfigMap := kubernetes.ConstructRolePermissionsConfigMap(
		hnp.GetRolePermissionsConfigMapName(),
		RolePermissionsFilename,
		rolePermissionsConfigMapData,
		hc.Name,
		hc.Namespace,
	)
	if err := controllerutil.SetControllerReference(hc, &desiredConfigMap, r.Scheme()); err != nil {
		return r.logErrorAndReturn(err, "could not set controller reference")
	}

	existingConfigMap, err := kubernetes.GetConfigMap(ctx, r, hnp.GetRolePermissionsConfigMapName(), hc.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info(fmt.Sprintf("creating configMap: %s", desiredConfigMap.Name))
			if createErr := r.Create(ctx, &desiredConfigMap); createErr != nil {
				return r.logErrorAndReturn(createErr, "unable to create role permissions configmap")
			}
			r.Log.Info(fmt.Sprintf("successfully created role permissions configmap name %s", desiredConfigMap.Name))
			humioClusterPrometheusMetrics.Counters.ConfigMapsCreated.Inc()
			return nil
		}
		return fmt.Errorf("unable to fetch role permissions configmap: %w", err)
	}

	if !equality.Semantic.DeepEqual(existingConfigMap.Data, desiredConfigMap.Data) {
		existingConfigMap.Data = desiredConfigMap.Data
		if updateErr := r.Update(ctx, &existingConfigMap); updateErr != nil {
			return fmt.Errorf("unable to update role permissions configmap: %w", updateErr)
		}
	}

	return nil
}

// Ensure ingress objects are deleted if ingress is disabled.
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

func (r *HumioClusterReconciler) getHumioHostnames(ctx context.Context, hc *humiov1alpha1.HumioCluster) (string, string, error) {
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

	hostname, esHostname, err := r.getHumioHostnames(ctx, hc)
	if err != nil {
		return r.logErrorAndReturn(err, "could not get hostnames for ingress resources")
	}

	// Due to ingress-ingress relying on ingress object annotations to enable/disable/adjust certain features we create multiple ingress objects.
	ingresses := []*networkingv1.Ingress{
		ConstructGeneralIngress(hc, hostname),
		ConstructStreamingQueryIngress(hc, hostname),
		ConstructIngestIngress(hc, hostname),
		ConstructESIngestIngress(hc, esHostname),
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

	return nil
}

// Ensure the CA Issuer is valid/ready
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

// Ensure we have a valid CA certificate to configure intra-cluster communication.
// Because generating the CA can take a while, we do this before we start tearing down mismatching pods
func (r *HumioClusterReconciler) ensureValidCASecret(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.TLSEnabled(hc) {
		return nil
	}

	r.Log.Info("checking for an existing CA secret")
	caSecretIsValid, err := validCASecret(ctx, r, hc.Namespace, getCASecretName(hc))
	if caSecretIsValid {
		r.Log.Info("found valid CA secret, nothing more to do")
		return nil
	}
	// CA secret is not valid, return if user specified their own custom CA secret
	if useExistingCA(hc) {
		return r.logErrorAndReturn(fmt.Errorf("configured to use existing CA secret, but the CA secret is invalid or got error when validating, err=%v", err), "specified CA secret invalid")
	}
	// CA secret is not valid, and should generate our own if it is not already present
	if !k8serrors.IsNotFound(err) {
		// Got error that was not due to the k8s secret not existing
		return r.logErrorAndReturn(err, "could not validate CA secret")
	}

	r.Log.Info("generating new CA certificate")
	ca, err := generateCACertificate()
	if err != nil {
		return r.logErrorAndReturn(err, "could not generate new CA certificate")
	}

	r.Log.Info("persisting new CA certificate")
	caSecretData := map[string][]byte{
		corev1.TLSCertKey:       ca.Certificate,
		corev1.TLSPrivateKeyKey: ca.Key,
	}
	caSecret := kubernetes.ConstructSecret(hc.Name, hc.Namespace, getCASecretName(hc), caSecretData, nil, nil)
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
			secret := kubernetes.ConstructSecret(hc.Name, hc.Namespace, fmt.Sprintf("%s-keystore-passphrase", hc.Name), secretData, nil, nil)
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

// Ensure we have a k8s secret holding the ca.crt
// This can be used in reverse proxies talking to Humio.
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
		certificate := ConstructNodeCertificate(hnp, kubernetes.RandomString())

		certificate.Annotations[certHashAnnotation] = GetDesiredCertHash(hnp)
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

// validateUserDefinedServiceAccountsExists confirms that the user-defined service accounts all exist as they should.
// If any of the service account names explicitly set does not exist, or that we get an error, we return an error.
// In case the user does not define any service accounts or that all user-defined service accounts already exists, we return nil.
func (r *HumioClusterReconciler) validateUserDefinedServiceAccountsExists(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if hc.Spec.HumioServiceAccountName != "" {
		_, err := kubernetes.GetServiceAccount(ctx, r, hc.Spec.HumioServiceAccountName, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "not all referenced service accounts exists")
			}
			return r.logErrorAndReturn(err, "could not get service accounts")
		}
	}
	if hc.Spec.InitServiceAccountName != "" {
		_, err := kubernetes.GetServiceAccount(ctx, r, hc.Spec.InitServiceAccountName, hc.Namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "not all referenced service accounts exists")
			}
			return r.logErrorAndReturn(err, "could not get service accounts")
		}
	}
	return nil
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

func (r *HumioClusterReconciler) isPvcOrphaned(ctx context.Context, hnp *HumioNodePool, hc *humiov1alpha1.HumioCluster, pvc corev1.PersistentVolumeClaim) (bool, error) {
	// first check the pods
	podList, err := kubernetes.ListPods(ctx, r.Client, hnp.GetNamespace(), hnp.GetCommonClusterLabels())
	if err != nil {
		return false, r.logErrorAndReturn(err, "could not list pods")
	}
	if pod, err := findPodForPvc(podList, pvc); err != nil {
		if pod.Spec.NodeName != "" {
			_, err := kubernetes.GetNode(ctx, r.Client, pod.Spec.NodeName)
			if k8serrors.IsNotFound(err) {
				return true, nil
			} else if err != nil {
				return false, r.logErrorAndReturn(err, fmt.Sprintf("could not get node %s", pod.Spec.NodeName))
			} else {
				return false, nil
			}
		}
	}
	// if there is no pod running, check the latest pod status
	for _, podStatus := range hc.Status.PodStatus {
		if podStatus.PvcName == pvc.Name {
			if podStatus.NodeName != "" {
				_, err := kubernetes.GetNode(ctx, r.Client, podStatus.NodeName)
				if k8serrors.IsNotFound(err) {
					return true, nil
				} else if err != nil {
					return false, r.logErrorAndReturn(err, fmt.Sprintf("could not get node %s", podStatus.NodeName))
				}
			}
		}
	}

	return false, nil
}

func (r *HumioClusterReconciler) isPodAttachedToOrphanedPvc(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, pod corev1.Pod) (bool, error) {
	pvcList, err := r.pvcList(ctx, hnp)
	if err != nil {
		return false, r.logErrorAndReturn(err, "failed to list pvcs")
	}
	pvc, err := FindPvcForPod(pvcList, pod)
	if err != nil {
		return true, r.logErrorAndReturn(err, "could not find pvc for pod")
	}
	pvcOrphaned, err := r.isPvcOrphaned(ctx, hnp, hc, pvc)
	if err != nil {
		return false, r.logErrorAndReturn(err, "could not check if pvc is orphaned")
	}
	return pvcOrphaned, nil
}

func (r *HumioClusterReconciler) ensureOrphanedPvcsAreDeleted(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	if hnp.OkToDeletePvc() {
		r.Log.Info("checking for orphaned pvcs")
		pvcList, err := kubernetes.ListPersistentVolumeClaims(ctx, r.Client, hc.Namespace, hnp.GetNodePoolLabels())
		if err != nil {
			return r.logErrorAndReturn(err, "failed to list pvcs")
		}
		for idx := range pvcList {
			pvcOrphaned, err := r.isPvcOrphaned(ctx, hnp, hc, pvcList[idx])
			if err != nil {
				return r.logErrorAndReturn(err, "could not check if pvc is orphaned")
			}
			if pvcOrphaned {
				if pvcList[idx].DeletionTimestamp == nil {
					r.Log.Info(fmt.Sprintf("node cannot be found for pvc. deleting pvc %s as "+
						"dataVolumePersistentVolumeClaimPolicy is set to %s", pvcList[idx].Name,
						humiov1alpha1.HumioPersistentVolumeReclaimTypeOnNodeDelete))
					err = r.Client.Delete(ctx, &pvcList[idx])
					if err != nil {
						return r.logErrorAndReturn(err, fmt.Sprintf("cloud not delete pvc %s", pvcList[idx].Name))
					}
				}
			}

		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureLicenseIsValid(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring license is valid")

	licenseSecretKeySelector := licenseSecretKeyRefOrDefault(hc)
	if licenseSecretKeySelector == nil {
		return fmt.Errorf("no license secret key selector provided")
	}

	licenseSecret, err := kubernetes.GetSecret(ctx, r, licenseSecretKeySelector.Name, hc.Namespace)
	if err != nil {
		return err
	}
	if _, ok := licenseSecret.Data[licenseSecretKeySelector.Key]; !ok {
		return r.logErrorAndReturn(fmt.Errorf("could not read the license"),
			fmt.Sprintf("key %s does not exist for secret %s", licenseSecretKeySelector.Key, licenseSecretKeySelector.Name))
	}

	licenseStr := string(licenseSecret.Data[licenseSecretKeySelector.Key])
	if _, err = humio.GetLicenseUIDFromLicenseString(licenseStr); err != nil {
		return r.logErrorAndReturn(err,
			"unable to parse license")
	}

	return nil
}

func (r *HumioClusterReconciler) ensureLicenseAndAdminToken(ctx context.Context, hc *humiov1alpha1.HumioCluster, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info("ensuring license and admin token")

	// Configure a Humio client without an API token which we can use to check the current license on the cluster
	cluster, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), false, false)
	if err != nil {
		return reconcile.Result{}, err
	}
	clientWithoutAPIToken := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	desiredLicenseString, err := r.getDesiredLicenseString(ctx, hc)
	if err != nil {
		_, _ = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
		return reconcile.Result{}, err
	}

	// Confirm we can parse the license provided in the HumioCluster resource
	desiredLicenseUID, err := humio.GetLicenseUIDFromLicenseString(desiredLicenseString)
	if err != nil {
		_, _ = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
		return reconcile.Result{}, err
	}

	// Fetch details on currently installed license
	licenseUID, licenseExpiry, getErr := r.HumioClient.GetLicenseUIDAndExpiry(ctx, clientWithoutAPIToken, req)
	// Install initial license
	if getErr != nil {
		if errors.As(getErr, &humioapi.EntityNotFound{}) {
			if installErr := r.HumioClient.InstallLicense(ctx, clientWithoutAPIToken, req, desiredLicenseString); installErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(installErr, "could not install initial license")
			}

			r.Log.Info(fmt.Sprintf("successfully installed initial license: uid=%s expires=%s",
				licenseUID, licenseExpiry.String()))
			return reconcile.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get license: %w", getErr)
	}

	// update status with license details
	defer func(ctx context.Context, hc *humiov1alpha1.HumioCluster) {
		if licenseUID != "" {
			licenseStatus := humiov1alpha1.HumioLicenseStatus{
				Type:       "onprem",
				Expiration: licenseExpiry.String(),
			}
			_, _ = r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withLicense(licenseStatus))
		}
	}(ctx, hc)

	cluster, err = helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), false, true)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not authenticate with bootstrap token")
	}
	clientWithBootstrapToken := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	if err = r.ensurePersonalAPITokenForAdminUser(ctx, clientWithBootstrapToken, req, hc); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "unable to create permission tokens")
	}

	// Configure a Humio client with an API token which we can use to check the current license on the cluster
	cluster, err = helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		return reconcile.Result{}, err
	}
	clientWithPersonalAPIToken := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	if licenseUID != desiredLicenseUID {
		r.Log.Info(fmt.Sprintf("updating license because of: licenseUID(%s) != desiredLicenseUID(%s)", licenseUID, desiredLicenseUID))
		if err = r.HumioClient.InstallLicense(ctx, clientWithPersonalAPIToken, req, desiredLicenseString); err != nil {
			return reconcile.Result{}, fmt.Errorf("could not install license: %w", err)
		}
		r.Log.Info(fmt.Sprintf("successfully installed license: uid=%s", desiredLicenseUID))
		return reconcile.Result{Requeue: true}, nil
	}

	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) ensurePersonalAPITokenForAdminUser(ctx context.Context, client *humioapi.Client, req reconcile.Request, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring permission tokens")
	return r.createPersonalAPIToken(ctx, client, req, hc, "admin")
}

func (r *HumioClusterReconciler) ensureService(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	r.Log.Info("ensuring service")
	existingService, err := kubernetes.GetService(ctx, r, hnp.GetNodePoolName(), hnp.GetNamespace())
	service := ConstructService(hnp)
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

func (r *HumioClusterReconciler) ensureInternalServiceExists(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("ensuring internal service")
	existingService, err := kubernetes.GetService(ctx, r, internalServiceName(hc.Name), hc.Namespace)
	service := constructInternalService(hc)
	if k8serrors.IsNotFound(err) {
		if err := controllerutil.SetControllerReference(hc, service, r.Scheme()); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		err = r.Create(ctx, service)
		if err != nil {
			return r.logErrorAndReturn(err, "unable to create internal service for HumioCluster")
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

type resourceConfig struct {
	enabled bool
	list    func() ([]client.Object, error)
	get     func() (client.Object, error)
	errMsg  string
	isPod   bool // Added to identify pod resources
}

// ensureNodePoolSpecificResourcesHaveLabelWithNodePoolName updates resources that were created prior to the introduction of node pools.
// We need this because multiple resources now includes an additional label containing the name of the node pool a given resource belongs to.
func (r *HumioClusterReconciler) ensureNodePoolSpecificResourcesHaveLabelWithNodePoolName(ctx context.Context, hnp *HumioNodePool) error {
	updateLabels := func(obj client.Object, labels map[string]string, errMsg string) error {
		if _, found := obj.GetLabels()[kubernetes.NodePoolLabelName]; !found {
			obj.SetLabels(labels)
			if err := r.Client.Update(ctx, obj); err != nil {
				return fmt.Errorf("%s: %w", errMsg, err)
			}
		}
		return nil
	}

	resources := []resourceConfig{
		{
			enabled: true,
			isPod:   true, // Mark this as pod resource
			list: func() ([]client.Object, error) {
				pods, err := kubernetes.ListPods(ctx, r.Client, hnp.GetNamespace(), hnp.GetCommonClusterLabels())
				if err != nil {
					return nil, err
				}
				result := make([]client.Object, len(pods))
				for i := range pods {
					result[i] = &pods[i]
				}
				return result, nil
			},
			errMsg: "unable to update pod",
		},
		{
			enabled: hnp.TLSEnabled(),
			list: func() ([]client.Object, error) {
				certs, err := kubernetes.ListCertificates(ctx, r.Client, hnp.GetNamespace(), hnp.GetCommonClusterLabels())
				if err != nil {
					return nil, err
				}
				result := make([]client.Object, len(certs))
				for i := range certs {
					result[i] = &certs[i]
				}
				return result, nil
			},
			errMsg: "unable to update certificate",
		},
		{
			enabled: hnp.PVCsEnabled(),
			list: func() ([]client.Object, error) {
				pvcs, err := kubernetes.ListPersistentVolumeClaims(ctx, r.Client, hnp.GetNamespace(), hnp.GetCommonClusterLabels())
				if err != nil {
					return nil, err
				}
				result := make([]client.Object, len(pvcs))
				for i := range pvcs {
					result[i] = &pvcs[i]
				}
				return result, nil
			},
			errMsg: "unable to update PVC",
		},
		{
			enabled: !hnp.HumioServiceAccountIsSetByUser(),
			get: func() (client.Object, error) {
				return kubernetes.GetServiceAccount(ctx, r.Client, hnp.GetHumioServiceAccountName(), hnp.GetNamespace())
			},
			errMsg: "unable to update Humio service account",
		},
		{
			enabled: !hnp.InitServiceAccountIsSetByUser(),
			get: func() (client.Object, error) {
				return kubernetes.GetServiceAccount(ctx, r.Client, hnp.GetInitServiceAccountName(), hnp.GetNamespace())
			},
			errMsg: "unable to update init service account",
		},
		{
			enabled: !hnp.InitServiceAccountIsSetByUser(),
			get: func() (client.Object, error) {
				return kubernetes.GetClusterRole(ctx, r.Client, hnp.GetInitClusterRoleName())
			},
			errMsg: "unable to update init cluster role",
		},
		{
			enabled: !hnp.InitServiceAccountIsSetByUser(),
			get: func() (client.Object, error) {
				return kubernetes.GetClusterRoleBinding(ctx, r.Client, hnp.GetInitClusterRoleBindingName())
			},
			errMsg: "unable to update init cluster role binding",
		},
	}

	for _, res := range resources {
		if !res.enabled {
			continue
		}

		if res.list != nil {
			objects, err := res.list()
			if err != nil {
				return fmt.Errorf("unable to list resources: %w", err)
			}
			for _, obj := range objects {
				labels := hnp.GetNodePoolLabels()
				if res.isPod {
					labels = hnp.GetPodLabels()
				}
				if err := updateLabels(obj, labels, res.errMsg); err != nil {
					return err
				}
			}
			continue
		}

		if obj, err := res.get(); err != nil {
			if !k8serrors.IsNotFound(err) {
				return fmt.Errorf("unable to get resource: %w", err)
			}
		} else if err := updateLabels(obj, hnp.GetNodePoolLabels(), res.errMsg); err != nil {
			return err
		}
	}

	return nil
}

// cleanupUnusedTLSCertificates finds all existing per-node certificates for a specific HumioCluster
// and cleans them up if we have no use for them anymore.
func (r *HumioClusterReconciler) cleanupUnusedTLSSecrets(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.UseCertManager() {
		return nil
	}

	// because these secrets are created by cert-manager we cannot use our typical label selector
	foundSecretList, err := kubernetes.ListSecrets(ctx, r, hc.Namespace, client.MatchingLabels{})
	if err != nil {
		return r.logErrorAndReturn(err, "unable to list secrets")
	}
	if len(foundSecretList) == 0 {
		return nil
	}

	for idx, secret := range foundSecretList {
		if !helpers.TLSEnabled(hc) {
			if secret.Type == corev1.SecretTypeOpaque {
				if secret.Name == fmt.Sprintf("%s-%s", hc.Name, "ca-keypair") ||
					secret.Name == fmt.Sprintf("%s-%s", hc.Name, "keystore-passphrase") {
					r.Log.Info(fmt.Sprintf("TLS is not enabled for cluster, removing unused secret: %s", secret.Name))
					if err := r.Delete(ctx, &foundSecretList[idx]); err != nil {
						return r.logErrorAndReturn(err, "could not delete TLS secret")
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
					return r.logErrorAndReturn(err, "unable to determine if secret is in use")
				}
			}
			if !inUse {
				r.Log.Info(fmt.Sprintf("deleting secret %s", secret.Name))
				if err = r.Delete(ctx, &foundSecretList[idx]); err != nil {
					return r.logErrorAndReturn(err, fmt.Sprintf("could not delete secret %s", secret.Name))

				}
				return nil
			}
		}
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return nil
}

func (r *HumioClusterReconciler) cleanupUnusedService(ctx context.Context, hnp *HumioNodePool) error {
	var existingService corev1.Service
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hnp.namespace,
		Name:      hnp.GetServiceName(),
	}, &existingService)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return r.logErrorAndReturn(err, "could not get node pool service")
	}

	r.Log.Info(fmt.Sprintf("found existing node pool service but not pool does not have nodes. Deleting node pool service %s", existingService.Name))
	if err = r.Delete(ctx, &existingService); err != nil {
		return r.logErrorAndReturn(err, "unable to delete node pool service")
	}

	return nil
}

// cleanupUnusedCAIssuer deletes the CA Issuer for a cluster if TLS has been disabled
func (r *HumioClusterReconciler) cleanupUnusedCAIssuer(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if helpers.TLSEnabled(hc) {
		return nil
	}

	if !helpers.UseCertManager() {
		return nil
	}

	var existingCAIssuer cmapi.Issuer
	err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, &existingCAIssuer)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return r.logErrorAndReturn(err, "could not get CA Issuer")
	}

	r.Log.Info("found existing CA Issuer but cluster is configured without TLS, deleting CA Issuer")
	if err = r.Delete(ctx, &existingCAIssuer); err != nil {
		return r.logErrorAndReturn(err, "unable to delete CA Issuer")
	}

	return nil
}

// cleanupUnusedTLSCertificates finds all existing per-node certificates and cleans them up if we have no matching pod for them
func (r *HumioClusterReconciler) cleanupUnusedTLSCertificates(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if !helpers.UseCertManager() {
		return nil
	}

	foundCertificateList, err := kubernetes.ListCertificates(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return r.logErrorAndReturn(err, "unable to list certificates")
	}
	if len(foundCertificateList) == 0 {
		return nil
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
					return r.logErrorAndReturn(err, "unable to determine if certificate is in use")
				}
			}
			if !inUse {
				r.Log.Info(fmt.Sprintf("deleting certificate %s", certificate.Name))
				if err = r.Delete(ctx, &foundCertificateList[idx]); err != nil {
					return r.logErrorAndReturn(err, fmt.Sprintf("could not delete certificate %s", certificate.Name))
				}
				return nil
			}
		}
	}

	// return empty result and no error indicating that everything was in the state we wanted it to be
	return nil
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
// If there are changes that fall under a recreate update, then the pod restart policy is set to PodRestartPolicyRecreate
// and the reconciliation will requeue and the deletions will continue to be executed until all the pods have been
// removed.
//
// nolint:gocyclo
func (r *HumioClusterReconciler) ensureMismatchedPodsAreDeleted(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) (reconcile.Result, error) {
	r.Log.Info("ensuring mismatching pods are deleted")

	attachments, result, err := r.constructPodAttachments(ctx, hc, hnp)
	emptyResult := reconcile.Result{}
	if result != emptyResult || err != nil {
		return result, err
	}

	// fetch list of all current pods for the node pool
	listOfAllCurrentPodsForNodePool, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to list pods")
	}

	// fetch podStatus where we collect information about current pods
	podsStatus, err := r.getPodsStatus(ctx, hc, hnp, listOfAllCurrentPodsForNodePool)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to get pod status")
	}

	podList := listOfAllCurrentPodsForNodePool
	if podsStatus.haveUnschedulablePodsOrPodsWithBadStatusConditions() {
		podList = podsStatus.podAreUnschedulableOrHaveBadStatusConditions
	}

	// based on all pods we have, fetch compare list of all current pods with desired pods, or the pods we have prioritized to delete
	desiredLifecycleState, desiredPod, err := r.getPodDesiredLifecycleState(ctx, hnp, podList, attachments, podsStatus.foundEvictedPodsOrPodsWithOrpahanedPVCs() || podsStatus.haveUnschedulablePodsOrPodsWithBadStatusConditions())
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "got error when getting pod desired lifecycle")
	}

	// dump the current state of things
	r.Log.Info(fmt.Sprintf("cluster state is %s. waitingOnPods=%v, ADifferenceWasDetectedAndManualDeletionsNotEnabled=%v, "+
		"revisionsInSync=%v, podRevisions=%v, podDeletionTimestampSet=%v, podNames=%v, podHumioVersions=%v, expectedRunningPods=%v, podsReady=%v, podsNotReady=%v nodePoolStatus=%v",
		hc.Status.State, podsStatus.waitingOnPods(), desiredLifecycleState.ADifferenceWasDetectedAndManualDeletionsNotEnabled(), podsStatus.podRevisionCountMatchesNodeCountAndAllPodsHaveRevision(hnp.GetDesiredPodRevision()),
		podsStatus.podRevisions, podsStatus.podDeletionTimestampSet, podsStatus.podNames, podsStatus.podImageVersions, podsStatus.nodeCount, podsStatus.readyCount, podsStatus.notReadyCount, hc.Status.NodePoolStatus))

	// when we detect changes, update status to reflect Upgrading/Restarting
	if hc.Status.State == humiov1alpha1.HumioClusterStateRunning || hc.Status.State == humiov1alpha1.HumioClusterStateConfigError {
		if desiredLifecycleState.FoundVersionDifference() {
			r.Log.Info(fmt.Sprintf("changing cluster state from %s to %s with pod revision %d for node pool %s", hc.Status.State, humiov1alpha1.HumioClusterStateUpgrading, hnp.GetDesiredPodRevision(), hnp.GetNodePoolName()))
			if result, err := r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withNodePoolState(humiov1alpha1.HumioClusterStateUpgrading, hnp.GetNodePoolName(), hnp.GetDesiredPodRevision(), hnp.GetDesiredPodHash(), hnp.GetDesiredBootstrapTokenHash(), "")); err != nil {
				return result, err
			}
			return reconcile.Result{Requeue: true}, nil
		}
		if !desiredLifecycleState.FoundVersionDifference() && desiredLifecycleState.FoundConfigurationDifference() {
			r.Log.Info(fmt.Sprintf("changing cluster state from %s to %s with pod revision %d for node pool %s", hc.Status.State, humiov1alpha1.HumioClusterStateRestarting, hnp.GetDesiredPodRevision(), hnp.GetNodePoolName()))
			if result, err := r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withNodePoolState(humiov1alpha1.HumioClusterStateRestarting, hnp.GetNodePoolName(), hnp.GetDesiredPodRevision(), hnp.GetDesiredPodHash(), hnp.GetDesiredBootstrapTokenHash(), "")); err != nil {
				return result, err
			}
			return reconcile.Result{Requeue: true}, nil
		}
	}

	// when no more changes are needed, update state to Running
	if hnp.GetState() != humiov1alpha1.HumioClusterStateRunning &&
		podsStatus.podRevisionCountMatchesNodeCountAndAllPodsHaveRevision(hnp.GetDesiredPodRevision()) &&
		podsStatus.notReadyCount == 0 &&
		!podsStatus.waitingOnPods() &&
		!desiredLifecycleState.FoundConfigurationDifference() &&
		!desiredLifecycleState.FoundVersionDifference() {
		r.Log.Info(fmt.Sprintf("updating cluster state as no difference was detected, updating from=%s to=%s", hnp.GetState(), humiov1alpha1.HumioClusterStateRunning))
		_, err := r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withNodePoolState(humiov1alpha1.HumioClusterStateRunning, hnp.GetNodePoolName(), hnp.GetDesiredPodRevision(), hnp.GetDesiredPodHash(), hnp.GetDesiredBootstrapTokenHash(), ""))
		return reconcile.Result{Requeue: true}, err
	}

	// we expect an annotation for the bootstrap token to be present
	desiredBootstrapTokenHash, found := desiredPod.Annotations[BootstrapTokenHashAnnotation]
	if !found {
		return reconcile.Result{}, fmt.Errorf("desiredPod does not have the mandatory annotation %s", BootstrapTokenHashAnnotation)
	}

	// calculate desired pod hash
	desiredPodHash := podSpecAsSHA256(hnp, *desiredPod)

	// save the new revision, hash and so on in one of two cases:
	// 1. the cluster is in some pod replacement state
	// 2. this is the first time we handle pods for this node pool
	if hnp.GetDesiredPodRevision() == 0 ||
		slices.Contains([]string{
			humiov1alpha1.HumioClusterStateUpgrading,
			humiov1alpha1.HumioClusterStateRestarting,
		}, hc.Status.State) {
		// if bootstrap token hash or desired pod hash differs, update node pool status with the new values
		if desiredPodHash != hnp.GetDesiredPodHash() ||
			desiredPod.Annotations[BootstrapTokenHashAnnotation] != hnp.GetDesiredBootstrapTokenHash() {
			oldRevision := hnp.GetDesiredPodRevision()
			newRevision := oldRevision + 1

			r.Log.Info(fmt.Sprintf("detected a new pod hash for nodepool=%s updating status with oldPodRevision=%d newPodRevision=%d oldPodHash=%s newPodHash=%s oldBootstrapTokenHash=%s newBootstrapTokenHash=%s clusterState=%s",
				hnp.GetNodePoolName(),
				oldRevision, newRevision,
				hnp.GetDesiredPodHash(), desiredPodHash,
				hnp.GetDesiredBootstrapTokenHash(), desiredBootstrapTokenHash,
				hc.Status.State,
			))

			_, err := r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().withNodePoolState(hc.Status.State, hnp.GetNodePoolName(), newRevision, desiredPodHash, desiredBootstrapTokenHash, ""))
			return reconcile.Result{Requeue: true}, err
		}
	}

	// delete evicted pods and pods attached using PVC's attached to worker nodes that no longer exists
	if podsStatus.foundEvictedPodsOrPodsWithOrpahanedPVCs() {
		r.Log.Info(fmt.Sprintf("found %d humio pods requiring deletion", len(podsStatus.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists)))
		r.Log.Info(fmt.Sprintf("deleting pod %s", podsStatus.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists[0].Name))
		if err = r.Delete(ctx, &podsStatus.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists[0]); err != nil {
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(r.logErrorAndReturn(err, fmt.Sprintf("could not delete pod %s", podsStatus.podsEvictedOrUsesPVCAttachedToHostThatNoLongerExists[0].Name)).Error()))
		}
		return reconcile.Result{RequeueAfter: time.Second + 1}, nil
	}

	podsForDeletion := desiredLifecycleState.podsToBeReplaced

	// if zone awareness is enabled, we pin a zone until we're done replacing all pods in that zone,
	// this is repeated for each zone with pods that needs replacing
	if *hnp.GetUpdateStrategy().EnableZoneAwareness && !helpers.UseEnvtest() {
		if hnp.GetZoneUnderMaintenance() == "" {
			// pick a zone if we haven't already picked one
			podListForCurrentZoneWithWrongPodRevisionOrPodHash := FilterPodsExcludePodsWithPodRevisionOrPodHash(listOfAllCurrentPodsForNodePool, hnp.GetDesiredPodRevision(), hnp.GetDesiredPodHash())
			podListForCurrentZoneWithWrongPodRevisionAndNonEmptyNodeName := FilterPodsExcludePodsWithEmptyNodeName(podListForCurrentZoneWithWrongPodRevisionOrPodHash)
			r.Log.Info(fmt.Sprintf("zone awareness enabled, len(podListForCurrentZoneWithWrongPodRevisionOrPodHash)=%d len(podListForCurrentZoneWithWrongPodRevisionAndNonEmptyNodeName)=%d", len(podListForCurrentZoneWithWrongPodRevisionOrPodHash), len(podListForCurrentZoneWithWrongPodRevisionAndNonEmptyNodeName)))

			// pin the zone if we can find a non-empty zone
			for _, pod := range podListForCurrentZoneWithWrongPodRevisionAndNonEmptyNodeName {
				newZoneUnderMaintenance, err := kubernetes.GetZoneForNodeName(ctx, r, pod.Spec.NodeName)
				if err != nil {
					return reconcile.Result{}, r.logErrorAndReturn(err, "unable to fetch zone")
				}
				if newZoneUnderMaintenance != "" {
					r.Log.Info(fmt.Sprintf("zone awareness enabled, pinning zone for nodePool=%s in oldZoneUnderMaintenance=%s newZoneUnderMaintenance=%s",
						hnp.GetNodePoolName(), hnp.GetZoneUnderMaintenance(), newZoneUnderMaintenance))
					return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
						withNodePoolState(hnp.GetState(), hnp.GetNodePoolName(), hnp.GetDesiredPodRevision(), hnp.GetDesiredPodHash(), hnp.GetDesiredBootstrapTokenHash(), newZoneUnderMaintenance))
				}
			}
		} else {
			// clear the zone-under-maintenance marker if no more work is left in that zone
			allPodsInZoneZoneUnderMaintenanceIncludingAlreadyMarkedForDeletion, err := FilterPodsByZoneName(ctx, r, listOfAllCurrentPodsForNodePool, hnp.GetZoneUnderMaintenance())
			if err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "got error filtering pods by zone name")
			}
			allPodsInZoneZoneUnderMaintenanceIncludingAlreadyMarkedForDeletionWithWrongHashOrRevision := FilterPodsExcludePodsWithPodRevisionOrPodHash(allPodsInZoneZoneUnderMaintenanceIncludingAlreadyMarkedForDeletion, hnp.GetDesiredPodRevision(), hnp.GetDesiredPodHash())
			if len(allPodsInZoneZoneUnderMaintenanceIncludingAlreadyMarkedForDeletionWithWrongHashOrRevision) == 0 {
				r.Log.Info(fmt.Sprintf("zone awareness enabled, clearing zone nodePool=%s in oldZoneUnderMaintenance=%s newZoneUnderMaintenance=%s",
					hnp.GetNodePoolName(), hnp.GetZoneUnderMaintenance(), ""))
				return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
					withNodePoolState(hnp.GetState(), hnp.GetNodePoolName(), hnp.GetDesiredPodRevision(), hnp.GetDesiredPodHash(), hnp.GetDesiredBootstrapTokenHash(), ""))
			}
		}
	}

	// delete pods up to maxUnavailable from (filtered) pod list
	if desiredLifecycleState.ADifferenceWasDetectedAndManualDeletionsNotEnabled() {
		if hc.Status.State == humiov1alpha1.HumioClusterStateRestarting || hc.Status.State == humiov1alpha1.HumioClusterStateUpgrading {
			if podsStatus.waitingOnPods() && desiredLifecycleState.ShouldRollingRestart() {
				r.Log.Info(fmt.Sprintf("pods %s should be deleted, but waiting because not all other pods are "+
					"ready. waitingOnPods=%v, clusterState=%s", desiredLifecycleState.namesOfPodsToBeReplaced(),
					podsStatus.waitingOnPods(), hc.Status.State),
					"podsStatus.readyCount", podsStatus.readyCount,
					"podsStatus.nodeCount", podsStatus.nodeCount,
					"podsStatus.notReadyCount", podsStatus.notReadyCount,
					"!podsStatus.haveUnschedulablePodsOrPodsWithBadStatusConditions()", !podsStatus.haveUnschedulablePodsOrPodsWithBadStatusConditions(),
					"!podsStatus.foundEvictedPodsOrPodsWithOrpahanedPVCs()", !podsStatus.foundEvictedPodsOrPodsWithOrpahanedPVCs(),
				)
				return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
					withMessage(waitingOnPodsMessage))
			}
		}

		for i := 0; i < podsStatus.scaledMaxUnavailableMinusNotReadyDueToMinReadySeconds() && i < len(podsForDeletion); i++ {
			pod := podsForDeletion[i]
			zone := ""
			if *hnp.GetUpdateStrategy().EnableZoneAwareness && !helpers.UseEnvtest() {
				zone, _ = kubernetes.GetZoneForNodeName(ctx, r.Client, pod.Spec.NodeName)
			}
			r.Log.Info(fmt.Sprintf("deleting pod[%d] %s", i, pod.Name),
				"zone", zone,
				"podsStatus.scaledMaxUnavailableMinusNotReadyDueToMinReadySeconds()", podsStatus.scaledMaxUnavailableMinusNotReadyDueToMinReadySeconds(),
				"len(podsForDeletion)", len(podsForDeletion),
			)
			if err = r.Delete(ctx, &pod); err != nil {
				return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
					withMessage(r.logErrorAndReturn(err, fmt.Sprintf("could not delete pod %s", pod.Name)).Error()))
			}
		}
	} else {
		// OnDelete update strategy is enabled, so user must manually delete the pods
		if desiredLifecycleState.FoundVersionDifference() || desiredLifecycleState.FoundConfigurationDifference() {
			r.Log.Info(fmt.Sprintf("pods %v should be deleted because cluster restart/upgrade, but refusing due to the configured upgrade strategy",
				desiredLifecycleState.namesOfPodsToBeReplaced()))
		}
	}

	// requeue if we're upgrading all pods as once and we still detect a difference, so there's still pods left
	if hc.Status.State == humiov1alpha1.HumioClusterStateUpgrading && desiredLifecycleState.ADifferenceWasDetectedAndManualDeletionsNotEnabled() && !desiredLifecycleState.ShouldRollingRestart() {
		r.Log.Info("requeuing after 1 sec as we are upgrading cluster, have more pods to delete and we are not doing rolling restart")
		return reconcile.Result{RequeueAfter: time.Second + 1}, nil
	}

	// return empty result, which allows reconciliation to continue and create the new pods
	r.Log.Info("nothing to do")
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

	ingressDiff := cmp.Diff(ingress.Spec, desiredIngress.Spec)
	if ingressDiff != "" {
		r.Log.Info("ingress specs do not match",
			"diff", ingressDiff,
		)
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
	// Exclude pods that are currently being evicted --> Ensures K8s keeps track of the pods waiting for eviction and doesn't remove pods continuously
	pods, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to list pods")
	}

	// if there are fewer pods than specified, create pods
	if len(pods) < hnp.GetNodeCount() {
		var expectedPodsList []corev1.Pod
		pvcClaimNamesInUse := make(map[string]struct{})

		for i := 1; i+len(pods) <= hnp.GetNodeCount(); i++ {
			attachments, err := r.newPodAttachments(ctx, hnp, pods, pvcClaimNamesInUse)
			if err != nil {
				return reconcile.Result{RequeueAfter: time.Second * 5}, r.logErrorAndReturn(err, "failed to get pod attachments")
			}
			pod, err := r.createPod(ctx, hc, hnp, attachments, expectedPodsList)
			if err != nil {
				return reconcile.Result{RequeueAfter: time.Second * 5}, r.logErrorAndReturn(err, "unable to create pod")
			}
			expectedPodsList = append(expectedPodsList, *pod)
			humioClusterPrometheusMetrics.Counters.PodsCreated.Inc()
		}

		// check that we can list the new pods
		// this is to avoid issues where the requeue is faster than kubernetes
		if err := r.waitForNewPods(ctx, hnp, pods, expectedPodsList); err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "failed to validate new pod")
		}

		// We have created all pods. Requeue immediately even if the pods are not ready. We will check the readiness status on the next reconciliation.
		return reconcile.Result{Requeue: true}, nil
	}

	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) processDownscaling(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info(fmt.Sprintf("processing downscaling request for humio node pool %s", hnp.GetNodePoolName()))
	clusterConfig, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not create a cluster config for the http client.")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(clusterConfig.Config(), req)

	// handle possible unmarked evictions
	r.Log.Info("Checking for unmarked evictions.")
	podsNotMarkedForEviction, err := r.getPodsNotMarkedForEviction(ctx, hnp)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to list pods not marked for eviction.")
	}
	err = r.handleUnmarkedEvictions(ctx, humioHttpClient, req, podsNotMarkedForEviction)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not process active evictions.")
	}

	// remove lingering nodes
	r.Log.Info("Checking for lingering evicted nodes.")
	for _, vhost := range hc.Status.EvictedNodeIds {
		_, err = r.unregisterNode(ctx, hc, humioHttpClient, req, vhost)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	labelsToMatch := hnp.GetNodePoolLabels()
	labelsToMatch[kubernetes.PodMarkedForDataEviction] = helpers.TrueStr
	podsMarkedForEviction, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), labelsToMatch)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to list pods marked for eviction.")
	}
	// If there are more pods than specified, evict pod
	if len(podsNotMarkedForEviction) > hnp.GetNodeCount() && len(podsMarkedForEviction) == 0 { // mark a single pod, to slowly reduce the node count.
		r.Log.Info("Desired pod count lower than the actual pod count. Marking for eviction.")
		err := r.markPodForEviction(ctx, hc, req, podsNotMarkedForEviction, hnp.GetNodePoolName())
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	// if there are pods marked for eviction
	if len(podsMarkedForEviction) > 0 {
		// check the eviction process
		r.Log.Info("Checking eviction process.")
		successfullyUnregistered := false

		for _, pod := range podsMarkedForEviction {
			vhostStr := pod.Annotations[kubernetes.LogScaleClusterVhost]
			vhost, err := strconv.Atoi(vhostStr)
			if err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("could not parse vhost from annotation %s", vhostStr))
			}
			nodeCanBeSafelyUnregistered, err := r.checkEvictionStatusForPod(ctx, humioHttpClient, req, vhost)
			if err != nil {
				return reconcile.Result{}, err
			}
			if nodeCanBeSafelyUnregistered {
				r.Log.Info(fmt.Sprintf("successfully evicted data from vhost %d", vhost))
				if !slices.Contains(hc.Status.EvictedNodeIds, vhost) {
					hc.Status.EvictedNodeIds = append(hc.Status.EvictedNodeIds, vhost) // keep track of the evicted node for unregistering
					err = r.Status().Update(ctx, hc)
					if err != nil {
						r.Log.Error(err, "failed to update cluster status.")
						return reconcile.Result{}, err
					}
				}
				r.Log.Info(fmt.Sprintf("removing pod %s containing vhost %d", pod.Name, vhost))
				if err := r.Delete(ctx, &pod); err != nil { // delete pod before unregistering node
					return reconcile.Result{}, r.logErrorAndReturn(err, fmt.Sprintf("failed to delete pod %s for vhost %d!", pod.Name, vhost))
				}
				humioClusterPrometheusMetrics.Counters.PodsDeleted.Inc()
				successfullyUnregistered, err = r.unregisterNode(ctx, hc, humioHttpClient, req, vhost)
				if err != nil {
					return reconcile.Result{}, err
				}
			}
		}
		if !successfullyUnregistered {
			// requeue eviction check for 60 seconds
			return reconcile.Result{RequeueAfter: time.Second * 60}, nil
		}
	}
	// check for pods currently being evicted ---> check the eviction status --> if evicted --> remove node --> else, requeue
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) getPodsNotMarkedForEviction(ctx context.Context, hnp *HumioNodePool) ([]corev1.Pod, error) {
	pods, err := kubernetes.ListPods(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
	if err != nil {
		return nil, r.logErrorAndReturn(err, "failed to list pods.")
	}
	var podsNotMarkedForEviction []corev1.Pod
	for _, pod := range pods {
		if val, found := pod.Labels[kubernetes.PodMarkedForDataEviction]; !found || val != helpers.TrueStr {
			podsNotMarkedForEviction = append(podsNotMarkedForEviction, pod)
		}
	}
	return podsNotMarkedForEviction, nil
}

func (r *HumioClusterReconciler) handleUnmarkedEvictions(ctx context.Context, humioHttpClient *humioapi.Client, req ctrl.Request, podsInNodePool []corev1.Pod) error {
	cluster, err := r.HumioClient.GetCluster(ctx, humioHttpClient, req)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to get humio cluster through the GraphQL API.")
	}
	getCluster := cluster.GetCluster()
	podNameToNodeIdMap := r.matchPodsToHosts(podsInNodePool, getCluster.GetNodes())
	nodesStatus, err := r.getClusterNodesStatus(ctx, humioHttpClient, req)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to get cluster nodes using GraphQL.")
	}

	for _, pod := range podsInNodePool {
		if pod.Spec.NodeName == "" {
			r.Log.Info(fmt.Sprintf("NodeName is empty for pod %s.", pod.Name))
			continue
		}
		vhost := podNameToNodeIdMap[pod.GetName()]
		marked, err := r.updateEvictionStatus(ctx, nodesStatus, pod, vhost)
		if err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("failed to update eviction status for vhost %d", vhost))
		}
		if marked {
			r.Log.Info(fmt.Sprintf("pod %s successfully marked for data eviction.", pod.GetName()))
		}
	}
	return nil
}

func (r *HumioClusterReconciler) unregisterNode(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioHttpClient *humioapi.Client, req ctrl.Request, vhost int) (bool, error) {
	r.Log.Info(fmt.Sprintf("unregistering vhost %d", vhost))

	nodesStatus, err := r.getClusterNodesStatus(ctx, humioHttpClient, req)
	if err != nil {
		return false, r.logErrorAndReturn(err, "failed to get cluster nodes using GraphQL")
	}

	if registered := r.isNodeRegistered(nodesStatus, vhost); !registered {
		r.Log.Info(fmt.Sprintf("vhost %d is already unregistered", vhost))
		hc.Status.EvictedNodeIds = RemoveIntFromSlice(hc.Status.EvictedNodeIds, vhost) // remove unregistered node from the status list
		err := r.Status().Update(ctx, hc)
		if err != nil {
			r.Log.Error(err, "failed to update cluster status.")
			return false, err
		}
		return true, nil
	}

	if alive := r.isEvictedNodeAlive(nodesStatus, vhost); !alive { // poll check for unregistering
		rawResponse, err := r.HumioClient.UnregisterClusterNode(ctx, humioHttpClient, req, vhost, false)
		if err != nil {
			return false, r.logErrorAndReturn(err, fmt.Sprintf("failed to unregister vhost %d", vhost))
		}
		response := rawResponse.GetClusterUnregisterNode()
		cluster := response.GetCluster()
		nodes := cluster.GetNodes()

		for _, node := range nodes { // check if node still exists
			if node.GetId() == vhost {
				r.Log.Info(fmt.Sprintf("could not unregister vhost %d. Requeuing...", vhost))
				return false, nil
			}
		}

		hc.Status.EvictedNodeIds = RemoveIntFromSlice(hc.Status.EvictedNodeIds, vhost) // remove unregistered node from the status list
		err = r.Status().Update(ctx, hc)
		if err != nil {
			r.Log.Error(err, "failed to update cluster status.")
			return false, err
		}
		r.Log.Info(fmt.Sprintf("successfully unregistered vhost %d", vhost))
	}
	return true, nil
}

func (r *HumioClusterReconciler) isNodeRegistered(nodesStatus []humiographql.GetEvictionStatusClusterNodesClusterNode, vhost int) bool {
	for _, node := range nodesStatus {
		if node.GetId() == vhost {
			return true
		}
	}
	return false
}

func (r *HumioClusterReconciler) isEvictedNodeAlive(nodesStatus []humiographql.GetEvictionStatusClusterNodesClusterNode, vhost int) bool {
	for i := 0; i < waitForPodTimeoutSeconds; i++ {
		for _, node := range nodesStatus {
			if node.GetId() == vhost {
				reasonsNodeCannotBeSafelyUnregistered := node.GetReasonsNodeCannotBeSafelyUnregistered()
				if !reasonsNodeCannotBeSafelyUnregistered.IsAlive {
					return false
				}
			}
		}
	}

	return true
}

func (r *HumioClusterReconciler) checkEvictionStatusForPodUsingClusterRefresh(ctx context.Context, humioHttpClient *humioapi.Client, req ctrl.Request, vhost int) (bool, error) {
	clusterManagementStatsResponse, err := r.HumioClient.RefreshClusterManagementStats(ctx, humioHttpClient, req, vhost)
	if err != nil {
		return false, r.logErrorAndReturn(err, "could not get cluster nodes status.")
	}
	clusterManagementStats := clusterManagementStatsResponse.GetRefreshClusterManagementStats()
	reasonsNodeCannotBeSafelyUnregistered := clusterManagementStats.GetReasonsNodeCannotBeSafelyUnregistered()
	if !reasonsNodeCannotBeSafelyUnregistered.GetLeadsDigest() &&
		!reasonsNodeCannotBeSafelyUnregistered.GetHasUnderReplicatedData() &&
		!reasonsNodeCannotBeSafelyUnregistered.GetHasDataThatExistsOnlyOnThisNode() {
		return true, nil
	}
	return false, nil
}

func (r *HumioClusterReconciler) checkEvictionStatusForPod(ctx context.Context, humioHttpClient *humioapi.Client, req ctrl.Request, vhost int) (bool, error) {
	for i := 0; i < waitForPodTimeoutSeconds; i++ {
		nodesStatus, err := r.getClusterNodesStatus(ctx, humioHttpClient, req)
		if err != nil {
			return false, r.logErrorAndReturn(err, "could not get cluster nodes status.")
		}
		for _, node := range nodesStatus {
			if node.GetId() == vhost {
				reasonsNodeCannotBeSafelyUnregistered := node.GetReasonsNodeCannotBeSafelyUnregistered()
				if !reasonsNodeCannotBeSafelyUnregistered.GetHasDataThatExistsOnlyOnThisNode() &&
					!reasonsNodeCannotBeSafelyUnregistered.GetHasUnderReplicatedData() &&
					!reasonsNodeCannotBeSafelyUnregistered.GetLeadsDigest() {
					// if cheap check is ok, run a cache refresh check
					if ok, _ := r.checkEvictionStatusForPodUsingClusterRefresh(ctx, humioHttpClient, req, vhost); ok {
						return true, nil
					}
				}
			}
		}
	}

	return false, nil
}

// Gracefully removes a LogScale pod from the nodepool using the following steps:
//
//  1. Matches pod names to node ids
//  2. Computes the zone from which the pod will be removed base on the current node allocation
//  3. Iterates through pods and for the first one found in the specified zone, sends an eviction request to the node
//  4. Checks if the eviction has started (with a timeout of 10 seconds)
//  5. If the eviction has started, it periodically checks every 60 seconds if the eviction has been completed
//  6. When the eviction is completed and there is no more data on that node, the node is unregistered from the cluster, and the pod is removed.
func (r *HumioClusterReconciler) markPodForEviction(ctx context.Context, hc *humiov1alpha1.HumioCluster, req ctrl.Request, podsInNodePool []corev1.Pod, nodePoolName string) error {
	// GetCluster gql query returns node ID and Zone
	clusterConfig, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		return r.logErrorAndReturn(err, "could not create a cluster config for the http client.")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(clusterConfig.Config(), req)
	cluster, err := r.HumioClient.GetCluster(ctx, humioHttpClient, req)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to get humio cluster through the GraphQL API.")
	}
	getCluster := cluster.GetCluster()
	podNameToNodeIdMap := r.matchPodsToHosts(podsInNodePool, getCluster.GetNodes())

	// Check Node Zones and gets the one with the most nodes. In case of a tie, the first zone is used
	podRemovalZone, err := r.getZoneForPodRemoval(ctx, podsInNodePool)
	if err != nil {
		return r.logErrorAndReturn(err, "failed to get pod removal zone")
	}

	for _, pod := range podsInNodePool {
		podLabel, err := r.getZoneFromPodNode(ctx, pod)
		if podLabel != podRemovalZone || err != nil {
			continue
		}
		if pod.Spec.NodeName == "" {
			r.Log.Info(fmt.Sprintf("NodeName is empty for pod %s.", pod.Name))
			continue
		}
		vhost := podNameToNodeIdMap[pod.GetName()]

		r.Log.Info(fmt.Sprintf("Marking pod %s with associated vhost %d for eviction.", pod.Name, vhost))
		err = r.HumioClient.SetIsBeingEvicted(ctx, humioHttpClient, req, vhost, true)
		if err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("failed to set data eviction for vhost %d", vhost))
		}

		nodesStatus, err := r.getClusterNodesStatus(ctx, humioHttpClient, req)
		if err != nil {
			return r.logErrorAndReturn(err, "failed to get cluster nodes using GraphQL")
		}

		marked, err := r.updateEvictionStatus(ctx, nodesStatus, pod, vhost)
		if err != nil {
			return r.logErrorAndReturn(err, fmt.Sprintf("failed to update eviction status for vhost %d", vhost))
		}
		if marked {
			r.Log.Info(fmt.Sprintf("pod %s successfully marked for data eviction", pod.GetName()))
		}
		return nil // return after one pod is processed to ensure pods are removed one-by-one
	}

	return r.logErrorAndReturn(err, fmt.Sprintf("No pod was found to be eligible for eviction in this node pool %s", nodePoolName))
}

func (r *HumioClusterReconciler) updateEvictionStatus(ctx context.Context, nodesStatus []humiographql.GetEvictionStatusClusterNodesClusterNode, pod corev1.Pod, vhost int) (bool, error) {
	// wait for eviction status to be updated
	isBeingEvicted := false
	for i := 0; i < waitForPodTimeoutSeconds; i++ {
		for _, node := range nodesStatus {
			if node.GetId() == vhost && *node.GetIsBeingEvicted() {
				isBeingEvicted = true
				break
			}
		}

		if isBeingEvicted { // skip the waiting if marked
			break
		}
		time.Sleep(time.Second * 1)
	}

	if !isBeingEvicted {
		return false, nil
	}

	r.Log.Info(fmt.Sprintf("marking node data eviction in progress for vhost %d", vhost))
	pod.Labels[kubernetes.PodMarkedForDataEviction] = helpers.TrueStr
	pod.Annotations[kubernetes.LogScaleClusterVhost] = strconv.Itoa(vhost)
	err := r.Update(ctx, &pod)
	if err != nil {
		return false, r.logErrorAndReturn(err, fmt.Sprintf("failed to annotated pod %s as 'marked for data eviction'", pod.GetName()))
	}
	return true, nil
}

func (r *HumioClusterReconciler) getClusterNodesStatus(ctx context.Context, humioHttpClient *humioapi.Client, req ctrl.Request) ([]humiographql.GetEvictionStatusClusterNodesClusterNode, error) {
	newClusterStatus, err := r.HumioClient.GetEvictionStatus(ctx, humioHttpClient, req)
	if err != nil {
		return nil, r.logErrorAndReturn(err, "failed to get eviction status")
	}
	getCluster := newClusterStatus.GetCluster()
	return getCluster.GetNodes(), nil
}

// Matches the set of pods in a node pool to host ids by checking the host URI and availability.
// The result is a map from pod name ---to---> node id (vhost)
func (r *HumioClusterReconciler) matchPodsToHosts(podsInNodePool []corev1.Pod, clusterNodes []humiographql.GetClusterClusterNodesClusterNode) map[string]int {
	vhostToPodMap := make(map[string]int)
	for _, pod := range podsInNodePool {
		for _, node := range clusterNodes {
			if node.GetIsAvailable() {
				podNameFromUri, err := GetPodNameFromNodeUri(node.GetUri())
				if err != nil {
					r.Log.Info(fmt.Sprintf("unable to get pod name from node uri: %s", err))
					continue
				}
				if podNameFromUri == pod.GetName() {
					vhostToPodMap[pod.GetName()] = node.GetId()
				}
			}
		}
	}
	return vhostToPodMap
}

func (r *HumioClusterReconciler) getZoneFromPodNode(ctx context.Context, pod corev1.Pod) (string, error) {
	if pod.Spec.NodeName == "" {
		return "", errors.New("pod node name is empty. Cannot properly compute Zone distribution for pods")
	}
	podNode, err := kubernetes.GetNode(ctx, r.Client, pod.Spec.NodeName)
	if err != nil || podNode == nil {
		return "", r.logErrorAndReturn(err, fmt.Sprintf("could not get Node for pod %s.", pod.Name))
	}
	return podNode.Labels[corev1.LabelTopologyZone], nil
}

func (r *HumioClusterReconciler) getZoneForPodRemoval(ctx context.Context, podsInNodePool []corev1.Pod) (string, error) {
	zoneCount := map[string]int{}
	for _, pod := range podsInNodePool {
		nodeLabel, err := r.getZoneFromPodNode(ctx, pod)
		if err != nil || nodeLabel == "" {
			return "", err
		}
		if _, ok := zoneCount[nodeLabel]; !ok {
			zoneCount[nodeLabel] = 0
		}
		zoneCount[nodeLabel]++
	}

	zoneForPodRemoval, err := GetKeyWithHighestValue(zoneCount)
	if err != nil {
		return "", errors.New("could compute find zone for pod removal")
	}
	return zoneForPodRemoval, nil
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
	hv := HumioVersionFromString(hnp.GetImage())
	if ok, _ := hv.AtLeast(HumioVersionMinimumSupported); !ok {
		return r.logErrorAndReturn(fmt.Errorf("unsupported Humio version: %s", hv.version.String()), fmt.Sprintf("Humio version must be at least %s", HumioVersionMinimumSupported))
	}
	return nil
}

func (r *HumioClusterReconciler) ensureValidStorageConfiguration(hnp *HumioNodePool) error {
	if hnp.GetNodeCount() <= 0 {
		return nil
	}

	errInvalidStorageConfiguration := fmt.Errorf("exactly one of dataVolumeSource and dataVolumePersistentVolumeClaimSpecTemplate must be set")

	emptyVolumeSource := corev1.VolumeSource{}
	emptyDataVolumePersistentVolumeClaimSpecTemplate := corev1.PersistentVolumeClaimSpec{}

	if reflect.DeepEqual(hnp.GetDataVolumeSource(), emptyVolumeSource) &&
		reflect.DeepEqual(hnp.GetDataVolumePersistentVolumeClaimSpecTemplateRAW(), emptyDataVolumePersistentVolumeClaimSpecTemplate) {
		return r.logErrorAndReturn(errInvalidStorageConfiguration, "no storage configuration provided")
	}

	if !reflect.DeepEqual(hnp.GetDataVolumeSource(), emptyVolumeSource) &&
		!reflect.DeepEqual(hnp.GetDataVolumePersistentVolumeClaimSpecTemplateRAW(), emptyDataVolumePersistentVolumeClaimSpecTemplate) {
		return r.logErrorAndReturn(errInvalidStorageConfiguration, "conflicting storage configuration provided")
	}

	return nil
}

func (r *HumioClusterReconciler) pvcList(ctx context.Context, hnp *HumioNodePool) ([]corev1.PersistentVolumeClaim, error) {
	var pvcList []corev1.PersistentVolumeClaim
	if hnp.PVCsEnabled() {
		foundPvcList, err := kubernetes.ListPersistentVolumeClaims(ctx, r, hnp.GetNamespace(), hnp.GetNodePoolLabels())
		if err != nil {
			return pvcList, err
		}
		for _, pvc := range foundPvcList {
			if pvc.DeletionTimestamp == nil {
				pvcList = append(pvcList, pvc)
			}
		}
	}
	return pvcList, nil
}

func (r *HumioClusterReconciler) getDesiredLicenseString(ctx context.Context, hc *humiov1alpha1.HumioCluster) (string, error) {
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
		if _, err := r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withState(humiov1alpha1.HumioClusterStateRunning)); err != nil {
			r.Log.Error(err, fmt.Sprintf("failed to set state to %s", humiov1alpha1.HumioClusterStateRunning))
		}
	}

	return string(licenseSecret.Data[licenseSecretKeySelector.Key]), nil
}

func (r *HumioClusterReconciler) verifyHumioClusterConfigurationIsValid(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) (reconcile.Result, error) {
	for _, pool := range humioNodePools.Filter(NodePoolFilterHasNode) {
		if err := r.setImageFromSource(ctx, pool); err != nil {
			r.Log.Info(fmt.Sprintf("failed to setImageFromSource, so setting ConfigError err=%v", err))
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withNodePoolState(humiov1alpha1.HumioClusterStateConfigError, pool.GetNodePoolName(), pool.GetDesiredPodRevision(), pool.GetDesiredPodHash(), pool.GetDesiredBootstrapTokenHash(), pool.GetZoneUnderMaintenance()))
		}
		if err := r.ensureValidHumioVersion(pool); err != nil {
			r.Log.Info(fmt.Sprintf("ensureValidHumioVersion failed, so setting ConfigError err=%v", err))
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withNodePoolState(humiov1alpha1.HumioClusterStateConfigError, pool.GetNodePoolName(), pool.GetDesiredPodRevision(), pool.GetDesiredPodHash(), pool.GetDesiredBootstrapTokenHash(), pool.GetZoneUnderMaintenance()))
		}
		if err := r.ensureValidStorageConfiguration(pool); err != nil {
			r.Log.Info(fmt.Sprintf("ensureValidStorageConfiguration failed, so setting ConfigError err=%v", err))
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withNodePoolState(humiov1alpha1.HumioClusterStateConfigError, pool.GetNodePoolName(), pool.GetDesiredPodRevision(), pool.GetDesiredPodHash(), pool.GetDesiredBootstrapTokenHash(), pool.GetZoneUnderMaintenance()))
		}
	}

	for _, fun := range []ctxHumioClusterFunc{
		r.ensureLicenseIsValid,
		r.ensureValidCASecret,
		r.ensureHeadlessServiceExists,
		r.ensureInternalServiceExists,
		r.validateUserDefinedServiceAccountsExists,
	} {
		if err := fun(ctx, hc); err != nil {
			r.Log.Info(fmt.Sprintf("someFunc failed, so setting ConfigError err=%v", err))
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withState(humiov1alpha1.HumioClusterStateConfigError))
		}
	}

	if len(humioNodePools.Filter(NodePoolFilterHasNode)) > 0 {
		if err := r.ensureNodePoolSpecificResourcesHaveLabelWithNodePoolName(ctx, humioNodePools.Filter(NodePoolFilterHasNode)[0]); err != nil {
			r.Log.Info(fmt.Sprintf("ensureNodePoolSpecificResourcesHaveLabelWithNodePoolName failed, so setting ConfigError err=%v", err))
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withState(humiov1alpha1.HumioClusterStateConfigError))
		}
	}

	if err := r.validateNodeCount(hc, humioNodePools.Items); err != nil {
		r.Log.Info(fmt.Sprintf("validateNodeCount failed, so setting ConfigError err=%v", err))
		return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withMessage(err.Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
	}

	for _, pool := range humioNodePools.Items {
		if err := r.validateInitialPodSpec(pool); err != nil {
			r.Log.Info(fmt.Sprintf("validateInitialPodSpec failed, so setting ConfigError err=%v", err))
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()).
				withNodePoolState(humiov1alpha1.HumioClusterStateConfigError, pool.GetNodePoolName(), pool.GetDesiredPodRevision(), pool.GetDesiredPodHash(), pool.GetDesiredBootstrapTokenHash(), pool.GetZoneUnderMaintenance()))
		}
	}
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) cleanupUnusedResources(ctx context.Context, hc *humiov1alpha1.HumioCluster, humioNodePools HumioNodePoolList) (reconcile.Result, error) {
	for _, hnp := range humioNodePools.Items {
		if err := r.ensureOrphanedPvcsAreDeleted(ctx, hc, hnp); err != nil {
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}

		if hnp.GetExtraKafkaConfigs() == "" {
			extraKafkaConfigsConfigMap, err := kubernetes.GetConfigMap(ctx, r, hnp.GetExtraKafkaConfigsConfigMapName(), hc.Namespace)
			if err == nil {
				if err = r.Delete(ctx, &extraKafkaConfigsConfigMap); err != nil {
					return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
						withMessage(err.Error()))
				}
			}
		}
	}

	for _, hnp := range humioNodePools.Items {
		if hnp.GetViewGroupPermissions() == "" {
			viewGroupPermissionsConfigMap, err := kubernetes.GetConfigMap(ctx, r, hnp.GetViewGroupPermissionsConfigMapName(), hc.Namespace)
			if err == nil {
				if err = r.Delete(ctx, &viewGroupPermissionsConfigMap); err != nil {
					return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
						withMessage(err.Error()))
				}
				break // only need to delete it once, since all node pools reference the same underlying configmap
			}
		}
	}

	for _, hnp := range humioNodePools.Items {
		if hnp.GetRolePermissions() == "" {
			rolePermissionsConfigMap, err := kubernetes.GetConfigMap(ctx, r, hnp.GetRolePermissionsConfigMapName(), hc.Namespace)
			if err == nil {
				if err = r.Delete(ctx, &rolePermissionsConfigMap); err != nil {
					return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
						withMessage(err.Error()))
				}
				break // only need to delete it once, since all node pools reference the same underlying configmap
			}
		}
	}

	for _, nodePool := range humioNodePools.Filter(NodePoolFilterDoesNotHaveNodes) {
		if err := r.cleanupUnusedService(ctx, nodePool); err != nil {
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}

	for _, fun := range []ctxHumioClusterFunc{
		r.cleanupUnusedTLSCertificates,
		r.cleanupUnusedTLSSecrets,
		r.cleanupUnusedCAIssuer,
	} {
		if err := fun(ctx, hc); err != nil {
			return r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
				withMessage(err.Error()))
		}
	}

	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) constructPodAttachments(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) (*podAttachments, reconcile.Result, error) {
	attachments := &podAttachments{}

	if hnp.DataVolumePersistentVolumeClaimSpecTemplateIsSetByUser() {
		attachments.dataVolumeSource = hnp.GetDataVolumePersistentVolumeClaimSpecTemplate("")
	}

	envVarSourceData, err := r.getEnvVarSource(ctx, hnp)
	if err != nil {
		result, _ := r.updateStatus(ctx, r.Client.Status(), hc, statusOptions().
			withMessage(r.logErrorAndReturn(err, "got error when getting pod envVarSource").Error()).
			withState(humiov1alpha1.HumioClusterStateConfigError))
		return nil, result, err
	}
	if envVarSourceData != nil {
		attachments.envVarSourceData = envVarSourceData
	}

	humioBootstrapTokens, err := kubernetes.ListHumioBootstrapTokens(ctx, r.Client, hc.GetNamespace(), kubernetes.LabelsForHumioBootstrapToken(hc.GetName()))
	if err != nil {
		return nil, reconcile.Result{}, r.logErrorAndReturn(err, "failed to get bootstrap token")
	}
	if len(humioBootstrapTokens) > 0 {
		if humioBootstrapTokens[0].Status.State == humiov1alpha1.HumioBootstrapTokenStateReady {
			attachments.bootstrapTokenSecretReference.secretReference = humioBootstrapTokens[0].Status.HashedTokenSecretKeyRef.SecretKeyRef
			bootstrapTokenHash, err := r.getDesiredBootstrapTokenHash(ctx, hc)
			if err != nil {
				return nil, reconcile.Result{}, r.logErrorAndReturn(err, "unable to find bootstrap token secret")
			}
			attachments.bootstrapTokenSecretReference.hash = bootstrapTokenHash
		}
	}

	return attachments, reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// mergeEnvVars returns a slice of environment variables.
// In case of a duplicate variable name, precedence is given to the value defined in into.
func mergeEnvVars(from, into []corev1.EnvVar) []corev1.EnvVar {
	var add bool
	if len(into) == 0 {
		return from
	}
	for _, commonVar := range from {
		for _, nodeVar := range into {
			if commonVar.Name == nodeVar.Name {
				add = false
				break
			}
			add = true
		}
		if add {
			into = append(into, commonVar)
		}
		add = false
	}
	return into
}

func getHumioNodePoolManagers(hc *humiov1alpha1.HumioCluster) HumioNodePoolList {
	var humioNodePools HumioNodePoolList
	humioNodePools.Add(NewHumioNodeManagerFromHumioCluster(hc))
	for idx := range hc.Spec.NodePools {
		humioNodePools.Add(NewHumioNodeManagerFromHumioNodePool(hc, &hc.Spec.NodePools[idx]))
	}
	return humioNodePools
}

// reconcileSinglePDB handles creation/update of a PDB for a single node pool
func (r *HumioClusterReconciler) reconcileSinglePDB(ctx context.Context, hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool) error {
	pdbSpec := hnp.GetPodDisruptionBudget()
	pdbName := hnp.GetPodDisruptionBudgetName()
	if pdbSpec == nil {
		r.Log.Info("PDB not configured by user, deleting any existing PDB", "nodePool", hnp.GetNodePoolName(), "pdb", pdbName)
		currentPDB := &policyv1.PodDisruptionBudget{}
		err := r.Get(ctx, client.ObjectKey{Name: pdbName, Namespace: hc.Namespace}, currentPDB)
		if err == nil {
			if delErr := r.Delete(ctx, currentPDB); delErr != nil {
				return fmt.Errorf("failed to delete orphaned PDB %s/%s: %w", hc.Namespace, pdbName, delErr)
			}
			r.Log.Info("deleted orphaned PDB", "pdb", pdbName)
		} else if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to get PDB %s/%s: %w", hc.Namespace, pdbName, err)
		}
		return nil
	}

	pods, err := kubernetes.ListPods(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods) == 0 {
		r.Log.Info("no pods found, skipping PDB creation")
		return nil
	}

	desiredPDB, err := r.constructPDB(hc, hnp, pdbSpec)
	if err != nil {
		r.Log.Error(err, "failed to construct PDB", "pdbName", pdbName)
		return fmt.Errorf("failed to construct PDB: %w", err)
	}

	return r.createOrUpdatePDB(ctx, hc, desiredPDB)
}

// constructPDB creates a PodDisruptionBudget object for a given HumioCluster and HumioNodePool
func (r *HumioClusterReconciler) constructPDB(hc *humiov1alpha1.HumioCluster, hnp *HumioNodePool, pdbSpec *humiov1alpha1.HumioPodDisruptionBudgetSpec) (*policyv1.PodDisruptionBudget, error) {
	pdbName := hnp.GetPodDisruptionBudgetName() // Use GetPodDisruptionBudgetName from HumioNodePool

	selector := &metav1.LabelSelector{
		MatchLabels: kubernetes.MatchingLabelsForHumioNodePool(hc.Name, hnp.GetNodePoolName()),
	}

	minAvailable := pdbSpec.MinAvailable
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pdbName,
			Namespace: hc.Namespace,
			Labels:    hnp.GetNodePoolLabels(),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: selector,
		},
	}

	// Set controller reference using controller-runtime utility
	if err := controllerutil.SetControllerReference(hc, pdb, r.Scheme()); err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}

	if minAvailable != nil {
		pdb.Spec.MinAvailable = minAvailable
	} else {
		defaultMinAvailable := intstr.IntOrString{
			Type:   intstr.Int,
			IntVal: 1,
		}
		pdb.Spec.MinAvailable = &defaultMinAvailable
	}

	return pdb, nil
}

// createOrUpdatePDB creates or updates a PodDisruptionBudget object
func (r *HumioClusterReconciler) createOrUpdatePDB(ctx context.Context, hc *humiov1alpha1.HumioCluster, desiredPDB *policyv1.PodDisruptionBudget) error {
	// Set owner reference so that the PDB is deleted when hc is deleted.
	if err := controllerutil.SetControllerReference(hc, desiredPDB, r.Scheme()); err != nil {
		return fmt.Errorf("failed to set owner reference on PDB %s/%s: %w", desiredPDB.Namespace, desiredPDB.Name, err)
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, desiredPDB, func() error {
		return nil
	})
	if err != nil {
		r.Log.Error(err, "failed to create or update PDB", "pdb", desiredPDB.Name)
		return fmt.Errorf("failed to create or update PDB %s/%s: %w", desiredPDB.Namespace, desiredPDB.Name, err)
	}
	r.Log.Info("PDB operation completed", "operation", op, "pdb", desiredPDB.Name)
	return nil
}
