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
	"errors"
	"fmt"
	"github.com/google/go-cmp/cmp"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sort"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioParserReconciler reconciles a HumioParser object
type HumioParserReconciler struct {
	client.Client
	BaseLogger  logr.Logger
	Log         logr.Logger
	HumioClient humio.Client
	Namespace   string
}

//+kubebuilder:rbac:groups=core.humio.com,resources=humioparsers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core.humio.com,resources=humioparsers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core.humio.com,resources=humioparsers/finalizers,verbs=update

func (r *HumioParserReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	r.Log = r.BaseLogger.WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r), "Reconcile.ID", kubernetes.RandomString())
	r.Log.Info("Reconciling HumioParser")

	// Fetch the HumioParser instance
	hp := &humiov1alpha1.HumioParser{}
	err := r.Get(ctx, req.NamespacedName, hp)
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

	cluster, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true)
	if err != nil || cluster == nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		err = r.setState(ctx, humiov1alpha1.HumioParserStateConfigError, hp)
		if err != nil {
			r.Log.Error(err, "unable to set cluster state")
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, err
	}

	r.Log.Info("Checking if parser is marked to be deleted")
	// Check if the HumioParser instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isHumioParserMarkedToBeDeleted := hp.GetDeletionTimestamp() != nil
	if isHumioParserMarkedToBeDeleted {
		r.Log.Info("Parser marked to be deleted")
		if helpers.ContainsElement(hp.GetFinalizers(), humioFinalizer) {
			// Run finalization logic for humioFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.Log.Info("Parser contains finalizer so run finalizer method")
			if err := r.finalize(ctx, cluster.Config(), req, hp); err != nil {
				r.Log.Error(err, "Finalizer method returned error")
				return reconcile.Result{}, err
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.Log.Info("Finalizer done. Removing finalizer")
			hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), humioFinalizer))
			err := r.Update(ctx, hp)
			if err != nil {
				return reconcile.Result{}, err
			}
			r.Log.Info("Finalizer removed successfully")
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer for this CR
	if !helpers.ContainsElement(hp.GetFinalizers(), humioFinalizer) {
		r.Log.Info("Finalizer not present, adding finalizer to parser")
		if err := r.addFinalizer(ctx, hp); err != nil {
			return reconcile.Result{}, err
		}
	}

	defer func(ctx context.Context, humioClient humio.Client, hp *humiov1alpha1.HumioParser) {
		curParser, err := humioClient.GetParser(cluster.Config(), req, hp)
		if errors.As(err, &humioapi.EntityNotFound{}) {
			_ = r.setState(ctx, humiov1alpha1.HumioParserStateNotFound, hp)
			return
		}
		if err != nil || curParser == nil {
			_ = r.setState(ctx, humiov1alpha1.HumioParserStateUnknown, hp)
			return
		}
		_ = r.setState(ctx, humiov1alpha1.HumioParserStateExists, hp)
	}(ctx, r.HumioClient, hp)

	// Get current parser
	r.Log.Info("get current parser")
	curParser, err := r.HumioClient.GetParser(cluster.Config(), req, hp)
	if errors.As(err, &humioapi.EntityNotFound{}) {
		r.Log.Info("parser doesn't exist. Now adding parser")
		// create parser
		_, err := r.HumioClient.AddParser(cluster.Config(), req, hp)
		if err != nil {
			r.Log.Error(err, "could not create parser")
			return reconcile.Result{}, fmt.Errorf("could not create parser: %w", err)
		}
		r.Log.Info("created parser")
		return reconcile.Result{Requeue: true}, nil
	}
	if err != nil {
		r.Log.Error(err, "could not check if parser exists", "Repository.Name", hp.Spec.RepositoryName)
		return reconcile.Result{}, fmt.Errorf("could not check if parser exists: %w", err)
	}

	currentTagFields := make([]string, len(curParser.TagFields))
	expectedTagFields := make([]string, len(hp.Spec.TagFields))
	currentTests := make([]string, len(curParser.Tests))
	expectedTests := make([]string, len(hp.Spec.TestData))
	_ = copy(currentTagFields, curParser.TagFields)
	_ = copy(expectedTagFields, hp.Spec.TagFields)
	_ = copy(currentTests, curParser.Tests)
	_ = copy(expectedTests, hp.Spec.TestData)
	sort.Strings(currentTagFields)
	sort.Strings(expectedTagFields)
	sort.Strings(currentTests)
	sort.Strings(expectedTests)
	parserScriptDiff := cmp.Diff(curParser.Script, hp.Spec.ParserScript)
	tagFieldsDiff := cmp.Diff(curParser.TagFields, hp.Spec.TagFields)
	testDataDiff := cmp.Diff(curParser.Tests, hp.Spec.TestData)

	if parserScriptDiff != "" || tagFieldsDiff != "" || testDataDiff != "" {
		r.Log.Info("parser information differs, triggering update", "parserScriptDiff", parserScriptDiff, "tagFieldsDiff", tagFieldsDiff, "testDataDiff", testDataDiff)
		_, err = r.HumioClient.UpdateParser(cluster.Config(), req, hp)
		if err != nil {
			r.Log.Error(err, "could not update parser")
			return reconcile.Result{}, fmt.Errorf("could not update parser: %w", err)
		}
	}

	// TODO: handle updates to parser name and repositoryName. Right now we just create the new parser,
	// and "leak/leave behind" the old parser.
	// A solution could be to add an annotation that includes the "old name" so we can see if it was changed.
	// A workaround for now is to delete the parser CR and create it again.

	r.Log.Info("done reconciling, will requeue after 15 seconds")
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HumioParserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioParser{}).
		Complete(r)
}

func (r *HumioParserReconciler) finalize(ctx context.Context, config *humioapi.Config, req reconcile.Request, hp *humiov1alpha1.HumioParser) error {
	_, err := helpers.NewCluster(ctx, r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager(), true)
	if k8serrors.IsNotFound(err) {
		return nil
	}

	return r.HumioClient.DeleteParser(config, req, hp)
}

func (r *HumioParserReconciler) addFinalizer(ctx context.Context, hp *humiov1alpha1.HumioParser) error {
	r.Log.Info("Adding Finalizer for the HumioParser")
	hp.SetFinalizers(append(hp.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(ctx, hp)
	if err != nil {
		r.Log.Error(err, "Failed to update HumioParser with finalizer")
		return err
	}
	return nil
}

func (r *HumioParserReconciler) setState(ctx context.Context, state string, hp *humiov1alpha1.HumioParser) error {
	if hp.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting parser state to %s", state))
	hp.Status.State = state
	return r.Status().Update(ctx, hp)
}
