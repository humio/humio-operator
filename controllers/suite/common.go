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

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/controllers"
	"github.com/humio/humio-operator/controllers/versions"
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

func MarkPodsAsRunningIfUsingEnvtest(ctx context.Context, client client.Client, pods []corev1.Pod, clusterName string) error {
	if !helpers.UseEnvtest() {
		return nil
	}

	UsingClusterBy(clusterName, "Simulating Humio container starts up and is marked Ready")
	for _, pod := range pods {
		err := MarkPodAsRunningIfUsingEnvtest(ctx, client, pod, clusterName)
		if err != nil {
			return err
		}
	}
	return nil
}

func MarkPodAsRunningIfUsingEnvtest(ctx context.Context, k8sClient client.Client, pod corev1.Pod, clusterName string) error {
	if !helpers.UseEnvtest() {
		return nil
	}

	UsingClusterBy(clusterName, fmt.Sprintf("Simulating Humio container starts up and is marked Ready (pod phase %s)", pod.Status.Phase))
	pod.Status.PodIP = "192.168.0.1"
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:   corev1.PodReady,
			Status: corev1.ConditionTrue,
		},
	}
	pod.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{
			Name:  controllers.InitContainerName,
			Ready: true,
		},
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{
			Name:  controllers.HumioContainerName,
			Ready: true,
		},
	}
	pod.Status.Phase = corev1.PodRunning
	return k8sClient.Status().Update(ctx, &pod)
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
	userID := int64(65534)

	nodeSpec := humiov1alpha1.HumioNodeSpec{
		Image:             versions.DefaultHumioImageVersion(),
		ExtraKafkaConfigs: "security.protocol=PLAINTEXT",
		NodeCount:         1,
		// Affinity needs to be overridden to exclude default value for kubernetes.io/arch to allow running local tests
		// on ARM-based machines without getting pods stuck in "Pending" due to no nodes matching the affinity rules.
		Affinity: corev1.Affinity{
			NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{
									Key:      corev1.LabelOSStable,
									Operator: corev1.NodeSelectorOpIn,
									Values: []string{
										"linux",
									},
								},
							},
						},
					},
				},
			},
		},
		EnvironmentVariables: []corev1.EnvVar{
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
				Value: "oauth",
			},
			{
				Name:  "ENABLE_IOC_SERVICE",
				Value: "false",
			},
			{
				Name:  "HUMIO_MEMORY_OPTS",
				Value: "-Xss2m -Xms1g -Xmx2g -XX:MaxDirectMemorySize=1g",
			},
			{
				Name:  "HUMIO_JVM_LOG_OPTS",
				Value: "-Xlog:gc+jni=debug:stdout -Xlog:gc*:stdout:time,tags",
			},
			{
				Name:  "HUMIO_OPTS",
				Value: "-Dakka.log-config-on-start=on -Dlog4j2.formatMsgNoLookups=true -Dzookeeper.client.secure=false",
			},
			{
				Name:  "IP_FILTER_ACTIONS",
				Value: "allow all",
			},
		},
		DataVolumePersistentVolumeClaimSpecTemplate: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: *resource.NewQuantity(1*1024*1024*1024, resource.BinarySI),
				},
			},
			StorageClassName: &storageClassNameStandard,
		},
	}

	if !helpers.UseDummyImage() {
		nodeSpec.SidecarContainers = []corev1.Container{
			{
				Name:    "wait-for-global-snapshot-on-disk",
				Image:   versions.SidecarWaitForGlobalImageVersion(),
				Command: []string{"/bin/sh"},
				Args: []string{
					"-c",
					"trap 'exit 0' 15; while true; do sleep 100 & wait $!; done",
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						Exec: &corev1.ExecAction{
							Command: []string{
								"/bin/sh",
								"-c",
								"ls /mnt/global*.json",
							},
						},
					},
					InitialDelaySeconds: 5,
					TimeoutSeconds:      5,
					PeriodSeconds:       10,
					SuccessThreshold:    1,
					FailureThreshold:    100,
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      "humio-data",
						MountPath: "/mnt",
						ReadOnly:  true,
					},
				},
				SecurityContext: &corev1.SecurityContext{
					Privileged:               helpers.BoolPtr(false),
					AllowPrivilegeEscalation: helpers.BoolPtr(false),
					ReadOnlyRootFilesystem:   helpers.BoolPtr(true),
					RunAsUser:                &userID,
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{
							"ALL",
						},
					},
				},
			},
		}
	}

	if UseDockerCredentials() {
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

func CreateLicenseSecret(ctx context.Context, clusterKey types.NamespacedName, k8sClient client.Client, cluster *humiov1alpha1.HumioCluster) {
	UsingClusterBy(cluster.Name, fmt.Sprintf("Creating the license secret %s", cluster.Spec.License.SecretKeyRef.Name))

	licenseString := "eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzUxMiJ9.eyJpc09lbSI6ZmFsc2UsImF1ZCI6Ikh1bWlvLWxpY2Vuc2UtY2hlY2siLCJzdWIiOiJIdW1pbyBFMkUgdGVzdHMiLCJ1aWQiOiJGUXNvWlM3Yk1PUldrbEtGIiwibWF4VXNlcnMiOjEwLCJhbGxvd1NBQVMiOnRydWUsIm1heENvcmVzIjoxLCJ2YWxpZFVudGlsIjoxNzQzMTY2ODAwLCJleHAiOjE3NzQ1OTMyOTcsImlzVHJpYWwiOmZhbHNlLCJpYXQiOjE2Nzk5ODUyOTcsIm1heEluZ2VzdEdiUGVyRGF5IjoxfQ.someinvalidsignature"

	// If we use a k8s that is not envtest, and we didn't specify we are using a dummy image, we require a valid license
	if !helpers.UseEnvtest() && !helpers.UseDummyImage() {
		licenseString = helpers.GetE2ELicenseFromEnvVar()
	}

	licenseSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-license", clusterKey.Name),
			Namespace: clusterKey.Namespace,
		},
		StringData: map[string]string{"license": licenseString},
		Type:       corev1.SecretTypeOpaque,
	}
	Expect(k8sClient.Create(ctx, &licenseSecret)).To(Succeed())
}

