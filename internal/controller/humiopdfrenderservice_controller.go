package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
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

const (
	humioPdfRenderServiceFinalizer = "core.humio.com/finalizer"
	deploymentName                 = "pdf-render-service"
	credentialsSecretName          = "regcred"
)

// Reconcile implements the reconciliation logic for HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name,
		"Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
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
			Name:      deploymentName, // Use constant
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
					"app": deploymentName, // Use constant
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
	// Use a longer requeue interval to prevent too frequent updates
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// finalize handles the cleanup when the resource is deleted
// nolint:unparam
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
	deploymentName := deploymentName

	// Ensure we're using the image from CR
	imageToUse := hprs.Spec.Image

	// Start with default labels
	labels := map[string]string{
		"app": deploymentName, // Use constant
	}
	// Add any custom labels from the spec
	if hprs.Spec.Labels != nil {
		for k, v := range hprs.Spec.Labels {
			labels[k] = v
		}
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: hprs.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &hprs.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: hprs.Spec.Annotations, // Directly use annotations from the spec
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: hprs.Spec.ServiceAccountName,
					Affinity:           hprs.Spec.Affinity,
					SecurityContext:    hprs.Spec.PodSecurityContext,
					Volumes: func() []corev1.Volume {
						if hprs.Spec.Volumes != nil {
							return hprs.Spec.Volumes
						}
						// Default volumes if not specified
						return []corev1.Volume{
							{
								Name: "app-temp",
								VolumeSource: corev1.VolumeSource{
									EmptyDir: &corev1.EmptyDirVolumeSource{
										Medium: corev1.StorageMediumMemory,
									},
								},
							},
							{
								Name: "tmp",
								VolumeSource: corev1.VolumeSource{
									EmptyDir: &corev1.EmptyDirVolumeSource{
										Medium: corev1.StorageMediumMemory,
									},
								},
							},
						}
					}(),
					Containers: []corev1.Container{{
						Name:            deploymentName, // Use constant
						Image:           imageToUse,     // Use the image from CR directly
						ImagePullPolicy: corev1.PullIfNotPresent,
						SecurityContext: func() *corev1.SecurityContext {
							if hprs.Spec.SecurityContext != nil {
								return hprs.Spec.SecurityContext
							}
							// Default security context if not specified
							return &corev1.SecurityContext{
								AllowPrivilegeEscalation: func() *bool { b := false; return &b }(),
								Privileged:               func() *bool { b := false; return &b }(),
								ReadOnlyRootFilesystem:   func() *bool { b := true; return &b }(),
								RunAsNonRoot:             func() *bool { b := true; return &b }(),
								RunAsUser:                func() *int64 { i := int64(1000); return &i }(),
								RunAsGroup:               func() *int64 { i := int64(1000); return &i }(),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							}
						}(),
						Resources: hprs.Spec.Resources,
						Ports: []corev1.ContainerPort{{
							ContainerPort: hprs.Spec.Port,
							Name:          "http",
						}},
						Env: hprs.Spec.Env,
						VolumeMounts: func() []corev1.VolumeMount {
							if hprs.Spec.VolumeMounts != nil {
								return hprs.Spec.VolumeMounts
							}
							// Default volume mounts if not specified
							return []corev1.VolumeMount{
								{
									Name:      "app-temp",
									MountPath: "/app/temp",
								},
								{
									Name:      "tmp",
									MountPath: "/tmp",
								},
							}
						}(),
						// Set liveness probe with nil check
						LivenessProbe: func() *corev1.Probe {
							if hprs.Spec.LivenessProbe != nil {
								probe := hprs.Spec.LivenessProbe.DeepCopy()
								// Ensure Scheme is set if not specified
								if probe.HTTPGet != nil && probe.HTTPGet.Scheme == "" {
									probe.HTTPGet.Scheme = corev1.URISchemeHTTP
								}
								return probe
							}
							// Default liveness probe with all required fields
							return &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/health",
										Port:   intstr.FromInt(int(hprs.Spec.Port)),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 30,
								TimeoutSeconds:      60,
								PeriodSeconds:       10,
								SuccessThreshold:    1,
								FailureThreshold:    3,
							}
						}(),
						// Set readiness probe with default values if not specified in CR
						ReadinessProbe: func() *corev1.Probe {
							if hprs.Spec.ReadinessProbe != nil {
								probe := hprs.Spec.ReadinessProbe.DeepCopy()
								// Ensure Scheme is set if not specified
								if probe.HTTPGet != nil && probe.HTTPGet.Scheme == "" {
									probe.HTTPGet.Scheme = corev1.URISchemeHTTP
								}
								return probe
							}
							// Default readiness probe with all required fields
							return &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path:   "/ready",
										Port:   intstr.FromInt(int(hprs.Spec.Port)),
										Scheme: corev1.URISchemeHTTP,
									},
								},
								InitialDelaySeconds: 30,
								TimeoutSeconds:      60,
								PeriodSeconds:       10,
								SuccessThreshold:    1,
								FailureThreshold:    1,
							}
						}(),
					}},
				},
			},
		},
	}

	// Add ImagePullSecrets with special handling for default credentials
	if hprs.Spec.ImagePullSecrets != nil {
		deployment.Spec.Template.Spec.ImagePullSecrets = hprs.Spec.ImagePullSecrets
	} else {
		// If no ImagePullSecrets specified, use default credentials
		// This ensures backward compatibility with existing deployments
		deployment.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{
			{
				Name: credentialsSecretName, // Use constant
			},
		}
	}

	return deployment
}

