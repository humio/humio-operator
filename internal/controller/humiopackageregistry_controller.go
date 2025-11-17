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

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	"github.com/humio/humio-operator/internal/registries"
)

// HumioPackageRegistryReconciler reconciles a HumioPackageRegistry object
type HumioPackageRegistryReconciler struct {
	client.Client
	CommonConfig
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
	Recorder   record.EventRecorder
	HTTPClient registries.HTTPClientInterface
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackageregistries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackageregistries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humiopackageregistries/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to move the current state of the cluster closer to the desired state.
func (r *HumioPackageRegistryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" && r.Namespace != req.Namespace {
		return reconcile.Result{}, nil
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("reconciling HumioPackageRegistry")

	// reading k8s object
	hpr, err := r.getK8sHumioPackageRegistry(ctx, req)
	if hpr == nil {
		// its unexpected so we requeue
		return reconcile.Result{}, nil
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	// handle delete logic
	isMarkedToBeDeleted := hpr.GetDeletionTimestamp() != nil
	if isMarkedToBeDeleted {
		r.Log.Info("HumioPackageRegistry marked to be deleted")
		if helpers.ContainsElement(hpr.GetFinalizers(), HumioFinalizer) {
			r.Log.Info("HumioPackageRegistry contains finalizer so run finalize method")
			if err := r.finalize(); err != nil {
				_ = r.setState(ctx, humiov1alpha1.HumioPackageRegistryStateUnknown, err.Error(), hpr)
				return reconcile.Result{}, logErrorAndReturn(r.Log, err, "finalize method returned an error")
			}
			// remove finalizer
			hpr.SetFinalizers(helpers.RemoveElement(hpr.GetFinalizers(), HumioFinalizer))
			err := r.Update(ctx, hpr)
			if err != nil {
				return reconcile.Result{}, logErrorAndReturn(r.Log, err, "update to remove finalizer failed")
			}
			// work completed, return
			return reconcile.Result{}, nil
		}
		// finalizer not present, return
		return reconcile.Result{}, nil
	}

	// Add finalizer so we can run cleanup on delete
	if !helpers.ContainsElement(hpr.GetFinalizers(), HumioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to HumioPackageRegistry")
		if err := r.addFinalizer(ctx, hpr); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	// if disabled, set state and return
	if !hpr.Spec.Enabled {
		_ = r.setState(ctx, humiov1alpha1.HumioPackageRegistryStateDisabled, "Registry is disabled", hpr)
		return reconcile.Result{}, nil
	}

	// get registry client
	rClient, err := r.getPackageRegistryClient(hpr)
	if err != nil || rClient == nil {
		r.Log.Error(err, "Failed to initialize registry client")
		_ = r.setState(ctx, humiov1alpha1.HumioPackageRegistryStateConfigError, err.Error(), hpr)
		return reconcile.Result{}, err
	}

	// test client connection
	err = rClient.CheckConnection(ctx)
	if err != nil {
		r.Log.Error(err, "Failed to check registry connection")
		_ = r.setState(ctx, humiov1alpha1.HumioPackageRegistryStateConfigError, err.Error(), hpr)
		return reconcile.Result{}, err
	}

	err = r.setState(ctx, humiov1alpha1.HumioPackageRegistryStateExists, "Connection tested successfully", hpr)
	if err != nil {
		return ctrl.Result{}, err
	}

	r.Log.Info("registry is healthy and active")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioPackageRegistryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioPackageRegistry{}).
		Named("humiopackageregistry").
		Complete(r)
}

func (r *HumioPackageRegistryReconciler) addFinalizer(ctx context.Context, hpr *humiov1alpha1.HumioPackageRegistry) error {
	r.Log.Info("Adding Finalizer for HumioPackageRegistry")
	hpr.SetFinalizers(append(hpr.GetFinalizers(), HumioFinalizer))

	// Update CR
	err := r.Update(ctx, hpr)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioPackageRegistry with finalizer")
	}
	return nil
}

func (r *HumioPackageRegistryReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}

func (r *HumioPackageRegistryReconciler) finalize() error {
	var err error
	return err
}

func (r *HumioPackageRegistryReconciler) setState(ctx context.Context, state string, message string, hpr *humiov1alpha1.HumioPackageRegistry) error {
	if hpr.Status.State == state && hpr.Status.Message == message {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting HumioPackageRegistry state to: %s, message to: %s", state, message))
	hpr.Status.State = state
	hpr.Status.Message = message
	return r.Status().Update(ctx, hpr)
}

func (r *HumioPackageRegistryReconciler) getK8sHumioPackageRegistry(ctx context.Context, req ctrl.Request) (*humiov1alpha1.HumioPackageRegistry, error) {
	hpr := &humiov1alpha1.HumioPackageRegistry{}
	err := r.Get(ctx, req.NamespacedName, hpr)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return hpr, nil
}

// getRegistryClient return a RegistryClientInterface from the provided HumioPackageRegistry
func (r *HumioPackageRegistryReconciler) getPackageRegistryClient(hpr *humiov1alpha1.HumioPackageRegistry) (registries.RegistryClientInterface, error) {
	client, err := registries.NewPackageRegistryClient(hpr, r.HTTPClient, r.Client, hpr.Namespace, r.Log)
	if err != nil {
		fmt.Printf("Could not initiate PackageRegistryClient for type: %s", hpr.Spec.RegistryType)
	}
	return client, err
}
