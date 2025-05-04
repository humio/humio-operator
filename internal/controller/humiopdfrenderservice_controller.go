package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
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
	// Namespace filtering
	if r.Namespace != "" && r.Namespace != req.Namespace {
		return reconcile.Result{}, nil
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioPdfRenderService")

	// ---------------------------------------------------------------------
	// 1. Fetch CR
	// ---------------------------------------------------------------------
	hprs := &humiov1alpha1.HumioPdfRenderService{}
	if err := r.Get(ctx, req.NamespacedName, hprs); err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("HumioPdfRenderService was deleted")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// ---------------------------------------------------------------------
	// 2. Finalizer handling
	// ---------------------------------------------------------------------
	if hprs.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(hprs, hprsFinalizer) {
			if err := r.finalize(ctx, hprs); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(hprs, hprsFinalizer)
			if err := r.Update(ctx, hprs); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(hprs, hprsFinalizer) {
		controllerutil.AddFinalizer(hprs, hprsFinalizer)
		if err := r.Update(ctx, hprs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// ---------------------------------------------------------------------
	// 3. Validate TLS config (short‑circuit if invalid)
	// ---------------------------------------------------------------------
	if err := r.validateTLSConfiguration(ctx, hprs); err != nil {
		_ = r.setStatus(ctx, hprs, humiov1alpha1.HumioClusterStateConfigError, 0, nil, err)
		return ctrl.Result{RequeueAfter: MaximumMinReadyRequeue}, nil
	}

	// ---------------------------------------------------------------------
	// 4. Reconcile children (Deployment + Service)
	// ---------------------------------------------------------------------
	dep, depErr := r.reconcileDeployment(ctx, hprs)
	if depErr != nil {
		_ = r.setStatus(ctx, hprs, humiov1alpha1.HumioClusterStateConfigError, 0, nil, depErr)
		return ctrl.Result{}, depErr
	}

	if err := r.reconcileService(ctx, hprs); err != nil {
		_ = r.setStatus(ctx, hprs, humiov1alpha1.HumioClusterStateConfigError, dep.Status.ReadyReplicas, nil, err)
		return ctrl.Result{}, err
	}

	// ---------------------------------------------------------------------
	// 5. Success – update status
	// ---------------------------------------------------------------------
	// Work out the state the CR should expose
	var state string
	desired := int32(1)
	if dep.Spec.Replicas != nil {
		desired = *dep.Spec.Replicas
	}

	switch {
	case desired == 0:
		// There is nothing to run – treat as scaled‑down explicitly.
		state = "ScaledDown"
	case dep.Status.ObservedGeneration < dep.Generation:
		// A new Deployment generation is rolling out.
		state = humiov1alpha1.HumioClusterStateUpgrading
	case dep.Status.ReadyReplicas < desired:
		// Pods still coming up.
		state = humiov1alpha1.HumioClusterStatePending
	default:
		// Everything is up‑to‑date and ready.
		state = humiov1alpha1.HumioClusterStateRunning
	}

	_ = r.setStatus(ctx, hprs, state, dep.Status.ReadyReplicas, nil, nil)
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// -----------------------------------------------------------------------------
// Child resources
// -----------------------------------------------------------------------------
// reconcileDeployment reconciles the Deployment for the HumioPdfRenderService.
// It creates or updates the Deployment based on the desired state.
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) (*appsv1.Deployment, error) {
	log := log.FromContext(ctx)
	desired := r.constructDesiredDeployment(hprs)

	var dep appsv1.Deployment
	dep.Name, dep.Namespace = desired.Name, desired.Namespace

	mutate := func() error {
		dep.Labels = desired.Labels
		dep.Annotations = desired.Annotations
		dep.Spec = desired.Spec
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

	mutate := func() error {
		svc.Labels = desired.Labels
		svc.Annotations = desired.Annotations
		svc.Spec.Ports = desired.Spec.Ports
		svc.Spec.Selector = desired.Spec.Selector
		svc.Spec.Type = corev1.ServiceTypeClusterIP
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

// -----------------------------------------------------------------------------
// Desired objects helpers (unchanged except trimmed comments)
// -----------------------------------------------------------------------------
func (r *HumioPdfRenderServiceReconciler) constructDesiredDeployment(hprs *humiov1alpha1.HumioPdfRenderService) *appsv1.Deployment {
	labels := labelsForHumioPdfRenderService(hprs.Name)

	replicas := hprs.Spec.Replicas
	image := hprs.Spec.Image
	port := hprs.Spec.Port
	if port == 0 {
		port = DefaultPdfRenderServicePort
	}

	envVars := append([]corev1.EnvVar(nil), hprs.Spec.EnvironmentVariables...)
	volumes, mounts := r.tlsVolumesAndMounts(hprs, &envVars)

	container := corev1.Container{
		Name:            "pdf-render-service",
		Image:           image,
		Ports:           []corev1.ContainerPort{{Name: "http", ContainerPort: int32(port), Protocol: corev1.ProtocolTCP}},
		Env:             envVars,
		VolumeMounts:    mounts,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources:       hprs.Spec.Resources,
		LivenessProbe:   hprs.Spec.LivenessProbe,
		ReadinessProbe:  hprs.Spec.ReadinessProbe,
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        hprs.Name,
			Namespace:   hprs.Namespace,
			Labels:      labels,
			Annotations: hprs.Spec.Annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels, Annotations: hprs.Spec.Annotations},
				Spec: corev1.PodSpec{
					Containers:       []corev1.Container{container},
					Volumes:          volumes,
					ImagePullSecrets: hprs.Spec.ImagePullSecrets,
				},
			},
		},
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
	portName := "http"
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
		portName = "https"
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        hprs.Name,
			Namespace:   hprs.Namespace,
			Labels:      labels,
			Annotations: hprs.Spec.Annotations,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       portName,
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

	secretName := fmt.Sprintf("%s-certificate", hprs.Name)
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

// Status helpers
func (r *HumioPdfRenderServiceReconciler) setStatus(
	ctx context.Context,
	hprs *humiov1alpha1.HumioPdfRenderService,
	state string,
	ready int32,
	pods []string,
	err error,
) error {
	// no-op if nothing changed
	if hprs.Status.State == state &&
		hprs.Status.ReadyReplicas == ready &&
		((err == nil && hprs.Status.Message == "") ||
			(err != nil && hprs.Status.Message == err.Error())) {
		return nil
	}

	hprs.Status.State = state
	hprs.Status.ReadyReplicas = ready
	hprs.Status.Nodes = pods
	if err != nil {
		hprs.Status.Message = err.Error()
	} else {
		hprs.Status.Message = ""
	}

	return r.Status().Update(ctx, hprs)
}

// Finalizer helper
func (r *HumioPdfRenderServiceReconciler) finalize(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	// Best‑effort deletion – ignore not‑found errors
	_ = r.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: hprs.Name, Namespace: hprs.Namespace}})
	_ = r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: hprs.Name, Namespace: hprs.Namespace}})
	return nil
}

// -----------------------------------------------------------------------------
// TLS validation (unchanged)
// -----------------------------------------------------------------------------
func (r *HumioPdfRenderServiceReconciler) validateTLSConfiguration(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	if hprs.Spec.TLS == nil || hprs.Spec.TLS.Enabled == nil || !*hprs.Spec.TLS.Enabled {
		return nil
	}
	secretName := fmt.Sprintf("%s-certificate", hprs.Name)
	if _, err := kubernetes.GetSecret(ctx, r.Client, secretName, hprs.Namespace); err != nil {
		return err
	}
	return nil
}

// -----------------------------------------------------------------------------
// Misc helpers
// -----------------------------------------------------------------------------
func labelsForHumioPdfRenderService(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "HumioPdfRenderService",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/component":  "pdf-render-service",
	}
}

// -----------------------------------------------------------------------------
// Setup
// -----------------------------------------------------------------------------
func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPdfRenderService{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}).
		Named("humiopdfrenderservice").
		Complete(r)
}
