package controller

import (
	"context"
	"fmt"
	"reflect" // Added import
	"sort"
	"time"

	"strings"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry" // Added import
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
)

const (
	// Service defaults
	DefaultPdfRenderServicePort = 5123

	// TLS‑related env‑vars
	pdfRenderUseTLSEnvVar      = "HUMIO_PDF_RENDER_USE_TLS"
	pdfRenderTLSCertPathEnvVar = "HUMIO_PDF_RENDER_TLS_CERT_PATH"
	pdfRenderTLSKeyPathEnvVar  = "HUMIO_PDF_RENDER_TLS_KEY_PATH"
	pdfRenderCAFileEnvVar      = "HUMIO_PDF_RENDER_CA_FILE"

	// TLS volume / mount
	pdfTLSCertMountPath  = "/etc/humio-pdf-render-service/tls"
	pdfTLSCertVolumeName = "humio-pdf-render-service-tls"

	// Finalizer applied to HumioPdfRenderService resources
	hprsFinalizer = "core.humio.com/finalizer"

	// All child resources are named <cr-name>-pdf-render-service
	childSuffix = "-pdf-render-service"
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

// Reconcile implements the reconciliation logic for HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, reconcileErr error) {
	r.Log = log.FromContext(ctx)

	hprs := &humiov1alpha1.HumioPdfRenderService{}
	if err := r.Get(ctx, req.NamespacedName, hprs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Defer status update to ensure it's always called.
	// The hprs object passed to updateStatus will be the one from the start of the reconcile loop (or fetched for finalizer removal).
	// reconcileErr and result are captured by the defer.
	defer func() {
		// Fetch the latest HPRS to get its current status for comparison and to ensure we have the latest resource version for update.
		// However, the observedGeneration should be based on the hprs.Generation that this reconcile loop processed.
		latestHprsForStatusUpdate := &humiov1alpha1.HumioPdfRenderService{}
		getErr := r.Get(ctx, req.NamespacedName, latestHprsForStatusUpdate)
		if getErr != nil {
			if client.IgnoreNotFound(getErr) != nil {
				r.Log.Error(getErr, "failed to get latest HumioPdfRenderService for status update")
			}
			// If the resource is gone, no need to update status.
			return
		}

		// If an error occurred during reconciliation, reflect it in the status message.
		// The state (e.g. ConfigError) should have been set on the hprs object within the main reconcile logic.
		// We pass the hprs object from the main reconcile loop (which has the intended state)
		// and the reconcileErr to updateStatus.
		// updateStatus will then use hprs.Status.State and hprs.Generation.
		currentReconcileState := hprs.Status.State // State determined by main reconcile logic
		if reconcileErr != nil && currentReconcileState != humiov1alpha1.HumioPdfRenderServiceStateConfigError && currentReconcileState != humiov1alpha1.HumioPdfRenderServiceStateError {
			// If a reconcile error occurred and we are not already in a specific error state,
			// we might want to set a generic error state.
			// For now, updateStatus will primarily use reconcileErr for the message.
			// The state itself should be set appropriately by the main logic before this defer runs.
			// If reconcileErr is not nil, updateStatus will set the message.
		}

		// Pass the original hprs object (containing the generation processed and desired state)
		// and the captured reconcileErr to updateStatus.
		if _, updateErr := r.updateStatus(ctx, hprs, currentReconcileState, reconcileErr); updateErr != nil {
			r.Log.Error(updateErr, "failed to update HumioPdfRenderService status")
			// If status update fails, ensure we requeue if not already doing so.
			if reconcileErr == nil && result.RequeueAfter == 0 && !result.Requeue {
				result = ctrl.Result{Requeue: true} // Override result if status update failed
			}
		}
	}()

	// -----------------------------------------------------------------------
	// 0. Short-circuit validation (TLS secret etc.)
	// -----------------------------------------------------------------------
	if err := r.validateTLSConfiguration(ctx, hprs); err != nil {
		reconcileErr = err
		hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		return ctrl.Result{Requeue: true}, reconcileErr
	}

	// -----------------------------------------------------------------------
	// 1. Handle deletion
	// -----------------------------------------------------------------------
	if !hprs.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(hprs, hprsFinalizer) {
			if err := r.cleanupOwnedResources(ctx, hprs); err != nil {
				r.Log.Error(err, "Error cleaning up owned resources during finalization")
				reconcileErr = err
				return ctrl.Result{Requeue: true}, reconcileErr
			}
			controllerutil.RemoveFinalizer(hprs, hprsFinalizer)
			// Use exponential backoff for finalizer removal
			if err := wait.ExponentialBackoff(retry.DefaultBackoff, func() (bool, error) {
				// Fetch the latest version for update
				currentHprsForFinalizer := &humiov1alpha1.HumioPdfRenderService{}
				if getErr := r.Get(ctx, req.NamespacedName, currentHprsForFinalizer); getErr != nil {
					if k8serrors.IsNotFound(getErr) { // CR deleted by another actor
						return true, nil // Stop retrying, already gone
					}
					return false, fmt.Errorf("failed to get latest HPRS for finalizer removal: %w", getErr)
				}
				// Ensure finalizer is still present before attempting removal on the fetched object
				if !controllerutil.ContainsFinalizer(currentHprsForFinalizer, hprsFinalizer) {
					return true, nil // Finalizer already removed
				}
				controllerutil.RemoveFinalizer(currentHprsForFinalizer, hprsFinalizer)
				updateErr := r.Update(ctx, currentHprsForFinalizer)
				if updateErr == nil {
					return true, nil // Success
				}
				if k8serrors.IsConflict(updateErr) {
					r.Log.Info("Conflict removing finalizer, will retry", "error", updateErr)
					return false, nil // Retry on conflict
				}
				return false, fmt.Errorf("failed to remove finalizer: %w", updateErr) // Non-conflict error
			}); err != nil {
				r.Log.Error(err, "Error removing finalizer during termination after retries")
				reconcileErr = err
				return ctrl.Result{Requeue: true}, reconcileErr
			}
		}
		return ctrl.Result{}, nil // Successfully processed deletion or finalizer already gone
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(hprs, hprsFinalizer) {
		controllerutil.AddFinalizer(hprs, hprsFinalizer)
		if err := r.Update(ctx, hprs); err != nil {
			reconcileErr = fmt.Errorf("failed to add finalizer: %w", err)
			return ctrl.Result{}, reconcileErr
		}
	}

	// 2. Reconcile children
	dep, err := r.reconcileDeployment(ctx, hprs)
	if err != nil {
		reconcileErr = fmt.Errorf("failed to reconcile deployment: %w", err)
		hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		return ctrl.Result{Requeue: true}, reconcileErr
	}
	if err = r.reconcileService(ctx, hprs); err != nil {
		reconcileErr = fmt.Errorf("failed to reconcile service: %w", err)
		hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		return ctrl.Result{Requeue: true}, reconcileErr
	}

	// 3. Determine HPRS state based on Deployment status
	determinedState := humiov1alpha1.HumioPdfRenderServiceStateUnknown // Default to unknown initially
	if hprs.Spec.Replicas == 0 {
		if dep != nil && dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0 &&
			dep.Status.ObservedGeneration == dep.Generation &&
			dep.Status.Replicas == 0 && dep.Status.UpdatedReplicas == 0 && dep.Status.ReadyReplicas == 0 && dep.Status.AvailableReplicas == 0 {
			determinedState = humiov1alpha1.HumioPdfRenderServiceStateScaledDown
		} else if dep == nil { // No deployment exists, and spec is 0 replicas
			determinedState = humiov1alpha1.HumioPdfRenderServiceStateScaledDown
		} else { // Spec is 0, but deployment not fully scaled down or absent
			determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		}
	} else if dep == nil { // Should not happen if reconcileDeployment was successful and replicas > 0
		r.Log.Info("Deployment not found, but expected for HPRS", "hprsName", hprs.Name)
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		reconcileErr = fmt.Errorf("deployment %s not found, expected for HPRS %s", childName(hprs), hprs.Name)
	} else if dep.Status.ObservedGeneration < dep.Generation {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	} else if dep.Spec.Replicas != nil && dep.Status.UpdatedReplicas < *dep.Spec.Replicas {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	} else if dep.Spec.Replicas != nil && dep.Status.ReadyReplicas < *dep.Spec.Replicas {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	} else if dep.Spec.Replicas != nil && dep.Status.AvailableReplicas < *dep.Spec.Replicas {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	} else if dep.Spec.Replicas != nil && dep.Status.ReadyReplicas == *dep.Spec.Replicas &&
		dep.Status.ObservedGeneration == dep.Generation &&
		hprs.Spec.Replicas > 0 {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateRunning
	} else { // Default to configuring if no other state matches or if replicas > 0 but not fully ready.
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	}
	hprs.Status.State = determinedState // Update the hprs object's status field for the deferred updateStatus call

	// 4. Requeue logic based on state
	if determinedState == humiov1alpha1.HumioPdfRenderServiceStateConfiguring {
		r.Log.Info("Requeuing: HPRS is in Configuring state.", "HPRSName", hprs.Name)
		result = ctrl.Result{RequeueAfter: 10 * time.Second}
		return result, reconcileErr
	}

	r.Log.Info("Reconciliation complete for HPRS.", "HPRSName", hprs.Name, "State", determinedState)
	return ctrl.Result{}, reconcileErr
}

// reconcileDeployment reconciles the Deployment for the HumioPdfRenderService.
// It creates or updates the Deployment based on the desired state.
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) (*appsv1.Deployment, error) {
	log := log.FromContext(ctx)
	desired := r.constructDesiredDeployment(hprs)

	var dep appsv1.Deployment
	dep.Name, dep.Namespace = desired.Name, desired.Namespace

	// Correct mutate function for Deployment
	mutate := func() error {
		// Copy ObjectMeta fields
		dep.Labels = desired.Labels
		dep.Annotations = desired.Annotations

		// Copy relevant Spec fields from desired Deployment
		dep.Spec.Replicas = desired.Spec.Replicas
		dep.Spec.Selector = desired.Spec.Selector
		// Copy the template spec
		dep.Spec.Template = desired.Spec.Template

		// Set owner reference on the Deployment itself
		return controllerutil.SetControllerReference(hprs, &dep, r.Scheme)
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &dep, mutate)
	if err != nil {
		return nil, fmt.Errorf("create/update Deployment: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Deployment reconciled", "operation", op)
	}
	return &dep, nil
}

