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

package webhooks

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/go-logr/logr"
	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1beta1 "github.com/humio/humio-operator/api/v1beta1"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
)

var _ webhook.CustomValidator = &HumioScheduledSearchValidator{}

const (
	expectedKindHss string = "HumioScheduledSearch"
	v1Hss           string = "v1alpha1"
	v2Hss           string = "v1beta1"
)

var expectedVersions = []string{v1Hss, v2Hss}

// HumioScheduledSearchValidator validates HumioScheduledSearch
type HumioScheduledSearchValidator struct {
	BaseLogger  logr.Logger
	Log         logr.Logger
	Client      client.Client
	HumioClient humio.Client
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *HumioScheduledSearchValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	err := v.validateKind(obj, expectedKindHss, expectedVersions)
	if err != nil {
		return nil, fmt.Errorf("error encountered while running HumioScheduledSearch validation webhook: %v", err)
	}

	return v.validatePayload(ctx, obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type
func (v *HumioScheduledSearchValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	err := v.validateKind(newObj, expectedKindHss, expectedVersions)
	if err != nil {
		return nil, fmt.Errorf("error encountered while running HumioScheduledSearch validation webhook: %v", err)
	}
	return v.validatePayload(ctx, newObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type
func (v *HumioScheduledSearchValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	// DELETE operatons don't hit the validate endpoint
	return nil, nil
}

func (v *HumioScheduledSearchValidator) buildWarnings(obj runtime.Object) (admission.Warnings, error) {
	_, ok := obj.(*corev1alpha1.HumioScheduledSearch)
	if !ok {
		return nil, fmt.Errorf("expected a HumioScheduledSearch object but got %T", obj)
	}
	return admission.Warnings{
		"core.humio.com/v1alpha1 HumioScheduledSearch is being deprecated; use core.humio.com/v1beta1",
	}, nil
}

func (v *HumioScheduledSearchValidator) validateKind(obj runtime.Object, expectedK string, expectedV []string) error {
	var err error

	kind := obj.GetObjectKind()
	if kind.GroupVersionKind().Kind != expectedK {
		return fmt.Errorf("unexpected Kind received in HumioScheduledSearch validation webhook: %v", kind.GroupVersionKind().Kind)
	}

	if !slices.Contains(expectedV, kind.GroupVersionKind().Version) {
		return fmt.Errorf("unexpected Version received in HumioScheduledSearch validation webhook: %v", kind.GroupVersionKind().Version)
	}
	return err
}

func (v *HumioScheduledSearchValidator) validatePayload(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	var err error
	kind := obj.GetObjectKind()

	if kind.GroupVersionKind().Version == v1Hss {
		return v.buildWarnings(obj)
	}
	if kind.GroupVersionKind().Version == v2Hss {
		// we need to check if the running Logscale version supports v1beta1 HumioScheduledSearch QueryTimestampType
		hss := obj.(*corev1beta1.HumioScheduledSearch)
		if hss.Spec.QueryTimestampType == humiographql.QueryTimestampTypeIngesttimestamp {
			clusterVersion, err := helpers.GetClusterImageVersion(ctx, v.Client, hss.Namespace, hss.Spec.ManagedClusterName, hss.Spec.ExternalClusterName)
			if err != nil {
				return nil, fmt.Errorf("could not retrieve cluster Logscale version: %v", err)
			}
			if exists, err := helpers.FeatureExists(clusterVersion, corev1beta1.HumioScheduledSearchV1alpha1DeprecatedInVersion); !exists {
				if err != nil {
					return nil, fmt.Errorf("could not check if feature exists: %v", err)
				}
				errString := fmt.Sprintf("The running Logscale version %s does not support HumioScheduledSearch with type: %v.\n",
					clusterVersion, humiographql.QueryTimestampTypeIngesttimestamp)
				errString += fmt.Sprintf("Upgrade to Logscale %v+ or use '%v' for field 'QueryTimestampType'",
					corev1beta1.HumioScheduledSearchV1alpha1DeprecatedInVersion, humiographql.QueryTimestampTypeEventtimestamp)
				return nil, errors.New(errString)
			}
		}
	}
	return nil, err
}
