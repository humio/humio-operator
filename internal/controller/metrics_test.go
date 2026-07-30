//go:build !integration
// +build !integration

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
	"testing"
	"time"

	io_prometheus_client "github.com/prometheus/client_model/go"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const labelPool = "pool"

const controllerLabel = "humiocluster"

func TestReconcileDurationMetric(t *testing.T) {
	t.Run("histogram appears after first observation", func(t *testing.T) {
		// Metric appears in registry after first observation
		metricName := "humio_operator_reconcile_duration_seconds"

		// Observe a value to ensure metric appears
		ReconcileDurationSeconds.WithLabelValues("registration-test", "test-pool").Observe(0.1)

		metricFamilies, err := metrics.Registry.Gather()
		if err != nil {
			t.Fatalf("failed to gather metrics: %v", err)
		}

		registered := false
		for _, mf := range metricFamilies {
			if mf.GetName() == metricName {
				registered = true
				if mf.GetType() != io_prometheus_client.MetricType_HISTOGRAM {
					t.Errorf("expected metric type HISTOGRAM, got %v", mf.GetType())
				}
				break
			}
		}

		if !registered {
			t.Errorf("metric %s not registered in prometheus registry", metricName)
		}
	})

	t.Run("observing duration increases metric value", func(t *testing.T) {
		// Get initial histogram count by querying the metric
		before := getHistogramCount("test-pool")

		// Observe a duration
		ReconcileDurationSeconds.WithLabelValues(controllerLabel, "test-pool").Observe(0.5)

		// Verify count increased
		after := getHistogramCount("test-pool")

		if after <= before {
			t.Errorf("expected metric observation count to increase, got before=%d, after=%d", before, after)
		}
	})

	t.Run("multiple observations accumulate", func(t *testing.T) {
		pool := "multi-observe-pool"
		controller := controllerLabel

		// Record initial state
		initial := getHistogramCount(pool)

		// Observe multiple durations
		ReconcileDurationSeconds.WithLabelValues(controller, pool).Observe(0.1)
		ReconcileDurationSeconds.WithLabelValues(controller, pool).Observe(0.2)
		ReconcileDurationSeconds.WithLabelValues(controller, pool).Observe(0.3)

		// Verify accumulated
		final := getHistogramCount(pool)

		if final != initial+3 {
			t.Errorf("expected 3 accumulated observations, got initial=%d, final=%d", initial, final)
		}
	})

	t.Run("observes realistic reconcile durations", func(t *testing.T) {
		pool := "realistic-pool"
		controller := controllerLabel

		// Simulate a reconcile loop timing
		start := time.Now()
		time.Sleep(10 * time.Millisecond)
		duration := time.Since(start).Seconds()

		initial := getHistogramCount(pool)
		ReconcileDurationSeconds.WithLabelValues(controller, pool).Observe(duration)
		final := getHistogramCount(pool)

		if final != initial+1 {
			t.Errorf("expected metric to record one observation, got initial=%d, final=%d", initial, final)
		}

		if duration < 0.01 {
			t.Errorf("expected duration >= 10ms, got %f seconds", duration)
		}
	})
}

func TestShadowReadFailuresMetric(t *testing.T) {
	t.Run("counter appears after first increment", func(t *testing.T) {
		// Metric appears in registry after first increment
		metricName := "humio_operator_shadow_read_failures_total"

		// Increment counter to ensure metric appears
		ShadowReadFailuresTotal.WithLabelValues("registration-test", "not_found").Inc()

		metricFamilies, err := metrics.Registry.Gather()
		if err != nil {
			t.Fatalf("failed to gather metrics: %v", err)
		}

		registered := false
		for _, mf := range metricFamilies {
			if mf.GetName() == metricName {
				registered = true
				if mf.GetType() != io_prometheus_client.MetricType_COUNTER {
					t.Errorf("expected metric type COUNTER, got %v", mf.GetType())
				}
				break
			}
		}

		if !registered {
			t.Errorf("metric %s not registered in prometheus registry", metricName)
		}
	})

	t.Run("incrementing counter increases metric value", func(t *testing.T) {
		// Get initial counter value
		before := getShadowReadFailuresCounter("test-pool", "not_found")

		// Increment the counter
		ShadowReadFailuresTotal.WithLabelValues("test-pool", "not_found").Inc()

		// Verify count increased
		after := getShadowReadFailuresCounter("test-pool", "not_found")

		if after != before+1 {
			t.Errorf("expected counter to increase by 1, got before=%f, after=%f", before, after)
		}
	})

	t.Run("multiple increments accumulate", func(t *testing.T) {
		// Record initial state
		initial := getShadowReadFailuresCounter("multi-pool", "timeout")

		// Increment multiple times
		ShadowReadFailuresTotal.WithLabelValues("multi-pool", "timeout").Inc()
		ShadowReadFailuresTotal.WithLabelValues("multi-pool", "timeout").Inc()
		ShadowReadFailuresTotal.WithLabelValues("multi-pool", "timeout").Inc()

		// Verify accumulated
		final := getShadowReadFailuresCounter("multi-pool", "timeout")

		if final != initial+3 {
			t.Errorf("expected 3 accumulated increments, got initial=%f, final=%f", initial, final)
		}
	})
}

