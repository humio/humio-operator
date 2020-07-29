package humioexternalcluster

import (
	"context"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
)

func (r *ReconcileHumioExternalCluster) setState(ctx context.Context, state string, hec *corev1alpha1.HumioExternalCluster) error {
	hec.Status.State = state
	err := r.client.Status().Update(ctx, hec)
	if err != nil {
		return err
	}
	return nil
}
