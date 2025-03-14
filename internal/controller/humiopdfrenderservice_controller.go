package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller/versions"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
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

	// TLS‑related env‑vars (only boolean switch, no certificate content)
	pdfRenderUseTLSEnvVar = "HUMIO_PDF_RENDER_USE_TLS"

	// Hash of the sanitised pod spec – kept in the pod-template just like HumioCluster
	HPRSPodSpecHashAnnotation = "humio.com/pod-spec-hash"

	// TLS volume / mount
	pdfTLSCertMountPath  = "/etc/tls"
	pdfTLSCertVolumeName = "tls" // For HPRS's own server cert
	caCertMountPath      = "/etc/ca"
	caCertVolumeName     = "ca" // For CA cert to talk to Humio Cluster

	// Finalizer applied to HumioPdfRenderService resources
	hprsFinalizer = "core.humio.com/finalizer"

	// Certificate hash annotation for tracking certificate changes
	HPRSCertificateHashAnnotation = "humio.com/hprs-certificate-hash"
)

// +kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiopdfrenderservices/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cert-manager.io,resources=issuers,verbs=get;list;watch;create;update;patch;delete

// HumioPdfRenderServiceReconciler reconciles a HumioPdfRenderService object
type HumioPdfRenderServiceReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	BaseLogger   logr.Logger
	CommonConfig CommonConfig
	Namespace    string
	Log          logr.Logger // Added back for helper functions
}

// isPdfRenderServiceEnabled checks if the PDF Render Service feature is enabled
// by checking if any HumioCluster has ENABLE_SCHEDULED_REPORT=true environment variable
func (r *HumioPdfRenderServiceReconciler) isPdfRenderServiceEnabled(ctx context.Context, namespace string) bool {
	var humioClusters humiov1alpha1.HumioClusterList
	if err := r.Client.List(ctx, &humioClusters, client.InNamespace(namespace)); err != nil {
		r.Log.Error(err, "Failed to list HumioClusters to check ENABLE_SCHEDULED_REPORT", "namespace", namespace)
		return false
	}

	r.Log.Info("Found HumioClusters", "count", len(humioClusters.Items), "namespace", namespace)

	if len(humioClusters.Items) == 0 {
		r.Log.Info("No HumioClusters found in namespace", "namespace", namespace)
		return false
	}

	for _, cluster := range humioClusters.Items {
		r.Log.Info("Checking HumioCluster for ENABLE_SCHEDULED_REPORT",
			"cluster", cluster.ObjectMeta.Name, "namespace", cluster.ObjectMeta.Namespace,
			"state", cluster.Status.State,
			"commonEnvVars", len(cluster.Spec.CommonEnvironmentVariables),
			"envVars", len(cluster.Spec.EnvironmentVariables))

		// Check both CommonEnvironmentVariables and EnvironmentVariables for ENABLE_SCHEDULED_REPORT
		for _, envVar := range cluster.Spec.CommonEnvironmentVariables {
			r.Log.Info("Checking CommonEnvironmentVariable", "name", envVar.Name, "value", envVar.Value)
			if envVar.Name == "ENABLE_SCHEDULED_REPORT" && strings.ToLower(envVar.Value) == "true" {
				r.Log.Info("Found ENABLE_SCHEDULED_REPORT=true in HumioCluster CommonEnvironmentVariables",
					"cluster", cluster.ObjectMeta.Name, "namespace", cluster.ObjectMeta.Namespace)
				return true
			}
		}
		for _, envVar := range cluster.Spec.EnvironmentVariables {
			r.Log.Info("Checking EnvironmentVariable", "name", envVar.Name, "value", envVar.Value)
			if envVar.Name == "ENABLE_SCHEDULED_REPORT" && strings.ToLower(envVar.Value) == "true" {
				r.Log.Info("Found ENABLE_SCHEDULED_REPORT=true in HumioCluster EnvironmentVariables",
					"cluster", cluster.ObjectMeta.Name, "namespace", cluster.ObjectMeta.Namespace)
				return true
			}
		}
	}
	r.Log.Info("No HumioCluster found with ENABLE_SCHEDULED_REPORT=true", "namespace", namespace)
	return false
}

// Reconcile implements the reconciliation logic for HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.BaseLogger.WithValues("hprsName", req.Name, "hprsNamespace", req.Namespace)
	r.Log = log

	// DEBUG: Add entry log to confirm controller is being called
	log.Info("HumioPdfRenderService controller Reconcile called", "request", req.String())

	hprs := &humiov1alpha1.HumioPdfRenderService{}
	if err := r.Client.Get(ctx, req.NamespacedName, hprs); err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("HumioPdfRenderService resource not found – probably deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("Reconciling HumioPdfRenderService")

	// Set default values
	hprs.SetDefaults()

	// Always publish status at the end of the reconcile loop
	var (
		reconcileErr error
		finalState   string
	)
	defer func() {
		// Only update status if the resource still exists and is not being deleted
		if hprs != nil && hprs.ObjectMeta.DeletionTimestamp.IsZero() {
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

	// Check if PDF Render Service feature is enabled by any HumioCluster
	// Only check this after finalizer logic to avoid interfering with deletion
	pdfFeatureEnabled := r.isPdfRenderServiceEnabled(ctx, hprs.ObjectMeta.Namespace)
	if !pdfFeatureEnabled {
		log.Info("No HumioCluster found with ENABLE_SCHEDULED_REPORT=true. Scaling down PDF Render Service deployment.")

		// Scale down the deployment to 0 replicas when feature is not enabled
		originalReplicas := hprs.Spec.Replicas
		hprs.Spec.Replicas = 0

		// Reconcile the deployment with 0 replicas to shut it down
		_, _, err := r.reconcileDeployment(ctx, hprs)
		if err != nil {
			reconcileErr = err
			finalState = humiov1alpha1.HumioPdfRenderServiceStateConfigError
			return ctrl.Result{}, reconcileErr
		}

		// Restore original replica count in spec for next reconciliation
		hprs.Spec.Replicas = originalReplicas

		// Set state to ScaledDown and requeue to check again later
		finalState = humiov1alpha1.HumioPdfRenderServiceStateScaledDown
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}

	log.Info("PDF Render Service feature is enabled - proceeding with reconciliation")

	// When TLS is enabled, handle certificate management
	if helpers.TLSEnabledForHPRS(hprs) {
		if helpers.UseCertManager() {
			// When cert-manager is available, ensure we have proper certificates in place FIRST.
			if err := r.EnsureValidCAIssuerForHPRS(ctx, hprs); err != nil {
				reconcileErr = err
				finalState = humiov1alpha1.HumioPdfRenderServiceStateConfigError
				return ctrl.Result{}, reconcileErr
			}

			if err := r.ensureHprsServerCertificate(ctx, hprs); err != nil {
				reconcileErr = err
				finalState = humiov1alpha1.HumioPdfRenderServiceStateConfigError
				return ctrl.Result{}, reconcileErr
			}
		}

		// Validate spec (TLS etc.) AFTER ensuring certificates are created (if using cert-manager).
		if err := r.validateTLSConfiguration(ctx, hprs); err != nil {
			reconcileErr = err
			finalState = humiov1alpha1.HumioPdfRenderServiceStateConfigError
			return ctrl.Result{}, reconcileErr
		}
	}

	// Reconcile children Deployment
	op, dep, err := r.reconcileDeployment(ctx, hprs)
	if err != nil {
		reconcileErr = err
		finalState = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		return ctrl.Result{}, reconcileErr
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, hprs); err != nil {
		reconcileErr = err
		finalState = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		return ctrl.Result{}, reconcileErr
	}

	// Reconcile HPA if autoscaling is enabled
	if err := r.reconcileHPA(ctx, hprs, dep); err != nil {
		reconcileErr = err
		finalState = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		return ctrl.Result{}, reconcileErr
	}

	// Determine state based on Deployment readiness
	targetState := humiov1alpha1.HumioPdfRenderServiceStateRunning
	if dep == nil || dep.Status.ReadyReplicas < hprs.Spec.Replicas {
		targetState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
	}
	if hprs.Spec.Replicas == 0 {
		targetState = humiov1alpha1.HumioPdfRenderServiceStateScaledDown
	}

	// Set final state for defer function to handle
	finalState = targetState

	// Requeue while configuring or in error state.
	if targetState == humiov1alpha1.HumioPdfRenderServiceStateConfiguring {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	if targetState == humiov1alpha1.HumioPdfRenderServiceStateConfigError {
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}
	// Requeue shortly after Deployment changes.
	if op == controllerutil.OperationResultCreated || op == controllerutil.OperationResultUpdated {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {

	builder := ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPdfRenderService{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		// Watch HumioClusters to detect when ENABLE_SCHEDULED_REPORT is set
		Watches(&humiov1alpha1.HumioCluster{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				// When a HumioCluster changes, requeue all HumioPdfRenderServices
				// so they can check if they should be enabled/disabled
				var hprsList humiov1alpha1.HumioPdfRenderServiceList
				if err := mgr.GetClient().List(ctx, &hprsList, client.InNamespace(obj.GetNamespace())); err != nil {
					return nil
				}

				var requests []reconcile.Request
				for _, hprs := range hprsList.Items {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      hprs.ObjectMeta.Name,
							Namespace: hprs.ObjectMeta.Namespace,
						},
					})
				}
				return requests
			},
		))

	// Only set up cert-manager watches if cert-manager is enabled
	if helpers.UseCertManager() {
		builder = builder.
			Owns(&cmapi.Certificate{}).
			Owns(&cmapi.Issuer{})
	}

	return builder.
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
							Name: h.ObjectMeta.Name, Namespace: h.ObjectMeta.Namespace}})
					}
				}
				return reqs
			},
		)).
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
		serverCertSecretName := fmt.Sprintf("%s-tls", childName(hprs))
		if secretName == serverCertSecretName {
			return true
		}
		// Also watch the CA keypair secret
		caSecretName := getCASecretNameForHPRS(hprs)
		if secretName == caSecretName {
			return true
		}
	}
	return false
}

