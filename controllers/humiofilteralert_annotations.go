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

func (r *HumioFilterAlertReconciler) reconcileHumioFilterAlertAnnotations(ctx context.Context, addedFilterAlert *humioapi.FilterAlert, hfa *humiov1alpha1.HumioFilterAlert, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info(fmt.Sprintf("Adding annotations to filter alert %q", addedFilterAlert.Name))
	currentFilterAlert := &humiov1alpha1.HumioFilterAlert{}
	err := r.Get(ctx, req.NamespacedName, currentFilterAlert)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to add annotations to filter alert")
	}

	// Copy annotations from the filter alerts transformer to get the current filter alert annotations
	hydratedHumioFilterAlert := &humiov1alpha1.HumioFilterAlert{}
	if err = humio.FilterAlertHydrate(hydratedHumioFilterAlert, addedFilterAlert); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to hydrate alert")
	}

	if len(currentFilterAlert.ObjectMeta.Annotations) < 1 {
		currentFilterAlert.ObjectMeta.Annotations = make(map[string]string)
	}
	for k, v := range hydratedHumioFilterAlert.Annotations {
		currentFilterAlert.ObjectMeta.Annotations[k] = v
	}

	err = r.Update(ctx, currentFilterAlert)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to add annotations to filter alert")
	}

	r.Log.Info("Added annotations to FilterAlert", "FilterAlert", hfa.Spec.Name)
	return reconcile.Result{}, nil
}
