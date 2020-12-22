/*
Copyright 2020 Humio https://humio.com

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"strconv"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"k8s.io/apimachinery/pkg/types"
)

// getLatestHumioCluster ensures we have the latest HumioCluster resource. It may have been changed during the
// reconciliation
func (r *HumioClusterReconciler) getLatestHumioCluster(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	return r.Get(ctx, types.NamespacedName{
		Name:      hc.Name,
		Namespace: hc.Namespace,
	}, hc)
}

// setState is used to change the cluster state
// TODO: we use this to determine if we should have a delay between startup of humio pods during bootstrap vs starting up pods during an image update
func (r *HumioClusterReconciler) setState(ctx context.Context, state string, hc *humiov1alpha1.HumioCluster) error {
	if hc.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting cluster state to %s", state))
	hc.Status.State = state
	return r.Status().Update(ctx, hc)
}

func (r *HumioClusterReconciler) setVersion(ctx context.Context, version string, hc *humiov1alpha1.HumioCluster) error {
	if hc.Status.State == version {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting cluster version to %s", version))
	hc.Status.Version = version
	return r.Status().Update(ctx, hc)
}

func (r *HumioClusterReconciler) setNodeCount(ctx context.Context, nodeCount int, hc *humiov1alpha1.HumioCluster) error {
	if hc.Status.NodeCount == nodeCount {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting cluster node count to %d", nodeCount))
	hc.Status.NodeCount = nodeCount
	return r.Status().Update(ctx, hc)
}

func (r *HumioClusterReconciler) setPod(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("setting cluster pod status")
	pods, err := kubernetes.ListPods(r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.Log.Error(err, "unable to set pod status")
		return err
	}

	hc.Status.PodStatus = []humiov1alpha1.HumioPodStatus{}
	for _, pod := range pods {
		podStatus := humiov1alpha1.HumioPodStatus{
			PodName: pod.Name,
		}
		if nodeIdStr, ok := pod.Labels[kubernetes.NodeIdLabelName]; ok {
			nodeId, err := strconv.Atoi(nodeIdStr)
			if err != nil {
				r.Log.Error(err, fmt.Sprintf("unable to set pod status, node id %s is invalid", nodeIdStr))
				return err
			}
			podStatus.NodeId = nodeId
		}
		if pvcsEnabled(hc) {
			for _, volume := range pod.Spec.Volumes {
				if volume.Name == "humio-data" {
					if volume.PersistentVolumeClaim != nil {
						podStatus.PvcName = volume.PersistentVolumeClaim.ClaimName
					} else {
						// This is not actually an error in every case. If the HumioCluster resource is migrating to
						// PVCs then this will happen in a rolling fashion thus some pods will not have PVCs for a
						// short time.
						r.Log.Info(fmt.Sprintf("unable to set pod pvc status for pod %s because there is no pvc attached to the pod", pod.Name))
					}
				}
			}
		}
		hc.Status.PodStatus = append(hc.Status.PodStatus, podStatus)
	}

	return r.Status().Update(ctx, hc)
}
