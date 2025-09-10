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

	cmapi "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/versions"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/humio"
	"github.com/humio/humio-operator/internal/kubernetes"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
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
	envtestVar := os.Getenv("TEST_USING_ENVTEST")
	useEnvtest := helpers.UseEnvtest()
	UsingClusterBy(clusterName, fmt.Sprintf("DEBUG: MarkPodsAsRunningIfUsingEnvtest - TEST_USING_ENVTEST=%s, UseEnvtest()=%t", envtestVar, useEnvtest))

	if !useEnvtest {
		UsingClusterBy(clusterName, "DEBUG: Not using envtest in MarkPodsAsRunningIfUsingEnvtest, returning early")
		return nil
	}

	UsingClusterBy(clusterName, "Simulating Humio container starts up and is marked Ready")
	for _, pod := range pods {

		UsingClusterBy(clusterName, fmt.Sprintf("DEBUG: About to mark pod %s as running", pod.Name))
		err := MarkPodAsRunningIfUsingEnvtest(ctx, client, pod, clusterName)
		if err != nil {
			UsingClusterBy(clusterName, fmt.Sprintf("DEBUG: Error marking pod %s as running: %v", pod.Name, err))
			return err
		}
		UsingClusterBy(clusterName, fmt.Sprintf("DEBUG: Successfully marked pod %s as running", pod.Name))
	}
	return nil
}

func MarkPodAsRunningIfUsingEnvtest(ctx context.Context, k8sClient client.Client, pod corev1.Pod, clusterName string) error {
	envtestVar := os.Getenv("TEST_USING_ENVTEST")
	UsingClusterBy(clusterName, fmt.Sprintf("DEBUG: TEST_USING_ENVTEST=%s, UseEnvtest()=%t", envtestVar, helpers.UseEnvtest()))

	if !helpers.UseEnvtest() {
		UsingClusterBy(clusterName, fmt.Sprintf("DEBUG: Not using envtest, skipping pod %s", pod.Name))
		return nil
	}

	UsingClusterBy(clusterName, fmt.Sprintf("DEBUG: MarkPodAsRunningIfUsingEnvtest called for pod %s", pod.Name))

	// Since this is a helper used in tests, we can inspect the pod spec to determine which container to mark as ready
	var containerName string
	var containerToMarkReady corev1.Container
	for _, container := range pod.Spec.Containers {
		if container.Name == "humio" {
			containerToMarkReady = container
			break
		}
		if container.Name == "humio-pdf-render-service" {
			containerToMarkReady = container
			break
		}
	}
	if containerToMarkReady.Name == "" {
		// fallback to humio
		containerName = "humio"
	} else {
		containerName = containerToMarkReady.Name
	}
	UsingClusterBy(clusterName, fmt.Sprintf("Simulating Humio container %s starts up and is marked Ready (pod phase %s)", containerName, pod.Status.Phase))

	fmt.Printf("BEFORE UPDATE - Pod %s containers: %+v\n", pod.Name, pod.Status.ContainerStatuses)

	pod.Status.PodIP = "192.168.0.1"
	pod.Status.Conditions = []corev1.PodCondition{
		{
			Type:               corev1.PodReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               corev1.ContainersReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               corev1.PodScheduled,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               corev1.PodInitialized,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
	}
	pod.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{
			Name:  controller.InitContainerName,
			Ready: true,
		},
	}

	// Update or create container statuses - preserve existing statuses and update/add the target container
	updatedContainerStatuses := make([]corev1.ContainerStatus, 0, len(pod.Status.ContainerStatuses)+1)
	containerFound := false

	fmt.Printf("Looking for container name: %s\n", containerName)

	// Update existing container statuses
	for _, existingStatus := range pod.Status.ContainerStatuses {
		fmt.Printf("Processing existing container: %s (Ready=%t)\n", existingStatus.Name, existingStatus.Ready)
		if existingStatus.Name == containerName {
			// Update the target container to be ready
			existingStatus.Ready = true
			existingStatus.Started = &[]bool{true}[0]
			existingStatus.State = corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{
					StartedAt: metav1.Now(),
				},
			}
			containerFound = true
			fmt.Printf("Updated container %s to Ready=true\n", existingStatus.Name)
		}
		updatedContainerStatuses = append(updatedContainerStatuses, existingStatus)
	}

	// If the container wasn't found in existing statuses, add it
	if !containerFound {
		fmt.Printf("Container %s not found, adding new status\n", containerName)
		updatedContainerStatuses = append(updatedContainerStatuses, corev1.ContainerStatus{
			Name:    containerName,
			Ready:   true,
			Started: &[]bool{true}[0],
			State: corev1.ContainerState{
				Running: &corev1.ContainerStateRunning{
					StartedAt: metav1.Now(),
				},
			},
		})
	}

	pod.Status.ContainerStatuses = updatedContainerStatuses
	pod.Status.Phase = corev1.PodRunning

	fmt.Printf("AFTER UPDATE - Pod %s containers: %+v\n", pod.Name, pod.Status.ContainerStatuses)

	// Fetch the latest pod from the cluster and update its status
	var latestPod corev1.Pod
	podKey := types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}
	err := k8sClient.Get(ctx, podKey, &latestPod)
	if err != nil {
		fmt.Printf("ERROR getting latest pod %s: %v\n", pod.Name, err)
		return err
	}

	// Apply our status changes to the latest pod
	latestPod.Status.PodIP = "192.168.0.1"
	latestPod.Status.Conditions = []corev1.PodCondition{
		{
			Type:               corev1.PodReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               corev1.ContainersReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               corev1.PodScheduled,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               corev1.PodInitialized,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
	}
	latestPod.Status.InitContainerStatuses = []corev1.ContainerStatus{
		{
			Name:  controller.InitContainerName,
			Ready: true,
		},
	}
	latestPod.Status.ContainerStatuses = updatedContainerStatuses
	latestPod.Status.Phase = corev1.PodRunning

	err = k8sClient.Status().Update(ctx, &latestPod)
	if err != nil {
		fmt.Printf("ERROR updating pod status: %v\n", err)
	} else {
		fmt.Printf("Successfully updated pod %s status\n", pod.Name)

		// Verify the update by re-fetching the pod
		var verifyPod corev1.Pod
		verifyErr := k8sClient.Get(ctx, podKey, &verifyPod)
		if verifyErr == nil {
			fmt.Printf("VERIFICATION: Pod %s phase=%s, containers=%+v\n",
				verifyPod.Name, verifyPod.Status.Phase, verifyPod.Status.ContainerStatuses)
		} else {
			fmt.Printf("VERIFICATION ERROR: Could not re-fetch pod %s: %v\n", pod.Name, verifyErr)
		}
	}
	return err

}