// reconcileService reconciles the Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileService(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	log := log.FromContext(ctx)
	desired := r.constructDesiredService(hprs)

	var svc corev1.Service
	svc.Name, svc.Namespace = desired.Name, desired.Namespace

	mutateFn := func() error {
		currentClusterIP := svc.Spec.ClusterIP // Preserve existing ClusterIP

		// Copy ObjectMeta fields
		svc.Labels = desired.Labels
		// Preserve existing annotations and add/override new ones from desired
		if svc.Annotations == nil {
			svc.Annotations = make(map[string]string)
		}
		for k, v := range desired.Annotations { // desired.Annotations comes from hprs.Spec.Annotations
			svc.Annotations[k] = v
		}

		// Copy relevant Spec fields from desired Service
		svc.Spec.Ports = desired.Spec.Ports
		svc.Spec.Selector = desired.Spec.Selector // Selector is based on hprs.Name, should be stable
		svc.Spec.Type = desired.Spec.Type
		if svc.Spec.Type == "" {
			svc.Spec.Type = corev1.ServiceTypeClusterIP
		}

		// If the type is ClusterIP, and we had an existing ClusterIP (and it's not "None"), preserve it.
		// CreateOrUpdate will clear ClusterIP if it's "None" and type is ClusterIP.
		// If the service is being created (svc.ObjectMeta.ResourceVersion == ""), ClusterIP will be assigned by Kubernetes.
		// If it's an update and currentClusterIP was valid, we re-assign it.
		if svc.Spec.Type == corev1.ServiceTypeClusterIP {
			if svc.ObjectMeta.ResourceVersion != "" && currentClusterIP != "" && currentClusterIP != "None" {
				svc.Spec.ClusterIP = currentClusterIP
			} else {
				// On creation, or if currentClusterIP was "None", let Kubernetes assign it or leave it empty for "None".
				// For "None", explicitly setting svc.Spec.ClusterIP = "None" might be needed if not handled by CreateOrUpdate.
				// However, controllerutil.CreateOrUpdate should handle this correctly.
			}
		} else {
			// For other service types (LoadBalancer, NodePort), ClusterIP might still be assigned.
			// We don't need to manage it explicitly here; Kubernetes handles it.
		}

		return controllerutil.SetControllerReference(hprs, &svc, r.Scheme)
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &svc, mutateFn)
	if err != nil {
		return fmt.Errorf("create/update Service %s: %w", desired.Name, err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Service reconciled", "serviceName", svc.Name, "operation", op)
	}
	return nil
}

