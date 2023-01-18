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
	LockGet     = "Get"
	LockRelease = "Release"
)

type ClusterGroupLock interface {
	Get(string) (reconcile.Result, error)
	Release(string) (reconcile.Result, error)
}

type clusterGroupLock struct {
	reconciler   *HumioClusterReconciler
	statusWriter client.StatusWriter
	humioCluster *humiov1alpha1.HumioCluster
}

func (r *HumioClusterReconciler) newClusterGroupLock(statusWriter client.StatusWriter, hc *humiov1alpha1.HumioCluster) ClusterGroupLock {
	return &clusterGroupLock{
		reconciler:   r,
		statusWriter: statusWriter,
		humioCluster: hc,
	}
}

func (a *clusterGroupLock) Get(state string) (reconcile.Result, error) {
	return a.reconciler.tryClusterGroupLock(
		a.statusWriter,
		a.humioCluster,
		state, LockGet)
}

func (a *clusterGroupLock) Release(state string) (reconcile.Result, error) {
	return a.reconciler.tryClusterGroupLock(
		a.statusWriter,
		a.humioCluster,
		state, LockRelease)
}

func (r *HumioClusterReconciler) tryClusterGroupLock(statusWriter client.StatusWriter, hc *humiov1alpha1.HumioCluster, state string, lock string) (reconcile.Result, error) {
	if !humioClusterGroupOrDefault(hc).Enabled {
		return reconcile.Result{}, nil
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		hcg := &humiov1alpha1.HumioClusterGroup{}
		if err := r.getLatestHumioClusterGroup(context.TODO(), hc, hcg); err != nil {
			if k8serrors.IsNotFound(err) {
				r.Log.Error(err, fmt.Sprintf("no cluster group with name \"%s\", skipping", humioClusterGroupOrDefault(hc).Name))
				return nil
			}
			return err
		}
		switch state {
		case humiov1alpha1.HumioClusterStateUpgrading:
			if lock == LockGet {
				if getClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateUpgrading)+1 > hcg.Spec.MaxConcurrentUpgrades {
					return fmt.Errorf("reached max concurrent upgrades (%d)", hcg.Spec.MaxConcurrentUpgrades)
				}
				incrementClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateUpgrading, hc.Name)
			}
			if lock == LockRelease {
				decrementClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateUpgrading, hc.Name)
			}
		case humiov1alpha1.HumioClusterStateRestarting:
			if lock == LockGet {
				if getClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateRestarting)+1 > hcg.Spec.MaxConcurrentRestarts {
					return fmt.Errorf("reached max concurrent restarts (%d)", hcg.Spec.MaxConcurrentRestarts)
				}
				incrementClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateRestarting, hc.Name)
			}
			if lock == LockRelease {
				decrementClusterGroupInProgress(hcg, humiov1alpha1.HumioClusterStateRestarting, hc.Name)
			}
		default:
			return fmt.Errorf("unknown state %s", state)
		}
		return statusWriter.Update(context.TODO(), hcg)
	}); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) getLatestHumioClusterGroup(ctx context.Context, hc *humiov1alpha1.HumioCluster, hcg *humiov1alpha1.HumioClusterGroup) error {
	return r.Get(ctx, types.NamespacedName{
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
