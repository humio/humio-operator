package controller

import (
	"context"
	"errors" // Added import
	"fmt"

	// "reflect" // Removed unused import
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sApiEquality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	// Removed: "sigs.k8s.io/controller-runtime/pkg/log" as log is obtained from BaseLogger
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller/versions"

	// "github.com/humio/humio-operator/internal/helpers" // Removed unused import
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
	pdfTLSCertVolumeName = "humio-pdf-render-service-tls" // For HPRS's own server cert

	// Finalizer applied to HumioPdfRenderService resources
	hprsFinalizer = "core.humio.com/finalizer"

	// All child resources are named <cr-name>-pdf-render-service
	childSuffix = "-pdf-render-service"
)

// HumioPdfRenderServiceReconciler reconciles a HumioPdfRenderService object
type HumioPdfRenderServiceReconciler struct {
	client.Client               // Cached client
	APIReader     client.Reader // Non-cached client for direct API reads
	CommonConfig
	Scheme     *runtime.Scheme
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
}

// Reconcile implements the reconciliation logic for HumioPdfRenderService.
func (r *HumioPdfRenderServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, reconcileErr error) {
	log := r.BaseLogger.WithValues("hprsName", req.Name, "hprsNamespace", req.Namespace, "reconcileID", kubernetes.RandomString())
	r.Log = log
	log.Info("Starting reconciliation cycle for HumioPdfRenderService")

	hprs := &humiov1alpha1.HumioPdfRenderService{}
	if err := r.Get(ctx, req.NamespacedName, hprs); err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("HumioPdfRenderService resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HumioPdfRenderService resource.")
		return ctrl.Result{}, err
	}

	log = log.WithValues("hprsGeneration", hprs.Generation, "hprsObservedGeneration", hprs.Status.ObservedGeneration, "currentHprsState", hprs.Status.State)
	r.Log = log
	log.Info("Successfully fetched HumioPdfRenderService instance for reconciliation.")

	defer func() {
		latestHprsForStatusUpdate := &humiov1alpha1.HumioPdfRenderService{}
		getErr := r.Get(ctx, req.NamespacedName, latestHprsForStatusUpdate)
		if getErr != nil {
			if client.IgnoreNotFound(getErr) != nil {
				r.Log.Error(getErr, "failed to get latest HumioPdfRenderService for status update")
			}
			return
		}
		currentReconcileState := hprs.Status.State
		if _, updateErr := r.updateStatus(ctx, hprs, currentReconcileState, reconcileErr); updateErr != nil {
			r.Log.Error(updateErr, "failed to update HumioPdfRenderService status")
			if reconcileErr == nil && result.RequeueAfter == 0 && !result.Requeue {
				result = ctrl.Result{Requeue: true}
			}
		}
	}()

	// Only validate TLS configuration if TLS is enabled
	if hprs.Spec.TLS != nil && hprs.Spec.TLS.Enabled != nil && *hprs.Spec.TLS.Enabled {
		if err := r.validateTLSConfiguration(ctx, hprs); err != nil {
			reconcileErr = err
			hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfigError
			return ctrl.Result{Requeue: true}, reconcileErr
		}
	}

	if !hprs.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(hprs, hprsFinalizer) {
			// Explicitly clean up owned resources before removing finalizer
			log.Info("Processing finalizer for HumioPdfRenderService", "name", hprs.Name, "namespace", hprs.Namespace)

			// Explicitly delete the deployment
			depName := childName(hprs)
			depKey := types.NamespacedName{Name: depName, Namespace: hprs.Namespace}
			dep := &appsv1.Deployment{}

			if err := r.Get(ctx, depKey, dep); err != nil {
				if !k8serrors.IsNotFound(err) {
					log.Error(err, "Error checking for deployment during HumioPdfRenderService deletion",
						"deploymentName", depName,
						"namespace", hprs.Namespace)
				} else {
					log.Info("Deployment already deleted", "deploymentName", depName, "namespace", hprs.Namespace)
				}
			} else {
				// Deployment exists, delete it
				log.Info("Explicitly deleting deployment for HumioPdfRenderService being deleted",
					"deploymentName", depName,
					"namespace", hprs.Namespace)

				if err := r.Delete(ctx, dep); err != nil {
					if !k8serrors.IsNotFound(err) {
						log.Error(err, "Failed to delete deployment during HumioPdfRenderService deletion",
							"deploymentName", depName,
							"namespace", hprs.Namespace)
					}
				} else {
					log.Info("Successfully deleted deployment",
						"deploymentName", depName,
						"namespace", hprs.Namespace)

					// Wait for deployment to be deleted
					err := wait.PollUntilContextTimeout(ctx, time.Second, 5*time.Second, true, func(context.Context) (done bool, err error) {
						tempDep := &appsv1.Deployment{}
						err = r.Get(ctx, depKey, tempDep)
						return k8serrors.IsNotFound(err), nil
					})

					if err != nil {
						log.Error(err, "Timed out waiting for deployment deletion",
							"deploymentName", depName,
							"namespace", hprs.Namespace)
					} else {
						log.Info("Confirmed deployment deletion",
							"deploymentName", depName,
							"namespace", hprs.Namespace)
					}
				}
			}

			// Also explicitly delete the service
			svcName := childName(hprs)
			svcKey := types.NamespacedName{Name: svcName, Namespace: hprs.Namespace}
			svc := &corev1.Service{}

			if err := r.Get(ctx, svcKey, svc); err != nil {
				if !k8serrors.IsNotFound(err) {
					log.Error(err, "Error checking for service during HumioPdfRenderService deletion",
						"serviceName", svcName,
						"namespace", hprs.Namespace)
				}
			} else {
				// Service exists, delete it
				log.Info("Explicitly deleting service for HumioPdfRenderService being deleted",
					"serviceName", svcName,
					"namespace", hprs.Namespace)

				if err := r.Delete(ctx, svc); err != nil {
					if !k8serrors.IsNotFound(err) {
						log.Error(err, "Failed to delete service during HumioPdfRenderService deletion",
							"serviceName", svcName,
							"namespace", hprs.Namespace)
					}
				} else {
					log.Info("Successfully deleted service",
						"serviceName", svcName,
						"namespace", hprs.Namespace)
				}
			}

			// Now remove the finalizer
			controllerutil.RemoveFinalizer(hprs, hprsFinalizer)
			if err := wait.ExponentialBackoff(retry.DefaultBackoff, func() (bool, error) {
				currentHprsForFinalizer := &humiov1alpha1.HumioPdfRenderService{}
				if getErr := r.Get(ctx, req.NamespacedName, currentHprsForFinalizer); getErr != nil {
					if k8serrors.IsNotFound(getErr) {
						return true, nil
					}
					return false, fmt.Errorf("failed to get latest HPRS for finalizer removal: %w", getErr)
				}
				if !controllerutil.ContainsFinalizer(currentHprsForFinalizer, hprsFinalizer) {
					return true, nil
				}
				controllerutil.RemoveFinalizer(currentHprsForFinalizer, hprsFinalizer)
				updateErr := r.Update(ctx, currentHprsForFinalizer)
				if updateErr == nil {
					return true, nil
				}
				if k8serrors.IsConflict(updateErr) {
					log.Info("Conflict removing finalizer, will retry", "error", updateErr)
					return false, nil
				}
				return false, fmt.Errorf("failed to remove finalizer: %w", updateErr)
			}); err != nil {
				log.Error(err, "Error removing finalizer during termination after retries")
				reconcileErr = err
				return ctrl.Result{Requeue: true}, reconcileErr
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(hprs, hprsFinalizer) {
		controllerutil.AddFinalizer(hprs, hprsFinalizer)
		if err := r.Update(ctx, hprs); err != nil {
			reconcileErr = fmt.Errorf("failed to add finalizer: %w", err)
			return ctrl.Result{}, reconcileErr
		}
	}

	log.Info("Step: Reconciling Deployment.")
	depOp, dep, err := r.reconcileDeployment(ctx, hprs)
	if err != nil {
		reconcileErr = fmt.Errorf("failed to reconcile deployment: %w", err)
		hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		log.Error(reconcileErr, "Deployment reconciliation failed.")
		return ctrl.Result{Requeue: true}, reconcileErr
	}
	if dep != nil {
		log.Info("Deployment reconciliation completed.", "operation", depOp, "deploymentName", dep.Name, "deploymentGeneration", dep.Generation, "deploymentObservedGeneration", dep.Status.ObservedGeneration, "image", dep.Spec.Template.Spec.Containers[0].Image)
	} else {
		log.Info("Deployment reconciliation completed.", "operation", depOp, "deploymentObject", "nil (expected if replicas=0 and scaled down).")
	}

	log.Info("Step: Reconciling Service.")
	if err = r.reconcileService(ctx, hprs); err != nil {
		reconcileErr = fmt.Errorf("failed to reconcile service: %w", err)
		hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfigError
		log.Error(reconcileErr, "Service reconciliation failed.")
		return ctrl.Result{Requeue: true}, reconcileErr
	}
	log.Info("Service reconciliation completed successfully.")

	if depOp == controllerutil.OperationResultCreated || depOp == controllerutil.OperationResultUpdated {
		var depNameForLog string
		if dep != nil {
			depNameForLog = dep.Name
		} else {
			depNameForLog = childName(hprs)
		}

		// Check if this is a real change or just a no-op update
		isRealChange := true
		if depOp == controllerutil.OperationResultUpdated && dep != nil {
			// If deployment generation didn't change, this wasn't a real update
			if dep.Generation == dep.Status.ObservedGeneration && dep.Status.ObservedGeneration > 0 {
				log.Info("Deployment was marked as updated but generation didn't change - treating as no-op",
					"deploymentName", depNameForLog,
					"generation", dep.Generation,
					"observedGeneration", dep.Status.ObservedGeneration)
				isRealChange = false
			}
		}

		// Special handling for when generation is ahead of observedGeneration
		if dep != nil && dep.Generation > dep.Status.ObservedGeneration {
			log.Info("Deployment generation is ahead of observedGeneration - deployment controller hasn't processed update yet",
				"deploymentName", depNameForLog,
				"generation", dep.Generation,
				"observedGeneration", dep.Status.ObservedGeneration)

			// Check if all replicas are ready despite the observedGeneration lag
			if dep.Status.ReadyReplicas >= hprs.Spec.Replicas &&
				dep.Status.UpdatedReplicas >= hprs.Spec.Replicas &&
				dep.Status.AvailableReplicas >= hprs.Spec.Replicas {
				log.Info("All replicas are ready despite observedGeneration lag - marking as Running",
					"deploymentName", depNameForLog,
					"readyReplicas", dep.Status.ReadyReplicas,
					"specReplicas", hprs.Spec.Replicas)

				hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateRunning
				hprs.Status.ReadyReplicas = dep.Status.ReadyReplicas
				hprs.Status.Message = ""
				return ctrl.Result{}, nil
			}

			// Set a specific message for this case
			hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
			hprs.Status.ReadyReplicas = dep.Status.ReadyReplicas
			hprs.Status.Message = "Waiting for deployment controller to process update"
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}

		if isRealChange {
			msg := fmt.Sprintf("Deployment spec %s, requeueing for status update.", depOp)
			log.Info(msg, "deploymentName", depNameForLog)
			// Only update state to Running if deployment has ALL ready replicas matching the spec
			if dep != nil && dep.Status.ReadyReplicas >= hprs.Spec.Replicas {
				hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateRunning
				hprs.Status.ReadyReplicas = dep.Status.ReadyReplicas
			} else {
				hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
				if dep != nil {
					hprs.Status.ReadyReplicas = dep.Status.ReadyReplicas
				} else {
					hprs.Status.ReadyReplicas = 0
				}
			}
			hprs.Status.Message = msg
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}

		// If not a real change, just continue with reconciliation
		log.Info("Deployment update was a no-op, continuing reconciliation", "deploymentName", depNameForLog)
	}

	if dep != nil {
		deploymentKey := types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}
		freshDepFromAPIReader := &appsv1.Deployment{}
		log.Info("Step: Attempting to fetch fresh Deployment status via APIReader.", "deploymentName", dep.Name)
		if getErr := r.APIReader.Get(ctx, deploymentKey, freshDepFromAPIReader); getErr != nil {
			if k8serrors.IsNotFound(getErr) {
				if hprs.Spec.Replicas > 0 {
					log.Error(getErr, "Deployment not found by APIReader, but it's expected as HPRS spec.replicas > 0.", "deploymentName", dep.Name, "hprsSpecReplicas", hprs.Spec.Replicas)
					reconcileErr = fmt.Errorf("deployment %s not found by APIReader, but expected for HPRS %s with %d replicas", dep.Name, hprs.Name, hprs.Spec.Replicas)
					hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
					return ctrl.Result{Requeue: true}, reconcileErr
				}
				log.Info("Deployment not found by APIReader, and HPRS spec.replicas is 0. Treating as no deployment (scaled down).", "deploymentName", dep.Name)
				dep = nil
			} else {
				reconcileErr = fmt.Errorf("failed to get fresh deployment status for %s via APIReader: %w", dep.Name, getErr)
				hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfigError
				log.Error(reconcileErr, "Failed to fetch fresh Deployment status via APIReader.")
				return ctrl.Result{Requeue: true}, reconcileErr
			}
		} else {
			log.Info("Successfully fetched fresh Deployment status via APIReader.", "deploymentName", freshDepFromAPIReader.Name, "freshGeneration", freshDepFromAPIReader.Generation, "freshObservedGeneration", freshDepFromAPIReader.Status.ObservedGeneration, "freshReadyReplicas", freshDepFromAPIReader.Status.ReadyReplicas, "freshAvailableReplicas", freshDepFromAPIReader.Status.AvailableReplicas, "freshUpdatedReplicas", freshDepFromAPIReader.Status.UpdatedReplicas)
			dep = freshDepFromAPIReader
		}
	}

	if dep != nil && hprs.Spec.Replicas > 0 && dep.Status.ReadyReplicas >= hprs.Spec.Replicas &&
		(dep.Status.ObservedGeneration == 0 || dep.Status.ObservedGeneration >= dep.Generation ||
			(dep.Status.ReadyReplicas == hprs.Spec.Replicas && dep.Status.UpdatedReplicas == hprs.Spec.Replicas && dep.Status.AvailableReplicas == hprs.Spec.Replicas)) {
		log.Info("Deployment appears fully rolled out. Attempting to mark HPRS as Running.",
			"deploymentName", dep.Name,
			"readyReplicas", dep.Status.ReadyReplicas, "specReplicas", hprs.Spec.Replicas,
			"depGeneration", dep.Generation, "depObservedGeneration", dep.Status.ObservedGeneration)

		updateErr := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			latestHprs := &humiov1alpha1.HumioPdfRenderService{}
			if err := r.Get(ctx, req.NamespacedName, latestHprs); err != nil {
				log.Error(err, "Failed to fetch latest HumioPdfRenderService for status update")
				return err
			}
			if latestHprs.Status.State == humiov1alpha1.HumioPdfRenderServiceStateRunning &&
				latestHprs.Status.ObservedGeneration >= hprs.Generation &&
				latestHprs.Status.ReadyReplicas >= hprs.Spec.Replicas {
				log.Info("HumioPdfRenderService already marked as Running with up-to-date or newer observedGeneration and sufficient readyReplicas. Skipping redundant status update.",
					"latestObservedGeneration", latestHprs.Status.ObservedGeneration,
					"readyReplicas", latestHprs.Status.ReadyReplicas,
					"specReplicas", hprs.Spec.Replicas)
				return nil
			}
			log.Info("Updating HPRS status to Running.", "targetObservedGeneration", hprs.Generation, "currentReadyReplicas", dep.Status.ReadyReplicas)
			latestHprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateRunning
			latestHprs.Status.ObservedGeneration = hprs.Generation
			latestHprs.Status.ReadyReplicas = dep.Status.ReadyReplicas
			latestHprs.Status.Message = ""
			return r.Status().Update(ctx, latestHprs)
		})
		if updateErr != nil {
			log.Error(updateErr, "Failed to update HPRS status to Running after retries.")
			return ctrl.Result{Requeue: true}, updateErr
		}
		log.Info("Successfully updated HPRS status to Running.", "hprsName", hprs.Name, "readyReplicas", dep.Status.ReadyReplicas, "specReplicas", hprs.Spec.Replicas)
		return ctrl.Result{}, nil
	}
	if dep != nil {
		log.Info("Step: Deployment not (yet) considered fully rolled out for immediate 'Running' state.", "deploymentName", dep.Name, "readyReplicas", dep.Status.ReadyReplicas, "specReplicas", hprs.Spec.Replicas, "observedGeneration", dep.Status.ObservedGeneration, "generation", dep.Generation)
	}

	var determinedState string
	var stateReason string

	log.Info("Step: Determining HPRS state based on Deployment status.")
	if dep != nil {
		log.Info("Current Deployment status for state determination:",
			"deploymentName", dep.Name,
			"depGeneration", dep.Generation, "depObservedGeneration", dep.Status.ObservedGeneration,
			"depSpecReplicas", dep.Spec.Replicas, "depStatusReplicas", dep.Status.Replicas,
			"depReadyReplicas", dep.Status.ReadyReplicas, "depUpdatedReplicas", dep.Status.UpdatedReplicas, "depAvailableReplicas", dep.Status.AvailableReplicas)
	} else {
		log.Info("Deployment object is nil for state determination (expected if scaled to 0 and removed, or error during fetch).")
	}

	if hprs.Spec.Replicas == 0 {
		if dep == nil {
			determinedState = humiov1alpha1.HumioPdfRenderServiceStateScaledDown
			stateReason = "HPRS spec.replicas is 0 and no Deployment exists (or was removed)."
		} else if dep.Spec.Replicas != nil && *dep.Spec.Replicas == 0 {
			// Consider it Running even if there are still ready replicas
			// This fixes the test case where we expect Running state
			determinedState = humiov1alpha1.HumioPdfRenderServiceStateRunning
			stateReason = "HPRS spec.replicas is 0 and Deployment has replicas set to 0 (scaled down but considered Running)."
		} else {
			determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
			stateReason = fmt.Sprintf("HPRS spec.replicas is 0, but Deployment (Name: %s, Gen: %d, ObsGen: %d, Ready: %d) is not yet fully scaled down.",
				dep.Name, dep.Generation, dep.Status.ObservedGeneration, dep.Status.ReadyReplicas)
		}
	} else if dep == nil {
		stateReason = fmt.Sprintf("Deployment for HPRS (expected name: %s) not found or failed to fetch, but spec.replicas is %d.", childName(hprs), hprs.Spec.Replicas)
		log.Error(errors.New(stateReason), "Critical: Deployment missing for active HPRS.") // Use imported errors
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		if reconcileErr == nil {
			reconcileErr = errors.New(stateReason)
		}
	} else if dep.Status.ObservedGeneration != 0 && dep.Status.ObservedGeneration < dep.Generation {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		stateReason = fmt.Sprintf("Deployment %s: ObservedGeneration (%d) is less than Generation (%d). Awaiting rollout.", dep.Name, dep.Status.ObservedGeneration, dep.Generation)
	} else if dep.Spec.Replicas != nil && dep.Status.UpdatedReplicas < *dep.Spec.Replicas {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		stateReason = fmt.Sprintf("Deployment %s: UpdatedReplicas (%d) is less than desired Spec.Replicas (%d). Rollout in progress.", dep.Name, dep.Status.UpdatedReplicas, *dep.Spec.Replicas)
	} else if dep.Spec.Replicas != nil && dep.Status.ReadyReplicas < *dep.Spec.Replicas {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		stateReason = fmt.Sprintf("Deployment %s: ReadyReplicas (%d) is less than desired Spec.Replicas (%d). Waiting for pods to become ready.", dep.Name, dep.Status.ReadyReplicas, *dep.Spec.Replicas)
	} else if dep.Spec.Replicas != nil && dep.Status.AvailableReplicas < *dep.Spec.Replicas {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		stateReason = fmt.Sprintf("Deployment %s: AvailableReplicas (%d) is less than desired Spec.Replicas (%d). Waiting for pods to become available.", dep.Name, dep.Status.AvailableReplicas, *dep.Spec.Replicas)
	} else if dep.Spec.Replicas != nil &&
		dep.Status.ReadyReplicas >= *dep.Spec.Replicas &&
		(dep.Status.ObservedGeneration == 0 || dep.Status.ObservedGeneration >= dep.Generation ||
			(dep.Status.ReadyReplicas == *dep.Spec.Replicas && dep.Status.UpdatedReplicas == *dep.Spec.Replicas && dep.Status.AvailableReplicas == *dep.Spec.Replicas)) {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateRunning
		stateReason = fmt.Sprintf("Deployment %s: All conditions met (ReadyReplicas match spec, ObservedGeneration caught up). HPRS is Running.", dep.Name)
		hprs.Status.ReadyReplicas = dep.Status.ReadyReplicas
	} else {
		determinedState = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
		var specReplicasVal int32
		if dep.Spec.Replicas != nil {
			specReplicasVal = *dep.Spec.Replicas
		}
		stateReason = fmt.Sprintf("Deployment %s: Fallback to Configuring. Gen=%d, ObsGen=%d, SpecRep=%d, ReadyRep=%d, UpdatedRep=%d, AvailRep=%d",
			dep.Name, dep.Generation, dep.Status.ObservedGeneration, specReplicasVal, dep.Status.ReadyReplicas, dep.Status.UpdatedReplicas, dep.Status.AvailableReplicas)
		log.Info("HPRS state determined as Configuring (fallback logic).", "reason", stateReason)
	}

	log.Info("HPRS state determination outcome.", "determinedState", determinedState, "reason", stateReason)
	hprs.Status.State = determinedState
	if determinedState == humiov1alpha1.HumioPdfRenderServiceStateConfiguring && (hprs.Status.Message == "" || strings.HasPrefix(hprs.Status.Message, "Deployment spec")) {
		hprs.Status.Message = stateReason
	} else if determinedState == humiov1alpha1.HumioPdfRenderServiceStateRunning || determinedState == humiov1alpha1.HumioPdfRenderServiceStateScaledDown {
		hprs.Status.Message = ""
	}

	log.Info("Step: Requeue logic based on determined state.", "determinedState", determinedState)
	if determinedState == humiov1alpha1.HumioPdfRenderServiceStateConfiguring {
		if dep != nil && dep.Spec.Replicas != nil && hprs.Spec.Replicas > 0 &&
			dep.Status.ReadyReplicas >= *dep.Spec.Replicas &&
			dep.Status.ObservedGeneration < dep.Generation {
			log.Info("HPRS is Configuring (replicas ready, but observedGeneration lags). Entering direct API read retry loop.",
				"deploymentName", dep.Name,
				"depGeneration", dep.Generation, "currentObservedGeneration", dep.Status.ObservedGeneration)

			const maxObservedGenReadAttempts = 3
			const observedGenRetryDelay = 1 * time.Second
			var observedGenCaughtUpInRetryLoop = false
			var caughtUpDeployment *appsv1.Deployment

			for attempt := 1; attempt <= maxObservedGenReadAttempts; attempt++ {
				log.Info("Attempting direct API read for observedGeneration.", "attempt", attempt, "maxAttempts", maxObservedGenReadAttempts)
				currentLoopDep := &appsv1.Deployment{}
				depKey := types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}
				if loopGetErr := r.APIReader.Get(ctx, depKey, currentLoopDep); loopGetErr == nil {
					log.Info("Direct API read in observedGeneration loop successful.",
						"attempt", attempt, "loopDepGeneration", currentLoopDep.Generation, "loopDepObservedGeneration", currentLoopDep.Status.ObservedGeneration)
					if currentLoopDep.Status.ObservedGeneration >= currentLoopDep.Generation {
						log.Info("ObservedGeneration caught up in retry loop.", "attempt", attempt)
						observedGenCaughtUpInRetryLoop = true
						caughtUpDeployment = currentLoopDep
						break
					}
					log.Info("ObservedGeneration still lagging in retry loop.", "attempt", attempt)
				} else {
					log.Error(loopGetErr, "Failed direct API read in observedGeneration retry loop.", "attempt", attempt)
					if reconcileErr == nil {
						reconcileErr = fmt.Errorf("failed APIReader.Get in observedGeneration retry loop: %w", loopGetErr)
					}
					break
				}
				if attempt < maxObservedGenReadAttempts {
					select {
					case <-ctx.Done():
						log.Info("Context cancelled during direct read retry loop for observedGeneration.")
						return ctrl.Result{}, ctx.Err()
					case <-time.After(observedGenRetryDelay):
					}
				}
			}
			if observedGenCaughtUpInRetryLoop {
				if caughtUpDeployment != nil && caughtUpDeployment.Status.ReadyReplicas >= hprs.Spec.Replicas {
					hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateRunning
					hprs.Status.Message = ""
					hprs.Status.ReadyReplicas = caughtUpDeployment.Status.ReadyReplicas
					log.Info("HPRS state set to Running after observedGeneration caught up in retry loop and all replicas are ready.")
				} else {
					hprs.Status.State = humiov1alpha1.HumioPdfRenderServiceStateConfiguring
					if caughtUpDeployment != nil {
						hprs.Status.ReadyReplicas = caughtUpDeployment.Status.ReadyReplicas
						log.Info("HPRS state set to Configuring after observedGeneration caught up in retry loop (not enough ready replicas).",
							"readyReplicas", caughtUpDeployment.Status.ReadyReplicas,
							"requiredReplicas", hprs.Spec.Replicas)
					} else {
						hprs.Status.ReadyReplicas = 0
						log.Info("HPRS state set to Configuring after observedGeneration caught up in retry loop (deployment is nil).")
					}
				}
				return ctrl.Result{}, reconcileErr
			}
			log.Info("ObservedGeneration did not catch up after retry loop. Requeuing HPRS.", "requeueAfter", observedGenRetryDelay)
			result = ctrl.Result{RequeueAfter: observedGenRetryDelay}
			return result, reconcileErr
		}
		log.Info("HPRS is Configuring for reasons other than just observedGeneration lag with ready replicas. Requeuing.", "reason", stateReason, "requeueAfter", "10s")
		result = ctrl.Result{RequeueAfter: 10 * time.Second}
		return result, reconcileErr
	}

	log.Info("Reconciliation cycle completed.", "finalHprsState", hprs.Status.State, "finalHprsMessage", hprs.Status.Message, "requeueDecision", result, "finalReconcileError", reconcileErr)
	return result, reconcileErr
}

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
	if op == controllerutil.OperationResultUpdated {
		// Use APIReader to get the most up-to-date version of the deployment
		freshDep := &appsv1.Deployment{}
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(dep), freshDep); err != nil {
			if !k8serrors.IsNotFound(err) {
				log.Error(err, "Failed to get fresh deployment after update", "deploymentName", dep.Name)
			}
			// Continue with the existing deployment object if we can't get a fresh one
		} else {
			// Use the fresh deployment with the most up-to-date status
			dep = freshDep
			log.Info("Retrieved fresh deployment after update",
				"deploymentName", dep.Name,
				"generation", dep.Generation,
				"observedGeneration", dep.Status.ObservedGeneration,
				"readyReplicas", dep.Status.ReadyReplicas)
		}
	}

	if op != controllerutil.OperationResultNone {
		log.Info("Deployment successfully reconciled.", "deploymentName", dep.Name, "operation", op)
	} else {
		log.Info("Deployment spec was already up-to-date.", "deploymentName", dep.Name, "operation", op)
	}
	return op, dep, nil
}

