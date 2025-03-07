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

package controller

import (
	"context"
	"fmt"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
)

type Option interface {
	Apply(hc *humiov1alpha1.HumioCluster)
	GetResult() (reconcile.Result, error)
}

type optionBuilder struct {
	options []Option
}

func (o *optionBuilder) Get() []Option {
	return o.options
}

type messageOption struct {
	message string
}

type stateOption struct {
	state                     string
	nodePoolName              string
	zoneUnderMaintenance      string
	desiredPodRevision        int
	desiredPodHash            string
	desiredBootstrapTokenHash string
}

type stateOptionList struct {
	statesList []stateOption
}

type versionOption struct {
	version string
}

type podsOption struct {
	pods humiov1alpha1.HumioPodStatusList
}

type licenseOption struct {
	license humiov1alpha1.HumioLicenseStatus
}

type nodeCountOption struct {
	nodeCount int
}

type observedGenerationOption struct {
	observedGeneration int64
}

type StatusOptions interface {
	Get() []Option
}

func statusOptions() *optionBuilder {
	return &optionBuilder{
		options: []Option{},
	}
}

func (o *optionBuilder) withMessage(msg string) *optionBuilder {
	o.options = append(o.options, messageOption{
		message: msg,
	})
	return o
}

func (o *optionBuilder) withState(state string) *optionBuilder {
	o.options = append(o.options, stateOption{
		state: state,
	})
	return o
}

func (o *optionBuilder) withNodePoolState(state string, nodePoolName string, podRevision int, podHash string, bootstrapTokenHash string, zoneName string) *optionBuilder {
	o.options = append(o.options, stateOption{
		state:                     state,
		nodePoolName:              nodePoolName,
		zoneUnderMaintenance:      zoneName,
		desiredPodRevision:        podRevision,
		desiredPodHash:            podHash,
		desiredBootstrapTokenHash: bootstrapTokenHash,
	})
	return o
}

func (o *optionBuilder) withNodePoolStatusList(humioNodePoolStatusList humiov1alpha1.HumioNodePoolStatusList) *optionBuilder {
	statesList := make([]stateOption, len(humioNodePoolStatusList))
	idx := 0
	for _, poolStatus := range humioNodePoolStatusList {
		statesList[idx] = stateOption{
			nodePoolName:              poolStatus.Name,
			state:                     poolStatus.State,
			zoneUnderMaintenance:      poolStatus.ZoneUnderMaintenance,
			desiredPodRevision:        poolStatus.DesiredPodRevision,
			desiredPodHash:            poolStatus.DesiredPodHash,
			desiredBootstrapTokenHash: poolStatus.DesiredBootstrapTokenHash,
		}
		idx++
	}
	o.options = append(o.options, stateOptionList{
		statesList: statesList,
	})
	return o
}

func (o *optionBuilder) withVersion(version string) *optionBuilder {
	o.options = append(o.options, versionOption{
		version: version,
	})
	return o
}

func (o *optionBuilder) withPods(pods humiov1alpha1.HumioPodStatusList) *optionBuilder {
	o.options = append(o.options, podsOption{
		pods: pods,
	})
	return o
}

func (o *optionBuilder) withLicense(license humiov1alpha1.HumioLicenseStatus) *optionBuilder {
	o.options = append(o.options, licenseOption{
		license: license,
	})
	return o
}

func (o *optionBuilder) withNodeCount(nodeCount int) *optionBuilder {
	o.options = append(o.options, nodeCountOption{
		nodeCount: nodeCount,
	})
	return o
}

func (o *optionBuilder) withObservedGeneration(observedGeneration int64) *optionBuilder {
	o.options = append(o.options, observedGenerationOption{
		observedGeneration: observedGeneration,
	})
	return o
}

func (m messageOption) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.Message = m.message
}

func (messageOption) GetResult() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (s stateOption) Apply(hc *humiov1alpha1.HumioCluster) {
	if s.state != "" {
		hc.Status.State = s.state
	}

	if s.nodePoolName != "" {
		for idx, nodePoolStatus := range hc.Status.NodePoolStatus {
			if nodePoolStatus.Name == s.nodePoolName {
				nodePoolStatus.State = s.state
				nodePoolStatus.ZoneUnderMaintenance = s.zoneUnderMaintenance
				nodePoolStatus.DesiredPodRevision = s.desiredPodRevision
				nodePoolStatus.DesiredPodHash = s.desiredPodHash
				nodePoolStatus.DesiredBootstrapTokenHash = s.desiredBootstrapTokenHash
				hc.Status.NodePoolStatus[idx] = nodePoolStatus
				return
			}
		}

		hc.Status.NodePoolStatus = append(hc.Status.NodePoolStatus, humiov1alpha1.HumioNodePoolStatus{
			Name:                      s.nodePoolName,
			State:                     s.state,
			ZoneUnderMaintenance:      s.zoneUnderMaintenance,
			DesiredPodRevision:        s.desiredPodRevision,
			DesiredPodHash:            s.desiredPodHash,
			DesiredBootstrapTokenHash: s.desiredBootstrapTokenHash,
		})
	}
}

