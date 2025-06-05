package suite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/versions"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	. "github.com/onsi/ginkgo/v2"
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
const DefaultTestTimeout = time.Second * 30 // Standard timeout used throughout the tests

func UsingClusterBy(cluster, text string, callbacks ...func()) {
	timestamp := time.Now().Format(time.RFC3339Nano)
	_, _ = fmt.Fprintln(GinkgoWriter, "STEP | "+timestamp+" | "+cluster+": "+text)
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
			Name:  controller.InitContainerName,
			Ready: true,
		},
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{
		{
			Name:  controller.HumioContainerName,
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

	// Wait for the HumioCluster resource to be fully deleted.
	// This is crucial because finalizers might delay the actual removal.
	UsingClusterBy(cluster.Name, "Waiting for HumioCluster resource deletion")
	Eventually(func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: hc.Name, Namespace: hc.Namespace}, &humiov1alpha1.HumioCluster{})
		return k8serrors.IsNotFound(err)
	}, time.Second*30, TestInterval).Should(BeTrue(), "HumioCluster resource should be deleted") // Increased timeout slightly

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

func CleanupBootstrapToken(ctx context.Context, k8sClient client.Client, hbt *humiov1alpha1.HumioBootstrapToken) {
	var bootstrapToken humiov1alpha1.HumioBootstrapToken
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: hbt.Name, Namespace: hbt.Namespace}, &bootstrapToken)).To(Succeed())

	UsingClusterBy(bootstrapToken.Name, "Deleting the cluster")

	Expect(k8sClient.Delete(ctx, &bootstrapToken)).To(Succeed())

	if bootstrapToken.Status.TokenSecretKeyRef.SecretKeyRef != nil {
		UsingClusterBy(bootstrapToken.Name, fmt.Sprintf("Deleting the secret %s", bootstrapToken.Status.TokenSecretKeyRef.SecretKeyRef))
		_ = k8sClient.Delete(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bootstrapToken.Status.TokenSecretKeyRef.SecretKeyRef.Name,
				Namespace: bootstrapToken.Namespace,
			},
		})
	}
	if bootstrapToken.Status.HashedTokenSecretKeyRef.SecretKeyRef != nil {
		UsingClusterBy(bootstrapToken.Name, fmt.Sprintf("Deleting the secret %s", bootstrapToken.Status.HashedTokenSecretKeyRef.SecretKeyRef))
		_ = k8sClient.Delete(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bootstrapToken.Status.HashedTokenSecretKeyRef.SecretKeyRef.Name,
				Namespace: bootstrapToken.Namespace,
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
						Name:      controller.HumioDataVolumeName,
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

func CreateLicenseSecretIfNeeded(ctx context.Context, clusterKey types.NamespacedName, k8sClient client.Client, cluster *humiov1alpha1.HumioCluster, shouldCreateLicense bool) {
	if !shouldCreateLicense {
		return
	}

	UsingClusterBy(cluster.Name, fmt.Sprintf("Creating the license secret %s", cluster.Spec.License.SecretKeyRef.Name))

	licenseString := "eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzUxMiJ9.eyJpc09lbSI6ZmFsc2UsImF1ZCI6Ikh1bWlvLWxpY2Vuc2UtY2hlY2siLCJzdWIiOiJIdW1pbyBFMkUgdGVzdHMiLCJ1aWQiOiJGUXNvWlM3Yk1PUldrbEtGIiwibWF4VXNlcnMiOjEwLCJhbGxvd1NBQVMiOnRydWUsIm1heENvcmVzIjoxLCJ2YWxpZFVudGlsIjoxNzQzMTY2ODAwLCJleHAiOjE3NzQ1OTMyOTcsImlzVHJpYWwiOmZhbHNlLCJpYXQiOjE2Nzk5ODUyOTcsIm1heEluZ2VzdEdiUGVyRGF5IjoxfQ.someinvalidsignature"

	// If we use a k8s that is not envtest, and we didn't specify we are using a dummy image, we require a valid license
	if !helpers.UseEnvtest() && !helpers.UseDummyImage() {
		licenseString = helpers.GetE2ELicenseFromEnvVar()
	}

	licenseSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
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

	CreateLicenseSecretIfNeeded(ctx, key, k8sClient, cluster, autoCreateLicense)
	createOptionalUserConfigurableResources(ctx, k8sClient, cluster, key)
	simulateHashedBootstrapTokenCreation(ctx, k8sClient, key)

	UsingClusterBy(key.Name, "Creating HumioCluster resource")
	Expect(k8sClient.Create(ctx, cluster)).Should(Succeed())
	if expectedState != humiov1alpha1.HumioClusterStateRunning {
		// Bail out if this is a test that doesn't expect the cluster to be running
		return
	}

	SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, cluster)
	waitForHumioClusterToEnterInitialRunningState(ctx, k8sClient, key, testTimeout)
	verifyNumClusterPods(ctx, k8sClient, key, cluster, testTimeout)
	verifyInitContainers(ctx, k8sClient, key, cluster)
	waitForHumioClusterToEnterRunningState(ctx, k8sClient, key, cluster, testTimeout)
	verifyInitialPodRevision(ctx, k8sClient, key, cluster, testTimeout)
	waitForAdminTokenSecretToGetPopulated(ctx, k8sClient, key, cluster, testTimeout)
	verifyPodAvailabilityZoneWhenUsingRealHumioContainers(ctx, k8sClient, humioClient, key, cluster, testTimeout)
	verifyReplicationFactorEnvironmentVariables(ctx, k8sClient, key, cluster)
	verifyNumPodsPodPhaseRunning(ctx, k8sClient, key, cluster, testTimeout)
	verifyNumPodsContainerStatusReady(ctx, k8sClient, key, cluster, testTimeout)
}

func createOptionalUserConfigurableResources(ctx context.Context, k8sClient client.Client, cluster *humiov1alpha1.HumioCluster, key types.NamespacedName) {
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
}

func waitForHumioClusterToEnterInitialRunningState(ctx context.Context, k8sClient client.Client, key types.NamespacedName, testTimeout time.Duration) {
	UsingClusterBy(key.Name, "Confirming cluster enters running state")
	var updatedHumioCluster humiov1alpha1.HumioCluster
	Eventually(func() string {
		err := k8sClient.Get(ctx, key, &updatedHumioCluster)
		if err != nil && !k8serrors.IsNotFound(err) {
			Expect(err).Should(Succeed())
		}
		return updatedHumioCluster.Status.State
	}, testTimeout, TestInterval).Should(BeIdenticalTo(humiov1alpha1.HumioClusterStateRunning))
}

func waitForHumioClusterToEnterRunningState(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
	UsingClusterBy(key.Name, "Confirming cluster enters running state")
	Eventually(func() string {
		clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(cluster).GetPodLabels())
		_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)

		for idx := range cluster.Spec.NodePools {
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioNodePool(cluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
			_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)
		}

		cluster = &humiov1alpha1.HumioCluster{}
		Expect(k8sClient.Get(ctx, key, cluster)).Should(Succeed())
		return cluster.Status.State
	}, testTimeout, TestInterval).Should(Equal(humiov1alpha1.HumioClusterStateRunning))
}