func (r *HumioPdfRenderServiceReconciler) ensureFinalizer(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) (ctrl.Result, error) {
	if hprs.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.AddFinalizer(hprs, hprsFinalizer) {
			return ctrl.Result{Requeue: true}, r.Client.Update(ctx, hprs)
		}
		return ctrl.Result{}, nil
	}
	// TODO: Implement deleteChildren in the next step
	if err := r.deleteChildren(ctx, hprs); err != nil {
		return ctrl.Result{}, err
	}
	if controllerutil.RemoveFinalizer(hprs, hprsFinalizer) {
		return ctrl.Result{Requeue: true}, r.Client.Update(ctx, hprs)
	}
	return ctrl.Result{}, nil
}

func (r *HumioPdfRenderServiceReconciler) deleteChildren(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	// bulk delete by label selector
	r.Log.Info("Bulk-deleting child Deployments and Services")

	selector := client.MatchingLabels{"humio-pdf-render-service": hprs.ObjectMeta.Name}

	// Delete all Deployments
	if err := r.Client.DeleteAllOf(
		ctx,
		&appsv1.Deployment{},
		client.InNamespace(hprs.ObjectMeta.Namespace),
		selector,
	); err != nil {
		r.Log.Error(err, "Failed to bulk delete Deployments")
		return err
	}

	// Delete all Services
	if err := r.Client.DeleteAllOf(
		ctx,
		&corev1.Service{},
		client.InNamespace(hprs.ObjectMeta.Namespace),
		selector,
	); err != nil {
		r.Log.Error(err, "Failed to bulk delete Services")
		return err
	}

	// Delete certificates and issuers if cert-manager is enabled
	if helpers.UseCertManager() {
		if err := r.Client.DeleteAllOf(
			ctx,
			&cmapi.Certificate{},
			client.InNamespace(hprs.ObjectMeta.Namespace),
			selector,
		); err != nil {
			r.Log.Error(err, "Failed to bulk delete Certificates")
			return err
		}

		if err := r.Client.DeleteAllOf(
			ctx,
			&cmapi.Issuer{},
			client.InNamespace(hprs.ObjectMeta.Namespace),
			selector,
		); err != nil {
			r.Log.Error(err, "Failed to bulk delete Issuers")
			return err
		}
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
		getErr = r.Client.Get(ctx, key, dep)

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

		// Check if we need to update by comparing only the fields we care about
		needsUpdate := false

		// Compare replicas (only if not using HPA)
		if !helpers.HpaEnabledForHPRS(hprs) && !reflect.DeepEqual(dep.Spec.Replicas, desired.Spec.Replicas) {
			needsUpdate = true
			log.Info("Replicas changed", "current", dep.Spec.Replicas, "desired", desired.Spec.Replicas)
		}

		// Compare labels
		if !reflect.DeepEqual(dep.Labels, desired.Labels) {
			needsUpdate = true
			log.Info("Labels changed")
		}

		// Compare annotations
		annotationsChanged := false
		for k, v := range desired.Annotations {
			if currentVal, ok := dep.Annotations[k]; !ok || currentVal != v {
				annotationsChanged = true
				break
			}
		}
		if annotationsChanged {
			needsUpdate = true
			log.Info("Annotations changed")
		}

		// Compare pod template spec using hash-based comparison like HumioCluster controller
		currentPod := &corev1.Pod{
			Spec: *dep.Spec.Template.Spec.DeepCopy(),
		}
		desiredPod := &corev1.Pod{
			Spec: *desired.Spec.Template.Spec.DeepCopy(),
		}

		// Sanitize both pods for comparison
		// sanitizedCurrentPod := sanitizePodForPdfRenderService(currentPod.DeepCopy())
		// sanitizedDesiredPod := sanitizePodForPdfRenderService(desiredPod.DeepCopy())

		sanitizedCurrentPod := SanitizePod(currentPod.DeepCopy(), SanitizePodOpts{
			TLSVolumeName: pdfTLSCertVolumeName,
			CAVolumeName:  caCertVolumeName,
		})
		sanitizedDesiredPod := SanitizePod(desiredPod.DeepCopy(), SanitizePodOpts{
			TLSVolumeName: pdfTLSCertVolumeName,
			CAVolumeName:  caCertVolumeName,
		})

		// Use hash-based comparison (without managed fields since HPRS doesn't have managed fields)
		currentHasher := NewPodHasher(sanitizedCurrentPod, nil)
		desiredHasher := NewPodHasher(sanitizedDesiredPod, nil)

		currentHash, err := currentHasher.PodHashMinusManagedFields()
		if err != nil {
			log.Error(err, "Failed to calculate current pod hash")
			return err
		}

		desiredHash, err := desiredHasher.PodHashMinusManagedFields()
		if err != nil {
			log.Error(err, "Failed to calculate desired pod hash")
			return err
		}

		if currentHash != desiredHash {
			needsUpdate = true
			log.Info("Pod template spec changed", "currentHash", currentHash, "desiredHash", desiredHash)

			// DEBUG: Add detailed logging to identify the differences
			currentJSON, _ := json.MarshalIndent(sanitizedCurrentPod.Spec, "", "  ")
			desiredJSON, _ := json.MarshalIndent(sanitizedDesiredPod.Spec, "", "  ")
			log.Info("DEBUG: Current sanitized pod spec", "spec", string(currentJSON))
			log.Info("DEBUG: Desired sanitized pod spec", "spec", string(desiredJSON))
		}

		// Compare pod template labels
		if !reflect.DeepEqual(dep.Spec.Template.Labels, desired.Spec.Template.Labels) {
			needsUpdate = true
			log.Info("Pod template labels changed")
		}

		// Compare pod template annotations (excluding dynamic ones)
		currentPodTemplateAnnotations := make(map[string]string)
		for k, v := range dep.Spec.Template.Annotations {
			currentPodTemplateAnnotations[k] = v
		}
		delete(currentPodTemplateAnnotations, HPRSCertificateHashAnnotation)

		desiredPodTemplateAnnotations := make(map[string]string)
		for k, v := range desired.Spec.Template.Annotations {
			desiredPodTemplateAnnotations[k] = v
		}
		delete(desiredPodTemplateAnnotations, HPRSCertificateHashAnnotation)

		if !reflect.DeepEqual(currentPodTemplateAnnotations, desiredPodTemplateAnnotations) {
			needsUpdate = true
			log.Info("Pod template annotations changed (excluding certificate hash)")
		}

		// Special handling for certificate hash annotation
		// Only update if the certificate actually changed
		currentCertHash := dep.Spec.Template.Annotations[HPRSCertificateHashAnnotation]
		desiredCertHash := desired.Spec.Template.Annotations[HPRSCertificateHashAnnotation]
		if currentCertHash != desiredCertHash && desiredCertHash != "" {
			needsUpdate = true
			log.Info("Certificate hash changed", "current", currentCertHash, "desired", desiredCertHash)
		}

		if !needsUpdate {
			log.Info("No changes detected in Deployment. Skipping update.", "deploymentName", dep.Name)
			op = controllerutil.OperationResultNone

			// In envtest environments, manually update the deployment status if observedGeneration is behind
			// This is needed because the deployment controller doesn't run in envtest
			if helpers.UseEnvtest() && dep.Status.ObservedGeneration < dep.Generation {
				log.Info("Updating deployment status in envtest since observedGeneration is behind",
					"currentObservedGeneration", dep.Status.ObservedGeneration,
					"currentGeneration", dep.Generation)

				// Update the observedGeneration to match the current generation
				dep.Status.ObservedGeneration = dep.Generation

				// Also update replicas count to match the spec
				if dep.Spec.Replicas != nil {
					dep.Status.Replicas = *dep.Spec.Replicas
					// In envtest, assume pods are ready since we don't have a real deployment controller
					dep.Status.ReadyReplicas = *dep.Spec.Replicas
				}

				statusErr := r.Client.Status().Update(ctx, dep)
				if statusErr != nil {
					log.Error(statusErr, "Failed to update deployment status in envtest")
				} else {
					log.Info("Successfully updated deployment status in envtest",
						"observedGeneration", dep.Status.ObservedGeneration,
						"readyReplicas", dep.Status.ReadyReplicas)
				}
			}

			return nil
		}

		// Apply updates
		dep.Labels = desired.Labels
		if dep.Annotations == nil {
			dep.Annotations = make(map[string]string)
		}
		for k, v := range desired.Annotations {
			dep.Annotations[k] = v
		}
		if !helpers.HpaEnabledForHPRS(hprs) {
			dep.Spec.Replicas = desired.Spec.Replicas
		}
		dep.Spec.Template = desired.Spec.Template
		dep.Spec.Strategy = desired.Spec.Strategy

		// Always ensure controller reference is set properly
		if errCtrl := controllerutil.SetControllerReference(hprs, dep, r.Scheme); errCtrl != nil {
			log.Error(errCtrl, "Failed to set controller reference on existing Deployment object before update.")
			return errCtrl
		}

		log.Info("Attempting to update Deployment.", "deploymentName", dep.Name, "newImage", dep.Spec.Template.Spec.Containers[0].Image)
		updateErr := r.Client.Update(ctx, dep)
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
	freshDep := &appsv1.Deployment{}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(dep), freshDep); err != nil {
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

// sanitizePodForPdfRenderService removes known nondeterministic fields from a pod and returns it.
// This is adapted from the HumioCluster controller's sanitizePod function for PDF Render Service pods.
// func sanitizePodForPdfRenderService(pod *corev1.Pod) *corev1.Pod {
// 	// Sanitize volumes to remove non-deterministic fields
// 	sanitizedVolumes := make([]corev1.Volume, 0)
// 	mode := int32(420)

// 	for _, volume := range pod.Spec.Volumes {
// 		if volume.Name == pdfTLSCertVolumeName {
// 			// Normalize TLS certificate volume
// 			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
// 				Name: pdfTLSCertVolumeName,
// 				VolumeSource: corev1.VolumeSource{
// 					Secret: &corev1.SecretVolumeSource{
// 						SecretName:  "", // Clear secret name for comparison
// 						DefaultMode: &mode,
// 					},
// 				},
// 			})
// 		} else if volume.Name == caCertVolumeName {
// 			// Normalize CA certificate volume
// 			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
// 				Name: caCertVolumeName,
// 				VolumeSource: corev1.VolumeSource{
// 					Secret: &corev1.SecretVolumeSource{
// 						SecretName: "", // Clear secret name for comparison
// 						Items: []corev1.KeyToPath{
// 							{
// 								Key:  "ca.crt",
// 								Path: "ca.crt",
// 							},
// 						},
// 						DefaultMode: &mode,
// 					},
// 				},
// 			})
// 		} else if strings.HasPrefix(volume.Name, "kube-api-access-") {
// 			// Normalize service account token volumes (auto-injected by k8s)
// 			sanitizedVolumes = append(sanitizedVolumes, corev1.Volume{
// 				Name:         "kube-api-access-",
// 				VolumeSource: corev1.VolumeSource{},
// 			})
// 		} else {
// 			// Keep other volumes as-is
// 			sanitizedVolumes = append(sanitizedVolumes, volume)
// 		}
// 	}
// 	pod.Spec.Volumes = sanitizedVolumes

// 	// Values we don't set ourselves but which gets default values set.
// 	// To get a cleaner diff we can set these values to their zero values.
// 	pod.Spec.RestartPolicy = ""
// 	pod.Spec.DNSPolicy = ""
// 	pod.Spec.SchedulerName = ""
// 	pod.Spec.Priority = nil
// 	pod.Spec.EnableServiceLinks = nil
// 	pod.Spec.PreemptionPolicy = nil
// 	pod.Spec.DeprecatedServiceAccount = ""
// 	pod.Spec.NodeName = ""

// 	for i := range pod.Spec.InitContainers {
// 		pod.Spec.InitContainers[i].TerminationMessagePath = ""
// 		pod.Spec.InitContainers[i].TerminationMessagePolicy = ""
// 		// Normalize ImagePullPolicy - let Kubernetes set the default based on image tag
// 		if pod.Spec.InitContainers[i].ImagePullPolicy == "" {
// 			imageParts := strings.Split(pod.Spec.InitContainers[i].Image, ":")
// 			if len(imageParts) == 1 || imageParts[len(imageParts)-1] == "latest" {
// 				pod.Spec.InitContainers[i].ImagePullPolicy = corev1.PullAlways
// 			} else {
// 				pod.Spec.InitContainers[i].ImagePullPolicy = corev1.PullIfNotPresent
// 			}
// 		}
// 	}

// 	for i := range pod.Spec.Containers {
// 		pod.Spec.Containers[i].TerminationMessagePath = ""
// 		pod.Spec.Containers[i].TerminationMessagePolicy = ""
// 		// Normalize ImagePullPolicy - let Kubernetes set the default based on image tag
// 		if pod.Spec.Containers[i].ImagePullPolicy == "" {
// 			imageParts := strings.Split(pod.Spec.Containers[i].Image, ":")
// 			if len(imageParts) == 1 || imageParts[len(imageParts)-1] == "latest" {
// 				pod.Spec.Containers[i].ImagePullPolicy = corev1.PullAlways
// 			} else {
// 				pod.Spec.Containers[i].ImagePullPolicy = corev1.PullIfNotPresent
// 			}
// 		}
// 	}

// 	// Sort lists of container environment variables, so we won't get a diff because the order changes.
// 	for i := range pod.Spec.Containers {
// 		sort.SliceStable(pod.Spec.Containers[i].Env, func(j, k int) bool {
// 			return pod.Spec.Containers[i].Env[j].Name > pod.Spec.Containers[i].Env[k].Name
// 		})
// 	}
// 	for i := range pod.Spec.InitContainers {
// 		sort.SliceStable(pod.Spec.InitContainers[i].Env, func(j, k int) bool {
// 			return pod.Spec.InitContainers[i].Env[j].Name > pod.Spec.InitContainers[i].Env[k].Name
// 		})
// 	}

// 	return pod
// }

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

// reconcileHPA creates, updates, or deletes the HPA for the HumioPdfRenderService based on autoscaling configuration.
func (r *HumioPdfRenderServiceReconciler) reconcileHPA(
	ctx context.Context,
	hprs *humiov1alpha1.HumioPdfRenderService,
	deployment *appsv1.Deployment,
) error {
	log := r.Log.WithValues("function", "reconcileHPA")

	hpaName := helpers.PdfRenderServiceHpaName(hprs.ObjectMeta.Name)
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hpaName,
			Namespace: hprs.ObjectMeta.Namespace,
		},
	}

	// If autoscaling is not enabled, ensure HPA is deleted
	if !helpers.HpaEnabledForHPRS(hprs) {
		log.Info("Autoscaling is disabled, ensuring HPA is deleted", "hpaName", hpaName)
		err := r.Delete(ctx, hpa)
		if err != nil && !k8serrors.IsNotFound(err) {
			log.Error(err, "failed to delete HPA", "hpaName", hpaName)
			return fmt.Errorf("failed to delete HPA %s: %w", hpaName, err)
		}
		if k8serrors.IsNotFound(err) {
			log.Info("HPA already deleted or does not exist", "hpaName", hpaName)
		} else {
			log.Info("HPA deleted successfully", "hpaName", hpaName)
		}
		return nil
	}

	// Autoscaling is enabled, ensure HPA exists and is up to date
	if deployment == nil {
		return fmt.Errorf("cannot create HPA: deployment does not exist yet")
	}

	log.Info("Autoscaling is enabled, ensuring HPA exists", "hpaName", hpaName)

	desired := r.constructDesiredHPA(hprs, deployment)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, hpa, func() error {
		// Apply the desired state
		hpa.Labels = desired.Labels
		hpa.Annotations = desired.Annotations
		hpa.Spec = desired.Spec

		// Set owner reference
		return controllerutil.SetControllerReference(hprs, hpa, r.Scheme)
	})
	if err != nil {
		log.Error(err, "failed to create or update HPA", "hpaName", hpaName)
		return fmt.Errorf("failed to reconcile HPA %s: %w", hpaName, err)
	}

	log.Info("HPA reconciled successfully", "hpaName", hpa.Name)
	return nil
}

