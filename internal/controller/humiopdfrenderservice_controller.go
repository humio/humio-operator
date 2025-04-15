/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
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
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
)

// HumioPdfRenderServiceReconciler reconciles a HumioPdfRenderService object
type HumioPdfRenderServiceReconciler struct {
	client.Client
	CommonConfig
	Scheme     *runtime.Scheme
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
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
	pdfRenderCAFileEnvVar          = "PDF_RENDER_CA_FILE"
	pdfTLSCertVolumeName           = "tpdf-render-tls-cert-volume"
	pdfTLSCertMountPath            = "/certs"
	humioPdfRenderServiceFinalizer = "core.humio.com/finalizer"
	credentialsSecretName          = "regcred"
	DefaultPdfRenderServicePort    = 5123
	// Constants for PDF rendering service and TLS
	pdfRenderTLSCertName  = "pdf-render-tls-cert"
	PdfRenderUseTLSEnvVar = "PDF_RENDER_USE_TLS"
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
	hprs := &humiov1alpha1.HumioPdfRenderService{}
	err := r.Get(ctx, req.NamespacedName, hprs)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	// Handle finalizer logic
	// Check if the HumioPdfRenderService instance is marked to be deleted
	isMarkedToBeDeleted := hprs.GetDeletionTimestamp() != nil
	if isMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(hprs, humioPdfRenderServiceFinalizer) {
			// Run finalization logic.
			if err := r.finalize(ctx, hprs); err != nil {
				return ctrl.Result{}, r.logErrorAndReturn(err, "Finalization failed")
			}

			// Remove finalizer.
			controllerutil.RemoveFinalizer(hprs, humioPdfRenderServiceFinalizer)
			err := r.Update(ctx, hprs)
			if err != nil {
				return ctrl.Result{}, r.logErrorAndReturn(err, "Failed to remove finalizer")
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer for this CR if it doesn't exist
	if !controllerutil.ContainsFinalizer(hprs, humioPdfRenderServiceFinalizer) {
		if err := r.addFinalizer(ctx, hprs); err != nil {
			return ctrl.Result{}, r.logErrorAndReturn(err, "Failed to add finalizer")
		}
	}

	// Validate TLS configuration
	validationErr := r.validateTLSConfiguration(ctx, hprs)
	if validationErr != nil {
		_ = r.setState(ctx, humiov1alpha1.HumioClusterStateConfigError, hprs) // Best effort state update
		// Log the validation error but continue to allow status update
		r.Log.Error(validationErr, "TLS configuration validation failed")
	}

	// Defer status update until the end of the reconcile loop
	defer func() {
		// Fetch the latest version to avoid race conditions when updating status
		latestHprs := &humiov1alpha1.HumioPdfRenderService{}
		getErr := r.Get(ctx, req.NamespacedName, latestHprs)
		if getErr != nil {
			if k8serrors.IsNotFound(getErr) {
				r.Log.Info("HumioPdfRenderService not found for deferred status update, perhaps deleted.")
				return // Object is gone, nothing to update
			}
			r.Log.Error(getErr, "Failed to get latest HumioPdfRenderService for deferred status update")
			return // Exit defer func on error
		}

		// Get the current deployment state for status calculation
		deployment := &appsv1.Deployment{}
		deploymentErr := r.Client.Get(ctx, types.NamespacedName{
			Name:      r.getResourceName(latestHprs), // Use latestHprs here
			Namespace: latestHprs.Namespace,
		}, deployment)

		// If TLS validation failed earlier, ensure state reflects ConfigError
		if validationErr != nil && latestHprs.Status.State != humiov1alpha1.HumioClusterStateConfigError {
			_ = r.setState(ctx, humiov1alpha1.HumioClusterStateConfigError, latestHprs)
			// Re-fetch after setting state to get the absolute latest for comparison
			_ = r.Get(ctx, req.NamespacedName, latestHprs)
		}

		// Update status based on the fetched deployment (or error)
		updateErr := r.updateStatus(ctx, latestHprs, deployment, deploymentErr)
		if updateErr != nil {
			r.Log.Error(updateErr, "Deferred status update failed")
		}
	}()

	// If TLS validation failed, return immediately after setting ConfigError state (defer will run)
	if validationErr != nil {
		return ctrl.Result{}, validationErr // Return the validation error to trigger requeue
	}

	// Ensure Deployment exists and is up to date
	_, err = r.reconcileDeployment(ctx, hprs) // Assign to _ as deployment is fetched again in defer
	if err != nil {
		_ = r.setState(ctx, humiov1alpha1.HumioClusterStateConfigError, hprs)
		return ctrl.Result{}, r.logErrorAndReturn(err, "Failed to reconcile Deployment")
	}

	// Reconcile Service using controllerutil.CreateOrUpdate
	if err := r.reconcileService(ctx, hprs); err != nil {
		_ = r.setState(ctx, humiov1alpha1.HumioClusterStateConfigError, hprs)
		return ctrl.Result{}, r.logErrorAndReturn(err, "Failed to reconcile Service")
	}

	r.Log.Info("Reconciliation completed successfully")
	// Status update is handled by the deferred function.
	return ctrl.Result{RequeueAfter: time.Minute * 1}, nil // Requeue after 1 minute
}

// validateTLSConfiguration checks if the required TLS secret exists when TLS is enabled.
func (r *HumioPdfRenderServiceReconciler) validateTLSConfiguration(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	if hprs.Spec.TLS == nil || hprs.Spec.TLS.Enabled == nil || !*hprs.Spec.TLS.Enabled {
		return nil // TLS not enabled
	}

	// Use the derived secret name as SecretName field doesn't exist on HumioClusterTLSSpec
	secretName := fmt.Sprintf("%s-certificate", hprs.Name)
	r.Log.Info("Validating TLS configuration, checking for derived secret", "SecretName", secretName, "Namespace", hprs.Namespace)

	secret, err := kubernetes.GetSecret(ctx, r.Client, secretName, hprs.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			errMsg := fmt.Sprintf("TLS is enabled but the required secret '%s' was not found in namespace '%s'", secretName, hprs.Namespace)
			r.Log.Error(err, errMsg)
			return fmt.Errorf(errMsg)
		}
		return r.logErrorAndReturn(err, fmt.Sprintf("failed to get TLS secret '%s'", secretName))
	}

	if _, ok := secret.Data[corev1.TLSCertKey]; !ok {
		errMsg := fmt.Sprintf("TLS secret '%s' is missing key '%s'", secretName, corev1.TLSCertKey)
		r.Log.Error(nil, errMsg)
		return fmt.Errorf(errMsg)
	}
	if _, ok := secret.Data[corev1.TLSPrivateKeyKey]; !ok {
		errMsg := fmt.Sprintf("TLS secret '%s' is missing key '%s'", secretName, corev1.TLSPrivateKeyKey)
		r.Log.Error(nil, errMsg)
		return fmt.Errorf(errMsg)
	}

	r.Log.Info("TLS configuration validated successfully", "SecretName", secretName)
	return nil
}

// finalize handles the cleanup when the resource is deleted
func (r *HumioPdfRenderServiceReconciler) finalize(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	r.Log.Info("Running finalizer for HumioPdfRenderService", "namespace", hprs.Namespace, "name", hprs.Name)

	// Explicitly delete the deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.getResourceName(hprs),
			Namespace: hprs.Namespace,
		},
	}
	err := r.Client.Delete(ctx, deployment)
	if err != nil && !k8serrors.IsNotFound(err) {
		return r.logErrorAndReturn(err, "Failed to delete Deployment during finalization")
	}
	r.Log.Info("Deployment deleted or already gone during finalization", "name", deployment.Name)

	// Explicitly delete the service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.getResourceName(hprs),
			Namespace: hprs.Namespace,
		},
	}
	err = r.Client.Delete(ctx, service)
	if err != nil && !k8serrors.IsNotFound(err) {
		return r.logErrorAndReturn(err, "Failed to delete Service during finalization")
	}
	r.Log.Info("Service deleted or already gone during finalization", "name", service.Name)

	r.Log.Info("Successfully finalized HumioPdfRenderService")
	return nil
}