func CreateAndBootstrapCluster(ctx context.Context, k8sClient client.Client, humioClient humio.Client, cluster *humiov1alpha1.HumioCluster, autoCreateLicense bool, expectedState string, testTimeout time.Duration) {
	key := types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      cluster.Name,
	}

	if autoCreateLicense {
		CreateLicenseSecret(ctx, key, k8sClient, cluster)
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

	if helpers.UseEnvtest() {
		// Simulate sidecar creating the secret which contains the admin token used to authenticate with humio
		secretData := map[string][]byte{"token": []byte("")}
		adminTokenSecretName := fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix)
		UsingClusterBy(key.Name, "Simulating the admin token secret containing the API token")
		desiredSecret := kubernetes.ConstructSecret(key.Name, key.Namespace, adminTokenSecretName, secretData, nil)
		Expect(k8sClient.Create(ctx, desiredSecret)).To(Succeed())

		UsingClusterBy(key.Name, "Simulating the creation of the HumioBootstrapToken resource")
		humioBootstrapToken := kubernetes.ConstructHumioBootstrapToken(key.Name, key.Namespace)
		humioBootstrapToken.Spec = humiov1alpha1.HumioBootstrapTokenSpec{
			ManagedClusterName: key.Name,
		}
		humioBootstrapToken.Status = humiov1alpha1.HumioBootstrapTokenStatus{
			State: humiov1alpha1.HumioBootstrapTokenStateReady,
			TokenSecretKeyRef: humiov1alpha1.HumioTokenSecretStatus{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-bootstrap-token", key.Name),
				},
				Key: "secret",
			},
			},
			HashedTokenSecretKeyRef: humiov1alpha1.HumioHashedTokenSecretStatus{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-bootstrap-token", key.Name),
				},
				Key: "hashedToken",
			}},
		}
		UsingClusterBy(key.Name, "Creating HumioBootstrapToken resource")
		Expect(k8sClient.Create(ctx, humioBootstrapToken)).Should(Succeed())
	}

	UsingClusterBy(key.Name, "Simulating the humio bootstrap token controller creating the secret containing the API token")
	secretData := map[string][]byte{"hashedToken": []byte("P2HS9.20.r+ZbMqd0pHF65h3yQiOt8n1xNytv/4ePWKIj3cElP7gt8YD+gOtdGGvJYmG229kyFWLs6wXx9lfSDiRGGu/xuQ"), "secret": []byte("cYsrKi6IeyOJVzVIdmVK3M6RGl4y9GpgduYKXk4qWvvj")}
	bootstrapTokenSecretName := fmt.Sprintf("%s-%s", key.Name, kubernetes.BootstrapTokenSecretNameSuffix)
	desiredSecret := kubernetes.ConstructSecret(key.Name, key.Namespace, bootstrapTokenSecretName, secretData, nil)
	Expect(k8sClient.Create(ctx, desiredSecret)).To(Succeed())

	UsingClusterBy(key.Name, "Creating HumioCluster resource")
	Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())

	if expectedState != humiov1alpha1.HumioClusterStateRunning {
		return
	}

	SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout)

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
		_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)
		return clusterPods
	}, testTimeout, TestInterval).Should(HaveLen(cluster.Spec.NodeCount))

	for idx, pool := range cluster.Spec.NodePools {
		Eventually(func() []corev1.Pod {
			var clusterPods []corev1.Pod
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
			_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)
			return clusterPods
		}, testTimeout, TestInterval).Should(HaveLen(pool.NodeCount))
	}

	clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetCommonClusterLabels())
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
		_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)

		for idx := range cluster.Spec.NodePools {
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
			_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)
		}

		updatedHumioCluster = humiov1alpha1.HumioCluster{}
		Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
		return updatedHumioCluster.Status.State
	}, testTimeout, TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))

	UsingClusterBy(key.Name, "Validating cluster has expected pod revision annotation")
	nodeMgrFromHumioCluster := controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster)
	if nodeMgrFromHumioCluster.GetNodeCount() > 0 {
		Eventually(func() int {
			updatedHumioCluster = humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
			return controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetDesiredPodRevision()
		}, testTimeout, TestInterval).Should(BeEquivalentTo(1))
	}

	UsingClusterBy(key.Name, "Waiting for the controller to populate the secret containing the admin token")
	Eventually(func() error {
		clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetCommonClusterLabels())
		for idx := range clusterPods {
			UsingClusterBy(key.Name, fmt.Sprintf("Pod status %s status: %v", clusterPods[idx].Name, clusterPods[idx].Status))
		}

		return k8sClient.Get(ctx, types.NamespacedName{
			Namespace: key.Namespace,
			Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix),
		}, &corev1.Secret{})
	}, testTimeout, TestInterval).Should(Succeed())

	if !helpers.UseEnvtest() && !helpers.UseDummyImage() {
		UsingClusterBy(key.Name, "Validating cluster nodes have ZONE configured correctly")
		if updatedHumioCluster.Spec.DisableInitContainer {
			Eventually(func() []string {
				clusterConfig, err := helpers.NewCluster(ctx, k8sClient, key.Name, "", key.Namespace, helpers.UseCertManager(), true, false)
				Expect(err).To(BeNil())
				Expect(clusterConfig).ToNot(BeNil())
				Expect(clusterConfig.Config()).ToNot(BeNil())

				cluster, err := humioClient.GetClusters(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
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
				clusterConfig, err := helpers.NewCluster(ctx, k8sClient, key.Name, "", key.Namespace, helpers.UseCertManager(), true, false)
				Expect(err).To(BeNil())
				Expect(clusterConfig).ToNot(BeNil())
				Expect(clusterConfig.Config()).ToNot(BeNil())

				cluster, err := humioClient.GetClusters(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
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
				Name:  "DEFAULT_DIGEST_REPLICATION_FACTOR",
				Value: strconv.Itoa(cluster.Spec.TargetReplicationFactor),
			},
			{
				Name:  "DEFAULT_SEGMENT_REPLICATION_FACTOR",
				Value: strconv.Itoa(cluster.Spec.TargetReplicationFactor),
			},
		}))
	}

	Expect(k8sClient.Get(ctx, key, &updatedHumioCluster)).Should(Succeed())
	Eventually(func() map[corev1.PodPhase]int {
		phaseToCount := map[corev1.PodPhase]int{
			corev1.PodRunning: 0,
		}

		updatedClusterPods, err := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
		if err != nil {
			return map[corev1.PodPhase]int{}
		}
		Expect(updatedClusterPods).To(HaveLen(updatedHumioCluster.Spec.NodeCount))

		for _, pod := range updatedClusterPods {
			phaseToCount[pod.Status.Phase] += 1
		}

		return phaseToCount

	}, testTimeout, TestInterval).Should(HaveKeyWithValue(corev1.PodRunning, updatedHumioCluster.Spec.NodeCount))

	for idx := range updatedHumioCluster.Spec.NodePools {
		Eventually(func() map[corev1.PodPhase]int {
			phaseToCount := map[corev1.PodPhase]int{
				corev1.PodRunning: 0,
			}

			updatedClusterPods, err := kubernetes.ListPods(ctx, k8sClient, updatedHumioCluster.Namespace, controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[idx]).GetPodLabels())
			if err != nil {
				return map[corev1.PodPhase]int{}
			}
			Expect(updatedClusterPods).To(HaveLen(updatedHumioCluster.Spec.NodePools[idx].NodeCount))

			for _, pod := range updatedClusterPods {
				phaseToCount[pod.Status.Phase] += 1
			}

			return phaseToCount

		}, testTimeout, TestInterval).Should(HaveKeyWithValue(corev1.PodRunning, updatedHumioCluster.Spec.NodePools[idx].NodeCount))
	}

	Eventually(func() int {
		numPodsReady := 0
		clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioCluster(&updatedHumioCluster).GetPodLabels())
		for _, pod := range clusterPods {
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.Name == controllers.HumioContainerName && containerStatus.Ready {
					numPodsReady++
				}
			}
		}
		return numPodsReady
	}, testTimeout, TestInterval).Should(BeIdenticalTo(updatedHumioCluster.Spec.NodeCount))

	for idx := range updatedHumioCluster.Spec.NodePools {
		Eventually(func() int {
			numPodsReady := 0
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controllers.NewHumioNodeManagerFromHumioNodePool(&updatedHumioCluster, &updatedHumioCluster.Spec.NodePools[idx]).GetPodLabels())
			for _, pod := range clusterPods {
				for _, containerStatus := range pod.Status.ContainerStatuses {
					if containerStatus.Name == controllers.HumioContainerName && containerStatus.Ready {
						numPodsReady++
					}
				}
			}
			return numPodsReady
		}, testTimeout, TestInterval).Should(BeIdenticalTo(updatedHumioCluster.Spec.NodePools[idx].NodeCount))
	}
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

