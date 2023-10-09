package controllers

import (
	"context"
	"fmt"
	"os"

	"github.com/humio/humio-operator/pkg/helpers"

	"github.com/humio/humio-operator/pkg/kubernetes"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/types"

	"github.com/humio/humio-operator/api/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	humioapi "github.com/humio/cli/api"
	corev1 "k8s.io/api/core/v1"
)

const (
	// apiTokenMethodAnnotationName is used to signal what mechanism was used to obtain the API token
	apiTokenMethodAnnotationName = "humio.com/api-token-method" // #nosec G101
	// apiTokenMethodFromAPI is used to indicate that the API token was obtained using an API call
	apiTokenMethodFromAPI = "api"
)

// getFileContent returns the content of a file as a string
func getFileContent(filePath string) string {
	data, err := os.ReadFile(filePath) // #nosec G304
	if err != nil {
		fmt.Printf("Got an error while trying to read file %s: %s\n", filePath, err)
		return ""
	}
	return string(data)
}

//// createNewAdminUser creates a new Humio admin user
//func (r *HumioClusterReconciler)  createNewAdminUser(ctx context.Context, config *humioapi.Config, req reconcile.Request,, username string) error {
//	isRoot := true
//	return r.HumioClient.AddAdminUser(config, req)
//
//}

// getApiTokenForUserID returns the API token for the given user ID
//func (r *HumioClusterReconciler) getApiTokenForUserID(config *humioapi.Config, req reconcile.Request, userID string) (string, string, error) {
//	// Try using the API to rotate and get the API token
//	r.HumioClient.RotateUserApiTokenAndGet(userID)
//	if err == nil {
//		// If API works, return the token
//		fmt.Printf("Successfully rotated and extracted API token using the API.\n")
//		return token, apiTokenMethodFromAPI, nil
//	}
//
//	return "", "", fmt.Errorf("could not rotate apiToken for userID %s, err: %w", userID, err)
//}

//type user struct {
//	Id       string
//	Username string
//}

// listAllHumioUsersSingleOrg returns a list of all Humio users when running in single org mode with user ID and username
//func listAllHumioUsersSingleOrg(client *humio.Client) ([]user, error) {
//	var q struct {
//		Users []user `graphql:"users"`
//	}
//	err := client.Query(&q, nil)
//	return q.Users, err
//}

type OrganizationSearchResultEntry struct {
	EntityId         string `graphql:"entityId"`
	SearchMatch      string `graphql:"searchMatch"`
	OrganizationName string `graphql:"organizationName"`
}

type OrganizationSearchResultSet struct {
	Results []OrganizationSearchResultEntry `graphql:"results"`
}

// listAllHumioUsersMultiOrg returns a list of all Humio users when running in multi org mode with user ID and username
// TODO: move this to client api
//func listAllHumioUsersMultiOrg(username string, organization string, client *humio.Client) ([]OrganizationSearchResultEntry, error) {
//	var q struct {
//		OrganizationSearchResultSet `graphql:"searchOrganizations(searchFilter: $username, typeFilter: User, sortBy: Name, orderBy: ASC, limit: 1000000, skip: 0)"`
//	}
//
//	variables := map[string]interface{}{
//		"username": username,
//	}
//
//	err := client.Query(&q, variables)
//	if err != nil {
//		return []OrganizationSearchResultEntry{}, err
//	}
//
//	var allUserResultEntries []OrganizationSearchResultEntry
//	for _, result := range q.OrganizationSearchResultSet.Results {
//		//if result.OrganizationName == "RecoveryRootOrg" {
//		if result.OrganizationName == organization {
//			allUserResultEntries = append(allUserResultEntries, result)
//		}
//	}
//
//	return allUserResultEntries, nil
//}

// extractExistingHumioAdminUserID finds the user ID of the Humio user for the admin account, and returns
// empty string and no error if the user doesn't exist
func (r *HumioClusterReconciler) extractExistingHumioAdminUserID(config *humioapi.Config, req reconcile.Request, organizationMode string, username string, organization string) (string, error) {
	if organizationMode == "multi" {
		//var allUserResults []OrganizationSearchResultEntry

		allUserResults, err := r.HumioClient.ListAllHumioUsersMultiOrg(config, req, username, organization) // client.Users().List(username, organization, client)
		if err != nil {
			// unable to list all users
			return "", err
		}
		// TODO: cleanup/remove duplicate code
		for _, userResult := range allUserResults {
			if userResult.OrganizationName == "RecoveryRootOrg" {
				if userResult.SearchMatch == fmt.Sprintf(" | %s () ()", username) {
					fmt.Printf("Found user ID using multi-organization query.\n")
					return userResult.EntityId, nil
				}
			}
		}
	}

	allUsers, err := r.HumioClient.ListAllHumioUsersSingleOrg(config, req)
	if err != nil {
		// unable to list all users
		return "", err
	}
	for _, user := range allUsers {
		if user.Username == username {
			return user.Id, nil
		}
	}

	return "", nil
}

