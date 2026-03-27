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

package cacheconfig

import (
	"fmt"
	"os"
	"strings"

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetCacheOptionsWithWatchNamespace returns cache.Options based on the watch configuration.
// Three mutually exclusive modes:
//   - WATCH_NAMESPACE set: watch specific namespaces (comma-separated)
//   - WATCH_LABEL_SELECTOR set: filter CRDs by label, exempt native types via ByObject
//   - Neither set: watch all namespaces
func GetCacheOptionsWithWatchNamespace() (cache.Options, error) {
	cacheOptions := cache.Options{}

	watchLabelSelector := os.Getenv("WATCH_LABEL_SELECTOR")
	watchNamespace := os.Getenv("WATCH_NAMESPACE")

	// Validate mutual exclusivity
	if watchNamespace != "" && watchLabelSelector != "" {
		return cacheOptions, fmt.Errorf("WATCH_NAMESPACE and WATCH_LABEL_SELECTOR are mutually exclusive")
	}

	// Label selector mode: filter CRDs by label, exempt native types.
	// Uses DefaultNamespaces with cache.AllNamespaces to ensure the cache has explicit namespace configuration for all types.
	if watchLabelSelector != "" {
		selector, parseErr := labels.Parse(watchLabelSelector)
		if parseErr != nil {
			return cacheOptions, fmt.Errorf("invalid WATCH_LABEL_SELECTOR %q: %w", watchLabelSelector, parseErr)
		}
		cacheOptions.DefaultNamespaces = map[string]cache.Config{
			cache.AllNamespaces: {LabelSelector: selector},
		}
		cacheOptions.ByObject = nativeTypesUnfiltered()
		return cacheOptions, nil
	}

	// Namespace mode (default logic)
	if watchNamespace == "" {
		return cacheOptions, nil
	}

	defaultNamespaces := make(map[string]cache.Config)
	for namespace := range strings.SplitSeq(watchNamespace, ",") {
		if namespace = strings.TrimSpace(namespace); namespace != "" {
			defaultNamespaces[namespace] = cache.Config{}
		}
	}

	if len(defaultNamespaces) > 0 {
		cacheOptions.DefaultNamespaces = defaultNamespaces
	}

	return cacheOptions, nil
}

// nativeTypesUnfiltered returns ByObject overrides for label selector mode.
// In label selector mode, the default label filters CRD objects by the configured label.
// Native objects created by the operator are filtered by app.kubernetes.io/managed-by=humio-operator instead.
// Types the operator reads but doesn't necessarily create (Secrets, ConfigMaps) use no filter.
//
// IMPORTANT: Update this map when adding new native Kubernetes types to any controller.
func nativeTypesUnfiltered() map[client.Object]cache.ByObject {
	managedByOperator := byObjectWithLabel(labels.SelectorFromSet(labels.Set{
		kubernetes.ManagedByLabelKey: kubernetes.ManagedByLabelValue,
	}))
	noFilter := byObjectWithLabel(labels.Everything())
	clusterScopedNoFilter := cache.ByObject{Label: labels.Everything()}

	m := map[client.Object]cache.ByObject{
		// Types created exclusively by this operator — filter by managed-by label
		&corev1.Pod{}:                            managedByOperator,
		&corev1.Service{}:                        managedByOperator,
		&corev1.ServiceAccount{}:                 managedByOperator,
		&corev1.PersistentVolumeClaim{}:          managedByOperator,
		&policyv1.PodDisruptionBudget{}:          managedByOperator,
		&networkingv1.Ingress{}:                  managedByOperator,
		&appsv1.Deployment{}:                     managedByOperator,
		&autoscalingv2.HorizontalPodAutoscaler{}: managedByOperator,

		// Types the operator reads but doesn't necessarily create — no filter
		&corev1.ConfigMap{}: noFilter,
		&corev1.Secret{}:    noFilter,

		// Cluster-scoped types — must use Label, not Namespaces
		&corev1.Node{}:                              clusterScopedNoFilter,
		&corev1.PersistentVolume{}:                  clusterScopedNoFilter,
		&apiextensionsv1.CustomResourceDefinition{}: clusterScopedNoFilter,
		&rbacv1.ClusterRole{}:                       clusterScopedNoFilter,
		&rbacv1.ClusterRoleBinding{}:                clusterScopedNoFilter,

		// Humio CRDs created internally by this operator
		&humiov1alpha1.HumioBootstrapToken{}:      managedByOperator,
		&humiov1alpha1.HumioTelemetryCollection{}: managedByOperator,
		&humiov1alpha1.HumioTelemetryExport{}:     managedByOperator,
	}
	if helpers.UseCertManager() {
		m[&cmapi.Certificate{}] = managedByOperator
		m[&cmapi.Issuer{}] = managedByOperator
	}
	return m
}

// byObjectWithLabel creates a ByObject that watches across all namespaces with the given label selector.
func byObjectWithLabel(selector labels.Selector) cache.ByObject {
	return cache.ByObject{
		Namespaces: map[string]cache.Config{
			cache.AllNamespaces: {LabelSelector: selector},
		},
	}
}
