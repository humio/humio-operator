package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
func (r *HumioPdfRenderServiceReconciler) finalize(_ context.Context, _ *corev1alpha1.HumioPdfRenderService) error {
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

	// Ensure we're using the image from CR
	imageToUse := hprs.Spec.Image

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

	// Set up environment variables
	var envVars []corev1.EnvVar
	if len(hprs.Spec.Env) > 0 {
		envVars = hprs.Spec.Env
	} else {
		envVars = []corev1.EnvVar{
			{
				Name:  "LOG_LEVEL",
				Value: "debug",
			},
		}
	}

	// Set up resources with default values if not specified
	resources := hprs.Spec.Resources

	// Check if resources are empty and set defaults
	if (resources.Limits == nil || resources.Limits.Cpu().IsZero()) &&
		(resources.Requests == nil || resources.Requests.Cpu().IsZero()) {
		// Define default CPU and memory limits and requests
		cpuLimit := resource.MustParse("500m")
		memLimit := resource.MustParse("512Mi")
		cpuRequest := resource.MustParse("100m")
		memRequest := resource.MustParse("128Mi")

		resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    cpuLimit,
				corev1.ResourceMemory: memLimit,
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    cpuRequest,
				corev1.ResourceMemory: memRequest,
			},
		}

		r.Log.Info("Using default resource settings for PDF render service",
			"CPU.Limits", cpuLimit.String(),
			"Memory.Limits", memLimit.String(),
			"CPU.Requests", cpuRequest.String(),
			"Memory.Requests", memRequest.String())
	}

	// Create annotations map with rollout timestamp
	annotations := map[string]string{
		"humio-pdf-render-service/restartedAt": time.Now().Format(time.RFC3339),
	}

	// Add user-provided annotations if any
	if hprs.Spec.Annotations != nil {
		for k, v := range hprs.Spec.Annotations {
			annotations[k] = v
		}
	}

	// Default port to 5123 if not specified
	port := int32(5123)
	if hprs.Spec.Port != 0 {
		port = hprs.Spec.Port
	}

	deployment.Spec = appsv1.DeploymentSpec{
		Replicas: &hprs.Spec.Replicas,
		Selector: &metav1.LabelSelector{
			MatchLabels: labels,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      labels,
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				ServiceAccountName: hprs.Spec.ServiceAccountName,
				Affinity:           hprs.Spec.Affinity,
				Containers: []corev1.Container{{
					Name:            "pdf-render-service",
					Image:           imageToUse, // Use the image from CR directly
					ImagePullPolicy: corev1.PullIfNotPresent,
					Resources:       resources, // Use either the CR-specified or default resources
					Ports: []corev1.ContainerPort{{
						ContainerPort: port,
						Name:          "http",
					}},
					Env: envVars,
					// Set liveness probe with nil check
					LivenessProbe: func() *corev1.Probe {
						if hprs.Spec.LivenessProbe != nil {
							return hprs.Spec.LivenessProbe.DeepCopy()
						}
						// Default liveness probe
						return &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/health",
									Port: intstr.FromInt(int(port)),
								},
							},
							InitialDelaySeconds: 30,
							TimeoutSeconds:      60,
						}
					}(),
					// Set readiness probe with default values if not specified in CR
					ReadinessProbe: func() *corev1.Probe {
						if hprs.Spec.ReadinessProbe != nil {
							return hprs.Spec.ReadinessProbe.DeepCopy()
						}
						// Default readiness probe
						return &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/ready",
									Port: intstr.FromInt(int(port)),
								},
							},
							InitialDelaySeconds: 10,
							TimeoutSeconds:      30,
							PeriodSeconds:       10,
						}
					}(),
				}},
			},
		},
	}

	return deployment
}