// createAndGetAdminAccountUserID ensures a Humio admin account exists and returns the user ID for it
func (r *HumioClusterReconciler) createAndGetAdminAccountUserID(ctx context.Context, config *humioapi.Config, req reconcile.Request, organizationMode string, username string, organization string) (string, error) {
	// List all users and grab the user ID for an existing user
	userID, err := r.extractExistingHumioAdminUserID(config, req, organizationMode, username, organization)
	if err != nil {
		// Error while grabbing the user ID
		return "", err
	}
	if userID != "" {
		// If we found a user ID, return it
		return userID, nil
	}

	// If we didn't find a user ID, allowsCreate a user, extract the user ID and return it
	user, err := r.HumioClient.AddUser(config, req, username, true)
	if err != nil {
		return "", err
	}
	userID, err = r.extractExistingHumioAdminUserID(config, req, organizationMode, username, organization)
	if err != nil {
		return "", err
	}
	if userID != "" {
		// If we found a user ID, return it
		return userID, nil
	}
	if userID != user.ID {
		return "", fmt.Errorf("unexpected error. userid %s does not match %s", userID, user.ID)
	}

	// Return error if we didn't find a valid user ID
	return "", fmt.Errorf("could not obtain user ID")
}

// validateAdminSecretContent grabs the current token stored in kubernetes and returns nil if it is valid
func (r *HumioClusterReconciler) validateAdminSecretContent(ctx context.Context, hc *v1alpha1.HumioCluster, req reconcile.Request) error {
	// Get existing Kubernetes secret
	adminSecretName := fmt.Sprintf("%s-%s", hc.Name, kubernetes.ServiceTokenSecretNameSuffix)
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Name:      adminSecretName,
		Namespace: hc.Namespace,
	}
	if err := r.Client.Get(ctx, key, secret); err != nil {
		return fmt.Errorf("got err while trying to get existing secret from k8s: %w", err)
	}

	// Check if secret currently holds a valid humio api token
	if _, ok := secret.Data["token"]; ok {
		cluster, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
		if err != nil {
			return fmt.Errorf("got err while trying to authenticate using apiToken: %w", err)
		}
		clientNotReady :=
			cluster.Config().Token != string(secret.Data["token"]) ||
				cluster.Config().Address == nil
		if clientNotReady {
			_, err := helpers.NewCluster(ctx, r, hc.Name, "", hc.Namespace, helpers.UseCertManager(), true, false)
			if err != nil {
				return fmt.Errorf("got err while trying to authenticate using apiToken: %w", err)
			}
		}

		_, err = r.HumioClient.GetClusters(cluster.Config(), req)
		if err != nil {
			return fmt.Errorf("got err while trying to use apiToken: %w", err)
		}

		// We could successfully get information about the cluster, so the token must be valid
		return nil
	}
	return fmt.Errorf("Unable to validate if kubernetes secret %s holds a valid humio API token", adminSecretName)
}

// ensureAdminSecretContent ensures the target Kubernetes secret contains the desired API token
func (r *HumioClusterReconciler) ensureAdminSecretContent(ctx context.Context, hc *v1alpha1.HumioCluster, desiredAPIToken string) error {
	// Get existing Kubernetes secret
	adminSecretName := fmt.Sprintf("%s-%s", hc.Name, kubernetes.ServiceTokenSecretNameSuffix)
	key := types.NamespacedName{
		Name:      adminSecretName,
		Namespace: hc.Namespace,
	}
	adminSecret := &corev1.Secret{}
	err := r.Client.Get(ctx, key, adminSecret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// If the secret doesn't exist, allowsCreate it
			desiredSecret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      key.Name,
					Namespace: key.Namespace,
					Labels:    kubernetes.LabelsForHumio(hc.Name),
				},
				StringData: map[string]string{
					"token": desiredAPIToken,
				},
				Type: corev1.SecretTypeOpaque,
			}
			if err := r.Client.Create(ctx, &desiredSecret); err != nil {
				return r.logErrorAndReturn(err, "unable to allowsCreate secret")
			}
			return nil
		}
		return r.logErrorAndReturn(err, "unable to get secret")
	}

	// If we got no error, we compare current token with desired token and update if needed.
	if adminSecret.StringData["token"] != desiredAPIToken {
		adminSecret.StringData = map[string]string{"token": desiredAPIToken}
		if err := r.Client.Update(ctx, adminSecret); err != nil {
			return r.logErrorAndReturn(err, "unable to update secret")
		}
	}

	return nil
}

