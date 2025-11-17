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
	"k8s.io/apimachinery/pkg/types"
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
	isMarkedToBeDeleted := hp.GetDeletionTimestamp() != nil
	if isMarkedToBeDeleted {
		p.Log.Info("HumioPackage marked to be deleted")
		if helpers.ContainsElement(hp.GetFinalizers(), HumioFinalizer) {
			p.Log.Info("HumioPackage contains finalizer so run finalize method")
			// finalize uninstalls package from views
			p.finalize(ctx, hp, humioHttpClient)
			hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), HumioFinalizer))
			err := p.Update(ctx, hp)
			if err != nil {
				return reconcile.Result{}, logErrorAndReturn(p.Log, err, "update to remove finalizer failed")
			}
			p.Log.Info("Successfully ran finalize method for HumioPackage", "package", hp.Spec.PackageName)
			// work completed, return
			return reconcile.Result{}, nil
		}
		// finalizer not present, return
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	p.Log.Info("Checking if HumioPackage requires finalizer")
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
		_ = p.setState(ctx, humiov1alpha1.HumioPackageStateConfigError, err.Error(), "", hp)
		return reconcile.Result{}, logErrorAndReturn(p.Log, err, "failed to initialize registry client")
	}

	// resolve install targets to view names
	viewNames, err := hp.Spec.ResolveInstallTargets(ctx, p.Client, hp.Namespace)
	if err != nil {
		p.Log.Error(err, "failed to resolve package install targets")
		_ = p.setState(ctx, humiov1alpha1.HumioPackageStateConfigError, fmt.Sprintf("Error resolving install targets: %s", err), "", hp)
		return reconcile.Result{}, err
	}

	// if package already marked as installed, confirm and return
	if hp.Status.HumioPackageName != "" {
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
			return reconcile.Result{}, nil
		}
	}

	// check package exists
	if !registryClient.CheckPackageExists(hp) {
		p.Log.Info("Package not found in registry")
		_ = p.setState(ctx, humiov1alpha1.HumioPackageStateNotFound, "Package not found in registry", "", hp)
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}
	p.Log.Info("Successfully found package in registry")

	// download package
	lsPackage := registries.NewLogscalePackage(hp, p.Log)
	path, err := lsPackage.BuildDownloadPath(PackagesDownloadPath, reconcileID)
	if err != nil {
		p.Log.Error(err, "failed to generate package download path")
		_ = p.setState(ctx, humiov1alpha1.HumioPackageStateConfigError, "Error generating download path", "", hp)
		return reconcile.Result{}, err
	}
	downloadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err = registryClient.DownloadPackage(downloadCtx, hp, path)
	if err != nil {
		p.Log.Error(err, "failed to download package to local path")
		_ = p.setState(ctx, humiov1alpha1.HumioPackageStateConfigError, "Error downloading package from registry", "", hp)
		return reconcile.Result{}, err
	}
	defer func() {
		if err := lsPackage.DeletePackage(); err != nil {
			p.Log.Error(err, "failed to cleanup package files")
		}
	}()

	// validate/analyze package content
	pkName, err := lsPackage.Validate(ctx, p.HumioClient, humioHttpClient, viewNames[0])
	if err != nil {
		p.Log.Error(err, "failed to validate package")
		msg := fmt.Sprintf("Package failed validation: %s", err)
		_ = p.setState(ctx, humiov1alpha1.HumioPackageStateConfigError, msg, "", hp)
		// validate failures cannot usually self-heal so we can delay
		return reconcile.Result{RequeueAfter: 10 * time.Second}, nil
	}
	p.Log.Info("Successfully validated package", "package", pkName)

	// install package in targets
	failedInstalls := p.installPackage(ctx, humioHttpClient, viewNames, hp, path)
	// we have install errors, set debug message on status and then return
	if len(failedInstalls) > 0 {
		// not all failed
		if len(failedInstalls) < len(viewNames) {
			_ = p.setState(ctx, humiov1alpha1.HumioPackageStatePartialFailed, "Package could not be installed in all targets", "", hp)
		} else {
			_ = p.setState(ctx, humiov1alpha1.HumioPackageStateFailed, "Package could not be installed", "", hp)
		}
		// we want to retry installing the package
		p.Log.Error(err, fmt.Sprintf("error installing package in targets: %v", failedInstalls))
		return reconcile.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// update status to success state
	err = p.setState(ctx, humiov1alpha1.HumioPackageStateExists, "Package installed successfully", pkName, hp)
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

func (p *HumioPackageReconciler) setState(ctx context.Context, state, message, pkName string, hp *humiov1alpha1.HumioPackage) error {
	if hp.Status.State == state && hp.Status.Message == message && hp.Status.HumioPackageName == pkName {
		return nil
	}
	// trim the err message
	if len(message) > 100 {
		message = message[0:90] + "..."
	}

	p.Log.Info(fmt.Sprintf("setting HumioPackage state to: %s, message to: %s", state, message))
	hp.Status.State = state
	hp.Status.Message = message
	// we don't want to reset the value in case of errors
	if pkName != "" {
		hp.Status.HumioPackageName = pkName
	}
	return p.Status().Update(ctx, hp)
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