/// ...existing imports...
// ...existing code...

// ─────────────────────────────────────────────────────────────────────────────
// Desired-object helpers
// ─────────────────────────────────────────────────────────────────────────────
func (r *HumioPdfRenderServiceReconciler) constructDesiredDeployment(
	hprs *humiov1alpha1.HumioPdfRenderService,
) *appsv1.Deployment {

	labels := labelsForHumioPdfRenderService(hprs.Name)
	replicas := hprs.Spec.Replicas
	port := servicePort(hprs)

	// Build container (+ env / mounts / volumes incl. TLS)
	envVars, vols, mounts := r.buildRuntimeAssets(hprs, port)
	container := r.buildPDFContainer(hprs, port, envVars, mounts)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        childName(hprs),
			Namespace:   hprs.Namespace,
			Labels:      labels,
			Annotations: hprs.Spec.Annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: hprs.Spec.Annotations,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: hprs.Spec.ServiceAccountName,
					Affinity:           hprs.Spec.Affinity,
					ImagePullSecrets:   hprs.Spec.ImagePullSecrets,
					SecurityContext:    hprs.Spec.PodSecurityContext,
					Containers:         []corev1.Container{container},
					Volumes:            vols,
				},
			},
		},
	}
}

// servicePort returns the port the PDF service should listen on.
func servicePort(hprs *humiov1alpha1.HumioPdfRenderService) int32 {
	if hprs.Spec.Port > 0 {
		return hprs.Spec.Port
	}
	return DefaultPdfRenderServicePort
}

