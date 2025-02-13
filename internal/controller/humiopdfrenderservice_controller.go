package controllers

import (
	"context"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
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

	// If the CR is disabled, skip reconciliation.
	if humioPdfRenderService.Spec.Enabled != nil && !*humioPdfRenderService.Spec.Enabled {
		logger.Info("HumioPdfRenderService is disabled, skipping reconciliation", "Name", humioPdfRenderService.Name)
		return ctrl.Result{}, nil
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

	// Add ingress reconciliation
	if err := r.reconcileIngress(ctx, &humioPdfRenderService); err != nil {
		logger.Error(err, "Failed to reconcile Ingress")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileIngress reconciles the Ingress for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileIngress(ctx context.Context, humioPdfRenderService *corev1alpha1.HumioPdfRenderService) error {
	logger := log.FromContext(ctx)
	if humioPdfRenderService.Spec.Ingress == nil || !humioPdfRenderService.Spec.Ingress.Enabled {
		logger.Info("Ingress is disabled, skipping reconciliation")
		return nil
	}

	ingress := r.constructIngress(humioPdfRenderService)
	ingress.SetNamespace(humioPdfRenderService.Namespace)

	if err := controllerutil.SetControllerReference(humioPdfRenderService, ingress, r.Scheme); err != nil {
		return err
	}

	existingIngress := &netv1.Ingress{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      ingress.Name,
		Namespace: ingress.Namespace,
	}, existingIngress)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("Creating Ingress", "Ingress.Name", ingress.Name)
			return r.Client.Create(ctx, ingress)
		}
		return err
	}

	// Update mutable fields on the existing Ingress.
	existingIngress.Spec = ingress.Spec
	logger.Info("Updating Ingress", "Ingress.Name", existingIngress.Name)
	return r.Client.Update(ctx, existingIngress)
}

// constructIngress constructs an Ingress for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructIngress(humioPdfRenderService *corev1alpha1.HumioPdfRenderService) *netv1.Ingress {
	ingressName := humioPdfRenderService.Name + "-ingress"

	ingress := &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ingressName,
			Namespace: humioPdfRenderService.Namespace,
			Annotations: map[string]string{
				"kubernetes.io/ingress.class": "nginx", // Assuming nginx ingress controller
			},
		},
		Spec: netv1.IngressSpec{
			Rules: []netv1.IngressRule{},
		},
	}

	// Create a variable for the PathType since we need its address.
	pathType := netv1.PathTypePrefix
	for _, host := range humioPdfRenderService.Spec.Ingress.Hosts {
		ingressRule := netv1.IngressRule{
			Host: host.Host,
			IngressRuleValue: netv1.IngressRuleValue{
				HTTP: &netv1.HTTPIngressRuleValue{
					Paths: []netv1.HTTPIngressPath{
						{
							Path:     "/", // Adjust path as needed
							PathType: &pathType,
							Backend: netv1.IngressBackend{
								Service: &netv1.IngressServiceBackend{
									Name: "humio-pdf-render-service", // Fixed service name
									Port: netv1.ServiceBackendPort{
										Number: humioPdfRenderService.Spec.Port,
									},
								},
							},
						},
					},
				},
			},
		}
		ingress.Spec.Rules = append(ingress.Spec.Rules, ingressRule)
	}
	return ingress
}

