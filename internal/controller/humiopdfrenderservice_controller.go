package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller/versions"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sApiEquality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
	pdfTLSCertVolumeName = "humio-pdf-render-service-tls" // For HPRS's own server cert
	caCertMountPath      = "/etc/humio-pdf-render-service/ca"
	caCertVolumeName     = "humio-pdf-render-service-ca" // For CA cert to talk to Humio Cluster

	// Finalizer applied to HumioPdfRenderService resources
	hprsFinalizer = "core.humio.com/finalizer"

	// All child resources are named <cr-name>-pdf-render-service
	childSuffix = "-pdf-render-service"
)

// HumioPdfRenderServiceReconciler reconciles a HumioPdfRenderService object
type HumioPdfRenderServiceReconciler struct {
	client.Client
	APIReader    client.Reader
	Scheme       *runtime.Scheme
	BaseLogger   logr.Logger
	CommonConfig CommonConfig
	Namespace    string
	Log          logr.Logger // Added back for helper functions
}

// Reconcile implements the reconciliation logic for HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.BaseLogger.WithValues("hprsName", req.Name, "hprsNamespace", req.Namespace)
	r.Log = log

	hprs := &humiov1alpha1.HumioPdfRenderService{}
	if err := r.Get(ctx, req.NamespacedName, hprs); err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("HumioPdfRenderService resource not found – probably deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Always publish status at the end of the reconcile loop
	var (
		reconcileErr error
		finalState   string
	)
	defer func() {
		// Only update status if the resource still exists and is not being deleted
		if hprs != nil && hprs.DeletionTimestamp.IsZero() {
			_ = r.updateStatus(ctx, hprs, finalState, reconcileErr)
		}
	}()

	// Replica sanity check
	if hprs.Spec.Replicas < 0 {
		reconcileErr = fmt.Errorf("spec.replicas must be non-negative")
		finalState = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		return ctrl.Result{}, reconcileErr
	}
	// Finalizer
	if res, err := r.ensureFinalizer(ctx, hprs); err != nil || res.Requeue {
		return res, err
	}

	// Validate spec (TLS etc.)
	if err := r.validateTLSConfiguration(ctx, hprs); err != nil {
		// validation failure ⇒ ConfigError
		hprs.Status.ReadyReplicas = 0
		_ = r.updateStatus(ctx, hprs, humiov1alpha1.HumioPdfRenderServiceStateConfigError, err)
		return ctrl.Result{}, err
	}

	// Reconcile children Deployment
	op, dep, err := r.reconcileDeployment(ctx, hprs)
	if err != nil {
		_ = r.updateStatus(ctx, hprs, humiov1alpha1.HumioPdfRenderServiceStateConfigError, err)
		return ctrl.Result{}, err
	}

	// Service
	if err := r.reconcileService(ctx, hprs); err != nil {
		_ = r.updateStatus(ctx, hprs, humiov1alpha1.HumioPdfRenderServiceStateConfigError, err)
		return ctrl.Result{}, err
	}

	// Determine state
	targetState := humiov1alpha1.HumioPdfRenderServiceStateRunning
	if dep == nil || dep.Status.ReadyReplicas < hprs.Spec.Replicas {
		targetState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	}
	if hprs.Spec.Replicas == 0 {
		targetState = humiov1alpha1.HumioPdfRenderServiceStateScaledDown
	}
	if dep != nil {
		hprs.Status.ReadyReplicas = dep.Status.ReadyReplicas
	} else {
		hprs.Status.ReadyReplicas = 0
	}

	// Update status
	_ = r.updateStatus(ctx, hprs, targetState, nil)

	// Requeue while configuring
	if targetState == humiov1alpha1.HumioPdfRenderServiceStateConfiguring {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// If we just created/updated the Deployment we requeue quickly so the
	// ready-replicas information is refreshed.
	if op == controllerutil.OperationResultCreated || op == controllerutil.OperationResultUpdated {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.APIReader = mgr.GetAPIReader()

	// ------------------------------------------------------------------
	// Register index: .spec.humioCluster.name  →  HumioPdfRenderService
	// Needed by findHumioPdfRenderServicesForHumioCluster() and HC tests
	// ------------------------------------------------------------------
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&humiov1alpha1.HumioPdfRenderService{},
		".spec.humioClusterRef.name",
		func(obj client.Object) []string {
			h := obj.(*humiov1alpha1.HumioPdfRenderService)
			if h.Spec.HumioCluster.Name != "" {
				return []string{h.Spec.HumioCluster.Name}
			}
			return nil
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPdfRenderService{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		// Re-queue when a referenced Secret changes (TLS rotation)
		// Re-queue when a referenced Secret changes (TLS rotation)
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				secret := obj.(*corev1.Secret)
				hprsList := &humiov1alpha1.HumioPdfRenderServiceList{}
				_ = mgr.GetClient().List(ctx, hprsList, client.InNamespace(secret.Namespace))
				var reqs []reconcile.Request
				for _, h := range hprsList.Items {
					if shouldWatchSecret(&h, secret.Name) {
						reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{
							Name: h.Name, Namespace: h.Namespace}})
					}
				}
				return reqs
			},
		)).
		Watches(
			&humiov1alpha1.HumioCluster{},
			handler.EnqueueRequestsFromMapFunc(r.findHumioPdfRenderServicesForHumioCluster),
		).
		Complete(r)
}

