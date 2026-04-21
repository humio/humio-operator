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
	"strings"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	"github.com/humio/humio-operator/internal/registries"
)

const (
	PackagesDownloadPath = "/tmp/packages"
)

// HumioPackageReconciler reconciles a HumioPackage object
type HumioPackageReconciler struct {
	client.Client
	CommonConfig
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
	HTTPClient  registries.HTTPClientInterface
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackages/finalizers,verbs=update

func (p *HumioPackageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error
	if p.Namespace != "" && p.Namespace != req.Namespace {
		return reconcile.Result{}, nil
	}

	reconcileID := kubernetes.RandomString()
	p.Log = p.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(p), "Reconcile.ID", reconcileID)
	p.Log.Info("Reconciling HumioPackage")

	// read k8s object
	hp, err := p.getK8sHumioPackage(ctx, req)
	if hp == nil || err != nil {
		return reconcile.Result{}, err
	}

	// setup humio client configuration
	cluster, err := helpers.NewCluster(ctx, p, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		return reconcile.Result{}, logErrorAndReturn(p.Log, err, "unable to obtain humio client config")
	}
	humioHttpClient := p.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	// handle delete logic
	if hp.GetDeletionTimestamp() != nil {
		return p.handlePackageDeletion(ctx, humioHttpClient, hp)
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
		p.Log.Info("Finalizer not present, adding finalizer to HumioPackage")
		hp.SetFinalizers(append(hp.GetFinalizers(), HumioFinalizer))
		err := p.Update(ctx, hp)
		p.Log.Info("Added finalizer to HumioPackage")
		return reconcile.Result{}, err
	}

	// get PackageRegistryClient
	registryClient, err := p.getPackageRegistryClient(ctx, hp)
	if err != nil || registryClient == nil {
		_ = p.setCondition(ctx, hp,
			humiov1alpha1.PackageConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageReasonConfigError,
			err.Error(), "")
		return reconcile.Result{}, logErrorAndReturn(p.Log, err, "failed to initialize registry client")
	}

	// resolve install targets to view names
	viewNames, err := hp.Spec.ResolveInstallTargets(ctx, p.Client, hp.Namespace)
	if err != nil {
		p.Log.Error(err, "failed to resolve package install targets")
		_ = p.setCondition(ctx, hp,
			humiov1alpha1.PackageConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageReasonConfigError,
			fmt.Sprintf("Error resolving install targets: %s", err), "")
		return reconcile.Result{}, err
	}

	// if package already marked as installed, confirm and return
	if p.checkPackageAlreadyInstalled(ctx, humioHttpClient, hp, viewNames) {
		return reconcile.Result{}, nil
	}

	// Validate package exists in registry
	if !registryClient.CheckPackageExists(hp) {
		p.Log.Info("Package not found in registry")
		_ = p.setCondition(ctx, hp,
			humiov1alpha1.PackageConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageReasonNotFound,
			"Package not found in registry", "")
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}
	p.Log.Info("Successfully found package in registry")

	// Download, validate and install package
	return p.downloadValidateAndInstall(ctx, humioHttpClient, hp, registryClient, viewNames, reconcileID)
}

