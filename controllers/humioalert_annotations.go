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

func (r *HumioAlertReconciler) reconcileHumioAlertAnnotations(ctx context.Context, addedAlert *humioapi.Alert, ha *humiov1alpha1.HumioAlert, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info(fmt.Sprintf("Adding ID \"%s\" to alert \"%s\"", addedAlert.ID, addedAlert.Name))
	currentAlert := &humiov1alpha1.HumioAlert{}
	err := r.Get(ctx, req.NamespacedName, currentAlert)
	if err != nil {
		r.Log.Error(err, "failed to add ID annotation to alert")
		return reconcile.Result{}, err
	}

	// Copy annotations from the alerts transformer to get the current alert ID
	hydratedHumioAlert := &humiov1alpha1.HumioAlert{}
	if err = humio.AlertHydrate(hydratedHumioAlert, addedAlert, map[string]string{}); err != nil {
		r.Log.Error(err, "failed to hydrate alert")
		return reconcile.Result{}, err
	}

	if len(currentAlert.ObjectMeta.Annotations) < 1 {
		currentAlert.ObjectMeta.Annotations = make(map[string]string)
	}
	for k, v := range hydratedHumioAlert.Annotations {
		currentAlert.ObjectMeta.Annotations[k] = v
	}

	err = r.Update(ctx, currentAlert)
	if err != nil {
		r.Log.Error(err, "failed to add ID annotation to alert")
		return reconcile.Result{}, err
	}

	r.Log.Info("Added id to Alert", "Alert", ha.Spec.Name)
	return reconcile.Result{}, nil
}
