package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AggregateAlertIdentifierAnnotation = "humio.com/aggregate-alert-id"
)

func AggregateAlertTransform(haa *humiov1alpha1.HumioAggregateAlert) (*humioapi.AggregateAlert, error) {
	aggregateAlert := &humioapi.AggregateAlert{
		Name:                  haa.Spec.Name,
		QueryString:           haa.Spec.QueryString,
		QueryTimestampType:    haa.Spec.QueryTimestampType,
		Description:           haa.Spec.Description,
		SearchIntervalSeconds: haa.Spec.SearchIntervalSeconds,
		ThrottleTimeSeconds:   haa.Spec.ThrottleTimeSeconds,
		ThrottleField:         haa.Spec.ThrottleField,
		Enabled:               haa.Spec.Enabled,
		ActionNames:           haa.Spec.Actions,
		Labels:                haa.Spec.Labels,
		QueryOwnershipType:    humioapi.QueryOwnershipTypeOrganization,
	}

	if _, ok := haa.ObjectMeta.Annotations[AggregateAlertIdentifierAnnotation]; ok {
		aggregateAlert.ID = haa.ObjectMeta.Annotations[AggregateAlertIdentifierAnnotation]
	}

	if aggregateAlert.Labels == nil {
		aggregateAlert.Labels = []string{}
	}

	return aggregateAlert, nil
}

func AggregateAlertHydrate(haa *humiov1alpha1.HumioAggregateAlert, aggregatealert *humioapi.AggregateAlert) error {
	haa.Spec = humiov1alpha1.HumioAggregateAlertSpec{
		Name:                  aggregatealert.Name,
		QueryString:           aggregatealert.QueryString,
		QueryTimestampType:    aggregatealert.QueryTimestampType,
		Description:           aggregatealert.Description,
		SearchIntervalSeconds: aggregatealert.SearchIntervalSeconds,
		ThrottleTimeSeconds:   aggregatealert.ThrottleTimeSeconds,
		ThrottleField:         aggregatealert.ThrottleField,
		Enabled:               aggregatealert.Enabled,
		Actions:               aggregatealert.ActionNames,
		Labels:                aggregatealert.Labels,
	}

	haa.ObjectMeta = metav1.ObjectMeta{
		Annotations: map[string]string{
			AggregateAlertIdentifierAnnotation: aggregatealert.ID,
		},
	}

	return nil
}
