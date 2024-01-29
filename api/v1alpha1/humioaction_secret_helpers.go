package v1alpha1

import "fmt"

var HaSecrets map[string]string = make(map[string]string)

func HaHasSecret(hn *HumioAction) (string, bool) {
	if secret, found := HaSecrets[fmt.Sprintf("%s-%s", hn.Namespace, hn.Name)]; found {
		return secret, true
	}
	return "", false
}

// Call this to set the secret in the map
func SecretFromHa(hn *HumioAction, token string) {
	key := fmt.Sprintf("%s-%s", hn.Namespace, hn.Name)
	value := token
	HaSecrets[key] = value
}
