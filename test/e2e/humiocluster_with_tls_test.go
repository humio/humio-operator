package e2e

import (
	goctx "context"
	"fmt"
	cmapi "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"

	corev1alpha1 "github.com/humio/humio-operator/pkg/apis/core/v1alpha1"
	"github.com/humio/humio-operator/pkg/kubernetes"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type humioClusterWithTLSTest struct {
	test              *testing.T
	cluster           *corev1alpha1.HumioCluster
	initialTLSEnabled bool
	updatedTLSEnabled bool
}

func newHumioClusterWithTLSTest(test *testing.T, clusterName, namespace string, initialTLSEnabled, updatedTLSEnabled bool) humioClusterTest {
	return &humioClusterWithTLSTest{
		test: test,
		cluster: &corev1alpha1.HumioCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace,
			},
			Spec: corev1alpha1.HumioClusterSpec{
				NodeCount: 2,
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
						Name:  "HUMIO_JVM_ARGS",
						Value: "-Xss2m -Xms256m -Xmx1536m -server -XX:+UseParallelOldGC -XX:+ScavengeBeforeFullGC -XX:+DisableExplicitGC -Dzookeeper.client.secure=false",
					},
				},
				ExtraKafkaConfigs: "security.protocol=PLAINTEXT",
			},
		},
		initialTLSEnabled: initialTLSEnabled,
		updatedTLSEnabled: updatedTLSEnabled,
	}
}

func (h *humioClusterWithTLSTest) Start(f *framework.Framework, ctx *framework.Context) error {
	cmapi.AddToScheme(f.Scheme)

	h.cluster.Spec.TLS = &corev1alpha1.HumioClusterTLSSpec{
		Enabled: &h.initialTLSEnabled,
	}
	h.cluster.Spec.EnvironmentVariables = append(h.cluster.Spec.EnvironmentVariables,
		corev1.EnvVar{
			Name:  "HUMIO_KAFKA_TOPIC_PREFIX",
			Value: h.cluster.Name,
		},
	)
	return f.Client.Create(goctx.TODO(), h.cluster, &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval})
}

func (h *humioClusterWithTLSTest) Update(f *framework.Framework) error {
	err := f.Client.Get(goctx.TODO(), types.NamespacedName{
		Namespace: h.cluster.Namespace,
		Name:      h.cluster.Name,
	}, h.cluster)
	if err != nil {
		return fmt.Errorf("could not get current cluster while updating: %s", err)
	}
	h.cluster.Spec.TLS.Enabled = &h.updatedTLSEnabled
	return f.Client.Update(goctx.TODO(), h.cluster)
}

func (h *humioClusterWithTLSTest) Teardown(f *framework.Framework) error {
	return f.Client.Delete(goctx.TODO(), h.cluster)
}

