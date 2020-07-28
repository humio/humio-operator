package humiocluster

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
)

const (
	podHashAnnotation          = "humio.com/pod-hash"
	podRevisionAnnotation      = "humio.com/pod-revision"
	podRestartPolicyAnnotation = "humio.com/pod-restart-policy"
	PodRestartPolicyRolling    = "rolling"
	PodRestartPolicyRecreate   = "recreate"
)

func (r *ReconcileHumioCluster) incrementHumioClusterPodRevision(ctx context.Context, hc *corev1alpha1.HumioCluster, restartPolicy string) (int, error) {
	newRevision, err := r.getHumioClusterPodRevision(hc)
	if err != nil {
		return -1, err
	}
	newRevision++
	r.logger.Infof("setting cluster pod revision to %d", newRevision)
	hc.Annotations[podRevisionAnnotation] = strconv.Itoa(newRevision)

	r.setRestartPolicy(hc, restartPolicy)

	err = r.client.Update(ctx, hc)
	if err != nil {
		return -1, fmt.Errorf("unable to set annotation %s on HumioCluster: %s", podRevisionAnnotation, err)
	}
	return newRevision, nil
}

func (r *ReconcileHumioCluster) getHumioClusterPodRevision(hc *corev1alpha1.HumioCluster) (int, error) {
	if hc.Annotations == nil {
		hc.Annotations = map[string]string{}
	}
	revision, ok := hc.Annotations[podRevisionAnnotation]
	if !ok {
		revision = "0"
	}
	existingRevision, err := strconv.Atoi(revision)
	if err != nil {
		return -1, fmt.Errorf("unable to read annotation %s on HumioCluster: %s", podRevisionAnnotation, err)
	}
	return existingRevision, nil
}

func (r *ReconcileHumioCluster) getHumioClusterPodRestartPolicy(hc *corev1alpha1.HumioCluster) string {
	if hc.Annotations == nil {
		hc.Annotations = map[string]string{}
	}
	existingPolicy, ok := hc.Annotations[podRestartPolicyAnnotation]
	if !ok {
		existingPolicy = PodRestartPolicyRecreate
	}
	return existingPolicy
}

func (r *ReconcileHumioCluster) setPodRevision(pod *corev1.Pod, newRevision int) error {
	pod.Annotations[podRevisionAnnotation] = strconv.Itoa(newRevision)
	return nil
}

func (r *ReconcileHumioCluster) setRestartPolicy(hc *corev1alpha1.HumioCluster, policy string) {
	r.logger.Infof("setting HumioCluster annotation %s to %s", podRestartPolicyAnnotation, policy)
	hc.Annotations[podRestartPolicyAnnotation] = policy
}