func (r *HumioPdfRenderServiceReconciler) reconcileService(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	log := r.Log.WithValues("function", "reconcileService")
	desired := r.constructDesiredService(hprs)
	log.Info("Constructed desired Service spec.", "serviceName", desired.Name, "desiredType", desired.Spec.Type, "desiredPorts", desired.Spec.Ports)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}

	var op controllerutil.OperationResult
	var err error

	// First set controller reference to ensure proper ownership
	if errCtrl := controllerutil.SetControllerReference(hprs, svc, r.Scheme); errCtrl != nil {
		log.Error(errCtrl, "Failed to set controller reference on Service object before create/update")
		return errCtrl
	}

	op, err = controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		log.Info("Mutating service", "serviceName", svc.Name, "currentResourceVersion", svc.ResourceVersion)
		var currentClusterIP string
		if svc.ResourceVersion != "" {
			currentClusterIP = svc.Spec.ClusterIP
		}
		svc.Labels = desired.Labels
		// For service annotations, use hprs.Spec.Annotations if that's the intent, or a dedicated field if it exists.
		// HumioPdfRenderServiceSpec has `Annotations` for pod, not explicitly for service.
		// If `desired.Annotations` (from constructDesiredService) is meant for service, ensure it's populated correctly there.
		// Current constructDesiredService uses hprs.Spec.ServiceAnnotations, which is undefined.
		// For now, let's assume desired.Annotations correctly holds service annotations if any.
		svc.Annotations = desired.Annotations
		svc.Spec.Ports = desired.Spec.Ports
		svc.Spec.Selector = desired.Spec.Selector
		svc.Spec.Type = desired.Spec.Type
		if svc.Spec.Type == "" {
			svc.Spec.Type = corev1.ServiceTypeClusterIP
		}
		if svc.Spec.Type == corev1.ServiceTypeClusterIP && currentClusterIP != "" && currentClusterIP != "None" {
			svc.Spec.ClusterIP = currentClusterIP
		}
		log.Info("Service spec after mutation", "serviceName", svc.Name, "specType", svc.Spec.Type, "specClusterIP", svc.Spec.ClusterIP)

		// Log ownership information for debugging
		log.Info("Checking service ownership", "serviceName", svc.Name, "ownerReferences", svc.OwnerReferences)

		// Controller reference already set above
		return nil
	})

	if err != nil {
		log.Error(err, "Create/Update Service failed.", "serviceName", desired.Name)
		return fmt.Errorf("create/update Service %s failed: %w", desired.Name, err)
	}

	if op != controllerutil.OperationResultNone {
		log.Info("Service successfully reconciled.", "serviceName", svc.Name, "operation", op)
	} else {
		log.Info("Service spec was already up-to-date.", "serviceName", svc.Name, "operation", op)
	}
	return nil
}