func (s stateOption) GetResult() (reconcile.Result, error) {
	if s.state == humiov1alpha1.HumioClusterStateRestarting || s.state == humiov1alpha1.HumioClusterStateUpgrading ||
		s.state == humiov1alpha1.HumioClusterStatePending {
		return reconcile.Result{RequeueAfter: time.Second * 1}, nil
	}
	if s.state == humiov1alpha1.HumioClusterStateConfigError {
		return reconcile.Result{RequeueAfter: time.Second * 10}, nil
	}
	return reconcile.Result{RequeueAfter: time.Second * 15}, nil
}

func (s stateOptionList) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.NodePoolStatus = humiov1alpha1.HumioNodePoolStatusList{}
	for _, poolStatus := range s.statesList {
		hc.Status.NodePoolStatus = append(hc.Status.NodePoolStatus, humiov1alpha1.HumioNodePoolStatus{
			Name:                      poolStatus.nodePoolName,
			State:                     poolStatus.state,
			ZoneUnderMaintenance:      poolStatus.zoneUnderMaintenance,
			DesiredPodRevision:        poolStatus.desiredPodRevision,
			DesiredPodHash:            poolStatus.desiredPodHash,
			DesiredBootstrapTokenHash: poolStatus.desiredBootstrapTokenHash,
		})
	}
}

func (s stateOptionList) GetResult() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (v versionOption) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.Version = v.version
}

func (versionOption) GetResult() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (p podsOption) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.PodStatus = p.pods
}

func (podsOption) GetResult() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (l licenseOption) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.LicenseStatus = l.license
}

func (licenseOption) GetResult() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (n nodeCountOption) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.NodeCount = n.nodeCount
}

func (nodeCountOption) GetResult() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (o observedGenerationOption) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.ObservedGeneration = fmt.Sprintf("%d", o.observedGeneration)
}

func (observedGenerationOption) GetResult() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) updateStatus(ctx context.Context, statusWriter client.StatusWriter, hc *humiov1alpha1.HumioCluster, options StatusOptions) (reconcile.Result, error) {
	opts := options.Get()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.getLatestHumioCluster(ctx, hc)
		if err != nil {
			return err
		}
		for _, opt := range opts {
			opt.Apply(hc)
		}
		return statusWriter.Update(ctx, hc)
	}); err != nil {
		return reconcile.Result{}, err
	}
	for _, opt := range opts {
		if res, err := opt.GetResult(); err != nil {
			return res, err
		}
	}
	for _, opt := range opts {
		res, _ := opt.GetResult()
		if res.Requeue || res.RequeueAfter > 0 {
			return res, nil
		}
	}
	return reconcile.Result{}, nil
}

// getLatestHumioCluster ensures we have the latest HumioCluster resource. It may have been changed during the
// reconciliation
func (r *HumioClusterReconciler) getLatestHumioCluster(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	return r.Get(ctx, types.NamespacedName{
		Name:      hc.Name,
		Namespace: hc.Namespace,
	}, hc)
}

// setState is used to change the cluster state
func (r *HumioClusterReconciler) setState(ctx context.Context, state string, hc *humiov1alpha1.HumioCluster) error {
	if hc.Status.State == state {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting cluster state to %s", state))
	// TODO: fix the logic in ensureMismatchedPodsAreDeleted() to allow it to work without doing setStateOptimistically().
	if err := r.setStateOptimistically(ctx, state, hc); err != nil {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			err := r.getLatestHumioCluster(ctx, hc)
			if err != nil {
				if !k8serrors.IsNotFound(err) {
					return err
				}
			}
			hc.Status.State = state
			return r.Status().Update(ctx, hc)
		})
		if err != nil {
			return r.logErrorAndReturn(err, "failed to update resource status")
		}
	}
	return nil
}

// setStateOptimistically will attempt to set the state without fetching the latest HumioCluster
func (r *HumioClusterReconciler) setStateOptimistically(ctx context.Context, state string, hc *humiov1alpha1.HumioCluster) error {
	if hc.Status.State == state {
		return nil
	}
	hc.Status.State = state
	return r.Status().Update(ctx, hc)
}