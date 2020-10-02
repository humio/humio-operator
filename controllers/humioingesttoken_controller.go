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
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	//"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/go-logr/logr"
	//"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

const humioFinalizer = "finalizer.humio.com" // TODO: Not only used for ingest tokens, but also parsers and repositories.

// HumioIngestTokenReconciler reconciles a HumioIngestToken object
type HumioIngestTokenReconciler struct {
	client.Client
	Log         logr.Logger // TODO: Migrate to *zap.SugaredLogger
	logger      *zap.SugaredLogger
	Scheme      *runtime.Scheme
	HumioClient humio.Client
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *HumioIngestTokenReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	logger, _ := zap.NewProduction()
	defer logger.Sync()
	r.logger = logger.Sugar().With("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.logger.Info("Reconciling HumioIngestToken")
	// TODO: Add back controllerutil.SetControllerReference everywhere we create k8s objects

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

	r.logger.Info("Checking if ingest token is marked to be deleted")
	// Check if the HumioIngestToken instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioIngestTokenMarkedToBeDeleted := hit.GetDeletionTimestamp() != nil
	if isHumioIngestTokenMarkedToBeDeleted {
		r.logger.Info("Ingest token marked to be deleted")
		if helpers.ContainsElement(hit.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.logger.Info("Ingest token contains finalizer so run finalizer method")
			if err := r.finalize(hit); err != nil {
				r.logger.Infof("Finalizer method returned error: %v", err)
				return reconcile.Result{}, err
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.logger.Info("Finalizer done. Removing finalizer")
			hit.SetFinalizers(helpers.RemoveElement(hit.GetFinalizers(), humioFinalizer))
			err := r.Update(context.TODO(), hit)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.logger.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hit.GetFinalizers(), humioFinalizer) {
		r.logger.Info("Finalizer not present, adding finalizer to ingest token")
		if err := r.addFinalizer(hit); err != nil {
			return reconcile.Result{}, err
		}
	}

	cluster, err := helpers.NewCluster(context.TODO(), r, hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName, hit.Namespace, helpers.UseCertManager())
	if err != nil || cluster.Config() == nil {
		r.logger.Errorf("unable to obtain humio client config: %s", err)
		return reconcile.Result{}, err
	}

	err = r.HumioClient.Authenticate(cluster.Config())
	if err != nil {
		r.logger.Warnf("unable to authenticate humio client: %s", err)
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}

	// Get current ingest token
	r.logger.Info("get current ingest token")
	curToken, err := r.HumioClient.GetIngestToken(hit)
	if err != nil {
		r.logger.Infof("could not check if ingest token exists in repo %s: %+v", hit.Spec.RepositoryName, err)
		return reconcile.Result{}, fmt.Errorf("could not check if ingest token exists: %s", err)
	}
	// If token doesn't exist, the Get returns: nil, err.
	// How do we distinguish between "doesn't exist" and "error while executing get"?
	// TODO: change the way we do errors from the API so we can get rid of this hack
	emptyToken := humioapi.IngestToken{}
	if emptyToken == *curToken {
		r.logger.Info("ingest token doesn't exist. Now adding ingest token")
		// create token
		_, err := r.HumioClient.AddIngestToken(hit)
		if err != nil {
			r.logger.Info("could not create ingest token: %s", err)
			return reconcile.Result{}, fmt.Errorf("could not create ingest token: %s", err)
		}
		r.logger.Infof("created ingest token: %s", hit.Spec.Name)
		return reconcile.Result{Requeue: true}, nil
	}

	// Trigger update if parser name changed
	if curToken.AssignedParser != hit.Spec.ParserName {
		r.logger.Infof("parser name differs, triggering update, parser should be %s but got %s", hit.Spec.ParserName, curToken.AssignedParser)
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
	r.logger.Info("Adding Finalizer for the HumioIngestToken")
	hit.SetFinalizers(append(hit.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(context.TODO(), hit)
	if err != nil {
		r.logger.Error(err, "Failed to update HumioIngestToken with finalizer")
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
			r.logger.Infof("successfully created ingest token secret %s for HumioIngestToken %s", hit.Spec.TokenSecretName, hit.Name)
			humioIngestTokenPrometheusMetrics.Counters.ServiceAccountSecretsCreated.Inc()
		}
	} else {
		// kubernetes secret exists, check if we need to update it
		r.logger.Infof("ingest token secret %s already exists for HumioIngestToken %s", hit.Spec.TokenSecretName, hit.Name)
		if string(existingSecret.Data["token"]) != string(desiredSecret.Data["token"]) {
			r.logger.Infof("ingest token %s stored in secret %s does not match the token in Humio. Updating token for %s.", hit.Name, hit.Spec.TokenSecretName)
			r.Update(ctx, desiredSecret)
		}
	}
	return nil
}

func (r *HumioIngestTokenReconciler) setState(ctx context.Context, state string, hit *humiov1alpha1.HumioIngestToken) error {
	hit.Status.State = state
	return r.Status().Update(ctx, hit)
}
