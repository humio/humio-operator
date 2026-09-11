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

	// ExpireAfterDeletionsTotal counts pods deleted by the expireAfter cycling mechanism.
	// The operator uses a direct DELETE (not the Eviction subresource) to avoid
	// self-blocking on the operator-managed eviction-protection PDB, so this
	// counter reflects deletes, not evictions.
	ExpireAfterDeletionsTotal *prometheus.CounterVec

	// NextPodExpiryTimestampSeconds is the Unix timestamp at which the next pod in a pool becomes eligible for expiry-based cycling.
	NextPodExpiryTimestampSeconds *prometheus.GaugeVec

	// EvictionProtectionPDBTTLExpiredTotal counts safety-valve PDB removals: cluster was unstable for 2h and protection was lifted.
	EvictionProtectionPDBTTLExpiredTotal *prometheus.CounterVec

	// ExpireAfterSkipsTotal counts reconciles where expireAfter cycling was skipped; reason labels: on_delete_strategy, cluster_not_running, min_ready_throttle, waiting_on_pods.
	ExpireAfterSkipsTotal *prometheus.CounterVec
)

func init() {
	ReconcileDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "humio_operator_reconcile_duration_seconds",
			Help:    "Duration of reconcile operations in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"namespace", "controller", "pool"},
	)

	ShadowReadFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "humio_operator_shadow_read_failures_total",
			Help: "Total number of shadow node pool read failures",
		},
		[]string{"namespace", "pool", "error_type"},
	)

	NodeCountUpdates = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "humio_operator_nodecount_updates_total",
			Help: "Count of nodeCount updates by source (hpa/spec/default) and clamp status",
		},
		[]string{"namespace", "pool", "source", "clamped"},
	)

	ShadowStaleness = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "humio_operator_shadow_staleness_consecutive",
			Help: "Consecutive shadow read failures (real-time staleness indicator)",
		},
		[]string{"namespace", "pool"},
	)

	ExpireAfterDeletionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "humio_operator_expire_after_deletions_total",
			Help: "Total pods deleted by the expireAfter cycling mechanism (direct DELETE; not the Eviction subresource)",
		},
		[]string{"namespace", "pool"},
	)

	NextPodExpiryTimestampSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "humio_operator_next_pod_expiry_timestamp_seconds",
			Help: "Unix timestamp of the next pod expiry in a pool (0 if expireAfter not set)",
		},
		[]string{"namespace", "pool"},
	)

	EvictionProtectionPDBTTLExpiredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "humio_operator_eviction_protection_pdb_ttl_expired_total",
			Help: "Times the safety-valve fired: eviction-protection PDB removed after TTL because cluster was wedged",
		},
		[]string{"namespace", "pool"},
	)

	ExpireAfterSkipsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "humio_operator_expire_after_skips_total",
			Help: "Reconciles where expireAfter cycling was skipped; diagnose stalled cycling with reason label",
		},
		[]string{"namespace", "pool", "reason"},
	)

	metrics.Registry.MustRegister(
		ReconcileDurationSeconds,
		ShadowReadFailuresTotal,
		NodeCountUpdates,
		ShadowStaleness,
		ExpireAfterDeletionsTotal,
		NextPodExpiryTimestampSeconds,
		EvictionProtectionPDBTTLExpiredTotal,
		ExpireAfterSkipsTotal,
	)
}