// buildRuntimeAssets returns fully-deduplicated env vars, volumes and mounts
// including (optional) TLS assets.
func (r *HumioPdfRenderServiceReconciler) buildRuntimeAssets(
	hprs *humiov1alpha1.HumioPdfRenderService,
	port int32,
) (env []corev1.EnvVar, vols []corev1.Volume, mounts []corev1.VolumeMount) {

	// 1. env
	env = append([]corev1.EnvVar(nil), hprs.Spec.EnvironmentVariables...)

	// 2. volumes / mounts – user-supplied
	vols = append([]corev1.Volume(nil), hprs.Spec.Volumes...)
	mounts = append([]corev1.VolumeMount(nil), hprs.Spec.VolumeMounts...)

	// 3. TLS additions (in-place append on env slice)
	tlsVols, tlsMounts := r.tlsVolumesAndMounts(hprs, &env)
	vols = dedupVolumes(append(vols, tlsVols...))
	mounts = dedupVolumeMounts(append(mounts, tlsMounts...))

	// 4. deterministic ordering
	env = sortEnv(env)

	return env, vols, mounts
}

// buildPDFContainer constructs the single container for the Deployment.
func (r *HumioPdfRenderServiceReconciler) buildPDFContainer(
	hprs *humiov1alpha1.HumioPdfRenderService,
	port int32,
	env []corev1.EnvVar,
	mounts []corev1.VolumeMount,
) corev1.Container {
	// Decide probe scheme
	scheme := corev1.URISchemeHTTP
	if hprs.Spec.TLS != nil && helpers.BoolTrue(hprs.Spec.TLS.Enabled) {
		scheme = corev1.URISchemeHTTPS
	}
	defProbe := func(path string) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   path,
					Port:   intstr.FromInt(int(port)),
					Scheme: scheme,
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       10,
		}
	}
	lProbe := firstNonNilProbe(
		hprs.Spec.LivenessProbe,
		defProbe(humiov1alpha1.DefaultPdfRenderServiceLiveness),
	)
	rProbe := firstNonNilProbe(
		hprs.Spec.ReadinessProbe,
		defProbe(humiov1alpha1.DefaultPdfRenderServiceReadiness),
	)

	// Ensure resources are properly set and never nil
	resources := hprs.Spec.Resources
	if resources.Limits == nil {
		resources.Limits = corev1.ResourceList{}
	}
	if resources.Requests == nil {
		resources.Requests = corev1.ResourceList{}
	}

	r.Log.Info("Creating container with resources",
		"memoryRequests", resources.Requests.Memory(),
		"cpuRequests", resources.Requests.Cpu(),
		"memoryLimits", resources.Limits.Memory(),
		"cpuLimits", resources.Limits.Cpu())

	return corev1.Container{
		Name:            "pdf-render-service",
		Image:           hprs.Spec.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             env,
		Ports: []corev1.ContainerPort{{
			Name:          "http",
			ContainerPort: port,
		}},
		VolumeMounts:    mounts,
		LivenessProbe:   lProbe,
		ReadinessProbe:  rProbe,
		Resources:       resources,
		SecurityContext: hprs.Spec.SecurityContext,
	}
}