// checkResourceChanges compares the current and desired resource requirements
func (r *HumioPdfRenderServiceReconciler) checkResourceChanges(
	existingDeployment *appsv1.Deployment,
	desiredResources corev1.ResourceRequirements) bool {

	if len(existingDeployment.Spec.Template.Spec.Containers) == 0 {
		return false
	}

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
		return true
	}
	return false
}

// checkImageChanges compares the current and desired container images
func (r *HumioPdfRenderServiceReconciler) checkReplicasChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if existingDeployment.Spec.Replicas == nil || *existingDeployment.Spec.Replicas != hprs.Spec.Replicas {
		r.Log.Info("Replicas changed", "Old", existingDeployment.Spec.Replicas, "New", hprs.Spec.Replicas)
		return true
	}
	return false
}

// checkImageChanges compares the current and desired container images
func (r *HumioPdfRenderServiceReconciler) checkImageChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if len(existingDeployment.Spec.Template.Spec.Containers) == 0 {
		return false
	}

	currentImage := existingDeployment.Spec.Template.Spec.Containers[0].Image
	desiredImage := hprs.Spec.Image
	if currentImage != desiredImage {
		r.Log.Info("Container image changed", "OldImage", currentImage, "NewImage", desiredImage)
		return true
	}
	return false
}

// checkDeploymentNeedsUpdate checks if the deployment needs an update by comparing
// the existing deployment with the desired state defined in the HumioPdfRenderService CR
func (r *HumioPdfRenderServiceReconciler) checkDeploymentNeedsUpdate(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService,
	desiredResources corev1.ResourceRequirements) bool {

	needsUpdate := false

	needsUpdate = r.checkResourceChanges(existingDeployment, desiredResources) || needsUpdate
	needsUpdate = r.checkImageChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkReplicasChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkEnvVarChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkProbeChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkServiceAccountChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkAffinityChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkImagePullSecretChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkVolumeMountChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkVolumeChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkSecurityContextChanges(existingDeployment, hprs) || needsUpdate
	needsUpdate = r.checkAnnotationChanges(existingDeployment, hprs) || needsUpdate // Use checkAnnotationChanges

	return needsUpdate
}