// constructDeployment constructs a Deployment for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDeployment(humioPdfRenderService *corev1alpha1.HumioPdfRenderService) *appsv1.Deployment {
	// Use the CR name plus a fixed suffix.
	deploymentName := humioPdfRenderService.Name + "-pdf-render-service"

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
	deployment := r.constructDeployment(humioPdfRenderService)
	deployment.SetNamespace(humioPdfRenderService.Namespace)

	if err := controllerutil.SetControllerReference(humioPdfRenderService, deployment, r.Scheme); err != nil {
		return err
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		// Update mutable fields on the Deployment.
		deployment.Spec.Replicas = &humioPdfRenderService.Spec.Replicas

		// Always update labels.
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{
			"app":                           "humio-pdf-render-service",
			"humio-pdf-render-service-name": humioPdfRenderService.Name,
		}

		// Update Annotations: if Spec.Annotations is non-nil, copy its contents,
		// otherwise reset to an empty map.
		newAnnotations := map[string]string{}
		if humioPdfRenderService.Spec.Annotations != nil {
			for k, v := range humioPdfRenderService.Spec.Annotations {
				newAnnotations[k] = v
			}
		}
		deployment.Spec.Template.ObjectMeta.Annotations = newAnnotations

		deployment.Spec.Template.Spec.ServiceAccountName = humioPdfRenderService.Spec.ServiceAccountName
		deployment.Spec.Template.Spec.Affinity = humioPdfRenderService.Spec.Affinity

		// Update container details.
		for i := range deployment.Spec.Template.Spec.Containers {
			if deployment.Spec.Template.Spec.Containers[i].Name == "humio-pdf-render-service" {
				deployment.Spec.Template.Spec.Containers[i].Image = humioPdfRenderService.Spec.Image
				deployment.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullIfNotPresent
				deployment.Spec.Template.Spec.Containers[i].Resources = humioPdfRenderService.Spec.Resources
				deployment.Spec.Template.Spec.Containers[i].Ports = []corev1.ContainerPort{{
					ContainerPort: humioPdfRenderService.Spec.Port,
					Name:          "http",
				}}
				deployment.Spec.Template.Spec.Containers[i].Env = humioPdfRenderService.Spec.Env
				deployment.Spec.Template.Spec.Containers[i].LivenessProbe = humioPdfRenderService.Spec.LivenessProbe
				deployment.Spec.Template.Spec.Containers[i].ReadinessProbe = humioPdfRenderService.Spec.ReadinessProbe
				break
			}
		}
		return nil
	})
	if err != nil {
		r.Log.Error(err, "Failed to reconcile Deployment", "Deployment.Name", deployment.Name, "Deployment.Namespace", deployment.Namespace)
	}
	return err
}

// constructService constructs a Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructService(humioPdfRenderService *corev1alpha1.HumioPdfRenderService) *corev1.Service {
	// Use a fixed short service name.
	serviceName := "humio-pdf-render-service"

	labels := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": humioPdfRenderService.Name,
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: humioPdfRenderService.Namespace,
		},
	}
	service.Spec = corev1.ServiceSpec{
		Selector: labels,
		Type:     humioPdfRenderService.Spec.ServiceType,
		Ports: []corev1.ServicePort{{
			Port:       humioPdfRenderService.Spec.Port,
			TargetPort: intstr.FromInt(int(humioPdfRenderService.Spec.Port)),
			Name:       "http",
		}},
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
			logger.Info("Creating Service", "Service.Name", service.Name, "Service.Namespace", service.Namespace)
			return r.Client.Create(ctx, service)
		}
		return err
	}

	// If the Service type needs to be changed, delete and recreate the Service.
	if existingService.Spec.Type != humioPdfRenderService.Spec.ServiceType {
		logger.Info("Service type change detected; deleting Service", "Service.Name", existingService.Name, "OldType", existingService.Spec.Type, "NewType", humioPdfRenderService.Spec.ServiceType)
		if err := r.Client.Delete(ctx, existingService); err != nil {
			return err
		}
		return r.Client.Create(ctx, service)
	}

	// Update labels and specification.
	existingService.Labels = map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": humioPdfRenderService.Name,
	}
	existingService.Spec.Type = humioPdfRenderService.Spec.ServiceType
	existingService.Spec.Ports = []corev1.ServicePort{{
		Port:       humioPdfRenderService.Spec.Port,
		TargetPort: intstr.FromInt(int(humioPdfRenderService.Spec.Port)),
		Name:       "http",
	}}

	// Update NodePort if service type is NodePort.
	if humioPdfRenderService.Spec.ServiceType == corev1.ServiceTypeNodePort {
		for i := range existingService.Spec.Ports {
			if existingService.Spec.Ports[i].Name == "http" {
				existingService.Spec.Ports[i].NodePort = humioPdfRenderService.Spec.NodePort
			}
		}
	}

	logger.Info("Updating Service", "Service.Name", existingService.Name, "Service.Namespace", existingService.Namespace)
	return r.Client.Update(ctx, existingService)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1alpha1.HumioPdfRenderService{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&netv1.Ingress{}).
		Complete(r)
}
