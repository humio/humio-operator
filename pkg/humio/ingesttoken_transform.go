package humio

import (
	humioapi "github.com/humio/cli/api"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

func IngestTokenTransform(hit *humiov1alpha1.HumioIngestToken) *humioapi.IngestToken {
	ingestToken := &humioapi.IngestToken{
		Name:           hit.Spec.Name,
		AssignedParser: hit.Spec.ParserName,
	}

	return ingestToken
}
