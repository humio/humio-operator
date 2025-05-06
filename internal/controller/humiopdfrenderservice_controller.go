package controller

import (
	"context"
	"fmt"
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
	"sigs.k8s.io/controller-runtime/pkg/handler"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	r.Log = log.FromContext(ctx)

	hprs := &humiov1alpha1.HumioPdfRenderService{}
	if err := r.Get(ctx, req.NamespacedName, hprs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// -----------------------------------------------------------------------
	// 0. Short-circuit validation (TLS secret etc.)
	// -----------------------------------------------------------------------
	if err := r.validateTLSConfiguration(ctx, hprs); err != nil {
		return r.updateStatus(ctx, hprs, humiov1alpha1.HumioClusterStateConfigError, err)
	}

	const finalizer = "core.humio.com/finalizer"

	// -----------------------------------------------------------------------
	// 1. Handle deletion
	// -----------------------------------------------------------------------
	// In Reconcile method
	if !hprs.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(hprs, hprsFinalizer) {
			if err := r.cleanupOwnedResources(ctx, hprs); err != nil {
				log.FromContext(ctx).Error(err, "Error cleaning up owned resources during finalization (non-fatal)")
			}
			controllerutil.RemoveFinalizer(hprs, hprsFinalizer)
			if err := r.Update(ctx, hprs); err != nil && !k8serrors.IsConflict(err) {
				log.FromContext(ctx).Error(err, "Error removing finalizer during termination (non-fatal)")
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(hprs, hprsFinalizer) {
		controllerutil.AddFinalizer(hprs, hprsFinalizer)
		if err := r.Update(ctx, hprs); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 2. Reconcile children
	dep, err := r.reconcileDeployment(ctx, hprs)
	if err != nil {
		return r.updateStatus(ctx, hprs, humiov1alpha1.HumioClusterStateConfigError, err)
	}
	if err = r.reconcileService(ctx, hprs); err != nil {
		return r.updateStatus(ctx, hprs, humiov1alpha1.HumioClusterStateConfigError, err)
	}

	// 3. Determine “Running” vs “Configuring”
	//    (env-test never creates pods, so we check only desired spec parity)
	state := humiov1alpha1.HumioPdfRenderServiceStateRunning
	// Handle explicit scaling to zero as a special case (ScaledDown state)
	if dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0 {
		state = humiov1alpha1.HumioPdfRenderServiceStateScaledDown
	} else if dep.Status.ObservedGeneration != dep.Generation {
		// Deployment hasn't processed the latest spec yet
		// Note: There's no direct "Pending" state in HumioPdfRenderService,
		// using Configuring is most appropriate
		state = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	} else if dep.Status.ReadyReplicas < dep.Status.Replicas {
		// Not all replicas are ready
		state = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	}

	// Add at the end of Reconcile method
	if state == humiov1alpha1.HumioPdfRenderServiceStateConfiguring ||
		state == humiov1alpha1.HumioPdfRenderServiceStateScalingUp {
		// Requeue after a short delay for transitional states
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return r.updateStatus(ctx, hprs, state, nil)
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
	desired := r.constructDesiredService(hprs) // This is a Service

	var svc corev1.Service // This is the actual Service being reconciled
	svc.Name, svc.Namespace = desired.Name, desired.Namespace

	// Correct mutate function for Service
	mutate := func() error {
		// Copy ObjectMeta fields
		svc.Labels = desired.Labels
		svc.Annotations = desired.Annotations

		// Copy relevant Spec fields from desired Service
		svc.Spec.Ports = desired.Spec.Ports
		svc.Spec.Selector = desired.Spec.Selector
		// Use the ServiceType from the desired spec, defaulting to ClusterIP if empty
		svc.Spec.Type = desired.Spec.Type
		if svc.Spec.Type == "" {
			svc.Spec.Type = corev1.ServiceTypeClusterIP
		}

		// Set owner reference on the Service itself
		return controllerutil.SetControllerReference(hprs, &svc, r.Scheme)
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &svc, mutate)
	if err != nil {
		return fmt.Errorf("create/update Service: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Service reconciled", "operation", op)
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
	if hprs.Spec.TLS == nil || hprs.Spec.TLS.Enabled == nil || !*hprs.Spec.TLS.Enabled {
		return nil, nil
	}

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

// TLS validation (unchanged)
func (r *HumioPdfRenderServiceReconciler) validateTLSConfiguration(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	if hprs.Spec.TLS == nil || hprs.Spec.TLS.Enabled == nil || !*hprs.Spec.TLS.Enabled {
		return nil
	}
	secretName := fmt.Sprintf("%s-certificate", childName(hprs))
	if _, err := kubernetes.GetSecret(ctx, r.Client, secretName, hprs.Namespace); err != nil {
		return err
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

// updateStatus updates the status of the HumioPdfRenderService with complete information
func (r *HumioPdfRenderServiceReconciler) updateStatus(
	ctx context.Context,
	hprs *humiov1alpha1.HumioPdfRenderService,
	state string,
	reconcileErr error,
) (ctrl.Result, error) {
	// Fetch Deployment to get replica status
	var dep appsv1.Deployment
	depKey := types.NamespacedName{Name: childName(hprs), Namespace: hprs.Namespace}
	depErr := r.Get(ctx, depKey, &dep)

	// Default ready to 0 if deployment not found
	ready := int32(0)
	var pods []string

	// Get deployment status if available
	if depErr == nil {
		ready = dep.Status.ReadyReplicas

		// Optionally collect pod names
		podList := &corev1.PodList{}
		if err := r.List(ctx, podList,
			client.InNamespace(hprs.Namespace),
			client.MatchingLabels(labelsForHumioPdfRenderService(hprs.Name))); err == nil {
			for _, pod := range podList.Items {
				pods = append(pods, pod.Name)
			}
		}
	}

	r.Log.Info("Updating status",
		"state", state,
		"readyReplicas", ready,
		"generation", hprs.Generation,
		"observedGeneration", hprs.Generation,
	)

	// Use the existing setStatus function with retry logic
	err := r.setStatus(ctx, hprs, state, ready, pods, reconcileErr)
	if err != nil {
		return ctrl.Result{Requeue: true}, err
	}
	return ctrl.Result{}, reconcileErr
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
func (r *HumioPdfRenderServiceReconciler) cleanupOwnedResources(ctx context.Context, pdfRenderSvc *humiov1alpha1.HumioPdfRenderService) error {
	log := log.FromContext(ctx)
	// Usually, OwnerReferences handle garbage collection of Deployment/Service.
	// If you create other resources *without* OwnerReferences, delete them here.
	log.Info("No external resources to clean up explicitly (assuming OwnerReferences handle Deployment/Service)")
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