// updateDeployment updates an existing deployment with the desired state
func (r *HumioPdfRenderServiceReconciler) updateDeployment(
	ctx context.Context,
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService,
	desiredResources corev1.ResourceRequirements) error {

	var lastConflictErr error
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the latest version of the deployment right before updating
		if err := r.Get(ctx, types.NamespacedName{
			Name:      existingDeployment.Name,
			Namespace: existingDeployment.Namespace,
		}, existingDeployment); err != nil {
			return err
		}

		// Set controller reference if not already set
		if !metav1.IsControlledBy(existingDeployment, hprs) {
			if err := controllerutil.SetControllerReference(hprs, existingDeployment, r.Scheme); err != nil {
				return r.logErrorAndReturn(err, "Failed to set controller reference on existing deployment")
			}
		}

		// Use helper functions to determine if updates are needed and apply them
		specUpdated := r.updateDeploymentSpec(existingDeployment, hprs, desiredResources)
		podSpecUpdated := r.updateDeploymentPodSpec(&existingDeployment.Spec.Template.Spec, hprs)
		// updateDeploymentMetadata modifies directly, check changes separately
		metadataChanged := r.checkMetadataChanges(existingDeployment, hprs)
		if metadataChanged {
			r.updateDeploymentMetadata(existingDeployment, hprs)
		}

		needsUpdate := specUpdated || podSpecUpdated || metadataChanged

		if needsUpdate {
			// Log the update operation with key details
			cpuLimit := "not set"
			memLimit := "not set"
			if desiredResources.Limits != nil {
				if cpu := desiredResources.Limits.Cpu(); cpu != nil {
					cpuLimit = cpu.String()
				}
				if mem := desiredResources.Limits.Memory(); mem != nil {
					memLimit = mem.String()
				}
			}

			r.Log.Info("Updating Deployment",
				"Deployment.Name", existingDeployment.Name,
				"Deployment.Namespace", existingDeployment.Namespace,
				"Image", hprs.Spec.Image,
				"Resources.Limits.CPU", cpuLimit,
				"Resources.Limits.Memory", memLimit)

			updateErr := r.Client.Update(ctx, existingDeployment)
			if updateErr != nil && k8serrors.IsConflict(updateErr) {
				lastConflictErr = updateErr
				r.Log.Info("Conflict detected during deployment update, retrying...",
					"Deployment.Name", existingDeployment.Name,
					"Deployment.Namespace", existingDeployment.Namespace)
			}
			return updateErr
		}

		// No changes needed
		r.Log.Info("No update needed for Deployment", "Deployment.Name", existingDeployment.Name)
		return nil
	})

	if retryErr != nil {
		r.Log.Error(retryErr, "Failed to update Deployment after retries",
			"Deployment.Name", existingDeployment.Name,
			"Deployment.Namespace", existingDeployment.Namespace)
		if lastConflictErr != nil {
			r.Log.Error(lastConflictErr, "Last conflict error details")
		}
	}
	return retryErr
}

// isPodSecurityContextEmpty checks if a PodSecurityContext is effectively empty
func isPodSecurityContextEmpty(sc *corev1.PodSecurityContext) bool {
	if sc == nil {
		return true
	}

	// Check if all fields have their zero values
	return sc.SELinuxOptions == nil && sc.RunAsUser == nil && sc.RunAsNonRoot == nil &&
		(len(sc.SupplementalGroups) == 0) && sc.FSGroup == nil && sc.RunAsGroup == nil &&
		len(sc.Sysctls) == 0 && // Fixed gosimple error
		sc.WindowsOptions == nil && sc.FSGroupChangePolicy == nil && sc.SeccompProfile == nil
}

// checkEnvVarChanges checks if environment variables have changed
func (r *HumioPdfRenderServiceReconciler) checkEnvVarChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if len(existingDeployment.Spec.Template.Spec.Containers) > 0 &&
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Env, hprs.Spec.Env) {
		r.Log.Info("Environment variables changed")
		return true
	}
	return false
}

// checkProbeChanges checks if liveness/readiness probes have changed
func (r *HumioPdfRenderServiceReconciler) checkProbeChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if len(existingDeployment.Spec.Template.Spec.Containers) == 0 {
		return false
	}

	port := int32(5123)
	if hprs.Spec.Port != 0 {
		port = hprs.Spec.Port
	}

	expectedLivenessProbe := r.getExpectedLivenessProbe(hprs, port)
	expectedReadinessProbe := r.getExpectedReadinessProbe(hprs, port)

	livenessChanged := existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe == nil ||
		!reflect.DeepEqual(*existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe, *expectedLivenessProbe)

	readinessChanged := existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe == nil ||
		!reflect.DeepEqual(*existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe, *expectedReadinessProbe)

	if livenessChanged {
		r.Log.Info("Liveness probe configuration changed",
			"Current", existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe,
			"Desired", expectedLivenessProbe)
	}

	if readinessChanged {
		r.Log.Info("Readiness probe configuration changed",
			"Current", existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe,
			"Desired", expectedReadinessProbe)
	}

	return livenessChanged || readinessChanged
}