// checkDeploymentNeedsUpdate checks if the deployment needs an update by comparing
// the existing deployment with the desired state defined in the HumioPdfRenderService CR
func (r *HumioPdfRenderServiceReconciler) checkDeploymentNeedsUpdate(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService,
	desiredResources corev1.ResourceRequirements) bool {

	needsUpdate := false

	// Check for resource changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 {
		currentResources := existingDeployment.Spec.Template.Spec.Containers[0].Resources

		r.Log.Info("Resource comparison",
			"Current.Limits.CPU", currentResources.Limits.Cpu().String(),
			"Current.Limits.Memory", currentResources.Limits.Memory().String(),
			"Current.Requests.CPU", currentResources.Requests.Cpu().String(),
			"Current.Requests.Memory", currentResources.Requests.Memory().String(),
			"Desired.Limits.CPU", desiredResources.Limits.Cpu().String(),
			"Desired.Limits.Memory", desiredResources.Limits.Memory().String(),
			"Desired.Requests.CPU", desiredResources.Requests.Cpu().String(),
			"Desired.Requests.Memory", desiredResources.Requests.Memory().String())

		if !reflect.DeepEqual(currentResources, desiredResources) {
			r.Log.Info("Resources changed")
			needsUpdate = true
		}
	}

	// Check for container image changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 {
		currentImage := existingDeployment.Spec.Template.Spec.Containers[0].Image
		desiredImage := hprs.Spec.Image
		if currentImage != desiredImage {
			r.Log.Info("Container image changed", "OldImage", currentImage, "NewImage", desiredImage)
			needsUpdate = true
		}
	}

	// Check replicas
	if existingDeployment.Spec.Replicas == nil || *existingDeployment.Spec.Replicas != hprs.Spec.Replicas {
		r.Log.Info("Replicas changed", "Old", existingDeployment.Spec.Replicas, "New", hprs.Spec.Replicas)
		needsUpdate = true
	}

	// Check for environment variable changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 &&
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Env, hprs.Spec.Env) {
		r.Log.Info("Environment variables changed")
		needsUpdate = true
	}

	// Check for probe changes
	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 {
		// Check liveness probe
		if (existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe == nil && hprs.Spec.LivenessProbe != nil) ||
			(existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe != nil && hprs.Spec.LivenessProbe == nil) ||
			!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe, hprs.Spec.LivenessProbe) {
			r.Log.Info("Liveness probe configuration changed")
			needsUpdate = true
		}

		// Check readiness probe
		if (existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe == nil && hprs.Spec.ReadinessProbe != nil) ||
			(existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe != nil && hprs.Spec.ReadinessProbe == nil) ||
			!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe, hprs.Spec.ReadinessProbe) {
			r.Log.Info("Readiness probe configuration changed")
			needsUpdate = true
		}
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

	return needsUpdate
}

// updateDeployment updates an existing deployment with the desired state
func (r *HumioPdfRenderServiceReconciler) updateDeployment(
	ctx context.Context,
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService,
	desiredResources corev1.ResourceRequirements) error {

	// 1. Set controller reference to ensure the deployment is garbage collected when CR is deleted
	if err := controllerutil.SetControllerReference(hprs, existingDeployment, r.Scheme); err != nil {
		return r.logErrorAndReturn(err, "Failed to set controller reference on existing deployment")
	}

	// 2. Update critical fields in one go
	existingDeployment.Spec.Replicas = &hprs.Spec.Replicas

	// 3. Prepare container updates - need to ensure we have at least one container
	if len(existingDeployment.Spec.Template.Spec.Containers) == 0 {
		existingDeployment.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name: "pdf-render-service",
			},
		}
	}

	// 4. Always update the image directly from the CR spec
	existingDeployment.Spec.Template.Spec.Containers[0].Image = hprs.Spec.Image
	existingDeployment.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent

	// 5. Update resources
	existingDeployment.Spec.Template.Spec.Containers[0].Resources = desiredResources

	// 6. Update port configuration
	port := int32(5123)
	if hprs.Spec.Port != 0 {
		port = hprs.Spec.Port
	}
	existingDeployment.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{
		{
			ContainerPort: port,
			Name:          "http",
		},
	}

	// 7. Update environment variables
	if len(hprs.Spec.Env) > 0 {
		existingDeployment.Spec.Template.Spec.Containers[0].Env = hprs.Spec.Env
	} else {
		existingDeployment.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
			{
				Name:  "LOG_LEVEL",
				Value: "debug",
			},
		}
	}

	// 8. Update probes
	r.updateProbes(existingDeployment, hprs, port)

	// 9. Update service account and affinity
	existingDeployment.Spec.Template.Spec.ServiceAccountName = hprs.Spec.ServiceAccountName
	existingDeployment.Spec.Template.Spec.Affinity = hprs.Spec.Affinity

	// 10. Update metadata (labels and annotations)
	r.updateDeploymentMetadata(existingDeployment, hprs)

	// 11. Log the update operation with key details
	r.Log.Info("Updating Deployment",
		"Deployment.Name", existingDeployment.Name,
		"Deployment.Namespace", existingDeployment.Namespace,
		"Image", hprs.Spec.Image,
		"Resources.Limits.CPU", desiredResources.Limits.Cpu().String(),
		"Resources.Limits.Memory", desiredResources.Limits.Memory().String())

	// 12. Perform a single update to apply all changes at once
	return r.Client.Update(ctx, existingDeployment)
}

