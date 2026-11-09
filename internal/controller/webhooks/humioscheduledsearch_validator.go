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

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/go-logr/logr"
	corev1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	corev1beta1 "github.com/humio/humio-operator/api/v1beta1"
	"github.com/humio/humio-operator/internal/api/humiographql"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
)

var (
	_ admission.Validator[*corev1alpha1.HumioScheduledSearch] = &HumioScheduledSearchValidator[*corev1alpha1.HumioScheduledSearch]{}
	_ admission.Validator[*corev1beta1.HumioScheduledSearch]  = &HumioScheduledSearchValidator[*corev1beta1.HumioScheduledSearch]{}
)

// HumioScheduledSearchValidator validates a HumioScheduledSearch of API version T.
type HumioScheduledSearchValidator[T runtime.Object] struct {
	BaseLogger  logr.Logger
	Log         logr.Logger
	Client      client.Client
	HumioClient humio.Client
}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type
func (v *HumioScheduledSearchValidator[T]) ValidateCreate(ctx context.Context, obj T) (admission.Warnings, error) {
	return v.validatePayload(ctx, obj)
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type
func (v *HumioScheduledSearchValidator[T]) ValidateUpdate(ctx context.Context, oldObj, newObj T) (admission.Warnings, error) {
	return v.validatePayload(ctx, newObj)
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type
func (v *HumioScheduledSearchValidator[T]) ValidateDelete(ctx context.Context, obj T) (admission.Warnings, error) {
	// DELETE operatons don't hit the validate endpoint
	return nil, nil
}

func (v *HumioScheduledSearchValidator[T]) validatePayload(ctx context.Context, obj T) (admission.Warnings, error) {
	switch hss := any(obj).(type) {
	case *corev1alpha1.HumioScheduledSearch:
		return admission.Warnings{
			"core.humio.com/v1alpha1 HumioScheduledSearch is being deprecated; use core.humio.com/v1beta1",
		}, nil
	case *corev1beta1.HumioScheduledSearch:
		// we need to check if the running Logscale version supports v1beta1 HumioScheduledSearch QueryTimestampType
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
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected type received in HumioScheduledSearch validation webhook: %T", obj)
	}
}