// addFinalizer adds the finalizer to the resource
func (r *HumioPdfRenderServiceReconciler) addFinalizer(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	r.Log.Info("Adding Finalizer for the HumioPdfRenderService")
	controllerutil.AddFinalizer(hprs, humioPdfRenderServiceFinalizer) // Use controllerutil helper
	err := r.Update(ctx, hprs)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioPdfRenderService with finalizer")
	}
	r.Log.Info("Finalizer added successfully")
	return nil
}

// setState updates the state field in the status, ensuring not to overwrite ConfigError unless necessary
func (r *HumioPdfRenderServiceReconciler) setState(ctx context.Context, state string, hprs *humiov1alpha1.HumioPdfRenderService) error {
	// Avoid overwriting ConfigError with transient states like Pending/Upgrading
	if hprs.Status.State == humiov1alpha1.HumioClusterStateConfigError && state != humiov1alpha1.HumioClusterStateRunning {
		// If already in ConfigError, only allow transition to Running
		if state == humiov1alpha1.HumioClusterStateConfigError {
			return nil // Already in ConfigError, no change needed
		} else {
			r.Log.Info("Preserving ConfigError state, attempted transition ignored", "TargetState", state)
			return nil // Do not change from ConfigError unless to Running
		}
	}

	if hprs.Status.State == state {
		return nil // State is already correct
	}

	r.Log.Info("Setting PDF render service state", "OldState", hprs.Status.State, "NewState", state)
	hprs.Status.State = state
	// Fetch the latest version before updating status to reduce conflicts
	latestHprs := &humiov1alpha1.HumioPdfRenderService{}
	if err := r.Get(ctx, types.NamespacedName{Name: hprs.Name, Namespace: hprs.Namespace}, latestHprs); err != nil {
		return r.logErrorAndReturn(err, "Failed to get latest HPRS before setting state")
	}
	latestHprs.Status.State = state // Apply the state change to the latest version
	return r.Status().Update(ctx, latestHprs)
}

