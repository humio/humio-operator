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

package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"time"

	humio "github.com/humio/cli/api"
	"github.com/shurcooL/graphql"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	// load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

// perhaps we move these somewhere else?
const localAdminTokenFile = "/data/humio-data/local-admin-token.txt"
const globalSnapshotFile = "/data/humio-data/global-data-snapshot.json"
const adminAccountUserName = "admin" // TODO: Pull this from an environment variable

const (
	// apiTokenMethodAnnotationName is used to signal what mechanism was used to obtain the API token
	apiTokenMethodAnnotationName = "humio.com/api-token-method"
	// apiTokenMethodFromAPI is used to indicate that the API token was obtained using an API call
	apiTokenMethodFromAPI = "api"
)

var (
	// We override these using ldflags when running "go build"
	commit  = "none"
	date    = "unknown"
	version = "master"
)

// getFileContent returns the content of a file as a string
func getFileContent(filePath string) string {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		fmt.Printf("Got an error while trying to read file %s: %s\n", filePath, err)
		return ""
	}
	return string(data)
}

// createNewAdminUser creates a new Humio admin user
func createNewAdminUser(client *humio.Client) error {
	isRoot := true
	_, err := client.Users().Add(adminAccountUserName, humio.UserChangeSet{
		IsRoot: &isRoot,
	})
	return err
}

// getApiTokenForUserID returns the API token for the given user ID
func getApiTokenForUserID(client *humio.Client, snapShotFile, userID string) (string, string, error) {
	// Try using the API to rotate and get the API token
	token, err := client.Users().RotateUserApiTokenAndGet(userID)
	if err == nil {
		// If API works, return the token
		fmt.Printf("Successfully rotated and extracted API token using the API.\n")
		return token, apiTokenMethodFromAPI, nil
	}

	return "", "", fmt.Errorf("could not find apiToken for userID: %s", userID)
}

type user struct {
	Id       string
	Username string
}

// listAllHumioUsersSingleOrg returns a list of all Humio users when running in single org mode with user ID and username
func listAllHumioUsersSingleOrg(client *humio.Client) ([]user, error) {
	var q struct {
		Users []user `graphql:"users"`
	}
	err := client.Query(&q, nil)
	return q.Users, err
}

type OrganizationSearchResultEntry struct {
	EntityId         string `graphql:"entityId"`
	SearchMatch      string `graphql:"searchMatch"`
	OrganizationName string `graphql:"organizationName"`
}

type OrganizationSearchResultSet struct {
	Results []OrganizationSearchResultEntry `graphql:"results"`
}

// listAllHumioUsersMultiOrg returns a list of all Humio users when running in multi org mode with user ID and username
func listAllHumioUsersMultiOrg(client *humio.Client) ([]OrganizationSearchResultEntry, error) {
	var q struct {
		OrganizationSearchResultSet `graphql:"searchOrganizations(searchFilter: $username, typeFilter: User, sortBy: Name, orderBy: ASC, limit: 1000000, skip: 0)"`
	}

	variables := map[string]interface{}{
		"username": graphql.String(adminAccountUserName),
	}

	err := client.Query(&q, variables)
	if err != nil {
		return []OrganizationSearchResultEntry{}, err
	}

	var allUserResultEntries []OrganizationSearchResultEntry
	for _, result := range q.OrganizationSearchResultSet.Results {
		if result.OrganizationName == "RecoveryRootOrg" {
			allUserResultEntries = append(allUserResultEntries, result)
		}
	}

	return allUserResultEntries, nil
}

// extractExistingHumioAdminUserID finds the user ID of the Humio user for the admin account, and returns
// empty string and no error if the user doesn't exist
func extractExistingHumioAdminUserID(client *humio.Client, organizationMode string) (string, error) {
	if organizationMode == "multi" {
		var allUserResults []OrganizationSearchResultEntry
		allUserResults, err := listAllHumioUsersMultiOrg(client)
		if err != nil {
			// unable to list all users
			return "", err
		}
		for _, userResult := range allUserResults {
			if userResult.OrganizationName == "RecoveryRootOrg" {
				if userResult.SearchMatch == fmt.Sprintf(" | %s () ()", adminAccountUserName) {
					fmt.Printf("Found user ID using multi-organization query.\n")
					return userResult.EntityId, nil
				}
			}
		}
	}

	allUsers, err := listAllHumioUsersSingleOrg(client)
	if err != nil {
		// unable to list all users
		return "", err
	}
	for _, user := range allUsers {
		if user.Username == adminAccountUserName {
			fmt.Printf("Found user ID using single-organization query.\n")
			return user.Id, nil
		}
	}

	return "", nil
}