// constructDesiredHPA creates a new HPA object for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDesiredHPA(
	hprs *humiov1alpha1.HumioPdfRenderService,
	deployment *appsv1.Deployment,
) *autoscalingv2.HorizontalPodAutoscaler {
	autoscalingSpec := hprs.Spec.Autoscaling
	hpaName := helpers.PdfRenderServiceHpaName(hprs.ObjectMeta.Name)

	labels := map[string]string{
		"app":                 "pdf-render-service",
		"humio.com/component": "pdf-render-service",
	}

	// Merge user-defined labels
	for k, v := range hprs.Spec.Labels {
		labels[k] = v
	}

	// Build metrics list
	metrics := make([]autoscalingv2.MetricSpec, 0)

	// Add custom metrics if provided
	if len(autoscalingSpec.Metrics) > 0 {
		metrics = append(metrics, autoscalingSpec.Metrics...)
	}

	// Add convenience CPU metric if specified
	if autoscalingSpec.TargetCPUUtilizationPercentage != nil {
		cpuMetric := autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: autoscalingSpec.TargetCPUUtilizationPercentage,
				},
			},
		}
		metrics = append(metrics, cpuMetric)
	}

	// Add convenience Memory metric if specified
	if autoscalingSpec.TargetMemoryUtilizationPercentage != nil {
		memoryMetric := autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceMemory,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: autoscalingSpec.TargetMemoryUtilizationPercentage,
				},
			},
		}
		metrics = append(metrics, memoryMetric)
	}

	// If no metrics are defined, default to 80% CPU utilization
	if len(metrics) == 0 {
		defaultCPUTarget := int32(80)
		cpuMetric := autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: &defaultCPUTarget,
				},
			},
		}
		metrics = append(metrics, cpuMetric)
	}

	// Set MinReplicas with default fallback
	minReplicas := autoscalingSpec.MinReplicas
	if minReplicas == nil {
		defaultMin := int32(1)
		minReplicas = &defaultMin
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:        hpaName,
			Namespace:   hprs.ObjectMeta.Namespace,
			Labels:      labels,
			Annotations: hprs.Spec.Annotations, // Use pod annotations for HPA
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       deployment.Name,
			},
			MinReplicas: minReplicas,
			MaxReplicas: autoscalingSpec.MaxReplicas,
			Metrics:     metrics,
			Behavior:    autoscalingSpec.Behavior,
		},
	}

	return hpa
}

