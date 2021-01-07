package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AlertIdentifierAnnotation = "humio.com/alert-id"
)

func AlertTransform(ha *humiov1alpha1.HumioAlert, actionIdMap map[string]string) (*humioapi.Alert, error) {
	alert := &humioapi.Alert{
		Name: ha.Spec.Name,
		Query: humioapi.HumioQuery{
			QueryString: ha.Spec.Query.QueryString,
			Start:       ha.Spec.Query.Start,
			End:         ha.Spec.Query.End,
			IsLive:      *ha.Spec.Query.IsLive,
		},
		Description:        ha.Spec.Description,
		ThrottleTimeMillis: ha.Spec.ThrottleTimeMillis,
		Silenced:           ha.Spec.Silenced,
		Notifiers:          actionIdsFromActionMap(ha.Spec.Actions, actionIdMap),
		Labels:             ha.Spec.Labels,
	}

	if _, ok := ha.ObjectMeta.Annotations[AlertIdentifierAnnotation]; ok {
		alert.ID = ha.ObjectMeta.Annotations[AlertIdentifierAnnotation]
	}

	return alert, nil
}

func AlertHydrate(ha *humiov1alpha1.HumioAlert, alert *humioapi.Alert, actionIdMap map[string]string) error {
	ha.Spec = humiov1alpha1.HumioAlertSpec{
		Name: alert.Name,
		Query: humiov1alpha1.HumioQuery{
			QueryString: alert.Query.QueryString,
			Start:       alert.Query.Start,
			End:         alert.Query.End,
			IsLive:      &alert.Query.IsLive,
		},
		Description:        alert.Description,
		ThrottleTimeMillis: alert.ThrottleTimeMillis,
		Silenced:           alert.Silenced,
		Actions:            actionIdsFromActionMap(ha.Spec.Actions, actionIdMap),
		Labels:             alert.Labels,
	}

	ha.ObjectMeta = metav1.ObjectMeta{
		Annotations: map[string]string{
			ActionIdentifierAnnotation: alert.ID,
		},
	}

	return nil
}

func actionIdsFromActionMap(actionList []string, actionIdMap map[string]string) []string {
	var actionIds []string
	for _, action := range actionList {
		for actionName, actionId := range actionIdMap {
			if actionName == action {
				actionIds = append(actionIds, actionId)
			}
		}
	}
	return actionIds
}
