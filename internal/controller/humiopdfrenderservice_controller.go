package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
)

// HumioPdfRenderServiceReconciler reconciles a HumioPdfRenderService object
type HumioPdfRenderServiceReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

const humioPdfRenderServiceFinalizer = "core.humio.com/finalizer"

// Reconcile implements the reconciliation logic for HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioPdfRenderService")

	// Fetch the HumioPdfRenderService instance
	hprs := &corev1alpha1.HumioPdfRenderService{}
	err := r.Get(ctx, req.NamespacedName, hprs)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", hprs.UID)

	// If the CR's namespace is empty, default it.
	if hprs.Namespace == "" {
		ns := r.Namespace
		if ns == "" {
			ns = req.Namespace
		}
		r.Log.Info("CR namespace is empty, defaulting", "Namespace", ns)
		hprs.Namespace = ns
	}

	// Check if the resource is being deleted
	if !hprs.ObjectMeta.DeletionTimestamp.IsZero() {
		r.Log.Info("HumioPdfRenderService is being deleted")
		if helpers.ContainsElement(hprs.GetFinalizers(), humioPdfRenderServiceFinalizer) {
			// Run finalization logic. If it fails, don't remove the finalizer so
			// we can retry during the next reconciliation
			if err := r.finalize(ctx, hprs); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Failed to run finalizer")
			}

			// Remove the finalizer once finalization is done
			hprs.SetFinalizers(helpers.RemoveElement(hprs.GetFinalizers(), humioPdfRenderServiceFinalizer))
			if err := r.Update(ctx, hprs); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Failed to remove finalizer")
			}
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR if not already present
	if !helpers.ContainsElement(hprs.GetFinalizers(), humioPdfRenderServiceFinalizer) {
		r.Log.Info("Adding finalizer to HumioPdfRenderService")
		if err := r.addFinalizer(ctx, hprs); err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "Failed to add finalizer")
		}
	}

	// Set up a deferred function to always update the status before returning
	defer func() {
		deployment := &appsv1.Deployment{}
		deploymentErr := r.Client.Get(ctx, types.NamespacedName{
			Name:      "pdf-render-service",
			Namespace: hprs.Namespace,
		}, deployment)

		if deploymentErr == nil {
			// Update status with pod names and readiness
			hprs.Status.ReadyReplicas = deployment.Status.ReadyReplicas

			// Gather pod names and update the nodes list
			podList := &corev1.PodList{}
			listOpts := []client.ListOption{
				client.InNamespace(hprs.Namespace),
				client.MatchingLabels(map[string]string{
					"app":                           "humio-pdf-render-service",
					"humio-pdf-render-service-name": hprs.Name,
				}),
			}
			if err = r.List(ctx, podList, listOpts...); err == nil {
				nodes := make([]string, 0, len(podList.Items))
				for _, pod := range podList.Items {
					nodes = append(nodes, pod.Name)
				}
				hprs.Status.Nodes = nodes
			}

			// Set state based on deployment status
			if deployment.Status.ReadyReplicas > 0 && deployment.Status.ReadyReplicas == deployment.Status.Replicas {
				_ = r.setState(ctx, corev1alpha1.HumioPdfRenderServiceStateExists, hprs)
			} else if deployment.Status.ReadyReplicas == 0 {
				_ = r.setState(ctx, corev1alpha1.HumioPdfRenderServiceStateNotFound, hprs)
			} else {
				_ = r.setState(ctx, corev1alpha1.HumioPdfRenderServiceStateUnknown, hprs)
			}
		} else {
			_ = r.setState(ctx, corev1alpha1.HumioPdfRenderServiceStateNotFound, hprs)
		}
	}()

	// Reconcile Deployment using controllerutil.CreateOrUpdate
	if err := r.reconcileDeployment(ctx, hprs); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "Failed to reconcile Deployment")
	}

	// Reconcile Service using controllerutil.CreateOrUpdate
	if err := r.reconcileService(ctx, hprs); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "Failed to reconcile Service")
	}

	r.Log.Info("Reconciliation completed successfully")
	return ctrl.Result{RequeueAfter: time.Second * 30}, nil
}

// finalize handles the cleanup when the resource is deleted
func (r *HumioPdfRenderServiceReconciler) finalize(ctx context.Context, hprs *corev1alpha1.HumioPdfRenderService) error {
	r.Log.Info("Running finalizer for HumioPdfRenderService")

	// Nothing special to do for this resource as Kubernetes will handle
	// the garbage collection of owned resources (deployment and service)
	// This is just a placeholder for any additional cleanup if needed in the future
	return nil
}

// addFinalizer adds the finalizer to the resource
func (r *HumioPdfRenderServiceReconciler) addFinalizer(ctx context.Context, hprs *corev1alpha1.HumioPdfRenderService) error {
	r.Log.Info("Adding Finalizer for the HumioPdfRenderService")
	hprs.SetFinalizers(append(hprs.GetFinalizers(), humioPdfRenderServiceFinalizer))

	// Update CR
	err := r.Update(ctx, hprs)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioPdfRenderService with finalizer")
	}
	return nil
}