// getHistogramCount returns the sample count for a histogram with given labels.
func getHistogramCount(pool string) uint64 {
	metricFamilies, err := metrics.Registry.Gather()
	if err != nil {
		return 0
	}

	for _, mf := range metricFamilies {
		if mf.GetName() == "humio_operator_reconcile_duration_seconds" {
			for _, m := range mf.GetMetric() {
				labels := m.GetLabel()
				if len(labels) == 2 &&
					labels[0].GetName() == "controller" && labels[0].GetValue() == controllerLabel &&
					labels[1].GetName() == labelPool && labels[1].GetValue() == pool {
					if h := m.GetHistogram(); h != nil {
						return h.GetSampleCount()
					}
				}
			}
		}
	}
	return 0
}

// getShadowReadFailuresCounter returns the current value of the shadow read failures counter with specific labels.
func getShadowReadFailuresCounter(pool, errorType string) float64 {
	metricFamilies, err := metrics.Registry.Gather()
	if err != nil {
		return 0
	}

	for _, mf := range metricFamilies {
		if mf.GetName() == "humio_operator_shadow_read_failures_total" {
			for _, m := range mf.GetMetric() {
				labels := m.GetLabel()
				if len(labels) == 2 &&
					labels[0].GetName() == "error_type" && labels[0].GetValue() == errorType &&
					labels[1].GetName() == labelPool && labels[1].GetValue() == pool {
					if c := m.GetCounter(); c != nil {
						return c.GetValue()
					}
				}
			}
		}
	}
	return 0
}

func TestNodeCountUpdatesMetric(t *testing.T) {
	t.Run("counter appears after first increment", func(t *testing.T) {
		// Metric appears in registry after first increment
		metricName := "humio_operator_nodecount_updates_total"

		// Increment counter to ensure metric appears
		NodeCountUpdates.WithLabelValues("registration-test", "hpa", "false").Inc()

		metricFamilies, err := metrics.Registry.Gather()
		if err != nil {
			t.Fatalf("failed to gather metrics: %v", err)
		}

		registered := false
		for _, mf := range metricFamilies {
			if mf.GetName() == metricName {
				registered = true
				if mf.GetType() != io_prometheus_client.MetricType_COUNTER {
					t.Errorf("expected metric type COUNTER, got %v", mf.GetType())
				}
				break
			}
		}

		if !registered {
			t.Errorf("metric %s not registered in prometheus registry", metricName)
		}
	})

	t.Run("incrementing counter with labels increases metric value", func(t *testing.T) {
		// Get initial counter value for specific labels
		before := getNodeCountUpdatesCounter("test-pool", "hpa", "true")

		// Increment the counter
		NodeCountUpdates.WithLabelValues("test-pool", "hpa", "true").Inc()

		// Verify count increased
		after := getNodeCountUpdatesCounter("test-pool", "hpa", "true")

		if after != before+1 {
			t.Errorf("expected counter to increase by 1, got before=%f, after=%f", before, after)
		}
	})

	t.Run("different label combinations track independently", func(t *testing.T) {
		// Track different label combinations
		pool1Before := getNodeCountUpdatesCounter("pool-1", "hpa", "false")
		pool2Before := getNodeCountUpdatesCounter("pool-2", "spec", "true")

		// Increment different combinations
		NodeCountUpdates.WithLabelValues("pool-1", "hpa", "false").Inc()
		NodeCountUpdates.WithLabelValues("pool-2", "spec", "true").Inc()
		NodeCountUpdates.WithLabelValues("pool-2", "spec", "true").Inc()

		// Verify each combination tracked independently
		pool1After := getNodeCountUpdatesCounter("pool-1", "hpa", "false")
		pool2After := getNodeCountUpdatesCounter("pool-2", "spec", "true")

		if pool1After != pool1Before+1 {
			t.Errorf("pool-1 expected +1, got before=%f, after=%f", pool1Before, pool1After)
		}

		if pool2After != pool2Before+2 {
			t.Errorf("pool-2 expected +2, got before=%f, after=%f", pool2Before, pool2After)
		}
	})

	t.Run("all valid source values work", func(t *testing.T) {
		sources := []string{"hpa", "spec", "default"}
		for _, source := range sources {
			before := getNodeCountUpdatesCounter("source-test-pool", source, "false")
			NodeCountUpdates.WithLabelValues("source-test-pool", source, "false").Inc()
			after := getNodeCountUpdatesCounter("source-test-pool", source, "false")

			if after != before+1 {
				t.Errorf("source %s: expected counter to increase by 1, got before=%f, after=%f", source, before, after)
			}
		}
	})
}