// getExpectedLivenessProbe returns the expected liveness probe configuration
func (r *HumioPdfRenderServiceReconciler) getExpectedLivenessProbe(
	hprs *corev1alpha1.HumioPdfRenderService,
	port int32) *corev1.Probe {

	if hprs.Spec.LivenessProbe != nil {
		probe := hprs.Spec.LivenessProbe.DeepCopy()
		if probe.HTTPGet != nil && probe.HTTPGet.Scheme == "" {
			probe.HTTPGet.Scheme = corev1.URISchemeHTTP
		}
		return probe
	}

	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   "/health",
				Port:   intstr.FromInt(int(port)),
				Scheme: corev1.URISchemeHTTP,
			},
		},
		InitialDelaySeconds: 30,
		TimeoutSeconds:      60,
		PeriodSeconds:       10,
		SuccessThreshold:    1,
		FailureThreshold:    3,
	}
}

// getExpectedReadinessProbe returns the expected readiness probe configuration
func (r *HumioPdfRenderServiceReconciler) getExpectedReadinessProbe(
	hprs *corev1alpha1.HumioPdfRenderService,
	port int32) *corev1.Probe {

	if hprs.Spec.ReadinessProbe != nil {
		probe := hprs.Spec.ReadinessProbe.DeepCopy()
		if probe.HTTPGet != nil && probe.HTTPGet.Scheme == "" {
			probe.HTTPGet.Scheme = corev1.URISchemeHTTP
		}
		return probe
	}

	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   "/ready",
				Port:   intstr.FromInt(int(port)),
				Scheme: corev1.URISchemeHTTP,
			},
		},
		InitialDelaySeconds: 30,
		TimeoutSeconds:      60,
		PeriodSeconds:       10,
		SuccessThreshold:    1,
		FailureThreshold:    1,
	}
}

// checkServiceAccountChanges checks if service account has changed
func (r *HumioPdfRenderServiceReconciler) checkServiceAccountChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if existingDeployment.Spec.Template.Spec.ServiceAccountName != hprs.Spec.ServiceAccountName {
		r.Log.Info("ServiceAccount changed",
			"Old", existingDeployment.Spec.Template.Spec.ServiceAccountName,
			"New", hprs.Spec.ServiceAccountName)
		return true
	}
	return false
}

// checkAffinityChanges checks if affinity configuration has changed
func (r *HumioPdfRenderServiceReconciler) checkAffinityChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Affinity, hprs.Spec.Affinity) {
		r.Log.Info("Affinity configuration changed")
		return true
	}
	return false
}

// checkImagePullSecretChanges checks if image pull secrets have changed
func (r *HumioPdfRenderServiceReconciler) checkImagePullSecretChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	currentSecrets := existingDeployment.Spec.Template.Spec.ImagePullSecrets
	desiredSecrets := hprs.Spec.ImagePullSecrets

	hasEcrCredentials := false
	if len(currentSecrets) == 1 {
		for _, secret := range currentSecrets {
			if secret.Name == credentialsSecretName { // Use constant
				hasEcrCredentials = true
				break
			}
		}
	}

	if hasEcrCredentials && desiredSecrets == nil {
		r.Log.Info("Preserving existing default credentials despite nil in spec") // Updated comment
		return false
	}

	if (currentSecrets == nil && desiredSecrets == nil) || (len(currentSecrets) == 0 && len(desiredSecrets) == 0) {
		return false
	}

	if (currentSecrets == nil && len(desiredSecrets) > 0) ||
		(len(currentSecrets) > 0 && desiredSecrets == nil && !hasEcrCredentials) ||
		!reflect.DeepEqual(currentSecrets, desiredSecrets) {
		r.Log.Info("ImagePullSecrets changed", "Current", currentSecrets, "Desired", desiredSecrets)
		return true
	}

	return false
}

// checkVolumeMountChanges checks if volume mounts have changed
func (r *HumioPdfRenderServiceReconciler) checkVolumeMountChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if len(existingDeployment.Spec.Template.Spec.Containers) == 0 {
		return false
	}

	expectedVolumeMounts := r.getExpectedVolumeMounts(hprs)
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].VolumeMounts, expectedVolumeMounts) {
		r.Log.Info("Volume mounts configuration changed")
		return true
	}
	return false
}

