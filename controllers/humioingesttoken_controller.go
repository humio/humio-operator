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
	"github.com/go-logr/logr"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

const humioFinalizer = "core.humio.com/finalizer" // TODO: Not only used for ingest tokens, but also parsers, repositories and views.

// HumioIngestTokenReconciler reconciles a HumioIngestToken object
type HumioIngestTokenReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens/finalizers,verbs=update

func (r *HumioIngestTokenReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioIngestToken")

	// Fetch the HumioIngestToken instance
	hit := &humiov1alpha1.HumioIngestToken{}
	err := r.Get(ctx, req.NamespacedName, hit)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	r.Log = r.Log.WithValues("Request.UID", hit.UID)

	cluster, err := helpers.NewCluster(ctx, r, hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName, hit.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioIngestTokenStateConfigError, hit)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}

	r.Log.Info("Checking if ingest token is marked to be deleted")
	// Check if the HumioIngestToken instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioIngestTokenMarkedToBeDeleted := hit.GetDeletionTimestamp() != nil
	if isHumioIngestTokenMarkedToBeDeleted {
		r.Log.Info("Ingest token marked to be deleted")
		if helpers.ContainsElement(hit.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Ingest token contains finalizer so run finalizer method")
			if err := r.finalize(ctx, cluster.Config(), req, hit); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.Log.Info("Finalizer done. Removing finalizer")
			hit.SetFinalizers(helpers.RemoveElement(hit.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, hit)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hit.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to ingest token")
		if err := r.addFinalizer(ctx, hit); err != nil {
			return reconcile.Result{}, err
		}
	}

	defer func(ctx context.Context, humioClient humio.Client, hit *humiov1alpha1.HumioIngestToken) {
		curToken, err := humioClient.GetIngestToken(cluster.Config(), req, hit)
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioIngestTokenStateUnknown, hit)
			return
		}
		emptyToken := humioapi.IngestToken{}
		if emptyToken != *curToken {
			_ = r.setState(ctx, humiov1alpha1.HumioIngestTokenStateExists, hit)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioIngestTokenStateNotFound, hit)
	}(ctx, r.HumioClient, hit)

	// Get current ingest token
	r.Log.Info("get current ingest token")
	curToken, err := r.HumioClient.GetIngestToken(cluster.Config(), req, hit)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if ingest token exists")
	}
	// If token doesn't exist, the Get returns: nil, err.
	// How do we distinguish between "doesn't exist" and "error while executing get"?
	// TODO: change the way we do errors from the API so we can get rid of this hack
	emptyToken := humioapi.IngestToken{}
	if emptyToken == *curToken {
		r.Log.Info("ingest token doesn't exist. Now adding ingest token")
		// create token
		_, err := r.HumioClient.AddIngestToken(cluster.Config(), req, hit)
		if err != nil {
			return reconcile.Result{}, r.logErrorAndReturn(err, "could not create ingest token")
		}
		r.Log.Info("created ingest token")
		return reconcile.Result{Requeue: true}, nil
	}

	// Trigger update if parser name changed
	if curToken.AssignedParser != hit.Spec.ParserName {
		r.Log.Info("parser name differs, triggering update", "Expected", hit.Spec.ParserName, "Got", curToken.AssignedParser)
		_, updateErr := r.HumioClient.UpdateIngestToken(cluster.Config(), req, hit)
		if updateErr != nil {
			return reconcile.Result{}, fmt.Errorf("could not update ingest token: %w", updateErr)
		}
	}

	err = r.ensureTokenSecretExists(ctx, cluster.Config(), req, hit, cluster)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("could not ensure token secret exists: %w", err)
	}

	// TODO: handle updates to ingest token name and repositoryName. Right now we just create the new ingest token,
	// and "leak/leave behind" the old token.
	// A solution could be to add an annotation that includes the "old name" so we can see if it was changed.
	// A workaround for now is to delete the ingest token CR and create it again.

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioIngestTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioIngestToken{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

func (r *HumioIngestTokenReconciler) finalize(ctx context.Context, config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
	_, err := helpers.NewCluster(ctx, r, hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName, hit.Namespace, helpers.UseCertManager(), true)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return r.HumioClient.DeleteIngestToken(config, req, hit)
}

func (r *HumioIngestTokenReconciler) addFinalizer(ctx context.Context, hit *humiov1alpha1.HumioIngestToken) error {
	r.Log.Info("Adding Finalizer for the HumioIngestToken")
	hit.SetFinalizers(append(hit.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(ctx, hit)
	if err != nil {
		return r.logErrorAndReturn(err, "Failed to update HumioIngestToken with finalizer")
	}
	return nil
}

func (r *HumioIngestTokenReconciler) ensureTokenSecretExists(ctx context.Context, config *humioapi.Config, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken, cluster helpers.ClusterInterface) error {
	if hit.Spec.TokenSecretName == "" {
		return nil
	}

	ingestToken, err := r.HumioClient.GetIngestToken(config, req, hit)
	if err != nil {
		return fmt.Errorf("failed to get ingest token: %w", err)
	}

	secretData := map[string][]byte{"token": []byte(ingestToken.Token)}
	desiredSecret := kubernetes.ConstructSecret(cluster.Name(), hit.Namespace, hit.Spec.TokenSecretName, secretData, hit.Spec.TokenSecretLabels)
	if err := controllerutil.SetControllerReference(hit, desiredSecret, r.Scheme()); err != nil {
		return fmt.Errorf("could not set controller reference: %w", err)
	}

	existingSecret, err := kubernetes.GetSecret(ctx, r, hit.Spec.TokenSecretName, hit.Namespace)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			err = r.Create(ctx, desiredSecret)
			if err != nil {
				return fmt.Errorf("unable to create ingest token secret for HumioIngestToken: %w", err)
			}
			r.Log.Info("successfully created ingest token secret", "TokenSecretName", hit.Spec.TokenSecretName)
			humioIngestTokenPrometheusMetrics.Counters.ServiceAccountSecretsCreated.Inc()
		}
	} else {
		// kubernetes secret exists, check if we need to update it
		r.Log.Info("ingest token secret already exists", "TokenSecretName", hit.Spec.TokenSecretName)
		if string(existingSecret.Data["token"]) != string(desiredSecret.Data["token"]) {
			r.Log.Info("secret does not match the token in Humio. Updating token", "TokenSecretName", hit.Spec.TokenSecretName)
			if err = r.Update(ctx, desiredSecret); err != nil {
				return r.logErrorAndReturn(err, "unable to update alert")
			}
		}
	}
	return nil
}

func (r *HumioIngestTokenReconciler) setState(ctx context.Context, state string, hit *humiov1alpha1.HumioIngestToken) error {
	if hit.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting ingest token state to %s", state))
	hit.Status.State = state
	return r.Status().Update(ctx, hit)
}

func (r *HumioIngestTokenReconciler) logErrorAndReturn(err error, msg string) error {
	r.Log.Error(err, msg)
	return fmt.Errorf("%s: %w", msg, err)
}
