package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func AggregateAlertTransform(haa *humiov1alpha1.HumioAggregateAlert) *humioapi.AggregateAlert {
	aggregateAlert := &humioapi.AggregateAlert{
		Name:                  haa.Spec.Name,
		QueryString:           haa.Spec.QueryString,
		QueryTimestampType:    haa.Spec.QueryTimestampType,
		Description:           haa.Spec.Description,
		SearchIntervalSeconds: haa.Spec.SearchIntervalSeconds,
		ThrottleTimeSeconds:   haa.Spec.ThrottleTimeSeconds,
		ThrottleField:         haa.Spec.ThrottleField,
		TriggerMode:           haa.Spec.TriggerMode,
		Enabled:               haa.Spec.Enabled,
		ActionNames:           haa.Spec.Actions,
		Labels:                haa.Spec.Labels,
		QueryOwnershipType:    humioapi.QueryOwnershipTypeOrganization,
	}

	if aggregateAlert.Labels == nil {
		aggregateAlert.Labels = []string{}
	}

	return aggregateAlert
}

func AggregateAlertHydrate(haa *humiov1alpha1.HumioAggregateAlert, aggregateAlert *humioapi.AggregateAlert) {
	haa.Spec = humiov1alpha1.HumioAggregateAlertSpec{
		Name:                  aggregateAlert.Name,
		QueryString:           aggregateAlert.QueryString,
		QueryTimestampType:    aggregateAlert.QueryTimestampType,
		Description:           aggregateAlert.Description,
		SearchIntervalSeconds: aggregateAlert.SearchIntervalSeconds,
		ThrottleTimeSeconds:   aggregateAlert.ThrottleTimeSeconds,
		ThrottleField:         aggregateAlert.ThrottleField,
		TriggerMode:           aggregateAlert.TriggerMode,
		Enabled:               aggregateAlert.Enabled,
		Actions:               aggregateAlert.ActionNames,
		Labels:                aggregateAlert.Labels,
	}
}