// getExpectedVolumeMounts returns the expected volume mounts configuration
func (r *HumioPdfRenderServiceReconciler) getExpectedVolumeMounts(
	hprs *corev1alpha1.HumioPdfRenderService) []corev1.VolumeMount {

	if hprs.Spec.VolumeMounts != nil {
		return hprs.Spec.VolumeMounts
	}

	return []corev1.VolumeMount{
		{
			Name:      "app-temp", // Corrected typo from app-ttemp
			MountPath: "/app/temp",
		},
		{
			Name:      "tmp",
			MountPath: "/tmp",
		},
	}
}

// checkVolumeChanges checks if volumes have changed
func (r *HumioPdfRenderServiceReconciler) checkVolumeChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	expectedVolumes := r.getExpectedVolumes(hprs)
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Volumes, expectedVolumes) {
		r.Log.Info("Volumes configuration changed")
		return true
	}
	return false
}

// getExpectedVolumes returns the expected volumes configuration
func (r *HumioPdfRenderServiceReconciler) getExpectedVolumes(
	hprs *corev1alpha1.HumioPdfRenderService) []corev1.Volume {

	if hprs.Spec.Volumes != nil {
		return hprs.Spec.Volumes
	}

	return []corev1.Volume{
		{
			Name: "app-temp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
		{
			Name: "tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
	}
}

// checkSecurityContextChanges checks if security contexts have changed
func (r *HumioPdfRenderServiceReconciler) checkSecurityContextChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if len(existingDeployment.Spec.Template.Spec.Containers) == 0 {
		return false
	}

	containerChanged := r.checkContainerSecurityContextChanges(existingDeployment, hprs)
	podChanged := r.checkPodSecurityContextChanges(existingDeployment, hprs)

	return containerChanged || podChanged
}

// checkContainerSecurityContextChanges checks if container security context has changed
func (r *HumioPdfRenderServiceReconciler) checkContainerSecurityContextChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	expected := r.getExpectedContainerSecurityContext(hprs)
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].SecurityContext, expected) {
		r.Log.Info("Container security context changed",
			"Current", existingDeployment.Spec.Template.Spec.Containers[0].SecurityContext,
			"Desired", expected)
		return true
	}
	return false
}

// getExpectedContainerSecurityContext returns the expected container security context
func (r *HumioPdfRenderServiceReconciler) getExpectedContainerSecurityContext(
	hprs *corev1alpha1.HumioPdfRenderService) *corev1.SecurityContext {

	if hprs.Spec.SecurityContext != nil {
		return hprs.Spec.SecurityContext
	}

	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: func() *bool { b := false; return &b }(),
		Privileged:               func() *bool { b := false; return &b }(),
		ReadOnlyRootFilesystem:   func() *bool { b := true; return &b }(),
		RunAsNonRoot:             func() *bool { b := true; return &b }(),
		RunAsUser:                func() *int64 { i := int64(1000); return &i }(),
		RunAsGroup:               func() *int64 { i := int64(1000); return &i }(),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

// checkPodSecurityContextChanges checks if pod security context has changed
func (r *HumioPdfRenderServiceReconciler) checkPodSecurityContextChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	expected := hprs.Spec.PodSecurityContext
	if expected == nil {
		expected = &corev1.PodSecurityContext{}
	}

	if !reflect.DeepEqual(existingDeployment.Spec.Template.Spec.SecurityContext, expected) {
		if !(existingDeployment.Spec.Template.Spec.SecurityContext == nil && isPodSecurityContextEmpty(expected)) &&
			!(expected == nil && isPodSecurityContextEmpty(existingDeployment.Spec.Template.Spec.SecurityContext)) {
			r.Log.Info("Pod security context changed",
				"Current", existingDeployment.Spec.Template.Spec.SecurityContext,
				"Desired", expected)
			return true
		}
	}
	return false
}

// checkAnnotationChanges checks if annotations have changed
func (r *HumioPdfRenderServiceReconciler) checkAnnotationChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	existingAnnotations := make(map[string]string)
	if existingDeployment.Spec.Template.ObjectMeta.Annotations != nil {
		for k, v := range existingDeployment.Spec.Template.ObjectMeta.Annotations {
			if k != "humio-pdf-render-service/restartedAt" {
				existingAnnotations[k] = v
			}
		}
	}

	specAnnotations := make(map[string]string)
	if hprs.Spec.Annotations != nil {
		for k, v := range hprs.Spec.Annotations {
			specAnnotations[k] = v
		}
	}

	if !reflect.DeepEqual(existingAnnotations, specAnnotations) {
		r.Log.Info("Annotations changed")
		return true
	}
	return false
}

