package controllers

import (
	"context"
	"fmt"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *HumioAggregateAlertReconciler) reconcileHumioAggregateAlertAnnotations(ctx context.Context, addedAggregateAlert *humioapi.AggregateAlert, haa *humiov1alpha1.HumioAggregateAlert, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info(fmt.Sprintf("Adding annotations to aggregate alert %q", addedAggregateAlert.Name))
	currentAggregateAlert := &humiov1alpha1.HumioAggregateAlert{}
	err := r.Get(ctx, req.NamespacedName, currentAggregateAlert)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to add annotations to aggregate alert")
	}

	// Copy annotations from the aggregate alerts transformer to get the current aggregate alert annotations
	hydratedHumioAggregateAlert := &humiov1alpha1.HumioAggregateAlert{}
	if err = humio.AggregateAlertHydrate(hydratedHumioAggregateAlert, addedAggregateAlert); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to hydrate alert")
	}

	if len(currentAggregateAlert.ObjectMeta.Annotations) < 1 {
		currentAggregateAlert.ObjectMeta.Annotations = make(map[string]string)
	}
	for k, v := range hydratedHumioAggregateAlert.Annotations {
		currentAggregateAlert.ObjectMeta.Annotations[k] = v
	}

	err = r.Update(ctx, currentAggregateAlert)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to add annotations to aggregate alert")
	}

	r.Log.Info("Added annotations to AggregateAlert", "AggregateAlert", haa.Spec.Name)
	return reconcile.Result{}, nil
}
