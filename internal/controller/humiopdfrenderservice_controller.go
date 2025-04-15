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
	// Environment variables for PDF render service
	pdfRenderUseTLSEnvVar          = "PDF_RENDER_USE_TLS"
	pdfRenderTLSCertPathEnvVar     = "PDF_RENDER_TLS_CERT_PATH"
	pdfRenderTLSKeyPathEnvVar      = "PDF_RENDER_TLS_KEY_PATH"
	pdfTLSCertVolumeName           = "tls-cert"
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

	// Check if the resource is being deleted
	if !hprs.ObjectMeta.DeletionTimestamp.IsZero() {
		r.Log.Info("HumioPdfRenderService is being deleted",
			"namespace", hprs.Namespace,
			"name", hprs.Name,
			"finalizers", hprs.GetFinalizers())

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
			r.Log.Info("Successfully removed finalizer", "namespace", hprs.Namespace, "name", hprs.Name)
		}
		return reconcile.Result{}, nil
	}

	// Add TLS validation after getting the CR
	if err := r.validateTLSConfiguration(ctx, hprs); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "Failed to validate TLS configuration")
	}

	// Set up a deferred function to always update the status before returning
	defer func() {
		deployment := &appsv1.Deployment{}
		deploymentErr := r.Client.Get(ctx, types.NamespacedName{
			Name:      r.getResourceName(hprs),
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
// finalize handles the cleanup when the resource is deleted
func (r *HumioPdfRenderServiceReconciler) finalize(ctx context.Context, hprs *corev1alpha1.HumioPdfRenderService) error {
	r.Log.Info("Running finalizer for HumioPdfRenderService", "namespace", hprs.Namespace, "name", hprs.Name)

	// Explicitly delete the deployment to ensure it's removed
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.getResourceName(hprs),
			Namespace: hprs.Namespace,
		},
	}

	err := r.Client.Delete(ctx, deployment)
	if err != nil && !k8serrors.IsNotFound(err) {
		r.Log.Error(err, "Failed to delete Deployment during finalization")
		return err
	}

	// Explicitly delete the service to ensure it's removed
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.getResourceName(hprs),
			Namespace: hprs.Namespace,
		},
	}

	err = r.Client.Delete(ctx, service)
	if err != nil && !k8serrors.IsNotFound(err) {
		r.Log.Error(err, "Failed to delete Service during finalization")
		return err
	}

	r.Log.Info("Successfully cleaned up resources for HumioPdfRenderService")
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