// checkMetadataChanges checks if metadata (labels, annotations) has changed
func (r *HumioPdfRenderServiceReconciler) checkMetadataChanges(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	// Check labels (only the 'app' label for now)
	expectedLabels := map[string]string{"app": deploymentName}
	if !reflect.DeepEqual(existingDeployment.Spec.Template.ObjectMeta.Labels, expectedLabels) {
		// Allow additional labels from spec, but ensure 'app' is correct
		currentAppLabel, ok := existingDeployment.Spec.Template.ObjectMeta.Labels["app"]
		if !ok || currentAppLabel != deploymentName {
			r.Log.Info("Deployment template labels changed (app label)")
			return true
		}
		// TODO: Consider a more robust check for spec labels if needed,
		// potentially comparing only labels defined in hprs.Spec.Labels
		// against existingDeployment.Spec.Template.ObjectMeta.Labels.
	}

	// Check annotations (ignoring restartedAt)
	existingAnnotations := make(map[string]string)
	if existingDeployment.Spec.Template.ObjectMeta.Annotations != nil {
		for k, v := range existingDeployment.Spec.Template.ObjectMeta.Annotations {
			if k != "humio-pdf-render-service/restartedAt" {
				existingAnnotations[k] = v
			}
		}
	}
	specAnnotations := hprs.Spec.Annotations
	// Treat nil spec annotations as empty map
	if specAnnotations == nil {
		specAnnotations = make(map[string]string)
	}

	if !reflect.DeepEqual(existingAnnotations, specAnnotations) {
		r.Log.Info("Deployment template annotations changed")
		return true
	}

	return false
}

// updateDeploymentMetadata updates the labels and annotations for the deployment
func (r *HumioPdfRenderServiceReconciler) updateDeploymentMetadata(
	deployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService) {
	// Initialize labels if needed
	if deployment.Spec.Template.ObjectMeta.Labels == nil {
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}

	// Update standard labels - NOTE: Using "pdf-render-service" to match what the tests expect
	deployment.Spec.Template.ObjectMeta.Labels["app"] = deploymentName // Use constant

	// Initialize annotations if needed
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}

	// Preserve existing annotations that aren't managed by this controller
	// Only update our custom annotations and restart timestamp
	if hprs.Spec.Annotations != nil {
		for k, v := range hprs.Spec.Annotations {
			deployment.Spec.Template.ObjectMeta.Annotations[k] = v
		}
	}
}

// updateDeploymentSpec updates the core fields of the deployment spec
func (r *HumioPdfRenderServiceReconciler) updateDeploymentSpec(
	existingDeployment *appsv1.Deployment,
	hprs *corev1alpha1.HumioPdfRenderService,
	desiredResources corev1.ResourceRequirements) bool {

	needsUpdate := false

	// Update replicas
	if existingDeployment.Spec.Replicas == nil || *existingDeployment.Spec.Replicas != hprs.Spec.Replicas {
		existingDeployment.Spec.Replicas = &hprs.Spec.Replicas
		needsUpdate = true
	}

	// Ensure we have at least one container
	if len(existingDeployment.Spec.Template.Spec.Containers) == 0 {
		existingDeployment.Spec.Template.Spec.Containers = []corev1.Container{
			{Name: deploymentName},
		}
		needsUpdate = true
	}

	container := &existingDeployment.Spec.Template.Spec.Containers[0]

	// Update image
	if container.Image != hprs.Spec.Image {
		container.Image = hprs.Spec.Image
		container.ImagePullPolicy = corev1.PullIfNotPresent
		needsUpdate = true
	}

	// Update resources
	if !reflect.DeepEqual(container.Resources, desiredResources) {
		r.Log.Info("Updating container resources", "Current", container.Resources, "Desired", desiredResources)
		container.Resources = desiredResources
		needsUpdate = true
	}

	// Update port
	port := int32(5123)
	if hprs.Spec.Port != 0 {
		port = hprs.Spec.Port
	}
	if len(container.Ports) == 0 || container.Ports[0].ContainerPort != port {
		container.Ports = []corev1.ContainerPort{
			{ContainerPort: port, Name: "http"},
		}
		needsUpdate = true
	}

	// Update probes
	needsUpdate = r.updateDeploymentProbes(container, hprs, port) || needsUpdate

	// Update environment variables
	needsUpdate = r.updateDeploymentEnvVars(container, hprs) || needsUpdate

	// Update volume mounts
	needsUpdate = r.updateDeploymentVolumeMounts(container, hprs) || needsUpdate

	// Update container security context
	needsUpdate = r.updateDeploymentContainerSecurityContext(container, hprs) || needsUpdate

	return needsUpdate
}