// shouldWatchSecret checks if the given secret is referenced by the HumioPdfRenderService's TLS configuration.
func shouldWatchSecret(hprs *humiov1alpha1.HumioPdfRenderService, secretName string) bool {
	if hprs.Spec.TLS != nil {
		// watch the CA secret if specified
		if hprs.Spec.TLS.CASecretName != "" && hprs.Spec.TLS.CASecretName == secretName {
			return true
		}
		// watch the generated server TLS secret by naming convention
		serverCertSecretName := childName(hprs) + "-tls"
		if secretName == serverCertSecretName {
			return true
		}
	}
	return false
}

func (r *HumioPdfRenderServiceReconciler) ensureFinalizer(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) (ctrl.Result, error) {
	if hprs.DeletionTimestamp.IsZero() {
		if controllerutil.AddFinalizer(hprs, hprsFinalizer) {
			return ctrl.Result{Requeue: true}, r.Update(ctx, hprs)
		}
		return ctrl.Result{}, nil
	}
	// TODO: Implement deleteChildren in the next step
	if err := r.deleteChildren(ctx, hprs); err != nil {
		return ctrl.Result{}, err
	}
	if controllerutil.RemoveFinalizer(hprs, hprsFinalizer) {
		return ctrl.Result{Requeue: true}, r.Update(ctx, hprs)
	}
	return ctrl.Result{}, nil
}

func (r *HumioPdfRenderServiceReconciler) deleteChildren(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	// bulk delete by label selector
	r.Log.Info("Bulk-deleting child Deployments and Services")

	selector := client.MatchingLabels{"humio-pdf-render-service": hprs.Name}

	// Delete all Deployments

	if err := r.DeleteAllOf(
		ctx,
		&appsv1.Deployment{},
		client.InNamespace(hprs.Namespace),
		selector,
	); err != nil {
		r.Log.Error(err, "Failed to bulk delete Deployments")
		return err
	}

	// Delete all Services
	if err := r.DeleteAllOf(
		ctx,
		&corev1.Service{},
		client.InNamespace(hprs.Namespace),
		selector,
	); err != nil {
		r.Log.Error(err, "Failed to bulk delete Services")
		return err
	}

	r.Log.Info("Bulk delete completed")
	return nil
}