// createAndGetAdminAccountUserID ensures a Humio admin account exists and returns the user ID for it
func createAndGetAdminAccountUserID(client *humio.Client, organizationMode string) (string, error) {
	// List all users and grab the user ID for an existing user
	userID, err := extractExistingHumioAdminUserID(client, organizationMode)
	if err != nil {
		// Error while grabbing the user ID
		return "", err
	}
	if userID != "" {
		// If we found a user ID, return it
		return userID, nil
	}

	// If we didn't find a user ID, create a user, extract the user ID and return it
	err = createNewAdminUser(client)
	if err != nil {
		return "", err
	}
	userID, err = extractExistingHumioAdminUserID(client, organizationMode)
	if err != nil {
		return "", err
	}
	if userID != "" {
		// If we found a user ID, return it
		return userID, nil
	}

	// Return error if we didn't find a valid user ID
	return "", fmt.Errorf("could not obtain user ID")
}

// validateAdminSecretContent grabs the current token stored in kubernetes and returns nil if it is valid
func validateAdminSecretContent(ctx context.Context, clientset *k8s.Clientset, namespace, clusterName, adminSecretNameSuffix string, humioNodeURL *url.URL) error {
	// Get existing Kubernetes secret
	adminSecretName := fmt.Sprintf("%s-%s", clusterName, adminSecretNameSuffix)
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, adminSecretName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("got err while trying to get existing secret from k8s: %s", err)
	}

	// Check if secret currently holds a valid humio api token
	if adminToken, ok := secret.Data["token"]; ok {
		humioClient := humio.NewClient(humio.Config{
			Address:   humioNodeURL,
			UserAgent: fmt.Sprintf("humio-operator-helper/%s (%s on %s)", version, commit, date),
			Token:     string(adminToken),
		})

		_, err = humioClient.Clusters().Get()
		if err != nil {
			return fmt.Errorf("got err while trying to use apiToken: %s", err)
		}

		// We could successfully get information about the cluster, so the token must be valid
		return nil
	}
	return fmt.Errorf("Unable to validate if kubernetes secret %s holds a valid humio API token", adminSecretName)
}

// ensureAdminSecretContent ensures the target Kubernetes secret contains the desired API token
func ensureAdminSecretContent(ctx context.Context, clientset *k8s.Clientset, namespace, clusterName, adminSecretNameSuffix, desiredAPIToken, methodUsedToObtainToken string) error {
	// Get existing Kubernetes secret
	adminSecretName := fmt.Sprintf("%s-%s", clusterName, adminSecretNameSuffix)
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, adminSecretName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// If the secret doesn't exist, create it
		secret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      adminSecretName,
				Namespace: namespace,
				Labels:    labelsForHumio(clusterName),
				Annotations: map[string]string{
					apiTokenMethodAnnotationName: methodUsedToObtainToken,
				},
			},
			StringData: map[string]string{
				"token": desiredAPIToken,
			},
			Type: corev1.SecretTypeOpaque,
		}
		_, err := clientset.CoreV1().Secrets(namespace).Create(ctx, &secret, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return fmt.Errorf("got err while getting the current k8s secret for apiToken: %s", err)
	}

	// If we got no error, we compare current token with desired token and update if needed.
	if secret.StringData["token"] != desiredAPIToken {
		secret.StringData = map[string]string{"token": desiredAPIToken}
		_, err := clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("got err while updating k8s secret for apiToken: %s", err)
		}
	}

	return nil
}

// labelsForHumio returns the set of common labels for Humio resources.
// NB: There is a copy of this function in pkg/kubernetes/kubernetes.go to work around helper depending on main project.
func labelsForHumio(clusterName string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/instance":   clusterName,
		"app.kubernetes.io/managed-by": "humio-operator",
		"app.kubernetes.io/name":       "humio",
	}
	return labels
}

// fileExists returns true if the specified path exists and is not a directory
func fileExists(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fileInfo.IsDir()
}

func newKubernetesClientset() *k8s.Clientset {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	clientset, err := k8s.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}
	return clientset
}