// updateDeploymentProbes updates the liveness and readiness probes
func (r *HumioPdfRenderServiceReconciler) updateDeploymentProbes(
	container *corev1.Container,
	hprs *corev1alpha1.HumioPdfRenderService,
	port int32) bool {

	needsUpdate := false
	expectedLivenessProbe := r.getExpectedLivenessProbe(hprs, port)
	expectedReadinessProbe := r.getExpectedReadinessProbe(hprs, port)

	if !reflect.DeepEqual(container.LivenessProbe, expectedLivenessProbe) {
		r.Log.Info("Updating liveness probe configuration")
		container.LivenessProbe = expectedLivenessProbe
		needsUpdate = true
	}

	if !reflect.DeepEqual(container.ReadinessProbe, expectedReadinessProbe) {
		r.Log.Info("Updating readiness probe configuration")
		container.ReadinessProbe = expectedReadinessProbe
		needsUpdate = true
	}
	return needsUpdate
}

// updateDeploymentEnvVars updates the environment variables
func (r *HumioPdfRenderServiceReconciler) updateDeploymentEnvVars(
	container *corev1.Container,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	if !reflect.DeepEqual(container.Env, hprs.Spec.Env) {
		// Use spec Env if provided, otherwise keep existing or set default if empty
		if len(hprs.Spec.Env) > 0 {
			container.Env = hprs.Spec.Env
			return true
		} else if len(container.Env) == 0 {
			// Set default only if current is also empty
			container.Env = []corev1.EnvVar{
				{Name: "LOG_LEVEL", Value: "debug"},
			}
			return true
		}
	}
	return false
}

// updateDeploymentVolumeMounts updates the volume mounts
func (r *HumioPdfRenderServiceReconciler) updateDeploymentVolumeMounts(
	container *corev1.Container,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	expectedVolumeMounts := r.getExpectedVolumeMounts(hprs)
	if !reflect.DeepEqual(container.VolumeMounts, expectedVolumeMounts) {
		container.VolumeMounts = expectedVolumeMounts
		return true
	}
	return false
}

// updateDeploymentContainerSecurityContext updates the container security context
func (r *HumioPdfRenderServiceReconciler) updateDeploymentContainerSecurityContext(
	container *corev1.Container,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	expectedContext := r.getExpectedContainerSecurityContext(hprs)
	if !reflect.DeepEqual(container.SecurityContext, expectedContext) {
		r.Log.Info("Updating container security context",
			"Current", container.SecurityContext,
			"Desired", expectedContext)
		container.SecurityContext = expectedContext.DeepCopy() // Use DeepCopy
		return true
	}
	return false
}

// updateDeploymentPodSpec updates fields directly under PodSpec
func (r *HumioPdfRenderServiceReconciler) updateDeploymentPodSpec(
	podSpec *corev1.PodSpec,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	needsUpdate := false

	// Update volumes
	expectedVolumes := r.getExpectedVolumes(hprs)
	if !reflect.DeepEqual(podSpec.Volumes, expectedVolumes) {
		podSpec.Volumes = expectedVolumes
		needsUpdate = true
	}

	// Update pod security context
	expectedPodSecCtx := hprs.Spec.PodSecurityContext
	if expectedPodSecCtx == nil {
		// If desired is nil, only update if current is not nil and not empty
		if podSpec.SecurityContext != nil && !isPodSecurityContextEmpty(podSpec.SecurityContext) {
			podSpec.SecurityContext = &corev1.PodSecurityContext{} // Set to empty
			needsUpdate = true
		}
	} else if !reflect.DeepEqual(podSpec.SecurityContext, expectedPodSecCtx) {
		podSpec.SecurityContext = expectedPodSecCtx.DeepCopy() // Use DeepCopy
		needsUpdate = true
	}

	// Update service account name
	if podSpec.ServiceAccountName != hprs.Spec.ServiceAccountName {
		podSpec.ServiceAccountName = hprs.Spec.ServiceAccountName
		needsUpdate = true
	}

	// Update affinity
	if !reflect.DeepEqual(podSpec.Affinity, hprs.Spec.Affinity) {
		podSpec.Affinity = hprs.Spec.Affinity
		needsUpdate = true
	}

	// Update image pull secrets
	needsUpdate = r.updateDeploymentImagePullSecrets(podSpec, hprs) || needsUpdate

	return needsUpdate
}

