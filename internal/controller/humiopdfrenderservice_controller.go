package controller

import (
	"context"
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
	"sigs.k8s.io/controller-runtime/pkg/log"

	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioPdfRenderServiceReconciler reconciles a HumioPdfRenderService object
type HumioPdfRenderServiceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Log       logr.Logger
	Namespace string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices/finalizers,verbs=update

// Reconcile implements the reconciliation logic for HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// If a namespace filter is set on the reconciler, only process requests in that namespace.
	if r.Namespace != "" && req.Namespace != r.Namespace {
		logger.Info("Skipping reconcile: request namespace does not match reconciler namespace filter", "req.Namespace", req.Namespace, "filter", r.Namespace)
		return ctrl.Result{}, nil
	}

	// Fetch the HumioPdfRenderService instance
	var humioPdfRenderService corev1alpha1.HumioPdfRenderService
	if err := r.Get(ctx, req.NamespacedName, &humioPdfRenderService); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// If the CR's namespace is empty, default it.
	if humioPdfRenderService.Namespace == "" {
		ns := r.Namespace
		if ns == "" {
			ns = req.Namespace
		}
		logger.Info("CR namespace is empty, defaulting", "Namespace", ns)
		humioPdfRenderService.Namespace = ns
	}

	logger.Info("Reconciling HumioPdfRenderService", "Name", humioPdfRenderService.Name, "Namespace", humioPdfRenderService.Namespace)

	// Update status before returning
	defer func() {
		if err := r.Status().Update(ctx, &humioPdfRenderService); err != nil {
			logger.Error(err, "Failed to update status")
		}
	}()

	// Handle deletion and finalizers if needed.
	if !humioPdfRenderService.ObjectMeta.DeletionTimestamp.IsZero() {
		logger.Info("HumioPdfRenderService is being deleted", "Name", humioPdfRenderService.Name)
		return ctrl.Result{}, nil
	}

	// Reconcile Deployment using controllerutil.CreateOrUpdate
	if err := r.reconcileDeployment(ctx, &humioPdfRenderService); err != nil {
		logger.Error(err, "Failed to reconcile Deployment")
		return ctrl.Result{}, err
	}

	// Reconcile Service using controllerutil.CreateOrUpdate
	if err := r.reconcileService(ctx, &humioPdfRenderService); err != nil {
		logger.Error(err, "Failed to reconcile Service")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// constructDeployment constructs a Deployment for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDeployment(humioPdfRenderService *corev1alpha1.HumioPdfRenderService) *appsv1.Deployment {
	deploymentName := "pdf-render-service"

	labels := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": humioPdfRenderService.Name,
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: humioPdfRenderService.Namespace,
		},
	}
	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: &humioPdfRenderService.Spec.Replicas,
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      labels,
				Annotations: humioPdfRenderService.Spec.Annotations,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: humioPdfRenderService.Spec.ServiceAccountName,
				Affinity:           humioPdfRenderService.Spec.Affinity,
				Containers: []corev1.Container{{
					Name:            "humio-pdf-render-service",
					Image:           humioPdfRenderService.Spec.Image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Resources:       humioPdfRenderService.Spec.Resources,
					Ports: []corev1.ContainerPort{{
						ContainerPort: humioPdfRenderService.Spec.Port,
						Name:          "http",
					}},
					Env:            humioPdfRenderService.Spec.Env,
					LivenessProbe:  humioPdfRenderService.Spec.LivenessProbe,
					ReadinessProbe: humioPdfRenderService.Spec.ReadinessProbe,
				}},
			},
		},
	}
	return deployment
}

