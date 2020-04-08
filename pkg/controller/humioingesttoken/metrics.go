package humioingesttoken

import (
	"reflect"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	prometheusMetrics = newPrometheusCollection()
)

type prometheusCollection struct {
	Counters prometheusCountersCollection
}

type prometheusCountersCollection struct {
	SecretsCreated               prometheus.Counter
	ServiceAccountSecretsCreated prometheus.Counter
}

func newPrometheusCollection() prometheusCollection {
	return prometheusCollection{
		Counters: prometheusCountersCollection{
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
	counters := reflect.ValueOf(prometheusMetrics.Counters)
	for i := 0; i < counters.NumField(); i++ {
		metric := counters.Field(i).Interface().(prometheus.Counter)
		metrics.Registry.MustRegister(metric)
	}
}
