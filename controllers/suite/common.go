package suite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	ginkgotypes "github.com/onsi/ginkgo/v2/types"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/controllers"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/humio"
	"github.com/humio/humio-operator/pkg/kubernetes"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	//lint:ignore ST1001 we use dot import for ginkgo as per their official instructions
	. "github.com/onsi/ginkgo/v2"

	//lint:ignore ST1001 we use dot import for gomega as per their official instructions
	. "github.com/onsi/gomega"
)

const (
	// apiTokenMethodAnnotationName is used to signal what mechanism was used to obtain the API token
	apiTokenMethodAnnotationName = "humio.com/api-token-method" // #nosec G101
	// apiTokenMethodFromAPI is used to indicate that the API token was obtained using an API call
	apiTokenMethodFromAPI = "api"
	// dockerUsernameEnvVar is used to login to docker when pulling images
	dockerUsernameEnvVar = "DOCKER_USERNAME"
	// dockerPasswordEnvVar is used to login to docker when pulling images
	dockerPasswordEnvVar = "DOCKER_PASSWORD"
	// DockerRegistryCredentialsSecretName is the name of the k8s secret containing the registry credentials
	DockerRegistryCredentialsSecretName = "regcred"
)

const TestInterval = time.Second * 1

func UsingClusterBy(cluster, text string, callbacks ...func()) {
	timestamp := time.Now().Format(time.RFC3339Nano)
	fmt.Fprintln(GinkgoWriter, "STEP | "+timestamp+" | "+cluster+": "+text)
	if len(callbacks) == 1 {
		callbacks[0]()
	}
	if len(callbacks) > 1 {
		panic("just one callback per By, please")
	}
}

func MarkPodsAsRunning(ctx context.Context, client client.Client, pods []corev1.Pod, clusterName string) error {
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		return nil
	}

	UsingClusterBy(clusterName, "Simulating Humio container starts up and is marked Ready")
	for nodeID, pod := range pods {
		err := MarkPodAsRunning(ctx, client, nodeID, pod, clusterName)
		if err != nil {
			return err
		}
	}
	return nil
}

func MarkPodAsRunning(ctx context.Context, client client.Client, nodeID int, pod corev1.Pod, clusterName string) error {
	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		return nil
	}

	UsingClusterBy(clusterName, fmt.Sprintf("Simulating Humio container starts up and is marked Ready (node %d, pod phase %s)", nodeID, pod.Status.Phase))
	pod.Status.PodIP = fmt.Sprintf("192.168.0.%d", nodeID)
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		},
	}
	pod.Status.Phase = corev1.PodRunning
	return client.Status().Update(ctx, &pod)
}

