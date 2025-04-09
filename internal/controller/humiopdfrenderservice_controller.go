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
	"sort"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

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
	Scheme               *runtime.Scheme
	BaseLogger           logr.Logger
	Log                  logr.Logger
	HumioClient          humio.Client
	Namespace            string
	DefaultPdfRenderPort int32 // Configurable default port
	// Add fields for other configurable defaults here if needed (e.g., SecurityContext, Resources)
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

const (
	humioPdfRenderServiceFinalizer = "core.humio.com/finalizer"
)

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
		// Use hprs.Name to get the deployment for status
		deploymentErr := r.Client.Get(ctx, types.NamespacedName{
			Name:      hprs.Name,
			Namespace: hprs.Namespace,
		}, deployment)

		if deploymentErr == nil {
			// Update status with pod names and readiness
			hprs.Status.ReadyReplicas = deployment.Status.ReadyReplicas

			// Gather pod names and update the nodes list
			podList := &corev1.PodList{}
			listOpts := []client.ListOption{
				client.InNamespace(hprs.Namespace),
				// Use hprs.Name in the label selector for pods
				client.MatchingLabels(map[string]string{
					"app": hprs.Name,
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
	// Rely on watches to trigger reconciliation, remove explicit requeue
	return ctrl.Result{}, nil
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

// constructDesiredDeployment constructs the desired Deployment state based on the HumioPdfRenderService CR.
func (r *HumioPdfRenderServiceReconciler) constructDesiredDeployment(hprs *corev1alpha1.HumioPdfRenderService) *appsv1.Deployment {
	// Define labels based on CR name
	labels := map[string]string{
		"app": hprs.Name, // Use CR name for the app label
	}
	// Add any custom labels from the spec
	if hprs.Spec.Labels != nil {
		for k, v := range hprs.Spec.Labels {
			labels[k] = v
		}
	}

	// Define selector based on CR name
	selector := map[string]string{
		"app": hprs.Name, // Selector must match the app label
	}

	// Determine port, using CR spec or configurable default
	port := hprs.Spec.Port
	if port == 0 {
		port = r.DefaultPdfRenderPort // Use configurable default
	}

	// Determine volumes, defaulting if necessary
	volumes := hprs.Spec.Volumes
	if volumes == nil {
		volumes = []corev1.Volume{
			{
				Name: "app-temp",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
				},
			},
			{
				Name: "tmp",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
				},
			},
		}

		// Add TLS certificate volume if TLS is enabled
		if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
			// Use the same certificate naming convention as the HumioCluster
			certSecretName := fmt.Sprintf("%s-certificate", hprs.Name)
			volumes = append(volumes, corev1.Volume{
				Name: "tls-cert",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: certSecretName,
					},
				},
			})
		}
	}

	// Determine volume mounts, defaulting if necessary
	volumeMounts := hprs.Spec.VolumeMounts
	if volumeMounts == nil {
		volumeMounts = []corev1.VolumeMount{
			{Name: "app-temp", MountPath: "/app/temp"},
			{Name: "tmp", MountPath: "/tmp"},
		}

		// Add TLS certificate volume mount if TLS is enabled
		if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "tls-cert",
				MountPath: "/etc/ssl/certs/pdf-render-service",
				ReadOnly:  true,
			})
		}
	}

	// Determine container security context, defaulting if necessary
	containerSecurityContext := hprs.Spec.SecurityContext
	if containerSecurityContext == nil {
		containerSecurityContext = &corev1.SecurityContext{
			AllowPrivilegeEscalation: helpers.BoolPtr(false),
			Privileged:               helpers.BoolPtr(false),
			ReadOnlyRootFilesystem:   helpers.BoolPtr(true),
			RunAsNonRoot:             helpers.BoolPtr(true),
			RunAsUser:                helpers.Int64Ptr(1000),
			RunAsGroup:               helpers.Int64Ptr(1000),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		}
	}

	// Determine probe scheme based on TLS configuration
	probeScheme := corev1.URISchemeHTTP
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
		probeScheme = corev1.URISchemeHTTPS
	}

	// Determine liveness probe, using CR spec or configurable default path
	livenessProbe := hprs.Spec.LivenessProbe
	if livenessProbe == nil {
		livenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   corev1alpha1.DefaultPdfRenderServiceLiveness, // Use configurable default path
					Port:   intstr.FromInt(int(port)),
					Scheme: probeScheme, // Use scheme based on TLS configuration
				},
			},
			InitialDelaySeconds: 30, TimeoutSeconds: 60, PeriodSeconds: 10, SuccessThreshold: 1, FailureThreshold: 3,
		}
	} else if livenessProbe.HTTPGet != nil && livenessProbe.HTTPGet.Scheme == "" {
		livenessProbe = livenessProbe.DeepCopy() // Avoid modifying the original spec
		livenessProbe.HTTPGet.Scheme = probeScheme
	}

	// Determine readiness probe, using CR spec or configurable default path
	readinessProbe := hprs.Spec.ReadinessProbe
	if readinessProbe == nil {
		readinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   corev1alpha1.DefaultPdfRenderServiceReadiness, // Use configurable default path
					Port:   intstr.FromInt(int(port)),
					Scheme: probeScheme, // Use scheme based on TLS configuration
				},
			},
			InitialDelaySeconds: 30, TimeoutSeconds: 60, PeriodSeconds: 10, SuccessThreshold: 1, FailureThreshold: 1,
		}
	} else if readinessProbe.HTTPGet != nil && readinessProbe.HTTPGet.Scheme == "" {
		readinessProbe = readinessProbe.DeepCopy() // Avoid modifying the original spec
		readinessProbe.HTTPGet.Scheme = probeScheme
	}

	// Construct the desired deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hprs.Name, // Use CR name for Deployment name
			Namespace: hprs.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &hprs.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selector, // Use selector based on CR name
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,                // Use labels based on CR name
					Annotations: hprs.Spec.Annotations, // Directly use annotations from the spec
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: hprs.Spec.ServiceAccountName,
					Affinity:           hprs.Spec.Affinity,
					SecurityContext:    hprs.Spec.PodSecurityContext, // Use directly from spec (can be nil)
					Volumes:            volumes,
					ImagePullSecrets:   hprs.Spec.ImagePullSecrets, // Use directly from spec (can be nil, no default)
					Containers: []corev1.Container{{
						Name:            hprs.Name, // Use CR name for container name
						Image:           hprs.Spec.Image,
						ImagePullPolicy: corev1.PullIfNotPresent,
						SecurityContext: containerSecurityContext,
						Resources:       hprs.Spec.Resources, // Use directly from spec
						Ports: []corev1.ContainerPort{{
							ContainerPort: port,
							Name:          "http",
						}},
						Env:            r.getTLSAwareEnvironmentVariables(hprs), // Add TLS env vars if needed
						VolumeMounts:   volumeMounts,
						LivenessProbe:  livenessProbe,
						ReadinessProbe: readinessProbe,
					}},
				},
			},
		},
	}

	return deployment
}

