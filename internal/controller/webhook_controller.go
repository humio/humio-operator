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
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	"github.com/humio/humio-operator/internal/helpers"
)

var (
	// ValidatingWebhookConfigurationName name of the k8s ValidatingWebhookConfiguration to create
	ValidatingWebhookConfigurationName = "humio-crd-validation"
	// CRDsRequiringConversion keep a list of CRDs we want to auto-migrate between versions to auto add conversion webhooks
	CRDsRequiringConversion = []string{"humioscheduledsearches.core.humio.com"}
	// GVKs Define the CRDs for which to create validation webhooks
	GVKs = []schema.GroupVersionKind{
		{
			Group:   "core.humio.com",
			Version: "v1alpha1",
			Kind:    "HumioScheduledSearch",
		},
		{
			Group:   "core.humio.com",
			Version: "v1beta1",
			Kind:    "HumioScheduledSearch",
		},
		// Add more GVKs here as needed
	}
	webhooks             []admissionregistrationv1.ValidatingWebhook
	webhookComponentName string = "webhook"
)

// WebhookClientConfigProvider defines the interface for creating webhook client configurations
type WebhookClientConfigProvider interface {
	GetClientConfig(namespace, serviceName string, caBundle []byte) *apiextensionsv1.WebhookClientConfig
}

// ServiceInfo holds service configuration information
type ServiceInfo struct {
	Name       string
	TargetPort int32
}

// ValidatingWebhookConfigurationProvider defines the interface for creating ValidatingWebhookConfigurations
type ValidatingWebhookConfigurationProvider interface {
	CreateValidatingWebhookConfiguration(namespace, operatorName string, caBundle []byte, gvks []schema.GroupVersionKind) *admissionregistrationv1.ValidatingWebhookConfiguration
	GetServiceInfo() *ServiceInfo
}

// ServiceBasedClientConfigProvider creates service-based webhook client configurations for production
type ServiceBasedClientConfigProvider struct{}

func (s *ServiceBasedClientConfigProvider) GetClientConfig(namespace, serviceName string, caBundle []byte) *apiextensionsv1.WebhookClientConfig {
	return &apiextensionsv1.WebhookClientConfig{
		Service: &apiextensionsv1.ServiceReference{
			Namespace: namespace,
			Name:      serviceName,
			Path:      helpers.StringPtr("/convert"),
		},
		CABundle: caBundle,
	}
}

// ServiceBasedValidatingWebhookProvider creates service-based ValidatingWebhookConfigurations for production
type ServiceBasedValidatingWebhookProvider struct{}

func (s *ServiceBasedValidatingWebhookProvider) GetServiceInfo() *ServiceInfo {
	return &ServiceInfo{
		Name:       helpers.GetOperatorWebhookServiceName(),
		TargetPort: 9443,
	}
}

func (s *ServiceBasedValidatingWebhookProvider) CreateValidatingWebhookConfiguration(namespace, operatorName string, caBundle []byte, gvks []schema.GroupVersionKind) *admissionregistrationv1.ValidatingWebhookConfiguration {
	failurePolicy := admissionregistrationv1.Fail
	sideEffects := admissionregistrationv1.SideEffectClassNone
	matchPolicy := admissionregistrationv1.Exact
	admissionReviewVersions := []string{"v1"}

	// Create a webhook for each GVK
	for _, gvk := range gvks {
		webhookPath := getValidationWebhookPath(gvk)
		// Convert resource name from singular to plural (add 's')
		pluralResource := getPluralForCrd(strings.ToLower(gvk.Kind))

		webhook := admissionregistrationv1.ValidatingWebhook{
			Name: fmt.Sprintf("v%s-%s.%s", strings.ToLower(gvk.Kind), gvk.Version, gvk.Group),
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{
					Namespace: namespace,
					Name:      helpers.GetOperatorWebhookServiceName(),
					Path:      helpers.StringPtr(webhookPath),
				},
				CABundle: caBundle,
			},
			Rules: []admissionregistrationv1.RuleWithOperations{
				{
					Operations: []admissionregistrationv1.OperationType{
						admissionregistrationv1.Create,
						admissionregistrationv1.Update,
					},
					Rule: admissionregistrationv1.Rule{
						APIGroups:   []string{gvk.Group},
						APIVersions: []string{gvk.Version},
						Resources:   []string{pluralResource},
					},
				},
			},
			FailurePolicy:           &failurePolicy,
			SideEffects:             &sideEffects,
			AdmissionReviewVersions: admissionReviewVersions,
			MatchPolicy:             &matchPolicy,
		}
		webhooks = append(webhooks, webhook)
	}

	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: ValidatingWebhookConfigurationName,
			Labels: map[string]string{
				"app.kubernetes.io/name":     operatorName,
				"app.kubernetes.io/instance": operatorName,
			},
		},
		Webhooks: webhooks,
	}
}