// reconcileDeployment creates or updates the Deployment for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileDeployment(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) (controllerutil.OperationResult, *appsv1.Deployment, error) {
	log := r.Log.WithValues("function", "reconcileDeployment")
	desired := r.constructDesiredDeployment(hprs)
	log.Info("Constructed desired Deployment spec.", "desiredImage", desired.Spec.Template.Spec.Containers[0].Image, "desiredReplicas", *desired.Spec.Replicas)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}

	op := controllerutil.OperationResultNone

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var getErr error
		key := client.ObjectKeyFromObject(dep)
		getErr = r.Get(ctx, key, dep)

		if k8serrors.IsNotFound(getErr) {
			log.Info("Deployment not found, attempting to create.", "deploymentName", key.Name)

			// Use CreateOrUpdate with a mutate function that sets all fields
			op, createErr := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
				dep.Labels = desired.Labels
				dep.Annotations = desired.Annotations
				dep.Spec = desired.Spec

				// Set controller reference to ensure proper ownership and garbage collection
				if errCtrl := controllerutil.SetControllerReference(hprs, dep, r.Scheme); errCtrl != nil {
					log.Error(errCtrl, "Failed to set controller reference on Deployment object",
						"deploymentName", dep.Name)
					return errCtrl
				}
				return nil
			})
			if createErr == nil {
				log.Info("Deployment creation/update attempt finished via CreateOrUpdate.", "operationResult", op)
			} else {
				log.Error(createErr, "Failed during CreateOrUpdate (creation path).", "deploymentName", dep.Name)
			}
			return createErr
		} else if getErr != nil {
			log.Error(getErr, "Failed to get Deployment for update check.", "deploymentName", key.Name)
			return fmt.Errorf("failed to get deployment %s: %w", key, getErr)
		}

		log.Info("Existing Deployment found.", "deploymentName", dep.Name, "currentImage", dep.Spec.Template.Spec.Containers[0].Image, "currentReplicas", dep.Spec.Replicas)
		originalDeployment := dep.DeepCopy()

		dep.Labels = desired.Labels
		dep.Annotations = desired.Annotations
		dep.Spec.Replicas = desired.Spec.Replicas
		dep.Spec.Template = desired.Spec.Template
		dep.Spec.Strategy = desired.Spec.Strategy

		// Always ensure controller reference is set properly
		if errCtrl := controllerutil.SetControllerReference(hprs, dep, r.Scheme); errCtrl != nil {
			log.Error(errCtrl, "Failed to set controller reference on existing Deployment object before update.")
			return errCtrl
		}

		// Log ownership information for debugging
		log.Info("Checking deployment ownership",
			"deploymentName", dep.Name,
			"ownerReferences", dep.OwnerReferences)

		// Check if there are any actual changes to apply
		specChanged := !k8sApiEquality.Semantic.DeepEqual(originalDeployment.Spec, dep.Spec)
		labelsChanged := !k8sApiEquality.Semantic.DeepEqual(originalDeployment.Labels, dep.Labels)
		annotationsChanged := !k8sApiEquality.Semantic.DeepEqual(originalDeployment.Annotations, dep.Annotations)

		if specChanged {
			log.Info("Deployment spec has changed, proceeding to update to pick up every difference (env, args, etc.)", "deploymentName", dep.Name)
			// we no longer special-case only image/replica diffs: any change in the pod template (env, mounts, args, etc.)
			// should trigger a Deployment.Update so your new environment variables are rolled out.
		}

		if !specChanged && !labelsChanged && !annotationsChanged {
			log.Info("No change in Deployment spec, labels, or annotations. Skipping update.", "deploymentName", dep.Name)
			op = controllerutil.OperationResultNone
			return nil
		}

		log.Info("Attempting to update Deployment.", "deploymentName", dep.Name, "newImage", dep.Spec.Template.Spec.Containers[0].Image)
		updateErr := r.Update(ctx, dep)
		if updateErr == nil {
			op = controllerutil.OperationResultUpdated
			log.Info("Deployment successfully updated.", "deploymentName", dep.Name)
		} else {
			if k8serrors.IsConflict(updateErr) {
				log.Info("Conflict during Deployment update, will retry.", "deploymentName", dep.Name)
			} else {
				log.Error(updateErr, "Failed to update Deployment.", "deploymentName", dep.Name)
			}
		}
		return updateErr
	})

	if err != nil {
		log.Error(err, "Create/Update Deployment failed after retries.", "deploymentName", desired.Name)
		return controllerutil.OperationResultNone, nil, fmt.Errorf("create/update Deployment %s failed after retries: %w", desired.Name, err)
	}

	// After successful update, if we're updating the deployment, ensure we get the latest version
	// with updated status fields to properly check readiness
	// After successful creation or update, ensure we get the latest version
	// with updated status fields to properly check readiness
	// Use APIReader to get the most up-to-date version of the deployment
	freshDep := &appsv1.Deployment{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(dep), freshDep); err != nil {
		if !k8serrors.IsNotFound(err) {
			log.Error(err, "Failed to get fresh deployment after reconciliation", "deploymentName", dep.Name)
		}
		// Continue with the existing deployment object if we can't get a fresh one
	} else {
		// Use the fresh deployment with the most up-to-date status
		dep = freshDep
		log.Info("Retrieved fresh deployment after reconciliation",
			"deploymentName", dep.Name,
			"generation", dep.Generation,
			"observedGeneration", dep.Status.ObservedGeneration,
			"readyReplicas", dep.Status.ReadyReplicas)
	}

	if op != controllerutil.OperationResultNone {
		log.Info("Deployment successfully reconciled.", "deploymentName", dep.Name, "operation", op)
	} else {
		log.Info("Deployment spec was already up-to-date.", "deploymentName", dep.Name, "operation", op)
	}
	return op, dep, nil
}