// constructDesiredService builds the desired Service object for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDesiredService(hprs *humiov1alpha1.HumioPdfRenderService) *corev1.Service {
	labels := labelsForHumioPdfRenderService(hprs.Name)
	port := hprs.Spec.Port
	if port == 0 {
		port = DefaultPdfRenderServicePort
	}

	// when TLS is on, name the port "https"
	protocol := "http"
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
		protocol = "https"
	}

	serviceType := hprs.Spec.ServiceType
	if serviceType == "" {
		serviceType = corev1.ServiceTypeClusterIP
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        childName(hprs),
			Namespace:   hprs.Namespace,
			Labels:      labels,
			Annotations: hprs.Spec.Annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:     serviceType, // Use the determined service type
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       protocol,
				Port:       int32(port),
				TargetPort: intstr.FromInt(int(port)),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

// tlsVolumesAndMounts builds TLS related env‑vars / volumes / mounts (if enabled)
func (r *HumioPdfRenderServiceReconciler) tlsVolumesAndMounts(hprs *humiov1alpha1.HumioPdfRenderService, env *[]corev1.EnvVar) ([]corev1.Volume, []corev1.VolumeMount) {
	if hprs.Spec.TLS == nil || !helpers.BoolTrue(hprs.Spec.TLS.Enabled) { // Use helper for nil-safe bool check
		return nil, nil
	}

	// Secret name for PDF render service's own certificate is derived, not from hprs.Spec.TLS.SecretName
	secretName := fmt.Sprintf("%s-certificate", childName(hprs))

	*env = append(*env,
		corev1.EnvVar{Name: pdfRenderUseTLSEnvVar, Value: "true"},
		corev1.EnvVar{Name: pdfRenderTLSCertPathEnvVar, Value: fmt.Sprintf("%s/%s", pdfTLSCertMountPath, corev1.TLSCertKey)},
		corev1.EnvVar{Name: pdfRenderTLSKeyPathEnvVar, Value: fmt.Sprintf("%s/%s", pdfTLSCertMountPath, corev1.TLSPrivateKeyKey)},
		corev1.EnvVar{Name: pdfRenderCAFileEnvVar, Value: fmt.Sprintf("%s/ca.crt", pdfTLSCertMountPath)},
	)

	vol := corev1.Volume{
		Name: pdfTLSCertVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: secretName},
		},
	}
	mnt := corev1.VolumeMount{
		Name:      pdfTLSCertVolumeName,
		MountPath: pdfTLSCertMountPath,
		ReadOnly:  true,
	}
	return []corev1.Volume{vol}, []corev1.VolumeMount{mnt}
}