// updateDeploymentImagePullSecrets updates the image pull secrets
func (r *HumioPdfRenderServiceReconciler) updateDeploymentImagePullSecrets(
	podSpec *corev1.PodSpec,
	hprs *corev1alpha1.HumioPdfRenderService) bool {

	currentSecrets := podSpec.ImagePullSecrets
	desiredSecrets := hprs.Spec.ImagePullSecrets

	hasEcrCredentials := false
	if len(currentSecrets) == 1 {
		for _, secret := range currentSecrets {
			if secret.Name == credentialsSecretName { // Use constant
				hasEcrCredentials = true
				break
			}
		}
	}

	if desiredSecrets != nil {
		if !reflect.DeepEqual(currentSecrets, desiredSecrets) {
			podSpec.ImagePullSecrets = desiredSecrets
			return true
		}
	} else if hasEcrCredentials {
		// Preserve default credentials if present and spec is nil
		r.Log.Info("Preserving existing default credentials despite nil in spec") // Updated comment
		// No change needed
	} else if len(currentSecrets) > 0 {
		// If desired is nil and current is not the default, clear it
		podSpec.ImagePullSecrets = nil
		return true
	}

	return false
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
			r.Log.Info("Creating Deployment",
				"Deployment.Name", deployment.Name,
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
			"CurrentOwner", owner.UID,
			"ExpectedOwner", hprs.UID)
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
		// Make sure to set Deployment's spec.selector.matchLabels to match the pod template labels
		if existingDeployment.Spec.Selector == nil {
			existingDeployment.Spec.Selector = &metav1.LabelSelector{}
		}

		if existingDeployment.Spec.Selector.MatchLabels == nil {
			existingDeployment.Spec.Selector.MatchLabels = map[string]string{}
		}

		// Use the same labels as in the pod template
		existingDeployment.Spec.Selector.MatchLabels["app"] = deploymentName // Use constant

		// Implement retry logic with exponential backoff for update conflicts
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Fetch the latest version of the deployment before updating
			if err := r.Client.Get(ctx, types.NamespacedName{
				Name:      existingDeployment.Name,
				Namespace: existingDeployment.Namespace,
			}, existingDeployment); err != nil {
				return err
			}
			return r.updateDeployment(ctx, existingDeployment, hprs, desiredResources)
		})

		if err != nil {
			return r.logErrorAndReturn(err, "Failed to update Deployment after retries")
		}
		return nil
	}

	r.Log.Info("No changes needed for Deployment", "Deployment.Name", existingDeployment.Name)
	return nil
}

// constructService constructs a Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructService(hprs *corev1alpha1.HumioPdfRenderService) *corev1.Service {
	serviceName := deploymentName // Use constant

	// Use the same labels as in the pod template to ensure service selects the pods
	labels := map[string]string{
		"app": deploymentName, // Use constant
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
					Port:       port,
					TargetPort: intstr.FromInt(int(port)),
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
			r.Log.Info("Creating Service", "Service.Name", service.Name, "Service.Namespace", service.Namespace)
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

	// Check selector changes - use "pdf-render-service" to match the pod labels
	expectedSelector := map[string]string{
		"app": deploymentName, // Use constant
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

		// Update labels - keep consistent with pod labels
		if existingService.Labels == nil {
			existingService.Labels = map[string]string{}
		}
		existingService.Labels["app"] = deploymentName // Use constant

		// Update selector to match pod labels
		existingService.Spec.Selector = expectedSelector

		r.Log.Info("Updating Service", "Service.Name", existingService.Name, "Service.Namespace", existingService.Namespace)

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
