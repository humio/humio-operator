package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	FilterAlertIdentifierAnnotation = "humio.com/filter-alert-id"
	QueryOwnershipTypeDefault       = "Organization"
)

func FilterAlertTransform(ha *humiov1alpha1.HumioFilterAlert) (*humioapi.FilterAlert, error) {
	filterAlert := &humioapi.FilterAlert{
		Name:               ha.Spec.Name,
		QueryString:        ha.Spec.QueryString,
		Description:        ha.Spec.Description,
		Enabled:            ha.Spec.Enabled,
		ActionNames:        ha.Spec.Actions,
		Labels:             ha.Spec.Labels,
		QueryOwnershipType: QueryOwnershipTypeDefault,
	}

	if _, ok := ha.ObjectMeta.Annotations[FilterAlertIdentifierAnnotation]; ok {
		filterAlert.ID = ha.ObjectMeta.Annotations[FilterAlertIdentifierAnnotation]
	}

	return filterAlert, nil
}

func FilterAlertHydrate(ha *humiov1alpha1.HumioFilterAlert, alert *humioapi.FilterAlert) error {
	ha.Spec = humiov1alpha1.HumioFilterAlertSpec{
		Name:        alert.Name,
		QueryString: alert.QueryString,
		Description: alert.Description,
		Enabled:     alert.Enabled,
		Actions:     ha.Spec.Actions,
		Labels:      alert.Labels,
	}

	ha.ObjectMeta = metav1.ObjectMeta{
		Annotations: map[string]string{
			FilterAlertIdentifierAnnotation: alert.ID,
		},
	}

	return nil
}