// reconcileService creates or updates the Service for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) reconcileService(
	ctx context.Context,
	hprs *humiov1alpha1.HumioPdfRenderService,
) error {
	log := r.Log.WithValues("function", "reconcileService")

	desired := r.constructDesiredService(hprs)
	log.Info("Constructed desired Service spec.",
		"serviceName", desired.Name,
		"desiredType", desired.Spec.Type,
		"desiredPorts", desired.Spec.Ports)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}

	// Create-or-Update handles both creation and patching
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		// When the object exists we arrive here with the *live* object in `svc`.
		// Preserve immutable fields:
		if svc.Spec.ClusterIP != "" &&
			svc.Spec.ClusterIP != "None" &&
			desired.Spec.Type == corev1.ServiceTypeClusterIP {
			desired.Spec.ClusterIP = svc.Spec.ClusterIP
		}

		// Apply the desired state
		svc.Labels = desired.Labels
		svc.Annotations = desired.Annotations
		svc.Spec.Type = desired.Spec.Type
		svc.Spec.Ports = desired.Spec.Ports
		svc.Spec.Selector = desired.Spec.Selector
		svc.Spec.ClusterIP = desired.Spec.ClusterIP

		// Set owner reference
		return controllerutil.SetControllerReference(hprs, svc, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to create or update Service", "serviceName", desired.Name)
		return fmt.Errorf("failed to reconcile Service %s: %w", desired.Name, err)
	}

	log.Info("Service reconciled successfully.", "serviceName", svc.Name)
	return nil
}

// constructDesiredDeployment creates a new Deployment object for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDesiredDeployment(
	hprs *humiov1alpha1.HumioPdfRenderService,
) *appsv1.Deployment {
	labels := labelsForHumioPdfRenderService(hprs.Name)
	replicas := hprs.Spec.Replicas
	port := getPdfRenderServicePort(hprs)

	if hprs.Spec.Image == "" {
		hprs.Spec.Image = versions.DefaultPDFRenderServiceImage()
	}

	envVars, vols, mounts := r.buildRuntimeAssets(hprs, port)
	container := r.buildPDFContainer(hprs, port, envVars, mounts)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        childName(hprs),
			Namespace:   hprs.Namespace,
			Labels:      labels,
			Annotations: hprs.Spec.Annotations, // Pod Annotations
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: hprs.Spec.Annotations, // Pod Annotations
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

// Get the port for the PDF Render Service
func getPdfRenderServicePort(hprs *humiov1alpha1.HumioPdfRenderService) int32 {
	if hprs.Spec.Port != 0 {
		return hprs.Spec.Port
	}
	return DefaultPdfRenderServicePort
}

// buildRuntimeAssets constructs the runtime assets for the PDF Render Service.
func (r *HumioPdfRenderServiceReconciler) buildRuntimeAssets(
	hprs *humiov1alpha1.HumioPdfRenderService,
	port int32,
) ([]corev1.EnvVar, []corev1.Volume, []corev1.VolumeMount) {
	envVars := []corev1.EnvVar{
		{Name: "HUMIO_PORT", Value: fmt.Sprintf("%d", port)},
		// LogLevel, HumioBaseURL, ExtraKafkaConfigs are not direct spec fields.
		// They should be set via EnvironmentVariables if needed.
		{Name: "HUMIO_NODE_ID", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
	}

	envVars = append(envVars, hprs.Spec.EnvironmentVariables...) // Use correct field
	envVars = sortEnv(envVars)

	vols, mounts := r.tlsVolumesAndMounts(hprs, &envVars)

	vols = append(vols, hprs.Spec.Volumes...)          // Use correct field
	mounts = append(mounts, hprs.Spec.VolumeMounts...) // Use correct field

	return dedupEnvVars(envVars), dedupVolumes(vols), dedupVolumeMounts(mounts)
}

// buildPDFContainer constructs the container for the PDF Render Service.
func (r *HumioPdfRenderServiceReconciler) buildPDFContainer(
	hprs *humiov1alpha1.HumioPdfRenderService,
	port int32,
	envVars []corev1.EnvVar,
	mounts []corev1.VolumeMount,
) corev1.Container {
	container := corev1.Container{
		Name:  "humio-pdf-render-service",
		Image: hprs.Spec.Image,
		// ImagePullPolicy is not on HPRS spec, defaults or set on container directly if needed
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: port, Protocol: corev1.ProtocolTCP},
		},
		Env:          envVars,
		VolumeMounts: mounts,
		Resources:    hprs.Spec.Resources,
	}

	defaultLivenessProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: humiov1alpha1.DefaultPdfRenderServiceLiveness, // Use constant
				Port: intstr.FromInt(int(port)),
			},
		},
		InitialDelaySeconds: 60, PeriodSeconds: 10, TimeoutSeconds: 5, FailureThreshold: 3, SuccessThreshold: 1,
	}
	container.LivenessProbe = firstNonNilProbe(hprs.Spec.LivenessProbe, defaultLivenessProbe)

	defaultReadinessProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: humiov1alpha1.DefaultPdfRenderServiceReadiness, // Use constant
				Port: intstr.FromInt(int(port)),
			},
		},
		InitialDelaySeconds: 10, PeriodSeconds: 10, TimeoutSeconds: 5, FailureThreshold: 3, SuccessThreshold: 1,
	}
	container.ReadinessProbe = firstNonNilProbe(hprs.Spec.ReadinessProbe, defaultReadinessProbe)

	if hprs.Spec.SecurityContext != nil { // Use correct field
		container.SecurityContext = hprs.Spec.SecurityContext
	}

	r.Log.Info("Creating container with resources", "memoryRequests", container.Resources.Requests.Memory().String(), "cpuRequests", container.Resources.Requests.Cpu().String(), "memoryLimits", container.Resources.Limits.Memory().String(), "cpuLimits", container.Resources.Limits.Cpu().String())
	return container
}