func (h *humioClusterWithTLSTest) Wait(f *framework.Framework) error {
	h.test.Log("waiting 30 seconds before we start checking resource states")
	time.Sleep(time.Second * 30)
	for start := time.Now(); time.Since(start) < timeout; {
		err := f.Client.Get(goctx.TODO(), types.NamespacedName{Name: h.cluster.ObjectMeta.Name, Namespace: h.cluster.ObjectMeta.Namespace}, h.cluster)
		if err != nil {
			h.test.Logf("could not get humio cluster: %s", err)
			time.Sleep(time.Second * 10)
			continue
		}

		h.test.Logf("cluster found to be in state: %s", h.cluster.Status.State)
		if h.cluster.Status.State == corev1alpha1.HumioClusterStateRunning {
			h.test.Logf("listing pods")
			foundPodList, err := kubernetes.ListPods(
				f.Client.Client,
				h.cluster.Namespace,
				kubernetes.MatchingLabelsForHumio(h.cluster.Name),
			)
			if err != nil {
				h.test.Logf("unable to list pods for cluster: %s", err)
				continue
			}
			if len(foundPodList) == 0 {
				h.test.Logf("no pods found")
				continue
			}

			h.test.Logf("found %d pods", len(foundPodList))

			// If any pod is currently being deleted, we need to wait.
			safeToContinue := true
			for _, pod := range foundPodList {
				h.test.Logf("checking pod: %s", pod.Name)
				if pod.DeletionTimestamp != nil {
					h.test.Logf("pod %s currently being deleted", pod.Name)
					safeToContinue = false
				}
				for _, containerStatus := range pod.Status.ContainerStatuses {
					if containerStatus.Ready != true {
						h.test.Logf("container status indicates it is NOT safe: %+v", containerStatus)
						safeToContinue = false
					}
				}
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady {
						if condition.Status == corev1.ConditionFalse {
							h.test.Logf("pod status indicates it is NOT safe: %+v", condition)
							safeToContinue = false
						}
					}
				}
			}
			if !safeToContinue {
				h.test.Logf("not safe to continue, waiting 10 seconds then checking again")
				time.Sleep(time.Second * 10)
				continue
			}

			// go through pods for the cluster and fail if EXTERNAL_URL is misconfigured
			h.test.Logf("no pod currently being deleted, continuing to check if the pods we found has the correct TLS configuration")
			for _, pod := range foundPodList {
				h.test.Logf("checking status of pod: %s", pod.Name)
				for _, container := range pod.Spec.Containers {
					if container.Name != "humio" {
						h.test.Logf("skipping container: %s", container.Name)
						continue
					}

					tlsSettingsFound := 0
					const tlsEnvVarsExpectedWhenEnabled = 6
					const tlsEnvVarsExpectedWhenDisabled = 0
					h.test.Logf("found humio container, checking if we have correct amount of TLS-specific configurations")

					for _, envVar := range container.Env {
						if envVar.Name == "EXTERNAL_URL" {
							if strings.HasPrefix(envVar.Value, "https://") {
								tlsSettingsFound++
							}
						}
						if strings.HasPrefix(envVar.Name, "TLS_") {
							// there are 5 of these right now: TLS_TRUSTSTORE_LOCATION, TLS_TRUSTSTORE_PASSWORD, TLS_KEYSTORE_LOCATION, TLS_KEYSTORE_PASSWORD, TLS_KEY_PASSWORD
							tlsSettingsFound++
						}
					}
					if *h.cluster.Spec.TLS.Enabled && tlsSettingsFound != tlsEnvVarsExpectedWhenEnabled {
						h.test.Logf("expected to find a total of %d TLS-related environment variables but only found: %d", tlsEnvVarsExpectedWhenEnabled, tlsSettingsFound)
						safeToContinue = false
					}
					if !*h.cluster.Spec.TLS.Enabled && tlsSettingsFound != tlsEnvVarsExpectedWhenDisabled {
						h.test.Logf("expected to find a total of %d TLS-related environment variables but only found: %d", tlsEnvVarsExpectedWhenDisabled, tlsSettingsFound)
						safeToContinue = false
					}
				}
			}
			if !safeToContinue {
				h.test.Logf("not safe to continue, waiting 10 seconds then checking again")
				time.Sleep(time.Second * 10)
				continue
			}

			// validate we have the expected amount of per-cluster TLS secrets
			foundSecretList, err := kubernetes.ListSecrets(f.Client.Client, h.cluster.Namespace, kubernetes.MatchingLabelsForHumio(h.cluster.Name))
			if err != nil {
				h.test.Logf("unable to list secrets: %s", err)
				continue
			}
			foundOpaqueTLSRelatedSecrets := 0
			for _, secret := range foundSecretList {
				if secret.Type != corev1.SecretTypeOpaque {
					continue
				}
				if secret.Name == fmt.Sprintf("%s-ca-keypair", h.cluster.Name) {
					foundOpaqueTLSRelatedSecrets++
				}
				if secret.Name == fmt.Sprintf("%s-keystore-passphrase", h.cluster.Name) {
					foundOpaqueTLSRelatedSecrets++
				}
			}
			if *h.cluster.Spec.TLS.Enabled == (foundOpaqueTLSRelatedSecrets == 0) {
				h.test.Logf("cluster TLS set to %+v, but found %d TLS-related secrets of type Opaque", *h.cluster.Spec.TLS.Enabled, foundOpaqueTLSRelatedSecrets)
				continue
			}
			if *h.cluster.Spec.TLS.Enabled && (foundOpaqueTLSRelatedSecrets != 2) {
				h.test.Logf("cluster TLS enabled but number of opaque TLS-related secrets is not correct, expected: %d, got: %d", 2, foundOpaqueTLSRelatedSecrets)
				continue
			}

			// validate we have the expected amount of per-node TLS secrets, because these secrets are created by cert-manager we cannot use our typical label selector
			foundSecretList, err = kubernetes.ListSecrets(f.Client.Client, h.cluster.Namespace, client.MatchingLabels{})
			if err != nil {
				h.test.Logf("unable to list secrets: %s", err)
				continue
			}
			foundTLSTypeSecrets := 0
			for _, secret := range foundSecretList {
				issuerName, found := secret.Annotations[cmapi.IssuerNameAnnotationKey]
				if !found || issuerName != h.cluster.Name {
					continue
				}
				if secret.Type == corev1.SecretTypeTLS {
					foundTLSTypeSecrets++
				}
			}
			if *h.cluster.Spec.TLS.Enabled == (foundTLSTypeSecrets == 0) {
				h.test.Logf("cluster TLS set to %+v, but found %d secrets of type TLS", *h.cluster.Spec.TLS.Enabled, foundTLSTypeSecrets)
				continue
			}
			if *h.cluster.Spec.TLS.Enabled && (foundTLSTypeSecrets != h.cluster.Spec.NodeCount+1) {
				// we expect one TLS secret per Humio node and one cluster-wide TLS secret
				h.test.Logf("cluster TLS enabled but number of secrets is not correct, expected: %d, got: %d", h.cluster.Spec.NodeCount+1, foundTLSTypeSecrets)
				continue
			}

			// validate we have the expected amount of Certificates
			foundCertificateList, err := kubernetes.ListCertificates(f.Client.Client, h.cluster.Namespace, kubernetes.MatchingLabelsForHumio(h.cluster.Name))
			if err != nil {
				h.test.Logf("unable to list certificates: %s", err)
				continue
			}
			if *h.cluster.Spec.TLS.Enabled == (len(foundCertificateList) == 0) {
				h.test.Logf("cluster TLS set to %+v, but found %d certificates", *h.cluster.Spec.TLS.Enabled, len(foundCertificateList))
				continue
			}
			if *h.cluster.Spec.TLS.Enabled && (len(foundCertificateList) != h.cluster.Spec.NodeCount+1) {
				// we expect one TLS certificate per Humio node and one cluster-wide certificate
				h.test.Logf("cluster TLS enabled but number of certificates is not correct, expected: %d, got: %d", h.cluster.Spec.NodeCount+1, len(foundCertificateList))
				continue
			}

			return nil
		}

		time.Sleep(time.Second * 10)
	}

	return fmt.Errorf("timed out waiting for cluster state to become correctly configured with TLS settings: %+v", h.cluster.Spec.TLS)
}