func verifyInitialPodRevision(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
	UsingClusterBy(key.Name, "Validating cluster has expected pod revision annotation")
	nodeMgrFromHumioCluster := controller.NewHumioNodeManagerFromHumioCluster(cluster)
	if nodeMgrFromHumioCluster.GetNodeCount() > 0 {
		Eventually(func() int {
			cluster = &humiov1alpha1.HumioCluster{}
			Expect(k8sClient.Get(ctx, key, cluster)).Should(Succeed())
			return controller.NewHumioNodeManagerFromHumioCluster(cluster).GetDesiredPodRevision()
		}, testTimeout, TestInterval).Should(BeEquivalentTo(1))
	}
}

func waitForAdminTokenSecretToGetPopulated(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
	UsingClusterBy(key.Name, "Waiting for the controller to populate the secret containing the admin token")
	Eventually(func() error {
		clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(cluster).GetCommonClusterLabels())
		for idx := range clusterPods {
			UsingClusterBy(key.Name, fmt.Sprintf("Pod status %s status: %v", clusterPods[idx].Name, clusterPods[idx].Status))
		}

		return k8sClient.Get(ctx, types.NamespacedName{
			Namespace: key.Namespace,
			Name:      fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix),
		}, &corev1.Secret{})
	}, testTimeout, TestInterval).Should(Succeed())
}

func verifyReplicationFactorEnvironmentVariables(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster) {
	UsingClusterBy(key.Name, "Confirming replication factor environment variables are set correctly")
	clusterPods, err := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(cluster).GetCommonClusterLabels())
	Expect(err).ToNot(HaveOccurred())
	for _, pod := range clusterPods {
		humioIdx, err := kubernetes.GetContainerIndexByName(pod, "humio")
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
}

