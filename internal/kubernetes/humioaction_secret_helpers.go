package kubernetes

import (
	"fmt"
	"sync"

	"github.com/humio/humio-operator/api/v1alpha1"
)

var (
	haSecrets          = make(map[string]string)
	haSecretsMu        sync.Mutex
	haWebhookHeaders   = make(map[string]map[string]string)
	haWebhookHeadersMu sync.Mutex
)

func GetSecretForHa(hn *v1alpha1.HumioAction) (string, bool) {
	haSecretsMu.Lock()
	defer haSecretsMu.Unlock()
	if secret, found := haSecrets[fmt.Sprintf("%s %s", hn.Namespace, hn.Name)]; found {
		return secret, true
	}
	return "", false
}

func StoreSingleSecretForHa(hn *v1alpha1.HumioAction, token string) {
	haSecretsMu.Lock()
	defer haSecretsMu.Unlock()
	key := fmt.Sprintf("%s %s", hn.Namespace, hn.Name)
	haSecrets[key] = token
}

func GetFullSetOfMergedWebhookheaders(hn *v1alpha1.HumioAction) (map[string]string, bool) {
	haWebhookHeadersMu.Lock()
	defer haWebhookHeadersMu.Unlock()
	if secret, found := haWebhookHeaders[fmt.Sprintf("%s %s", hn.Namespace, hn.Name)]; found {
		return secret, true
	}
	return nil, false
}

func StoreFullSetOfMergedWebhookActionHeaders(hn *v1alpha1.HumioAction, resolvedSecretHeaders map[string]string) {
	haWebhookHeadersMu.Lock()
	defer haWebhookHeadersMu.Unlock()
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