// URLBasedValidatingWebhookProvider creates URL-based ValidatingWebhookConfigurations for testing
type URLBasedValidatingWebhookProvider struct {
	WebhookPort int
	WebhookHost string
}

func (u *URLBasedValidatingWebhookProvider) GetServiceInfo() *ServiceInfo {
	return nil // URL-based providers don't need Services
}

func (u *URLBasedValidatingWebhookProvider) CreateValidatingWebhookConfiguration(namespace, operatorName string, caBundle []byte, gvks []schema.GroupVersionKind) *admissionregistrationv1.ValidatingWebhookConfiguration {
	failurePolicy := admissionregistrationv1.Fail
	sideEffects := admissionregistrationv1.SideEffectClassNone
	matchPolicy := admissionregistrationv1.Exact
	admissionReviewVersions := []string{"v1"}

	// Create a webhook for each GVK
	for _, gvk := range gvks {
		webhookPath := getValidationWebhookPath(gvk)
		// Convert resource name from singular to plural (add 's')
		pluralResource := getPluralForCrd(strings.ToLower(gvk.Kind))

		webhook := admissionregistrationv1.ValidatingWebhook{
			Name: fmt.Sprintf("v%s-%s.%s", strings.ToLower(gvk.Kind), gvk.Version, gvk.Group),
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				URL:      &[]string{fmt.Sprintf("https://%s:%d%s", u.WebhookHost, u.WebhookPort, webhookPath)}[0],
				CABundle: caBundle,
			},
			Rules: []admissionregistrationv1.RuleWithOperations{
				{
					Operations: []admissionregistrationv1.OperationType{
						admissionregistrationv1.Create,
						admissionregistrationv1.Update,
					},
					Rule: admissionregistrationv1.Rule{
						APIGroups:   []string{gvk.Group},
						APIVersions: []string{gvk.Version},
						Resources:   []string{pluralResource},
					},
				},
			},
			FailurePolicy:           &failurePolicy,
			SideEffects:             &sideEffects,
			AdmissionReviewVersions: admissionReviewVersions,
			MatchPolicy:             &matchPolicy,
		}
		webhooks = append(webhooks, webhook)
	}

	return &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: ValidatingWebhookConfigurationName,
			Labels: map[string]string{
				"app.kubernetes.io/name":     operatorName,
				"app.kubernetes.io/instance": operatorName,
			},
		},
		Webhooks: webhooks,
	}
}

type URLBasedClientConfigProvider struct {
	WebhookPort int
	WebhookHost string
}

func (u *URLBasedClientConfigProvider) GetClientConfig(namespace, serviceName string, caBundle []byte) *apiextensionsv1.WebhookClientConfig {
	// Use standard conversion path for both testing and production
	webhookURL := fmt.Sprintf("https://%s:%d/convert", u.WebhookHost, u.WebhookPort)
	return &apiextensionsv1.WebhookClientConfig{
		URL:      &webhookURL,
		CABundle: caBundle,
	}
}

// NewProductionWebhookSetupReconciler creates a reconciler configured for production use
func NewProductionWebhookSetupReconciler(client client.Client, cache cache.Cache, baseLogger logr.Logger, certGenerator *helpers.WebhookCertGenerator,
	operatorName, namespace string, requeuePeriod time.Duration) *WebhookSetupReconciler {
	return &WebhookSetupReconciler{
		Client: client,
		CommonConfig: CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		BaseLogger:                             baseLogger,
		CertGenerator:                          certGenerator,
		OperatorName:                           operatorName,
		Cache:                                  cache,
		Namespace:                              namespace,
		ClientConfigProvider:                   &ServiceBasedClientConfigProvider{},
		ValidatingWebhookConfigurationProvider: &ServiceBasedValidatingWebhookProvider{},
	}
}

