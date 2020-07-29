package main

import (
	"fmt"
	humio "github.com/humio/cli/api"
	"github.com/humio/humio-operator/pkg/kubernetes"
	"github.com/savaki/jq"
	"io/ioutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"net/http"
	"os"
	"strings"
	"time"

	// load all auth plugins
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

// perhaps we move these somewhere else?
const localAdminTokenFile = "/data/humio-data/local-admin-token.txt"
const globalSnapshotFile = "/data/humio-data/global-data-snapshot.json"
const adminAccountUserName = "admin" // TODO: Pull this from an environment variable

// getFileContent returns the content of a file as a string
func getFileContent(filePath string) string {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return ""
	}
	return string(data)
}

// createNewAdminUser creates a new Humio admin user
func createNewAdminUser(client *humio.Client) error {
	isRoot := bool(true)
	_, err := client.Users().Add(adminAccountUserName, humio.UserChangeSet{
		IsRoot: &isRoot,
	})
	return err
}

// getApiTokenForUserID returns the API token for the given user ID by extracting it from the global snapshot
func getApiTokenForUserID(snapShotFile, userID string) (string, error) {
	op, err := jq.Parse(fmt.Sprintf(".users.%s.entity.apiToken", userID))
	if err != nil {
		return "", err
	}

	snapShotFileContent := getFileContent(snapShotFile)
	data, _ := op.Apply([]byte(snapShotFileContent))
	apiToken := strings.ReplaceAll(string(data), "\"", "")
	if string(data) != "" {
		return apiToken, nil
	}

	return "", fmt.Errorf("could not find apiToken for userID: %s", userID)
}

type user struct {
	Id       string
	Username string
}

// listAllHumioUsers returns a list of all Humio users with user ID and username
func listAllHumioUsers(client *humio.Client) ([]user, error) {
	var q struct {
		Users []user `graphql:"users"`
	}
	err := client.Query(&q, nil)
	return q.Users, err
}

// extractExistingHumioAdminUserID finds the user ID of the Humio user for the admin account
func extractExistingHumioAdminUserID(client *humio.Client) (string, error) {
	allUsers, err := listAllHumioUsers(client)
	if err != nil {
		// unable to list all users
		return "", err
	}
	userID := ""
	for _, user := range allUsers {
		if user.Username == adminAccountUserName {
			userID = user.Id
		}
	}
	return userID, nil
}

// createAndGetAdminAccountUserID ensures a Humio admin account exists and returns the user ID for it
func createAndGetAdminAccountUserID(client *humio.Client) (string, error) {
	// List all users and grab the user ID for an existing user
	userID, err := extractExistingHumioAdminUserID(client)
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
	userID, err = extractExistingHumioAdminUserID(client)
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

// ensureAdminSecretContent ensures the target Kubernetes secret contains the desired API token
func ensureAdminSecretContent(clientset *k8s.Clientset, namespace, clusterName, adminSecretNameSuffix, desiredAPIToken string) error {
	// Get existing Kubernetes secret
	adminSecretName := fmt.Sprintf("%s-%s", clusterName, adminSecretNameSuffix)
	secret, err := clientset.CoreV1().Secrets(namespace).Get(adminSecretName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		// If the secret doesn't exist, create it
		secret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      adminSecretName,
				Namespace: namespace,
				Labels:    kubernetes.LabelsForHumio(clusterName),
			},
			StringData: map[string]string{
				"token": desiredAPIToken,
			},
			Type: corev1.SecretTypeOpaque,
		}
		_, err := clientset.CoreV1().Secrets(namespace).Create(&secret)
		return err
	} else if err != nil {
		// If we got an error which was not because the secret doesn't exist, return the error
		return err
	}

	// If we got no error, we compare current token with desired token and update if needed.
	if secret.StringData["token"] != desiredAPIToken {
		secret.StringData = map[string]string{"token": desiredAPIToken}
		_, err := clientset.CoreV1().Secrets(namespace).Update(secret)
		if err != nil {
			return err
		}
	}

	return nil
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
			fmt.Printf("waiting on files %s, %s\n", localAdminTokenFile, globalSnapshotFile)
			time.Sleep(5 * time.Second)
			continue
		}

		// Get local admin token and create humio client with it
		localAdminToken := getFileContent(localAdminTokenFile)
		if localAdminToken == "" {
			fmt.Printf("local admin token file is empty\n")
			time.Sleep(5 * time.Second)
			continue
		}

		humioClient, err := humio.NewClient(humio.Config{
			Address: humioNodeURL,
			Token:   localAdminToken,
		})
		if err != nil {
			fmt.Printf("got err trying to create humio client: %s\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Get user ID of admin account
		userID, err := createAndGetAdminAccountUserID(humioClient)
		if err != nil {
			fmt.Printf("got err trying to obtain user ID of admin user: %s\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Get API token for user ID of admin account
		apiToken, err := getApiTokenForUserID(globalSnapshotFile, userID)
		if err != nil {
			fmt.Printf("got err trying to obtain api token of admin user: %s\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// Update Kubernetes secret if needed
		err = ensureAdminSecretContent(clientset, namespace, clusterName, adminSecretNameSuffix, apiToken)
		if err != nil {
			fmt.Printf("got error ensuring k8s secret contains apiToken: %s\n", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// All done, wait a bit then run validation again
		fmt.Printf("validated token. waiting 30 seconds\n")
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

	clientset := newKubernetesClientset()

	node, err := clientset.CoreV1().Nodes().Get(nodeName, metav1.GetOptions{})
	if err != nil {
		panic(err.Error())
	} else {
		zone := node.Labels[corev1.LabelZoneFailureDomain]
		err := ioutil.WriteFile(targetFile, []byte(zone), 0644)
		if err != nil {
			panic("unable to write file with availability zone information")
		}
	}
}

// httpHandler simply returns a HTTP 200 with the text OK
func httpHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "OK")
}

func main() {
	fmt.Printf("Starting humio-operator-helper version %s\n", Version)
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