// constructDesiredDeployment creates a new Deployment object for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDesiredDeployment(
	hprs *humiov1alpha1.HumioPdfRenderService,
) *appsv1.Deployment {
	labels := labelsForHumioPdfRenderService(hprs.ObjectMeta.Name)
	replicas := hprs.Spec.Replicas
	port := getPdfRenderServicePort(hprs)

	if hprs.Spec.Image == "" {
		hprs.Spec.Image = versions.DefaultPDFRenderServiceImage()
	}

	envVars, vols, mounts := r.buildRuntimeAssets(hprs, port)
	container := r.buildPDFContainer(hprs, port, envVars, mounts)

	// Prepare annotations for deployment and pod template
	deploymentAnnotations := make(map[string]string)
	podTemplateAnnotations := make(map[string]string)

	// Copy user-provided annotations
	if hprs.Spec.Annotations != nil {
		for k, v := range hprs.Spec.Annotations {
			deploymentAnnotations[k] = v
			podTemplateAnnotations[k] = v
		}
	}

	// Add certificate hash annotation for TLS-enabled services to trigger pod restarts on cert changes
	if helpers.TLSEnabledForHPRS(hprs) && helpers.UseCertManager() {
		certHash := r.getHprsCertificateHash(hprs)
		if certHash != "" {
			podTemplateAnnotations[HPRSCertificateHashAnnotation] = certHash
		}
	}

	// We have to set this as it will be defaulted by kubernetes and we will otherwise trigger an update loop
	terminationGracePeriodSeconds := int64(30)

	// Initialize pod security context - even if nil, Kubernetes will add an empty object
	podSecurityContext := hprs.Spec.PodSecurityContext
	if podSecurityContext == nil {
		// Set an empty SecurityContext to match what Kubernetes will default to
		podSecurityContext = &corev1.PodSecurityContext{}
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        childName(hprs),
			Namespace:   hprs.ObjectMeta.Namespace,
			Labels:      labels,
			Annotations: deploymentAnnotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: podTemplateAnnotations,
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: &terminationGracePeriodSeconds,
					ServiceAccountName:            hprs.Spec.ServiceAccountName,
					Affinity:                      hprs.Spec.Affinity,
					ImagePullSecrets:              hprs.Spec.ImagePullSecrets,
					SecurityContext:               podSecurityContext,
					Containers:                    []corev1.Container{container},
					Volumes:                       vols,
				},
			},
		},
	}

	// ------------------------------------------------------------------
	// ➊ Compute a stable hash of the sanitised pod spec and persist it as
	//    an annotation (same pattern as HumioCluster controller).
	// ------------------------------------------------------------------
	tmpPod := &corev1.Pod{Spec: dep.Spec.Template.Spec}
	sanitised := SanitizePod(tmpPod.DeepCopy(), SanitizePodOpts{
		TLSVolumeName: pdfTLSCertVolumeName,
		CAVolumeName:  caCertVolumeName,
	})

	hasher := NewPodHasher(sanitised, nil)
	if hash, err := hasher.PodHashMinusManagedFields(); err == nil {
		if dep.Spec.Template.Annotations == nil {
			dep.Spec.Template.Annotations = map[string]string{}
		}
		dep.Spec.Template.Annotations[HPRSPodSpecHashAnnotation] = hash
	}
	// ------------------------------------------------------------------

	return dep
}