// constructDesiredService creates a new Service object for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDesiredService(hprs *humiov1alpha1.HumioPdfRenderService) *corev1.Service {
	labels := labelsForHumioPdfRenderService(hprs.Name)
	port := getPdfRenderServicePort(hprs)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        childName(hprs),
			Namespace:   hprs.Namespace,
			Labels:      labels,
			Annotations: hprs.Spec.ServiceAnnotations, // Service Annotations
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
			Type: corev1.ServiceTypeClusterIP,
		},
	}
	if hprs.Spec.ServiceType != "" {
		svc.Spec.Type = hprs.Spec.ServiceType
	}
	return svc
}

// tlsVolumesAndMounts constructs the TLS volumes and mounts for the PDF Render Service.
// It also sets the appropriate environment variables for TLS configuration.
func (r *HumioPdfRenderServiceReconciler) tlsVolumesAndMounts(hprs *humiov1alpha1.HumioPdfRenderService, env *[]corev1.EnvVar) ([]corev1.Volume, []corev1.VolumeMount) {
	var vols []corev1.Volume
	var mounts []corev1.VolumeMount

	// Check hprs.Spec.TLS and hprs.Spec.TLS.Enabled safely
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
		// Secret for HPRS's own server certificate
		serverCertSecretName := childName(hprs) + "-tls" // Default convention

		*env = append(*env,
			corev1.EnvVar{Name: pdfRenderUseTLSEnvVar, Value: "true"},
			corev1.EnvVar{Name: pdfRenderTLSCertPathEnvVar, Value: fmt.Sprintf("%s/%s", pdfTLSCertMountPath, corev1.TLSCertKey)},
			corev1.EnvVar{Name: pdfRenderTLSKeyPathEnvVar, Value: fmt.Sprintf("%s/%s", pdfTLSCertMountPath, corev1.TLSPrivateKeyKey)},
		)

		// Volume for HPRS's own server certificate
		vols = append(vols, corev1.Volume{
			Name: pdfTLSCertVolumeName, // Volume for the server cert
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: serverCertSecretName},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: pdfTLSCertVolumeName, MountPath: pdfTLSCertMountPath, ReadOnly: true})

		// If CA secret is specified, mount it
		if hprs.Spec.TLS.CASecretName != "" {
			*env = append(*env, corev1.EnvVar{Name: pdfRenderCAFileEnvVar, Value: fmt.Sprintf("%s/%s", caCertMountPath, corev1.ServiceAccountTokenKey)}) // "ca.crt"
			vols = append(vols, corev1.Volume{
				Name: caCertVolumeName, // Volume for the CA cert
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: hprs.Spec.TLS.CASecretName},
				},
			})
			mounts = append(mounts, corev1.VolumeMount{Name: caCertVolumeName, MountPath: caCertMountPath, ReadOnly: true})
		}
	}
	return dedupVolumes(vols), dedupVolumeMounts(mounts)
}

