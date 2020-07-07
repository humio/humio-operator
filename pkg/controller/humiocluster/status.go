package humiocluster

import (
	"context"
	"strconv"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
)

// setState is used to change the cluster state
// TODO: we use this to determine if we should have a delay between startup of humio pods during bootstrap vs starting up pods during an image update
func (r *ReconcileHumioCluster) setState(ctx context.Context, state string, hc *corev1alpha1.HumioCluster) error {
	r.logger.Infof("setting cluster state to %s", state)
	hc.Status.State = state
	err := r.client.Status().Update(ctx, hc)
	if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileHumioCluster) setVersion(ctx context.Context, version string, hc *corev1alpha1.HumioCluster) {
	r.logger.Infof("setting cluster version to %s", version)
	hc.Status.Version = version
	err := r.client.Status().Update(ctx, hc)
	if err != nil {
		r.logger.Errorf("unable to set version status %s", err)
	}
}

func (r *ReconcileHumioCluster) setNodeCount(ctx context.Context, nodeCount int, hc *corev1alpha1.HumioCluster) {
	r.logger.Infof("setting cluster node count to %d", nodeCount)
	hc.Status.NodeCount = nodeCount
	err := r.client.Status().Update(ctx, hc)
	if err != nil {
		r.logger.Errorf("unable to set node count status %s", err)
	}
}

func (r *ReconcileHumioCluster) setPod(ctx context.Context, hc *corev1alpha1.HumioCluster) {
	r.logger.Info("setting cluster pod status")
	var pvcs []corev1.PersistentVolumeClaim
	pods, err := kubernetes.ListPods(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.logger.Errorf("unable to set pod status: %s", err)
	}

	if pvcsEnabled(hc) {
		pvcs, err = kubernetes.ListPersistentVolumeClaims(r.client, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
		if err != nil {
			r.logger.Errorf("unable to set pod status: %s", err)
		}
	}

	hc.Status.PodStatus = []corev1alpha1.HumioPodStatus{}
	for _, pod := range pods {
		podStatus := corev1alpha1.HumioPodStatus{
			PodName: pod.Name,
		}
		if nodeIdStr, ok := pod.Labels[kubernetes.NodeIdLabelName]; ok {
			nodeId, err := strconv.Atoi(nodeIdStr)
			if err != nil {
				r.logger.Errorf("unable to set pod status, nodeid %s is invalid:", nodeIdStr, err)
			}
			podStatus.NodeId = nodeId
		}
		if pvcsEnabled(hc) {
			pvc, err := findPvcForPod(pvcs, pod)
			if err != nil {
				r.logger.Errorf("unable to set pod status: %s:", err)

			}
			podStatus.PvcName = pvc.Name
		}
		hc.Status.PodStatus = append(hc.Status.PodStatus, podStatus)
	}

	err = r.client.Status().Update(ctx, hc)
	if err != nil {
		r.logger.Errorf("unable to set pod status %s", err)
	}
}