func verifyNumPodsPodPhaseRunning(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
	Expect(k8sClient.Get(ctx, key, cluster)).Should(Succeed())
	Eventually(func() map[corev1.PodPhase]int {
		phaseToCount := map[corev1.PodPhase]int{
			corev1.PodRunning: 0,
		}

		updatedClusterPods, err := kubernetes.ListPods(ctx, k8sClient, cluster.Namespace, controller.NewHumioNodeManagerFromHumioCluster(cluster).GetPodLabels())
		if err != nil {
			return map[corev1.PodPhase]int{}
		}
		Expect(updatedClusterPods).To(HaveLen(cluster.Spec.NodeCount))

		for _, pod := range updatedClusterPods {
			phaseToCount[pod.Status.Phase] += 1
		}

		return phaseToCount

	}, testTimeout, TestInterval).Should(HaveKeyWithValue(corev1.PodRunning, cluster.Spec.NodeCount))

	for idx := range cluster.Spec.NodePools {
		Eventually(func() map[corev1.PodPhase]int {
			phaseToCount := map[corev1.PodPhase]int{
				corev1.PodRunning: 0,
			}

			updatedClusterPods, err := kubernetes.ListPods(ctx, k8sClient, cluster.Namespace, controller.NewHumioNodeManagerFromHumioNodePool(cluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
			if err != nil {
				return map[corev1.PodPhase]int{}
			}
			Expect(updatedClusterPods).To(HaveLen(cluster.Spec.NodePools[idx].NodeCount))

			for _, pod := range updatedClusterPods {
				phaseToCount[pod.Status.Phase] += 1
			}

			return phaseToCount

		}, testTimeout, TestInterval).Should(HaveKeyWithValue(corev1.PodRunning, cluster.Spec.NodePools[idx].NodeCount))
	}
}

func verifyPodAvailabilityZoneWhenUsingRealHumioContainers(ctx context.Context, k8sClient client.Client, humioClient humio.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
	if !helpers.UseEnvtest() && !helpers.UseDummyImage() {
		UsingClusterBy(key.Name, "Validating cluster nodes have ZONE configured correctly")
		if cluster.Spec.DisableInitContainer {
			Eventually(func() []string {
				clusterConfig, err := helpers.NewCluster(ctx, k8sClient, key.Name, "", key.Namespace, helpers.UseCertManager(), true, false)
				Expect(err).ToNot(HaveOccurred())
				Expect(clusterConfig).ToNot(BeNil())
				Expect(clusterConfig.Config()).ToNot(BeNil())

				humioHttpClient := humioClient.GetHumioHttpClient(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
				cluster, err := humioClient.GetCluster(ctx, humioHttpClient)
				if err != nil {
					return []string{fmt.Sprintf("got err: %s", err)}
				}
				getCluster := cluster.GetCluster()
				if len(getCluster.GetNodes()) < 1 {
					return []string{}
				}
				keys := make(map[string]bool)
				var zoneList []string
				for _, node := range getCluster.GetNodes() {
					zone := node.Zone
					if zone != nil {
						if _, value := keys[*zone]; !value {
							keys[*zone] = true
							zoneList = append(zoneList, *zone)
						}
					}
				}
				return zoneList
			}, testTimeout, TestInterval).Should(BeEmpty())
		} else {
			Eventually(func() []string {
				clusterConfig, err := helpers.NewCluster(ctx, k8sClient, key.Name, "", key.Namespace, helpers.UseCertManager(), true, false)
				Expect(err).ToNot(HaveOccurred())
				Expect(clusterConfig).ToNot(BeNil())
				Expect(clusterConfig.Config()).ToNot(BeNil())

				humioHttpClient := humioClient.GetHumioHttpClient(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
				cluster, err := humioClient.GetCluster(ctx, humioHttpClient)
				getCluster := cluster.GetCluster()
				if err != nil || len(getCluster.GetNodes()) < 1 {
					return []string{}
				}
				keys := make(map[string]bool)
				var zoneList []string
				for _, node := range getCluster.GetNodes() {
					zone := node.Zone
					if zone != nil {
						if _, value := keys[*zone]; !value {
							keys[*zone] = true
							zoneList = append(zoneList, *zone)
						}
					}
				}
				return zoneList
			}, testTimeout, TestInterval).ShouldNot(BeEmpty())
		}
	}
}

func verifyNumClusterPods(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
	UsingClusterBy(key.Name, "Waiting to have the correct number of pods")
	Eventually(func() []corev1.Pod {
		var clusterPods []corev1.Pod
		clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(cluster).GetPodLabels())
		_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)
		return clusterPods
	}, testTimeout, TestInterval).Should(HaveLen(cluster.Spec.NodeCount))

	for idx, pool := range cluster.Spec.NodePools {
		Eventually(func() []corev1.Pod {
			var clusterPods []corev1.Pod
			clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioNodePool(cluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
			_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, clusterPods, key.Name)
			return clusterPods
		}, testTimeout, TestInterval).Should(HaveLen(pool.NodeCount))
	}
}

func simulateHashedBootstrapTokenCreation(ctx context.Context, k8sClient client.Client, key types.NamespacedName) {
	if helpers.UseEnvtest() {
		// Simulate sidecar creating the secret which contains the admin token used to authenticate with humio
		secretData := map[string][]byte{"token": []byte("")}
		adminTokenSecretName := fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix)
		UsingClusterBy(key.Name, "Simulating the admin token secret containing the API token")
		desiredSecret := kubernetes.ConstructSecret(key.Name, key.Namespace, adminTokenSecretName, secretData, nil, nil)
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
	desiredSecret := kubernetes.ConstructSecret(key.Name, key.Namespace, bootstrapTokenSecretName, secretData, nil, nil)
	Expect(k8sClient.Create(ctx, desiredSecret)).To(Succeed())
}

func verifyNumPodsContainerStatusReady(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
	Eventually(func() int {
		numPodsReady := 0
		clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(cluster).GetPodLabels())
		for _, pod := range clusterPods {
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.Name == controller.HumioContainerName && containerStatus.Ready {
					numPodsReady++
				}
			}
		}
		return numPodsReady
	}, testTimeout, TestInterval).Should(BeIdenticalTo(cluster.Spec.NodeCount))

	for idx := range cluster.Spec.NodePools {
		Eventually(func() int {
			numPodsReady := 0
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioNodePool(cluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
			for _, pod := range clusterPods {
				for _, containerStatus := range pod.Status.ContainerStatuses {
					if containerStatus.Name == controller.HumioContainerName && containerStatus.Ready {
						numPodsReady++
					}
				}
			}
			return numPodsReady
		}, testTimeout, TestInterval).Should(BeIdenticalTo(cluster.Spec.NodePools[idx].NodeCount))
	}
}

func verifyInitContainers(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster) []corev1.Pod {
	clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(cluster).GetCommonClusterLabels())
	humioIdx, err := kubernetes.GetContainerIndexByName(clusterPods[0], controller.HumioContainerName)
	Expect(err).ToNot(HaveOccurred())
	humioContainerArgs := strings.Join(clusterPods[0].Spec.Containers[humioIdx].Args, " ")
	if cluster.Spec.DisableInitContainer {
		UsingClusterBy(key.Name, "Confirming pods do not use init container")
		Expect(clusterPods[0].Spec.InitContainers).To(BeEmpty())
		Expect(humioContainerArgs).ToNot(ContainSubstring("export ZONE="))
	} else {
		UsingClusterBy(key.Name, "Confirming pods have an init container")
		Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(1))
		Expect(humioContainerArgs).To(ContainSubstring("export ZONE="))
	}

	for idx := range cluster.Spec.NodePools {
		clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioNodePool(cluster, &cluster.Spec.NodePools[idx]).GetPodLabels())
		humioIdx, err = kubernetes.GetContainerIndexByName(clusterPods[0], controller.HumioContainerName)
		Expect(err).ToNot(HaveOccurred())
		humioContainerArgs = strings.Join(clusterPods[0].Spec.Containers[humioIdx].Args, " ")
		if cluster.Spec.DisableInitContainer {
			UsingClusterBy(key.Name, "Confirming pods do not use init container")
			Expect(clusterPods[0].Spec.InitContainers).To(BeEmpty())
			Expect(humioContainerArgs).ToNot(ContainSubstring("export ZONE="))
		} else {
			UsingClusterBy(key.Name, "Confirming pods have an init container")
			Expect(clusterPods[0].Spec.InitContainers).To(HaveLen(1))
			Expect(humioContainerArgs).To(ContainSubstring("export ZONE="))
		}
	}
	return clusterPods
}