// normalizeEnvVars sorts env vars by name and removes duplicates for consistent comparison
func normalizeEnvVars(envVars []corev1.EnvVar) []corev1.EnvVar {
	if len(envVars) == 0 {
		return nil
	}

	// Remove duplicates by creating a map
	envMap := make(map[string]corev1.EnvVar)
	for _, env := range envVars {
		envMap[env.Name] = env
	}

	// Convert back to slice and sort by name
	result := make([]corev1.EnvVar, 0, len(envMap))
	for _, env := range envMap {
		result = append(result, env)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// getTLSAwareEnvironmentVariables adds TLS-related environment variables if TLS is enabled
func (r *HumioPdfRenderServiceReconciler) getTLSAwareEnvironmentVariables(hprs *corev1alpha1.HumioPdfRenderService) []corev1.EnvVar {
	// Start with the environment variables from the spec
	envVars := hprs.Spec.EnvironmentVariables

	// If TLS is enabled, add the necessary environment variables
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
		tlsEnvVars := []corev1.EnvVar{
			{
				Name:  "PDF_RENDER_USE_TLS",
				Value: "true",
			},
			{
				Name:  "PDF_RENDER_TLS_CERT_PATH",
				Value: "/etc/ssl/certs/pdf-render-service/tls.crt",
			},
			{
				Name:  "PDF_RENDER_TLS_KEY_PATH",
				Value: "/etc/ssl/certs/pdf-render-service/tls.key",
			},
		}

		// Append TLS env vars to existing env vars
		if envVars == nil {
			envVars = tlsEnvVars
		} else {
			envVars = append(envVars, tlsEnvVars...)
		}
	}

	return envVars
}

// reconcileDeployment reconciles the Deployment for the HumioPdfRenderService
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, hprs *corev1alpha1.HumioPdfRenderService) error {
	desiredDeployment := r.constructDesiredDeployment(hprs)
	desiredDeployment.SetNamespace(hprs.Namespace)

	if err := controllerutil.SetControllerReference(hprs, desiredDeployment, r.Scheme); err != nil {
		return r.logErrorAndReturn(err, "Failed to set controller reference on desired deployment")
	}

	existingDeployment := &appsv1.Deployment{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: hprs.Name, Namespace: hprs.Namespace}, existingDeployment)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Deployment doesn't exist, create it
			r.Log.Info("Creating Deployment", "Deployment.Name", desiredDeployment.Name, "Deployment.Namespace", desiredDeployment.Namespace)
			if createErr := r.Client.Create(ctx, desiredDeployment); createErr != nil {
				return r.logErrorAndReturn(createErr, "Failed to create Deployment")
			}
			r.Log.Info("Successfully created Deployment", "Deployment.Name", desiredDeployment.Name)
			return nil // Creation successful
		}
		return r.logErrorAndReturn(err, "Failed to get Deployment")
	}

	// Deployment exists, check if update is needed using RetryOnConflict
	var lastConflictErr error
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return r.handleDeploymentUpdate(ctx, hprs, existingDeployment, &lastConflictErr)
	})

	if retryErr != nil {
		// Log the final error after retries
		logMsg := "Failed to update Deployment after retries"
		if lastConflictErr != nil {
			logMsg = fmt.Sprintf("%s (last error: conflict)", logMsg)
		}
		return r.logErrorAndReturn(retryErr, logMsg)
	}

	return nil
}

