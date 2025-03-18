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
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reqLogger := r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name)
	r.Log = reqLogger
	reqLogger.Info("Reconciling HumioPdfRenderService")

	// Fetch the HumioPdfRenderService instance
	hprs := &humiov1alpha1.HumioPdfRenderService{}
	err := r.Get(ctx, req.NamespacedName, hprs)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Initialize the Status if it doesn't exist yet
	if hprs.Status.State == "" {
		hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		if err := r.Status().Update(ctx, hprs); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile the Deployment
	if err := r.reconcileDeployment(ctx, hprs); err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile the Service
	if err := r.reconcileService(ctx, hprs); err != nil {
		return ctrl.Result{}, err
	}

	// Update status to Ready if we've made it this far
	if hprs.Status.State != humiov1alpha1.HumioPdfRenderServiceStateReady {
		hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateReady
		if err := r.Status().Update(ctx, hprs); err != nil {
			return ctrl.Result{}, err
		}
	}

	reqLogger.Info("Reconciled HumioPdfRenderService successfully")
	return ctrl.Result{}, nil
}

// constructDeployment creates a Deployment object for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDeployment(hprs *humiov1alpha1.HumioPdfRenderService) (*appsv1.Deployment, error) {
	deploymentName := "pdf-render-service"

	labels := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": hprs.Name,
	}

	// Set default port if not specified
	port := hprs.Spec.Port
	if port == 0 {
		port = 8080
	}

	// Construct container for the deployment
	container := corev1.Container{
		Name:  "pdf-render-service",
		Image: getImageString(hprs),
		Ports: []corev1.ContainerPort{
			{
				ContainerPort: port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
	}

	// Set ImagePullPolicy if specified, otherwise default to IfNotPresent
	imagePullPolicy := hprs.Spec.ImagePullPolicy
	if imagePullPolicy == "" {
		imagePullPolicy = corev1.PullIfNotPresent
	}
	container.ImagePullPolicy = imagePullPolicy

	// Set resources if specified
	if !reflect.DeepEqual(hprs.Spec.Resources, corev1.ResourceRequirements{}) {
		container.Resources = hprs.Spec.Resources
	}

	// Set environment variables if specified
	if len(hprs.Spec.Env) > 0 {
		container.Env = hprs.Spec.Env
	}

	// Set liveness probe if specified
	if hprs.Spec.LivenessProbe != nil {
		container.LivenessProbe = hprs.Spec.LivenessProbe
	}

	// Set readiness probe if specified
	if hprs.Spec.ReadinessProbe != nil {
		container.ReadinessProbe = hprs.Spec.ReadinessProbe
	}

	// Add volume mounts if specified
	if len(hprs.Spec.VolumeMounts) > 0 {
		container.VolumeMounts = hprs.Spec.VolumeMounts
	}

	// Add container security context if specified
	if hprs.Spec.ContainerSecurityContext != nil {
		container.SecurityContext = hprs.Spec.ContainerSecurityContext
	}

	// Configure pod template spec
	podTemplateSpec := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Containers:         []corev1.Container{container},
			ServiceAccountName: hprs.Spec.ServiceAccountName,
		},
	}

	// Add pod annotations if specified
	if hprs.Spec.Annotations != nil {
		podTemplateSpec.ObjectMeta.Annotations = hprs.Spec.Annotations
	}

	// Add affinity if specified
	if hprs.Spec.Affinity != nil {
		podTemplateSpec.Spec.Affinity = hprs.Spec.Affinity
	}

	// Add pod security context if specified
	if hprs.Spec.SecurityContext != nil {
		podTemplateSpec.Spec.SecurityContext = hprs.Spec.SecurityContext
	}

	// Add image pull secrets if specified
	if len(hprs.Spec.ImagePullSecrets) > 0 {
		podTemplateSpec.Spec.ImagePullSecrets = hprs.Spec.ImagePullSecrets
	}

	// Add tolerations if specified
	if len(hprs.Spec.Tolerations) > 0 {
		podTemplateSpec.Spec.Tolerations = hprs.Spec.Tolerations
	}

	// Add volumes if specified
	if len(hprs.Spec.Volumes) > 0 {
		podTemplateSpec.Spec.Volumes = hprs.Spec.Volumes
	}

	// Create the deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: hprs.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &hprs.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: podTemplateSpec,
		},
	}

	return deployment, nil
}

