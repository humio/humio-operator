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

package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

// HumioBootstrapTokenReconciler reconciles a HumioBootstrapToken object
type HumioBootstrapTokenReconciler struct {
	client.Client
	BaseLogger logr.Logger
	Log        logr.Logger
	Namespace  string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=HumioBootstrapTokens,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=HumioBootstrapTokens/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=HumioBootstrapTokens/finalizers,verbs=update

// Reconcile runs the reconciler for a HumioBootstrapToken object
func (r *HumioBootstrapTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioBootstrapToken")

	// Fetch the HumioBootstrapToken
	hbt := &humiov1alpha1.HumioBootstrapToken{}
	if err := r.Get(ctx, req.NamespacedName, hbt); err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: time.Second * 60}, nil
}

func (r *HumioBootstrapTokenReconciler) execCommand(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken) error {
	return nil
}

func (r *HumioBootstrapTokenReconciler) createPod(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken) error {
	existingPod := &corev1.Pod{}
	humioCluster := &humiov1alpha1.HumioCluster{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hbt.Namespace,
		Name:      hbt.Spec.ManagedClusterName,
	}, humioCluster); err != nil {
		if k8serrors.IsNotFound(err) {
			humioCluster = nil
		}
	}
	humioBootstrapTokenConfig := NewHumioBootstrapTokenConfig(hbt, humioCluster)
	pod, err := ConstructBootstrapPod(&humioBootstrapTokenConfig)
	if err != nil {
		return r.logErrorAndReturn(err, "could not construct pod")
	}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, existingPod); err != nil {
		if k8serrors.IsNotFound(err) {
			if err := controllerutil.SetControllerReference(hbt, pod, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			if err := r.Create(ctx, pod); err != nil {
				return r.logErrorAndReturn(err, "could not create pod")
			}
		}
	}
	return nil
}

func (r *HumioBootstrapTokenReconciler) deletePod(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken) error {
	existingPod := &corev1.Pod{}
	humioBootstrapTokenConfig := NewHumioBootstrapTokenConfig(hbt)
	pod, err := ConstructBootstrapPod(&humioBootstrapTokenConfig)
	if err != nil {
		return r.logErrorAndReturn(err, "could not construct pod")
	}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Name,
	}, existingPod); err != nil {
		if !k8serrors.IsNotFound(err) {
			if err := r.Delete(ctx, pod); err != nil {
				return r.logErrorAndReturn(err, "could not delete pod")
			}
		}
	}
	return nil
}

func (r *HumioBootstrapTokenReconciler) ensureBootstrapTokenSecret(ctx context.Context, hbt *humiov1alpha1.HumioBootstrapToken) error {
	if !hbt.Spec.TokenSecret.CreateIfMissing {
		return nil
	}

	existingSecret := &corev1.Secret{}
	humioBootstrapTokenConfig := NewHumioBootstrapTokenConfig(hbt)

	if err := r.Get(ctx, types.NamespacedName{
		Namespace: hbt.Namespace,
		Name:      humioBootstrapTokenConfig.bootstrapTokenName(),
	}, existingSecret); err != nil {
		if k8serrors.IsNotFound(err) {
			randomPass := kubernetes.RandomString()
			secretData := map[string][]byte{
				"passphrase": []byte(randomPass),
			}
			secret := kubernetes.ConstructSecret(hbt.Name, hbt.Namespace, humioBootstrapTokenConfig.bootstrapTokenName(), secretData, nil)
			if err := controllerutil.SetControllerReference(hbt, secret, r.Scheme()); err != nil {
				return r.logErrorAndReturn(err, "could not set controller reference")
			}
			r.Log.Info(fmt.Sprintf("creating secret: %s", secret.Name))
			if err := r.Create(ctx, secret); err != nil {
				return r.logErrorAndReturn(err, "could not create secret")
			}
			return nil
		} else {
			return r.logErrorAndReturn(err, "could not get secret")
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioBootstrapTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioBootstrapToken{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

func (r *HumioBootstrapTokenReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