// labelsForHumio returns the set of common labels for Humio resources.
// NB: There is a copy of this function in pkg/kubernetes/kubernetes.go to work around helper depending on main project.
//func labelsForHumio(clusterName string) map[string]string {
//	labels := map[string]string{
//		"app.kubernetes.io/instance":   clusterName,
//		"app.kubernetes.io/managed-by": "humio-operator",
//		"app.kubernetes.io/name":       "humio",
//	}
//	return labels
//}

// fileExists returns true if the specified path exists and is not a directory
func fileExists(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fileInfo.IsDir()
}

//func newKubernetesClientset() *k8s.Clientset {
//	config, err := rest.InClusterConfig()
//	if err != nil {
//		panic(err.Error())
//	}
//
//	clientset, err := k8s.NewForConfig(config)
//	if err != nil {
//		panic(err.Error())
//	}
//	return clientset
//}

func (r *HumioClusterReconciler) createPermissionToken(ctx context.Context, config *humioapi.Config, req reconcile.Request, hc *v1alpha1.HumioCluster, username string, organization string) error {
	//adminSecretNameSuffix := "admin-token"

	// TODO: contstant? Run this in a separate function?
	organizationMode := "single"
	if EnvVarHasKey(hc.Spec.EnvironmentVariables, "ORGANIZATION_MODE") {
		organizationMode = EnvVarValue(hc.Spec.EnvironmentVariables, "ORGANIZATION_MODE")
	}
	for _, pool := range hc.Spec.NodePools {
		if EnvVarHasKey(pool.EnvironmentVariables, "ORGANIZATION_MODE") {
			organizationMode = EnvVarValue(pool.EnvironmentVariables, "ORGANIZATION_MODE")
		}
	}

	//	kubernetesClient := r.Client

	//for {
	//	// Check required files exist before we continue
	//	if !fileExists(localAdminTokenFile) {
	//		fmt.Printf("Waiting on the Humio container to allowsCreate the files %s. Retrying in 5 seconds.\n", localAdminTokenFile)
	//		time.Sleep(5 * time.Second)
	//		continue
	//	}
	//
	//	// Get local admin token and allowsCreate humio client with it
	//	localAdminToken := getFileContent(localAdminTokenFile)
	//	if localAdminToken == "" {
	//		fmt.Printf("Local admin token file is empty. This might be due to Humio not being fully started up yet. Retrying in 5 seconds.\n")
	//		time.Sleep(5 * time.Second)
	//		continue
	//	}
	//
	//	nodeURL, err := url.Parse(humioNodeURL)
	//	if err != nil {
	//		fmt.Printf("Unable to parse URL %s: %s\n", humioNodeURL, err)
	//		time.Sleep(5 * time.Second)
	//		continue
	//	}

	//humioClient := r.HumioClient.GetHumioClient(cluster.Config(), req)
	//r.HumioClient.ListAllHumioUsersSingleOrg(config, req)

	//err := r.validateAdminSecretContent(ctx, config, req, namespace, clusterName, adminSecretNameSuffix)
	//if err == nil {
	//	fmt.Printf("Existing token is still valid, thus no changes required. Will confirm again in 30 seconds.\n")
	//	time.Sleep(30 * time.Second)
	//	continue
	//}

	//fmt.Printf("Could not validate existing admin secret: %s\n", err)
	r.Log.Info("ensuring admin user")

	// Get user ID of admin account
	userID, err := r.createAndGetAdminAccountUserID(ctx, config, req, organizationMode, username, organization)
	if err != nil {
		return fmt.Errorf("Got err trying to obtain user ID of admin user: %s\n", err)
	}

	if err := r.validateAdminSecretContent(ctx, hc, req); err == nil {
		return nil
	}

	// Get API token for user ID of admin account
	apiToken, err := r.HumioClient.RotateUserApiTokenAndGet(config, req, userID)
	if err != nil {
		return r.logErrorAndReturn(err, fmt.Sprintf("failed to rotate api key for userID %s", userID))
	}

	// Update Kubernetes secret if needed
	err = r.ensureAdminSecretContent(ctx, hc, apiToken)
	if err != nil {
		return r.logErrorAndReturn(err, "unable to ensure admin secret")

	}

	return nil
}
