package humiocluster

import (
	"context"
	"fmt"
	"strconv"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	log               = logf.Log.WithName("controller_humiocluster")
	metricPodsCreated = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "humio_controller_pods_created_total",
		Help: "Total number of pod objects created by controller",
	})
	metricPodsDeleted = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "humio_controller_pods_deleted_total",
		Help: "Total number of pod objects deleted by controller",
	})
)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new HumioCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileHumioCluster{client: mgr.GetClient(), scheme: mgr.GetScheme()}
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

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner HumioCluster
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &corev1alpha1.HumioCluster{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileHumioCluster implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileHumioCluster{}

// ReconcileHumioCluster reconciles a HumioCluster object
type ReconcileHumioCluster struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a HumioCluster object and makes changes based on the state read
// and what is in the HumioCluster.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileHumioCluster) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling HumioCluster")

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

	// Ensure pods exist
	err = r.ensurePodsExist(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// All done, don't requeue
	return reconcile.Result{}, nil
}

func (r *ReconcileHumioCluster) ensurePodsExist(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) error {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	for nodeID := 0; nodeID < humioCluster.Spec.NodeCount; nodeID++ {
		var existingPod corev1.Pod
		pod := constructPod(humioCluster, nodeID)

		if err := controllerutil.SetControllerReference(humioCluster, pod, r.scheme); err != nil {
			return err
		}

		if err := r.client.Get(context.TODO(), types.NamespacedName{
			Namespace: humioCluster.Namespace,
			Name:      fmt.Sprintf("%s-core-%d", humioCluster.Name, nodeID),
		}, &existingPod); err != nil {
			if k8serrors.IsNotFound(err) {

				err := r.client.Create(context.TODO(), pod)
				if err != nil {
					log.Info(fmt.Sprintf("unable to create pod: %v", err))
					return fmt.Errorf("unable to create Pod for HumioCluster: %v", err)
				}
				log.Info(fmt.Sprintf("successfully created pod %s for HumioCluster %s with node id: %d", pod.Name, humioCluster.Name, nodeID))
				metricPodsCreated.Inc()
			}
		}
	}
	return nil
}

func constructPod(hc *corev1alpha1.HumioCluster, nodeID int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-core-%d", hc.Name, nodeID),
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name, nodeID),
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
			Hostname:  fmt.Sprintf("%s-core-%d", hc.Name, nodeID),
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
					Env:             envVarList(hc),
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
							ClaimName: fmt.Sprintf("%s-core-%d", hc.Name, nodeID),
						},
					},
				},
			},
		},
	}
}

func envVarList(humioCluster *corev1alpha1.HumioCluster) []corev1.EnvVar {
	setEnvironmentVariableDefaults(humioCluster)
	return humioCluster.Spec.EnvironmentVariables
}

func labelsForHumio(clusterName string, nodeID int) map[string]string {
	labels := map[string]string{
		"app":           "humio",
		"humio_cr":      clusterName,
		"humio_node_id": strconv.Itoa(nodeID),
	}
	return labels
}

// newPodForCR returns a busybox pod with the same name/namespace as the cr
func newPodForCR(cr *corev1alpha1.HumioCluster) *corev1.Pod {
	labels := map[string]string{
		"app": cr.Name,
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pod",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "busybox",
					Image:   "busybox",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}
}

func init() {
	metrics.Registry.MustRegister(metricPodsCreated)
	metrics.Registry.MustRegister(metricPodsDeleted)
}
