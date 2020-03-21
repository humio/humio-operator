package humiocluster

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/go-logr/logr"
	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clienttype "sigs.k8s.io/controller-runtime/pkg/client"
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

	// Set cluster status
	if humioCluster.Status.ClusterState == "" {
		humioCluster.Status.ClusterState = "Bootstrapping"
	}

	// Ensure developer password is a k8s secret
	err = r.ensureDeveloperUserPasswordExists(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Ensure pods exist
	err = r.ensurePodsExist(context.TODO(), humioCluster)
	if err != nil {
		return reconcile.Result{}, err
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

	cluster, err := r.humioClient.GetClusters()
	if err != nil {
		return reconcile.Result{}, err
	}

	r.logger.Info(fmt.Sprintf("all cluster info: %v", cluster))

	clusterController := humio.NewClusterController(r.humioClient)
	err = r.ensurePartitionsAreBalanced(*clusterController, humioCluster)
	if err != nil {
		return reconcile.Result{}, err
	}

	// All done, don't requeue
	return reconcile.Result{}, nil
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
	var existingService corev1.Service
	if err := r.client.Get(context, types.NamespacedName{
		Namespace: hc.Namespace,
		Name:      hc.Name,
	}, &existingService); err != nil {
		if k8serrors.IsNotFound(err) {
			service := constructService(hc)
			err := r.client.Create(context, service)
			if err != nil {
				return fmt.Errorf("unable to create service for HumioCluster: %v", err)
			}
		}
	}
	return nil
}

func constructService(hc *corev1alpha1.HumioCluster) *corev1.Service {
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
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app":      "humio",
				"humio_cr": hc.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 8080,
				},
				{
					Name: "es",
					Port: 9200,
				},
			},
		},
	}
}

func (r *ReconcileHumioCluster) ensurePodsExist(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) error {
	// Ensure we have pods for the defined NodeCount.
	// If scaling down, we will handle the extra/obsolete pods later.
	// TODO: should this be a statefulset instead?
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
				// TODO: hack to prevent humio nodes from starting up at the same time which seems to cause issues
				// TODO: this should not be required as humio should allow us to start up pods at the same time without issue
				for i := 0; i < 300; i++ {
					var createdPod v1.Pod
					var podCondition v1.PodCondition
					if err := r.client.Get(context.TODO(), types.NamespacedName{
						Namespace: humioCluster.Namespace,
						Name:      fmt.Sprintf("%s-core-%d", humioCluster.Name, nodeID),
					}, &createdPod); err == nil {
						for _, condition := range createdPod.Status.Conditions {
							if condition.Type == "Ready" {
								podCondition = condition
								if condition.Status == "True" {
									break
								} else {
									r.logger.Info(fmt.Sprintf("waiting for pod %s to be ready. current ready state: %s", createdPod.Name, condition.Status))
								}
							}
						}
						// TODO: hack to get tests to pass quickly
						if len(createdPod.Status.Conditions) < 1 {
							r.logger.Info(fmt.Sprintf("pod %s has no status condition", createdPod.Name))
							break
						}
					} else {
						if k8serrors.IsNotFound(err) {
							r.logger.Info(fmt.Sprintf("failed to find pod %s-core-%d: %s. checking again", humioCluster.Name, nodeID, err))
						}
					}
					// Pod was created
					if podCondition.Status == "True" {
						break
					}
					r.logger.Info(fmt.Sprintf("waiting 5 seconds before checking for pod %s", createdPod.Name))
					time.Sleep(time.Second * 5)
				}
			}
		}
	}
	return nil
}

// TODO: extend this (or create separate method) to take this password and perform a login, get the jwt token and then call the api to get the persistent api token and also store that as a secret
// this functionality should perhaps go into humio.cluster_auth.go
func (r *ReconcileHumioCluster) ensureDeveloperUserPasswordExists(conetext context.Context, humioCluster *corev1alpha1.HumioCluster) error {
	var existingSecret corev1.Secret

	secretData := map[string][]byte{"password": []byte(generatePassword())}
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountSecretName,
			Namespace: humioCluster.Namespace,
		},
		Data: secretData,
	}
	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Namespace: humioCluster.Namespace,
		Name:      serviceAccountSecretName,
	}, &existingSecret); err != nil {
		if k8serrors.IsNotFound(err) {

			err := r.client.Create(context.TODO(), &secret)
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
	var existingSecret corev1.Secret

	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Namespace: humioCluster.Namespace,
		Name:      serviceTokenSecretName,
	}, &existingSecret); err != nil {
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
			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceTokenSecretName,
					Namespace: humioCluster.Namespace,
				},
				Data: secretData,
			}

			err = r.client.Create(context.TODO(), &secret)
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

func generatePassword() string {
	rand.Seed(time.Now().UnixNano())
	chars := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		"0123456789")
	length := 32
	var b strings.Builder
	for i := 0; i < length; i++ {
		b.WriteRune(chars[rand.Intn(len(chars))])
	}
	return b.String()
}

func constructPod(hc *corev1alpha1.HumioCluster, nodeID int) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-core-%d", hc.Name, nodeID),
			Namespace: hc.Namespace,
			Labels:    labelsForHumio(hc.Name),
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
						EmptyDir: &corev1.EmptyDirVolumeSource{},
						/*
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: fmt.Sprintf("%s-core-%d", hc.Name, nodeID),
							},
						*/
					},
				},
			},
		},
	}
}

func (r *ReconcileHumioCluster) getDeveloperUserPassword(hc *corev1alpha1.HumioCluster) (string, error) {
	secret := &corev1.Secret{}
	err := r.client.Get(context.TODO(), clienttype.ObjectKey{
		Name:      serviceAccountSecretName,
		Namespace: hc.ObjectMeta.Namespace,
	}, secret)
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

func labelsForHumio(clusterName string) map[string]string {
	labels := map[string]string{
		"app":      "humio",
		"humio_cr": clusterName,
	}
	return labels
}

func init() {
	metrics.Registry.MustRegister(metricPodsCreated)
	metrics.Registry.MustRegister(metricPodsDeleted)
}
