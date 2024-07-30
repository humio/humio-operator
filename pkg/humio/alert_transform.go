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
		Name:               ha.Spec.Name,
		QueryString:        ha.Spec.Query.QueryString,
		QueryStart:         ha.Spec.Query.Start,
		Description:        ha.Spec.Description,
		ThrottleTimeMillis: ha.Spec.ThrottleTimeMillis,
		ThrottleField:      ha.Spec.ThrottleField,
		Enabled:            !ha.Spec.Silenced,
		Actions:            actionIdsFromActionMap(ha.Spec.Actions, actionIdMap),
		Labels:             ha.Spec.Labels,
	}

	if alert.QueryStart == "" {
		alert.QueryStart = "1d"
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
			QueryString: alert.QueryString,
			Start:       alert.QueryStart,
		},
		Description:        alert.Description,
		ThrottleTimeMillis: alert.ThrottleTimeMillis,
		ThrottleField:      alert.ThrottleField,
		Silenced:           !alert.Enabled,
		Actions:            actionIdsFromActionMap(ha.Spec.Actions, actionIdMap),
		Labels:             alert.Labels,
	}

	ha.ObjectMeta = metav1.ObjectMeta{
		Annotations: map[string]string{
			AlertIdentifierAnnotation: alert.ID,
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