// NewTestWebhookSetupReconciler creates a reconciler configured for testing use
func NewTestWebhookSetupReconciler(client client.Client, cache cache.Cache, baseLogger logr.Logger, certGenerator *helpers.WebhookCertGenerator,
	operatorName, namespace string, requeuePeriod time.Duration, webhookPort int, webhookHost string) *WebhookSetupReconciler {
	return &WebhookSetupReconciler{
		Client: client,
		CommonConfig: CommonConfig{
			RequeuePeriod: requeuePeriod,
		},
		BaseLogger:    baseLogger,
		CertGenerator: certGenerator,
		OperatorName:  operatorName,
		Cache:         cache,
		Namespace:     namespace,
		ClientConfigProvider: &URLBasedClientConfigProvider{
			WebhookPort: webhookPort,
			WebhookHost: webhookHost,
		},
		ValidatingWebhookConfigurationProvider: &URLBasedValidatingWebhookProvider{
			WebhookPort: webhookPort,
			WebhookHost: webhookHost,
		},
	}
}

type WebhookSetupReconciler struct {
	client.Client
	CommonConfig
	BaseLogger                             logr.Logger
	CertGenerator                          *helpers.WebhookCertGenerator
	OperatorName                           string
	Cache                                  cache.Cache
	Namespace                              string
	ClientConfigProvider                   WebhookClientConfigProvider
	ValidatingWebhookConfigurationProvider ValidatingWebhookConfigurationProvider
}

// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=get;list;create;update;patch;watch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;update;patch;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;create;update;patch

// Helper function to check if a CRD requires conversion webhook
func (r *WebhookSetupReconciler) requiresConversionWebhook(crdName string) bool {
	return slices.Contains(CRDsRequiringConversion, crdName)
}

func (r *WebhookSetupReconciler) updateCRD(crd *apiextensionsv1.CustomResourceDefinition, caBundle []byte, log logr.Logger) bool {
	updated := false

	// Check if this CRD requires conversion webhook setup
	if r.requiresConversionWebhook(crd.Name) {
		log.Info("setting conversion webhook configuration for CRD", "crd", crd.Name)

		// Get the operator namespace and validate it's not empty
		namespace := helpers.GetOperatorNamespace()
		if namespace == "" {
			namespace = r.Namespace
		}
		serviceName := helpers.GetOperatorWebhookServiceName()

		// Get ClientConfig from provider
		clientConfig := r.ClientConfigProvider.GetClientConfig(namespace, serviceName, caBundle)

		// Create the complete conversion configuration
		conversion := &apiextensionsv1.CustomResourceConversion{
			Strategy: apiextensionsv1.WebhookConverter,
			Webhook: &apiextensionsv1.WebhookConversion{
				ClientConfig:             clientConfig,
				ConversionReviewVersions: []string{"v1", "v1beta1"},
			},
		}

		// Set the conversion configuration
		crd.Spec.Conversion = conversion
		updated = true
	}

	return updated
}

// updateCRDWithRetry handles resource version conflicts by re-reading and retrying
func (r *WebhookSetupReconciler) updateCRDWithRetry(ctx context.Context, crd *apiextensionsv1.CustomResourceDefinition, caBundle []byte, log logr.Logger) error {
	const maxRetries = 3
	const backOff = 2

	for attempt := range maxRetries {
		if attempt > 0 {
			// Re-read the CRD to get the latest resource version
			if err := r.Get(ctx, client.ObjectKey{Name: crd.Name}, crd); err != nil {
				return fmt.Errorf("failed to re-read CRD on attempt %d: %w", attempt+1, err)
			}

			// Apply our changes to the fresh copy
			if !r.updateCRD(crd, caBundle, log) {
				// No update needed
				return nil
			}
		}
		// Try to update
		if err := r.Update(ctx, crd); err != nil {
			if client.IgnoreNotFound(err) != nil && attempt < maxRetries-1 {
				log.Info("resource version conflict, retrying", "crd", crd.Name, "attempt", attempt+1)
				time.Sleep(time.Second * backOff) // sleep before retry
				continue
			}
			return err
		}
		return nil
	}

	return fmt.Errorf("failed to update CRD after %d attempts", maxRetries)
}

