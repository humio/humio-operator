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
	"reflect"
	"sort"
	"strconv"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/client-go/util/retry"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"k8s.io/apimachinery/pkg/types"
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
	state string
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

func (m messageOption) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.Message = m.message
}

func (m messageOption) GetResult() (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

func (s stateOption) Apply(hc *humiov1alpha1.HumioCluster) {
	hc.Status.State = s.state
}

func (s stateOption) GetResult() (reconcile.Result, error) {
	if s.state == humiov1alpha1.HumioClusterStateRestarting || s.state == humiov1alpha1.HumioClusterStateUpgrading {
		return reconcile.Result{RequeueAfter: time.Second * 5}, nil
	}
	if s.state == humiov1alpha1.HumioClusterStatePending {
		return reconcile.Result{RequeueAfter: time.Second * 1}, nil
	}
	return reconcile.Result{}, nil
}

func (r *HumioClusterReconciler) updateStatus(statusWriter client.StatusWriter, hc *humiov1alpha1.HumioCluster, options StatusOptions) (reconcile.Result, error) {
	opts := options.Get()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.getLatestHumioCluster(context.TODO(), hc)
		if err != nil {
			return err
		}
		for _, opt := range opts {
			opt.Apply(hc)
		}
		return statusWriter.Update(context.TODO(), hc)
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
				if !errors.IsNotFound(err) {
					return err
				}
			}
			hc.Status.State = state
			err = r.Status().Update(ctx, hc)
			if err != nil {
			}
			return err
		})
		if err != nil {
			return fmt.Errorf("failed to update resource status: %w", err)
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

func (r *HumioClusterReconciler) setVersion(ctx context.Context, version string, hc *humiov1alpha1.HumioCluster) error {
	if hc.Status.State == version {
		return nil
	}
	if version == "" {
		version = "Unknown"
	}
	r.Log.Info(fmt.Sprintf("setting cluster version to %s", version))
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.getLatestHumioCluster(ctx, hc)
		if err != nil {
			return err
		}
		hc.Status.Version = version
		return r.Status().Update(ctx, hc)
	})
	if err != nil {
		return fmt.Errorf("failed to update resource status: %w", err)
	}
	return nil
}

func (r *HumioClusterReconciler) setLicense(ctx context.Context, licenseStatus humiov1alpha1.HumioLicenseStatus, hc *humiov1alpha1.HumioCluster) error {
	if reflect.DeepEqual(hc.Status.LicenseStatus, licenseStatus) {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting cluster license status to %v", licenseStatus))
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.getLatestHumioCluster(ctx, hc)
		if err != nil {
			return err
		}
		hc.Status.LicenseStatus = licenseStatus
		return r.Status().Update(ctx, hc)
	})
	if err != nil {
		return fmt.Errorf("failed to update resource status: %w", err)
	}
	return nil
}

func (r *HumioClusterReconciler) setNodeCount(ctx context.Context, nodeCount int, hc *humiov1alpha1.HumioCluster) error {
	if hc.Status.NodeCount == nodeCount {
		return nil
	}
	r.Log.Info(fmt.Sprintf("setting cluster node count to %d", nodeCount))
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.getLatestHumioCluster(ctx, hc)
		if err != nil {
			return err
		}
		hc.Status.NodeCount = nodeCount
		return r.Status().Update(ctx, hc)
	})
	if err != nil {
		return fmt.Errorf("failed to update resource status: %w", err)
	}
	return nil
}

func (r *HumioClusterReconciler) setPod(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	r.Log.Info("setting cluster pod status")
	pods, err := kubernetes.ListPods(ctx, r, hc.Namespace, kubernetes.MatchingLabelsForHumio(hc.Name))
	if err != nil {
		r.Log.Error(err, "unable to set pod status")
		return err
	}

	podStatusList := humiov1alpha1.HumioPodStatusList{}
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
		podStatusList = append(podStatusList, podStatus)
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.getLatestHumioCluster(ctx, hc)
		if err != nil {
			return err
		}
		sort.Sort(podStatusList)
		hc.Status.PodStatus = podStatusList
		return r.Status().Update(ctx, hc)
	})
	if err != nil {
		return fmt.Errorf("failed to update resource status: %w", err)
	}
	return nil
}

func (r *HumioClusterReconciler) setObservedGeneration(ctx context.Context, hc *humiov1alpha1.HumioCluster) error {
	if hc.Status.ObservedGeneration == fmt.Sprintf("%d", hc.GetGeneration()) {
		return nil
	}

	r.Log.Info(fmt.Sprintf("setting ObservedGeneration to %d", hc.GetGeneration()))
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		err := r.getLatestHumioCluster(ctx, hc)
		if err != nil {
			return err
		}
		hc.Status.ObservedGeneration = fmt.Sprintf("%d", hc.GetGeneration())
		return r.Status().Update(ctx, hc)
	})
	if err != nil {
		return fmt.Errorf("failed to update resource status: %w", err)
	}
	return nil
}
