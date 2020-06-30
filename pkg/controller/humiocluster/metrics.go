package humiocluster

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
	PodsCreated                  prometheus.Counter
	PodsDeleted                  prometheus.Counter
	PvcsCreated                  prometheus.Counter
	PvcsDeleted                  prometheus.Counter
	SecretsCreated               prometheus.Counter
	ClusterRolesCreated          prometheus.Counter
	ClusterRoleBindingsCreated   prometheus.Counter
	RolesCreated                 prometheus.Counter
	RoleBindingsCreated          prometheus.Counter
	ServiceAccountsCreated       prometheus.Counter
	ServiceAccountSecretsCreated prometheus.Counter
	IngressesCreated             prometheus.Counter
}

func newPrometheusCollection() prometheusCollection {
	return prometheusCollection{
		Counters: prometheusCountersCollection{
			PodsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_pods_created_total",
				Help: "Total number of pod objects created by controller",
			}),
			PodsDeleted: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_pods_deleted_total",
				Help: "Total number of pod objects deleted by controller",
			}),
			PvcsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_pvcs_created_total",
				Help: "Total number of pvc objects created by controller",
			}),
			PvcsDeleted: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_pvcs_deleted_total",
				Help: "Total number of pvc objects deleted by controller",
			}),
			SecretsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_secrets_created_total",
				Help: "Total number of secret objects created by controller",
			}),
			ClusterRolesCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_cluster_roles_created_total",
				Help: "Total number of cluster role objects created by controller",
			}),
			ClusterRoleBindingsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_cluster_role_bindings_created_total",
				Help: "Total number of cluster role bindings objects created by controller",
			}),
			RolesCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_roles_created_total",
				Help: "Total number of role objects created by controller",
			}),
			RoleBindingsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_role_bindings_created_total",
				Help: "Total number of role bindings objects created by controller",
			}),
			ServiceAccountsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_service_accounts_created_total",
				Help: "Total number of service accounts objects created by controller",
			}),
			ServiceAccountSecretsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_service_account_secrets_created_total",
				Help: "Total number of service account secrets objects created by controller",
			}),
			IngressesCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_ingresses_created_total",
				Help: "Total number of ingress objects created by controller",
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
