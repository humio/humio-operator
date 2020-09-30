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
	"reflect"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	humioIngestTokenPrometheusMetrics = newHumioIngestTokenPrometheusCollection()
)

type humioIngestTokenPrometheusCollection struct {
	Counters humioIngestTokenPrometheusCountersCollection
}

type humioIngestTokenPrometheusCountersCollection struct {
	SecretsCreated               prometheus.Counter
	ServiceAccountSecretsCreated prometheus.Counter
}

func newHumioIngestTokenPrometheusCollection() humioIngestTokenPrometheusCollection {
	return humioIngestTokenPrometheusCollection{
		Counters: humioIngestTokenPrometheusCountersCollection{
			SecretsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humioingesttoken_controller_secrets_created_total",
				Help: "Total number of secret objects created by controller",
			}),
			ServiceAccountSecretsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humioingesttoken_controller_service_account_secrets_created_total",
				Help: "Total number of service account secrets objects created by controller",
			}),
		},
	}
}

func init() {
	counters := reflect.ValueOf(humioIngestTokenPrometheusMetrics.Counters)
	for i := 0; i < counters.NumField(); i++ {
		metric := counters.Field(i).Interface().(prometheus.Counter)
		metrics.Registry.MustRegister(metric)
	}
}