// setState updates the state field in the status
func (r *HumioPdfRenderServiceReconciler) setState(ctx context.Context, state string, hprs *corev1alpha1.HumioPdfRenderService) error {
	if hprs.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting PDF render service state to %s", state))
	hprs.Status.State = state
	return r.Status().Update(ctx, hprs)
}

// logErrorAndReturn logs an error and returns it with additional context
func (r *HumioPdfRenderServiceReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// constructDeployment constructs a Deployment for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDeployment(hprs *corev1alpha1.HumioPdfRenderService) *appsv1.Deployment {
	deploymentName := "pdf-render-service"

	labels := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": hprs.Name,
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: hprs.Namespace,
		},
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: &hprs.Spec.Replicas,
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      labels,
				Annotations: hprs.Spec.Annotations,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: hprs.Spec.ServiceAccountName,
				Affinity:           hprs.Spec.Affinity,
				Containers: []corev1.Container{{
					Name:            "humio-pdf-render-service",
					Image:           hprs.Spec.Image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Resources:       hprs.Spec.Resources,
					Ports: []corev1.ContainerPort{{
						ContainerPort: hprs.Spec.Port,
						Name:          "http",
					}},
					Env:            hprs.Spec.Env,
					LivenessProbe:  hprs.Spec.LivenessProbe,
					ReadinessProbe: hprs.Spec.ReadinessProbe,
				}},
			},
		},
	}
	return deployment
}

// reconcileDeployment reconciles the Deployment for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, hprs *corev1alpha1.HumioPdfRenderService) error {
	deployment := r.constructDeployment(hprs)
	deployment.SetNamespace(hprs.Namespace)

	if err := controllerutil.SetControllerReference(hprs, deployment, r.Scheme); err != nil {
		return err
	}

	existingDeployment := &appsv1.Deployment{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
	}, existingDeployment)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Creating Deployment", "Deployment.Name", deployment.Name, "Deployment.Namespace", deployment.Namespace, "Image", hprs.Spec.Image)
			return r.Client.Create(ctx, deployment)
		}
		return err
	}

	// Check if we need to update the deployment
	needsUpdate := false

	// Check image
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 &&
		existingDeployment.Spec.Template.Spec.Containers[0].Image != hprs.Spec.Image {
		r.Log.Info("Image changed", "Old", existingDeployment.Spec.Template.Spec.Containers[0].Image, "New", hprs.Spec.Image)
		needsUpdate = true
	}

	// Check replicas
	if existingDeployment.Spec.Replicas == nil || *existingDeployment.Spec.Replicas != hprs.Spec.Replicas {
		r.Log.Info("Replicas changed", "Old", existingDeployment.Spec.Replicas, "New", hprs.Spec.Replicas)
		needsUpdate = true
	}

	// Check for resource changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 &&
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Resources, hprs.Spec.Resources) {
		r.Log.Info("Resources changed")
		needsUpdate = true
	}

	// Check for environment variable changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 &&
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Env, hprs.Spec.Env) {
		r.Log.Info("Environment variables changed")
		needsUpdate = true
	}

	// Check for probe changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 && (!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe, hprs.Spec.LivenessProbe) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe, hprs.Spec.ReadinessProbe)) {
		r.Log.Info("Probe configuration changed")
		needsUpdate = true
	}

	// Check for ServiceAccount changes
	if existingDeployment.Spec.Template.Spec.ServiceAccountName != hprs.Spec.ServiceAccountName {
		r.Log.Info("ServiceAccount changed", "Old", existingDeployment.Spec.Template.Spec.ServiceAccountName, "New", hprs.Spec.ServiceAccountName)
		needsUpdate = true
	}

	// Check for Affinity changes
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Affinity, hprs.Spec.Affinity) {
		r.Log.Info("Affinity configuration changed")
		needsUpdate = true
	}

	// Check for annotation changes
	if !reflect.DeepEqual(existingDeployment.Spec.Template.ObjectMeta.Annotations, hprs.Spec.Annotations) {
		r.Log.Info("Annotations changed")
		needsUpdate = true
	}

	// If an update is needed or we want to force a rolling update, apply all the necessary changes
	if needsUpdate {
		// Update critical fields
		existingDeployment.Spec.Replicas = &hprs.Spec.Replicas

		// Update container details
		if len(existingDeployment.Spec.Template.Spec.Containers) > 0 {
			existingDeployment.Spec.Template.Spec.Containers[0].Image = hprs.Spec.Image
			existingDeployment.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent
			existingDeployment.Spec.Template.Spec.Containers[0].Resources = hprs.Spec.Resources
			existingDeployment.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{{
				ContainerPort: hprs.Spec.Port,
				Name:          "http",
			}}
			existingDeployment.Spec.Template.Spec.Containers[0].Env = hprs.Spec.Env
			existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe = hprs.Spec.LivenessProbe
			existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe = hprs.Spec.ReadinessProbe
		}

		// Update labels and annotations
		if existingDeployment.Spec.Template.ObjectMeta.Labels == nil {
			existingDeployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
		}
		existingDeployment.Spec.Template.ObjectMeta.Labels["app"] = "humio-pdf-render-service"
		existingDeployment.Spec.Template.ObjectMeta.Labels["humio-pdf-render-service-name"] = hprs.Name

		newAnnotations := map[string]string{}
		if hprs.Spec.Annotations != nil {
			for k, v := range hprs.Spec.Annotations {
				newAnnotations[k] = v
			}
		}
		existingDeployment.Spec.Template.ObjectMeta.Annotations = newAnnotations

		// Update other pod spec fields
		existingDeployment.Spec.Template.Spec.ServiceAccountName = hprs.Spec.ServiceAccountName
		existingDeployment.Spec.Template.Spec.Affinity = hprs.Spec.Affinity

		// Always add a timestamp annotation to force a rollout when configuration changes
		// This ensures pods get recreated when we update the CR
		if existingDeployment.Spec.Template.Annotations == nil {
			existingDeployment.Spec.Template.Annotations = map[string]string{}
		}
		existingDeployment.Spec.Template.Annotations["humio-pdf-render-service/restartedAt"] = time.Now().Format(time.RFC3339)

		r.Log.Info("Updating Deployment", "Deployment.Name", existingDeployment.Name,
			"Deployment.Namespace", existingDeployment.Namespace,
			"Image", hprs.Spec.Image)

		return r.Client.Update(ctx, existingDeployment)
	}

	r.Log.Info("No changes needed for Deployment", "Deployment.Name", existingDeployment.Name)
	return nil
}