func CleanupCluster(ctx context.Context, k8sClient client.Client, hc *humiov1alpha1.HumioCluster) {
	var cluster humiov1alpha1.HumioCluster
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: hc.Name, Namespace: hc.Namespace}, &cluster)).To(Succeed())
	UsingClusterBy(cluster.Name, "Cleaning up any user-defined service account we've created")
	if cluster.Spec.HumioServiceAccountName != "" {
		serviceAccount, err := kubernetes.GetServiceAccount(ctx, k8sClient, cluster.Spec.HumioServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, serviceAccount)).To(Succeed())
		}
	}
	if cluster.Spec.InitServiceAccountName != "" {
		clusterRoleBinding, err := kubernetes.GetClusterRoleBinding(ctx, k8sClient, cluster.Spec.InitServiceAccountName)
		if err == nil {
			Expect(k8sClient.Delete(ctx, clusterRoleBinding)).To(Succeed())
		}

		clusterRole, err := kubernetes.GetClusterRole(ctx, k8sClient, cluster.Spec.InitServiceAccountName)
		if err == nil {
			Expect(k8sClient.Delete(ctx, clusterRole)).To(Succeed())
		}

		serviceAccount, err := kubernetes.GetServiceAccount(ctx, k8sClient, cluster.Spec.InitServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, serviceAccount)).To(Succeed())
		}
	}
	if cluster.Spec.AuthServiceAccountName != "" {
		roleBinding, err := kubernetes.GetRoleBinding(ctx, k8sClient, cluster.Spec.AuthServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, roleBinding)).To(Succeed())
		}

		role, err := kubernetes.GetRole(ctx, k8sClient, cluster.Spec.AuthServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, role)).To(Succeed())
		}

		serviceAccount, err := kubernetes.GetServiceAccount(ctx, k8sClient, cluster.Spec.AuthServiceAccountName, cluster.Namespace)
		if err == nil {
			Expect(k8sClient.Delete(ctx, serviceAccount)).To(Succeed())
		}
	}

	UsingClusterBy(cluster.Name, "Cleaning up any secrets for the cluster")
	var allSecrets corev1.SecretList
	Expect(k8sClient.List(ctx, &allSecrets)).To(Succeed())
	for idx, secret := range allSecrets.Items {
		if secret.Type == corev1.SecretTypeServiceAccountToken {
			// Secrets holding service account tokens are automatically GC'ed when the ServiceAccount goes away.
			continue
		}
		// Only consider secrets not already being marked for deletion
		if secret.DeletionTimestamp == nil {
			if secret.Name == cluster.Name ||
				secret.Name == fmt.Sprintf("%s-admin-token", cluster.Name) ||
				strings.HasPrefix(secret.Name, fmt.Sprintf("%s-core-", cluster.Name)) {
				// This includes the following objects which do not have an ownerReference pointing to the HumioCluster, so they will not automatically be cleaned up:
				// - <CLUSTER_NAME>: Holds the CA bundle for the TLS certificates, created by cert-manager because of a Certificate object and uses secret type kubernetes.io/tls.
				// - <CLUSTER_NAME>-admin-token: Holds the API token for the Humio API, created by the auth sidecar and uses secret type "Opaque".
				// - <CLUSTER_NAME>-core-XXXXXX: Holds the node-specific TLS certificate in a JKS bundle, created by cert-manager because of a Certificate object and uses secret type kubernetes.io/tls.

				UsingClusterBy(cluster.Name, fmt.Sprintf("Cleaning up secret %s", secret.Name))
				_ = k8sClient.Delete(ctx, &allSecrets.Items[idx])
			}
		}
	}

	UsingClusterBy(cluster.Name, "Deleting the cluster")
	Expect(k8sClient.Delete(ctx, &cluster)).To(Succeed())

	if cluster.Spec.License.SecretKeyRef != nil {
		UsingClusterBy(cluster.Name, fmt.Sprintf("Deleting the license secret %s", cluster.Spec.License.SecretKeyRef.Name))
		_ = k8sClient.Delete(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cluster.Spec.License.SecretKeyRef.Name,
				Namespace: cluster.Namespace,
			},
		})
	}
}

func ConstructBasicNodeSpecForHumioCluster(key types.NamespacedName) humiov1alpha1.HumioNodeSpec {
	storageClassNameStandard := "standard"
	nodeSpec := humiov1alpha1.HumioNodeSpec{
		Image:             controllers.Image,
		ExtraKafkaConfigs: "security.protocol=PLAINTEXT",
		NodeCount:         helpers.IntPtr(1),
		EnvironmentVariables: []corev1.EnvVar{
			{
				Name:  "ZOOKEEPER_URL",
				Value: "humio-cp-zookeeper-0.humio-cp-zookeeper-headless.default:2181",
			},
			{
				Name:  "KAFKA_SERVERS",
				Value: "humio-cp-kafka-0.humio-cp-kafka-headless.default:9092",
			},
			{
				Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
				Value: key.Name,
			},
			{
				Name:  "AUTHENTICATION_METHOD",
				Value: "single-user",
			},
			{
				Name:  "SINGLE_USER_PASSWORD",
				Value: "password",
			},
			{
				Name:  "ENABLE_IOC_SERVICE",
				Value: "false",
			},
			{
				Name:  "HUMIO_MEMORY_OPTS",
				Value: "-Xss2m -Xms1g -Xmx2g -XX:MaxDirectMemorySize=1g",
			},
		},
		DataVolumePersistentVolumeClaimSpecTemplate: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: *resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				},
			},
			StorageClassName: &storageClassNameStandard,
		},
	}

	if useDockerCredentials() {
		nodeSpec.ImagePullSecrets = []corev1.LocalObjectReference{
			{Name: DockerRegistryCredentialsSecretName},
		}
	}
	return nodeSpec
}

