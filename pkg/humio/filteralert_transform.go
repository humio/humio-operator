package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func FilterAlertTransform(hfa *humiov1alpha1.HumioFilterAlert) *humioapi.FilterAlert {
	filterAlert := &humioapi.FilterAlert{
		Name:                hfa.Spec.Name,
		QueryString:         hfa.Spec.QueryString,
		Description:         hfa.Spec.Description,
		ThrottleTimeSeconds: hfa.Spec.ThrottleTimeSeconds,
		ThrottleField:       hfa.Spec.ThrottleField,
		Enabled:             hfa.Spec.Enabled,
		ActionNames:         hfa.Spec.Actions,
		Labels:              hfa.Spec.Labels,
		QueryOwnershipType:  humioapi.QueryOwnershipTypeOrganization,
	}

	if filterAlert.Labels == nil {
		filterAlert.Labels = []string{}
	}

	return filterAlert
}

func FilterAlertHydrate(hfa *humiov1alpha1.HumioFilterAlert, alert *humioapi.FilterAlert) {
	hfa.Spec = humiov1alpha1.HumioFilterAlertSpec{
		Name:                alert.Name,
		QueryString:         alert.QueryString,
		Description:         alert.Description,
		ThrottleTimeSeconds: alert.ThrottleTimeSeconds,
		ThrottleField:       alert.ThrottleField,
		Enabled:             alert.Enabled,
		Actions:             alert.ActionNames,
		Labels:              alert.Labels,
	}
}