// validateTLSConfiguration checks if the TLS configuration is valid.
// It verifies that the server certificate secret exists and contains the required keys.
// It also checks if the CA certificate secret exists if specified.
func (r *HumioPdfRenderServiceReconciler) validateTLSConfiguration(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	// Check hprs.Spec.TLS and hprs.Spec.TLS.Enabled safely
	if hprs.Spec.TLS == nil || hprs.Spec.TLS.Enabled == nil || !*hprs.Spec.TLS.Enabled {
		return nil // TLS is not enabled, no validation needed
	}

	// Validate server certificate secret
	serverCertSecretName := childName(hprs) + "-tls" // Default convention
	var tlsSecret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: serverCertSecretName, Namespace: hprs.Namespace}, &tlsSecret); err != nil {
		if k8serrors.IsNotFound(err) {
			return fmt.Errorf("TLS is enabled for HPRS %s/%s, but its server TLS-certificate secret %s was not found: %w", hprs.Namespace, hprs.Name, serverCertSecretName, err)
		}
		return fmt.Errorf("failed to get HPRS server TLS-certificate secret %s for HPRS %s/%s: %w", serverCertSecretName, hprs.Namespace, hprs.Name, err)
	}
	if _, ok := tlsSecret.Data[corev1.TLSCertKey]; !ok {
		return fmt.Errorf("HPRS server TLS-certificate secret %s for HPRS %s/%s is missing key %s", serverCertSecretName, hprs.Namespace, hprs.Name, corev1.TLSCertKey)
	}
	if _, ok := tlsSecret.Data[corev1.TLSPrivateKeyKey]; !ok {
		return fmt.Errorf("HPRS server TLS-certificate secret %s for HPRS %s/%s is missing key %s", serverCertSecretName, hprs.Namespace, hprs.Namespace, corev1.TLSPrivateKeyKey)
	}

	// Validate CA certificate secret if specified
	if hprs.Spec.TLS.CASecretName != "" {
		var caSecret corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{Name: hprs.Spec.TLS.CASecretName, Namespace: hprs.Namespace}, &caSecret); err != nil {
			if k8serrors.IsNotFound(err) {
				return fmt.Errorf("TLS is enabled for HPRS %s/%s, and CA-certificate secret %s is specified, but the secret was not found: %w", hprs.Namespace, hprs.Name, hprs.Spec.TLS.CASecretName, err)
			}
			return fmt.Errorf("failed to get CA-certificate secret %s for HPRS %s/%s: %w", hprs.Spec.TLS.CASecretName, hprs.Namespace, hprs.Name, err)
		}
		if _, ok := caSecret.Data[corev1.ServiceAccountTokenKey]; !ok { // "ca.crt"
			return fmt.Errorf("CA-certificate secret %s for HPRS %s/%s is missing key %s", hprs.Spec.TLS.CASecretName, hprs.Namespace, hprs.Name, corev1.ServiceAccountTokenKey)
		}
	}

	return nil
}

// childName generates the name for the child resources (Deployment, Service) of the HumioPdfRenderService.
func childName(hprs *humiov1alpha1.HumioPdfRenderService) string {
	return hprs.Name + childSuffix
}

// labelsForHumioPdfRenderService returns the labels for the HumioPdfRenderService resources.
func labelsForHumioPdfRenderService(name string) map[string]string {
	return map[string]string{"app": "humio-pdf-render-service", "humio-pdf-render-service": name}
}

