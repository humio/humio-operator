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

func (r *HumioActionReconciler) reconcileHumioActionAnnotations(ctx context.Context, addedAction *humioapi.Action, ha *humiov1alpha1.HumioAction, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info(fmt.Sprintf("Adding ID %s to action %s", addedAction.ID, addedAction.Name))
	actionCR := &humiov1alpha1.HumioAction{}
	err := r.Get(ctx, req.NamespacedName, actionCR)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to add ID annotation to action")
	}

	if len(actionCR.ObjectMeta.Annotations) < 1 {
		actionCR.ObjectMeta.Annotations = make(map[string]string)
	}

	actionCR.ObjectMeta.Annotations[humio.ActionIdentifierAnnotation] = addedAction.ID

	err = r.Update(ctx, actionCR)
	if err != nil {
		return reconcile.Result{}, r.logErrorAndReturn(err, "failed to add ID annotation to action")
	}

	r.Log.Info("Added ID to Action", "Action", ha.Spec.Name)
	return reconcile.Result{}, nil
}