func (r *HumioPdfRenderServiceReconciler) setStatus(
	ctx context.Context,
	hprs *humiov1alpha1.HumioPdfRenderService,
	state string,
	ready int32,
	pods []string,
	reconcileErr error,
) error {
	newObservedGeneration := hprs.Generation

	r.Log.Info("Updating status",
		"state", state,
		"readyReplicas", ready,
		"generation", hprs.Generation,
		"observedGeneration", newObservedGeneration,
	)

	updateStatus := func() error {
		latest := &humiov1alpha1.HumioPdfRenderService{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(hprs), latest); err != nil {
			return err
		}
		latest.Status.State = state
		latest.Status.ReadyReplicas = ready
		latest.Status.Nodes = pods
		latest.Status.ObservedGeneration = newObservedGeneration
		if reconcileErr != nil {
			latest.Status.Message = reconcileErr.Error()
		} else {
			latest.Status.Message = ""
		}
		return r.Status().Update(ctx, latest)
	}

	// retry on resource‐version conflicts
	return wait.ExponentialBackoff(wait.Backoff{
		Steps:    5,
		Duration: 50 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
	}, func() (bool, error) {
		err := updateStatus()
		if err == nil {
			return true, nil
		}
		if k8serrors.IsConflict(err) {
			r.Log.Info("Conflict updating HPRS.status, retrying", "err", err)
			return false, nil
		}
		return false, err
	})
}

// TLS validation
func (r *HumioPdfRenderServiceReconciler) validateTLSConfiguration(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	if hprs.Spec.TLS == nil || !helpers.BoolTrue(hprs.Spec.TLS.Enabled) { // Use helper
		return nil
	}
	// Secret name for PDF render service's own certificate is derived
	secretName := fmt.Sprintf("%s-certificate", childName(hprs))

	if _, err := kubernetes.GetSecret(ctx, r.Client, secretName, hprs.Namespace); err != nil {
		r.Log.Error(err, "TLS secret not found", "secretName", secretName, "namespace", hprs.Namespace)
		return fmt.Errorf("TLS secret %s not found in namespace %s: %w", secretName, hprs.Namespace, err)
	}
	return nil
}

// Misc helpers
func labelsForHumioPdfRenderService(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "HumioPdfRenderService",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/component":  "pdf-render-service",
	}
}