// updateStatus updates the status of the HumioPdfRenderService resource.
func (r *HumioPdfRenderServiceReconciler) updateStatus(
	ctx context.Context,
	hprsForStatusUpdate *humiov1alpha1.HumioPdfRenderService,
	currentStateFromReconcileLoop string,
	reconcileErr error,
) error {
	log := r.Log.WithValues("function", "updateStatus", "targetState", currentStateFromReconcileLoop)

	// Always update ObservedGeneration
	hprsForStatusUpdate.Status.ObservedGeneration = hprsForStatusUpdate.Generation

	// Update State and Message based on the outcome of the reconcile loop
	hprsForStatusUpdate.Status.State = currentStateFromReconcileLoop
	if reconcileErr != nil {
		hprsForStatusUpdate.Status.Message = fmt.Sprintf("Reconciliation failed: %v", reconcileErr)
	} else if hprsForStatusUpdate.Status.State == humiov1alpha1.HumioPdfRenderServiceStateRunning || hprsForStatusUpdate.Status.State == humiov1alpha1.HumioPdfRenderServiceStateScaledDown {
		hprsForStatusUpdate.Status.Message = "" // Clear message on success/scaled down
	}

	// Update Conditions based on the determined state
	// Condition: Available
	availableStatus := metav1.ConditionFalse
	availableReason := "DeploymentUnavailable"
	availableMessage := "Deployment is not available"
	switch hprsForStatusUpdate.Status.State {
	case humiov1alpha1.HumioPdfRenderServiceStateRunning:
		availableStatus = metav1.ConditionTrue
		availableReason = "DeploymentAvailable"
		availableMessage = "Deployment is available and ready"
	case humiov1alpha1.HumioPdfRenderServiceStateScaledDown:
		availableStatus = metav1.ConditionFalse
		availableReason = "ScaledDown"
		availableMessage = "HPRS is scaled down to 0 replicas"
	}
	setStatusCondition(hprsForStatusUpdate, metav1.Condition{
		Type:               string(humiov1alpha1.HumioPdfRenderServiceAvailable),
		Status:             availableStatus,
		Reason:             availableReason,
		Message:            availableMessage,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: hprsForStatusUpdate.Generation,
	})

	// Condition: Progressing
	progressingStatus := metav1.ConditionFalse
	progressingReason := "ReconciliationComplete"
	progressingMessage := "Reconciliation is complete"
	if hprsForStatusUpdate.Status.State == humiov1alpha1.HumioPdfRenderServiceStateConfiguring {
		progressingStatus = metav1.ConditionTrue
		progressingReason = "Configuring"
		progressingMessage = hprsForStatusUpdate.Status.Message // Use the stateReason from reconcile loop
	}
	setStatusCondition(hprsForStatusUpdate, metav1.Condition{
		Type:               string(humiov1alpha1.HumioPdfRenderServiceProgressing),
		Status:             progressingStatus,
		Reason:             progressingReason,
		Message:            progressingMessage,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: hprsForStatusUpdate.Generation,
	})

	// Condition: Degraded
	degradedStatus := metav1.ConditionFalse
	degradedReason := "ReconciliationSucceeded"
	degradedMessage := "Reconciliation succeeded"
	if hprsForStatusUpdate.Status.State == humiov1alpha1.HumioPdfRenderServiceStateConfigError {
		degradedStatus = metav1.ConditionTrue
		degradedReason = "ConfigError"
		degradedMessage = hprsForStatusUpdate.Status.Message // Use the error message from reconcile loop
	} else if reconcileErr != nil {
		degradedStatus = metav1.ConditionTrue
		degradedReason = "ReconciliationFailed"
		degradedMessage = fmt.Sprintf("Reconciliation failed: %v", reconcileErr)
	}
	setStatusCondition(hprsForStatusUpdate, metav1.Condition{
		Type:               string(humiov1alpha1.HumioPdfRenderServiceDegraded),
		Status:             degradedStatus,
		Reason:             degradedReason,
		Message:            degradedMessage,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: hprsForStatusUpdate.Generation,
	})

	// Condition: ScaledDown
	scaledDownStatus := metav1.ConditionFalse
	scaledDownReason := "NotScaledDown"
	scaledDownMessage := "HPRS is not scaled down"
	if hprsForStatusUpdate.Status.State == humiov1alpha1.HumioPdfRenderServiceStateScaledDown {
		scaledDownStatus = metav1.ConditionTrue
		scaledDownReason = "ScaledDown"
		scaledDownMessage = "HPRS is scaled down to 0 replicas"
	}
	setStatusCondition(hprsForStatusUpdate, metav1.Condition{
		Type:               string(humiov1alpha1.HumioPdfRenderServiceScaledDown),
		Status:             scaledDownStatus,
		Reason:             scaledDownReason,
		Message:            scaledDownMessage,
		LastTransitionTime: metav1.Now(),
		ObservedGeneration: hprsForStatusUpdate.Generation,
	})

	// Attempt to update the status with retries
	statusUpdateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentHprs := &humiov1alpha1.HumioPdfRenderService{}
		err := r.Get(ctx, types.NamespacedName{Name: hprsForStatusUpdate.Name, Namespace: hprsForStatusUpdate.Namespace}, currentHprs)
		if err != nil {
			return err // Return the error to RetryOnConflict
		}

		// Update the status fields on the fetched object
		currentHprs.Status.ObservedGeneration = hprsForStatusUpdate.Status.ObservedGeneration
		currentHprs.Status.State = hprsForStatusUpdate.Status.State
		currentHprs.Status.Message = hprsForStatusUpdate.Status.Message
		currentHprs.Status.ReadyReplicas = hprsForStatusUpdate.Status.ReadyReplicas
		currentHprs.Status.Conditions = hprsForStatusUpdate.Status.Conditions // Copy the updated conditions

		updateErr := r.Status().Update(ctx, currentHprs)
		if updateErr == nil {
			log.Info("Successfully updated HumioPdfRenderService status.")
			return nil // Success
		}
		if k8serrors.IsConflict(updateErr) {
			log.Info("Conflict updating status, will retry.")
			return updateErr // Return conflict error to RetryOnConflict
		}
		return updateErr // Return other errors to RetryOnConflict
	})
	if statusUpdateErr != nil {
		log.Error(statusUpdateErr, "Failed to update HumioPdfRenderService status after retries.")
		return statusUpdateErr
	}

	return nil // Status update successful
}