// logErrorAndReturn logs an error and returns it with additional context
func (r *HumioPdfRenderServiceReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

// getResourceName generates a resource name based on the CR name
func (r *HumioPdfRenderServiceReconciler) getResourceName(hprs *humiov1alpha1.HumioPdfRenderService) string {
	// Using hprs.Name directly as suggested in Plan Item 2 for simplification
	return hprs.Name
}

// updateStatus updates the status fields of the HumioPdfRenderService resource based on the Deployment state.
func (r *HumioPdfRenderServiceReconciler) updateStatus(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService, deployment *appsv1.Deployment, deploymentErr error) error {
	log := log.FromContext(ctx)
	originalStatus := hprs.Status.DeepCopy() // Keep original for comparison
	desiredStatus := hprs.Status.DeepCopy()  // Work on a copy

	var calculatedState string
	var readyReplicas int32
	var podNames []string

	// Determine state based on deployment status or error
	if deploymentErr != nil {
		if k8serrors.IsNotFound(deploymentErr) {
			log.Info("Deployment not found for status update, calculating state as Pending")
			calculatedState = humiov1alpha1.HumioClusterStatePending
			readyReplicas = 0
			podNames = []string{}
		} else {
			log.Error(deploymentErr, "failed to get Deployment for status update, calculating state as ConfigError")
			calculatedState = humiov1alpha1.HumioClusterStateConfigError
			readyReplicas = 0
			podNames = []string{}
		}
	} else {
		// Deployment found, calculate state based on its status
		readyReplicas = deployment.Status.ReadyReplicas

		// Get Pod names
		podList := &corev1.PodList{}
		listOpts := []client.ListOption{
			client.InNamespace(hprs.Namespace),
			client.MatchingLabels(deployment.Spec.Selector.MatchLabels),
		}
		if err := r.List(ctx, podList, listOpts...); err != nil {
			log.Error(err, "failed to list pods for status update, calculating state as ConfigError")
			calculatedState = humiov1alpha1.HumioClusterStateConfigError
			podNames = []string{}
		} else {
			podNames = make([]string, len(podList.Items))
			for i, pod := range podList.Items {
				podNames[i] = pod.Name
			}
			sort.Strings(podNames)

			// State calculation logic
			desiredReplicas := int32(1)
			if deployment.Spec.Replicas != nil {
				desiredReplicas = *deployment.Spec.Replicas
			}

			if desiredReplicas == 0 {
				calculatedState = "ScaledDown" // Consider adding a constant
			} else if deployment.Status.ReadyReplicas == desiredReplicas && deployment.Status.ObservedGeneration == deployment.Generation {
				calculatedState = humiov1alpha1.HumioClusterStateRunning
			} else if deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.UpdatedReplicas < desiredReplicas {
				calculatedState = humiov1alpha1.HumioClusterStateUpgrading
			} else {
				calculatedState = humiov1alpha1.HumioClusterStatePending
			}
		}
	}

	// Apply calculated values to desiredStatus
	desiredStatus.ReadyReplicas = readyReplicas
	desiredStatus.Nodes = podNames

	// *** Crucial Logic: Preserve ConfigError ***
	// If the current state IS ConfigError, only allow transition to Running.
	// Otherwise, update the state normally.
	if originalStatus.State == humiov1alpha1.HumioClusterStateConfigError {
		if calculatedState == humiov1alpha1.HumioClusterStateRunning {
			desiredStatus.State = humiov1alpha1.HumioClusterStateRunning // Allow transition out of ConfigError only if Running
			log.Info("Transitioning from ConfigError to Running")
		} else {
			desiredStatus.State = humiov1alpha1.HumioClusterStateConfigError // Preserve ConfigError
			log.V(1).Info("Preserving ConfigError state", "CalculatedState", calculatedState)
		}
	} else {
		// If not currently in ConfigError, update state normally
		desiredStatus.State = calculatedState
	}

	// Only update if the status has actually changed
	if !reflect.DeepEqual(*originalStatus, *desiredStatus) {
		log.Info("updating HumioPdfRenderService status", "OldState", originalStatus.State, "NewState", desiredStatus.State, "ReadyReplicas", desiredStatus.ReadyReplicas, "Nodes", desiredStatus.Nodes)
		hprs.Status = *desiredStatus // Apply the calculated status to the object to be updated
		if err := r.Status().Update(ctx, hprs); err != nil {
			log.Error(err, "failed to update HumioPdfRenderService status")
			return err // Return error to potentially trigger requeue
		}
		log.Info("Successfully updated HumioPdfRenderService status")
	} else {
		log.V(1).Info("HumioPdfRenderService status already up-to-date")
	}

	return nil // Status update successful or no change needed
}

// constructDesiredDeployment builds the desired Deployment object based on the HumioPdfRenderService spec.
func (r *HumioPdfRenderServiceReconciler) constructDesiredDeployment(hprs *humiov1alpha1.HumioPdfRenderService) *appsv1.Deployment {
	labels := labelsForHumioPdfRenderService(hprs.Name)
	annotations := make(map[string]string)
	for k, v := range hprs.Spec.Annotations {
		annotations[k] = v
	}

	// Use the Replicas value directly from the spec (it's int32, not pointer)
	replicas := hprs.Spec.Replicas
	// DeploymentSpec.Replicas expects *int32, so we take the address.
	replicasPtr := &replicas

	image := hprs.Spec.Image
	port := hprs.Spec.Port
	if port == 0 {
		port = DefaultPdfRenderServicePort // Use default if not specified in spec (though CRD has default)
	}

	// Use EnvironmentVariables field name
	envVars := make([]corev1.EnvVar, len(hprs.Spec.EnvironmentVariables))
	copy(envVars, hprs.Spec.EnvironmentVariables)

	tlsEnabled := hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled
	var volumes []corev1.Volume
	var volumeMounts []corev1.VolumeMount
	containerPortName := "http" // Default port name

	if tlsEnabled {
		// Use derived secret name
		secretName := fmt.Sprintf("%s-certificate", hprs.Name)
		containerPortName = "https" // Change port name for TLS

		envVars = append(envVars, corev1.EnvVar{
			Name:  pdfRenderUseTLSEnvVar,
			Value: "true",
		}, corev1.EnvVar{
			Name:  pdfRenderTLSKeyPathEnvVar, // Use new env var name
			Value: fmt.Sprintf("%s/%s", pdfTLSCertMountPath, corev1.TLSCertKey),
		}, corev1.EnvVar{
			Name:  pdfRenderTLSKeyPathEnvVar, // Use new env var name
			Value: fmt.Sprintf("%s/%s", pdfTLSCertMountPath, corev1.TLSPrivateKeyKey),
		}, corev1.EnvVar{ // Add CA file env var
			Name:  pdfRenderCAFileEnvVar,
			Value: fmt.Sprintf("%s/%s", pdfTLSCertMountPath, "ca.crt"), // Point to ca.crt within the mount
		})

		volumes = append(volumes, corev1.Volume{
			Name: pdfTLSCertVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      pdfTLSCertVolumeName,
			MountPath: pdfTLSCertMountPath, // Use constant for mount path
			ReadOnly:  true,
		})
	}

	volumes = append(volumes, hprs.Spec.Volumes...)
	volumeMounts = append(volumeMounts, hprs.Spec.VolumeMounts...)

	container := corev1.Container{
		Name:            "pdf-render-service",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent, // TODO: Make configurable?
		Ports: []corev1.ContainerPort{{
			ContainerPort: int32(port),
			Name:          containerPortName, // Use dynamic port name
			Protocol:      corev1.ProtocolTCP,
		}},
		Env:             envVars,
		Resources:       hprs.Spec.Resources,
		VolumeMounts:    volumeMounts,
		LivenessProbe:   hprs.Spec.LivenessProbe,   // Use probes from spec
		ReadinessProbe:  hprs.Spec.ReadinessProbe,  // Use probes from spec
		SecurityContext: hprs.Spec.SecurityContext, // Container security context
	}

	podSpec := corev1.PodSpec{
		ServiceAccountName: hprs.Spec.ServiceAccountName,
		Containers:         []corev1.Container{container},
		Volumes:            volumes,
		Affinity:           hprs.Spec.Affinity,
		ImagePullSecrets:   hprs.Spec.ImagePullSecrets,
		SecurityContext:    hprs.Spec.PodSecurityContext, // Pod security context
		// TODO: Add Tolerations, NodeSelector from spec if added later
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.getResourceName(hprs),
			Namespace:   hprs.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicasPtr, // Use pointer to the replicas value
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: annotations,
				},
				Spec: podSpec,
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RollingUpdateDeploymentStrategyType,
			},
		},
	}

	return deployment
}