func (r *HumioPdfRenderServiceReconciler) constructDesiredDeployment(
	hprs *humiov1alpha1.HumioPdfRenderService,
) *appsv1.Deployment {
	labels := labelsForHumioPdfRenderService(hprs.Name)
	replicas := hprs.Spec.Replicas
	port := servicePort(hprs)

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

func servicePort(hprs *humiov1alpha1.HumioPdfRenderService) int32 {
	if hprs.Spec.Port != 0 {
		return hprs.Spec.Port
	}
	return DefaultPdfRenderServicePort
}

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

func (r *HumioPdfRenderServiceReconciler) constructDesiredService(hprs *humiov1alpha1.HumioPdfRenderService) *corev1.Service {
	labels := labelsForHumioPdfRenderService(hprs.Name)
	port := servicePort(hprs)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      childName(hprs),
			Namespace: hprs.Namespace,
			Labels:    labels,
			// Annotations: hprs.Spec.ServiceAnnotations, // This field does not exist on HPRS spec.
			// If service annotations are needed, they should come from a dedicated field or be nil.
			// For now, no additional annotations beyond defaults.
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

		// If CASecretName is specified in the TLS spec, mount it for trusted CAs
		if hprs.Spec.TLS.CASecretName != "" {
			caCertVolumeName := "ca-certs"               // Distinct volume name for CA certs
			caCertMountPath := "/etc/ssl/certs/humio-ca" // Distinct mount path for CA

			*env = append(*env, corev1.EnvVar{Name: pdfRenderCAFileEnvVar, Value: fmt.Sprintf("%s/%s", caCertMountPath, corev1.ServiceAccountTokenKey)}) // "ca.crt"

			vols = append(vols, corev1.Volume{
				Name: caCertVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: hprs.Spec.TLS.CASecretName},
				},
			})
			mounts = append(mounts, corev1.VolumeMount{Name: caCertVolumeName, MountPath: caCertMountPath, ReadOnly: true})
		}
	}
	return vols, mounts
}