// WaitForReconcileToSync waits until the controller has observed the latest
// spec of the HumioCluster – i.e. .status.observedGeneration is at least the
// current .metadata.generation.
//
// We re-read the object every poll to avoid the bug where the generation was
// captured before the reconciler modified the spec (which increments the
// generation).  This previously made the helper compare the *old* generation
// with the *new* observedGeneration and fail with
// “expected 3 to equal 2”.
func WaitForReconcileToSync(
	ctx context.Context,
	key types.NamespacedName,
	k8sClient client.Client,
	cluster *humiov1alpha1.HumioCluster,
	timeout time.Duration,
) {
	UsingClusterBy(key.Name, "Waiting for HumioCluster observedGeneration to catch up")

	Eventually(func(g Gomega) bool {
		latest := &humiov1alpha1.HumioCluster{}
		err := k8sClient.Get(ctx, key, latest)
		g.Expect(err).NotTo(HaveOccurred(), "failed to fetch HumioCluster")

		currentGen := latest.GetGeneration()

		obsGen, _ := strconv.ParseInt(latest.Status.ObservedGeneration, 10, 64)
		return obsGen >= currentGen
	}, timeout, TestInterval).Should(BeTrue(),
		"HumioCluster %s/%s observedGeneration did not reach generation",
		key.Namespace, key.Name)
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

func SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx context.Context, key types.NamespacedName, k8sClient client.Client, testTimeout time.Duration, cluster *humiov1alpha1.HumioCluster) {
	UsingClusterBy(key.Name, "Simulating HumioBootstrapToken Controller running and adding the secret and status")
	Eventually(func() error {
		var bootstrapImage string
		bootstrapImage = "test"
		if cluster.Spec.Image != "" {
			bootstrapImage = cluster.Spec.Image
		}
		if cluster.Spec.ImageSource != nil {
			configMap, err := kubernetes.GetConfigMap(ctx, k8sClient, cluster.Spec.ImageSource.ConfigMapRef.Name, cluster.Namespace)
			if err != nil && !k8serrors.IsNotFound(err) {
				Expect(err).Should(Succeed())
			} else {
				bootstrapImage = configMap.Data[cluster.Spec.ImageSource.ConfigMapRef.Key]
			}
		}
		for _, nodePool := range cluster.Spec.NodePools {
			if nodePool.HumioNodeSpec.Image != "" {
				bootstrapImage = nodePool.HumioNodeSpec.Image
				break
			}
			if nodePool.ImageSource != nil {
				configMap, err := kubernetes.GetConfigMap(ctx, k8sClient, nodePool.ImageSource.ConfigMapRef.Name, cluster.Namespace)
				if err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).Should(Succeed())
				} else {
					bootstrapImage = configMap.Data[nodePool.ImageSource.ConfigMapRef.Key]
					break
				}
			}
		}
		updatedHumioBootstrapToken, err := GetHumioBootstrapToken(ctx, key, k8sClient)
		if err != nil {
			return err
		}
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
		updatedHumioBootstrapToken.Status.BootstrapImage = bootstrapImage
		return k8sClient.Status().Update(ctx, &updatedHumioBootstrapToken)
	}, testTimeout, TestInterval).Should(Succeed())
}

