package humiocluster

// placeholder
// manage developer user password, get developer user token, store as a secret.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	humioapi "github.com/humio/cli/api"
	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/humio"
)

// GetPersistentToken asdf
func GetPersistentToken(hc *corev1alpha1.HumioCluster, url string, humioClient humio.Client) (string, error) {
	jwtToken, err := getJWTForSingleUser(hc, url)
	if err != nil {
		return "", err
	}
	err = humioClient.Authenticate(&humioapi.Config{Token: jwtToken})
	if err != nil {
		return "", err
	}

	persistentToken, err := humioClient.ApiToken()
	if err != nil {
		return "", err
	}
	return persistentToken, nil
}

// getJWTForSingleUser performs a login to humio with the given credentials and returns a valid JWT token
// url should be fmt.Sprintf("http://%s.%s:8080/api/v1/login", hc.Name, hc.Namespace)
func getJWTForSingleUser(hc *corev1alpha1.HumioCluster, url string) (string, error) {
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
	// TODO: get baseurl from humio service name
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(bytesRepresentation))

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

func getDeveloperUserPassword(hc *corev1alpha1.HumioCluster) (string, error) {
	// TODO: all this stuff
	// kubernetes.GetSecret(kubernetes.ServiceAccountSecretName, hc)
	// extract secret data
	// base64 decrypt
	return "blah", nil
}