func (r *WebhookSetupReconciler) readCABundle(namespace string) ([]byte, error) {
	certGen := helpers.NewCertGenerator(r.CertGenerator.CertPath, r.CertGenerator.CertName, r.CertGenerator.KeyName, r.CertGenerator.ServiceName, namespace)
	certPEM, err := certGen.GetCABundle()
	if err != nil {
		return nil, fmt.Errorf("could not read certificate file %s/%s", r.CertGenerator.CertPath, r.CertGenerator.CertName)
	}
	return certPEM, nil
}

// SyncExistingResources performs initial sync of all existing webhooks and CRDs
func (r *WebhookSetupReconciler) SyncExistingResources(ctx context.Context) error {
	log := r.BaseLogger.WithValues("component", "webhook-setup", "operation", "sync-existing")
	log.Info("starting initial sync of existing CRDs")

	// Read CA bundle once for all CRD updates
	caBundle, err := r.readCABundle(r.Namespace)
	if err != nil {
		log.Error(err, "unable to read CA bundle from certificate file")
		return fmt.Errorf("failed to read CA bundle: %w", err)
	}

	// Sync existing CustomResourceDefinitions that require conversion webhooks
	var crds apiextensionsv1.CustomResourceDefinitionList
	if err := r.List(ctx, &crds); err != nil {
		log.Error(err, "failed to list CustomResourceDefinitions")
		return err
	}

	log.Info("Found CRDs during sync", "count", len(crds.Items))
	for _, crd := range crds.Items {
		if r.requiresConversionWebhook(crd.Name) {
			log.Info("configuring conversion webhook for CRD", "name", crd.Name)
			// Update CRD with conversion webhook configuration
			if r.updateCRD(&crd, caBundle, log) {
				if err := r.updateCRDWithRetry(ctx, &crd, caBundle, log); err != nil {
					log.Error(err, "failed to update CRD", "crd", crd.Name)
				} else {
					log.Info("successfully configured CRD conversion webhook", "crd", crd.Name)
				}
			} else {
				log.Info("CRD conversion webhook already in sync", "crd", crd.Name)
			}
		}
	}

	log.Info("completed initial sync of existing CRDs")
	return nil
}

// createOrUpdateWebhookService creates or updates the Service resource needed for the webhook configuration
func (r *WebhookSetupReconciler) createOrUpdateWebhookService(ctx context.Context, serviceName string, targetPort int32, log logr.Logger) error {
	// Define the desired service configuration
	desiredService := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: r.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       r.OperatorName,
				"app.kubernetes.io/instance":   r.OperatorName,
				"app.kubernetes.io/managed-by": r.OperatorName,
				"app.kubernetes.io/component":  webhookComponentName,
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "webhook",
					Port:       443,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromInt32(targetPort),
				},
			},
			Selector: map[string]string{
				"app.kubernetes.io/instance":  r.OperatorName,
				"app.kubernetes.io/component": webhookComponentName,
				"app":                         r.OperatorName,
			},
		},
	}

	// Check if Service already exists
	existingService := &corev1.Service{}
	serviceKey := client.ObjectKey{Name: serviceName, Namespace: r.Namespace}
	if err := r.Get(ctx, serviceKey, existingService); err == nil {
		// Service exists, update it with desired configuration
		existingService.ObjectMeta = desiredService.ObjectMeta
		existingService.Spec = desiredService.Spec

		if err := r.Update(ctx, existingService); err != nil {
			return fmt.Errorf("failed to update Service: %w", err)
		}
		log.Info("updated webhook Service", "name", serviceName, "namespace", r.Namespace, "targetPort", targetPort)
		return nil
	} else if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to check Service existence: %w", err)
	}

	// Service doesn't exist, create it
	if err := r.Create(ctx, desiredService); err != nil {
		if client.IgnoreAlreadyExists(err) == nil {
			log.Info("service was created by another process", "name", serviceName)
		} else {
			return fmt.Errorf("failed to create Service: %w", err)
		}
	} else {
		log.Info("created webhook validation k8s service", "name", serviceName, "namespace", r.Namespace, "targetPort", targetPort)
	}

	return nil
}