func GetHumioBootstrapToken(ctx context.Context, key types.NamespacedName, k8sClient client.Client) (humiov1alpha1.HumioBootstrapToken, error) {
	hbtList, err := kubernetes.ListHumioBootstrapTokens(ctx, k8sClient, key.Namespace, kubernetes.LabelsForHumioBootstrapToken(key.Name))
	if err != nil {
		return humiov1alpha1.HumioBootstrapToken{}, err
	}
	if len(hbtList) == 0 {
		return humiov1alpha1.HumioBootstrapToken{}, fmt.Errorf("no humiobootstraptokens for cluster %s", key.Name)
	}
	if len(hbtList) > 1 {
		return humiov1alpha1.HumioBootstrapToken{}, fmt.Errorf("too many humiobootstraptokens for cluster %s. found list : %+v", key.Name, hbtList)
	}
	return hbtList[0], nil
}

// WaitForObservedGeneration waits until .status.observedGeneration is at least the
// current .metadata.generation. It re-reads the object on every poll so it is
// tolerant of extra reconciles that may bump the generation while we are
// waiting.
func WaitForObservedGeneration(
	ctx context.Context,
	k8sClient client.Client,
	obj client.Object,
	timeout, interval time.Duration,
) {
	objKind := obj.GetObjectKind().GroupVersionKind().Kind
	if objKind == "" {
		objKind = reflect.TypeOf(obj).String()
	}

	UsingClusterBy("", fmt.Sprintf(
		"Waiting for observedGeneration to catch up for %s %s/%s",
		objKind, obj.GetNamespace(), obj.GetName()))

	key := client.ObjectKeyFromObject(obj)

	Eventually(func(g Gomega) bool {
		// Always work on a fresh copy so we see the latest generation.
		latest := obj.DeepCopyObject().(client.Object)
		err := k8sClient.Get(ctx, key, latest)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get resource")

		currentGeneration := latest.GetGeneration()
		var observedGen int64

		switch typed := latest.(type) {
		case *humiov1alpha1.HumioPdfRenderService:
			observedGen = typed.Status.ObservedGeneration

		case *humiov1alpha1.HumioCluster:
			val, err := strconv.ParseInt(typed.Status.ObservedGeneration, 10, 64)
			if err != nil {
				observedGen = 0
			} else {
				observedGen = val
			}

		case *appsv1.Deployment:
			observedGen = typed.Status.ObservedGeneration

		default:
			// Resource does not expose observedGeneration – consider it ready.
			return true
		}

		return observedGen >= currentGeneration
	}, timeout, interval).Should(BeTrue(),
		"%s %s/%s observedGeneration did not catch up with generation",
		objKind, obj.GetNamespace(), obj.GetName())
}