// handleDeploymentUpdate handles the update logic for an existing deployment
func (r *HumioPdfRenderServiceReconciler) handleDeploymentUpdate(
	ctx context.Context,
	hprs *corev1alpha1.HumioPdfRenderService,
	existingDeployment *appsv1.Deployment,
	lastConflictErr *error) error {

	// Get the latest version of the deployment inside the retry loop
	if err := r.Get(ctx, types.NamespacedName{Name: hprs.Name, Namespace: hprs.Namespace}, existingDeployment); err != nil {
		// If not found during retry, it might have been deleted, return nil to stop retrying
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Deployment not found during update retry, likely deleted.")
			return nil
		}
		return err
	}

	// Re-construct desired state in case CR changed between retries
	currentDesiredDeployment := r.constructDesiredDeployment(hprs)

	// Check if an update is needed
	needsUpdate, err := r.isDeploymentUpdateNeeded(hprs, existingDeployment, currentDesiredDeployment)
	if err != nil {
		r.Log.Error(err, "Error checking if deployment needs update")
	}

	// If update is needed, apply the desired state to the existing object
	if needsUpdate {
		return r.updateDeployment(ctx, existingDeployment, currentDesiredDeployment, lastConflictErr)
	}

	r.Log.Info("No changes needed for Deployment", "Deployment.Name", existingDeployment.Name)
	return nil
}