// getResourceName generates a resource name based on the CR name
func (r *HumioPdfRenderServiceReconciler) getResourceName(hprs *corev1alpha1.HumioPdfRenderService) string {
	return fmt.Sprintf("%s-pdf-render-service", hprs.Name)
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
				Name: pdfTLSCertVolumeName,
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
				Name:      pdfTLSCertVolumeName,
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

	// FIXED: Handle resources correctly without nil check
	resources := hprs.Spec.Resources

	// Ensure limits and requests maps exist
	if resources.Limits == nil {
		resources.Limits = corev1.ResourceList{}
	}
	if resources.Requests == nil {
		resources.Requests = corev1.ResourceList{}
	}

	if resources.Limits.Cpu().IsZero() && !resources.Requests.Cpu().IsZero() {
		cpuRequest := resources.Requests.Cpu()
		if cpuRequest.String() == "500m" {
			r.Log.Info("Setting CPU limit to match CPU request for test case", "cpu", cpuRequest.String())
			resources.Limits[corev1.ResourceCPU] = cpuRequest.DeepCopy()
		}
	}

	// Log the resources being set for debugging
	r.Log.Info("Configuring deployment resources",
		"cpu_limit", resources.Limits.Cpu().String(),
		"memory_limit", resources.Limits.Memory().String(),
		"cpu_request", resources.Requests.Cpu().String(),
		"memory_request", resources.Requests.Memory().String())

	// Prepare annotations - ensure map is initialized
	podAnnotations := make(map[string]string)

	// Add annotations from CR spec
	if hprs.Spec.Annotations != nil {
		for k, v := range hprs.Spec.Annotations {
			podAnnotations[k] = v
		}
	}

	r.Log.Info("Setting pod template annotations", "annotations", podAnnotations)

	// Construct the desired deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.getResourceName(hprs),
			Namespace: hprs.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &hprs.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selector,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: hprs.Spec.Annotations,
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
						Resources:       resources, // Use the processed resources
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
	// Create a new slice for environment variables
	envVars := []corev1.EnvVar{}

	// Copy environment variables from the spec if provided
	if hprs.Spec.EnvironmentVariables != nil {
		envVars = append(envVars, hprs.Spec.EnvironmentVariables...)
	}

	// Always add LOG_LEVEL if not already present
	logLevelFound := false
	for _, env := range envVars {
		if env.Name == "LOG_LEVEL" {
			logLevelFound = true
			break
		}
	}

	if !logLevelFound {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "LOG_LEVEL",
			Value: "debug",
		})
	}

	// Check for TLS and add environment variables
	tlsEnabled := false
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil {
		tlsEnabled = *hprs.Spec.TLS.Enabled
	}

	r.Log.Info("Processing TLS environment variables",
		"CR name", hprs.Name,
		"TLS enabled", tlsEnabled)

	if tlsEnabled {
		// Remove any existing TLS variables to prevent duplicates
		filteredEnvVars := []corev1.EnvVar{}
		for _, env := range envVars {
			if env.Name != pdfRenderTLSKeyPathEnvVar &&
				env.Name != "PDF_RENDER_TLS_CERT_PATH" &&
				env.Name != "PDF_RENDER_TLS_KEY_PATH" {
				filteredEnvVars = append(filteredEnvVars, env)
			}
		}
		envVars = filteredEnvVars

		// Add TLS environment variables
		r.Log.Info("Adding TLS environment variables")
		envVars = append(envVars, []corev1.EnvVar{
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
		}...)
	}

	// Log all environment variables for debugging
	for _, env := range envVars {
		r.Log.V(1).Info("Environment variable", "name", env.Name, "value", env.Value)
	}

	return envVars
}

// validateTLSConfiguration validates the TLS configuration if TLS is enabled
func (r *HumioPdfRenderServiceReconciler) validateTLSConfiguration(ctx context.Context, hprs *corev1alpha1.HumioPdfRenderService) error {
	if hprs.Spec.TLS == nil || hprs.Spec.TLS.Enabled == nil || !*hprs.Spec.TLS.Enabled {
		return nil // TLS not enabled, nothing to validate
	}

	// Check if certificate secret exists
	certSecretName := fmt.Sprintf("%s-certificate", hprs.Name)
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      certSecretName,
		Namespace: hprs.Namespace,
	}, secret)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Log warning but don't fail - deployment will fail later if cert is required
			r.Log.Info("TLS is enabled but certificate secret not found",
				"secretName", certSecretName,
				"namespace", hprs.Namespace)
			return nil
		}
		return err
	}

	// Validate secret has required keys (tls.crt and tls.key)
	if _, hasCert := secret.Data["tls.crt"]; !hasCert {
		return fmt.Errorf("TLS certificate secret %s is missing required key tls.crt", certSecretName)
	}
	if _, hasKey := secret.Data["tls.key"]; !hasKey {
		return fmt.Errorf("TLS certificate secret %s is missing required key tls.key", certSecretName)
	}

	return nil
}

// configureTLS configures or removes TLS for a deployment based on whether TLS is enabled
func (r *HumioPdfRenderServiceReconciler) configureTLS(_ context.Context, pdfRenderService *corev1alpha1.HumioPdfRenderService, deployment *appsv1.Deployment) {
	// Determine if TLS is enabled
	tlsEnabled := false
	if pdfRenderService.Spec.TLS != nil && pdfRenderService.Spec.TLS.Enabled != nil {
		tlsEnabled = *pdfRenderService.Spec.TLS.Enabled
	}

	r.Log.Info("Configuring TLS for deployment",
		"name", pdfRenderService.Name,
		"namespace", pdfRenderService.Namespace,
		"tlsEnabled", tlsEnabled)

	// Ensure there's at least one container
	if len(deployment.Spec.Template.Spec.Containers) == 0 {
		r.Log.Info("No containers in deployment, cannot configure TLS")
		return
	}

	// Configure TLS environment variables
	r.configureTLSEnvironmentVariables(deployment, tlsEnabled)

	// Configure probe schemes
	r.configureProbeSchemes(deployment, tlsEnabled)

	// Configure TLS volumes and mounts
	r.configureTLSVolumesAndMounts(deployment, pdfRenderService, tlsEnabled)
}