// CreatePdfRenderServiceCR creates a basic HumioPdfRenderService CR with better error handling
func CreatePdfRenderServiceCR(ctx context.Context, k8sClient client.Client, pdfKey types.NamespacedName, tlsEnabled bool) *humiov1alpha1.HumioPdfRenderService {
	pdfCR := &humiov1alpha1.HumioPdfRenderService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pdfKey.Name,
			Namespace: pdfKey.Namespace,
		},
		Spec: humiov1alpha1.HumioPdfRenderServiceSpec{
			Image:    versions.DefaultPDFRenderServiceImage(),
			Replicas: 1,
			Port:     controller.DefaultPdfRenderServicePort,
		},
	}
	if tlsEnabled {
		pdfCR.Spec.TLS = &humiov1alpha1.HumioPDFRenderServiceTLSSpec{
			Enabled: helpers.BoolPtr(true),
		}
	}

	UsingClusterBy(pdfKey.Name, fmt.Sprintf("Creating HumioPdfRenderService %s", pdfKey.String()))
	Expect(k8sClient.Create(ctx, pdfCR)).Should(Succeed())

	// Wait for the CR to be created with proper error handling
	Eventually(func(g Gomega) *humiov1alpha1.HumioPdfRenderService {
		var createdPdf humiov1alpha1.HumioPdfRenderService
		err := k8sClient.Get(ctx, pdfKey, &createdPdf)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get HumioPdfRenderService %s", pdfKey.String())
		return &createdPdf
	}, DefaultTestTimeout, TestInterval).ShouldNot(BeNil())

	return pdfCR
}