// reconcileDeployment reconciles the Deployment for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	deployment, err := r.constructDeployment(hprs)
	if err != nil {
		return err
	}

	if err := controllerutil.SetControllerReference(hprs, deployment, r.Scheme); err != nil {
		return err
	}

	// Check if this Deployment already exists
	existingDeployment := &appsv1.Deployment{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, existingDeployment)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Creating Deployment", "Deployment.Name", deployment.Name, "Deployment.Namespace", deployment.Namespace)
			return r.Client.Create(ctx, deployment)
		}
		return err
	}

	// Deployment exists, check if we need to update it
	needsUpdate := false

	// Check for image changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 && existingDeployment.Spec.Template.Spec.Containers[0].Image != getImageString(hprs) {
		r.Log.Info("Image changed", "Old", existingDeployment.Spec.Template.Spec.Containers[0].Image, "New", getImageString(hprs))
		needsUpdate = true
	}

	// Check for replicas changes
	if existingDeployment.Spec.Replicas != nil && *existingDeployment.Spec.Replicas != hprs.Spec.Replicas {
		r.Log.Info("Replicas changed", "Old", existingDeployment.Spec.Replicas, "New", hprs.Spec.Replicas)
		needsUpdate = true
	}

	// Check for resource changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 && !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Resources, hprs.Spec.Resources) {
		r.Log.Info("Resources changed")
		needsUpdate = true
	}

	// Check for environment variable changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 && !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Env, hprs.Spec.Env) {
		r.Log.Info("Environment variables changed")
		needsUpdate = true
	}

	// Check for probe changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 && (!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe, hprs.Spec.LivenessProbe) || !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe, hprs.Spec.ReadinessProbe)) {
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

	// Check for image pull policy changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 {
		imagePullPolicy := hprs.Spec.ImagePullPolicy
		if imagePullPolicy == "" {
			imagePullPolicy = corev1.PullIfNotPresent
		}
		if existingDeployment.Spec.Template.Spec.Containers[0].ImagePullPolicy != imagePullPolicy {
			r.Log.Info("ImagePullPolicy changed", "Old", existingDeployment.Spec.Template.Spec.Containers[0].ImagePullPolicy, "New", imagePullPolicy)
			needsUpdate = true
		}
	}

	// Check for security context changes
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.SecurityContext, hprs.Spec.SecurityContext) {
		r.Log.Info("SecurityContext changed")
		needsUpdate = true
	}

	// Check for container security context changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 && !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].SecurityContext, hprs.Spec.ContainerSecurityContext) {
		r.Log.Info("ContainerSecurityContext changed")
		needsUpdate = true
	}

	// Check for image pull secrets changes
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.ImagePullSecrets, hprs.Spec.ImagePullSecrets) {
		r.Log.Info("ImagePullSecrets changed")
		needsUpdate = true
	}

	// Check for tolerations changes
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Tolerations, hprs.Spec.Tolerations) {
		r.Log.Info("Tolerations changed")
		needsUpdate = true
	}

	// Check for volumes changes
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Volumes, hprs.Spec.Volumes) {
		r.Log.Info("Volumes changed")
		needsUpdate = true
	}

	// Check for volume mounts changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 && !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].VolumeMounts, hprs.Spec.VolumeMounts) {
		r.Log.Info("VolumeMounts changed")
		needsUpdate = true
	}

	if needsUpdate {
		r.Log.Info("Updating Deployment", "Deployment.Name", deployment.Name, "Deployment.Namespace", deployment.Namespace)
		existingDeployment.Spec = deployment.Spec
		return r.Client.Update(ctx, existingDeployment)
	}

	return nil
}

// reconcileService reconciles the Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileService(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	serviceName := "pdf-render-service"

	labels := map[string]string{
		"app":                           "humio-pdf-render-service",
		"humio-pdf-render-service-name": hprs.Name,
	}

	// Set default port if not specified
	port := hprs.Spec.Port
	if port == 0 {
		port = 8080
	}

	// Create service object
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: hprs.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       port,
					TargetPort: intstr.FromInt(int(port)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	// Set service type if specified
	if hprs.Spec.ServiceType != "" {
		service.Spec.Type = hprs.Spec.ServiceType
	}

	if err := controllerutil.SetControllerReference(hprs, service, r.Scheme); err != nil {
		return err
	}

	// Check if this Service already exists
	existingService := &corev1.Service{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, existingService)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Creating Service", "Service.Name", service.Name, "Service.Namespace", service.Namespace)
			return r.Client.Create(ctx, service)
		}
		return err
	}

	// Service exists, check if we need to update it
	needsUpdate := false

	// Check for port changes
	if len(existingService.Spec.Ports) > 0 && existingService.Spec.Ports[0].Port != port {
		r.Log.Info("Port changed", "Old", existingService.Spec.Ports[0].Port, "New", port)
		needsUpdate = true
	}

	// Check for service type changes
	if hprs.Spec.ServiceType != "" && existingService.Spec.Type != hprs.Spec.ServiceType {
		r.Log.Info("Service type changed", "Old", existingService.Spec.Type, "New", hprs.Spec.ServiceType)
		needsUpdate = true
	}

	if needsUpdate {
		r.Log.Info("Updating Service", "Service.Name", service.Name, "Service.Namespace", service.Namespace)
		// We need to preserve the ClusterIP when updating
		service.Spec.ClusterIP = existingService.Spec.ClusterIP

		// Update the service
		return r.Client.Update(ctx, service)
	}

	return nil
}

// getImageString returns the container image to use
func getImageString(hprs *humiov1alpha1.HumioPdfRenderService) string {
	imageRegistry := "docker.io/"
	if hprs.Spec.ImageRegistry != "" {
		imageRegistry = hprs.Spec.ImageRegistry
	}

	imageRepository := "humio/pdf-render-service"
	if hprs.Spec.ImageRepository != "" {
		imageRepository = hprs.Spec.ImageRepository
	}

	imageTag := "latest"
	if hprs.Spec.ImageTag != "" {
		imageTag = hprs.Spec.ImageTag
	}

	return fmt.Sprintf("%s%s:%s", imageRegistry, imageRepository, imageTag)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.BaseLogger = log.Log.WithName("controllers").WithName("HumioPdfRenderService")

	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPdfRenderService{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