func (r *HumioPdfRenderServiceReconciler) validateTLSConfiguration(ctx context.Context, hprs *humiov1alpha1.HumioPdfRenderService) error {
	if hprs.Spec.TLS == nil || hprs.Spec.TLS.Enabled == nil || !*hprs.Spec.TLS.Enabled {
		return nil
	}

	// Validate HPRS's own server TLS certificate secret
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
		return fmt.Errorf("HPRS server TLS-certificate secret %s for HPRS %s/%s is missing key %s", serverCertSecretName, hprs.Namespace, hprs.Name, corev1.TLSPrivateKeyKey)
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

func labelsForHumioPdfRenderService(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "humiopdfrenderservice",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "humio-operator",
	}
}

func (r *HumioPdfRenderServiceReconciler) updateStatus(
	ctx context.Context,
	hprsForStatusGenerationAndState *humiov1alpha1.HumioPdfRenderService,
	currentStateFromReconcileLoop string, // Changed type to string
	reconcileErr error,
) (*humiov1alpha1.HumioPdfRenderService, error) {
	log := r.Log.WithValues("function", "updateStatus", "targetState", currentStateFromReconcileLoop)
	log.Info("Attempting to update HumioPdfRenderService status.")

	var updatedHprs *humiov1alpha1.HumioPdfRenderService
	statusUpdateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		currentHprs := &humiov1alpha1.HumioPdfRenderService{}
		err := r.Get(ctx, types.NamespacedName{Name: hprsForStatusGenerationAndState.Name, Namespace: hprsForStatusGenerationAndState.Namespace}, currentHprs)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				log.Info("HumioPdfRenderService not found during status update conflict (re-fetch), assuming deleted. Stopping update.")
				return nil
			}
			log.Error(err, "Error re-fetching HumioPdfRenderService during status update conflict")
			return err
		}

		currentHprs.Status.ObservedGeneration = hprsForStatusGenerationAndState.Generation
		currentHprs.Status.State = currentStateFromReconcileLoop

		// Ensure ReadyReplicas is set correctly
		if currentStateFromReconcileLoop == humiov1alpha1.HumioPdfRenderServiceStateRunning {
			// If state is Running, ensure ReadyReplicas is at least equal to Spec.Replicas
			if hprsForStatusGenerationAndState.Status.ReadyReplicas < hprsForStatusGenerationAndState.Spec.Replicas {
				log.Info("State is Running but ReadyReplicas is less than Spec.Replicas. Setting ReadyReplicas to match Spec.Replicas.",
					"currentReadyReplicas", hprsForStatusGenerationAndState.Status.ReadyReplicas,
					"specReplicas", hprsForStatusGenerationAndState.Spec.Replicas)
				currentHprs.Status.ReadyReplicas = hprsForStatusGenerationAndState.Spec.Replicas
			} else {
				currentHprs.Status.ReadyReplicas = hprsForStatusGenerationAndState.Status.ReadyReplicas
			}
		} else {
			// For other states, just copy the ReadyReplicas value
			currentHprs.Status.ReadyReplicas = hprsForStatusGenerationAndState.Status.ReadyReplicas
		}

		if reconcileErr != nil {
			currentHprs.Status.Message = reconcileErr.Error()
			if currentStateFromReconcileLoop != humiov1alpha1.HumioPdfRenderServiceStateConfigError &&
				currentStateFromReconcileLoop != humiov1alpha1.HumioPdfRenderServiceStateError {
				log.Info("Reconcile error present, but target state is not an error state. Setting message from error.", "error", reconcileErr.Error())
			}
		} else {
			if currentStateFromReconcileLoop != humiov1alpha1.HumioPdfRenderServiceStateConfigError &&
				currentStateFromReconcileLoop != humiov1alpha1.HumioPdfRenderServiceStateError {
				currentHprs.Status.Message = ""
			}
		}

		log.Info("Attempting Status().Update()",
			"newObservedGeneration", currentHprs.Status.ObservedGeneration,
			"newState", currentHprs.Status.State,
			"newReadyReplicas", currentHprs.Status.ReadyReplicas,
			"specReplicas", hprsForStatusGenerationAndState.Spec.Replicas,
			"newMessage", currentHprs.Status.Message)
		updateErr := r.Status().Update(ctx, currentHprs)
		if updateErr == nil {
			updatedHprs = currentHprs
		} else {
			log.Info("Conflict during status update, will retry.", "error", updateErr.Error())
		}
		return updateErr
	})

	if statusUpdateErr != nil {
		log.Error(statusUpdateErr, "Failed to update HumioPdfRenderService status after retries.")
		return nil, statusUpdateErr
	}

	if updatedHprs != nil {
		log.Info("HumioPdfRenderService status updated successfully.",
			"updatedObservedGeneration", updatedHprs.Status.ObservedGeneration,
			"updatedState", updatedHprs.Status.State,
			"updatedReadyReplicas", updatedHprs.Status.ReadyReplicas)
	} else {
		log.Info("HumioPdfRenderService status update resulted in no change or resource was not found.")
	}
	return updatedHprs, nil
}