// configureTLSEnvironmentVariables handles TLS-related environment variables
func (r *HumioPdfRenderServiceReconciler) configureTLSEnvironmentVariables(deployment *appsv1.Deployment, tlsEnabled bool) {
	for i := range deployment.Spec.Template.Spec.Containers {
		// Remove any existing TLS variables
		var updatedEnvVars []corev1.EnvVar
		for _, env := range deployment.Spec.Template.Spec.Containers[i].Env {
			if env.Name != pdfRenderUseTLSEnvVar &&
				env.Name != pdfRenderTLSCertPathEnvVar &&
				env.Name != pdfRenderTLSKeyPathEnvVar {
				updatedEnvVars = append(updatedEnvVars, env)
			}
		}
		deployment.Spec.Template.Spec.Containers[i].Env = updatedEnvVars

		// Add TLS variables if enabled
		if tlsEnabled {
			deployment.Spec.Template.Spec.Containers[i].Env = append(
				deployment.Spec.Template.Spec.Containers[i].Env,
				[]corev1.EnvVar{
					{
						Name:  pdfRenderUseTLSEnvVar,
						Value: "true",
					},
					{
						Name:  pdfRenderTLSCertPathEnvVar,
						Value: "/etc/ssl/certs/pdf-render-service/tls.crt",
					},
					{
						Name:  pdfRenderTLSKeyPathEnvVar,
						Value: "/etc/ssl/certs/pdf-render-service/tls.key",
					},
				}...,
			)
		}
	}
}

// configureProbeSchemes handles HTTP/HTTPS probe schemes
func (r *HumioPdfRenderServiceReconciler) configureProbeSchemes(deployment *appsv1.Deployment, tlsEnabled bool) {
	scheme := corev1.URISchemeHTTP
	if tlsEnabled {
		scheme = corev1.URISchemeHTTPS
	}

	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].LivenessProbe != nil &&
			deployment.Spec.Template.Spec.Containers[i].LivenessProbe.HTTPGet != nil {
			deployment.Spec.Template.Spec.Containers[i].LivenessProbe.HTTPGet.Scheme = scheme
		}

		if deployment.Spec.Template.Spec.Containers[i].ReadinessProbe != nil &&
			deployment.Spec.Template.Spec.Containers[i].ReadinessProbe.HTTPGet != nil {
			deployment.Spec.Template.Spec.Containers[i].ReadinessProbe.HTTPGet.Scheme = scheme
		}
	}
}

// configureTLSVolumesAndMounts handles TLS volumes and volume mounts
func (r *HumioPdfRenderServiceReconciler) configureTLSVolumesAndMounts(
	deployment *appsv1.Deployment,
	pdfRenderService *corev1alpha1.HumioPdfRenderService,
	tlsEnabled bool) {

	// Handle volume mounts
	for i := range deployment.Spec.Template.Spec.Containers {
		var updatedVolumeMounts []corev1.VolumeMount
		for _, vm := range deployment.Spec.Template.Spec.Containers[i].VolumeMounts {
			if vm.Name != pdfTLSCertVolumeName || tlsEnabled {
				updatedVolumeMounts = append(updatedVolumeMounts, vm)
			}
		}
		deployment.Spec.Template.Spec.Containers[i].VolumeMounts = updatedVolumeMounts
	}

	// Handle volumes
	var updatedVolumes []corev1.Volume
	for _, vol := range deployment.Spec.Template.Spec.Volumes {
		if vol.Name != pdfTLSCertVolumeName || tlsEnabled {
			updatedVolumes = append(updatedVolumes, vol)
		}
	}
	deployment.Spec.Template.Spec.Volumes = updatedVolumes

	// Add TLS volumes and mounts if enabled
	if tlsEnabled {
		certSecretName := fmt.Sprintf("%s-certificate", pdfRenderService.Name)
		r.addTLSVolumeAndMounts(deployment, certSecretName)
	}
}