// reconcileDeployment reconciles the Deployment for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, humioPdfRenderService *corev1alpha1.HumioPdfRenderService) error {
	logger := log.FromContext(ctx)
	deployment := r.constructDeployment(humioPdfRenderService)
	deployment.SetNamespace(humioPdfRenderService.Namespace)

	if err := controllerutil.SetControllerReference(humioPdfRenderService, deployment, r.Scheme); err != nil {
		return err
	}

	existingDeployment := &appsv1.Deployment{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
	}, existingDeployment)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("Creating Deployment", "Deployment.Name", deployment.Name, "Deployment.Namespace", deployment.Namespace, "Image", humioPdfRenderService.Spec.Image)
			return r.Client.Create(ctx, deployment)
		}
		return err
	}

	// Check if we need to update the deployment
	needsUpdate := false

	// Check image
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 &&
		existingDeployment.Spec.Template.Spec.Containers[0].Image != humioPdfRenderService.Spec.Image {
		logger.Info("Image changed", "Old", existingDeployment.Spec.Template.Spec.Containers[0].Image, "New", humioPdfRenderService.Spec.Image)
		needsUpdate = true
	}

	// Check replicas
	if existingDeployment.Spec.Replicas == nil || *existingDeployment.Spec.Replicas != humioPdfRenderService.Spec.Replicas {
		logger.Info("Replicas changed", "Old", existingDeployment.Spec.Replicas, "New", humioPdfRenderService.Spec.Replicas)
		needsUpdate = true
	}

	// Check for resource changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 &&
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Resources, humioPdfRenderService.Spec.Resources) {
		logger.Info("Resources changed")
		needsUpdate = true
	}

	// Check for environment variable changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 &&
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Env, humioPdfRenderService.Spec.Env) {
		logger.Info("Environment variables changed")
		needsUpdate = true
	}

	// Check for probe changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 && (!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe, humioPdfRenderService.Spec.LivenessProbe) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe, humioPdfRenderService.Spec.ReadinessProbe)) {
		logger.Info("Probe configuration changed")
		needsUpdate = true
	}

	// Check for ServiceAccount changes
	if existingDeployment.Spec.Template.Spec.ServiceAccountName != humioPdfRenderService.Spec.ServiceAccountName {
		logger.Info("ServiceAccount changed", "Old", existingDeployment.Spec.Template.Spec.ServiceAccountName, "New", humioPdfRenderService.Spec.ServiceAccountName)
		needsUpdate = true
	}

	// Check for Affinity changes
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Affinity, humioPdfRenderService.Spec.Affinity) {
		logger.Info("Affinity configuration changed")
		needsUpdate = true
	}

	// Check for annotation changes
	if !reflect.DeepEqual(existingDeployment.Spec.Template.ObjectMeta.Annotations, humioPdfRenderService.Spec.Annotations) {
		logger.Info("Annotations changed")
		needsUpdate = true
	}

	// If an update is needed or we want to force a rolling update, apply all the necessary changes
	if needsUpdate {
		// Update critical fields
		existingDeployment.Spec.Replicas = &humioPdfRenderService.Spec.Replicas

		// Update container details
		if len(existingDeployment.Spec.Template.Spec.Containers) > 0 {
			existingDeployment.Spec.Template.Spec.Containers[0].Image = humioPdfRenderService.Spec.Image
			existingDeployment.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent
			existingDeployment.Spec.Template.Spec.Containers[0].Resources = humioPdfRenderService.Spec.Resources
			existingDeployment.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{{
				ContainerPort: humioPdfRenderService.Spec.Port,
				Name:          "http",
			}}
			existingDeployment.Spec.Template.Spec.Containers[0].Env = humioPdfRenderService.Spec.Env
			existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe = humioPdfRenderService.Spec.LivenessProbe
			existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe = humioPdfRenderService.Spec.ReadinessProbe
		}

		// Update labels and annotations
		if existingDeployment.Spec.Template.ObjectMeta.Labels == nil {
			existingDeployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
		}
		existingDeployment.Spec.Template.ObjectMeta.Labels["app"] = "humio-pdf-render-service"
		existingDeployment.Spec.Template.ObjectMeta.Labels["humio-pdf-render-service-name"] = humioPdfRenderService.Name

		newAnnotations := map[string]string{}
		if humioPdfRenderService.Spec.Annotations != nil {
			for k, v := range humioPdfRenderService.Spec.Annotations {
				newAnnotations[k] = v
			}
		}
		existingDeployment.Spec.Template.ObjectMeta.Annotations = newAnnotations

		// Update other pod spec fields
		existingDeployment.Spec.Template.Spec.ServiceAccountName = humioPdfRenderService.Spec.ServiceAccountName
		existingDeployment.Spec.Template.Spec.Affinity = humioPdfRenderService.Spec.Affinity

		// Always add a timestamp annotation to force a rollout when configuration changes
		// This ensures pods get recreated when we update the CR
		if existingDeployment.Spec.Template.Annotations == nil {
			existingDeployment.Spec.Template.Annotations = map[string]string{}
		}
		existingDeployment.Spec.Template.Annotations["humio-pdf-render-service/restartedAt"] = time.Now().Format(time.RFC3339)

		logger.Info("Updating Deployment", "Deployment.Name", existingDeployment.Name,
			"Deployment.Namespace", existingDeployment.Namespace,
			"Image", humioPdfRenderService.Spec.Image)

		return r.Client.Update(ctx, existingDeployment)
	}

	logger.Info("No changes needed for Deployment", "Deployment.Name", existingDeployment.Name)
	return nil
}

