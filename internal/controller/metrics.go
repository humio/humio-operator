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

// Package controller implements Kubernetes controllers for Humio resources.
package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ReconcileDurationSeconds tracks reconcile loop duration.
	ReconcileDurationSeconds *prometheus.HistogramVec

	// ShadowReadFailuresTotal tracks shadow node pool read failures.
	ShadowReadFailuresTotal *prometheus.CounterVec

	// NodeCountUpdates tracks replica count changes and their sources.
	NodeCountUpdates *prometheus.CounterVec

	// ShadowStaleness tracks consecutive shadow read failures per pool.
	ShadowStaleness *prometheus.GaugeVec
)

func init() {
	ReconcileDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "humio_operator_reconcile_duration_seconds",
			Help:    "Duration of reconcile operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"controller", "pool"},
	)

	ShadowReadFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "humio_operator_shadow_read_failures_total",
			Help: "Total number of shadow node pool read failures",
		},
		[]string{"pool", "error_type"},
	)

	NodeCountUpdates = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "humio_operator_nodecount_updates_total",
			Help: "Count of nodeCount updates by source (hpa/spec/default) and clamp status",
		},
		[]string{"pool", "source", "clamped"},
	)

	ShadowStaleness = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "humio_operator_shadow_staleness_consecutive",
			Help: "Consecutive shadow read failures (real-time staleness indicator)",
		},
		[]string{"pool"},
	)

	// Register the metric with controller-runtime registry
	metrics.Registry.MustRegister(ReconcileDurationSeconds)
	metrics.Registry.MustRegister(ShadowReadFailuresTotal)
	metrics.Registry.MustRegister(NodeCountUpdates)
	metrics.Registry.MustRegister(ShadowStaleness)
}