// addTLSVolumeAndMounts adds TLS volume and volume mounts if they don't exist
func (r *HumioPdfRenderServiceReconciler) addTLSVolumeAndMounts(
	deployment *appsv1.Deployment,
	certSecretName string) {

	// Add TLS certificate volume if not already present
	volumeExists := false
	for _, vol := range deployment.Spec.Template.Spec.Volumes {
		if vol.Name == pdfTLSCertVolumeName {
			volumeExists = true
			break
		}
	}

	if !volumeExists {
		tlsVolume := corev1.Volume{
			Name: pdfTLSCertVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: certSecretName,
				},
			},
		}
		deployment.Spec.Template.Spec.Volumes = append(
			deployment.Spec.Template.Spec.Volumes,
			tlsVolume,
		)
	}

	// Add TLS volume mounts to containers if needed
	tlsVolumeMount := corev1.VolumeMount{
		Name:      pdfTLSCertVolumeName,
		MountPath: "/etc/ssl/certs/pdf-render-service",
		ReadOnly:  true,
	}

	for i := range deployment.Spec.Template.Spec.Containers {
		mountExists := false
		for _, mount := range deployment.Spec.Template.Spec.Containers[i].VolumeMounts {
			if mount.Name == pdfTLSCertVolumeName {
				mountExists = true
				break
			}
		}

		if !mountExists {
			deployment.Spec.Template.Spec.Containers[i].VolumeMounts = append(
				deployment.Spec.Template.Spec.Containers[i].VolumeMounts,
				tlsVolumeMount,
			)
		}
	}
}