// updateProbes sets the liveness and readiness probes for the deployment
func (r *HumioPdfRenderServiceReconciler) updateProbes(
	deployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService,
	port int32) {

	// Set liveness probe
	if hprs.Spec.LivenessProbe != nil {
		deployment.Spec.Template.Spec.Containers[0].LivenessProbe = hprs.Spec.LivenessProbe.DeepCopy()
	} else {
		// Set default liveness probe
		deployment.Spec.Template.Spec.Containers[0].LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/health",
					Port: intstr.FromInt(int(port)),
				},
			},
			InitialDelaySeconds: 30,
			TimeoutSeconds:      60,
		}
	}

	// Set readiness probe
	if hprs.Spec.ReadinessProbe != nil {
		deployment.Spec.Template.Spec.Containers[0].ReadinessProbe = hprs.Spec.ReadinessProbe.DeepCopy()
	} else {
		// Set default readiness probe - ensuring values match with constructDeployment method
		deployment.Spec.Template.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/ready",
					Port: intstr.FromInt(int(port)),
				},
			},
			InitialDelaySeconds: 10,
			TimeoutSeconds:      30,
			PeriodSeconds:       10,
		}
	}
}

// updateDeploymentMetadata updates the labels and annotations for the deployment
func (r *HumioPdfRenderServiceReconciler) updateDeploymentMetadata(
	deployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) {

	// Initialize labels if needed
	if deployment.Spec.Template.ObjectMeta.Labels == nil {
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}

	// Update standard labels
	deployment.Spec.Template.ObjectMeta.Labels["app"] = "humio-pdf-render-service"
	deployment.Spec.Template.ObjectMeta.Labels["humio-pdf-render-service-name"] = hprs.Name

	// Initialize annotations if needed
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}

	// Add/update annotations from CR specification
	if hprs.Spec.Annotations != nil {
		for k, v := range hprs.Spec.Annotations {
			deployment.Spec.Template.ObjectMeta.Annotations[k] = v
		}
	}

	// Always add a timestamp annotation to force a rollout when configuration changes
	deployment.Spec.Template.ObjectMeta.Annotations["humio-pdf-render-service/restartedAt"] = time.Now().Format(time.RFC3339)
}

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
			r.Log.Info("Creating Deployment", "Deployment.Name", deployment.Name,
				"Deployment.Namespace", deployment.Namespace,
				"Image", hprs.Spec.Image,
				"Resources", deployment.Spec.Template.Spec.Containers[0].Resources)
			return r.Client.Create(ctx, deployment)
		}
		return err
	}

	// Check if the deployment is already owned by a different controller
	if owner := metav1.GetControllerOf(existingDeployment); owner != nil && owner.UID != hprs.UID {
		r.Log.Info("Deployment is owned by a different controller. Deleting and recreating.",
			"CurrentOwner", owner.UID, "ExpectedOwner", hprs.UID)
		if err := r.Client.Delete(ctx, existingDeployment); err != nil {
			return r.logErrorAndReturn(err, "Failed to delete deployment owned by a different controller")
		}
		// Wait briefly for the deletion to complete
		time.Sleep(500 * time.Millisecond)
		return r.Client.Create(ctx, deployment)
	}

	// Get the desired resources (which may include defaults)
	desiredResources := deployment.Spec.Template.Spec.Containers[0].Resources

	// Check if we need to update the deployment
	needsUpdate := r.checkDeploymentNeedsUpdate(existingDeployment, hprs, desiredResources)

	// If an update is needed, apply all the necessary changes in a single update
	if needsUpdate {
		return r.updateDeployment(ctx, existingDeployment, hprs, desiredResources)
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

	// Default port to 5123 if not specified
	port := int32(5123)
	if hprs.Spec.Port != 0 {
		port = hprs.Spec.Port
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: hprs.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Type:     hprs.Spec.ServiceType,
			Ports: []corev1.ServicePort{
				{
					Port:       port,                      // Use the port from CR or default
					TargetPort: intstr.FromInt(int(port)), // Match target port to container port
					Protocol:   corev1.ProtocolTCP,
					Name:       "http",
				},
			},
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

	// Check service type
	if existingService.Spec.Type != hprs.Spec.ServiceType {
		r.Log.Info("Service type changed", "Old", existingService.Spec.Type, "New", hprs.Spec.ServiceType)
		needsUpdate = true
	}

	// Get the port to use (default or from CR)
	port := int32(5123)
	if hprs.Spec.Port != 0 {
		port = hprs.Spec.Port
	}

	// Check port configuration
	if len(existingService.Spec.Ports) != 1 ||
		existingService.Spec.Ports[0].Port != port ||
		existingService.Spec.Ports[0].TargetPort.IntVal != port {
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
		existingService.Spec.Type = hprs.Spec.ServiceType

		// Get the port to use (default or from CR)
		port := int32(5123)
		if hprs.Spec.Port != 0 {
			port = hprs.Spec.Port
		}

		existingService.Spec.Ports = []corev1.ServicePort{{
			Port:       port,
			TargetPort: intstr.FromInt(int(port)),
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
