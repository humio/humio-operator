package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func AlertTransform(ha *humiov1alpha1.HumioAlert, actionIdMap map[string]string) *humioapi.Alert {
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

	return alert
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
