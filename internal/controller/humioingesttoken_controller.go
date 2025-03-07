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
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	humioapi "github.com/humio/humio-operator/internal/api"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

// +kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.humio.com,resources=humioingesttokens/finalizers,verbs=update

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

	cluster, err := helpers.NewCluster(ctx, r, hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName, hit.Namespace, helpers.UseCertManager(), true, false)
	if err != nil || cluster == nil || cluster.Config() == nil {
		setStateErr := r.setState(ctx, humiov1alpha1.HumioIngestTokenStateConfigError, hit)
		if setStateErr != nil {
			return reconcile.Result{}, r.logErrorAndReturn(setStateErr, "unable to set cluster state")
		}
		return reconcile.Result{RequeueAfter: 5 * time.Second}, r.logErrorAndReturn(err, "unable to obtain humio client config")
	}
	humioHttpClient := r.HumioClient.GetHumioHttpClient(cluster.Config(), req)

	r.Log.Info("Checking if ingest token is marked to be deleted")
	// Check if the HumioIngestToken instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioIngestTokenMarkedToBeDeleted := hit.GetDeletionTimestamp() != nil
	if isHumioIngestTokenMarkedToBeDeleted {
		r.Log.Info("Ingest token marked to be deleted")
		if helpers.ContainsElement(hit.GetFinalizers(), humioFinalizer) {
			_, err := r.HumioClient.GetIngestToken(ctx, humioHttpClient, req, hit)
			if errors.As(err, &humioapi.EntityNotFound{}) {
				hit.SetFinalizers(helpers.RemoveElement(hit.GetFinalizers(), humioFinalizer))
				err := r.Update(ctx, hit)
				if err != nil {
					return reconcile.Result{}, err
				}
				r.Log.Info("Finalizer removed successfully")
				return reconcile.Result{Requeue: true}, nil
			}

			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Ingest token contains finalizer so run finalizer method")
			if err := r.finalize(ctx, humioHttpClient, req, hit); err != nil {
				return reconcile.Result{}, r.logErrorAndReturn(err, "Finalizer method returned error")
			}
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
		_, err := humioClient.GetIngestToken(ctx, humioHttpClient, req, hit)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioIngestTokenStateNotFound, hit)
			return
		}
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioIngestTokenStateUnknown, hit)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioIngestTokenStateExists, hit)
	}(ctx, r.HumioClient, hit)

	// Get current ingest token
	r.Log.Info("get current ingest token")
	curToken, err := r.HumioClient.GetIngestToken(ctx, humioHttpClient, req, hit)
	if err != nil {
		if errors.As(err, &humioapi.EntityNotFound{}) {
			r.Log.Info("ingest token doesn't exist. Now adding ingest token")
			// create token
			addErr := r.HumioClient.AddIngestToken(ctx, humioHttpClient, req, hit)
			if addErr != nil {
				return reconcile.Result{}, r.logErrorAndReturn(addErr, "could not create ingest token")
			}
			r.Log.Info("created ingest token")
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, r.logErrorAndReturn(err, "could not check if ingest token exists")
	}

	if asExpected, diffKeysAndValues := ingestTokenAlreadyAsExpected(hit, curToken); !asExpected {
		r.Log.Info("information differs, triggering update",
			"diff", diffKeysAndValues,
		)
		err = r.HumioClient.UpdateIngestToken(ctx, humioHttpClient, req, hit)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("could not update ingest token: %w", err)
		}
	}

	err = r.ensureTokenSecretExists(ctx, humioHttpClient, req, hit, cluster)
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
		Named("humioingesttoken").
		Owns(&corev1.Secret{}).
		Complete(r)
}

func (r *HumioIngestTokenReconciler) finalize(ctx context.Context, client *humioapi.Client, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken) error {
	_, err := helpers.NewCluster(ctx, r, hit.Spec.ManagedClusterName, hit.Spec.ExternalClusterName, hit.Namespace, helpers.UseCertManager(), true, false)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	return r.HumioClient.DeleteIngestToken(ctx, client, req, hit)
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

func (r *HumioIngestTokenReconciler) ensureTokenSecretExists(ctx context.Context, client *humioapi.Client, req reconcile.Request, hit *humiov1alpha1.HumioIngestToken, cluster helpers.ClusterInterface) error {
	if hit.Spec.TokenSecretName == "" {
		return nil
	}

	ingestToken, err := r.HumioClient.GetIngestToken(ctx, client, req, hit)
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
				return r.logErrorAndReturn(err, "unable to update ingest token")
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

// ingestTokenAlreadyAsExpected compares fromKubernetesCustomResource and fromGraphQL. It returns a boolean indicating
// if the details from GraphQL already matches what is in the desired state of the custom resource.
// If they do not match, a map is returned with details on what the diff is.
func ingestTokenAlreadyAsExpected(fromKubernetesCustomResource *humiov1alpha1.HumioIngestToken, fromGraphQL *humiographql.IngestTokenDetails) (bool, map[string]string) {
	keyValues := map[string]string{}

	// Expects a parser assigned, but none found
	if fromGraphQL.GetParser() == nil && fromKubernetesCustomResource.Spec.ParserName != nil {
		keyValues["shouldAssignParser"] = *fromKubernetesCustomResource.Spec.ParserName
	}

	// Expects no parser assigned, but found one
	if fromGraphQL.GetParser() != nil && fromKubernetesCustomResource.Spec.ParserName == nil {
		keyValues["shouldUnassignParser"] = fromGraphQL.GetParser().GetName()
	}

	// Parser already assigned, but not the one we expected
	if fromGraphQL.GetParser() != nil && fromKubernetesCustomResource.Spec.ParserName != nil {
		if diff := cmp.Diff(fromGraphQL.GetParser().GetName(), *fromKubernetesCustomResource.Spec.ParserName); diff != "" {
			keyValues["parserName"] = diff
		}
	}

	return len(keyValues) == 0, keyValues
}