// getHprsCertificateHash returns the current certificate hash for HPRS, similar to GetDesiredCertHash in HumioCluster
func (r *HumioPdfRenderServiceReconciler) getHprsCertificateHash(hprs *humiov1alpha1.HumioPdfRenderService) string {
	certificate := r.constructHprsCertificate(hprs)

	// Clear annotations for consistent hashing (following HumioCluster pattern)
	certificate.Annotations = nil
	certificate.ResourceVersion = ""

	b, _ := json.Marshal(certificate)
	return helpers.AsSHA256(string(b))
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
		{Name: "HUMIO_NODE_ID", Value: "0"}, // PDF render service doesn't need unique node IDs
	}

	envVars = append(envVars, hprs.Spec.EnvironmentVariables...) // Use correct field

	vols, mounts := r.tlsVolumesAndMounts(hprs, &envVars)

	vols = append(vols, hprs.Spec.Volumes...)          // Use correct field
	mounts = append(mounts, hprs.Spec.VolumeMounts...) // Use correct field

	// Deduplicate first, then sort to ensure stable ordering
	envVars = dedupEnvVars(envVars)
	envVars = sortEnv(envVars)
	return envVars, dedupVolumes(vols), dedupVolumeMounts(mounts)
}

// cleanResources removes 0-valued CPU/Memory requests & limits so the object
// stored by the API server equals the one we later rebuild in reconcile loops.
func cleanResources(rr corev1.ResourceRequirements) corev1.ResourceRequirements {
	clean := corev1.ResourceRequirements{}

	// Requests
	if len(rr.Requests) > 0 {
		for k, v := range rr.Requests {
			if !v.IsZero() {
				if clean.Requests == nil {
					clean.Requests = corev1.ResourceList{}
				}
				clean.Requests[k] = v.DeepCopy()
			}
		}
	}
	// Limits
	if len(rr.Limits) > 0 {
		for k, v := range rr.Limits {
			if !v.IsZero() {
				if clean.Limits == nil {
					clean.Limits = corev1.ResourceList{}
				}
				clean.Limits[k] = v.DeepCopy()
			}
		}
	}
	return clean
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
		Args:  []string{"--port", fmt.Sprintf("%d", port)},
		Ports: []corev1.ContainerPort{
			{Name: "http", ContainerPort: port, Protocol: corev1.ProtocolTCP},
		},
		Env:          envVars,
		VolumeMounts: mounts,
		Resources:    cleanResources(hprs.Spec.Resources),
	}

	// Always set ImagePullPolicy to avoid reconciliation loops
	if hprs.Spec.ImagePullPolicy != "" {
		container.ImagePullPolicy = hprs.Spec.ImagePullPolicy
	} else {
		// Default to PullIfNotPresent for PDF render service images
		container.ImagePullPolicy = corev1.PullIfNotPresent
	}

	// Add TLS arguments when TLS is enabled
	if helpers.TLSEnabledForHPRS(hprs) {
		container.Args = append(container.Args,
			"--tls-cert=/etc/tls/tls.crt",
			"--tls-key=/etc/tls/tls.key",
		)

		// Add CA file argument if custom CA is specified
		if hprs.Spec.TLS.CASecretName != "" {
			container.Args = append(container.Args, "--ca-file=/etc/ca/ca.crt")
		}
	}

	// Determine scheme based on TLS configuration
	scheme := "http"
	if helpers.TLSEnabledForHPRS(hprs) {
		scheme = "https"
	}

	defaultLivenessProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   humiov1alpha1.DefaultPdfRenderServiceLiveness,
				Port:   intstr.FromInt(int(port)),
				Scheme: corev1.URIScheme(strings.ToUpper(scheme)),
			},
		},
		InitialDelaySeconds: 60, PeriodSeconds: 10, TimeoutSeconds: 5, FailureThreshold: 3, SuccessThreshold: 1,
	}
	container.LivenessProbe = hprs.Spec.LivenessProbe
	if container.LivenessProbe == nil {
		container.LivenessProbe = defaultLivenessProbe
	}

	defaultReadinessProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   humiov1alpha1.DefaultPdfRenderServiceReadiness,
				Port:   intstr.FromInt(int(port)),
				Scheme: corev1.URIScheme(strings.ToUpper(scheme)),
			},
		},
		InitialDelaySeconds: 10, PeriodSeconds: 10, TimeoutSeconds: 5, FailureThreshold: 3, SuccessThreshold: 1,
	}
	container.ReadinessProbe = hprs.Spec.ReadinessProbe
	if container.ReadinessProbe == nil {
		container.ReadinessProbe = defaultReadinessProbe
	}

	if hprs.Spec.SecurityContext != nil { // Use correct field
		container.SecurityContext = hprs.Spec.SecurityContext
	}

	r.Log.Info("Creating container with resources",
		"memoryRequests", container.Resources.Requests.Memory().String(),
		"cpuRequests", container.Resources.Requests.Cpu().String(),
		"memoryLimits", container.Resources.Limits.Memory().String(),
		"cpuLimits", container.Resources.Limits.Cpu().String())

	return container
}