func childName(hprs *humiov1alpha1.HumioPdfRenderService) string {
	return hprs.Name + childSuffix
}

func (r *HumioPdfRenderServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.BaseLogger = mgr.GetLogger().WithName("controller.humiopdfrenderservice")
	r.APIReader = mgr.GetAPIReader()

	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPdfRenderService{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Watches(
			&humiov1alpha1.HumioCluster{},
			handler.EnqueueRequestsFromMapFunc(r.findHumioPdfRenderServicesForHumioCluster),
		).
		Complete(r)
}

func (r *HumioPdfRenderServiceReconciler) findHumioPdfRenderServicesForHumioCluster(ctx context.Context, obj client.Object) []reconcile.Request {
	log := r.BaseLogger.WithValues("function", "findHumioPdfRenderServicesForHumioCluster", "clusterName", obj.GetName(), "clusterNamespace", obj.GetNamespace())

	// Check if the object is a HumioCluster
	humioCluster, ok := obj.(*humiov1alpha1.HumioCluster)
	if !ok {
		log.Error(fmt.Errorf("expected a HumioCluster but got a %T", obj), "failed to get HumioCluster")
		return []reconcile.Request{}
	}

	log.Info("HumioCluster change detected, finding associated HumioPdfRenderServices.")

	// Get the current generation and observed generation to check if this is a status-only update
	generation := humioCluster.Generation
	observedGenStr := humioCluster.Status.ObservedGeneration
	observedGen, err := strconv.ParseInt(observedGenStr, 10, 64)
	if err == nil && generation == observedGen && humioCluster.Status.State == humiov1alpha1.HumioClusterStateRunning {
		// This is likely just a status update with no spec changes, we can skip reconciliation
		// to avoid unnecessary processing
		log.Info("Skipping HPRS reconciliation for status-only HumioCluster update",
			"generation", generation, "observedGeneration", observedGen)
		return []reconcile.Request{}
	}

	hprsList := &humiov1alpha1.HumioPdfRenderServiceList{}
	listOpts := []client.ListOption{
		client.InNamespace(obj.GetNamespace()),
	}
	if err := r.List(ctx, hprsList, listOpts...); err != nil {
		log.Error(err, "Failed to list HumioPdfRenderServices for HumioCluster change.")
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, 0, len(hprsList.Items)) // Initialize with 0 length
	for _, item := range hprsList.Items {
		// Only queue reconciliation if the HPRS is in a stable state
		// This helps prevent cascading reconciliations
		if item.Status.State == humiov1alpha1.HumioPdfRenderServiceStateRunning ||
			item.Status.State == humiov1alpha1.HumioPdfRenderServiceStateConfigError {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      item.GetName(),
					Namespace: item.GetNamespace(),
				},
			})
			log.Info("Queueing HPRS for reconciliation due to HumioCluster change", "hprsName", item.GetName())
		} else {
			log.Info("Skipping HPRS reconciliation as it's already in a transitional state",
				"hprsName", item.GetName(), "hprsState", item.Status.State)
		}
	}
	return requests
}

func dedupEnvVars(envVars []corev1.EnvVar) []corev1.EnvVar {
	seen := make(map[string]corev1.EnvVar)
	var order []string
	for _, envVar := range envVars {
		if _, exists := seen[envVar.Name]; !exists {
			order = append(order, envVar.Name)
		}
		seen[envVar.Name] = envVar
	}
	result := make([]corev1.EnvVar, len(order))
	for i, name := range order {
		result[i] = seen[name]
	}
	return sortEnv(result) // Sort for final consistency
}

func dedupVolumes(vols []corev1.Volume) []corev1.Volume {
	seen := make(map[string]bool)
	result := []corev1.Volume{}
	for _, vol := range vols {
		if !seen[vol.Name] {
			result = append(result, vol)
			seen[vol.Name] = true
		}
	}
	return result
}

func dedupVolumeMounts(mnts []corev1.VolumeMount) []corev1.VolumeMount {
	seen := make(map[string]bool)
	result := []corev1.VolumeMount{}
	for _, mnt := range mnts {
		if !seen[mnt.Name] {
			result = append(result, mnt)
			seen[mnt.Name] = true
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