// reconcileDeployment ensures the Deployment for the HumioPdfRenderService exists and matches the desired state.
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) (*appsv1.Deployment, error) {
	log := log.FromContext(ctx)
	desiredDeployment := r.constructDesiredDeployment(hprs)

	if err := controllerutil.SetControllerReference(hprs, desiredDeployment, r.Scheme); err != nil {
		return nil, r.logErrorAndReturn(err, "Failed to set controller reference on Deployment")
	}

	foundDeployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: desiredDeployment.Name, Namespace: desiredDeployment.Namespace}, foundDeployment)
	if err != nil && k8serrors.IsNotFound(err) {
		log.Info("Creating a new Deployment", "Deployment.Namespace", desiredDeployment.Namespace, "Deployment.Name", desiredDeployment.Name)
		err = r.Create(ctx, desiredDeployment)
		if err != nil {
			return nil, r.logErrorAndReturn(err, "Failed to create new Deployment")
		}
		return desiredDeployment, nil
	} else if err != nil {
		return nil, r.logErrorAndReturn(err, "Failed to get Deployment")
	}

	// Deployment exists, check for updates
	log.V(1).Info("Deployment already exists, checking for updates", "Deployment.Name", foundDeployment.Name)

	needsUpdate := false

	// Check top-level metadata (Annotations)
	if r.checkMetadataChanges(foundDeployment, desiredDeployment) {
		log.Info("Deployment metadata changes detected", "Deployment.Name", foundDeployment.Name)
		r.updateDeploymentMetadata(foundDeployment, desiredDeployment)
		needsUpdate = true
	}

	// Check deployment spec (Replicas, PodTemplateSpec)
	if r.checkDeploymentSpecChanges(foundDeployment, desiredDeployment) {
		log.Info("Deployment spec changes detected", "Deployment.Name", foundDeployment.Name)
		r.updateDeploymentSpec(foundDeployment, desiredDeployment)
		needsUpdate = true
	}

	if needsUpdate {
		log.Info("Updating existing Deployment", "Deployment.Namespace", foundDeployment.Namespace, "Deployment.Name", foundDeployment.Name)
		err = r.Update(ctx, foundDeployment)
		if err != nil {
			return nil, r.logErrorAndReturn(err, "Failed to update Deployment")
		}
		log.Info("Deployment updated successfully", "Deployment.Name", foundDeployment.Name)
		return foundDeployment, nil
	}

	log.V(1).Info("No changes detected for existing Deployment", "Deployment.Name", foundDeployment.Name)
	return foundDeployment, nil
}