// setStatusCondition sets the given condition in the status of the HumioPdfRenderService.
func setStatusCondition(hprs *humiov1alpha1.HumioPdfRenderService, condition metav1.Condition) {
	meta.SetStatusCondition(&hprs.Status.Conditions, condition)
}

// findHumioPdfRenderServicesForHumioCluster returns a list of reconcile.Requests for HumioPdfRenderService resources
func (r *HumioPdfRenderServiceReconciler) findHumioPdfRenderServicesForHumioCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	log := r.BaseLogger.WithValues("function", "findHumioPdfRenderServicesForHumioCluster")
	humioCluster, ok := obj.(*humiov1alpha1.HumioCluster)
	if !ok {
		log.Error(fmt.Errorf("expected a HumioCluster but got a %T", obj), "failed to get HumioCluster")
		return []reconcile.Request{}
	}

	hprsList := &humiov1alpha1.HumioPdfRenderServiceList{}
	listOpts := []client.ListOption{
		client.InNamespace(humioCluster.GetNamespace()),
		client.MatchingFields{".spec.humioClusterRef.name": humioCluster.GetName()},
	}

	if err := r.List(ctx, hprsList, listOpts...); err != nil {
		log.Error(err, "failed to list HumioPdfRenderServices for HumioCluster")
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(hprsList.Items)) // Initialize with 0 length
	for _, item := range hprsList.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      item.GetName(),
				Namespace: item.GetNamespace(),
			},
		})
	}
	log.Info("Found HumioPdfRenderServices for HumioCluster", "humioCluster", humioCluster.GetName(), "count", len(requests))
	return requests
}

// dedupEnvVars, dedupVolumes, and dedupVolumeMounts are utility functions to remove duplicates from slices
func dedupEnvVars(envVars []corev1.EnvVar) []corev1.EnvVar {
	seen := make(map[string]corev1.EnvVar)
	order := []string{}
	for _, env := range envVars {
		if _, ok := seen[env.Name]; !ok {
			seen[env.Name] = env
			order = append(order, env.Name)
		}
	}
	result := make([]corev1.EnvVar, len(order))
	for i, name := range order {
		result[i] = seen[name]
	}
	return result
}

func dedupVolumes(vols []corev1.Volume) []corev1.Volume {
	seen := make(map[string]corev1.Volume)
	result := []corev1.Volume{}
	for _, vol := range vols {
		if _, ok := seen[vol.Name]; !ok {
			seen[vol.Name] = vol
			result = append(result, vol)
		}
	}
	return result
}

func dedupVolumeMounts(mnts []corev1.VolumeMount) []corev1.VolumeMount {
	seen := make(map[string]corev1.VolumeMount)
	result := []corev1.VolumeMount{}
	for _, mnt := range mnts {
		if _, ok := seen[mnt.Name]; !ok {
			seen[mnt.Name] = mnt
			result = append(result, mnt)
		}
	}
	return result
}

func sortEnv(env []corev1.EnvVar) []corev1.EnvVar {
	sort.Slice(env, func(i, j int) bool {
		return env[i].Name < env[j].Name
	})
	return env
}

func firstNonNilProbe(p *corev1.Probe, fallback *corev1.Probe) *corev1.Probe {
	if p != nil {
		return p
	}
	return fallback
}
