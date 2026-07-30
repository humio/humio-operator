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

package clusters

import (
	"context"
	"fmt"
	"time"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller"
	"github.com/humio/humio-operator/internal/controller/suite"
	"github.com/humio/humio-operator/internal/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("HumioCluster Dependency Check", Label("real"), func() {

	BeforeEach(func() {
		testHumioClient.ClearHumioClientConnections("")
	})

	AfterEach(func() {
		testHumioClient.ClearHumioClientConnections("")
	})

	Context("Humio Cluster with Kafka Dependency Check", func() {
		It("Should successfully pass Kafka dependency check before starting", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-kafka-dep-check",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = ptr.To(int32(1))
			toCreate.Spec.DependencyCheck = &humiov1alpha1.DependencyCheckConfig{
				Enforcement:          "required",
				TimeoutSeconds:       300,
				RetryIntervalSeconds: 5,
			}

			suite.UsingClusterBy(key.Name, "Creating cluster with Kafka dependency check enabled")
			ctx := context.Background()
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStateRunning, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			suite.UsingClusterBy(key.Name, "Verifying init container has dependency check configuration")
			clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
			Expect(clusterPods).To(HaveLen(int(*toCreate.Spec.NodeCount)))

			pod := clusterPods[0]
			initContainerIdx, err := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
			Expect(err).ToNot(HaveOccurred())

			initContainer := pod.Spec.InitContainers[initContainerIdx]

			// Verify MODE is set to init-with-checks
			modeEnv, found := getEnvVar(initContainer.Env, "MODE")
			Expect(found).To(BeTrue())
			Expect(modeEnv).To(Equal("init-with-checks"))

			// Verify dependency check is enabled
			depCheckEnabled, found := getEnvVar(initContainer.Env, "DEPENDENCY_CHECK_ENABLED")
			Expect(found).To(BeTrue())
			Expect(depCheckEnabled).To(Equal("true"))

			// Verify enforcement mode
			enforcement, found := getEnvVar(initContainer.Env, "DEPENDENCY_CHECK_ENFORCEMENT")
			Expect(found).To(BeTrue())
			Expect(enforcement).To(Equal("required"))

			// Verify Kafka check is auto-discovered and enabled
			checkKafka, found := getEnvVar(initContainer.Env, "CHECK_KAFKA")
			Expect(found).To(BeTrue())
			Expect(checkKafka).To(Equal("true"))

			// Verify Kafka servers are passed through
			kafkaServers, found := getEnvVar(initContainer.Env, "KAFKA_SERVERS")
			Expect(found).To(BeTrue())
			Expect(kafkaServers).To(Equal("humio-cp-kafka-0.humio-cp-kafka-headless.default:9092"))

			suite.UsingClusterBy(key.Name, "Verifying init container completed successfully (dependency check passed)")
			// If the pod is running, it means the init container completed successfully
			// which means the Kafka dependency check passed
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				if len(clusterPods) == 0 {
					return false
				}
				pod := clusterPods[0]
				return pod.Status.Phase == corev1.PodRunning
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "Checking init container logs for successful check")
			// Get init container logs to verify the check ran
			Eventually(func() string {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				if len(clusterPods) == 0 {
					return ""
				}
				pod := clusterPods[0]

				// Check init container status
				for _, initStatus := range pod.Status.InitContainerStatuses {
					if initStatus.Name == controller.InitContainerName {
						if initStatus.State.Terminated != nil && initStatus.State.Terminated.ExitCode == 0 {
							return fmt.Sprintf("Init container completed with exit code 0 at %v", initStatus.State.Terminated.FinishedAt)
						}
					}
				}
				return ""
			}, testTimeout, suite.TestInterval).ShouldNot(BeEmpty())
		})
	})

	Context("Humio Cluster with Failing Dependency Check", func() {
		It("Should block pod startup when Kafka is unreachable", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-failing-dep-check",
				Namespace: testProcessNamespace,
			}

			// Use a non-existent Kafka server to force failure
			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = ptr.To(int32(1))
			for i := range toCreate.Spec.EnvironmentVariables {
				if toCreate.Spec.EnvironmentVariables[i].Name == "KAFKA_SERVERS" {
					toCreate.Spec.EnvironmentVariables[i].Value = "non-existent-kafka-server:9092"
					break
				}
			}
			toCreate.Spec.DependencyCheck = &humiov1alpha1.DependencyCheckConfig{
				Enforcement:          "required",
				TimeoutSeconds:       60,
				RetryIntervalSeconds: 2,
			}

			suite.UsingClusterBy(key.Name, "Creating cluster with unreachable Kafka server")
			ctx := context.Background()
			// Use Pending state so CreateAndBootstrapCluster returns after creating the CR
			// without waiting for Running — the cluster will never reach Running because the
			// init container is intentionally blocked.
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStatePending, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			// Bootstrap token secret must exist before the controller can create pods.
			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, toCreate)

			suite.UsingClusterBy(key.Name, "Verifying pod is created but init container is blocked")
			verifyInitContainerBlocked(ctx, k8sClient, key, toCreate, testTimeout)
			suite.UsingClusterBy(key.Name, "Dependency check correctly blocked pod startup due to unreachable Kafka")
		})
	})

	Context("Humio Cluster with S3 Dependency Check Configuration", func() {
		It("Should configure S3 dependency check and block pod when bucket is inaccessible", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-s3-dep-check",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = ptr.To(int32(1))
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "S3_STORAGE_BUCKET",
					Value: "nonexistent-test-bucket-12345", // This bucket doesn't exist
				},
				corev1.EnvVar{
					Name:  "S3_STORAGE_REGION",
					Value: "us-east-1",
				},
				corev1.EnvVar{
					Name:  "S3_ACCESS_KEY_ID",
					Value: "test-access-key",
				},
				corev1.EnvVar{
					Name:  "S3_SECRET_ACCESS_KEY",
					Value: "test-secret-key",
				},
			)
			toCreate.Spec.DependencyCheck = &humiov1alpha1.DependencyCheckConfig{
				Enforcement:          "required",
				TimeoutSeconds:       60,
				RetryIntervalSeconds: 2,
			}

			suite.UsingClusterBy(key.Name, "Creating cluster with S3 dependency check configuration")
			ctx := context.Background()
			// Use Pending state — the cluster will never reach Running because the init
			// container is blocked by the failing S3 check.
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStatePending, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			// Bootstrap token secret must exist before the controller can create pods.
			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, toCreate)

			suite.UsingClusterBy(key.Name, "Verifying S3 dependency check configuration in init container")
			var clusterPods []corev1.Pod
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				return len(clusterPods)
			}, testTimeout, suite.TestInterval).Should(BeNumerically(">", 0))
			pod := clusterPods[0]
			initContainerIdx, err := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
			Expect(err).ToNot(HaveOccurred())

			initContainer := pod.Spec.InitContainers[initContainerIdx]

			// Verify S3 check is auto-discovered and enabled
			checkS3, found := getEnvVar(initContainer.Env, "CHECK_S3")
			Expect(found).To(BeTrue())
			Expect(checkS3).To(Equal("true"))

			// Verify S3 environment variables are passed through
			s3Bucket, found := getEnvVar(initContainer.Env, "S3_STORAGE_BUCKET")
			Expect(found).To(BeTrue())
			Expect(s3Bucket).To(Equal("nonexistent-test-bucket-12345"))

			s3Region, found := getEnvVar(initContainer.Env, "S3_STORAGE_REGION")
			Expect(found).To(BeTrue())
			Expect(s3Region).To(Equal("us-east-1"))

			s3AccessKey, found := getEnvVar(initContainer.Env, "S3_ACCESS_KEY_ID")
			Expect(found).To(BeTrue())
			Expect(s3AccessKey).To(Equal("test-access-key"))

			s3SecretKey, found := getEnvVar(initContainer.Env, "S3_SECRET_ACCESS_KEY")
			Expect(found).To(BeTrue())
			Expect(s3SecretKey).To(Equal("test-secret-key"))

			suite.UsingClusterBy(key.Name, "Verifying pod is blocked due to inaccessible S3 bucket")
			verifyInitContainerBlocked(ctx, k8sClient, key, toCreate, testTimeout)
			suite.UsingClusterBy(key.Name, "S3 permission check correctly blocked pod startup")
		})
	})

	Context("Humio Cluster with GCS Dependency Check Configuration", func() {
		It("Should configure GCS dependency check and block pod when bucket is inaccessible", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-gcs-dep-check",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = ptr.To(int32(1))
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "GCP_STORAGE_BUCKET",
					Value: "nonexistent-gcs-test-bucket-67890", // This bucket doesn't exist
				},
				corev1.EnvVar{
					Name:  "GOOGLE_APPLICATION_CREDENTIALS",
					Value: "/var/secrets/gcp/key.json", // Path doesn't exist
				},
			)
			toCreate.Spec.DependencyCheck = &humiov1alpha1.DependencyCheckConfig{
				Enforcement:          "required",
				TimeoutSeconds:       60,
				RetryIntervalSeconds: 2,
			}

			suite.UsingClusterBy(key.Name, "Creating cluster with GCS dependency check configuration")
			ctx := context.Background()
			// Use Pending state — the cluster will never reach Running because the init
			// container is blocked by the failing GCS check.
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStatePending, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			// Bootstrap token secret must exist before the controller can create pods.
			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, toCreate)

			suite.UsingClusterBy(key.Name, "Verifying GCS dependency check configuration in init container")
			var clusterPods []corev1.Pod
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				return len(clusterPods)
			}, testTimeout, suite.TestInterval).Should(BeNumerically(">", 0))
			pod := clusterPods[0]
			initContainerIdx, err := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
			Expect(err).ToNot(HaveOccurred())

			initContainer := pod.Spec.InitContainers[initContainerIdx]

			// Verify GCS check is auto-discovered and enabled
			checkGCS, found := getEnvVar(initContainer.Env, "CHECK_GCS")
			Expect(found).To(BeTrue())
			Expect(checkGCS).To(Equal("true"))

			// Verify GCS environment variables are passed through
			gcsBucket, found := getEnvVar(initContainer.Env, "GCP_STORAGE_BUCKET")
			Expect(found).To(BeTrue())
			Expect(gcsBucket).To(Equal("nonexistent-gcs-test-bucket-67890"))

			gcsCredsPath, found := getEnvVar(initContainer.Env, "GOOGLE_APPLICATION_CREDENTIALS")
			Expect(found).To(BeTrue())
			Expect(gcsCredsPath).To(Equal("/var/secrets/gcp/key.json"))

			suite.UsingClusterBy(key.Name, "Verifying pod is blocked due to inaccessible GCS bucket")
			verifyInitContainerBlocked(ctx, k8sClient, key, toCreate, testTimeout)
			suite.UsingClusterBy(key.Name, "GCS permission check correctly blocked pod startup")
		})
	})

	Context("Humio Cluster with All Three Dependency Checks", func() {
		It("Should configure all dependency checks (Kafka, S3, GCS) simultaneously via auto-discovery", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-all-dep-checks",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = ptr.To(int32(1))
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "S3_STORAGE_BUCKET",
					Value: "my-s3-bucket",
				},
				corev1.EnvVar{
					Name:  "S3_STORAGE_REGION",
					Value: "us-west-2",
				},
				corev1.EnvVar{
					Name:  "GCP_STORAGE_BUCKET",
					Value: "my-gcs-bucket",
				},
			)
			toCreate.Spec.DependencyCheck = &humiov1alpha1.DependencyCheckConfig{
				Enforcement:          "required",
				TimeoutSeconds:       300,
				RetryIntervalSeconds: 5,
			}

			suite.UsingClusterBy(key.Name, "Creating cluster with all three dependency checks auto-discovered")
			ctx := context.Background()
			// Use Pending state: S3 and GCS checks use fake bucket names and will
			// block pod startup in a kind environment, so the cluster never reaches Running.
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStatePending, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			// Bootstrap token secret must exist before the controller can create pods.
			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, toCreate)

			suite.UsingClusterBy(key.Name, "Verifying all dependency checks are configured in init container")
			var clusterPods []corev1.Pod
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				return len(clusterPods)
			}, testTimeout, suite.TestInterval).Should(BeNumerically(">", 0))
			pod := clusterPods[0]
			initContainerIdx, err := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
			Expect(err).ToNot(HaveOccurred())

			initContainer := pod.Spec.InitContainers[initContainerIdx]

			// Verify all three checks are auto-discovered and enabled
			checkKafka, found := getEnvVar(initContainer.Env, "CHECK_KAFKA")
			Expect(found).To(BeTrue())
			Expect(checkKafka).To(Equal("true"))

			checkS3, found := getEnvVar(initContainer.Env, "CHECK_S3")
			Expect(found).To(BeTrue())
			Expect(checkS3).To(Equal("true"))

			checkGCS, found := getEnvVar(initContainer.Env, "CHECK_GCS")
			Expect(found).To(BeTrue())
			Expect(checkGCS).To(Equal("true"))

			// Verify all environment variables are present
			_, found = getEnvVar(initContainer.Env, "KAFKA_SERVERS")
			Expect(found).To(BeTrue())
			_, found = getEnvVar(initContainer.Env, "S3_STORAGE_BUCKET")
			Expect(found).To(BeTrue())
			_, found = getEnvVar(initContainer.Env, "S3_STORAGE_REGION")
			Expect(found).To(BeTrue())
			_, found = getEnvVar(initContainer.Env, "GCP_STORAGE_BUCKET")
			Expect(found).To(BeTrue())

			// Verify enforcement mode
			enforcement, found := getEnvVar(initContainer.Env, "DEPENDENCY_CHECK_ENFORCEMENT")
			Expect(found).To(BeTrue())
			Expect(enforcement).To(Equal("required"))

			suite.UsingClusterBy(key.Name, "All three dependency checks configured successfully via auto-discovery")
		})
	})

	Context("Humio Cluster with Exclude list", func() {
		It("Should skip excluded check types even when trigger env vars are present", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-exclude-dep-check",
				Namespace: testProcessNamespace,
			}

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = ptr.To(int32(1))
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "S3_STORAGE_BUCKET",
					Value: "my-s3-bucket",
				},
				corev1.EnvVar{
					Name:  "S3_STORAGE_REGION",
					Value: "us-east-1",
				},
			)
			toCreate.Spec.DependencyCheck = &humiov1alpha1.DependencyCheckConfig{
				Enforcement:          "required",
				TimeoutSeconds:       300,
				RetryIntervalSeconds: 5,
				Exclude:              []humiov1alpha1.DependencyCheckType{"s3"},
			}

			suite.UsingClusterBy(key.Name, "Creating cluster with S3 excluded from dependency checks")
			ctx := context.Background()
			// Use Pending state since the cluster won't reach Running with invalid S3 config.
			// We only need to verify init container env vars, not a fully running cluster.
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStatePending, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)

			// Simulate bootstrap token so the controller proceeds to create pods
			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, toCreate)

			suite.UsingClusterBy(key.Name, "Waiting for pods to be created")
			var clusterPods []corev1.Pod
			Eventually(func() int {
				clusterPods, _ = kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				return len(clusterPods)
			}, testTimeout, time.Second*2).Should(BeNumerically(">=", int(*toCreate.Spec.NodeCount)))

			suite.UsingClusterBy(key.Name, "Verifying S3 check is excluded but Kafka is still present")

			pod := clusterPods[0]
			initContainerIdx, err := kubernetes.GetInitContainerIndexByName(pod, controller.InitContainerName)
			Expect(err).ToNot(HaveOccurred())

			initContainer := pod.Spec.InitContainers[initContainerIdx]

			// Kafka should still be auto-discovered (KAFKA_SERVERS is set by default)
			checkKafka, found := getEnvVar(initContainer.Env, "CHECK_KAFKA")
			Expect(found).To(BeTrue())
			Expect(checkKafka).To(Equal("true"))

			// S3 should be excluded despite env vars being present
			_, found = getEnvVar(initContainer.Env, "CHECK_S3")
			Expect(found).To(BeFalse())

			suite.UsingClusterBy(key.Name, "Exclude list correctly prevented S3 dependency check")
		})
	})

	Context("Humio Cluster with S3 Dependency Check - Successful", func() {
		It("Should successfully pass S3 dependency check with mock server", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-s3-success",
				Namespace: testProcessNamespace,
			}

			// Deploy mock HTTP server that returns 200 OK
			mockServerKey := types.NamespacedName{
				Name:      "mock-s3-server",
				Namespace: testProcessNamespace,
			}

			suite.UsingClusterBy(key.Name, "Deploying mock S3 server")
			mockServer := suite.ConstructMockHTTPServer(mockServerKey)
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, mockServer.Deployment)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mockServer.Service)).Should(Succeed())
			defer suite.CleanupMockServer(ctx, k8sClient, mockServer)

			// Wait for mock server to be ready
			suite.UsingClusterBy(key.Name, "Waiting for mock S3 server to be ready")
			waitForMockServerReady(ctx, k8sClient, mockServerKey, testTimeout)

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = ptr.To(int32(1))
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "S3_STORAGE_BUCKET",
					Value: "test-bucket", // Bucket name doesn't matter for mock
				},
				corev1.EnvVar{
					Name:  "S3_STORAGE_REGION",
					Value: "us-east-1",
				},
				corev1.EnvVar{
					Name:  "S3_STORAGE_ENDPOINT",
					Value: fmt.Sprintf("http://%s.%s.svc.cluster.local", mockServerKey.Name, mockServerKey.Namespace),
				},
				corev1.EnvVar{
					Name:  "S3_STORAGE_PATH_STYLE_ACCESS",
					Value: "true",
				},
				corev1.EnvVar{
					Name:  "S3_ACCESS_KEY_ID",
					Value: "test-key",
				},
				corev1.EnvVar{
					Name:  "S3_SECRET_ACCESS_KEY",
					Value: "test-secret",
				},
			)
			toCreate.Spec.DependencyCheck = &humiov1alpha1.DependencyCheckConfig{
				Enforcement:          "required",
				TimeoutSeconds:       120,
				RetryIntervalSeconds: 5,
			}

			suite.UsingClusterBy(key.Name, "Creating cluster with S3 dependency check pointing to mock server")
			// Use Pending state: the S3 env vars in EnvironmentVariables also configure LogScale's
			// S3 storage backend, which would crash against the http-echo mock server (it returns
			// plain text, not S3 XML). The test focus is that the dependency check init container
			// passes — LogScale reaching Running state is not required for that.
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStatePending, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, toCreate)

			suite.UsingClusterBy(key.Name, "Waiting for pod to reach Running state (checks should pass)")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				if len(clusterPods) == 0 {
					return false
				}
				return clusterPods[0].Status.Phase == corev1.PodRunning
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "S3 dependency check passed successfully with mock server")
		})
	})

	Context("Humio Cluster with GCS Dependency Check - Successful", func() {
		It("Should successfully pass GCS dependency check with mock server", func() {
			key := types.NamespacedName{
				Name:      "humiocluster-gcs-success",
				Namespace: testProcessNamespace,
			}

			// Deploy mock HTTP server that returns 200 OK
			mockServerKey := types.NamespacedName{
				Name:      "mock-gcs-server",
				Namespace: testProcessNamespace,
			}

			suite.UsingClusterBy(key.Name, "Deploying mock GCS server")
			mockServer := suite.ConstructMockHTTPServer(mockServerKey)
			ctx := context.Background()
			Expect(k8sClient.Create(ctx, mockServer.Deployment)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mockServer.Service)).Should(Succeed())
			defer suite.CleanupMockServer(ctx, k8sClient, mockServer)

			// Wait for mock server to be ready
			suite.UsingClusterBy(key.Name, "Waiting for mock GCS server to be ready")
			waitForMockServerReady(ctx, k8sClient, mockServerKey, testTimeout)

			toCreate := suite.ConstructBasicSingleNodeHumioCluster(key, true)
			toCreate.Spec.NodeCount = ptr.To(int32(1))
			toCreate.Spec.EnvironmentVariables = append(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "GCP_STORAGE_BUCKET",
					Value: "test-gcs-bucket",
				},
				corev1.EnvVar{
					Name:  "GCP_STORAGE_ENDPOINT_BASE",
					Value: fmt.Sprintf("http://%s.%s.svc.cluster.local", mockServerKey.Name, mockServerKey.Namespace),
				},
			)
			toCreate.Spec.DependencyCheck = &humiov1alpha1.DependencyCheckConfig{
				Enforcement:          "required",
				TimeoutSeconds:       120,
				RetryIntervalSeconds: 5,
			}

			suite.UsingClusterBy(key.Name, "Creating cluster with GCS dependency check pointing to mock server")
			// Use Pending state: GCP_STORAGE_BUCKET in EnvironmentVariables may configure LogScale's
			// GCS backend; the http-echo mock server does not serve valid GCS responses. The test
			// focus is that the dependency check init container passes.
			suite.CreateAndBootstrapCluster(ctx, k8sClient, testHumioClient, toCreate, true, humiov1alpha1.HumioClusterStatePending, testTimeout)
			defer suite.CleanupCluster(ctx, k8sClient, toCreate)
			suite.SimulateHumioBootstrapTokenCreatingSecretAndUpdatingStatus(ctx, key, k8sClient, testTimeout, toCreate)

			suite.UsingClusterBy(key.Name, "Waiting for pod to reach Running state (checks should pass)")
			Eventually(func() bool {
				clusterPods, _ := kubernetes.ListPods(ctx, k8sClient, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
				if len(clusterPods) == 0 {
					return false
				}
				return clusterPods[0].Status.Phase == corev1.PodRunning
			}, testTimeout, suite.TestInterval).Should(BeTrue())

			suite.UsingClusterBy(key.Name, "GCS dependency check passed successfully with mock server")
		})
	})
})

