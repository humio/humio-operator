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
	humioClusterPrometheusMetrics = newHumioClusterPrometheusCollection()
)

type humioClusterPrometheusCollection struct {
	Counters humioClusterPrometheusCountersCollection
}

type humioClusterPrometheusCountersCollection struct {
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
	ConfigMapsCreated            prometheus.Counter
}

func newHumioClusterPrometheusCollection() humioClusterPrometheusCollection {
	return humioClusterPrometheusCollection{
		Counters: humioClusterPrometheusCountersCollection{
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
			ConfigMapsCreated: prometheus.NewCounter(prometheus.CounterOpts{
				Name: "humiocluster_controller_configmaps_created_total",
				Help: "Total number of configmap objects created by controller",
			}),
		},
	}
}

func init() {
	counters := reflect.ValueOf(humioClusterPrometheusMetrics.Counters)
	for i := 0; i < counters.NumField(); i++ {
		metric := counters.Field(i).Interface().(prometheus.Counter)
		metrics.Registry.MustRegister(metric)
	}
}