// updateStatus updates the status of the HumioPdfRenderService.
// It is the single point of truth for status updates and is called via defer in Reconcile.
// hprsObj is the CR object from the beginning of the reconcile loop (or fetched for finalizer update),
// containing the Generation that was processed and the State determined by the reconcile logic.
func (r *HumioPdfRenderServiceReconciler) updateStatus(
	ctx context.Context,
	hprsObj *humiov1alpha1.HumioPdfRenderService, // The HPRS object whose spec was reconciled
	determinedState string, // The state determined by the main reconcile loop for hprsObj.Generation
	reconcileErr error, // Any error that occurred during reconciliation of hprsObj.Generation
) (ctrl.Result, error) {
	log := r.Log.WithValues("humiopdfrenderservice", client.ObjectKeyFromObject(hprsObj))

	// Fetch the latest version of HPRS to apply the status update to.
	currentHprs := &humiov1alpha1.HumioPdfRenderService{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(hprsObj), currentHprs); err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("HumioPdfRenderService not found, skipping status update.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get latest HumioPdfRenderService for status update")
		return ctrl.Result{Requeue: true}, err
	}

	// Fetch Deployment to get replica status
	var dep appsv1.Deployment
	depKey := types.NamespacedName{Name: childName(currentHprs), Namespace: currentHprs.Namespace}
	depErr := r.Get(ctx, depKey, &dep)

	readyReplicas := int32(0)
	var podNames []string

	if depErr == nil {
		readyReplicas = dep.Status.ReadyReplicas
		podList := &corev1.PodList{}
		if err := r.List(ctx, podList,
			client.InNamespace(currentHprs.Namespace),
			client.MatchingLabels(labelsForHumioPdfRenderService(currentHprs.Name))); err == nil {
			for _, pod := range podList.Items {
				podNames = append(podNames, pod.Name)
			}
			sort.Strings(podNames) // Sort for consistent comparison
		} else {
			log.V(1).Info("Could not list pods for HPRS", "error", err)
		}
	} else if !k8serrors.IsNotFound(depErr) {
		log.Error(depErr, "Failed to get Deployment for status update, readyReplicas will be 0")
	}

	// Prepare the status update.
	statusChanged := false
	if currentHprs.Status.State != determinedState {
		currentHprs.Status.State = determinedState
		statusChanged = true
	}
	if currentHprs.Status.ReadyReplicas != readyReplicas {
		currentHprs.Status.ReadyReplicas = readyReplicas
		statusChanged = true
	}

	// Sort currentHprs.Status.Nodes before comparison if it's not nil
	if currentHprs.Status.Nodes != nil {
		sort.Strings(currentHprs.Status.Nodes)
	}
	if !reflect.DeepEqual(currentHprs.Status.Nodes, podNames) { // Use reflect.DeepEqual for slice comparison
		currentHprs.Status.Nodes = podNames
		statusChanged = true
	}

	currentMsg := ""
	if reconcileErr != nil {
		currentMsg = reconcileErr.Error()
	}
	if currentHprs.Status.Message != currentMsg {
		currentHprs.Status.Message = currentMsg
		statusChanged = true
	}

	// Set ObservedGeneration to the generation of the spec that was processed.
	if currentHprs.Status.ObservedGeneration != hprsObj.Generation {
		currentHprs.Status.ObservedGeneration = hprsObj.Generation
		statusChanged = true
	}

	if !statusChanged {
		log.V(1).Info("Status unchanged, skipping update.")
		if reconcileErr != nil { // If an error occurred but status didn't change, still return error for potential requeue
			return ctrl.Result{Requeue: true}, reconcileErr
		}
		return ctrl.Result{}, nil
	}

	log.Info("Updating HumioPdfRenderService status",
		"state", currentHprs.Status.State,
		"readyReplicas", currentHprs.Status.ReadyReplicas,
		"observedGeneration", currentHprs.Status.ObservedGeneration,
		"message", currentHprs.Status.Message,
	)

	// Retry on conflict
	updateErr := wait.ExponentialBackoff(retry.DefaultBackoff, func() (bool, error) {
		err := r.Status().Update(ctx, currentHprs)
		if err == nil {
			return true, nil // Update successful
		}
		if k8serrors.IsConflict(err) {
			log.Info("Conflict during status update, retrying...", "error", err)
			// Re-fetch the latest version of HPRS before retrying the update
			if getErr := r.Get(ctx, client.ObjectKeyFromObject(currentHprs), currentHprs); getErr != nil {
				log.Error(getErr, "Failed to re-fetch HumioPdfRenderService during status update conflict")
				return false, getErr // Stop retrying if re-fetch fails
			}
			// Re-apply the desired changes to the re-fetched object's status
			currentHprs.Status.State = determinedState
			currentHprs.Status.ReadyReplicas = readyReplicas
			currentHprs.Status.Nodes = podNames
			currentHprs.Status.Message = currentMsg
			currentHprs.Status.ObservedGeneration = hprsObj.Generation // Ensure this uses the reconciled generation
			return false, nil                                          // Retry the update
		}
		log.Error(err, "Failed to update HumioPdfRenderService status", "nonConflictError", err)
		return false, err // Non-conflict error, stop retrying
	})

	if updateErr != nil {
		return ctrl.Result{Requeue: true}, fmt.Errorf("failed to update HPRS status after retries: %w", updateErr)
	}

	// If reconcileErr was the reason for the update, return it to ensure requeue if necessary.
	if reconcileErr != nil {
		return ctrl.Result{Requeue: true}, reconcileErr
	}

	return ctrl.Result{}, nil
}