func UseDockerCredentials() bool {
	return os.Getenv(dockerUsernameEnvVar) != "" && os.Getenv(dockerPasswordEnvVar) != "" &&
		os.Getenv(dockerUsernameEnvVar) != "none" && os.Getenv(dockerPasswordEnvVar) != "none"
}

func CreateDockerRegredSecret(ctx context.Context, namespace corev1.Namespace, k8sClient client.Client) {
	if !UseDockerCredentials() {
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

func SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx context.Context, key types.NamespacedName, k8sClient client.Client, testTimeout time.Duration) {
	UsingClusterBy(key.Name, "Simulating HumioBootstrapToken Controller running and adding the secret and status")
	Eventually(func() error {
		hbtList, err := kubernetes.ListHumioBootstrapTokens(ctx, k8sClient, key.Namespace, kubernetes.LabelsForHumioBootstrapToken(key.Name))
		if err != nil {
			return err
		}
		if len(hbtList) == 0 {
			return fmt.Errorf("no humiobootstraptokens for cluster %s", key.Name)
		}
		if len(hbtList) > 1 {
			return fmt.Errorf("too many humiobootstraptokens for cluster %s. found list : %+v", key.Name, hbtList)
		}

		updatedHumioBootstrapToken := hbtList[0]
		updatedHumioBootstrapToken.Status.State = humiov1alpha1.HumioBootstrapTokenStateReady
		updatedHumioBootstrapToken.Status.TokenSecretKeyRef = humiov1alpha1.HumioTokenSecretStatus{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-bootstrap-token", key.Name),
				},
				Key: "secret",
			},
		}
		updatedHumioBootstrapToken.Status.HashedTokenSecretKeyRef = humiov1alpha1.HumioHashedTokenSecretStatus{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: fmt.Sprintf("%s-bootstrap-token", key.Name),
				},
				Key: "hashedToken",
			},
		}
		return k8sClient.Status().Update(ctx, &updatedHumioBootstrapToken)
	}, testTimeout, TestInterval).Should(Succeed())
}
