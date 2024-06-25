package v1alpha1

import "fmt"

var haSecrets map[string]string = make(map[string]string)
var haWebhookHeaders map[string]map[string]string = make(map[string]map[string]string) // TODO: or maybe we just add it as a magic annotation?

func GetSecretForHa(hn *HumioAction) (string, bool) {
	if secret, found := haSecrets[fmt.Sprintf("%s %s", hn.Namespace, hn.Name)]; found {
		return secret, true
	}
	return "", false
}

// Call this to set the secret in the map
func SetSecretForHa(hn *HumioAction, token string) {
	key := fmt.Sprintf("%s %s", hn.Namespace, hn.Name)
	haSecrets[key] = token
}
