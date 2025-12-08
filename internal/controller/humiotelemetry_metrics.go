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
	"reflect"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	humioTelemetryPrometheusMetrics = newHumioTelemetryPrometheusCollection()
)

type humioTelemetryPrometheusCollection struct {
	Counters humioTelemetryPrometheusCountersCollection
	Gauges   humioTelemetryPrometheusGaugesCollection
}

type humioTelemetryPrometheusCountersCollection struct {
	CollectionsTotal          prometheus.Counter
	CollectionsSuccessTotal   prometheus.Counter
	CollectionsErrorTotal     prometheus.Counter
	ExportsTotal              prometheus.Counter
	ExportsSuccessTotal       prometheus.Counter
	ExportsErrorTotal         prometheus.Counter
	TelemetryResourcesCreated prometheus.Counter
	TelemetryResourcesDeleted prometheus.Counter
}

type humioTelemetryPrometheusGaugesCollection struct {
	ActiveTelemetryResources prometheus.Gauge
}

func newHumioTelemetryPrometheusCollection() humioTelemetryPrometheusCollection {
	return humioTelemetryPrometheusCollection{
		Counters: humioTelemetryPrometheusCountersCollection{
			CollectionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiotelemetry_controller_collections_total",
				Help: "Total number of telemetry data collections attempted",
			}),
			CollectionsSuccessTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiotelemetry_controller_collections_success_total",
				Help: "Total number of successful telemetry data collections",
			}),
			CollectionsErrorTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiotelemetry_controller_collections_error_total",
				Help: "Total number of failed telemetry data collections",
			}),
			ExportsTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiotelemetry_controller_exports_total",
				Help: "Total number of telemetry data export attempts",
			}),
			ExportsSuccessTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiotelemetry_controller_exports_success_total",
				Help: "Total number of successful telemetry data exports",
			}),
			ExportsErrorTotal: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiotelemetry_controller_exports_error_total",
				Help: "Total number of failed telemetry data exports",
			}),
			TelemetryResourcesCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiotelemetry_controller_resources_created_total",
				Help: "Total number of HumioTelemetry resources created",
			}),
			TelemetryResourcesDeleted: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiotelemetry_controller_resources_deleted_total",
				Help: "Total number of HumioTelemetry resources deleted",
			}),
		},
		Gauges: humioTelemetryPrometheusGaugesCollection{
			ActiveTelemetryResources: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: "humiotelemetry_controller_active_resources",
				Help: "Current number of active HumioTelemetry resources",
			}),
		},
	}
}

func init() {
	// Register counters
	counters := reflect.ValueOf(humioTelemetryPrometheusMetrics.Counters)
	for i := 0; i < counters.NumField(); i++ {
		metric := counters.Field(i).Interface().(prometheus.Counter)
		metrics.Registry.MustRegister(metric)
	}

	// Register gauges
	gauges := reflect.ValueOf(humioTelemetryPrometheusMetrics.Gauges)
	for i := 0; i < gauges.NumField(); i++ {
		metric := gauges.Field(i).Interface().(prometheus.Gauge)
		metrics.Registry.MustRegister(metric)
	}
}