// EnsurePdfRenderDeploymentReady waits for a PDF render service deployment to be ready
// Accepts either the CR name (adds suffix) or the full deployment name
func EnsurePdfRenderDeploymentReady(ctx context.Context, k8sClient client.Client, key types.NamespacedName) {
	// Always use the correct deployment name format with suffix
	deploymentKey := key
	if !strings.HasSuffix(key.Name, "-pdf-render-service") {
		deploymentKey.Name = key.Name + "-pdf-render-service"
	}

	UsingClusterBy(key.Name, fmt.Sprintf("Ensuring PDF render deployment %s in namespace %s is ready",
		deploymentKey.Name, deploymentKey.Namespace))

	// Wait for the deployment to exist first
	Eventually(func(g Gomega) bool {
		var deployment appsv1.Deployment
		err := k8sClient.Get(ctx, deploymentKey, &deployment)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				// Log information but keep waiting instead of returning error
				fmt.Printf("Deployment %s/%s not found yet, waiting...\n",
					deploymentKey.Namespace, deploymentKey.Name)
				return false
			}
			// Unexpected error
			fmt.Printf("Error getting deployment %s/%s: %v\n",
				deploymentKey.Namespace, deploymentKey.Name, err)
			return false
		}
		return true
	}, DefaultTestTimeout*2, TestInterval).Should(BeTrue(),
		fmt.Sprintf("Timed out waiting for deployment %s/%s to exist",
			deploymentKey.Namespace, deploymentKey.Name))

	// For envtest: manually update the deployment status
	if helpers.UseEnvtest() {
		var deployment appsv1.Deployment
		err := k8sClient.Get(ctx, deploymentKey, &deployment)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to get deployment for status update")

		deployment.Status.Replicas = 1
		deployment.Status.ReadyReplicas = 1
		deployment.Status.AvailableReplicas = 1
		deployment.Status.ObservedGeneration = deployment.Generation

		err = k8sClient.Status().Update(ctx, &deployment)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to update deployment status")
	}

	// Verify deployment is ready
	Eventually(func(g Gomega) int32 {
		var updatedDeployment appsv1.Deployment
		err := k8sClient.Get(ctx, deploymentKey, &updatedDeployment)
		if err != nil {
			fmt.Printf("Error checking deployment readiness for %s/%s: %v\n",
				deploymentKey.Namespace, deploymentKey.Name, err)
			return 0
		}
		fmt.Printf("Deployment %s/%s status: %d/%d replicas ready\n",
			deploymentKey.Namespace, deploymentKey.Name,
			updatedDeployment.Status.ReadyReplicas, updatedDeployment.Status.Replicas)

		// If replicas is set to 0, we should consider it ready when observed generation is up to date
		if updatedDeployment.Spec.Replicas != nil && *updatedDeployment.Spec.Replicas == 0 {
			if updatedDeployment.Status.ObservedGeneration >= updatedDeployment.Generation {
				return 1 // Return non-zero to satisfy the BeNumerically(">", 0) check
			}
		}

		return updatedDeployment.Status.ReadyReplicas
	}, DefaultTestTimeout, TestInterval).Should(BeNumerically(">", 0))
}

// CleanupPdfRenderServiceResources cleans up all resources related to a PDF render service
func CleanupPdfRenderServiceResources(ctx context.Context, k8sClient client.Client, key types.NamespacedName) {
	// Delete HumioPdfRenderService if it exists
	pdfCR := &humiov1alpha1.HumioPdfRenderService{}
	if err := k8sClient.Get(ctx, key, pdfCR); err == nil {
		UsingClusterBy(key.Name, fmt.Sprintf("Deleting HumioPdfRenderService %s", key.String()))
		_ = k8sClient.Delete(ctx, pdfCR)

		// Wait for deletion
		Eventually(func() bool {
			err := k8sClient.Get(ctx, key, &humiov1alpha1.HumioPdfRenderService{})
			return k8serrors.IsNotFound(err)
		}, DefaultTestTimeout, TestInterval).Should(BeTrue())
	}

	// Clean up any orphaned deployment
	deploymentKey := types.NamespacedName{
		Name:      key.Name + "-pdf-render-service",
		Namespace: key.Namespace,
	}
	deployment := &appsv1.Deployment{}
	if err := k8sClient.Get(ctx, deploymentKey, deployment); err == nil {
		UsingClusterBy(key.Name, fmt.Sprintf("Deleting orphaned deployment %s", deploymentKey.String()))
		_ = k8sClient.Delete(ctx, deployment)
	}

	// Clean up any orphaned service
	service := &corev1.Service{}
	if err := k8sClient.Get(ctx, deploymentKey, service); err == nil {
		UsingClusterBy(key.Name, fmt.Sprintf("Deleting orphaned service %s", deploymentKey.String()))
		_ = k8sClient.Delete(ctx, service)
	}
}

// CleanupPdfRenderServiceCR safely deletes a HumioPdfRenderService CR and waits for its deletion
func CleanupPdfRenderServiceCR(ctx context.Context, k8sClient client.Client, pdfCR *humiov1alpha1.HumioPdfRenderService) {
	if pdfCR == nil {
		return
	}

	serviceName := pdfCR.Name
	serviceNamespace := pdfCR.Namespace
	key := types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}

	UsingClusterBy(serviceName, fmt.Sprintf("Cleaning up HumioPdfRenderService %s", key.String()))

	// Get the latest version of the resource
	latestPdfCR := &humiov1alpha1.HumioPdfRenderService{}
	err := k8sClient.Get(ctx, key, latestPdfCR)

	// If not found, it's already deleted
	if k8serrors.IsNotFound(err) {
		return
	}

	// If other error, report it but continue
	if err != nil {
		UsingClusterBy(serviceName, fmt.Sprintf("Error getting HumioPdfRenderService for cleanup: %v", err))
		return
	}

	// Only attempt deletion if not already being deleted
	if latestPdfCR.GetDeletionTimestamp() == nil {
		Expect(k8sClient.Delete(ctx, latestPdfCR)).To(Succeed())
	}

	// Wait for deletion with appropriate timeout
	Eventually(func() bool {
		err := k8sClient.Get(ctx, key, latestPdfCR)
		return k8serrors.IsNotFound(err)
	}, DefaultTestTimeout, TestInterval).Should(BeTrue(),
		"HumioPdfRenderService %s/%s should be deleted", serviceNamespace, serviceName)
}