// handlePackageDeletion handles the deletion logic for packages
func (p *HumioPackageReconciler) handlePackageDeletion(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioPackage) (ctrl.Result, error) {
	p.Log.Info("HumioPackage marked to be deleted")
	if !helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
		return reconcile.Result{}, nil
	}

	// Check for force finalize annotation
	if ShouldForceFinalize(hp) {
		p.Log.Info("Force finalize annotation detected, removing finalizer without cleanup",
			"resource", hp.Name,
			"namespace", hp.Namespace)
		hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), HumioFinalizer))
		err := p.Update(ctx, hp)
		if err != nil {
			return reconcile.Result{}, err
		}
		p.Log.Info("Finalizer removed successfully via force-finalize annotation")
		return reconcile.Result{Requeue: true}, nil
	}

	// Check if data deletion is allowed
	if !hp.Spec.AllowDataDeletion {
		return reconcile.Result{}, logErrorAndReturn(p.Log,
			fmt.Errorf("package may contain data and data deletion not enabled. Set spec.allowDataDeletion to true to allow deletion"),
			"data deletion not enabled")
	}

	p.Log.Info("HumioPackage contains finalizer so run finalize method")
	// finalize uninstalls package from views
	p.finalize(ctx, hp, humioHttpClient)
	hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), HumioFinalizer))
	err := p.Update(ctx, hp)
	if err != nil {
		return reconcile.Result{}, logErrorAndReturn(p.Log, err, "update to remove finalizer failed")
	}
	p.Log.Info("Successfully ran finalize method for HumioPackage", "package", hp.Spec.PackageName)
	return reconcile.Result{}, nil
}

// checkPackageAlreadyInstalled checks if package is already installed in all views
func (p *HumioPackageReconciler) checkPackageAlreadyInstalled(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioPackage, viewNames []string) bool {
	if hp.Status.HumioPackageName == "" {
		return false
	}

	validated := make([]string, 0, len(viewNames))
	for _, view := range viewNames {
		packageDetails, err := p.HumioClient.CheckPackage(ctx, humioHttpClient, hp, view)
		if err != nil || packageDetails == nil {
			p.Log.Error(err, "package not installed in view", "view", view)
			continue
		}
		validated = append(validated, view)
	}
	// confirmed package installed in all views
	if len(viewNames) == len(validated) {
		return true
	}
	return false
}

// downloadValidateAndInstall orchestrates the package download, validation, and installation process
func (p *HumioPackageReconciler) downloadValidateAndInstall(ctx context.Context, humioHttpClient *humioapi.Client, hp *humiov1alpha1.HumioPackage, registryClient registries.RegistryClientInterface, viewNames []string, reconcileID string) (ctrl.Result, error) {
	// Download package
	lsPackage, path, err := p.downloadPackage(ctx, hp, registryClient, reconcileID)
	if err != nil {
		return reconcile.Result{}, err
	}
	defer func() {
		if err := lsPackage.DeletePackage(); err != nil {
			p.Log.Error(err, "failed to cleanup package files")
		}
	}()

	// Validate package
	pkName, result, err := p.validatePackage(ctx, hp, humioHttpClient, lsPackage, viewNames)
	if err != nil {
		return result, err
	}

	// Install package
	return p.installAndUpdateStatus(ctx, hp, humioHttpClient, viewNames, path, pkName)
}

// downloadPackage downloads the package from the registry to a local path
func (p *HumioPackageReconciler) downloadPackage(ctx context.Context, hp *humiov1alpha1.HumioPackage, registryClient registries.RegistryClientInterface, reconcileID string) (*registries.LogscalePackage, string, error) {
	lsPackage := registries.NewLogscalePackage(hp, p.Log)
	path, err := lsPackage.BuildDownloadPath(PackagesDownloadPath, reconcileID)
	if err != nil {
		p.Log.Error(err, "failed to generate package download path")
		_ = p.setCondition(ctx, hp,
			humiov1alpha1.PackageConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageReasonConfigError,
			"Error generating download path", "")
		return nil, "", err
	}

	downloadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = registryClient.DownloadPackage(downloadCtx, hp, path)
	if err != nil {
		p.Log.Error(err, "failed to download package to local path")
		_ = p.setCondition(ctx, hp,
			humiov1alpha1.PackageConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageReasonConfigError,
			"Error downloading package from registry", "")
		return nil, "", err
	}

	return lsPackage, path, nil
}