// authMode creates an admin account in Humio, then extracts the apiToken for the user and saves the token in a
// Kubernetes secret such that the operator can access it
func authMode() {
	adminSecretNameSuffix, found := os.LookupEnv("ADMIN_SECRET_NAME_SUFFIX")
	if !found || adminSecretNameSuffix == "" {
		panic("environment variable ADMIN_SECRET_NAME_SUFFIX not set or empty")
	}

	clusterName, found := os.LookupEnv("CLUSTER_NAME")
	if !found || clusterName == "" {
		panic("environment variable CLUSTER_NAME not set or empty")
	}

	namespace, found := os.LookupEnv("NAMESPACE")
	if !found || namespace == "" {
		panic("environment variable NAMESPACE not set or empty")
	}

	humioNodeURL, found := os.LookupEnv("HUMIO_NODE_URL")
	if !found || humioNodeURL == "" {
		panic("environment variable HUMIO_NODE_URL not set or empty")
	}

	organizationMode, _ := os.LookupEnv("ORGANIZATION_MODE")

	ctx := context.Background()

	go func() {
		// Run separate go routine for readiness/liveness endpoint
		http.HandleFunc("/", httpHandler)
		err := http.ListenAndServe(":8180", nil)
		if err != nil {
			panic("could not bind on :8180")
		}
	}()

	clientset := newKubernetesClientset()

	for {
		// Check required files exist before we continue
		if !fileExists(localAdminTokenFile) || !fileExists(globalSnapshotFile) {
			fmt.Printf("Waiting on the Humio container to create the files %s and %s. Retrying in 5 seconds.\n", localAdminTokenFile, globalSnapshotFile)
			time.Sleep(5 * time.Second)
			continue
		}

		// Get local admin token and create humio client with it
		localAdminToken := getFileContent(localAdminTokenFile)
		if localAdminToken == "" {
			fmt.Printf("Local admin token file is empty. This might be due to Humio not being fully started up yet. Retrying in 5 seconds.\n")
			time.Sleep(5 * time.Second)
			continue
		}

		humioNodeURL, err := url.Parse(humioNodeURL)
		if err != nil {
			fmt.Printf("Unable to parse URL %s: %s\n", humioNodeURL, err)
			time.Sleep(5 * time.Second)
			continue
		}

		err = validateAdminSecretContent(ctx, clientset, namespace, clusterName, adminSecretNameSuffix, humioNodeURL)
		if err == nil {
			fmt.Printf("Existing token is still valid, thus no changes required. Will confirm again in 30 seconds.\n")
			time.Sleep(30 * time.Second)
			continue
		}

		fmt.Printf("Could not validate existing admin secret: %s\n", err)
		fmt.Printf("Continuing to create/update token.\n")

		humioClient := humio.NewClient(humio.Config{
			Address:   humioNodeURL,
			UserAgent: fmt.Sprintf("humio-operator-helper/%s (%s on %s)", version, commit, date),
			Token:     localAdminToken,
		})

		// Get user ID of admin account
		userID, err := createAndGetAdminAccountUserID(humioClient, organizationMode)
		if err != nil {
			fmt.Printf("Got err trying to obtain user ID of admin user: %s\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Get API token for user ID of admin account
		apiToken, methodUsed, err := getApiTokenForUserID(humioClient, globalSnapshotFile, userID)
		if err != nil {
			fmt.Printf("Got err trying to obtain api token of admin user: %s\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Update Kubernetes secret if needed
		err = ensureAdminSecretContent(ctx, clientset, namespace, clusterName, adminSecretNameSuffix, apiToken, methodUsed)
		if err != nil {
			fmt.Printf("Got error ensuring k8s secret contains apiToken: %s\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// All done, wait a bit then run validation again
		fmt.Printf("Successfully created/updated token. Will confirm again in 30 seconds that it is still valid.\n")
		time.Sleep(30 * time.Second)
	}
}

// initMode looks up the availability zone of the Kubernetes node defined in environment variable NODE_NAME and saves
// the result to the file defined in environment variable TARGET_FILE
func initMode() {
	nodeName, found := os.LookupEnv("NODE_NAME")
	if !found || nodeName == "" {
		panic("environment variable NODE_NAME not set or empty")
	}

	targetFile, found := os.LookupEnv("TARGET_FILE")
	if !found || targetFile == "" {
		panic("environment variable TARGET_FILE not set or empty")
	}

	ctx := context.Background()

	clientset := newKubernetesClientset()

	node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	} else {
		zone, found := node.Labels[corev1.LabelZoneFailureDomainStable]
		if !found {
			zone, _ = node.Labels[corev1.LabelZoneFailureDomain]
		}
		err := ioutil.WriteFile(targetFile, []byte(zone), 0644)
		if err != nil {
			panic(fmt.Sprintf("unable to write file with availability zone information: %s", err))
		}
	}
}

// httpHandler simply returns a HTTP 200 with the text OK
func httpHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK")
}

func main() {
	fmt.Printf("Starting humio-operator-helper %s (%s on %s)\n", version, commit, date)
	mode, found := os.LookupEnv("MODE")
	if !found || mode == "" {
		panic("environment variable MODE not set or empty")
	}
	switch mode {
	case "auth":
		authMode()
	case "init":
		initMode()
	default:
		panic("unsupported mode")
	}
}
