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
	"github.com/go-logr/zapr"
	humioapi "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/helpers"
	uberzap "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

// HumioParserReconciler reconciles a HumioParser object
type HumioParserReconciler struct {
	client.Client
	Log         logr.Logger
	Scheme      *runtime.Scheme
	HumioClient humio.Client
	Namespace   string
}

// +kubebuilder:rbac:groups=core.humio.com,resources=humioparsers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.humio.com,resources=humioparsers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *HumioParserReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	zapLog, _ := uberzap.NewProduction(uberzap.AddCaller(), uberzap.AddCallerSkip(1))
	defer zapLog.Sync()
	r.Log = zapr.NewLogger(zapLog).WithValues("Request.Namespace", req.Namespace, "Request.Name", req.Name, "Request.Type", helpers.GetTypeName(r))
	r.Log.Info("Reconciling HumioParser")

	if r.Namespace != "" {
		if r.Namespace != req.Namespace {
			return reconcile.Result{}, nil
		}
	}

	// Fetch the HumioParser instance
	hp := &humiov1alpha1.HumioParser{}
	err := r.Get(context.TODO(), req.NamespacedName, hp)
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

	defer func(ctx context.Context, humioClient humio.Client, hp *humiov1alpha1.HumioParser) {
		curParser, err := humioClient.GetParser(hp)
		if err != nil {
			r.setState(ctx, humiov1alpha1.HumioParserStateUnknown, hp)
			return
		}
		emptyParser := humioapi.Parser{}
		if reflect.DeepEqual(emptyParser, *curParser) {
			r.setState(ctx, humiov1alpha1.HumioParserStateNotFound, hp)
			return
		}
		r.setState(ctx, humiov1alpha1.HumioParserStateExists, hp)
	}(context.TODO(), r.HumioClient, hp)

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
			if err := r.finalize(hp); err != nil {
				r.Log.Error(err, "Finalizer method returned error")
				return reconcile.Result{}, err
			}

			// Remove humioFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			r.Log.Info("Finalizer done. Removing finalizer")
			hp.SetFinalizers(helpers.RemoveElement(hp.GetFinalizers(), humioFinalizer))
			err := r.Update(context.TODO(), hp)
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
		if err := r.addFinalizer(hp); err != nil {
			return reconcile.Result{}, err
		}
	}

	cluster, err := helpers.NewCluster(context.TODO(), r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager())
	if err != nil || cluster.Config() == nil {
		r.Log.Error(err, "unable to obtain humio client config")
		return reconcile.Result{}, err
	}

	err = r.HumioClient.Authenticate(cluster.Config())
	if err != nil {
		r.Log.Error(err, "unable to authenticate humio client")
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 5}, err
	}

	// Get current parser
	r.Log.Info("get current parser")
	curParser, err := r.HumioClient.GetParser(hp) // This returns 401 instead of 200
	if err != nil {
		r.Log.Error(err, "could not check if parser exists", "Repository.Name", hp.Spec.RepositoryName)
		return reconcile.Result{}, fmt.Errorf("could not check if parser exists: %s", err)
	}

	emptyParser := humioapi.Parser{Tests: []humioapi.ParserTestCase{}, TagFields: nil} // when using a real humio, we need to do this, ensure tests work the same way. tests currently set this to nil whereas it should be the empty list
	if reflect.DeepEqual(emptyParser, *curParser) {
		r.Log.Info("parser doesn't exist. Now adding parser")
		// create parser
		_, err := r.HumioClient.AddParser(hp)
		if err != nil {
			r.Log.Error(err, "could not create parser")
			return reconcile.Result{}, fmt.Errorf("could not create parser: %s", err)
		}
		r.Log.Info("created parser")
		return reconcile.Result{Requeue: true}, nil
	}

	if (curParser.Script != hp.Spec.ParserScript) || !reflect.DeepEqual(curParser.TagFields, hp.Spec.TagFields) || !reflect.DeepEqual(curParser.Tests, helpers.MapTests(hp.Spec.TestData, helpers.ToTestCase)) {
		r.Log.Info("parser information differs, triggering update")
		_, err = r.HumioClient.UpdateParser(hp)
		if err != nil {
			r.Log.Error(err, "could not update parser")
			return reconcile.Result{}, fmt.Errorf("could not update parser: %s", err)
		}
	}

	// TODO: handle updates to parser name and repositoryName. Right now we just create the new parser,
	// and "leak/leave behind" the old parser.
	// A solution could be to add an annotation that includes the "old name" so we can see if it was changed.
	// A workaround for now is to delete the parser CR and create it again.

	// All done, requeue every 15 seconds even if no changes were made
	return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 15}, nil
}

func (r *HumioParserReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&humiov1alpha1.HumioParser{}).
		Complete(r)
}

func (r *HumioParserReconciler) finalize(hp *humiov1alpha1.HumioParser) error {
	_, err := helpers.NewCluster(context.TODO(), r, hp.Spec.ManagedClusterName, hp.Spec.ExternalClusterName, hp.Namespace, helpers.UseCertManager())
	if errors.IsNotFound(err) {
		return nil
	}

	return r.HumioClient.DeleteParser(hp)
}

func (r *HumioParserReconciler) addFinalizer(hp *humiov1alpha1.HumioParser) error {
	r.Log.Info("Adding Finalizer for the HumioParser")
	hp.SetFinalizers(append(hp.GetFinalizers(), humioFinalizer))

	// Update CR
	err := r.Update(context.TODO(), hp)
	if err != nil {
		r.Log.Error(err, "Failed to update HumioParser with finalizer")
		return err
	}
	return nil
}

func (r *HumioParserReconciler) setState(ctx context.Context, state string, hp *humiov1alpha1.HumioParser) error {
	hp.Status.State = state
	return r.Status().Update(ctx, hp)
}