// constructService constructs a Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructService(hprs *corev1alpha1.HumioPdfRenderService) *corev1.Service {
	serviceName := "pdf-render-service"

	labels := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": hprs.Name,
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: hprs.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP, // Always use ClusterIP as we've restricted ServiceType
			Ports: []corev1.ServicePort{{
				Port:       hprs.Spec.Port,
				TargetPort: intstr.FromInt(int(hprs.Spec.Port)),
				Name:       "http",
			}},
		},
	}

	return service
}

// reconcileService reconciles the Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileService(ctx context.Context, hprs *corev1alpha1.HumioPdfRenderService) error {
	service := r.constructService(hprs)
	service.SetNamespace(hprs.Namespace)

	if err := controllerutil.SetControllerReference(hprs, service, r.Scheme); err != nil {
		return err
	}

	existingService := &corev1.Service{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      service.Name,
		Namespace: service.Namespace,
	}, existingService)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Creating Service", "Service.Name", service.Name,
				"Service.Namespace", service.Namespace)
			return r.Client.Create(ctx, service)
		}
		return err
	}

	// Check if we need to update the service
	needsUpdate := false

	// Check service type - should always be ClusterIP now
	if existingService.Spec.Type != corev1.ServiceTypeClusterIP {
		r.Log.Info("Service type changed", "Old", existingService.Spec.Type, "New", corev1.ServiceTypeClusterIP)
		needsUpdate = true
	}

	// Check port configuration
	if len(existingService.Spec.Ports) != 1 ||
		existingService.Spec.Ports[0].Port != hprs.Spec.Port ||
		existingService.Spec.Ports[0].TargetPort.IntVal != hprs.Spec.Port {
		r.Log.Info("Port configuration changed")
		needsUpdate = true
	}

	// Check selector changes
	expectedSelector := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": hprs.Name,
	}
	if !reflect.DeepEqual(existingService.Spec.Selector, expectedSelector) {
		r.Log.Info("Service selector changed")
		needsUpdate = true
	}

	// If an update is needed, apply all the necessary changes
	if needsUpdate {
		// Update the service specification
		existingService.Spec.Type = corev1.ServiceTypeClusterIP
		existingService.Spec.Ports = []corev1.ServicePort{{
			Port:       hprs.Spec.Port,
			TargetPort: intstr.FromInt(int(hprs.Spec.Port)),
			Name:       "http",
		}}

		// Update labels
		if existingService.Labels == nil {
			existingService.Labels = map[string]string{}
		}
		existingService.Labels["app"] = "humio-pdf-render-service"
		existingService.Labels["humio-pdf-render-service-name"] = hprs.Name

		// Update selector
		existingService.Spec.Selector = expectedSelector

		r.Log.Info("Updating Service", "Service.Name", existingService.Name,
			"Service.Namespace", existingService.Namespace)

		return r.Client.Update(ctx, existingService)
	}

	r.Log.Info("No changes needed for Service", "Service.Name", existingService.Name)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.HumioPdfRenderService{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
