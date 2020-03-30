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

// GetPersistentToken performs a login using the single-user credentials to obtain a JWT token,
// then uses the JWT token to extract the persistent API token for the same user
func GetPersistentToken(hc *corev1alpha1.HumioCluster, url, password string, humioClient humio.Client) (string, error) {
	jwtToken, err := getJWTForSingleUser(hc, url, password)
	if err != nil {
		return "", err
	}
	err = humioClient.Authenticate(&humioapi.Config{Token: jwtToken, Address: url})
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
func getJWTForSingleUser(hc *corev1alpha1.HumioCluster, url, password string) (string, error) {
	message := map[string]interface{}{
		"login":    "developer",
		"password": password,
	}

	bytesRepresentation, err := json.Marshal(message)
	if err != nil {
		return "", err
	}
	// TODO: get baseurl from humio service name
	resp, err := http.Post(fmt.Sprintf("%sapi/v1/login", url), "application/json", bytes.NewBuffer(bytesRepresentation))

	if err != nil {
		return "", fmt.Errorf("could not perform login to obtain jwt token: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("wrong status code when fetching token for user %s, expected 200, but got: %v", "developer", resp.Status)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("unable to decode response body: %s", err)
	}

	return result["token"], nil
}
