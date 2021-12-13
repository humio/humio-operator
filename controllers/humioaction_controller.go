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
	"reflect"

	"github.com/go-logr/logr"
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// HumioActionReconciler reconciles a HumioAction object
type HumioActionReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioactions/finalizers,verbs=update

func (r *HumioActionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioAction")

	ha := &humiov1alpha1.HumioAction{}
	err := r.Get(ctx, req.NamespacedName, ha)
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

	cluster, err := helpers.NewCluster(ctx, r, ha.Spec.ManagedClusterName, ha.Spec.ExternalClusterName, ha.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		err = r.setState(ctx, humiov1alpha1.HumioActionStateConfigError, ha)
		if err != nil {
			r.Log.Error(err, "unable to set action state")
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	err = r.resolveSecrets(ctx, ha)
	if err != nil {
		r.Log.Error(err, "could not resolve secret references")
		return reconcile.Result{}, fmt.Errorf("could not resolve secret references: %s", err)
	}

	if _, err := humio.NotifierFromAction(ha); err != nil {
		r.Log.Error(err, "unable to validate action")
		err = r.setState(ctx, humiov1alpha1.HumioActionStateConfigError, ha)
		if err != nil {
			r.Log.Error(err, "unable to set action state")
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	curNotifier, err := r.HumioClient.GetNotifier(cluster.Config(), req, ha)
	if curNotifier != nil && err != nil {
		r.Log.Error(err, "got unexpected error when checking if action exists")
		stateErr := r.setState(ctx, humiov1alpha1.HumioActionStateUnknown, ha)
		if stateErr != nil {
			r.Log.Error(stateErr, "unable to set action state")
			return reconcile.Result{}, stateErr
		}
		return reconcile.Result{}, fmt.Errorf("could not check if action exists: %s", err)
	}

	defer func(ctx context.Context, humioClient humio.Client, ha *humiov1alpha1.HumioAction) {
		curNotifier, err := r.HumioClient.GetNotifier(cluster.Config(), req, ha)
		if err != nil {
			_ = r.setState(ctx, humiov1alpha1.HumioActionStateUnknown, ha)
			return
		}
		if curNotifier == nil {
			_ = r.setState(ctx, humiov1alpha1.HumioActionStateNotFound, ha)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioActionStateExists, ha)
	}(ctx, r.HumioClient, ha)

	return r.reconcileHumioAction(ctx, cluster.Config(), curNotifier, ha, req)
}

func (r *HumioActionReconciler) reconcileHumioAction(ctx context.Context, config *humioapi.Config, curNotifier *humioapi.Notifier, ha *humiov1alpha1.HumioAction, req ctrl.Request) (reconcile.Result, error) {
	// Delete
	r.Log.Info("Checking if Action is marked to be deleted")
	isMarkedForDeletion := ha.GetDeletionTimestamp() != nil
	if isMarkedForDeletion {
		r.Log.Info("Action marked to be deleted")
		if helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Deleting Action")
			if err := r.HumioClient.DeleteNotifier(config, req, ha); err != nil {
				r.Log.Error(err, "Delete Action returned error")
				return reconcile.Result{}, err
			}

			r.Log.Info("Action Deleted. Removing finalizer")
			ha.SetFinalizers(helpers.RemoveElement(ha.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, ha)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	r.Log.Info("Checking if Action requires finalizer")
	// Add finalizer for this CR
	if !helpers.ContainsElement(ha.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to Action")
		ha.SetFinalizers(append(ha.GetFinalizers(), humioFinalizer))
		err := r.Update(ctx, ha)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if action needs to be created")
	// Add Action
	if curNotifier == nil {
		r.Log.Info("Action doesn't exist. Now adding action")
		addedNotifier, err := r.HumioClient.AddNotifier(config, req, ha)
		if err != nil {
			r.Log.Error(err, "could not create action")
			return reconcile.Result{}, fmt.Errorf("could not create Action: %s", err)
		}
		r.Log.Info("Created action", "Action", ha.Spec.Name)

		result, err := r.reconcileHumioActionAnnotations(ctx, addedNotifier, ha, req)
		if err != nil {
			return result, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	r.Log.Info("Checking if action needs to be updated")
	// Update
	expectedNotifier, err := humio.NotifierFromAction(ha)
	if err != nil {
		r.Log.Error(err, "could not parse expected action")
		return reconcile.Result{}, fmt.Errorf("could not parse expected action: %s", err)
	}
	if !reflect.DeepEqual(*curNotifier, *expectedNotifier) {
		r.Log.Info(fmt.Sprintf("Action differs, triggering update, expected %#v, got: %#v",
			expectedNotifier,
			curNotifier))
		notifier, err := r.HumioClient.UpdateNotifier(config, req, ha)
		if err != nil {
			r.Log.Error(err, "could not update action")
			return reconcile.Result{}, fmt.Errorf("could not update action: %s", err)
		}
		if notifier != nil {
			r.Log.Info(fmt.Sprintf("Updated notifier %q", notifier.Name))
		}
	}

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{}, nil
}

func (r *HumioActionReconciler) resolveSecrets(ctx context.Context, ha *humiov1alpha1.HumioAction) error {
	var err error

	if ha.Spec.SlackPostMessageProperties != nil {
		ha.Spec.SlackPostMessageProperties.ApiToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.SlackPostMessageProperties.ApiToken, ha.Spec.SlackPostMessageProperties.ApiTokenSource)
		if err != nil {
			return fmt.Errorf("slackPostMessageProperties.ingestTokenSource.%v", err)
		}
	}

	if ha.Spec.OpsGenieProperties != nil {
		ha.Spec.OpsGenieProperties.GenieKey, err = r.resolveField(ctx, ha.Namespace, ha.Spec.OpsGenieProperties.GenieKey, ha.Spec.OpsGenieProperties.GenieKeySource)
		if err != nil {
			return fmt.Errorf("opsGenieProperties.ingestTokenSource.%v", err)
		}
	}

	if ha.Spec.HumioRepositoryProperties != nil {
		ha.Spec.HumioRepositoryProperties.IngestToken, err = r.resolveField(ctx, ha.Namespace, ha.Spec.HumioRepositoryProperties.IngestToken, ha.Spec.HumioRepositoryProperties.IngestTokenSource)
		if err != nil {
			return fmt.Errorf("humioRepositoryProperties.ingestTokenSource.%v", err)
		}
	}

	return nil
}

func (r *HumioActionReconciler) resolveField(ctx context.Context, namespace, value string, ref humiov1alpha1.VarSource) (string, error) {
	if value != "" {
		return value, nil
	}

	if ref.SecretKeyRef != nil {
		secret, err := kubernetes.GetSecret(ctx, r, ref.SecretKeyRef.Name, namespace)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return "", fmt.Errorf("secretKeyRef was set but no secret exists by name %s in namespace %s", ref.SecretKeyRef.Name, namespace)
			}
			return "", fmt.Errorf("unable to get secret with name %s in namespace %s", ref.SecretKeyRef.Name, namespace)
		}
		value, ok := secret.Data[ref.SecretKeyRef.Key]
		if !ok {
			return "", fmt.Errorf("secretKeyRef was found but it does not contain the key %s", ref.SecretKeyRef.Key)
		}
		return string(value), nil
	}

	return "", nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioActionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioAction{}).
		Complete(r)
}

func (r *HumioActionReconciler) setState(ctx context.Context, state string, hr *humiov1alpha1.HumioAction) error {
	if hr.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting action state to %s", state))
	hr.Status.State = state
	return r.Status().Update(ctx, hr)
}