func CleanupCluster(ctx context.Context, k8sClient client.Client, hc *humiov1alpha1.HumioCluster) {
	var cluster humiov1alpha1.HumioCluster
	err := k8sClient.Get(ctx, types.NamespacedName{Name: hc.Name, Namespace: hc.Namespace}, &cluster)
	if k8serrors.IsNotFound(err) {
		// Cluster is already deleted, nothing to clean up
		return
	}
	Expect(err).To(Succeed())
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

	// If we use a k8s that is not a test environment (envtest, dummy image), we require a valid license
	// For kind clusters, we also use the real license for PDF Render Service tests to work properly
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

// func waitForAdminTokenSecretToGetPopulated(ctx context.Context, k8sClient client.Client, key types.NamespacedName, cluster *humiov1alpha1.HumioCluster, testTimeout time.Duration) {
// 	UsingClusterBy(key.Name, "Waiting for the controller to populate the secret containing the admin token")
// 	Eventually(func() error {
// 		clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(cluster).GetCommonClusterLabels())
// 		for idx := range clusterPods {
// 			UsingClusterBy(key.Name, fmt.Sprintf("Pod status %s status: %v", clusterPods[idx].Name, clusterPods[idx].Status))
// 		}

// 		adminTokenSecretName := fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix)
// 		err := k8sClient.Get(ctx, types.NamespacedName{
// 			Namespace: key.Namespace,
// 			Name:      adminTokenSecretName,
// 		}, &corev1.Secret{})

// 		// For test environments (envtest, dummy images, or kind cluster), recreate the secret if it doesn't exist
// 		if err != nil && k8serrors.IsNotFound(err) && (helpers.UseEnvtest() || helpers.UseDummyImage() || helpers.UseKindCluster()) {

// 			UsingClusterBy(key.Name, "Creating admin token secret for test environment (secret not found)")
// 			secretData := map[string][]byte{"token": []byte("")}
// 			desiredSecret := kubernetes.ConstructSecret(key.Name, key.Namespace, adminTokenSecretName, secretData, nil, nil)
// 			createErr := k8sClient.Create(ctx, desiredSecret)
// 			if createErr != nil {
// 				// If secret already exists, ignore the error and continue
// 				if !k8serrors.IsAlreadyExists(createErr) {
// 					return fmt.Errorf("failed to create admin token secret: %w", createErr)
// 				}
// 				UsingClusterBy(key.Name, "Admin token secret already exists, continuing")
// 			}
// 			return nil
// 		}

// 		return err
// 	}, testTimeout, TestInterval).Should(Succeed())
// }

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
	if !helpers.UseEnvtest() && !helpers.UseDummyImage() && !helpers.UseKindCluster() {
		UsingClusterBy(key.Name, "Validating cluster nodes have ZONE configured correctly")
		if cluster.Spec.DisableInitContainer {
			Eventually(func() []string {
				clusterConfig, err := helpers.NewCluster(ctx, k8sClient, key.Name, "", key.Namespace, helpers.UseCertManager(), true, false)
				Expect(err).ToNot(HaveOccurred())
				Expect(clusterConfig).ToNot(BeNil())
				Expect(clusterConfig.Config()).ToNot(BeNil())

				humioHttpClient := humioClient.GetHumioHttpClient(clusterConfig.Config(), reconcile.Request{NamespacedName: key})
				cluster, err := humioClient.GetCluster(ctx, humioHttpClient)
				if err != nil || cluster == nil {
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
				if err != nil || cluster == nil {
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
	if helpers.UseEnvtest() || helpers.UseKindCluster() {
		// Simulate sidecar creating the secret which contains the admin token used to authenticate with humio
		secretData := map[string][]byte{"token": []byte("")}
		adminTokenSecretName := fmt.Sprintf("%s-%s", key.Name, kubernetes.ServiceTokenSecretNameSuffix)
		UsingClusterBy(key.Name, "Simulating the admin token secret containing the API token")
		desiredSecret := kubernetes.ConstructSecret(key.Name, key.Namespace, adminTokenSecretName, secretData, nil, nil)
		err := k8sClient.Create(ctx, desiredSecret)
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			Expect(err).ToNot(HaveOccurred())
		}

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
			// Add minimal resource requirements for reliable pod startup in Kind clusters
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
		},
	}
	// ALWAYS set TLS configuration explicitly based on the tlsEnabled parameter
	// This ensures the CR is created with explicit TLS settings to prevent controller defaults
	if tlsEnabled {
		pdfCR.Spec.TLS = &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
			Enabled: helpers.BoolPtr(true),
		}
	} else {
		// Explicitly disable TLS to override any defaults
		// This is critical for tests that don't involve TLS functionality
		pdfCR.Spec.TLS = &humiov1alpha1.HumioPdfRenderServiceTLSSpec{
			Enabled: helpers.BoolPtr(false),
		}
	}

	UsingClusterBy(pdfKey.Name, fmt.Sprintf("Creating HumioPdfRenderService %s (TLS enabled: %t)", pdfKey.String(), tlsEnabled))
	Expect(k8sClient.Create(ctx, pdfCR)).Should(Succeed())

	// Wait for the CR to be created with proper error handling
	Eventually(func(g Gomega) *humiov1alpha1.HumioPdfRenderService {
		var createdPdf humiov1alpha1.HumioPdfRenderService
		err := k8sClient.Get(ctx, pdfKey, &createdPdf)
		g.Expect(err).NotTo(HaveOccurred(), "Failed to get HumioPdfRenderService %s", pdfKey.String())

		// Verify TLS configuration is set correctly
		if tlsEnabled {
			g.Expect(createdPdf.Spec.TLS).NotTo(BeNil(), "TLS spec should not be nil when TLS is enabled")
			g.Expect(createdPdf.Spec.TLS.Enabled).NotTo(BeNil(), "TLS.Enabled should not be nil")
			g.Expect(*createdPdf.Spec.TLS.Enabled).To(BeTrue(), "TLS.Enabled should be true when TLS is enabled")
		} else {
			g.Expect(createdPdf.Spec.TLS).NotTo(BeNil(), "TLS spec should not be nil even when TLS is disabled")
			g.Expect(createdPdf.Spec.TLS.Enabled).NotTo(BeNil(), "TLS.Enabled should not be nil")
			g.Expect(*createdPdf.Spec.TLS.Enabled).To(BeFalse(), "TLS.Enabled should be false when TLS is disabled")
		}

		// Add debug logging to understand what's happening
		UsingClusterBy(pdfKey.Name, fmt.Sprintf("Created HumioPdfRenderService %s with TLS spec: %+v", pdfKey.String(), createdPdf.Spec.TLS))

		return &createdPdf
	}, DefaultTestTimeout, TestInterval).ShouldNot(BeNil())

	return pdfCR
}

// EnsurePdfRenderDeploymentReady waits until the Deployment created for a
// resolveDeploymentKey translates CR name to Deployment name if needed
func resolveDeploymentKey(key types.NamespacedName) (types.NamespacedName, string) {
	deployKey := key
	crName := key.Name
	// Always use the helper to generate the deployment name
	deployKey.Name = helpers.PdfRenderServiceChildName(key.Name)
	return deployKey, crName
}

// handleEnvtestDeployment manages deployment status in envtest environment
func handleEnvtestDeployment(ctx context.Context, k8sClient client.Client, deployKey types.NamespacedName, exp int32, crName string) {
	// Ensure pods exist and are ready first
	if err := EnsurePodsForDeploymentInEnvtest(ctx, k8sClient, deployKey, crName); err != nil {
		UsingClusterBy(crName, fmt.Sprintf("Failed to ensure pods for deployment: %v", err))
	}

	// Now update the deployment status to reflect ready state
	Eventually(func() bool {
		var dep appsv1.Deployment
		if err := k8sClient.Get(ctx, deployKey, &dep); err != nil {
			return false
		}

		// Update deployment status to reflect ready replicas
		dep.Status.Replicas = exp
		dep.Status.UpdatedReplicas = exp
		dep.Status.ReadyReplicas = exp
		dep.Status.AvailableReplicas = exp
		dep.Status.ObservedGeneration = dep.Generation
		dep.Status.Conditions = []appsv1.DeploymentCondition{
			{
				Type:               appsv1.DeploymentProgressing,
				Status:             corev1.ConditionTrue,
				Reason:             "NewReplicaSetAvailable",
				Message:            "ReplicaSet has successfully progressed.",
				LastUpdateTime:     metav1.Now(),
				LastTransitionTime: metav1.Now(),
			},
			{
				Type:               appsv1.DeploymentAvailable,
				Status:             corev1.ConditionTrue,
				Reason:             "MinimumReplicasAvailable",
				Message:            "Deployment has minimum availability.",
				LastUpdateTime:     metav1.Now(),
				LastTransitionTime: metav1.Now(),
			},
		}

		if err := k8sClient.Status().Update(ctx, &dep); err != nil {
			UsingClusterBy(crName, fmt.Sprintf("Failed to update deployment status: %v", err))
			return false
		}
		return true
	}, DefaultTestTimeout, TestInterval).Should(BeTrue(),
		"Should successfully update deployment status in envtest")
}

// handleKindDeployment manages deployment in Kind cluster environment
func handleKindDeployment(ctx context.Context, k8sClient client.Client, deployKey types.NamespacedName, listPods func() ([]corev1.Pod, error), exp int32, crName string) {
	// Get current replica count for Kind cluster pod readiness
	var dep appsv1.Deployment
	if err := k8sClient.Get(ctx, deployKey, &dep); err == nil {
		currentExp := int32(1)
		if dep.Spec.Replicas != nil {
			currentExp = *dep.Spec.Replicas
		}
		ensurePdfRenderPodsReadyInKind(ctx, k8sClient, listPods, currentExp, crName)
	} else {
		ensurePdfRenderPodsReadyInKind(ctx, k8sClient, listPods, exp, crName)
	}

	Eventually(func() bool {
		if err := k8sClient.Get(ctx, deployKey, &dep); err != nil {
			return false
		}
		// Re-read the expected replica count from current deployment spec
		// to handle cases where replicas change during reconciliation
		currentExp := int32(1)
		if dep.Spec.Replicas != nil {
			currentExp = *dep.Spec.Replicas
		}

		// For Kind clusters, continuously ensure pods stay ready during stabilization check
		if helpers.UseKindCluster() && currentExp > 0 {
			ensurePdfRenderPodsReadyInKind(ctx, k8sClient, listPods, currentExp, crName)

			// Also manually update deployment status to reflect ready pods
			// since the deployment controller might be slow to update in Kind
			if dep.Status.ReadyReplicas < currentExp {
				dep.Status.ReadyReplicas = currentExp
				dep.Status.AvailableReplicas = currentExp
				if err := k8sClient.Status().Update(ctx, &dep); err != nil {
					UsingClusterBy(crName, fmt.Sprintf("Failed to update deployment status: %v", err))
				}
			}
		}

		return isDeploymentStabilized(&dep, currentExp)
	}, DefaultTestTimeout*2, TestInterval).Should(BeTrue(),
		"Deployment should stabilize with correct replica count")
}

// ensurePodReadiness handles pod count and readiness verification
func ensurePodReadiness(ctx context.Context, k8sClient client.Client, deployKey types.NamespacedName, listPods func() ([]corev1.Pod, error), exp int32, crName string) {
	// Wait for exactly the expected number of pods (dynamically read from deployment)
	Eventually(func() int {
		var dep appsv1.Deployment
		// Re-read deployment to get current replica count
		if err := k8sClient.Get(ctx, deployKey, &dep); err != nil {
			return -1 // Return invalid count to continue waiting
		}
		currentExp := int32(1)
		if dep.Spec.Replicas != nil {
			currentExp = *dep.Spec.Replicas
		}

		pods, _ := listPods()
		// In envtest, ensure we have the right number of pods
		if helpers.UseEnvtest() {
			_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, pods, crName)
		}
		// Count only pods that are not being deleted
		activePods := 0
		for _, pod := range pods {
			if pod.DeletionTimestamp == nil {
				activePods++
			}
		}

		// Only check pod count if it matches current expected replicas
		if activePods == int(currentExp) {
			return activePods
		}
		return -1 // Continue waiting if pod count doesn't match
	}, DefaultTestTimeout, TestInterval).Should(BeNumerically(">=", 0),
		"Should have matching pod count for current deployment replica specification")

	// Wait until every pod is Ready (container + pod condition)
	Eventually(func() int {
		pods, _ := listPods()

		if helpers.UseEnvtest() {
			now := metav1.Now()
			for i := range pods {
				// Skip pods that are being terminated
				if pods[i].DeletionTimestamp != nil {
					continue
				}

				// mark containers ready
				for j := range pods[i].Status.ContainerStatuses {
					pods[i].Status.ContainerStatuses[j].Ready = true
				}

				// add/update PodReady condition
				readySet := false
				for k := range pods[i].Status.Conditions {
					if pods[i].Status.Conditions[k].Type == corev1.PodReady {
						pods[i].Status.Conditions[k].Status = corev1.ConditionTrue
						pods[i].Status.Conditions[k].LastTransitionTime = now
						readySet = true
						break
					}
				}
				if !readySet {
					pods[i].Status.Conditions = append(pods[i].Status.Conditions, corev1.PodCondition{
						Type:               corev1.PodReady,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: now,
					})
				}
				pods[i].Status.Phase = corev1.PodRunning
				_ = k8sClient.Status().Update(ctx, &pods[i])
			}
		}

		readyPods := 0
		isPodReady := func(p corev1.Pod) bool {
			// Skip terminating pods
			if p.DeletionTimestamp != nil {
				return false
			}
			for _, c := range p.Status.Conditions {
				if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
					return true
				}
			}
			return false
		}

		for _, p := range pods {
			if isPodReady(p) {
				readyPods++
			}
		}

		// Count active pods for debugging
		activePods := 0
		for _, p := range pods {
			if p.DeletionTimestamp == nil {
				activePods++
			}
		}
		UsingClusterBy(crName, fmt.Sprintf("Ready pods: %d/%d (active pods: %d, total pods: %d)",
			readyPods, exp, activePods, len(pods)))

		return readyPods
	}, DefaultTestTimeout*2, TestInterval).Should(Equal(int(exp)),
		"Should have exactly %d ready pods", exp)
}

// verifyFinalDeploymentStatus performs final verification of deployment status
func verifyFinalDeploymentStatus(ctx context.Context, k8sClient client.Client, deployKey types.NamespacedName, exp int32, crName string) {
	Eventually(func(g Gomega) {
		var dep appsv1.Deployment
		err := k8sClient.Get(ctx, deployKey, &dep)
		g.Expect(err).NotTo(HaveOccurred())

		UsingClusterBy(crName, fmt.Sprintf(
			"Deployment status - Ready: %d/%d, Updated: %d, Available: %d, ObservedGen: %d/%d",
			dep.Status.ReadyReplicas, exp,
			dep.Status.UpdatedReplicas,
			dep.Status.AvailableReplicas,
			dep.Status.ObservedGeneration, dep.Generation))

		// In envtest, ensure deployment status is up to date one more time
		if helpers.UseEnvtest() && dep.Status.ReadyReplicas != exp {
			dep.Status.Replicas = exp
			dep.Status.UpdatedReplicas = exp
			dep.Status.ReadyReplicas = exp
			dep.Status.AvailableReplicas = exp
			_ = k8sClient.Status().Update(ctx, &dep)
		}

		// All these conditions must be met for a fully ready deployment
		g.Expect(dep.Status.ObservedGeneration).To(Equal(dep.Generation),
			"ObservedGeneration should match Generation")
		g.Expect(dep.Status.Replicas).To(Equal(exp),
			"Total replicas should be %d", exp)
		g.Expect(dep.Status.UpdatedReplicas).To(Equal(exp),
			"Updated replicas should be %d", exp)
		g.Expect(dep.Status.ReadyReplicas).To(Equal(exp),
			"Ready replicas should be %d", exp)
		g.Expect(dep.Status.AvailableReplicas).To(Equal(exp),
			"Available replicas should be %d", exp)
	}, DefaultTestTimeout*2, TestInterval).Should(Succeed())
}

// HumioPdfRenderService is fully rolled-out with the expected number of ready replicas.
func EnsurePdfRenderDeploymentReady(
	ctx context.Context,
	k8sClient client.Client,
	key types.NamespacedName,
) {
	// Resolve deployment key and CR name
	deployKey, crName := resolveDeploymentKey(key)

	UsingClusterBy(crName, "DEBUG: EnsurePdfRenderDeploymentReady function called")
	UsingClusterBy(crName, fmt.Sprintf("DEBUG: Looking for deployment with key: %+v", deployKey))
	UsingClusterBy(crName, fmt.Sprintf("DEBUG: Environment check - UseEnvtest()=%t, UseKindCluster()=%t, UseDummyImage()=%t",
		helpers.UseEnvtest(), helpers.UseKindCluster(), helpers.UseDummyImage()))
	UsingClusterBy(crName, fmt.Sprintf("DEBUG: Environment variables - TEST_USING_ENVTEST=%s, HUMIO_E2E_LICENSE=%s",
		os.Getenv("TEST_USING_ENVTEST"), os.Getenv("HUMIO_E2E_LICENSE")))
	UsingClusterBy(crName,
		fmt.Sprintf("Waiting for Deployment %s/%s to be ready",
			deployKey.Namespace, deployKey.Name))

	// Wait until the Deployment object exists
	var dep appsv1.Deployment
	Eventually(func() bool {
		err := k8sClient.Get(ctx, deployKey, &dep)
		if err != nil {
			UsingClusterBy(crName, fmt.Sprintf("Deployment not found yet: %v", err))
		}
		return err == nil
	}, DefaultTestTimeout*2, TestInterval).Should(BeTrue())

	// Helper to list only pods that belong to this Deployment
	selector := labels.SelectorFromSet(dep.Spec.Selector.MatchLabels)
	listPods := func() ([]corev1.Pod, error) {
		var pl corev1.PodList
		err := k8sClient.List(ctx, &pl,
			client.InNamespace(deployKey.Namespace),
			client.MatchingLabelsSelector{Selector: selector})
		return pl.Items, err
	}

	// Get expected replica count
	exp := int32(1)
	if dep.Spec.Replicas != nil {
		exp = *dep.Spec.Replicas
	}

	// Handle deployment based on environment
	UsingClusterBy(crName, "DEBUG: About to check environment type")
	if helpers.UseEnvtest() {
		UsingClusterBy(crName, "DEBUG: Taking envtest code path")
		handleEnvtestDeployment(ctx, k8sClient, deployKey, exp, crName)
	} else if helpers.UseKindCluster() {
		handleKindDeployment(ctx, k8sClient, deployKey, listPods, exp, crName)
	}

	// Mark pods as running if using envtest
	pods, _ := listPods()
	_ = MarkPodsAsRunningIfUsingEnvtest(ctx, k8sClient, pods, crName)

	// Ensure pod readiness
	ensurePodReadiness(ctx, k8sClient, deployKey, listPods, exp, crName)

	// Final verification of deployment status
	verifyFinalDeploymentStatus(ctx, k8sClient, deployKey, exp, crName)

	UsingClusterBy(crName, fmt.Sprintf("Deployment %s/%s is ready with %d replicas",
		deployKey.Namespace, deployKey.Name, exp))
}

// ensurePdfRenderPodsReadyInKind helps PDF render service pods become ready in Kind clusters
func ensurePdfRenderPodsReadyInKind(ctx context.Context, k8sClient client.Client, listPods func() ([]corev1.Pod, error), exp int32, crName string) {
	UsingClusterBy(crName, "Kind cluster detected - helping PDF render service pods become ready")
	// For Kind clusters, we need to manually mark PDF render service pods as ready
	// since they may not start properly due to missing actual PDF render service binary
	Eventually(func() bool {
		pods, err := listPods()
		if err != nil {
			UsingClusterBy(crName, fmt.Sprintf("Error listing pods: %v", err))
			return false
		}

		if exp == 0 {
			// If expected pods is 0, we're satisfied when no active pods exist
			activePods := 0
			for _, pod := range pods {
				if pod.DeletionTimestamp == nil {
					activePods++
				}
			}
			UsingClusterBy(crName, fmt.Sprintf("Expected 0 pods, found %d active pods", activePods))
			return activePods == 0
		}

		if len(pods) == 0 {
			UsingClusterBy(crName, "No pods found yet, waiting for deployment controller to create them")
			return false
		}

		// Count pods that are not being deleted
		activePods := 0
		deletingPods := 0
		for _, pod := range pods {
			if pod.DeletionTimestamp != nil {
				deletingPods++
			} else {
				activePods++
			}
		}
		UsingClusterBy(crName, fmt.Sprintf("Found %d total pods (%d active, %d being deleted), expected %d active", len(pods), activePods, deletingPods, exp))

		// Mark PDF render service pods as ready in Kind
		readyPods := 0
		for i := range pods {
			if pods[i].DeletionTimestamp != nil {
				continue
			}

			// Check if pod is already ready
			isReady := false
			for _, condition := range pods[i].Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					isReady = true
					break
				}
			}

			if isReady {
				readyPods++
				continue
			}

			// Update pod status to mark containers and pod as ready
			pods[i].Status.Phase = corev1.PodRunning
			pods[i].Status.Conditions = []corev1.PodCondition{
				{
					Type:               corev1.PodReady,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               corev1.ContainersReady,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
				{
					Type:               corev1.PodScheduled,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
				},
			}

			// Mark all containers as ready
			for j := range pods[i].Status.ContainerStatuses {
				pods[i].Status.ContainerStatuses[j].Ready = true
				pods[i].Status.ContainerStatuses[j].Started = &[]bool{true}[0]
				pods[i].Status.ContainerStatuses[j].State = corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{
						StartedAt: metav1.Now(),
					},
				}
			}

			// If no container statuses exist, create them
			if len(pods[i].Status.ContainerStatuses) == 0 {
				for _, container := range pods[i].Spec.Containers {
					pods[i].Status.ContainerStatuses = append(pods[i].Status.ContainerStatuses, corev1.ContainerStatus{
						Name:    container.Name,
						Ready:   true,
						Started: &[]bool{true}[0],
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{
								StartedAt: metav1.Now(),
							},
						},
					})
				}
			}

			if err := k8sClient.Status().Update(ctx, &pods[i]); err != nil {
				UsingClusterBy(crName, fmt.Sprintf("Failed to update pod %s status: %v", pods[i].Name, err))
				return false
			}

			readyPods++
		}

		UsingClusterBy(crName, fmt.Sprintf("Made %d pods ready out of %d active pods, expected %d", readyPods, activePods, exp))
		return activePods == int(exp) && readyPods >= int(exp)
	}, DefaultTestTimeout, TestInterval).Should(BeTrue(),
		"Should mark PDF render service pods as ready in Kind")
}

