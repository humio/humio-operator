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
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
)

const (
	alertQueryStart  = "24h"
	alertQueryEnd    = "now"
	alertQueryIsLive = true
)

func setAlertDefaults(ha *humiov1alpha1.HumioAlert) {
	if ha.Spec.Query.IsLive == nil {
		ha.Spec.Query.IsLive = helpers.BoolPtr(alertQueryIsLive)
	}
	if ha.Spec.Query.Start == "" {
		ha.Spec.Query.Start = alertQueryStart
	}
	if ha.Spec.Query.End == "" {
		ha.Spec.Query.End = alertQueryEnd
	}
}