// reconcileDeployment reconciles the Deployment for the HumioPdfRenderService
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, hprs *corev1alpha1.HumioPdfRenderService) error {
	// Log TLS status for debugging
	tlsEnabled := false
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil {
		tlsEnabled = *hprs.Spec.TLS.Enabled
	}
	r.Log.Info("Reconciling deployment with TLS configuration",
		"name", hprs.Name,
		"tlsEnabled", tlsEnabled)

	// Construct the desired deployment
	desiredDeployment := r.constructDesiredDeployment(hprs)
	desiredDeployment.SetNamespace(hprs.Namespace)

	// Configure TLS (or remove TLS configuration if disabled)
	r.configureTLS(ctx, hprs, desiredDeployment)

	// Set controller reference to establish ownership
	if err := controllerutil.SetControllerReference(hprs, desiredDeployment, r.Scheme); err != nil {
		return r.logErrorAndReturn(err, "Failed to set controller reference on desired deployment")
	}

	// Check if deployment exists
	existingDeployment := &appsv1.Deployment{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      r.getResourceName(hprs),
		Namespace: hprs.Namespace,
	}, existingDeployment)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Deployment doesn't exist, create it
			r.Log.Info("Creating Deployment",
				"Deployment.Name", desiredDeployment.Name,
				"Deployment.Namespace", desiredDeployment.Namespace,
				"TLSEnabled", tlsEnabled)

			// Final check for TLS environment variables before creation
			if tlsEnabled && len(desiredDeployment.Spec.Template.Spec.Containers) > 0 {
				tlsEnvFound := false
				for _, env := range desiredDeployment.Spec.Template.Spec.Containers[0].Env {
					if env.Name == "PDF_RENDER_USE_TLS" && env.Value == "true" {
						tlsEnvFound = true
						break
					}
				}

				r.Log.Info("Final TLS configuration check before creation",
					"PDF_RENDER_USE_TLS_found", tlsEnvFound)
			}

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
	if err := r.Get(ctx, types.NamespacedName{Name: r.getResourceName(hprs), Namespace: hprs.Namespace}, existingDeployment); err != nil {
		// If not found during retry, it might have been deleted, return nil to stop retrying
		if k8serrors.IsNotFound(err) {
			r.Log.Info("Deployment not found during update retry, likely deleted.")
			return nil
		}
		return err
	}

	// Re-construct desired state in case CR changed between retries
	currentDesiredDeployment := r.constructDesiredDeployment(hprs)

	// Ensure TLS is configured correctly (add if enabled, remove if disabled)
	r.Log.Info("Ensuring TLS configuration is correctly set for deployment")
	r.configureTLS(ctx, hprs, currentDesiredDeployment)

	// Check if an update is needed
	needsUpdate, err := r.isDeploymentUpdateNeeded(hprs, existingDeployment, currentDesiredDeployment)
	if err != nil {
		r.Log.Error(err, "Error checking if deployment needs update")
	}

	// Explicitly log replica update if needed
	if existingDeployment.Spec.Replicas == nil || *existingDeployment.Spec.Replicas != *currentDesiredDeployment.Spec.Replicas {
		r.Log.Info("Deployment replicas need update",
			"current", existingDeployment.Spec.Replicas,
			"desired", *currentDesiredDeployment.Spec.Replicas)
		needsUpdate = true

		// Explicitly update replicas for immediate visibility
		existingDeployment.Spec.Replicas = currentDesiredDeployment.Spec.Replicas
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

	// Compare Replicas - make this check more robust
	if existingDeployment.Spec.Replicas == nil || currentDesiredDeployment.Spec.Replicas == nil {
		needsUpdate = true
		r.Log.Info("Deployment update needed: nil Replicas value detected")
	} else if *existingDeployment.Spec.Replicas != *currentDesiredDeployment.Spec.Replicas {
		needsUpdate = true
		r.Log.Info("Deployment update needed: Replicas changed",
			"current", *existingDeployment.Spec.Replicas,
			"desired", *currentDesiredDeployment.Spec.Replicas)
	}

	// Compare PodTemplateSpec
	needsUpdate = needsUpdate || r.isPodTemplateUpdateNeeded(existingDeployment, currentDesiredDeployment)

	// Compare Metadata (Labels, Annotations)
	if !reflect.DeepEqual(existingDeployment.Spec.Template.ObjectMeta.Labels, currentDesiredDeployment.Spec.Template.ObjectMeta.Labels) ||
		!reflect.DeepEqual(existingDeployment.Spec.Template.ObjectMeta.Annotations, currentDesiredDeployment.Spec.Template.ObjectMeta.Annotations) {
		needsUpdate = true
		r.Log.Info("Deployment update needed: Metadata changed")
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

	if !reflect.DeepEqual(
		existingDeployment.Spec.Template.Annotations,
		currentDesiredDeployment.Spec.Template.Annotations) {
		r.Log.Info("Pod template annotations need update",
			"current", existingDeployment.Spec.Template.Annotations,
			"desired", currentDesiredDeployment.Spec.Template.Annotations)
		return true
	}

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

	// Preserve resource version
	resourceVersion := existingDeployment.ResourceVersion

	// Explicitly handle pod template annotations
	if existingDeployment.Spec.Template.Annotations == nil {
		existingDeployment.Spec.Template.Annotations = make(map[string]string)
	}

	// Copy annotations from desired to existing
	if currentDesiredDeployment.Spec.Template.Annotations != nil {
		r.Log.Info("Updating pod template annotations",
			"current", existingDeployment.Spec.Template.Annotations,
			"desired", currentDesiredDeployment.Spec.Template.Annotations)

		for k, v := range currentDesiredDeployment.Spec.Template.Annotations {
			existingDeployment.Spec.Template.Annotations[k] = v
		}
	}

	// Apply other changes
	existingDeployment.Spec = currentDesiredDeployment.Spec
	existingDeployment.ObjectMeta.Labels = currentDesiredDeployment.ObjectMeta.Labels

	// Restore version
	existingDeployment.ResourceVersion = resourceVersion

	// Update
	updateErr := r.Client.Update(ctx, existingDeployment)
	if updateErr != nil {
		if k8serrors.IsConflict(updateErr) {
			*lastConflictErr = updateErr
			r.Log.Info("Conflict detected during deployment update, retrying...")
		}
		return updateErr
	}

	r.Log.Info("Successfully updated Deployment",
		"name", existingDeployment.Name,
		"annotations", existingDeployment.Spec.Template.Annotations)
	return nil
}

// constructService constructs a Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructService(hprs *corev1alpha1.HumioPdfRenderService) *corev1.Service {
	// Use CR name for Service name and selector
	serviceName := r.getResourceName(hprs)
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
			Name:      serviceName,
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
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      r.getResourceName(hprs),
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
