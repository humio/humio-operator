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
	"github.com/go-logr/zapr"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	uberzap "go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

const humioFinalizer = "finalizer.humio.com" // TODO: Not only used for ingest tokens, but also parsers, repositories and views.

// HumioIngestTokenReconciler reconciles a HumioIngestToken object
type HumioIngestTokenReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	HumioClient humio.Client
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *HumioIngestTokenReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	zapLog, _ := uberzap.NewProduction(uberzap.AddCaller(), uberzap.AddCallerSkip(1))
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioIngestToken")

	// Fetch the HumioIngestToken instance
	hit := &humiov1alpha1.HumioIngestToken{}
	err := r.Get(context.TODO(), req.NamespacedName, hit)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	defer func(ctx context.Context, humioClient humio.Client, hit *humiov1alpha1.HumioIngestToken) {
		curToken, err := humioClient.GetIngestToken(hit)
		if err != nil {
			r.setState(ctx, humiov1alpha1.HumioIngestTokenStateUnknown, hit)
			return
		}
		emptyToken := humioapi.IngestToken{}
		if emptyToken != *curToken {
			r.setState(ctx, humiov1alpha1.HumioIngestTokenStateExists, hit)
			return
		}
		r.setState(ctx, humiov1alpha1.HumioIngestTokenStateNotFound, hit)
	}(context.TODO(), r.HumioClient, hit)

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
			if err := r.finalize(hit); err != nil {
				r.Log.Error(err, "Finalizer method returned error")
				return reconcile.Result{}, err
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.Log.Info("Finalizer done. Removing finalizer")
			hit.SetFinalizers(helpers.RemoveElement(hit.GetFinalizers(), humioFinalizer))
			err := r.Update(context.TODO(), hit)
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
		if err := r.addFinalizer(hit); err != nil {
			return reconcile.Result{}, err
		}
	}

	cluster, err := helpers.NewCluster(context.TODO(), r, hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName, hit.Namespace, helpers.UseCertManager())
	if err != nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		return reconcile.Result{}, err
	}

	err = r.HumioClient.Authenticate(cluster.Config())
	if err != nil {
		r.Log.Error(err, "unable to authenticate humio client")
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}

	// Get current ingest token
	r.Log.Info("get current ingest token")
	curToken, err := r.HumioClient.GetIngestToken(hit)
	if err != nil {
		r.Log.Error(err, "could not check if ingest token exists", "Repository.Name", hit.Spec.RepositoryName)
		return reconcile.Result{}, fmt.Errorf("could not check if ingest token exists: %s", err)
	}
	// If token doesn't exist, the Get returns: nil, err.
	// How do we distinguish between "doesn't exist" and "error while executing get"?
	// TODO: change the way we do errors from the API so we can get rid of this hack
	emptyToken := humioapi.IngestToken{}
	if emptyToken == *curToken {
		r.Log.Info("ingest token doesn't exist. Now adding ingest token")
		// create token
		_, err := r.HumioClient.AddIngestToken(hit)
		if err != nil {
			r.Log.Error(err, "could not create ingest token")
			return reconcile.Result{}, fmt.Errorf("could not create ingest token: %s", err)
		}
		r.Log.Info("created ingest token")
		return reconcile.Result{Requeue: true}, nil
	}

	// Trigger update if parser name changed
	if curToken.AssignedParser != hit.Spec.ParserName {
		r.Log.Info("parser name differs, triggering update", "Expected", hit.Spec.ParserName, "Got", curToken.AssignedParser)
		_, updateErr := r.HumioClient.UpdateIngestToken(hit)
		if updateErr != nil {
			return reconcile.Result{}, fmt.Errorf("could not update ingest token: %s", updateErr)
		}
	}

	err = r.ensureTokenSecretExists(context.TODO(), hit, cluster)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("could not ensure token secret exists: %s", err)
	}

	// TODO: handle updates to ingest token name and repositoryName. Right now we just create the new ingest token,
	// and "leak/leave behind" the old token.
	// A solution could be to add an annotation that includes the "old name" so we can see if it was changed.
	// A workaround for now is to delete the ingest token CR and create it again.

	// All done, requeue every 15 seconds even if no changes were made
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
}

func (r *HumioIngestTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioIngestToken{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

func (r *HumioIngestTokenReconciler) finalize(hit *humiov1alpha1.HumioIngestToken) error {
	_, err := helpers.NewCluster(context.TODO(), r, hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName, hit.Namespace, helpers.UseCertManager())
	if errors.IsNotFound(err) {
		return nil
	}

	return r.HumioClient.DeleteIngestToken(hit)
}

func (r *HumioIngestTokenReconciler) addFinalizer(hit *humiov1alpha1.HumioIngestToken) error {
	r.Log.Info("Adding Finalizer for the HumioIngestToken")
	hit.SetFinalizers(append(hit.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(context.TODO(), hit)
	if err != nil {
		r.Log.Error(err, "Failed to update HumioIngestToken with finalizer")
		return err
	}
	return nil
}

func (r *HumioIngestTokenReconciler) ensureTokenSecretExists(ctx context.Context, hit *humiov1alpha1.HumioIngestToken, cluster helpers.ClusterInterface) error {
	if hit.Spec.TokenSecretName == "" {
		return nil
	}

	ingestToken, err := r.HumioClient.GetIngestToken(hit)
	if err != nil {
		return fmt.Errorf("failed to get ingest token: %s", err)
	}

	secretData := map[string][]byte{"token": []byte(ingestToken.Token)}
	desiredSecret := kubernetes.ConstructSecret(cluster.Name(), hit.Namespace, hit.Spec.TokenSecretName, secretData)
	if err := controllerutil.SetControllerReference(hit, desiredSecret, r.Scheme); err != nil {
		return fmt.Errorf("could not set controller reference: %s", err)
	}

	existingSecret, err := kubernetes.GetSecret(ctx, r, hit.Spec.TokenSecretName, hit.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Create(ctx, desiredSecret)
			if err != nil {
				return fmt.Errorf("unable to create ingest token secret for HumioIngestToken: %s", err)
			}
			r.Log.Info("successfully created ingest token secret", "TokenSecretName", hit.Spec.TokenSecretName)
			humioIngestTokenPrometheusMetrics.Counters.ServiceAccountSecretsCreated.Inc()
		}
	} else {
		// kubernetes secret exists, check if we need to update it
		r.Log.Info("ingest token secret already exists", "TokenSecretName", hit.Spec.TokenSecretName)
		if string(existingSecret.Data["token"]) != string(desiredSecret.Data["token"]) {
			r.Log.Info("secret does not match the token in Humio. Updating token", "TokenSecretName", hit.Spec.TokenSecretName)
			r.Update(ctx, desiredSecret)
		}
	}
	return nil
}

func (r *HumioIngestTokenReconciler) setState(ctx context.Context, state string, hit *humiov1alpha1.HumioIngestToken) error {
	hit.Status.State = state
	return r.Status().Update(ctx, hit)
}