// isDeploymentUpdateNeeded determines if a deployment needs to be updated
func (r *HumioPdfRenderServiceReconciler) isDeploymentUpdateNeeded(
	hprs *corev1alpha1.HumioPdfRenderService,
	existingDeployment *appsv1.Deployment,
	currentDesiredDeployment *appsv1.Deployment) (bool, error) {

	needsUpdate := false

	// Check/Set Controller Reference
	if !metav1.IsControlledBy(existingDeployment, hprs) {
		if err := controllerutil.SetControllerReference(hprs, existingDeployment, r.Scheme); err != nil {
			return true, err
		}
		needsUpdate = true
	}

	// Compare Replicas
	if existingDeployment.Spec.Replicas == nil || *existingDeployment.Spec.Replicas != *currentDesiredDeployment.Spec.Replicas {
		needsUpdate = true
	}

	// Compare PodTemplateSpec
	needsUpdate = needsUpdate || r.isPodTemplateUpdateNeeded(existingDeployment, currentDesiredDeployment)

	// Compare Metadata (Labels, Annotations)
	if !reflect.DeepEqual(existingDeployment.Spec.Template.ObjectMeta.Labels, currentDesiredDeployment.Spec.Template.ObjectMeta.Labels) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.ObjectMeta.Annotations, currentDesiredDeployment.Spec.Template.ObjectMeta.Annotations) {
		needsUpdate = true
	}

	return needsUpdate, nil
}

// isPodTemplateUpdateNeeded checks if the pod template spec needs to be updated
func (r *HumioPdfRenderServiceReconciler) isPodTemplateUpdateNeeded(
	existingDeployment *appsv1.Deployment,
	currentDesiredDeployment *appsv1.Deployment) bool {

	// Normalize Env Vars for comparison
	currentEnv := normalizeEnvVars(existingDeployment.Spec.Template.Spec.Containers[0].Env)
	desiredEnv := normalizeEnvVars(currentDesiredDeployment.Spec.Template.Spec.Containers[0].Env)

	// Compare significant fields in PodSpec and ContainerSpec
	return existingDeployment.Spec.Template.Spec.ServiceAccountName != currentDesiredDeployment.Spec.Template.Spec.ServiceAccountName ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Affinity, currentDesiredDeployment.Spec.Template.Spec.Affinity) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Volumes, currentDesiredDeployment.Spec.Template.Spec.Volumes) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.ImagePullSecrets, currentDesiredDeployment.Spec.Template.Spec.ImagePullSecrets) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.SecurityContext, currentDesiredDeployment.Spec.Template.Spec.SecurityContext) ||
		// Container comparisons
		existingDeployment.Spec.Template.Spec.Containers[0].Image != currentDesiredDeployment.Spec.Template.Spec.Containers[0].Image ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Ports, currentDesiredDeployment.Spec.Template.Spec.Containers[0].Ports) ||
		!reflect.DeepEqual(currentEnv, desiredEnv) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].Resources, currentDesiredDeployment.Spec.Template.Spec.Containers[0].Resources) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].VolumeMounts, currentDesiredDeployment.Spec.Template.Spec.Containers[0].VolumeMounts) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].LivenessProbe, currentDesiredDeployment.Spec.Template.Spec.Containers[0].LivenessProbe) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe, currentDesiredDeployment.Spec.Template.Spec.Containers[0].ReadinessProbe) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.Spec.Containers[0].SecurityContext, currentDesiredDeployment.Spec.Template.Spec.Containers[0].SecurityContext)
}

// updateDeployment updates the existing deployment with the desired state
func (r *HumioPdfRenderServiceReconciler) updateDeployment(
	ctx context.Context,
	existingDeployment *appsv1.Deployment,
	currentDesiredDeployment *appsv1.Deployment,
	lastConflictErr *error) error {

	r.Log.Info("Updating Deployment", "Deployment.Name", existingDeployment.Name)
	// Preserve existing resource version to ensure update targets the correct version
	resourceVersion := existingDeployment.ResourceVersion
	// Apply desired state
	existingDeployment.Spec = currentDesiredDeployment.Spec
	existingDeployment.ObjectMeta.Labels = currentDesiredDeployment.ObjectMeta.Labels // Update top-level labels too
	// Restore the resource version
	existingDeployment.ResourceVersion = resourceVersion

	updateErr := r.Client.Update(ctx, existingDeployment)
	if updateErr != nil {
		if k8serrors.IsConflict(updateErr) {
			*lastConflictErr = updateErr
			r.Log.Info("Conflict detected during deployment update, retrying...", "Deployment.Name", existingDeployment.Name)
		}
		return updateErr
	}
	r.Log.Info("Successfully submitted Deployment update", "Deployment.Name", existingDeployment.Name)
	return nil
}

