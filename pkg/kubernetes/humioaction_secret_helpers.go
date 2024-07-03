package kubernetes

import (
	"fmt"
	"github.com/humio/humio-operator/api/v1alpha1"
)

var haSecrets map[string]string = make(map[string]string)
var haWebhookHeaders map[string]map[string]string = make(map[string]map[string]string)

func GetSecretForHa(hn *v1alpha1.HumioAction) (string, bool) {
	if secret, found := haSecrets[fmt.Sprintf("%s %s", hn.Namespace, hn.Name)]; found {
		return secret, true
	}
	return "", false
}

func StoreSingleSecretForHa(hn *v1alpha1.HumioAction, token string) {
	key := fmt.Sprintf("%s %s", hn.Namespace, hn.Name)
	haSecrets[key] = token
}

func GetFullSetOfMergedWebhookheaders(hn *v1alpha1.HumioAction) (map[string]string, bool) {
	if secret, found := haWebhookHeaders[fmt.Sprintf("%s %s", hn.Namespace, hn.Name)]; found {
		return secret, true
	}
	return nil, false
}

func StoreFullSetOfMergedWebhookActionHeaders(hn *v1alpha1.HumioAction, resolvedSecretHeaders map[string]string) {
	key := fmt.Sprintf("%s %s", hn.Namespace, hn.Name)
	if len(resolvedSecretHeaders) == 0 {
		haWebhookHeaders[key] = hn.Spec.WebhookProperties.Headers
		return
	}
	if hn.Spec.WebhookProperties.Headers == nil {
		haWebhookHeaders[key] = resolvedSecretHeaders
		return
	}
	mergedHeaders := make(map[string]string, len(hn.Spec.WebhookProperties.Headers)+len(resolvedSecretHeaders))
	for headerName, headerValue := range hn.Spec.WebhookProperties.Headers {
		mergedHeaders[headerName] = headerValue
	}
	for headerName, headerValue := range resolvedSecretHeaders {
		mergedHeaders[headerName] = headerValue
	}
	haWebhookHeaders[key] = mergedHeaders
}
