package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func ScheduledSearchTransform(hss *humiov1alpha1.HumioScheduledSearch) *humioapi.ScheduledSearch {
	scheduledSearch := &humioapi.ScheduledSearch{
		Name:               hss.Spec.Name,
		QueryString:        hss.Spec.QueryString,
		Description:        hss.Spec.Description,
		QueryStart:         hss.Spec.QueryStart,
		QueryEnd:           hss.Spec.QueryEnd,
		Schedule:           hss.Spec.Schedule,
		TimeZone:           hss.Spec.TimeZone,
		BackfillLimit:      hss.Spec.BackfillLimit,
		Enabled:            hss.Spec.Enabled,
		ActionNames:        hss.Spec.Actions,
		Labels:             hss.Spec.Labels,
		QueryOwnershipType: humioapi.QueryOwnershipTypeOrganization,
	}

	if scheduledSearch.Labels == nil {
		scheduledSearch.Labels = []string{}
	}

	return scheduledSearch
}

func ScheduledSearchHydrate(hss *humiov1alpha1.HumioScheduledSearch, scheduledSearch *humioapi.ScheduledSearch) {
	hss.Spec = humiov1alpha1.HumioScheduledSearchSpec{
		Name:          scheduledSearch.Name,
		QueryString:   scheduledSearch.QueryString,
		Description:   scheduledSearch.Description,
		QueryStart:    scheduledSearch.QueryStart,
		QueryEnd:      scheduledSearch.QueryEnd,
		Schedule:      scheduledSearch.Schedule,
		TimeZone:      scheduledSearch.TimeZone,
		BackfillLimit: scheduledSearch.BackfillLimit,
		Enabled:       scheduledSearch.Enabled,
		Actions:       scheduledSearch.ActionNames,
		Labels:        scheduledSearch.Labels,
	}

	return
}