// constructDesiredService builds the desired Service object based on the HumioPdfRenderService spec.
func (r *HumioPdfRenderServiceReconciler) constructDesiredService(hprs *humiov1alpha1.HumioPdfRenderService) *corev1.Service {
	labels := labelsForHumioPdfRenderService(hprs.Name)
	// Use hprs.Spec.Annotations for service annotations as ServiceAnnotations field doesn't exist
	annotations := make(map[string]string)
	for k, v := range hprs.Spec.Annotations {
		annotations[k] = v
	}

	port := hprs.Spec.Port
	if port == 0 {
		port = DefaultPdfRenderServicePort // Use default if not specified (though CRD has default)
	}

	serviceType := hprs.Spec.ServiceType
	if serviceType == "" {
		serviceType = corev1.ServiceTypeClusterIP // Default to ClusterIP if empty
	}

	tlsEnabled := hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled
	servicePortName := "http" // Default port name
	if tlsEnabled {
		servicePortName = "https" // Change port name for TLS
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.getResourceName(hprs), // Use same name as deployment
			Namespace:   hprs.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels, // Select pods with matching labels
			Ports: []corev1.ServicePort{{
				Name:     servicePortName, // Use dynamic port name
				Port:     int32(port),
				Protocol: corev1.ProtocolTCP,
				// TargetPort defaults to Port
			}},
			Type: serviceType,
		},
	}
	return service
}