// getEnvVar returns the plain Value of the named environment variable and whether it was found.
// For entries that use ValueFrom (e.g. secretKeyRef), Value is empty string.
func getEnvVar(envVars []corev1.EnvVar, name string) (string, bool) {
	env := suite.FindEnvVar(envVars, name)
	if env == nil {
		return "", false
	}
	return env.Value, true
}

// verifyInitContainerBlocked asserts that a pod has been created and that its init
// container is blocked (not ready, phase not Running). Use this in tests where a
// failing dependency check is expected to prevent pod startup.
func verifyInitContainerBlocked(ctx context.Context, k8sC client.Client, key types.NamespacedName, toCreate *humiov1alpha1.HumioCluster, timeout time.Duration) {
	// Wait for the pod to be scheduled.
	var clusterPods []corev1.Pod
	Eventually(func() int {
		clusterPods, _ = kubernetes.ListPods(ctx, k8sC, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
		return len(clusterPods)
	}, timeout, suite.TestInterval).Should(Equal(int(*toCreate.Spec.NodeCount)))

	// Wait for the init container to appear in the pod status.
	var pod corev1.Pod
	Eventually(func() bool {
		clusterPods, _ = kubernetes.ListPods(ctx, k8sC, key.Namespace, controller.NewHumioNodeManagerFromHumioCluster(toCreate).GetPodLabels())
		if len(clusterPods) == 0 {
			return false
		}
		pod = clusterPods[0]
		for _, initStatus := range pod.Status.InitContainerStatuses {
			if initStatus.Name == controller.InitContainerName {
				return true
			}
		}
		return false
	}, timeout, suite.TestInterval).Should(BeTrue(), "init container should appear in pod status")

	// Pod must not have reached Running — the init container is blocking it.
	Expect(pod.Status.Phase).ToNot(Equal(corev1.PodRunning))

	initContainerFound := false
	for _, initStatus := range pod.Status.InitContainerStatuses {
		if initStatus.Name == controller.InitContainerName {
			initContainerFound = true
			if initStatus.State.Terminated != nil {
				Expect(initStatus.State.Terminated.ExitCode).ToNot(Equal(int32(0)))
			}
			Expect(initStatus.Ready).To(BeFalse())
		}
	}
	Expect(initContainerFound).To(BeTrue())
}

// waitForMockServerReady blocks until the first pod backing the mock server deployment
// transitions to PodRunning.
func waitForMockServerReady(ctx context.Context, k8sC client.Client, key types.NamespacedName, timeout time.Duration) {
	Eventually(func() bool {
		pods, _ := kubernetes.ListPods(ctx, k8sC, key.Namespace, map[string]string{"app": key.Name})
		if len(pods) == 0 {
			return false
		}
		return pods[0].Status.Phase == corev1.PodRunning
	}, timeout, suite.TestInterval).Should(BeTrue())
}