// validatePackage validates and analyzes the package content
func (p *HumioPackageReconciler) validatePackage(ctx context.Context, hp *humiov1alpha1.HumioPackage, humioHttpClient *humioapi.Client, lsPackage *registries.LogscalePackage, viewNames []string) (string, ctrl.Result, error) {
	pkName, err := lsPackage.Validate(ctx, p.HumioClient, humioHttpClient, viewNames[0])
	if err != nil {
		p.Log.Error(err, "failed to validate package")
		msg := fmt.Sprintf("Package failed validation: %s", err)
		_ = p.setCondition(ctx, hp,
			humiov1alpha1.PackageConditionTypeReady,
			metav1.ConditionFalse,
			humiov1alpha1.PackageReasonConfigError,
			msg, "")
		// validate failures cannot usually self-heal so we can delay
		return "", reconcile.Result{RequeueAfter: 10 * time.Second}, err
	}
	p.Log.Info("Successfully validated package", "package", pkName)
	return pkName, ctrl.Result{}, nil
}

// installAndUpdateStatus installs the package in all targets and updates the status accordingly
func (p *HumioPackageReconciler) installAndUpdateStatus(ctx context.Context, hp *humiov1alpha1.HumioPackage, humioHttpClient *humioapi.Client, viewNames []string, path string, pkName string) (ctrl.Result, error) {
	failedInstalls := p.installPackage(ctx, humioHttpClient, viewNames, hp, path)

	// Check for install errors
	if len(failedInstalls) > 0 {
		// Partial failure - some targets succeeded
		if len(failedInstalls) < len(viewNames) {
			_ = p.setCondition(ctx, hp,
				humiov1alpha1.PackageConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.PackageReasonPartialFailed,
				"Package could not be installed in all targets", "")
		} else {
			// Complete failure - all targets failed
			_ = p.setCondition(ctx, hp,
				humiov1alpha1.PackageConditionTypeReady,
				metav1.ConditionFalse,
				humiov1alpha1.PackageReasonFailed,
				"Package could not be installed", "")
		}
		// Retry installing the package
		p.Log.Error(nil, fmt.Sprintf("error installing package in targets: %v", failedInstalls))
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Success - update status
	err := p.setCondition(ctx, hp,
		humiov1alpha1.PackageConditionTypeReady,
		metav1.ConditionTrue,
		humiov1alpha1.PackageReasonInstalled,
		"Package installed successfully", pkName)
	if err != nil {
		return reconcile.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Managep.
func (p *HumioPackageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPackage{}).
		Named("humiopackage").
		Complete(p)
}

func (p *HumioPackageReconciler) getK8sHumioPackage(ctx context.Context, req ctrl.Request) (*humiov1alpha1.HumioPackage, error) {
	hp := &humiov1alpha1.HumioPackage{}
	err := p.Get(ctx, req.NamespacedName, hp)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return hp, nil
}

// getPackageRegistryClient returns a RegistryClientInterface for the provided HumioPackage
func (p *HumioPackageReconciler) getPackageRegistryClient(ctx context.Context, hp *humiov1alpha1.HumioPackage) (registries.RegistryClientInterface, error) {
	var err error
	hpr := &humiov1alpha1.HumioPackageRegistry{}
	registryNamespace := hp.Namespace
	if hp.Spec.RegistryRef.Namespace != "" {
		registryNamespace = hp.Spec.RegistryRef.Namespace
	}

	hprName := types.NamespacedName{
		Name:      hp.Spec.RegistryRef.Name,
		Namespace: registryNamespace,
	}
	err = p.Get(ctx, hprName, hpr)
	if err != nil {
		return nil, fmt.Errorf("invalid HumioPackageRegistry referenced by package: %s, error: %v", hp.Spec.RegistryRef.Name, err)
	}

	client, err := registries.NewPackageRegistryClient(hpr, p.HTTPClient, p.Client, registryNamespace, p.Log)
	if err != nil {
		return nil, fmt.Errorf("could not initiate PackageRegistryClient for type: %s, error: %s", hpr.Spec.RegistryType, err)
	}

	return client, err
}

// setCondition sets a condition on the HumioPackage resource and maintains backward compatibility with the State field
//
//nolint:unparam // conditionType is kept as parameter for future use with additional condition types (e.g., Synced)
func (p *HumioPackageReconciler) setCondition(ctx context.Context,
	hp *humiov1alpha1.HumioPackage,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message, pkName string) error {

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &humiov1alpha1.HumioPackage{}
		if err := p.Get(ctx, client.ObjectKeyFromObject(hp), latest); err != nil {
			return err
		}

		// Trim the message
		if len(message) > 100 {
			message = message[0:90] + "..."
		}

		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               conditionType,
			Status:             status,
			ObservedGeneration: latest.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             reason,
			Message:            message,
		})

		// BACKWARD COMPATIBILITY: Update State and Message fields based on condition
		latest.Status.State = p.stateFromCondition(status, reason)
		latest.Status.Message = message
		// We don't want to reset the value in case of errors
		if pkName != "" {
			latest.Status.HumioPackageName = pkName
		}

		return p.Status().Update(ctx, latest)
	})
}