// reconcileService ensures the Service for the HumioPdfRenderService exists and matches the desired state using CreateOrUpdate.
func (r *HumioPdfRenderServiceReconciler) reconcileService(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	log := log.FromContext(ctx)
	desiredService := r.constructDesiredService(hprs)
	serviceName := desiredService.Name

	// Create a function that encapsulates the logic to mutate the object to the desired state.
	mutateFn := func() error {
		// Set the controller reference.
		if err := controllerutil.SetControllerReference(hprs, desiredService, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference on Service: %w", err)
		}
		// Update the spec fields from the desired state.
		// CreateOrUpdate handles merging metadata like labels/annotations.
		// We need to ensure the Spec is updated correctly.
		currentSpec := r.constructDesiredService(hprs).Spec // Get the truly desired spec
		desiredService.Spec.Ports = currentSpec.Ports       // Update Ports
		desiredService.Spec.Selector = currentSpec.Selector // Update Selector
		desiredService.Spec.Type = currentSpec.Type         // Update Type
		// Update other relevant Spec fields if necessary

		log.V(1).Info("Applying desired state to Service inside CreateOrUpdate", "Service.Name", serviceName)
		return nil
	}

	// Use controllerutil.CreateOrUpdate.
	opResult, err := controllerutil.CreateOrUpdate(ctx, r.Client, desiredService, mutateFn)

	if err != nil {
		return r.logErrorAndReturn(err, fmt.Sprintf("Failed to reconcile Service %s", serviceName))
	}

	if opResult != controllerutil.OperationResultNone {
		log.Info("Service reconciled", "Service.Name", serviceName, "Operation", opResult)
	} else {
		log.V(1).Info("Service already in desired state", "Service.Name", serviceName)
	}
	return nil
}

// checkEnvVarChanges compares the environment variables of the first container.
func (r *HumioPdfRenderServiceReconciler) checkEnvVarChanges(existingPodSpec *corev1.PodSpec, desiredPodSpec *corev1.PodSpec) bool {
	if len(existingPodSpec.Containers) == 0 || len(desiredPodSpec.Containers) == 0 {
		return len(existingPodSpec.Containers) != len(desiredPodSpec.Containers)
	}
	// Use reflect.DeepEqual for simple comparison. Consider sorting for order-independent check if needed.
	return !reflect.DeepEqual(existingPodSpec.Containers[0].Env, desiredPodSpec.Containers[0].Env)
}

// checkProbeChanges compares liveness and readiness probes.
func (r *HumioPdfRenderServiceReconciler) checkProbeChanges(existingPodSpec *corev1.PodSpec, desiredPodSpec *corev1.PodSpec) bool {
	if len(existingPodSpec.Containers) == 0 || len(desiredPodSpec.Containers) == 0 {
		return len(existingPodSpec.Containers) != len(desiredPodSpec.Containers)
	}
	if !reflect.DeepEqual(existingPodSpec.Containers[0].LivenessProbe, desiredPodSpec.Containers[0].LivenessProbe) {
		return true
	}
	if !reflect.DeepEqual(existingPodSpec.Containers[0].ReadinessProbe, desiredPodSpec.Containers[0].ReadinessProbe) {
		return true
	}
	return false
}

// checkServiceAccountChanges compares the ServiceAccountName in the PodSpec.
func (r *HumioPdfRenderServiceReconciler) checkServiceAccountChanges(existingPodSpec *corev1.PodSpec, desiredPodSpec *corev1.PodSpec) bool {
	return existingPodSpec.ServiceAccountName != desiredPodSpec.ServiceAccountName
}

// checkAffinityChanges compares the Affinity rules in the PodSpec.
func (r *HumioPdfRenderServiceReconciler) checkAffinityChanges(existingPodSpec *corev1.PodSpec, desiredPodSpec *corev1.PodSpec) bool {
	return !reflect.DeepEqual(existingPodSpec.Affinity, desiredPodSpec.Affinity)
}

// checkImagePullSecretChanges compares the ImagePullSecrets in the PodSpec.
func (r *HumioPdfRenderServiceReconciler) checkImagePullSecretChanges(existingPodSpec *corev1.PodSpec, desiredPodSpec *corev1.PodSpec) bool {
	return !reflect.DeepEqual(existingPodSpec.ImagePullSecrets, desiredPodSpec.ImagePullSecrets)
}