// constructService constructs a Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructService(hprs *corev1alpha1.HumioPdfRenderService) *corev1.Service {
	// Use CR name for Service name and selector
	serviceName := hprs.Name
	selector := map[string]string{
		"app": hprs.Name, // Selector must match the Deployment/Pod labels
	}

	// Default port to 5123 if not specified
	port := int32(5123)
	if hprs.Spec.Port != 0 {
		port = hprs.Spec.Port
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName, // Use CR name
			Namespace: hprs.Namespace,
			// Labels for the service itself can be added here if needed
		},
		Spec: corev1.ServiceSpec{
			Selector: selector, // Use selector based on CR name
			Type:     hprs.Spec.ServiceType,
			Ports: func() []corev1.ServicePort {
				// Create base ports array
				ports := []corev1.ServicePort{}

				// If TLS is enabled, add only HTTPS port
				if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
					// Add HTTPS port only
					ports = append(ports, corev1.ServicePort{
						Port:       port,
						TargetPort: intstr.FromInt(int(port)),
						Protocol:   corev1.ProtocolTCP,
						Name:       "https",
					})
				} else {
					// If TLS is not enabled, just add HTTP port
					ports = append(ports, corev1.ServicePort{
						Port:       port,
						TargetPort: intstr.FromInt(int(port)),
						Protocol:   corev1.ProtocolTCP,
						Name:       "http",
					})
				}

				return ports
			}(),
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
	// Use hprs.Name to get the service
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      hprs.Name,
		Namespace: hprs.Namespace,
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

	// Check port configuration based on TLS status
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
		// When TLS is enabled, we expect 1 port (HTTPS only)
		if len(existingService.Spec.Ports) != 1 {
			r.Log.Info("Port count changed for TLS-enabled service", "Old", len(existingService.Spec.Ports), "New", 1)
			needsUpdate = true
		} else {
			// Check if the port is HTTPS with correct configuration
			if existingService.Spec.Ports[0].Name != "https" ||
				existingService.Spec.Ports[0].Port != port ||
				existingService.Spec.Ports[0].TargetPort.IntVal != port {
				r.Log.Info("HTTPS port configuration changed")
				needsUpdate = true
			}
		}
	} else {
		// When TLS is disabled, we expect 1 port (HTTP)
		if len(existingService.Spec.Ports) != 1 {
			r.Log.Info("Port count changed for non-TLS service", "Old", len(existingService.Spec.Ports), "New", 1)
			needsUpdate = true
		} else if existingService.Spec.Ports[0].Port != port ||
			existingService.Spec.Ports[0].TargetPort.IntVal != port ||
			existingService.Spec.Ports[0].Name != "http" {
			r.Log.Info("HTTP port configuration changed")
			needsUpdate = true
		}
	}

	// Selector check: Ensure selector matches desired state (based on CR name)
	desiredSelector := map[string]string{"app": hprs.Name}
	if !reflect.DeepEqual(existingService.Spec.Selector, desiredSelector) {
		r.Log.Info("Service selector changed", "Old", existingService.Spec.Selector, "New", desiredSelector)
		needsUpdate = true
	}

	// If an update is needed, apply the necessary changes
	if needsUpdate {
		// Preserve existing resource version and clusterIP
		resourceVersion := existingService.ResourceVersion
		clusterIP := existingService.Spec.ClusterIP

		// Apply desired state from the constructed service
		existingService.Spec = service.Spec // This includes Type, Ports, Selector

		// Restore immutable fields
		existingService.ResourceVersion = resourceVersion
		existingService.Spec.ClusterIP = clusterIP

		r.Log.Info("Updating Service", "Service.Name", existingService.Name, "Service.Namespace", existingService.Namespace)
		if updateErr := r.Client.Update(ctx, existingService); updateErr != nil {
			return r.logErrorAndReturn(updateErr, "Failed to update Service")
		}
		r.Log.Info("Successfully updated Service", "Service.Name", existingService.Name)
		return nil
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
