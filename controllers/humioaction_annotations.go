package controllers

import (
	"context"
	"fmt"

	"github.com/humio/humio-operator/pkg/humio"

	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *HumioActionReconciler) reconcileHumioActionAnnotations(addedNotifier *humioapi.Notifier, ha *humiov1alpha1.HumioAction, req ctrl.Request) (reconcile.Result, error) {
	r.Log.Info(fmt.Sprintf("Adding ID %s to action %s", addedNotifier.ID, addedNotifier.Name))
	currentAction := &humiov1alpha1.HumioAction{}
	err := r.Get(context.TODO(), req.NamespacedName, currentAction)
	if err != nil {
		r.Log.Error(err, "failed to add ID annotation to action")
		return reconcile.Result{}, err
	}

	// Copy annotations from the actions transformer to get the current action ID
	addedAction, err := humio.ActionFromNotifier(addedNotifier)
	if err != nil {
		r.Log.Error(err, "failed to add ID annotation to action")
		return reconcile.Result{}, err
	}
	if len(currentAction.ObjectMeta.Annotations) < 1 {
		currentAction.ObjectMeta.Annotations = make(map[string]string)
	}
	for k, v := range addedAction.Annotations {
		currentAction.ObjectMeta.Annotations[k] = v
	}

	err = r.Update(context.TODO(), currentAction)
	if err != nil {
		r.Log.Error(err, "failed to add ID annotation to action")
		return reconcile.Result{}, err
	}

	r.Log.Info("Added ID to Action", "Action", ha.Spec.Name)
	return reconcile.Result{}, nil
}