// constructDesiredService creates a new Service object for the HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) constructDesiredService(hprs *humiov1alpha1.HumioPdfRenderService) *corev1.Service {
	labels := labelsForHumioPdfRenderService(hprs.ObjectMeta.Name)
	port := getPdfRenderServicePort(hprs)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        childName(hprs),
			Namespace:   hprs.ObjectMeta.Namespace,
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

	if !helpers.TLSEnabledForHPRS(hprs) {
		return vols, mounts
	}

	// Server certificate configuration
	serverCertSecretName := fmt.Sprintf("%s-tls", childName(hprs))

	// Add only boolean TLS switch environment variable
	*env = append(*env, corev1.EnvVar{Name: pdfRenderUseTLSEnvVar, Value: "true"})

	// Add server certificate volume
	vols = append(vols, corev1.Volume{
		Name: pdfTLSCertVolumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: serverCertSecretName,
				DefaultMode: func() *int32 {
					mode := int32(0440)
					return &mode
				}(),
			},
		},
	})

	// Add server certificate mount
	mounts = append(mounts, corev1.VolumeMount{
		Name:      pdfTLSCertVolumeName,
		MountPath: pdfTLSCertMountPath,
		ReadOnly:  true,
	})

	// CA certificate configuration - for communicating with HumioCluster
	if hprs.Spec.TLS.CASecretName != "" {
		// Add CA certificate volume
		vols = append(vols, corev1.Volume{
			Name: caCertVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: hprs.Spec.TLS.CASecretName,
					Items: []corev1.KeyToPath{
						{
							Key:  "ca.crt",
							Path: "ca.crt",
						},
					},
					DefaultMode: func() *int32 {
						mode := int32(0440)
						return &mode
					}(),
				},
			},
		})

		// Add CA certificate mount
		mounts = append(mounts, corev1.VolumeMount{
			Name:      caCertVolumeName,
			MountPath: caCertMountPath,
			ReadOnly:  true,
		})
	}

	return vols, mounts
}

// EnsureValidCAIssuerForHPRS uses the shared generic helper to ensure a valid CA Issuer exists
func (r *HumioPdfRenderServiceReconciler) EnsureValidCAIssuerForHPRS(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	if !helpers.TLSEnabledForHPRS(hprs) {
		return nil
	}

	r.Log.Info("checking for an existing valid CA Issuer")

	config := GenericCAIssuerConfig{
		Namespace:    hprs.ObjectMeta.Namespace,
		Name:         childName(hprs),
		Labels:       labelsForHumioPdfRenderService(hprs.ObjectMeta.Name),
		CASecretName: getCASecretNameForHPRS(hprs),
	}

	return EnsureValidCAIssuerGeneric(ctx, r.Client, hprs, r.Scheme, config, r.Log)
}

// ensureHprsServerCertificate follows the exact same pattern as HumioCluster's ensureHumioNodeCertificates
func (r *HumioPdfRenderServiceReconciler) ensureHprsServerCertificate(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	if !helpers.TLSEnabledForHPRS(hprs) {
		return nil
	}

	certificateName := fmt.Sprintf("%s-tls", childName(hprs))
	certificate := r.constructHprsCertificate(hprs)

	// Calculate desired certificate hash following HumioCluster pattern
	certificateForHash := certificate.DeepCopy()
	certificateForHash.Annotations = nil
	certificateForHash.ResourceVersion = ""
	b, _ := json.Marshal(certificateForHash)
	desiredCertificateHash := helpers.AsSHA256(string(b))

	existingCertificate := &cmapi.Certificate{}
	err := r.Client.Get(ctx, types.NamespacedName{Namespace: hprs.ObjectMeta.Namespace, Name: certificateName}, existingCertificate)
	if k8serrors.IsNotFound(err) {
		certificate.Annotations[HPRSCertificateHashAnnotation] = desiredCertificateHash
		r.Log.Info(fmt.Sprintf("creating server certificate with name %s", certificate.Name))
		if err := controllerutil.SetControllerReference(hprs, &certificate, r.Scheme); err != nil {
			return r.logErrorAndReturn(err, "could not set controller reference")
		}
		r.Log.Info(fmt.Sprintf("creating server certificate: %s", certificate.Name))
		if err := r.Client.Create(ctx, &certificate); err != nil {
			return r.logErrorAndReturn(err, "could not create server certificate")
		}
		return nil
	}
	if err != nil {
		return r.logErrorAndReturn(err, "could not get server certificate")
	}

	// Check if we should update the existing certificate
	currentCertificateHash := existingCertificate.Annotations[HPRSCertificateHashAnnotation]
	if currentCertificateHash != desiredCertificateHash {
		r.Log.Info(fmt.Sprintf("server certificate %s doesn't have expected hash, got: %s, expected: %s",
			existingCertificate.Name, currentCertificateHash, desiredCertificateHash))

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			currentCertificate := &cmapi.Certificate{}
			err := r.Client.Get(ctx, types.NamespacedName{
				Namespace: existingCertificate.Namespace,
				Name:      existingCertificate.Name}, currentCertificate)
			if err != nil {
				return err
			}

			desiredCertificate := r.constructHprsCertificate(hprs)
			desiredCertificate.ResourceVersion = currentCertificate.ResourceVersion
			if desiredCertificate.Annotations == nil {
				desiredCertificate.Annotations = make(map[string]string)
			}
			desiredCertificate.Annotations[HPRSCertificateHashAnnotation] = desiredCertificateHash
			r.Log.Info(fmt.Sprintf("updating server certificate with name %s", desiredCertificate.Name))
			if err := controllerutil.SetControllerReference(hprs, &desiredCertificate, r.Scheme); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			return r.Client.Update(ctx, &desiredCertificate)
		})
		if err != nil {
			if !k8serrors.IsNotFound(err) {
				return r.logErrorAndReturn(err, "failed to update server certificate")
			}
		}
	}
	return nil
}

