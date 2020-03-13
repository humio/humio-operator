package humio

// placeholder
// manage developer user password, get developer user token, store as a secret.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
)

// GetJWTForSingleUser performs a login to humio with the given credentials and returns a valid JWT token
func GetJWTForSingleUser(hc corev1alpha1.HumioCluster) (string, error) {
	password, err := getDeveloperUserPassword(hc)
	if err != nil {
		return "", err
	}
	message := map[string]interface{}{
		"login":    "developer",
		"password": password,
	}

	bytesRepresentation, err := json.Marshal(message)
	if err != nil {
		return "", err
	}
	// TODO: where to get BaseURL?
	// resp, err := http.Post(hc.Status.BaseURL+"api/v1/login", "application/json", bytes.NewBuffer(bytesRepresentation))
	resp, err := http.Post("baseurl/api/v1/login", "application/json", bytes.NewBuffer(bytesRepresentation))

	if err != nil {
		return "", fmt.Errorf("could not perform login to obtain jwt token: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("wrong status code when fetching token, expected 200, but got: %v", resp.Status)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("unable to decode response body: %v", err)
	}

	return result["token"], nil
}

func getDeveloperUserPassword(hc corev1alpha1.HumioCluster) (string, error) {
	// TODO: all this stuff
	// kubernetes.GetSecret(kubernetes.ServiceAccountSecretName, hc)
	// extract secret data
	// base64 decrypt
	return "blah", nil
}