// CreatePdfRenderServiceAndWait is a convenience wrapper that
// 1. Creates a HumioPdfRenderService CR (optionally overriding Image & TLS)
// 2. Waits until the controller has observed the new generation
// 3. Waits for the child Deployment to become “Ready”
// The returned CR is suitable for defer-cleanup.
// CreatePdfRenderServiceAndWait is a convenience wrapper that
// 1. Creates a HumioPdfRenderService CR (optionally overriding Image & TLS)
// 2. Creates TLS certificate if TLS is enabled
// 3. Waits until the controller has observed the new generation
// 4. Waits for the child Deployment to become "Ready"
// The returned CR is suitable for defer-cleanup.
func CreatePdfRenderServiceAndWait(
	ctx context.Context,
	k8sClient client.Client,
	pdfKey types.NamespacedName,
	image string,
	tlsEnabled bool,
) *humiov1alpha1.HumioPdfRenderService {

	// Step 1 – If TLS is enabled, create the certificate secret first
	if tlsEnabled && helpers.UseCertManager() {
		// Create TLS certificate secret for PDF render service
		tlsSecretName := fmt.Sprintf("%s-pdf-render-service-tls", pdfKey.Name)

		// Generate CA certificate
		caCert, err := controller.GenerateCACertificate()
		Expect(err).ToNot(HaveOccurred(), "Failed to generate CA certificate for PDF render service")

		tlsSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      tlsSecretName,
				Namespace: pdfKey.Namespace,
			},
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSCertKey:       caCert.Certificate,
				corev1.TLSPrivateKeyKey: caCert.Key,
			},
		}

		UsingClusterBy(pdfKey.Name, fmt.Sprintf("Creating TLS certificate secret %s for PDF render service", tlsSecretName))
		Expect(k8sClient.Create(ctx, tlsSecret)).To(Succeed())
	}

	// Step 2 – create the CR
	pdfCR := CreatePdfRenderServiceCR(ctx, k8sClient, pdfKey, tlsEnabled)

	// Optional image override
	if image != "" && pdfCR.Spec.Image != image {
		pdfCR.Spec.Image = image
		Expect(k8sClient.Update(ctx, pdfCR)).To(Succeed())
	}

	// Step 3 – wait for the controller to reconcile the change
	WaitForObservedGeneration(ctx, k8sClient, pdfCR, DefaultTestTimeout, TestInterval)

	// Step 4 – make sure the Deployment is rolled out & Ready
	deploymentKey := types.NamespacedName{
		Name:      pdfKey.Name + "-pdf-render-service",
		Namespace: pdfKey.Namespace,
	}
	EnsurePdfRenderDeploymentReady(ctx, k8sClient, deploymentKey)

	// Step 5 – In envtest environments, manually set the PDF service status to Running
	// since the PDF render service controller doesn't run to update the status automatically
	if helpers.UseEnvtest() {
		UsingClusterBy(pdfKey.Name, "Setting PDF render service status to Running for envtest")
		Eventually(func() error {
			var updated humiov1alpha1.HumioPdfRenderService
			if err := k8sClient.Get(ctx, pdfKey, &updated); err != nil {
				return err
			}
			updated.Status.State = humiov1alpha1.HumioPdfRenderServiceStateRunning
			updated.Status.ReadyReplicas = 1
			updated.Status.ObservedGeneration = updated.Generation
			return k8sClient.Status().Update(ctx, &updated)
		}, DefaultTestTimeout, TestInterval).Should(Succeed())
	}

	return pdfCR
}

func CleanupClusterIfExists(ctx context.Context, k8sClient client.Client, key types.NamespacedName) {
	var hc humiov1alpha1.HumioCluster
	err := k8sClient.Get(ctx, key, &hc)
	if k8serrors.IsNotFound(err) {
		return // nothing to clean
	}
	Expect(err).NotTo(HaveOccurred())
	CleanupCluster(ctx, k8sClient, &hc)
}