// constructHprsCertificate builds the desired Certificate object for HPRS.
func (r *HumioPdfRenderServiceReconciler) constructHprsCertificate(hprs *humiov1alpha1.HumioPdfRenderService) cmapi.Certificate {
	certificateName := fmt.Sprintf("%s-tls", childName(hprs))
	dnsNames := []string{
		childName(hprs), // service name
		fmt.Sprintf("%s.%s", childName(hprs), hprs.ObjectMeta.Namespace),                   // service.namespace
		fmt.Sprintf("%s.%s.svc", childName(hprs), hprs.ObjectMeta.Namespace),               // service.namespace.svc
		fmt.Sprintf("%s.%s.svc.cluster.local", childName(hprs), hprs.ObjectMeta.Namespace), // FQDN
	}
	if hprs.Spec.TLS != nil && len(hprs.Spec.TLS.ExtraHostnames) > 0 {
		dnsNames = append(dnsNames, hprs.Spec.TLS.ExtraHostnames...)
	}

	return cmapi.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:        certificateName,
			Namespace:   hprs.ObjectMeta.Namespace,
			Labels:      labelsForHumioPdfRenderService(hprs.ObjectMeta.Name),
			Annotations: map[string]string{},
		},
		Spec: cmapi.CertificateSpec{
			DNSNames:   dnsNames,
			SecretName: certificateName,
			IssuerRef: cmmeta.ObjectReference{
				Name: childName(hprs),
				Kind: "Issuer",
			},
			Usages: []cmapi.KeyUsage{
				cmapi.UsageDigitalSignature,
				cmapi.UsageKeyEncipherment,
				cmapi.UsageServerAuth,
			},
		},
	}
}

// validateTLSConfiguration ensures a valid TLS configuration for the PDF render service.
// This checks the server certificate first, then the CA certificate, following test expectations.
func (r *HumioPdfRenderServiceReconciler) validateTLSConfiguration(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	// Double-check TLS configuration to ensure we never validate TLS when it's explicitly disabled
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && !*hprs.Spec.TLS.Enabled {
		// TLS is explicitly disabled - never validate certificates
		return nil
	}

	if !helpers.TLSEnabledForHPRS(hprs) {
		return nil
	}

	// Step 1: Validate server certificate secret existence and keys FIRST.
	// This ensures we fail early with the expected "TLS-certificate" error message if the server cert is missing.
	serverCertSecretName := fmt.Sprintf("%s-tls", childName(hprs))
	var tlsSecret corev1.Secret
	if err := r.Client.Get(ctx, types.NamespacedName{Name: serverCertSecretName, Namespace: hprs.ObjectMeta.Namespace}, &tlsSecret); err != nil {
		if k8serrors.IsNotFound(err) {
			if helpers.UseCertManager() {
				// When using cert-manager, the certificate creation might still be in progress
				// Check if the Certificate resource exists first
				certificateName := fmt.Sprintf("%s-tls", childName(hprs))
				var cert cmapi.Certificate
				if certErr := r.Client.Get(ctx, types.NamespacedName{Name: certificateName, Namespace: hprs.ObjectMeta.Namespace}, &cert); certErr != nil {
					if k8serrors.IsNotFound(certErr) {
						// Certificate resource doesn't exist, this is a real error
						return fmt.Errorf("TLS is enabled for HPRS %s/%s, but its server TLS-certificate secret %s was not found: %w", hprs.ObjectMeta.Namespace, hprs.ObjectMeta.Name, serverCertSecretName, err)
					}
					// Other error getting certificate
					return fmt.Errorf("failed to check Certificate resource %s for HPRS %s/%s: %w", certificateName, hprs.ObjectMeta.Namespace, hprs.ObjectMeta.Name, certErr)
				}
				// Certificate exists but secret doesn't - cert-manager is still working
				r.Log.Info("Certificate resource exists but secret is not ready yet, cert-manager is still processing",
					"certificateName", certificateName, "secretName", serverCertSecretName, "hprsName", hprs.ObjectMeta.Name)
				// Return a non-fatal error that will cause requeue
				return fmt.Errorf("TLS certificate secret %s is not ready yet, cert-manager is still processing the certificate", serverCertSecretName)
			} else {
				// When not using cert-manager, this is a configuration error
				return fmt.Errorf("TLS is enabled for HPRS %s/%s, but its server TLS-certificate secret %s was not found and cert-manager is not enabled: %w", hprs.ObjectMeta.Namespace, hprs.ObjectMeta.Name, serverCertSecretName, err)
			}
		}
		return fmt.Errorf("failed to get HPRS server TLS-certificate secret %s for HPRS %s/%s: %w", serverCertSecretName, hprs.ObjectMeta.Namespace, hprs.ObjectMeta.Name, err)
	}
	if _, ok := tlsSecret.Data[corev1.TLSCertKey]; !ok {
		return fmt.Errorf("HPRS server TLS-certificate secret %s for HPRS %s/%s is missing key %s", serverCertSecretName, hprs.ObjectMeta.Namespace, hprs.ObjectMeta.Name, corev1.TLSCertKey)
	}
	if _, ok := tlsSecret.Data[corev1.TLSPrivateKeyKey]; !ok {
		return fmt.Errorf("HPRS server TLS-certificate secret %s for HPRS %s/%s is missing key %s", serverCertSecretName, hprs.ObjectMeta.Namespace, hprs.ObjectMeta.Name, corev1.TLSPrivateKeyKey)
	}

	// Step 2: Only after server certificate is valid, ensure CA secret is valid
	if err := r.ensureValidCASecretForHPRS(ctx, hprs); err != nil {
		return err
	}

	return nil
}