// getNodeCountUpdatesCounter returns the current value of the nodecount updates counter with specific labels.
func getNodeCountUpdatesCounter(pool, source, clamped string) float64 {
	metricFamilies, err := metrics.Registry.Gather()
	if err != nil {
		return 0
	}

	for _, mf := range metricFamilies {
		if mf.GetName() == "humio_operator_nodecount_updates_total" {
			for _, m := range mf.GetMetric() {
				labels := m.GetLabel()
				if len(labels) == 3 &&
					labels[0].GetName() == "clamped" && labels[0].GetValue() == clamped &&
					labels[1].GetName() == labelPool && labels[1].GetValue() == pool &&
					labels[2].GetName() == "source" && labels[2].GetValue() == source {
					if c := m.GetCounter(); c != nil {
						return c.GetValue()
					}
				}
			}
		}
	}
	return 0
}

func TestShadowStalenessMetric(t *testing.T) {
	t.Run("gauge appears after first set", func(t *testing.T) {
		// Metric appears in registry after first set
		metricName := "humio_operator_shadow_staleness_consecutive"

		// Set gauge value to ensure metric appears
		ShadowStaleness.WithLabelValues("registration-test").Set(1)

		metricFamilies, err := metrics.Registry.Gather()
		if err != nil {
			t.Fatalf("failed to gather metrics: %v", err)
		}

		registered := false
		for _, mf := range metricFamilies {
			if mf.GetName() == metricName {
				registered = true
				if mf.GetType() != io_prometheus_client.MetricType_GAUGE {
					t.Errorf("expected metric type GAUGE, got %v", mf.GetType())
				}
				break
			}
		}

		if !registered {
			t.Errorf("metric %s not registered in prometheus registry", metricName)
		}
	})

	t.Run("setting gauge value updates metric", func(t *testing.T) {
		// Set gauge to 5
		ShadowStaleness.WithLabelValues("test-pool").Set(5)

		// Verify gauge value is 5
		value := getShadowStalenessGauge("test-pool")

		if value != 5.0 {
			t.Errorf("expected gauge value 5.0, got %f", value)
		}
	})

	t.Run("resetting gauge to zero", func(t *testing.T) {
		pool := "reset-pool"

		// Set to non-zero value first
		ShadowStaleness.WithLabelValues(pool).Set(10)

		// Reset to 0
		ShadowStaleness.WithLabelValues(pool).Set(0)

		// Verify gauge value is 0
		value := getShadowStalenessGauge(pool)

		if value != 0.0 {
			t.Errorf("expected gauge value 0.0, got %f", value)
		}
	})

	t.Run("different pools track independently", func(t *testing.T) {
		// Set different values for different pools
		ShadowStaleness.WithLabelValues("pool-a").Set(3)
		ShadowStaleness.WithLabelValues("pool-b").Set(7)

		// Verify each pool has its own value
		valueA := getShadowStalenessGauge("pool-a")
		valueB := getShadowStalenessGauge("pool-b")

		if valueA != 3.0 {
			t.Errorf("pool-a expected 3.0, got %f", valueA)
		}

		if valueB != 7.0 {
			t.Errorf("pool-b expected 7.0, got %f", valueB)
		}
	})
}

// getShadowStalenessGauge returns the current value of the shadow staleness gauge for a specific pool.
func getShadowStalenessGauge(pool string) float64 {
	metricFamilies, err := metrics.Registry.Gather()
	if err != nil {
		return 0
	}

	for _, mf := range metricFamilies {
		if mf.GetName() == "humio_operator_shadow_staleness_consecutive" {
			for _, m := range mf.GetMetric() {
				labels := m.GetLabel()
				if len(labels) == 1 &&
					labels[0].GetName() == labelPool && labels[0].GetValue() == pool {
					if g := m.GetGauge(); g != nil {
						return g.GetValue()
					}
				}
			}
		}
	}
	return 0
}