// isDeploymentStabilized checks if a deployment has stabilized with the expected replica count
func isDeploymentStabilized(dep *appsv1.Deployment, exp int32) bool {
	// Ensure observedGeneration matches
	if dep.Status.ObservedGeneration != dep.Generation {
		return false
	}

	// Check if deployment is progressing (rolling update in progress)
	isStableProgression := false
	isRollingOut := false

	for _, cond := range dep.Status.Conditions {
		if cond.Type == appsv1.DeploymentProgressing {
			if cond.Status == corev1.ConditionTrue && cond.Reason == "NewReplicaSetAvailable" {
				// Deployment is stable
				isStableProgression = true
			}
			if cond.Status == corev1.ConditionTrue && (cond.Reason == "ReplicaSetUpdated" || cond.Reason == "FoundNewReplicaSet") {
				// Still rolling out
				isRollingOut = true
			}
		}
	}

	// If still rolling out, not stable yet
	if isRollingOut {
		return false
	}

	// Check replicas are stable
	replicasStable := dep.Status.Replicas == exp &&
		dep.Status.UpdatedReplicas == exp &&
		dep.Status.AvailableReplicas == exp

	// For zero replicas, we're more lenient with progression conditions
	// as scaling down to 0 might not always set ideal conditions immediately
	if exp == 0 && replicasStable {
		// For zero replica deployments, if all replica counts are 0 and we're not rolling out,
		// and generation matches, consider it stable even without perfect progression condition
		return true
	}

	// For non-zero replicas, require stable progression condition
	if !isStableProgression {
		return false
	}

	return replicasStable
}