// checkVolumeMountChanges compares the VolumeMounts of the first container.
func (r *HumioPdfRenderServiceReconciler) checkVolumeMountChanges(existingPodSpec *corev1.PodSpec, desiredPodSpec *corev1.PodSpec) bool {
	if len(existingPodSpec.Containers) == 0 || len(desiredPodSpec.Containers) == 0 {
		return len(existingPodSpec.Containers) != len(desiredPodSpec.Containers)
	}
	// Use reflect.DeepEqual. Consider sorting for order-independent check.
	return !reflect.DeepEqual(existingPodSpec.Containers[0].VolumeMounts, desiredPodSpec.Containers[0].VolumeMounts)
}

// checkVolumeChanges compares the Volumes defined in the PodSpec.
func (r *HumioPdfRenderServiceReconciler) checkVolumeChanges(existingPodSpec *corev1.PodSpec, desiredPodSpec *corev1.PodSpec) bool {
	// Use reflect.DeepEqual. Consider sorting for order-independent check.
	return !reflect.DeepEqual(existingPodSpec.Volumes, desiredPodSpec.Volumes)
}

// checkSecurityContextChanges compares the SecurityContext (Pod and Container).
func (r *HumioPdfRenderServiceReconciler) checkSecurityContextChanges(existingPodSpec *corev1.PodSpec, desiredPodSpec *corev1.PodSpec) bool {
	if !reflect.DeepEqual(existingPodSpec.SecurityContext, desiredPodSpec.SecurityContext) {
		return true // PodSecurityContext differs
	}
	if len(existingPodSpec.Containers) == 0 || len(desiredPodSpec.Containers) == 0 {
		return len(existingPodSpec.Containers) != len(desiredPodSpec.Containers)
	}
	if !reflect.DeepEqual(existingPodSpec.Containers[0].SecurityContext, desiredPodSpec.Containers[0].SecurityContext) {
		return true // Container SecurityContext differs
	}
	return false
}

// checkPodTemplateAnnotationChanges compares annotations on the PodTemplateSpec.
func (r *HumioPdfRenderServiceReconciler) checkPodTemplateAnnotationChanges(existingPodTemplate *corev1.PodTemplateSpec, desiredPodTemplate *corev1.PodTemplateSpec) bool {
	// Use DeepEqual for simplicity. Assumes desired state is the source of truth for managed annotations.
	return !reflect.DeepEqual(existingPodTemplate.Annotations, desiredPodTemplate.Annotations)
}