func ConstructBasicSingleNodeHumioCluster(key types.NamespacedName, useAutoCreatedLicense bool) *humiov1alpha1.HumioCluster {
	humioCluster := &humiov1alpha1.HumioCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
		},
		Spec: humiov1alpha1.HumioClusterSpec{
			TargetReplicationFactor: 1,
			HumioNodeSpec:           ConstructBasicNodeSpecForHumioCluster(key),
		},
	}

	humioVersion, _ := controllers.HumioVersionFromString(controllers.NewHumioNodeManagerFromHumioCluster(humioCluster).GetImage())
	if ok, _ := humioVersion.AtLeast(controllers.HumioVersionWithLauncherScript); ok {
		humioCluster.Spec.EnvironmentVariables = append(humioCluster.Spec.EnvironmentVariables, corev1.EnvVar{
			Name:  "HUMIO_GC_OPTS",
			Value: "-XX:+UseParallelGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC",
		})
		humioCluster.Spec.EnvironmentVariables = append(humioCluster.Spec.EnvironmentVariables, corev1.EnvVar{
			Name:  "HUMIO_JVM_LOG_OPTS",
			Value: "-Xlog:gc+jni=debug:stdout -Xlog:gc*:stdout:time,tags",
		})
		humioCluster.Spec.EnvironmentVariables = append(humioCluster.Spec.EnvironmentVariables, corev1.EnvVar{
			Name:  "HUMIO_OPTS",
			Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
		})
	} else {
		humioCluster.Spec.EnvironmentVariables = append(humioCluster.Spec.EnvironmentVariables, corev1.EnvVar{
			Name:  "HUMIO_JVM_ARGS",
			Value: "-Xss2m -Xms256m -Xmx2g -server -XX:+UseParallelGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
		})
	}

	if useAutoCreatedLicense {
		humioCluster.Spec.License = humiov1alpha1.HumioClusterLicenseSpec{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-license", key.Name),
				},
				Key: "license",
			},
		}
	}
	return humioCluster
}