// stateFromCondition converts condition status and reason to legacy State field value
//
//nolint:unparam // reason parameter kept for consistency with other controllers
func (p *HumioPackageReconciler) stateFromCondition(status metav1.ConditionStatus, reason string) string {
	if status == metav1.ConditionTrue {
		return humiov1alpha1.HumioPackageStateExists
	}
	switch reason {
	case humiov1alpha1.PackageReasonNotFound:
		return humiov1alpha1.HumioPackageStateNotFound
	case humiov1alpha1.PackageReasonConfigError:
		return humiov1alpha1.HumioPackageStateConfigError
	case humiov1alpha1.PackageReasonFailed:
		return humiov1alpha1.HumioPackageStateFailed
	case humiov1alpha1.PackageReasonPartialFailed:
		return humiov1alpha1.HumioPackageStatePartialFailed
	default:
		return humiov1alpha1.HumioPackageStateUnknown
	}
}

func (p *HumioPackageReconciler) finalize(ctx context.Context, hp *humiov1alpha1.HumioPackage, humioHttpClient *humioapi.Client) {
	// if no hp.Status.HumioPackageName set return
	if hp.Status.HumioPackageName == "" {
		p.Log.Info("no hp.Status.HumioPackageName value set, probably not installed, returning")
		return
	}
	// resolve install targets to view names
	viewNames, resolveErr := hp.Spec.ResolveInstallTargets(ctx, p.Client, hp.Namespace)
	if resolveErr != nil {
		p.Log.Error(resolveErr, "Failed to resolve package install targets during finalize")
		// Don't fail finalize if we can't resolve targets - the package might have been deleted
		return
	}
	for _, view := range viewNames {
		if _, err := helpers.Retry(func() (bool, error) {
			return p.HumioClient.UninstallPackage(ctx, humioHttpClient, hp, view)
		}, 3, 1*time.Second); err != nil {
			// Check if the error is about package not being installed - this is not actually an error
			// since the desired state (package not installed) is already achieved
			if strings.Contains(err.Error(), "is not installed") {
				p.Log.Info("package is already uninstalled, continuing with finalize", "view", view, "package", hp.Spec.GetPackageName())
				continue
			}
			p.Log.Error(err, "could not uninstall package from view: %s", view)
		}
	}
}

func (p *HumioPackageReconciler) installPackage(ctx context.Context, humioHttpClient *humioapi.Client, installTo []string, hp *humiov1alpha1.HumioPackage, path string) map[string]string {
	failedInstalls := make(map[string]string)
	// install package in targets
	for _, view := range installTo {
		err := p.HumioClient.InstallPackageFromZip(ctx, humioHttpClient, hp, path, view)
		if err != nil {
			p.Log.Error(err, "Failed to install package in view", "view", view)
			failedInstalls[view] = view
			continue
		}
	}
	return failedInstalls
}