// checkDeploymentSpecChanges checks for differences in key fields of the DeploymentSpec and its PodTemplateSpec.
func (r *HumioPdfRenderServiceReconciler) checkDeploymentSpecChanges(existingDeployment *appsv1.Deployment, desiredDeployment *appsv1.Deployment) bool {
	log := r.Log.WithValues("Deployment.Name", existingDeployment.Name)

	// Compare Replicas pointer values
	if existingDeployment.Spec.Replicas == nil || desiredDeployment.Spec.Replicas == nil {
		if existingDeployment.Spec.Replicas != desiredDeployment.Spec.Replicas {
			log.Info("Change detected: Replicas (nil mismatch)")
			return true
		}
	} else if *existingDeployment.Spec.Replicas != *desiredDeployment.Spec.Replicas {
		log.Info("Change detected: Replicas value", "Existing", *existingDeployment.Spec.Replicas, "Desired", *desiredDeployment.Spec.Replicas)
		return true
	}

	existingPodSpec := &existingDeployment.Spec.Template.Spec
	desiredPodSpec := &desiredDeployment.Spec.Template.Spec

	// Container count (basic check)
	if len(existingPodSpec.Containers) != len(desiredPodSpec.Containers) {
		log.Info("Change detected: Container count", "Existing", len(existingPodSpec.Containers), "Desired", len(desiredPodSpec.Containers))
		return true
	}
	if len(existingPodSpec.Containers) == 0 { // If both are 0, skip container checks
		// Check other pod spec fields directly if needed
	} else {
		// Compare fields of the first container (assuming one container)
		existingContainer := &existingPodSpec.Containers[0]
		desiredContainer := &desiredPodSpec.Containers[0]

		if existingContainer.Image != desiredContainer.Image {
			log.Info("Change detected: Container Image", "Existing", existingContainer.Image, "Desired", desiredContainer.Image)
			return true
		}
		if !reflect.DeepEqual(existingContainer.Ports, desiredContainer.Ports) {
			log.Info("Change detected: Container Ports")
			return true
		}
		if !reflect.DeepEqual(existingContainer.Resources, desiredContainer.Resources) {
			log.Info("Change detected: Container Resources")
			return true
		}
	}

	// Use granular checks for specific pod spec fields
	if r.checkEnvVarChanges(existingPodSpec, desiredPodSpec) {
		log.Info("Change detected: Env Vars")
		return true
	}
	if r.checkVolumeChanges(existingPodSpec, desiredPodSpec) {
		log.Info("Change detected: Volumes")
		return true
	}
	if r.checkVolumeMountChanges(existingPodSpec, desiredPodSpec) {
		log.Info("Change detected: Volume Mounts")
		return true
	}
	if r.checkAffinityChanges(existingPodSpec, desiredPodSpec) {
		log.Info("Change detected: Affinity")
		return true
	}
	if r.checkSecurityContextChanges(existingPodSpec, desiredPodSpec) {
		log.Info("Change detected: Security Context")
		return true
	}
	if r.checkImagePullSecretChanges(existingPodSpec, desiredPodSpec) {
		log.Info("Change detected: Image Pull Secrets")
		return true
	}
	if r.checkServiceAccountChanges(existingPodSpec, desiredPodSpec) {
		log.Info("Change detected: Service Account")
		return true
	}
	if r.checkProbeChanges(existingPodSpec, desiredPodSpec) {
		log.Info("Change detected: Probes")
		return true
	}

	// Check Pod Template Annotations and Labels
	if r.checkPodTemplateAnnotationChanges(&existingDeployment.Spec.Template, &desiredDeployment.Spec.Template) {
		log.Info("Change detected: Pod Template Annotations")
		return true
	}
	if !reflect.DeepEqual(existingDeployment.Spec.Template.Labels, desiredDeployment.Spec.Template.Labels) {
		log.Info("Change detected: Pod Template Labels")
		return true
	}

	return false // No relevant spec changes detected
}

// updateDeploymentSpec updates the existing Deployment's Spec based on the desired state.
func (r *HumioPdfRenderServiceReconciler) updateDeploymentSpec(existingDeployment *appsv1.Deployment, desiredDeployment *appsv1.Deployment) {
	// Update replicas
	existingDeployment.Spec.Replicas = desiredDeployment.Spec.Replicas

	// Update the entire PodTemplateSpec (simpler than field-by-field)
	existingDeployment.Spec.Template.Spec = desiredDeployment.Spec.Template.Spec

	// Update Pod Template Metadata (Annotations, Labels)
	existingDeployment.Spec.Template.ObjectMeta.Annotations = desiredDeployment.Spec.Template.ObjectMeta.Annotations
	existingDeployment.Spec.Template.ObjectMeta.Labels = desiredDeployment.Spec.Template.ObjectMeta.Labels

	// Update Deployment Strategy if needed (usually static)
	existingDeployment.Spec.Strategy = desiredDeployment.Spec.Strategy
}

// checkMetadataChanges compares top-level Deployment metadata (Annotations).
func (r *HumioPdfRenderServiceReconciler) checkMetadataChanges(existingDeployment *appsv1.Deployment, desiredDeployment *appsv1.Deployment) bool {
	// Use DeepEqual for annotations. Assumes desired state is the source of truth for managed annotations.
	if !reflect.DeepEqual(existingDeployment.Annotations, desiredDeployment.Annotations) {
		r.Log.Info("Change detected: Deployment Annotations")
		return true
	}
	// Labels are usually immutable or tied to selector, avoid direct comparison unless necessary.
	return false
}

// updateDeploymentMetadata updates the existing Deployment's top-level metadata (Annotations).
func (r *HumioPdfRenderServiceReconciler) updateDeploymentMetadata(existingDeployment *appsv1.Deployment, desiredDeployment *appsv1.Deployment) {
	// Overwrite existing annotations with the desired ones.
	existingDeployment.Annotations = desiredDeployment.Annotations
	// If nil, make it an empty map to avoid issues
	if existingDeployment.Annotations == nil {
		existingDeployment.Annotations = make(map[string]string)
	}
}

// Helper function to generate labels for resources.
func labelsForHumioPdfRenderService(name string) map[string]string {
	// Use the CR name directly for the instance label
	return map[string]string{
		"app.kubernetes.io/name":       "HumioPdfRenderService",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/component":  "pdf-render-service",
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPdfRenderService{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}).
		Named("humiopdfrenderservice").
		Complete(r)
}
