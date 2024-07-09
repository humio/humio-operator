package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	FilterAlertIdentifierAnnotation = "humio.com/filter-alert-id"
	QueryOwnershipTypeDefault       = "Organization"
)

func FilterAlertTransform(hfa *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error) {
	filterAlert := &humioapi.FilterAlert{
		Name:               hfa.Spec.Name,
		QueryString:        hfa.Spec.QueryString,
		Description:        hfa.Spec.Description,
		ThrottleField:      hfa.Spec.ThrottleField,
		Enabled:            hfa.Spec.Enabled,
		ActionNames:        hfa.Spec.Actions,
		Labels:             hfa.Spec.Labels,
		QueryOwnershipType: QueryOwnershipTypeDefault,
	}

	if hfa.Spec.ThrottleTimeSeconds != nil {
		filterAlert.ThrottleTimeSeconds = *hfa.Spec.ThrottleTimeSeconds
	}

	if _, ok := hfa.ObjectMeta.Annotations[FilterAlertIdentifierAnnotation]; ok {
		filterAlert.ID = hfa.ObjectMeta.Annotations[FilterAlertIdentifierAnnotation]
	}

	return filterAlert, nil
}

func FilterAlertHydrate(hfa *humiov1alpha1.HumioFilterAlert, alert *humioapi.FilterAlert) error {
	hfa.Spec = humiov1alpha1.HumioFilterAlertSpec{
		Name:          alert.Name,
		QueryString:   alert.QueryString,
		Description:   alert.Description,
		ThrottleField: alert.ThrottleField,
		Enabled:       alert.Enabled,
		Actions:       alert.ActionNames,
		Labels:        alert.Labels,
	}

	if alert.ThrottleTimeSeconds != 0 {
		hfa.Spec.ThrottleTimeSeconds = helpers.IntPtr(alert.ThrottleTimeSeconds)
	}

	hfa.ObjectMeta = metav1.ObjectMeta{
		Annotations: map[string]string{
			FilterAlertIdentifierAnnotation: alert.ID,
		},
	}

	return nil
}