// ensureValidCASecretForHPRS ensures a valid CA secret exists for the HumioPdfRenderService.
// It follows the same pattern as HumioCluster's ensureValidCASecret for consistency.
// Returns an error if TLS is enabled but CA secret validation or creation fails.
func (r *HumioPdfRenderServiceReconciler) ensureValidCASecretForHPRS(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	// Early return if TLS is not enabled
	if !helpers.TLSEnabledForHPRS(hprs) {
		return nil
	}

	// Validate input parameters
	if hprs == nil {
		return r.logErrorAndReturn(fmt.Errorf("HumioPdfRenderService cannot be nil"), "invalid input parameter")
	}

	caSecretName := getCASecretNameForHPRS(hprs)
	r.Log.Info("checking for existing CA secret", "secretName", caSecretName, "namespace", hprs.ObjectMeta.Namespace)

	// Check if existing CA secret is valid
	caSecretIsValid, err := validCASecret(ctx, r.Client, hprs.ObjectMeta.Namespace, caSecretName)
	if caSecretIsValid {
		r.Log.Info("found valid CA secret, nothing more to do", "secretName", caSecretName)
		return nil
	}

	// Handle case where user specified their own custom CA secret
	if helpers.UseExistingCAForHPRS(hprs) {
		return r.logErrorAndReturn(
			fmt.Errorf("configured to use existing CA secret %s, but validation failed: %w", caSecretName, err),
			"specified CA secret invalid")
	}

	// Handle validation errors that are not "not found"
	if err != nil && !k8serrors.IsNotFound(err) {
		return r.logErrorAndReturn(err, "could not validate CA secret")
	}

	// Generate new CA certificate
	r.Log.Info("generating new CA certificate for PDF render service", "namespace", hprs.ObjectMeta.Namespace)
	caCert, err := GenerateCACertificate()
	if err != nil {
		return r.logErrorAndReturn(err, "could not generate new CA certificate")
	}

	// Validate generated certificate
	if len(caCert.Certificate) == 0 || len(caCert.Key) == 0 {
		return r.logErrorAndReturn(fmt.Errorf("generated CA certificate is invalid"), "invalid CA certificate generated")
	}

	// Create CA secret data
	caSecretData := map[string][]byte{
		corev1.TLSCertKey:       caCert.Certificate,
		corev1.TLSPrivateKeyKey: caCert.Key,
	}

	// Construct and create the CA secret
	caSecret := kubernetes.ConstructSecret(hprs.ObjectMeta.Name, hprs.ObjectMeta.Namespace, caSecretName, caSecretData, nil, nil)
	if err := controllerutil.SetControllerReference(hprs, caSecret, r.Scheme); err != nil {
		return r.logErrorAndReturn(err, "could not set controller reference")
	}

	r.Log.Info("creating CA secret for PDF render service", "secretName", caSecret.Name, "namespace", caSecret.Namespace)
	if err := r.Client.Create(ctx, caSecret); err != nil {
		// Handle case where secret was created by another reconciliation loop
		if k8serrors.IsAlreadyExists(err) {
			r.Log.Info("CA secret already exists, continuing", "secretName", caSecret.Name)
			return nil
		}
		return r.logErrorAndReturn(err, "could not create CA secret")
	}

	r.Log.Info("successfully created CA secret for PDF render service", "secretName", caSecret.Name)
	return nil
}

// childName generates the name for the child resources (Deployment, Service) of the HumioPdfRenderService.
func childName(hprs *humiov1alpha1.HumioPdfRenderService) string {
	return helpers.PdfRenderServiceChildName(hprs.ObjectMeta.Name)
}

// labelsForHumioPdfRenderService returns the labels for the HumioPdfRenderService resources.
func labelsForHumioPdfRenderService(name string) map[string]string {
	// Kubernetes label values cannot exceed 63 characters
	const maxLabelLength = 63
	labelValue := name
	if len(labelValue) > maxLabelLength {
		labelValue = labelValue[:maxLabelLength]
	}
	return map[string]string{"app": "humio-pdf-render-service", "humio-pdf-render-service": labelValue}
}

// getCASecretNameForHPRS returns the name of the CA secret for the PDF render service
func getCASecretNameForHPRS(hprs *humiov1alpha1.HumioPdfRenderService) string {
	return helpers.GetCASecretNameForHPRS(hprs)
}

func (r *HumioPdfRenderServiceReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

func (r *HumioPdfRenderServiceReconciler) updateStatus(
	ctx context.Context,
	hprs *humiov1alpha1.HumioPdfRenderService,
	targetState string,
	reconcileErr error,
) error {
	log := r.Log.WithValues("function", "updateStatus", "targetState", targetState)

	// Persist the new status with conflict-retry
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := &humiov1alpha1.HumioPdfRenderService{}
		if err := r.Client.Get(ctx, client.ObjectKeyFromObject(hprs), current); err != nil {
			return err
		}

		// Build the desired status using the current object's generation
		desired := current.Status.DeepCopy()
		desired.ObservedGeneration = current.ObjectMeta.Generation
		desired.State = targetState

		// Fetch current deployment status to get accurate ReadyReplicas
		deploymentName := helpers.PdfRenderServiceChildName(current.ObjectMeta.Name)
		deployment := &appsv1.Deployment{}
		if err := r.Client.Get(ctx, types.NamespacedName{
			Name:      deploymentName,
			Namespace: current.ObjectMeta.Namespace,
		}, deployment); err != nil {
			if k8serrors.IsNotFound(err) {
				desired.ReadyReplicas = 0
			} else {
				// If we can't fetch deployment, keep current value
				log.Error(err, "Failed to fetch deployment for ReadyReplicas", "deploymentName", deploymentName)
			}
		} else {
			desired.ReadyReplicas = deployment.Status.ReadyReplicas
		}

		if reconcileErr != nil {
			desired.Message = fmt.Sprintf("Reconciliation failed: %v", reconcileErr)
		} else if targetState == humiov1alpha1.HumioPdfRenderServiceStateRunning ||
			targetState == humiov1alpha1.HumioPdfRenderServiceStateScaledDown {
			desired.Message = ""
		}

		// Create a temporary object to set conditions on the desired status
		tempHPRS := &humiov1alpha1.HumioPdfRenderService{Status: *desired}

		setStatusCondition(tempHPRS, buildCondition(
			string(humiov1alpha1.HumioPdfRenderServiceAvailable),
			targetState == humiov1alpha1.HumioPdfRenderServiceStateRunning,
			"DeploymentAvailable", "DeploymentUnavailable",
		))

		setStatusCondition(tempHPRS, buildCondition(
			string(humiov1alpha1.HumioPdfRenderServiceProgressing),
			targetState == humiov1alpha1.HumioPdfRenderServiceStateConfiguring,
			"Configuring", "ReconciliationComplete",
			desired.Message,
		))

		setStatusCondition(tempHPRS, buildCondition(
			string(humiov1alpha1.HumioPdfRenderServiceDegraded),
			targetState == humiov1alpha1.HumioPdfRenderServiceStateConfigError || reconcileErr != nil,
			"ConfigError", "ReconciliationSucceeded",
			desired.Message,
		))

		setStatusCondition(tempHPRS, buildCondition(
			string(humiov1alpha1.HumioPdfRenderServiceScaledDown),
			targetState == humiov1alpha1.HumioPdfRenderServiceStateScaledDown,
			"ScaledDown", "NotScaledDown",
		))

		// Apply the updated conditions back to desired
		desired = &tempHPRS.Status

		// Short-circuit if nothing actually changed
		if reflect.DeepEqual(current.Status, *desired) {
			return nil
		}

		current.Status = *desired
		if err := r.Client.Status().Update(ctx, current); err != nil {
			if k8serrors.IsConflict(err) {
				log.Info("Status conflict – retrying")
			} else {
				log.Error(err, "Failed to update status")
			}
			return err
		}
		log.Info("Status updated", "observedGeneration", desired.ObservedGeneration, "state", desired.State)
		return nil
	})
}

// helpers
func buildCondition(condType string, trueStatus bool, trueReason, falseReason string, msg ...string) metav1.Condition {
	status := metav1.ConditionFalse
	reason := falseReason
	if trueStatus {
		status = metav1.ConditionTrue
		reason = trueReason
	}
	message := ""
	if len(msg) > 0 {
		message = msg[0]
	}
	return metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}
}

// setStatusCondition sets the given condition in the status of the HumioPdfRenderService.
func setStatusCondition(hprs *humiov1alpha1.HumioPdfRenderService, condition metav1.Condition) {
	meta.SetStatusCondition(&hprs.Status.Conditions, condition)
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