// createOrUpdateValidatingWebhookConfiguration creates or updates the ValidatingWebhookConfiguration
func (r *WebhookSetupReconciler) createOrUpdateValidatingWebhookConfiguration(ctx context.Context, webhookConfig *admissionregistrationv1.ValidatingWebhookConfiguration, log logr.Logger) error {
	existingWebhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	webhookKey := client.ObjectKey{Name: webhookConfig.Name}
	if err := r.Get(ctx, webhookKey, existingWebhook); err == nil {
		// Webhook exists, update it with desired configuration
		existingWebhook.Labels = webhookConfig.Labels
		existingWebhook.Webhooks = webhookConfig.Webhooks

		if err := r.Update(ctx, existingWebhook); err != nil {
			return fmt.Errorf("failed to update ValidatingWebhookConfiguration: %w", err)
		}
		log.Info("updated ValidatingWebhookConfiguration", "name", webhookConfig.Name)
		return nil
	} else if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to check ValidatingWebhookConfiguration existence: %w", err)
	}

	// ValidatingWebhookConfiguration doesn't exist, create it
	if err := r.Create(ctx, webhookConfig); err != nil {
		return fmt.Errorf("failed to create ValidatingWebhookConfiguration: %w", err)
	}
	log.Info("created ValidatingWebhookConfiguration", "name", webhookConfig.Name)
	return nil
}

// Start implements the manager.Runnable for automatic start
func (r *WebhookSetupReconciler) Start(ctx context.Context) error {
	log := r.BaseLogger.WithValues("component", "webhook-setup", "operation", "start")
	log.Info("starting WebhookSetupReconciler initial reconciler, waiting for caches to sync")

	// This waits for all caches to be synced
	if r.Cache != nil {
		if !r.Cache.WaitForCacheSync(ctx) {
			return fmt.Errorf("failed to wait for cache sync")
		}
	} else {
		// Fallback: short delay if cache not available
		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	log.Info("caches synced, creating webhook resources")

	// Read CA bundle for ValidatingWebhookConfiguration creation
	caBundle, err := r.readCABundle(r.Namespace)
	if err != nil {
		log.Error(err, "unable to read CA bundle from certificate file")
		return fmt.Errorf("failed to read CA bundle: %w", err)
	}

	// Create ValidatingWebhookConfiguration using the provider
	if r.ValidatingWebhookConfigurationProvider != nil {
		// Check if Service is needed and create it
		serviceInfo := r.ValidatingWebhookConfigurationProvider.GetServiceInfo()
		if serviceInfo != nil {
			log.Info("creating k8s service for webhook setup", "serviceName", serviceInfo.Name, "targetPort", serviceInfo.TargetPort)
			if _, err := helpers.Retry(func() (any, error) {
				return nil, r.createOrUpdateWebhookService(ctx, serviceInfo.Name, serviceInfo.TargetPort, log)
			}, 5, 1*time.Second); err != nil {
				return fmt.Errorf("failed to create webhook service: %w", err)
			}
		}

		webhookConfig := r.ValidatingWebhookConfigurationProvider.CreateValidatingWebhookConfiguration(r.Namespace, r.OperatorName, caBundle, GVKs)

		// Create or update the ValidatingWebhookConfiguration
		if _, err := helpers.Retry(func() (any, error) {
			return nil, r.createOrUpdateValidatingWebhookConfiguration(ctx, webhookConfig, log)
		}, 5, 1*time.Second); err != nil {
			return fmt.Errorf("failed to create or update ValidatingWebhookConfiguration: %w", err)
		}
	}

	log.Info("performing initial resource sync")
	return r.SyncExistingResources(ctx)
}

// this is how controller-runtime implicitly generates the webhook path
func getValidationWebhookPath(gvk schema.GroupVersionKind) string {
	group := strings.ReplaceAll(gvk.Group, ".", "-")
	kind := strings.ToLower(gvk.Kind)
	return fmt.Sprintf("/validate-%s-%s-%s", group, gvk.Version, kind)
}

func getPluralForCrd(kind string) string {
	var plural string
	switch kind {
	case "humioscheduledsearch":
		plural = "humioscheduledsearches"
	default:
		plural = kind + "s"
	}
	return plural
}
