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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"k8s.io/client-go/util/retry"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	HumioClusterGroupLockGet     = "Get"
	HumioClusterGroupLockRelease = "Release"
)

type clusterGroupLock struct {
	client       client.Client
	statusWriter client.StatusWriter
	humioCluster *humiov1alpha1.HumioCluster
}

func newClusterGroupLock(client client.Client, statusWriter client.StatusWriter, hc *humiov1alpha1.HumioCluster) *clusterGroupLock {
	return &clusterGroupLock{
		client:       client,
		statusWriter: statusWriter,
		humioCluster: hc,
	}
}

func (c *clusterGroupLock) tryClusterGroupLock(state string, lock string) (reconcile.Result, error) {
	if !humioClusterGroupOrDefault(c.humioCluster).Enabled {
		return reconcile.Result{}, nil
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		hcg := &humiov1alpha1.HumioClusterGroup{}
		if err := c.getLatestHumioClusterGroup(context.TODO(), c.humioCluster, hcg); err != nil {
			if k8serrors.IsNotFound(err) {
				return fmt.Errorf(fmt.Sprintf("no cluster group with name \"%s\"", humioClusterGroupOrDefault(c.humioCluster).Name))
			}
			return err
		}
		switch state {
		case humiov1alpha1.HumioClusterStateUpgrading:
			if lock == HumioClusterGroupLockGet {
				if getClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateUpgrading)+1 > hcg.Spec.MaxConcurrentUpgrades {
					return fmt.Errorf("reached max concurrent upgrades (%d)", hcg.Spec.MaxConcurrentUpgrades)
				}
				incrementClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateUpgrading, c.humioCluster.Name)
			}
			if lock == HumioClusterGroupLockRelease {
				decrementClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateUpgrading, c.humioCluster.Name)
			}
		case humiov1alpha1.HumioClusterStateRestarting:
			if lock == HumioClusterGroupLockGet {
				if getClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateRestarting)+1 > hcg.Spec.MaxConcurrentRestarts {
					return fmt.Errorf("reached max concurrent restarts (%d)", hcg.Spec.MaxConcurrentRestarts)
				}
				incrementClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateRestarting, c.humioCluster.Name)
			}
			if lock == HumioClusterGroupLockRelease {
				decrementClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateRestarting, c.humioCluster.Name)
			}
		default:
			return fmt.Errorf("unknown state %s", state)
		}
		return c.statusWriter.Update(context.TODO(), hcg)
	}); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (c *clusterGroupLock) getLatestHumioClusterGroup(ctx context.Context, hc *humiov1alpha1.HumioCluster, hcg *humiov1alpha1.HumioClusterGroup) error {
	return c.client.Get(ctx, types.NamespacedName{
		Name:      humioClusterGroupOrDefault(hc).Name,
		Namespace: hc.Namespace,
	}, hcg)
}

func getClusterGroupInProgress(hcg *humiov1alpha1.HumioClusterGroup, stateType string) int {
	for _, clusterGroupStatus := range hcg.Status {
		if clusterGroupStatus.Type == stateType {
			return clusterGroupStatus.InProgressCount
		}
	}
	return 0
}

func incrementClusterGroupInProgress(hcg *humiov1alpha1.HumioClusterGroup, stateType string, humioClusterName string) {
	if len(hcg.Status) == 0 {
		hcg.Status = []humiov1alpha1.HumioClusterGroupStatusHumioClusterStatusAggregate{
			{
				Type:            humiov1alpha1.HumioClusterStateUpgrading,
				InProgressCount: 0,
				ClusterList:     []humiov1alpha1.HumioClusterGroupStatusHumioClusterStatus{},
			},
			{
				Type:            humiov1alpha1.HumioClusterStateRestarting,
				InProgressCount: 0,
				ClusterList:     []humiov1alpha1.HumioClusterGroupStatusHumioClusterStatus{},
			},
		}
	}
	for idx, clusterGroupStatus := range hcg.Status {
		if clusterGroupStatus.Type == stateType {
			hcg.Status[idx].InProgressCount++
			for clusterIdx, cluster := range clusterGroupStatus.ClusterList {
				if cluster.ClusterName == humioClusterName {
					hcg.Status[idx].ClusterList[clusterIdx].LastUpdateTime = &metav1.Time{Time: time.Now()}
					return
				}
			}
			hcg.Status[idx].ClusterList = append(hcg.Status[idx].ClusterList,
				humiov1alpha1.HumioClusterGroupStatusHumioClusterStatus{
					ClusterName:    humioClusterName,
					ClusterState:   stateType,
					LastUpdateTime: &metav1.Time{Time: time.Now()},
				},
			)
		}
	}
}

func decrementClusterGroupInProgress(hcg *humiov1alpha1.HumioClusterGroup, stateType string, humioClusterName string) {
	for idx, clusterGroupStatus := range hcg.Status {
		if clusterGroupStatus.Type == stateType {
			hcg.Status[idx].InProgressCount--
			for clusterIdx, cluster := range clusterGroupStatus.ClusterList {
				if cluster.ClusterName == humioClusterName {
					hcg.Status[idx].ClusterList = append(hcg.Status[idx].ClusterList[:clusterIdx], hcg.Status[idx].ClusterList[clusterIdx+1:]...)
				}
			}
			return
		}
	}
}
