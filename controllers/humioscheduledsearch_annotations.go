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

func (r *HumioScheduledSearchReconciler) reconcileHumioScheduledSearchAnnotations(ctx context.Context, addedScheduledSearch *humioapi.ScheduledSearch, hss *humiov1alpha1.HumioScheduledSearch, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info(fmt.Sprintf("Adding annotations to scheduled search %q", addedScheduledSearch.Name))
	currentScheduledSearch := &humiov1alpha1.HumioScheduledSearch{}
	err := r.Get(ctx, req.NamespacedName, currentScheduledSearch)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to add annotations to scheduled search")
	}

	// Copy annotations from the scheduled search transformer to get the current scheduled search annotations
	hydratedHumioScheduledSearch := &humiov1alpha1.HumioScheduledSearch{}
	if err = humio.ScheduledSearchHydrate(hydratedHumioScheduledSearch, addedScheduledSearch); err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to hydrate scheduled search")
	}

	if len(currentScheduledSearch.ObjectMeta.Annotations) < 1 {
		currentScheduledSearch.ObjectMeta.Annotations = make(map[string]string)
	}
	for k, v := range hydratedHumioScheduledSearch.Annotations {
		currentScheduledSearch.ObjectMeta.Annotations[k] = v
	}

	err = r.Update(ctx, currentScheduledSearch)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to add annotations to scheduled search")
	}

	r.Log.Info("Added annotations to ScheduledSearch", "ScheduledSearch", hss.Spec.Name)
	return reconcile.Result{}, nil
}
