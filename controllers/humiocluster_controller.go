/*
Copyright 2019 Humio.

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
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humio "github.com/humio/humio-operator/pkg/humio"

	"k8s.io/apimachinery/pkg/types"
)

var (
	humioClusterOwnerKey = ".metadata.controller"
	apiGVStr             = humiov1alpha1.GroupVersion.String()
	metricPodsCreated    = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "humio_controller_pods_created_total",
		Help: "Total number of pod objects created by controller",
	})
	metricPodsDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "humio_controller_pods_deleted_total",
		Help: "Total number of pod objects deleted by controller",
	})
)

// HumioClusterReconciler reconciles a HumioCluster object
type HumioClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=core,resources=service,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=endpoint,verbs=get;list;watch;create;update;patch;delete

// Reconcile ....
func (r *HumioClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("humioCluster", req.NamespacedName)

	// Get the current state of the CR
	var humioCluster humiov1alpha1.HumioCluster
	if err := r.Get(ctx, req.NamespacedName, &humioCluster); err != nil {
		//r.Log.Error(err, "Unable to fetch HumioCluster")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		log.Info(fmt.Sprintf("unable to fetch humiocluster: %v", err))
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Abort if we have just reconciled
	if humioCluster.Status.StateLastUpdatedUnix != 0 {
		lastUpdated := time.Unix(humioCluster.Status.StateLastUpdatedUnix, 0)
		if time.Since(lastUpdated) < 15*time.Second {
			// last reconciliation done very recently, requeue it and return
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
		}
	}

	// Ensure all resources for node pools are created
	for _, pool := range humioCluster.Spec.NodePools {
		err := r.ensureHumioPoolExists(ctx, humioCluster, pool)
		if err != nil {
			log.Info(fmt.Sprintf("could not create node pool %s because %v", pool.Name, err))
		}
	}

	// Ensure we have service objects pointing to each type of node
	if err := r.ensureHeadlessServiceExists(ctx, humioCluster); err != nil {
		log.Info("could not create headless service: %v", err)
	}
	validNodeTypes := []humiov1alpha1.HumioNodeType{
		humiov1alpha1.Ingest,
		humiov1alpha1.Digest,
		humiov1alpha1.Storage,
	}
	for _, t := range validNodeTypes {
		if err := r.ensureServiceExists(ctx, t, humioCluster); err != nil {
			log.Info(fmt.Sprintf("could not create service for node type %v", t))
		}
	}

	// Ensure we have valid base URL, and grab a JWT token
	if err := updateBaseURL(r.Client, &humioCluster); err != nil {
		log.Info(fmt.Sprintf("unable to obtain a valid base url: %v", err))
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}
	if humioCluster.Status.BaseURL != "" {
		if err := updateJWTToken(&humioCluster); err != nil {
			log.Info(fmt.Sprintf("could not get a valid jwt token: %v", err))
			return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
		}
	}

	// If we have both baseURL and JWT token we can connect to Humio
	if humioCluster.Status.BaseURL != "" && humioCluster.Status.JWTToken != "" {

		// Update status indicator for whether we have missing data or not
		noDataMissing, err := humio.NoDataMissing(r.Client, humioCluster)
		humioCluster.Status.AllDataAvailable = fmt.Sprintf("%v", noDataMissing)

		// TODO(mike): Add more safeguards, we cant just assume that results are recent.
		allHumioNodesAvailable, err := humio.AreAllRegisteredNodesAvailable(r.Client, humioCluster)
		if err != nil {
			log.Info(fmt.Sprintf("could not determine if humio nodes are available: %v", err))
		}
		currentlyRegisteredNodeCount, err := humio.CountNodesRegistered(humioCluster)
		if err != nil {
			log.Info(fmt.Sprintf("could determine current humio node count: %v", err))
		}
		specNodeCount := 0
		for _, p := range humioCluster.Spec.NodePools {
			specNodeCount += p.NodeCount
		}
		if specNodeCount == currentlyRegisteredNodeCount && allHumioNodesAvailable {
			// Continue if expected number of nodes are registered and all is available

			// Ensure storage partitions is balanced across storage nodes
			balancedStorage, err := humio.IsStoragePartitionsBalanced(humioCluster)
			if err != nil {
				log.Info(fmt.Sprintf("could not reassign/rebalance storage partitions: %v", err))
			}
			if !balancedStorage {
				if err := humio.RebalanceStoragePartitions(humioCluster); err != nil {
					log.Info(fmt.Sprintf("could not reassign/rebalance storage partitions: %v", err))
				}
			}

			// Ensure ingest partitions is balanced across digest nodes
			balancedIngest, err := humio.IsIngestPartitionsBalanced(humioCluster)
			if err != nil {
				log.Info(fmt.Sprintf("could not reassign/rebalance ingest partitions: %v", err))
			}
			if !balancedIngest {
				if err := humio.RebalanceIngestPartitions(humioCluster); err != nil {
					log.Info(fmt.Sprintf("could not reassign/rebalance ingest partitions: %v", err))
				}
			}

			// If any partition was not balanced, ensure we start data redistribution
			if !balancedStorage || !balancedIngest {
				if err := humio.StartDataRedistribution(humioCluster); err != nil {
					log.Info(fmt.Sprintf("could not start data redistribution: %v", err))
				}
			}
		}
	}

	// Go through expected node pools and clean up excess resources
	for _, pool := range humioCluster.Spec.NodePools {
		if err := r.cleanupHumioNodePoolResources(ctx, humioCluster, pool); err != nil {
			log.Info(fmt.Sprintf("could not clean up node pool resources: %v", err))
		}
	}

	// List all pods and remove the pod if it is member of a pool that has been removed
	allPods, err := humio.ListPods(r.Client, humioCluster)
	if err != nil {
		log.Info(fmt.Sprintf("could not list all pods in cluster: %v", err))
	}
	for _, p := range allPods {
		if !humio.ContainsNodePoolName(p.Labels["humio_node_pool"], humioCluster) {
			r.cleanupPod(humioCluster, p)
			continue
		}
		// Refresh pod labels
		for _, hnp := range humioCluster.Spec.NodePools {
			if p.Labels["humio_node_pool"] == hnp.Name {
				p.Labels[fmt.Sprintf("humio_type_%s", humiov1alpha1.Ingest)] = fmt.Sprintf("%v", humio.IsIngestNode(hnp))
				p.Labels[fmt.Sprintf("humio_type_%s", humiov1alpha1.Digest)] = fmt.Sprintf("%v", humio.IsDigestNode(hnp))
				p.Labels[fmt.Sprintf("humio_type_%s", humiov1alpha1.Storage)] = fmt.Sprintf("%v", humio.IsStorageNode(hnp))
			}
		}
		if err := r.Client.Update(ctx, &p); err != nil {
			log.Info(fmt.Sprintf("could not update labels for pod: %v", err))
		}
	}

	// Update timestamp for last reconciliation
	humioCluster.Status.StateLastUpdatedUnix = time.Now().Unix()
	err = r.Status().Update(ctx, &humioCluster)
	if err != nil {
		log.Info(fmt.Sprintf("could not update status: %v", err))
		return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
	}

	log.Info("successfully reconciled cluster")
	return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, nil
}

func labelsForHumio(clusterName string, hnp humiov1alpha1.HumioNodePool, nodeID int) map[string]string {
	labels := map[string]string{
		"app":             "humio",
		"humio_cr":        clusterName,
		"humio_node_pool": hnp.Name,
		"humio_node_id":   strconv.Itoa(nodeID),
	}
	return labels
}

func updateBaseURL(c client.Client, hc *humiov1alpha1.HumioCluster) error {
	baseURL, err := humio.GetHumioBaseURL(c, *hc)
	if err != nil {
		return fmt.Errorf("could not get token: %v", err)
	}
	hc.Status.BaseURL = baseURL
	return nil
}

func updateJWTToken(hc *humiov1alpha1.HumioCluster) error {
	token, err := humio.GetJWTForSingleUser(*hc)
	if err != nil {
		return fmt.Errorf("could not get token: %v", err)
	}
	hc.Status.JWTToken = token
	return nil
}

func (r *HumioClusterReconciler) ensureHeadlessServiceExists(ctx context.Context, hc humiov1alpha1.HumioCluster) error {
	var existingService corev1.Service
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, &existingService); err != nil {
		if k8serrors.IsNotFound(err) {
			service := constructHeadlessService(hc)
			err := r.Create(ctx, service)
			if err != nil {
				return fmt.Errorf("unable to create headless service for HumioCluster: %v", err)
			}
		}
	}
	return nil
}

func (r *HumioClusterReconciler) ensureServiceExists(ctx context.Context, hnt humiov1alpha1.HumioNodeType, hc humiov1alpha1.HumioCluster) error {
	var existingService corev1.Service
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      fmt.Sprintf("%s-%s", hc.Name, hnt),
	}, &existingService); err != nil {
		if k8serrors.IsNotFound(err) {
			service := constructService(hnt, hc)
			err := r.Create(ctx, service)
			if err != nil {
				return fmt.Errorf("unable to create %v service for HumioCluster: %v", hnt, err)
			}
		}
	}
	return nil
}

func constructEnvVarList(hc humiov1alpha1.HumioCluster, nodeID int) *[]corev1.EnvVar {
	return &[]corev1.EnvVar{
		{
			Name: "THIS_POD_IP",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name: "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.namespace",
				},
			},
		},
		{Name: "BOOTSTRAP_HOST_ID", Value: strconv.Itoa(nodeID)},
		{Name: "HUMIO_JVM_ARGS", Value: "-Xss2m -Xms256m -Xmx1536m -server -XX:+UseParallelOldGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC"},
		{Name: "HUMIO_PORT", Value: "8080"},
		{Name: "ELASTIC_PORT", Value: "9200"},
		{Name: "KAFKA_MANAGED_BY_HUMIO", Value: "true"},
		{Name: "AUTHENTICATION_METHOD", Value: "single-user"},
		{Name: "SINGLE_USER_PASSWORD", Value: hc.Spec.SingleUserPassword},
		{Name: "KAFKA_SERVERS", Value: "humio-cp-kafka-0.humio-cp-kafka-headless:9092"},
		{Name: "ZOOKEEPER_URL", Value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless:2181"},
		{
			Name:  "EXTERNAL_URL", // URL used by other Humio hosts.
			Value: fmt.Sprintf("http://$(POD_NAME).%s.$(POD_NAMESPACE).svc.cluster.local:$(HUMIO_PORT)", hc.Name),
			//Value: "http://$(POD_NAME).humio-humio-core-headless.$(POD_NAMESPACE).svc.cluster.local:8080",
			//Value: "http://$(THIS_POD_IP):$(HUMIO_PORT)",
		},
		{
			Name: "PUBLIC_URL", // URL used by users/browsers.
			//Value: "http://$(POD_NAME).humio-humio-core-headless.$(POD_NAMESPACE).svc.cluster.local:8080",
			Value: "http://$(THIS_POD_IP):$(HUMIO_PORT)",
		},
	}
}

func constructPod(hc humiov1alpha1.HumioCluster, hnp humiov1alpha1.HumioNodePool, nodeID int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%d", hc.Name, hnp.Name, nodeID),
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name, hnp, nodeID),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: hc.APIVersion,
					Kind:       hc.Kind,
					Name:       hc.Name,
					UID:        hc.UID,
				},
			},
		},
		Spec: corev1.PodSpec{
			Hostname:  fmt.Sprintf("%s-%s-%d", hc.Name, hnp.Name, nodeID),
			Subdomain: hc.Name,
			Containers: []corev1.Container{
				{
					Name:  "humio",
					Image: fmt.Sprintf("%s:%s", hc.Spec.Image, hc.Spec.Version),
					Ports: []corev1.ContainerPort{
						{
							Name:          "http",
							ContainerPort: 8080,
							Protocol:      "TCP",
						},
						{
							Name:          "es",
							ContainerPort: 9200,
							Protocol:      "TCP",
						},
					},
					Env:             *constructEnvVarList(hc, nodeID),
					ImagePullPolicy: "IfNotPresent",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "humio-data",
							MountPath: "/data",
						},
					},
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/api/v1/status",
								Port: intstr.IntOrString{IntVal: 8080},
							},
						},
						InitialDelaySeconds: 90,
						PeriodSeconds:       5,
						TimeoutSeconds:      2,
						SuccessThreshold:    1,
						FailureThreshold:    12,
					},
					LivenessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/api/v1/status",
								Port: intstr.IntOrString{IntVal: 8080},
							},
						},
						InitialDelaySeconds: 90,
						PeriodSeconds:       5,
						TimeoutSeconds:      2,
						SuccessThreshold:    1,
						FailureThreshold:    12,
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "humio-data",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: fmt.Sprintf("%s-%s-%d", hc.Name, hnp.Name, nodeID),
						},
					},
				},
			},
		},
	}
}

func constructHeadlessService(hc humiov1alpha1.HumioCluster) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hc.Name,
			Namespace: hc.Namespace,
			Labels: map[string]string{
				"app":      "humio",
				"humio_cr": hc.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: hc.APIVersion,
					Kind:       hc.Kind,
					Name:       hc.Name,
					UID:        hc.UID,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
			Selector: map[string]string{
				"app":      "humio",
				"humio_cr": hc.Name,
			},
			PublishNotReadyAddresses: true,
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 8080,
				},
			},
		},
	}
}

func constructService(hnt humiov1alpha1.HumioNodeType, hc humiov1alpha1.HumioCluster) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", hc.Name, hnt),
			Namespace: hc.Namespace,
			Labels: map[string]string{
				"app":                             "humio",
				"humio_cr":                        hc.Name,
				fmt.Sprintf("humio_type_%s", hnt): "true",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: hc.APIVersion,
					Kind:       hc.Kind,
					Name:       hc.Name,
					UID:        hc.UID,
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app":                             "humio",
				"humio_cr":                        hc.Name,
				fmt.Sprintf("humio_type_%s", hnt): "true",
			},
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 8080,
				},
			},
		},
	}
}

func constructPersistentVolumeClaim(hc humiov1alpha1.HumioCluster, hnp humiov1alpha1.HumioNodePool, nodeID int) *corev1.PersistentVolumeClaim {
	diskSizeGB := 5
	if hnp.NodeDiskSizeGB != 0 {
		diskSizeGB = hnp.NodeDiskSizeGB
	}

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%d", hc.Name, hnp.Name, nodeID),
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name, hnp, nodeID),
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: hc.APIVersion,
					Kind:       hc.Kind,
					Name:       hc.Name,
					UID:        hc.UID,
				},
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},

			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dGi", diskSizeGB)),
				},
			},
		},
	}
}

func (r *HumioClusterReconciler) ensureHumioPoolExists(ctx context.Context, humioCluster humiov1alpha1.HumioCluster, humioNodePool humiov1alpha1.HumioNodePool) error {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	for poolID := 0; poolID < humioNodePool.NodeCount; poolID++ {
		var existingPVC corev1.PersistentVolumeClaim
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: humioCluster.Namespace,
			Name:      fmt.Sprintf("%s-%s-%d", humioCluster.Name, humioNodePool.Name, humioNodePool.FirstNodeID+poolID),
		}, &existingPVC); err != nil {
			if k8serrors.IsNotFound(err) {
				pvc := constructPersistentVolumeClaim(humioCluster, humioNodePool, humioNodePool.FirstNodeID+poolID)
				err := r.Create(ctx, pvc)
				if err != nil {
					log.Info(fmt.Sprintf("unable to create pvc: %v", err))
					return fmt.Errorf("unable to create pvc for HumioCluster: %v", err)
				}
				log.Info(fmt.Sprintf("successfully created pvc for HumioCluster %s with node id: %d", humioCluster.Name, humioNodePool.FirstNodeID+poolID))
			}
		}

		var existingPod corev1.Pod
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: humioCluster.Namespace,
			Name:      fmt.Sprintf("%s-%s-%d", humioCluster.Name, humioNodePool.Name, humioNodePool.FirstNodeID+poolID),
		}, &existingPod); err != nil {
			if k8serrors.IsNotFound(err) {
				pod := constructPod(humioCluster, humioNodePool, humioNodePool.FirstNodeID+poolID)
				err := r.Create(ctx, pod)
				if err != nil {
					log.Info(fmt.Sprintf("unable to create pod: %v", err))
					return fmt.Errorf("unable to create Pod for HumioCluster: %v", err)
				}
				log.Info(fmt.Sprintf("successfully created pod for HumioCluster %s with node id: %d", humioCluster.Name, humioNodePool.FirstNodeID+poolID))
				metricPodsCreated.Inc()
			}
		}
	}
	return nil
}

func (r *HumioClusterReconciler) cleanupPod(humioCluster humiov1alpha1.HumioCluster, p corev1.Pod) {
	pID, err := strconv.Atoi(p.Labels["humio_node_id"])
	if err != nil {
		log.Info(fmt.Sprintf("could not parse node id: %v", err))
		return
	}
	nodeRegistered, err := humio.IsNodeRegistered(humioCluster, pID)
	if nodeRegistered {
		if err := humio.MoveStorageRouteAwayFromNode(humioCluster, pID); err != nil {
			log.Info(fmt.Sprintf("could not move storage route away from node %d: %v", pID, err))
		}
		if err := humio.MoveIngestRoutesAwayFromNode(humioCluster, pID); err != nil {
			log.Info(fmt.Sprintf("could not move ingest routes away from node %d: %v", pID, err))
		}
		if err := humio.StartDataRedistribution(humioCluster); err != nil {
			log.Info(fmt.Sprintf("could not start data redistribution: %v", err))
		}
	}

	safeToUnregisterPod, err := humio.CanBeSafelyUnregistered(humioCluster, pID)
	if err != nil {
		log.Info(fmt.Sprintf("could not determine if it is safe to delete pod: %v", err))
		return
	}
	if safeToUnregisterPod {
		if err := humio.ClusterUnregisterNode(humioCluster, pID); err != nil {
			log.Info(fmt.Sprintf("could not unregister node %d: %v", pID, err))
		}
	}
	if !nodeRegistered {
		log.Info(fmt.Sprint("deleting pod"))
		if err := r.Client.DeleteAllOf(
			context.Background(),
			&corev1.Pod{},
			client.InNamespace(humioCluster.Namespace),
			client.MatchingLabels{
				"app":             "humio",
				"humio_cr":        p.Labels["humio_cr"],
				"humio_node_pool": p.Labels["humio_node_pool"],
				"humio_node_id":   p.Labels["humio_node_id"],
			},
		); err != nil {
			log.Info(fmt.Sprintf("could not delete pod with label humio_node_id=%d: %v", pID, err))
		}

		if err := r.Client.DeleteAllOf(
			context.Background(),
			&corev1.PersistentVolumeClaim{},
			client.InNamespace(humioCluster.Namespace),
			client.MatchingLabels{
				"app":             "humio",
				"humio_cr":        p.Labels["humio_cr"],
				"humio_node_pool": p.Labels["humio_node_pool"],
				"humio_node_id":   p.Labels["humio_node_id"],
			},
		); err != nil {
			log.Info(fmt.Sprintf("could not delete pvc with label humio_node_id=%d: %v", pID, err))
		}
	}
}

func (r *HumioClusterReconciler) cleanupHumioNodePoolResources(ctx context.Context, humioCluster humiov1alpha1.HumioCluster, humioNodePool humiov1alpha1.HumioNodePool) error {
	// Grab list of all pods associated with the cluster
	allPodsForCluster, err := humio.ListPods(r.Client, humioCluster)
	if err != nil {
		return fmt.Errorf("could not list pods for cluster: %v", err)
	}
	for _, p := range allPodsForCluster {
		if p.Labels["humio_node_pool"] != humioNodePool.Name {
			// Ignore pods not part of the relevant node pool
			continue
		}
		if p.DeletionTimestamp != nil {
			// Ignore pods already marked for deletion
			continue
		}
		pID, err := strconv.Atoi(p.Labels["humio_node_id"])
		if err != nil {
			return fmt.Errorf("could not convert humio_node_id to int: %v", err)
		}
		// If outside expected node id range, then remove it
		if pID < humioNodePool.FirstNodeID || pID >= humioNodePool.FirstNodeID+humioNodePool.NodeCount {
			r.cleanupPod(humioCluster, p)
		}
		// Update node type labels
		p.Labels[fmt.Sprintf("humio_type_%s", humiov1alpha1.Ingest)] = fmt.Sprintf("%v", humio.IsIngestNode(humioNodePool))
		p.Labels[fmt.Sprintf("humio_type_%s", humiov1alpha1.Digest)] = fmt.Sprintf("%v", humio.IsDigestNode(humioNodePool))
		p.Labels[fmt.Sprintf("humio_type_%s", humiov1alpha1.Storage)] = fmt.Sprintf("%v", humio.IsStorageNode(humioNodePool))
	}
	return nil
}

// SetupWithManager ...
func (r *HumioClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(&corev1.Pod{}, humioClusterOwnerKey, func(rawObj runtime.Object) []string {
		// grab the pod object, extract the owner...
		job := rawObj.(*corev1.Pod)
		owner := metav1.GetControllerOf(job)
		if owner == nil {
			return nil
		}
		// ...make sure it's a HumioCluster...
		if owner.APIVersion != apiGVStr || owner.Kind != "HumioCluster" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioCluster{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

func init() {
	metrics.Registry.MustRegister(metricPodsCreated)
	metrics.Registry.MustRegister(metricPodsDeleted)
}