// childName returns the name of the child resources (Deployment and Service) for the HumioPdfRenderService.
func childName(hprs *humiov1alpha1.HumioPdfRenderService) string {
	return hprs.Name + childSuffix
}

// Setup
func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPdfRenderService{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}).
		// watch TLS-certificate Secrets named "<CR-name>-certificate"
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				if strings.HasSuffix(o.GetName(), "-certificate") {
					return []reconcile.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: o.GetNamespace(),
							Name:      strings.TrimSuffix(o.GetName(), "-certificate"),
						},
					}}
				}
				return nil
			}),
		).
		Named("humiopdfrenderservice").
		Complete(r)
}

// Example cleanup function (adapt as needed)
func (r *HumioPdfRenderServiceReconciler) cleanupOwnedResources(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	log := log.FromContext(ctx).WithValues("humiopdfrenderservice", client.ObjectKeyFromObject(hprs))
	log.Info("Cleaning up owned resources for HumioPdfRenderService")

	// Resources are managed by OwnerReferences, so explicit deletion is usually not needed.
	// If there were any resources created *without* OwnerReferences,
	// or if a specific cleanup sequence is required before Kubernetes GC kicks in,
	// that logic would go here.
	// For this controller, assuming OwnerReferences are sufficient for Deployment and Service.

	log.Info("Cleanup: Assuming OwnerReferences handle garbage collection of Deployment and Service.")
	return nil
}

// labelsSubset returns true if all key-value pairs in 'sub' are present and equal in 'super'.
func labelsSubset(sub, super map[string]string) bool {
	if sub == nil {
		return true
	}
	if super == nil {
		return false
	}
	for k, v := range sub {
		if superVal, ok := super[k]; !ok || superVal != v {
			return false
		}
	}
	return true
}

// dedupVolumes removes duplicate volumes, keeping the first occurrence of each volume name.
func dedupVolumes(vols []corev1.Volume) []corev1.Volume {
	seen := make(map[string]struct{}, len(vols))
	out := make([]corev1.Volume, 0, len(vols))
	for _, v := range vols {
		if _, ok := seen[v.Name]; ok {
			continue
		}
		seen[v.Name] = struct{}{}
		out = append(out, v)
	}
	return out
}

// dedupVolumeMounts removes duplicate volume mounts, keeping the first occurrence
// of each unique combination of mount name and mount path.
func dedupVolumeMounts(mnts []corev1.VolumeMount) []corev1.VolumeMount {
	seen := make(map[string]struct{}, len(mnts))
	out := make([]corev1.VolumeMount, 0, len(mnts))
	for _, m := range mnts {
		key := m.Name + "|" + m.MountPath
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, m)
	}
	return out
}

// sortEnv returns the slice in deterministic (Name) order.
func sortEnv(env []corev1.EnvVar) []corev1.EnvVar {
	sort.SliceStable(env, func(i, j int) bool { return env[i].Name < env[j].Name })
	return env
}

// firstNonNilProbe returns p if not nil, otherwise fallback.
func firstNonNilProbe(p *corev1.Probe, fallback *corev1.Probe) *corev1.Probe {
	if p != nil {
		return p
	}
	return fallback
}