// constructService constructs a Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructService(humioPdfRenderService *corev1alpha1.HumioPdfRenderService) *corev1.Service {
	serviceName := "pdf-render-service"

	labels := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": humioPdfRenderService.Name,
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: humioPdfRenderService.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     corev1.ServiceTypeClusterIP, // Always use ClusterIP as we've restricted ServiceType
			Ports: []corev1.ServicePort{{
				Port:       humioPdfRenderService.Spec.Port,
				TargetPort: intstr.FromInt(int(humioPdfRenderService.Spec.Port)),
				Name:       "http",
			}},
		},
	}

	return service
}

// reconcileService reconciles the Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileService(ctx context.Context, humioPdfRenderService *corev1alpha1.HumioPdfRenderService) error {
	logger := log.FromContext(ctx)
	service := r.constructService(humioPdfRenderService)
	service.SetNamespace(humioPdfRenderService.Namespace)

	if err := controllerutil.SetControllerReference(humioPdfRenderService, service, r.Scheme); err != nil {
		return err
	}

	existingService := &corev1.Service{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      service.Name,
		Namespace: service.Namespace,
	}, existingService)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("Creating Service", "Service.Name", service.Name,
				"Service.Namespace", service.Namespace)
			return r.Client.Create(ctx, service)
		}
		return err
	}

	// Check if we need to update the service
	needsUpdate := false

	// Check service type - should always be ClusterIP now
	if existingService.Spec.Type != corev1.ServiceTypeClusterIP {
		logger.Info("Service type changed", "Old", existingService.Spec.Type, "New", corev1.ServiceTypeClusterIP)
		needsUpdate = true
	}

	// Check port configuration
	if len(existingService.Spec.Ports) != 1 ||
		existingService.Spec.Ports[0].Port != humioPdfRenderService.Spec.Port ||
		existingService.Spec.Ports[0].TargetPort.IntVal != humioPdfRenderService.Spec.Port {
		logger.Info("Port configuration changed")
		needsUpdate = true
	}

	// Check selector changes
	expectedSelector := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": humioPdfRenderService.Name,
	}
	if !reflect.DeepEqual(existingService.Spec.Selector, expectedSelector) {
		logger.Info("Service selector changed")
		needsUpdate = true
	}

	// If an update is needed, apply all the necessary changes
	if needsUpdate {
		// Update the service specification
		existingService.Spec.Type = corev1.ServiceTypeClusterIP
		existingService.Spec.Ports = []corev1.ServicePort{{
			Port:       humioPdfRenderService.Spec.Port,
			TargetPort: intstr.FromInt(int(humioPdfRenderService.Spec.Port)),
			Name:       "http",
		}}

		// Update labels
		if existingService.Labels == nil {
			existingService.Labels = map[string]string{}
		}
		existingService.Labels["app"] = "humio-pdf-render-service"
		existingService.Labels["humio-pdf-render-service-name"] = humioPdfRenderService.Name

		// Update selector
		existingService.Spec.Selector = expectedSelector

		logger.Info("Updating Service", "Service.Name", existingService.Name,
			"Service.Namespace", existingService.Namespace)

		return r.Client.Update(ctx, existingService)
	}

	logger.Info("No changes needed for Service", "Service.Name", existingService.Name)
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