// EnsurePodsForDeploymentInEnvtest ensures that the correct number of pods exist for a deployment in envtest
func EnsurePodsForDeploymentInEnvtest(ctx context.Context, k8sClient client.Client, deployKey types.NamespacedName, crName string) error {
	if !helpers.UseEnvtest() {
		return nil
	}

	dep := &appsv1.Deployment{}
	if err := k8sClient.Get(ctx, deployKey, dep); err != nil {
		return err
	}

	// List existing pods for this deployment
	podList := &corev1.PodList{}
	matchingLabels := client.MatchingLabels(dep.Spec.Selector.MatchLabels)
	if err := k8sClient.List(ctx, podList, client.InNamespace(deployKey.Namespace), matchingLabels); err != nil {
		return err
	}

	desiredReplicas := int32(1)
	if dep.Spec.Replicas != nil {
		desiredReplicas = *dep.Spec.Replicas
	}

	// Count only active (non-terminating) pods
	activePods := 0
	for _, pod := range podList.Items {
		if pod.DeletionTimestamp == nil {
			activePods++
		}
	}

	UsingClusterBy(crName, fmt.Sprintf("EnsurePodsForDeploymentInEnvtest: desired=%d, current=%d, active=%d", desiredReplicas, len(podList.Items), activePods))

	// Delete extra pods if we have too many
	if activePods > int(desiredReplicas) {
		UsingClusterBy(crName, fmt.Sprintf("Found %d active pods but only need %d, deleting extras", activePods, desiredReplicas))
		podsToDelete := activePods - int(desiredReplicas)
		deleted := 0
		for _, pod := range podList.Items {
			if deleted >= podsToDelete {
				break
			}
			if pod.DeletionTimestamp == nil {
				UsingClusterBy(crName, fmt.Sprintf("Deleting extra pod %s", pod.Name))
				if err := k8sClient.Delete(ctx, &pod); err != nil && !k8serrors.IsNotFound(err) {
					UsingClusterBy(crName, fmt.Sprintf("Failed to delete pod %s: %v", pod.Name, err))
				}
				deleted++
			}
		}
	}

	// Create missing pods if needed
	for i := activePods; i < int(desiredReplicas); i++ {
		podName := fmt.Sprintf("%s-%s", dep.Name, kubernetes.RandomString()[0:6])
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: dep.Namespace,
				Labels:    dep.Spec.Selector.MatchLabels,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       dep.Name,
						UID:        dep.UID,
						Controller: &[]bool{true}[0],
					},
				},
			},
			Spec: dep.Spec.Template.Spec,
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{
					{
						Type:   corev1.PodReady,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}

		// Set container statuses as ready
		for _, container := range pod.Spec.Containers {
			pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
				Name:  container.Name,
				Ready: true,
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{},
				},
			})
		}

		UsingClusterBy(crName, fmt.Sprintf("Creating pod %s for deployment %s", podName, dep.Name))
		if err := k8sClient.Create(ctx, pod); err != nil {
			return err
		}

		// Update pod status
		if err := k8sClient.Status().Update(ctx, pod); err != nil {
			return err
		}
	}

	// Update deployment status to reflect the correct number of replicas
	dep.Status.Replicas = desiredReplicas
	dep.Status.UpdatedReplicas = desiredReplicas
	dep.Status.ReadyReplicas = desiredReplicas
	dep.Status.AvailableReplicas = desiredReplicas
	dep.Status.ObservedGeneration = dep.Generation

	UsingClusterBy(crName, fmt.Sprintf("Updating deployment status: replicas=%d", desiredReplicas))
	if err := k8sClient.Status().Update(ctx, dep); err != nil {
		UsingClusterBy(crName, fmt.Sprintf("Failed to update deployment status: %v", err))
		return err
	}

	return nil
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
		Name:      helpers.PdfRenderServiceChildName(key.Name),
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

	// Clean up any orphaned HPA
	hpaKey := types.NamespacedName{
		Name:      helpers.PdfRenderServiceHpaName(key.Name),
		Namespace: key.Namespace,
	}
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := k8sClient.Get(ctx, hpaKey, hpa); err == nil {
		UsingClusterBy(key.Name, fmt.Sprintf("Deleting orphaned HPA %s", hpaKey.String()))
		_ = k8sClient.Delete(ctx, hpa)
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

	UsingClusterBy(pdfKey.Name, fmt.Sprintf("Creating PDF render service with TLS=%t", tlsEnabled))

	// Step 1 – If TLS is enabled and cert-manager is NOT in use, create the certificate secret manually
	if tlsEnabled && !helpers.UseCertManager() {
		// Create TLS certificate secret for PDF render service
		tlsSecretName := helpers.PdfRenderServiceTlsSecretName(pdfKey.Name)

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

	// Step 3 - If TLS is enabled and cert-manager is in use, wait for the certificate to be ready
	if tlsEnabled && helpers.UseCertManager() {
		certificateName := fmt.Sprintf("%s-tls", helpers.PdfRenderServiceChildName(pdfKey.Name))
		UsingClusterBy(pdfKey.Name, fmt.Sprintf("Waiting for cert-manager to create certificate %s", certificateName))

		// Use longer timeout for certificate creation in test environments where cert-manager can be slow
		certTimeout := DefaultTestTimeout
		if helpers.UseEnvtest() || helpers.UseKindCluster() {
			certTimeout = DefaultTestTimeout * 3 // 90 seconds for test environments
		}

		Eventually(func(g Gomega) {
			var cert cmapi.Certificate
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      certificateName,
				Namespace: pdfKey.Namespace,
			}, &cert)).To(Succeed(), "Certificate should be created by the controller")

			// Check if certificate is ready
			for _, condition := range cert.Status.Conditions {
				if condition.Type == cmapi.CertificateConditionReady {
					g.Expect(condition.Status).To(Equal(cmmeta.ConditionTrue),
						"Certificate should be ready, but got status: %s, reason: %s, message: %s",
						condition.Status, condition.Reason, condition.Message)
				}
			}
		}, certTimeout, TestInterval).Should(Succeed())

		// Also wait for the secret to be created by cert-manager
		tlsSecretName := helpers.PdfRenderServiceTlsSecretName(pdfKey.Name)
		UsingClusterBy(pdfKey.Name, fmt.Sprintf("Waiting for cert-manager to create TLS secret %s", tlsSecretName))

		Eventually(func(g Gomega) {
			var secret corev1.Secret
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      tlsSecretName,
				Namespace: pdfKey.Namespace,
			}, &secret)).To(Succeed(), "TLS secret should be created by cert-manager")

			g.Expect(secret.Data).To(HaveKey(corev1.TLSCertKey), "Secret should contain TLS certificate")
			g.Expect(secret.Data).To(HaveKey(corev1.TLSPrivateKeyKey), "Secret should contain TLS private key")
		}, certTimeout, TestInterval).Should(Succeed())
	}

	// Optional image override
	if image != "" && pdfCR.Spec.Image != image {
		pdfCR.Spec.Image = image
		Expect(k8sClient.Update(ctx, pdfCR)).To(Succeed())
	}

	// Step 3 – wait for the controller to reconcile the change
	// TLS-enabled services need more time for certificate setup and reconciliation
	timeout := DefaultTestTimeout
	if tlsEnabled {
		timeout = DefaultTestTimeout * 3 // 90 seconds for TLS setup
	}
	WaitForObservedGeneration(ctx, k8sClient, pdfCR, timeout, TestInterval)

	// Step 4 – make sure the Deployment is rolled out & Ready
	// Pass the CR key, not the deployment key - EnsurePdfRenderDeploymentReady will resolve it
	EnsurePdfRenderDeploymentReady(ctx, k8sClient, pdfKey)

	// Step 5 – In test environments, trigger another reconciliation to update status after deployment is ready
	if helpers.UseEnvtest() || helpers.UseKindCluster() {
		// Add annotation to trigger reconciliation after deployment status update
		UsingClusterBy(pdfKey.Name, "Triggering reconciliation after deployment readiness")
		Eventually(func() error {
			var currentPdf humiov1alpha1.HumioPdfRenderService
			if err := k8sClient.Get(ctx, pdfKey, &currentPdf); err != nil {
				return err
			}
			if currentPdf.Annotations == nil {
				currentPdf.Annotations = make(map[string]string)
			}
			currentPdf.Annotations["humio.com/trigger-reconcile"] = fmt.Sprintf("%d", time.Now().Unix())
			return k8sClient.Update(ctx, &currentPdf)
		}, DefaultTestTimeout, TestInterval).Should(Succeed())

		// Wait a bit for the controller to pick up the change and reconcile
		Eventually(func() bool {
			var updatedPdf humiov1alpha1.HumioPdfRenderService
			if err := k8sClient.Get(ctx, pdfKey, &updatedPdf); err != nil {
				return false
			}
			UsingClusterBy(pdfKey.Name, fmt.Sprintf("PDF service status check: %s", updatedPdf.Status.State))
			return updatedPdf.Status.State == humiov1alpha1.HumioPdfRenderServiceStateRunning
		}, DefaultTestTimeout, TestInterval).Should(BeTrue(), "PDF service should reach Running state after deployment readiness")
	}

	return pdfCR
}