func CreateAndBootstrapCluster(ctx context.Context, k8sClient client.Client, humioClient humio.Client, cluster *humiov1alpha1.HumioCluster, autoCreateLicense bool, expectedState string, testTimeout time.Duration) {
	key := types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      cluster.Name,
	}

	if autoCreateLicense {
		UsingClusterBy(cluster.Name, fmt.Sprintf("Creating the license secret %s", cluster.Spec.License.SecretKeyRef.Name))

		licenseSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-license", key.Name),
				Namespace: key.Namespace,
			},
			StringData: map[string]string{"license": os.Getenv("HUMIO_E2E_LICENSE")},
			Type:       corev1.SecretTypeOpaque,
		}
		Expect(k8sClient.Create(ctx, &licenseSecret)).To(Succeed())
	}

	if cluster.Spec.HumioServiceAccountName != "" {
		UsingClusterBy(key.Name, "Creating service account for humio container")
		humioServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.HumioServiceAccountName, cluster.Namespace, map[string]string{}, map[string]string{})
		Expect(k8sClient.Create(ctx, humioServiceAccount)).To(Succeed())
	}

	if !cluster.Spec.DisableInitContainer {
		if cluster.Spec.InitServiceAccountName != "" {
			if cluster.Spec.InitServiceAccountName != cluster.Spec.HumioServiceAccountName {
				UsingClusterBy(key.Name, "Creating service account for init container")
				initServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.InitServiceAccountName, cluster.Namespace, map[string]string{}, map[string]string{})
				Expect(k8sClient.Create(ctx, initServiceAccount)).To(Succeed())
			}

			UsingClusterBy(key.Name, "Creating cluster role for init container")
			initClusterRole := kubernetes.ConstructInitClusterRole(cluster.Spec.InitServiceAccountName, map[string]string{})
			Expect(k8sClient.Create(ctx, initClusterRole)).To(Succeed())

			UsingClusterBy(key.Name, "Creating cluster role binding for init container")
			initClusterRoleBinding := kubernetes.ConstructClusterRoleBinding(cluster.Spec.InitServiceAccountName, initClusterRole.Name, key.Namespace, cluster.Spec.InitServiceAccountName, map[string]string{})
			Expect(k8sClient.Create(ctx, initClusterRoleBinding)).To(Succeed())
		}
	}

	if cluster.Spec.AuthServiceAccountName != "" {
		if cluster.Spec.AuthServiceAccountName != cluster.Spec.HumioServiceAccountName {
			UsingClusterBy(key.Name, "Creating service account for auth container")
			authServiceAccount := kubernetes.ConstructServiceAccount(cluster.Spec.AuthServiceAccountName, cluster.Namespace, map[string]string{}, map[string]string{})
			Expect(k8sClient.Create(ctx, authServiceAccount)).To(Succeed())
		}

		UsingClusterBy(key.Name, "Creating role for auth container")
		authRole := kubernetes.ConstructAuthRole(cluster.Spec.AuthServiceAccountName, key.Namespace, map[string]string{})
		Expect(k8sClient.Create(ctx, authRole)).To(Succeed())

		UsingClusterBy(key.Name, "Creating role binding for auth container")
		authRoleBinding := kubernetes.ConstructRoleBinding(cluster.Spec.AuthServiceAccountName, authRole.Name, key.Namespace, cluster.Spec.AuthServiceAccountName, map[string]string{})
		Expect(k8sClient.Create(ctx, authRoleBinding)).To(Succeed())
	}

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") != "true" {
		// Simulate sidecar creating the secret which contains the admin token used to authenticate with humio
		secretData := map[string][]byte{"token": []byte("")}
		adminTokenSecretName := fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix)
		UsingClusterBy(key.Name, "Simulating the auth container creating the secret containing the API token")
		desiredSecret := kubernetes.ConstructSecret(key.Name, key.Namespace, adminTokenSecretName, secretData, nil)
		Expect(k8sClient.Create(ctx, desiredSecret)).To(Succeed())
	}

	UsingClusterBy(key.Name, "Creating HumioCluster resource")
	Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())

	if expectedState != humiov1alpha1.HumioClusterStateRunning {
		return
	}

	UsingClusterBy(key.Name, "Confirming cluster enters running state")
	var updatedHumioCluster humiov1alpha1.HumioCluster
	Eventually(func() string {
		err := k8sClient.Get(ctx, key, &updatedHumioCluster)
		if err != nil && !k8serrors.IsNotFound(err) {
			Expect(err).Should(Succeed())
		}
		return updatedHumioCluster.Status.State
	}, testTimeout, TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))

	UsingClusterBy(key.Name, "Waiting to have the correct number of pods")

	Eventually(func() []corev1.Pod {
		var clusterPods []corev1.Pod
		clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
		_ = MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)
		return clusterPods
	}, testTimeout, TestInterval).Should(HaveLen(*cluster.Spec.NodeCount))

	for idx, pool := range cluster.Spec.NodePools {
		Eventually(func() []corev1.Pod {
			var clusterPods []corev1.Pod
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
			_ = MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)
			return clusterPods
		}, testTimeout, TestInterval).Should(HaveLen(*pool.NodeCount))
	}

	clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
	humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], controllers.HumioContainerName)
	Expect(err).ToNot(HaveOccurred())
	humioContainerArgs := strings.Join(clusterPods[0].Spec.Containers[humioIdx].Args, " ")
	if cluster.Spec.DisableInitContainer {
		UsingClusterBy(key.Name, "Confirming pods do not use init container")
		Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(0))
		Expect(humioContainerArgs).ToNot(ContainSubstring("export ZONE="))
	} else {
		UsingClusterBy(key.Name, "Confirming pods have an init container")
		Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(1))
		Expect(humioContainerArgs).To(ContainSubstring("export ZONE="))
	}

	for idx := range cluster.Spec.NodePools {
		clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
		humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], controllers.HumioContainerName)
		Expect(err).ToNot(HaveOccurred())
		humioContainerArgs := strings.Join(clusterPods[0].Spec.Containers[humioIdx].Args, " ")
		if cluster.Spec.DisableInitContainer {
			UsingClusterBy(key.Name, "Confirming pods do not use init container")
			Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(0))
			Expect(humioContainerArgs).ToNot(ContainSubstring("export ZONE="))
		} else {
			UsingClusterBy(key.Name, "Confirming pods have an init container")
			Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(1))
			Expect(humioContainerArgs).To(ContainSubstring("export ZONE="))
		}
	}

	UsingClusterBy(key.Name, "Confirming cluster enters running state")
	Eventually(func() string {
		clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
		_ = MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)

		for idx := range cluster.Spec.NodePools {
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
			_ = MarkPodsAsRunning(ctx, k8sClient, clusterPods, key.Name)
		}

		updatedHumioCluster = humiov1alpha1.HumioCluster{}
		Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
		return updatedHumioCluster.Status.State
	}, testTimeout, TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

	UsingClusterBy(key.Name, "Validating cluster has expected pod revision annotation")
	revisionKey, _ := controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetHumioClusterNodePoolRevisionAnnotation()
	Eventually(func() map[string]string {
		updatedHumioCluster = humiov1alpha1.HumioCluster{}
		Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
		return updatedHumioCluster.Annotations
	}, testTimeout, TestInterval).Should(HaveKeyWithValue(revisionKey, "1"))

	UsingClusterBy(key.Name, "Waiting for the auth sidecar to populate the secret containing the API token")
	Eventually(func() error {
		clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
		for idx := range clusterPods {
			UsingClusterBy(key.Name, fmt.Sprintf("Pod status %s status: %v", clusterPods[idx].Name, clusterPods[idx].Status))
		}

		return k8sClient.Get(ctx, types.NamespacedName{
			Namespace: key.Namespace,
			Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix),
		}, &corev1.Secret{})
	}, testTimeout, TestInterval).Should(Succeed())

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		UsingClusterBy(key.Name, "Validating API token was obtained using the API method")
		var apiTokenSecret corev1.Secret
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Namespace: key.Namespace,
				Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix),
			}, &apiTokenSecret)
		}, testTimeout, TestInterval).Should(Succeed())
		Expect(apiTokenSecret.Annotations).Should(HaveKeyWithValue(apiTokenMethodAnnotationName, apiTokenMethodFromAPI))
	}

	if os.Getenv("TEST_USE_EXISTING_CLUSTER") == "true" {
		UsingClusterBy(key.Name, "Validating cluster nodes have ZONE configured correctly")
		if updatedHumioCluster.Spec.DisableInitContainer {
			Eventually(func() []string {
				clusterConfig, err := helpers.NewCluster(ctx, k8sClient, key.Name, "", key.Namespace, helpers.UseCertManager(), true)
				Expect(err).To(BeNil())
				Expect(clusterConfig).ToNot(BeNil())
				Expect(clusterConfig.Config()).ToNot(BeNil())

				cluster, err := humioClient.GetClusters(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
				UsingClusterBy(key.Name, fmt.Sprintf("Obtained the following cluster details: %#+v, err: %v", cluster, err))
				if err != nil {
					return []string{fmt.Sprintf("got err: %s", err)}
				}
				if len(cluster.Nodes) < 1 {
					return []string{}
				}
				keys := make(map[string]bool)
				var zoneList []string
				for _, node := range cluster.Nodes {
					if _, value := keys[node.Zone]; !value {
						if node.Zone != "" {
							keys[node.Zone] = true
							zoneList = append(zoneList, node.Zone)
						}
					}
				}
				return zoneList
			}, testTimeout, TestInterval).Should(BeEmpty())
		} else {
			Eventually(func() []string {
				clusterConfig, err := helpers.NewCluster(ctx, k8sClient, key.Name, "", key.Namespace, helpers.UseCertManager(), true)
				Expect(err).To(BeNil())
				Expect(clusterConfig).ToNot(BeNil())
				Expect(clusterConfig.Config()).ToNot(BeNil())

				cluster, err := humioClient.GetClusters(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
				UsingClusterBy(key.Name, fmt.Sprintf("Obtained the following cluster details: %#+v, err: %v", cluster, err))
				if err != nil || len(cluster.Nodes) < 1 {
					return []string{}
				}
				keys := make(map[string]bool)
				var zoneList []string
				for _, node := range cluster.Nodes {
					if _, value := keys[node.Zone]; !value {
						if node.Zone != "" {
							keys[node.Zone] = true
							zoneList = append(zoneList, node.Zone)
						}
					}
				}
				return zoneList
			}, testTimeout, TestInterval).ShouldNot(BeEmpty())
		}
	}

	UsingClusterBy(key.Name, "Confirming replication factor environment variables are set correctly")
	for _, pod := range clusterPods {
		humioIdx, err = kubernetes.GetContainerIndexByName(pod, "humio")
		Expect(err).ToNot(HaveOccurred())
		Expect(pod.Spec.Containers[humioIdx].Env).To(ContainElements([]corev1.EnvVar{
			{
				Name:  "DIGEST_REPLICATION_FACTOR",
				Value: strconv.Itoa(cluster.Spec.TargetReplicationFactor),
			},
			{
				Name:  "STORAGE_REPLICATION_FACTOR",
				Value: strconv.Itoa(cluster.Spec.TargetReplicationFactor),
			},
		}))
	}

	Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
	IncrementGenerationAndWaitForReconcileToSync(ctx, key, k8sClient, testTimeout)
}

func IncrementGenerationAndWaitForReconcileToSync(ctx context.Context, key types.NamespacedName, k8sClient client.Client, testTimeout time.Duration) {
	UsingClusterBy(key.Name, "Incrementing HumioCluster Generation")

	// Force an update the status field to trigger a new resource generation
	var humioClusterBeforeUpdate humiov1alpha1.HumioCluster
	Eventually(func() error {
		Expect(k8sClient.Get(ctx, key, &humioClusterBeforeUpdate)).Should(Succeed())
		humioClusterBeforeUpdate.Generation = humioClusterBeforeUpdate.GetGeneration() + 1
		return k8sClient.Update(ctx, &humioClusterBeforeUpdate)
	}, testTimeout, TestInterval).Should(Succeed())

	WaitForReconcileToSync(ctx, key, k8sClient, &humioClusterBeforeUpdate, testTimeout)
}

func WaitForReconcileToSync(ctx context.Context, key types.NamespacedName, k8sClient client.Client, currentHumioCluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
	UsingClusterBy(key.Name, "Waiting for the reconcile loop to complete")
	if currentHumioCluster == nil {
		var updatedHumioCluster humiov1alpha1.HumioCluster
		Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
		currentHumioCluster = &updatedHumioCluster
	}

	beforeGeneration := currentHumioCluster.GetGeneration()
	Eventually(func() int64 {
		Expect(k8sClient.Get(ctx, key, currentHumioCluster)).Should(Succeed())
		observedGen, err := strconv.Atoi(currentHumioCluster.Status.ObservedGeneration)
		if err != nil {
			return -2
		}
		return int64(observedGen)
	}, testTimeout, TestInterval).Should(BeNumerically("==", beforeGeneration))
}

type stdoutErrLine struct {
	// We reuse the same names as Ginkgo so when we print out the relevant log lines we have a common field and value to jump from the test result to the relevant log lines by simply searching for the ID shown in the result.
	CapturedGinkgoWriterOutput, CapturedStdOutErr string

	// Line contains either the CapturedGinkgoWriterOutput or CapturedStdOutErr we get in the spec/suite report.
	Line string

	// LineNumber represents the index of line in the provided slice of lines. This may help to understand what order things were output in case two lines mention the same timestamp.
	LineNumber int

	// State includes information about if a given report passed or failed
	State ginkgotypes.SpecState
}

func PrintLinesWithRunID(runID string, lines []string, specState ginkgotypes.SpecState) {
	for idx, line := range lines {
		output := stdoutErrLine{
			CapturedGinkgoWriterOutput: runID,
			CapturedStdOutErr:          runID,
			Line:                       line,
			LineNumber:                 idx,
			State:                      specState,
		}
		u, _ := json.Marshal(output)
		fmt.Println(string(u))
	}
}

func useDockerCredentials() bool {
	return os.Getenv(dockerUsernameEnvVar) != "" && os.Getenv(dockerPasswordEnvVar) != ""
}

func CreateDockerRegredSecret(ctx context.Context, namespace corev1.Namespace, k8sClient client.Client) {
	if !useDockerCredentials() {
		return
	}

	By("Creating docker registry credentials secret")
	dockerConfigJsonContent, err := json.Marshal(map[string]map[string]map[string]string{
		"auths": {
			"index.docker.io/v1/": {
				"auth": base64.StdEncoding.EncodeToString(
					[]byte(fmt.Sprintf("%s:%s", os.Getenv(dockerUsernameEnvVar), os.Getenv(dockerPasswordEnvVar))),
				),
			},
		},
	})
	Expect(err).ToNot(HaveOccurred())

	regcredSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DockerRegistryCredentialsSecretName,
			Namespace: namespace.Name,
		},
		Data: map[string][]byte{".dockerconfigjson": dockerConfigJsonContent},
		Type: corev1.SecretTypeDockerConfigJson,
	}
	Expect(k8sClient.Create(ctx, &regcredSecret)).To(Succeed())
}
