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

package controller

import (
	"strings"
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/controller/versions"
	"github.com/humio/humio-operator/internal/helpers"
	"github.com/humio/humio-operator/internal/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

var _ = Describe("HumioCluster Defaults", func() {

	BeforeEach(func() {
		// failed test runs that don't clean up leave resources behind.

	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test

	})

	// Add Tests for OpenAPI validation (or additional CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Humio Cluster without initially specifying PUBLIC_URL", func() {
		It("Should handle cluster defaults correctly", func() {
			spec := humiov1alpha1.HumioClusterSpec{
				TLS: &humiov1alpha1.HumioClusterTLSSpec{
					Enabled: helpers.BoolPtr(false),
				},
			}

			toCreate := &humiov1alpha1.HumioCluster{
				Spec: spec,
			}

			By("Confirming the humio node manager configures default PUBLIC_URL")
			hnp := NewHumioNodeManagerFromHumioCluster(toCreate)
			Expect(hnp.GetEnvironmentVariables()).Should(ContainElements([]corev1.EnvVar{
				{
					Name:  "PUBLIC_URL",
					Value: "http://$(THIS_POD_IP):$(HUMIO_PORT)",
				},
			}))

			By("Confirming the humio node manager correctly returns a newly added unrelated environment variable")
			toCreate.Spec.EnvironmentVariables = hnp.AppendEnvVarToEnvVarsIfNotAlreadyPresent(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "test",
					Value: "test",
				},
			)
			hnp = NewHumioNodeManagerFromHumioCluster(toCreate)
			Expect(hnp.GetEnvironmentVariables()).To(ContainElement(
				corev1.EnvVar{
					Name:  "test",
					Value: "test",
				}),
			)

			By("Confirming the humio node manager correctly overrides the PUBLIC_URL")
			toCreate.Spec.EnvironmentVariables = hnp.AppendEnvVarToEnvVarsIfNotAlreadyPresent(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "PUBLIC_URL",
					Value: "test",
				})
			hnp = NewHumioNodeManagerFromHumioCluster(toCreate)
			Expect(hnp.GetEnvironmentVariables()).To(ContainElement(
				corev1.EnvVar{
					Name:  "PUBLIC_URL",
					Value: "test",
				}),
			)
		})
	})

	Context("Humio Cluster with overriding PUBLIC_URL", func() {
		It("Should handle cluster defaults correctly", func() {
			spec := humiov1alpha1.HumioClusterSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					EnvironmentVariables: []corev1.EnvVar{
						{
							Name:  "PUBLIC_URL",
							Value: "test",
						},
					},
				},

				TLS: &humiov1alpha1.HumioClusterTLSSpec{
					Enabled: helpers.BoolPtr(false),
				},
			}

			toCreate := &humiov1alpha1.HumioCluster{
				Spec: spec,
			}
			hnp := NewHumioNodeManagerFromHumioCluster(toCreate)

			By("Confirming the humio node manager correctly overrides the PUBLIC_URL")
			toCreate.Spec.EnvironmentVariables = hnp.AppendEnvVarToEnvVarsIfNotAlreadyPresent(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "PUBLIC_URL",
					Value: "test",
				})
			Expect(hnp.GetEnvironmentVariables()).To(ContainElement(
				corev1.EnvVar{
					Name:  "PUBLIC_URL",
					Value: "test",
				}),
			)

			By("Confirming the humio node manager correctly updates the PUBLIC_URL override")
			updatedEnvVars := make([]corev1.EnvVar, len(toCreate.Spec.EnvironmentVariables))
			for i, k := range toCreate.Spec.EnvironmentVariables {
				if k.Name == "PUBLIC_URL" {
					updatedEnvVars[i] = corev1.EnvVar{
						Name:  "PUBLIC_URL",
						Value: "updated",
					}
				} else {
					updatedEnvVars[i] = k
				}
			}
			toCreate.Spec.EnvironmentVariables = updatedEnvVars
			hnp = NewHumioNodeManagerFromHumioCluster(toCreate)
			Expect(hnp.GetEnvironmentVariables()).To(ContainElement(
				corev1.EnvVar{
					Name:  "PUBLIC_URL",
					Value: "updated",
				}),
			)
		})
	})

	Context("Humio Cluster Log4j Environment Variable", func() {
		It("Should contain supported Log4J Environment Variable", func() {
			versions := []string{"1.20.1", "master", "latest"}
			for _, version := range versions {
				image := "humio/humio-core"
				if version != "" {
					image = strings.Join([]string{image, version}, ":")
				}
				toCreate := &humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							Image: image,
						},
					},
				}

				hnp := NewHumioNodeManagerFromHumioCluster(toCreate)
				Expect(hnp.GetEnvironmentVariables()).Should(ContainElements([]corev1.EnvVar{
					{
						Name:  "HUMIO_LOG4J_CONFIGURATION",
						Value: "log4j2-json-stdout.xml",
					},
				}))
			}
		})
	})

	Context("When merging containers into pods", func() {
		It("Should correctly merge regular containers", func() {
			By("Merging a container into an empty pod")
			emptyPodSpec := &corev1.PodSpec{
				Containers: []corev1.Container{},
			}
			newContainer := corev1.Container{
				Name:  "test-container",
				Image: "test-image",
				Env: []corev1.EnvVar{
					{Name: "TEST_ENV", Value: "test-value"},
				},
			}
			result := MergeContainerIntoPod(emptyPodSpec, newContainer)
			Expect(result.Containers).To(HaveLen(1))
			Expect(result.Containers[0]).To(Equal(newContainer))

			By("Merging a container with an existing container")
			existingPodSpec := &corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test-container",
						Image: "old-image",
						Env: []corev1.EnvVar{
							{Name: "EXISTING_ENV", Value: "existing-value"},
						},
					},
				},
			}
			updatedContainer := corev1.Container{
				Name:  "test-container",
				Image: "new-image",
				Env: []corev1.EnvVar{
					{Name: "NEW_ENV", Value: "new-value"},
				},
			}
			result = MergeContainerIntoPod(existingPodSpec, updatedContainer)
			Expect(result.Containers).To(HaveLen(1))
			Expect(result.Containers[0].Image).To(Equal("new-image"))
			Expect(result.Containers[0].Env).To(ContainElements(
				corev1.EnvVar{Name: "EXISTING_ENV", Value: "existing-value"},
				corev1.EnvVar{Name: "NEW_ENV", Value: "new-value"},
			))
		})

		It("Should correctly merge init containers", func() {
			By("Merging an init container into an empty pod")
			emptyPodSpec := &corev1.PodSpec{
				InitContainers: []corev1.Container{},
			}
			newInitContainer := corev1.Container{
				Name:  "test-init-container",
				Image: "test-init-image",
				Env: []corev1.EnvVar{
					{Name: "TEST_INIT_ENV", Value: "test-init-value"},
				},
			}
			result := MergeInitContainerIntoPod(emptyPodSpec, newInitContainer)
			Expect(result.InitContainers).To(HaveLen(1))
			Expect(result.InitContainers[0]).To(Equal(newInitContainer))

			By("Merging an init container with an existing init container")
			existingPodSpec := &corev1.PodSpec{
				InitContainers: []corev1.Container{
					{
						Name:  "test-init-container",
						Image: "old-init-image",
						Env: []corev1.EnvVar{
							{Name: "EXISTING_INIT_ENV", Value: "existing-init-value"},
						},
					},
				},
			}
			updatedInitContainer := corev1.Container{
				Name:  "test-init-container",
				Image: "new-init-image",
				Env: []corev1.EnvVar{
					{Name: "NEW_INIT_ENV", Value: "new-init-value"},
				},
			}
			result = MergeInitContainerIntoPod(existingPodSpec, updatedInitContainer)
			Expect(result.InitContainers).To(HaveLen(1))
			Expect(result.InitContainers[0].Image).To(Equal("new-init-image"))
			Expect(result.InitContainers[0].Env).To(ContainElements(
				corev1.EnvVar{Name: "EXISTING_INIT_ENV", Value: "existing-init-value"},
				corev1.EnvVar{Name: "NEW_INIT_ENV", Value: "new-init-value"},
			))
		})
	})

	Context("Bootstrap Token Auto Create Defaults", func() {
		It("Should return true when BootstrapToken is nil", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					// BootstrapToken is nil
				},
			}
			Expect(bootstrapTokenAutoCreateOrDefault(hc)).To(BeTrue())
		})

		It("Should return true when AutoCreate is nil", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					BootstrapToken: &humiov1alpha1.HumioBootstrapTokenConfig{
						// AutoCreate is nil
					},
				},
			}
			Expect(bootstrapTokenAutoCreateOrDefault(hc)).To(BeTrue())
		})

		It("Should return true when AutoCreate is explicitly set to true", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					BootstrapToken: &humiov1alpha1.HumioBootstrapTokenConfig{
						AutoCreate: helpers.BoolPtr(true),
					},
				},
			}
			Expect(bootstrapTokenAutoCreateOrDefault(hc)).To(BeTrue())
		})

		It("Should return false when AutoCreate is explicitly set to false", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					BootstrapToken: &humiov1alpha1.HumioBootstrapTokenConfig{
						AutoCreate: helpers.BoolPtr(false),
					},
				},
			}
			Expect(bootstrapTokenAutoCreateOrDefault(hc)).To(BeFalse())
		})
	})
})

func Test_constructContainerArgs(t *testing.T) {
	type fields struct {
		humioCluster            *humiov1alpha1.HumioCluster
		expectedContainerArgs   []string
		unexpectedContainerArgs []string
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			"no cpu resource settings, ephemeral disks and init container",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "USING_EPHEMERAL_DISKS",
									Value: "true",
								},
							},
						},
					},
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZONE=",
				},
				[]string{},
			},
		},
		{
			"cpu resource settings, ephemeral disks and init container",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "USING_EPHEMERAL_DISKS",
									Value: "true",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
				[]string{
					"export ZONE=",
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
				},
			},
		},
		{
			"no cpu resource settings, ephemeral disks and init container disabled",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "USING_EPHEMERAL_DISKS",
									Value: "true",
								},
							},
							DisableInitContainer: true,
						},
					},
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
				},
				[]string{
					"export ZONE=",
				},
			},
		},
		{
			"cpu resource settings, ephemeral disks and init container disabled",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "USING_EPHEMERAL_DISKS",
									Value: "true",
								},
							},
							DisableInitContainer: true,
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
				[]string{},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZONE=",
				},
			},
		},
		{
			"no cpu resource settings, without ephemeral disks and init container",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{},
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZONE=",
				},
				[]string{},
			},
		},
		{
			"cpu resource settings, without ephemeral disks and init container",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
				[]string{
					"export ZONE=",
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
				},
			},
		},
		{
			"no cpu resource settings, without ephemeral disks and init container disabled",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							DisableInitContainer: true,
						},
					},
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
				},
				[]string{
					"export ZONE=",
				},
			},
		},
		{
			"cpu resource settings, without ephemeral disks and init container disabled",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							DisableInitContainer: true,
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
				[]string{},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZONE=",
				},
			},
		},
		{
			"cpu cores envvar, ephemeral disks and init container",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "USING_EPHEMERAL_DISKS",
									Value: "true",
								},
								{
									Name:  "CORES",
									Value: "1",
								},
							},
						},
					},
				},
				[]string{
					"export ZONE=",
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
				},
			},
		},
		{
			"cpu cores envvar, ephemeral disks and init container disabled",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "USING_EPHEMERAL_DISKS",
									Value: "true",
								},
								{
									Name:  "CORES",
									Value: "1",
								},
							},
							DisableInitContainer: true,
						},
					},
				},
				[]string{},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZONE=",
				},
			},
		},
		{
			"cpu cores envvar, without ephemeral disks and init container",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "CORES",
									Value: "1",
								},
							},
						},
					},
				},
				[]string{
					"export ZONE=",
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
				},
			},
		},
		{
			"cpu cores envvar, without ephemeral disks and init container disabled",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "CORES",
									Value: "1",
								},
							},
							DisableInitContainer: true,
						},
					},
				},
				[]string{},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZONE=",
				},
			},
		},
		{
			"cpu cores envvar and cpu resource settings",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "CORES",
									Value: "1",
								},
							},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI),
								},
							},
						},
					},
				},
				[]string{},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hnp := NewHumioNodeManagerFromHumioCluster(tt.fields.humioCluster)
			pod, _ := ConstructPod(hnp, "", &podAttachments{})
			humioIdx, _ := kubernetes.GetContainerIndexByName(*pod, HumioContainerName)

			got, _ := ConstructContainerArgs(hnp, pod.Spec.Containers[humioIdx].Env)
			for _, expected := range tt.fields.expectedContainerArgs {
				if !strings.Contains(got[1], expected) {
					t.Errorf("constructContainerArgs()[1] = %v, expected to find substring %v", got[1], expected)
				}
			}
			for _, unexpected := range tt.fields.unexpectedContainerArgs {
				if strings.Contains(got[1], unexpected) {
					t.Errorf("constructContainerArgs()[1] = %v, did not expect find substring %v", got[1], unexpected)
				}
			}
		})
	}
}

// Helper function to find environment variable by name
func findEnvVar(envVars []corev1.EnvVar, name string) *corev1.EnvVar {
	for i := range envVars {
		if envVars[i].Name == name {
			return &envVars[i]
		}
	}
	return nil
}

var _ = Describe("HumioCluster Dependency Check", func() {

	Context("Dependency Check Configuration", func() {
		It("Should copy DependencyCheck field in NewHumioNodeManagerFromHumioCluster", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       300,
							RetryIntervalSeconds: 5,
						},
					},
				},
			}

			By("Creating node manager from cluster")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			By("Verifying DependencyCheck field is copied")
			Expect(hnp.DependencyCheckEnabled()).To(BeTrue())
			Expect(hnp.GetDependencyCheckConfig()).NotTo(BeNil())
			Expect(hnp.GetDependencyCheckConfig().TimeoutSeconds).To(Equal(300))
			Expect(hnp.GetDependencyCheckConfig().RetryIntervalSeconds).To(Equal(5))
			Expect(hnp.GetDependencyCheckConfig().Enforcement).To(Equal("required"))
		})

		It("Should copy DependencyCheck field in NewHumioNodeManagerFromHumioNodePool", func() {
			hc := &humiov1alpha1.HumioCluster{}
			hnpSpec := &humiov1alpha1.HumioNodePoolSpec{
				HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
					DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
						Enforcement:          "advisory",
						TimeoutSeconds:       600,
						RetryIntervalSeconds: 10,
					},
				},
			}

			By("Creating node manager from node pool")
			hnp := NewHumioNodeManagerFromHumioNodePool(hc, hnpSpec)

			By("Verifying DependencyCheck field is copied")
			Expect(hnp.DependencyCheckEnabled()).To(BeTrue())
			Expect(hnp.GetDependencyCheckConfig()).NotTo(BeNil())
			Expect(hnp.GetDependencyCheckConfig().TimeoutSeconds).To(Equal(600))
			Expect(hnp.GetDependencyCheckConfig().RetryIntervalSeconds).To(Equal(10))
			Expect(hnp.GetDependencyCheckConfig().Enforcement).To(Equal("advisory"))
		})

		It("Should handle nil DependencyCheck (backward compatibility)", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						DependencyCheck: nil,
					},
				},
			}

			By("Creating node manager without dependency check config")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			By("Verifying dependency check is disabled")
			Expect(hnp.DependencyCheckEnabled()).To(BeFalse())
			Expect(hnp.GetDependencyCheckConfig()).To(BeNil())
		})

		It("Should treat enforcement=disabled as disabled", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement: "disabled",
						},
					},
				},
			}

			By("Creating node manager with disabled enforcement")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			By("Verifying dependency check is disabled")
			Expect(hnp.DependencyCheckEnabled()).To(BeFalse())
			Expect(hnp.GetDependencyCheckConfig().Enforcement).To(Equal("disabled"))
		})

		It("Should treat empty enforcement as enabled (defaults to required at runtime)", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{},
					},
				},
			}

			By("Creating node manager with empty enforcement")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)

			By("Verifying dependency check is enabled when enforcement is empty")
			Expect(hnp.DependencyCheckEnabled()).To(BeTrue())
			Expect(hnp.GetDependencyCheckConfig().Enforcement).To(Equal(""))
		})
	})

	Context("Init Container MODE Configuration", func() {
		It("Should use init mode when dependency check is disabled", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
					},
				},
			}

			By("Constructing pod without dependency check")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})

			By("Verifying pod construction succeeds")
			Expect(err).ToNot(HaveOccurred())
			Expect(pod).NotTo(BeNil())

			By("Verifying init container has MODE=init")
			Expect(pod.Spec.InitContainers).To(HaveLen(1))
			Expect(pod.Spec.InitContainers[0].Name).To(Equal(InitContainerName))

			modeEnv := findEnvVar(pod.Spec.InitContainers[0].Env, "MODE")
			Expect(modeEnv).NotTo(BeNil())
			Expect(modeEnv.Value).To(Equal("init"))
		})

		It("Should use init-with-checks mode when dependency check is enabled", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "KAFKA_SERVERS", Value: "kafka:9092"},
						},
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       300,
							RetryIntervalSeconds: 5,
						},
					},
				},
			}

			By("Constructing pod with dependency check")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})

			By("Verifying pod construction succeeds")
			Expect(err).ToNot(HaveOccurred())
			Expect(pod).NotTo(BeNil())

			By("Verifying init container has MODE=init-with-checks")
			Expect(pod.Spec.InitContainers).To(HaveLen(1))
			Expect(pod.Spec.InitContainers[0].Name).To(Equal(InitContainerName))

			modeEnv := findEnvVar(pod.Spec.InitContainers[0].Env, "MODE")
			Expect(modeEnv).NotTo(BeNil())
			Expect(modeEnv.Value).To(Equal("init-with-checks"))
		})
	})

	Context("Init Container Environment Variables - Auto Discovery", func() {
		It("Should auto-discover Kafka check from KAFKA_SERVERS env var", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "KAFKA_SERVERS", Value: "kafka-1:9092,kafka-2:9092"},
						},
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       300,
							RetryIntervalSeconds: 5,
						},
					},
				},
			}

			By("Constructing pod with Kafka env var present")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying Kafka check is auto-discovered")
			initEnv := pod.Spec.InitContainers[0].Env

			Expect(findEnvVar(initEnv, "DEPENDENCY_CHECK_ENABLED")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "DEPENDENCY_CHECK_ENABLED").Value).To(Equal("true"))

			Expect(findEnvVar(initEnv, "DEPENDENCY_CHECK_ENFORCEMENT")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "DEPENDENCY_CHECK_ENFORCEMENT").Value).To(Equal("required"))

			Expect(findEnvVar(initEnv, "CHECK_KAFKA")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "CHECK_KAFKA").Value).To(Equal("true"))

			Expect(findEnvVar(initEnv, "KAFKA_SERVERS")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "KAFKA_SERVERS").Value).To(Equal("kafka-1:9092,kafka-2:9092"))

			Expect(findEnvVar(initEnv, "DEPENDENCY_CHECK_TIMEOUT")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "DEPENDENCY_CHECK_TIMEOUT").Value).To(Equal("300"))

			Expect(findEnvVar(initEnv, "DEPENDENCY_CHECK_RETRY_INTERVAL")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "DEPENDENCY_CHECK_RETRY_INTERVAL").Value).To(Equal("5"))
		})

		It("Should auto-discover S3 check from S3_STORAGE_BUCKET env var", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "S3_STORAGE_BUCKET", Value: "my-bucket"},
							{Name: "S3_STORAGE_REGION", Value: "us-west-2"},
							{Name: "S3_STORAGE_ENDPOINT", Value: "https://s3.amazonaws.com"},
							{Name: "S3_ACCESS_KEY_ID", Value: "test-key"},
							{Name: "S3_SECRET_ACCESS_KEY", Value: "test-secret"},
						},
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       300,
							RetryIntervalSeconds: 5,
						},
					},
				},
			}

			By("Constructing pod with S3 env vars present")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying S3 check is auto-discovered")
			initEnv := pod.Spec.InitContainers[0].Env

			Expect(findEnvVar(initEnv, "CHECK_S3")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "CHECK_S3").Value).To(Equal("true"))

			Expect(findEnvVar(initEnv, "S3_STORAGE_BUCKET")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "S3_STORAGE_BUCKET").Value).To(Equal("my-bucket"))

			Expect(findEnvVar(initEnv, "S3_STORAGE_REGION")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "S3_ACCESS_KEY_ID")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "S3_SECRET_ACCESS_KEY")).NotTo(BeNil())
		})

		It("Should auto-discover GCS check from GCP_STORAGE_BUCKET env var", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "GCP_STORAGE_BUCKET", Value: "my-gcs-bucket"},
							{Name: "GOOGLE_APPLICATION_CREDENTIALS", Value: "/var/secrets/gcp/key.json"},
						},
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       300,
							RetryIntervalSeconds: 5,
						},
					},
				},
			}

			By("Constructing pod with GCS env vars present")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying GCS check is auto-discovered")
			initEnv := pod.Spec.InitContainers[0].Env

			Expect(findEnvVar(initEnv, "CHECK_GCS")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "CHECK_GCS").Value).To(Equal("true"))

			Expect(findEnvVar(initEnv, "GCP_STORAGE_BUCKET")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "GCP_STORAGE_BUCKET").Value).To(Equal("my-gcs-bucket"))

			Expect(findEnvVar(initEnv, "GOOGLE_APPLICATION_CREDENTIALS")).NotTo(BeNil())
		})

		It("Should auto-discover multiple checks simultaneously", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "KAFKA_SERVERS", Value: "kafka:9092"},
							{Name: "S3_STORAGE_BUCKET", Value: "bucket"},
							{Name: "S3_STORAGE_REGION", Value: "us-east-1"},
							{Name: "GCP_STORAGE_BUCKET", Value: "gcs-bucket"},
						},
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       600,
							RetryIntervalSeconds: 10,
						},
					},
				},
			}

			By("Constructing pod with all check env vars present")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying all check types are auto-discovered")
			initEnv := pod.Spec.InitContainers[0].Env

			Expect(findEnvVar(initEnv, "CHECK_KAFKA")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "CHECK_S3")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "CHECK_GCS")).NotTo(BeNil())

			Expect(findEnvVar(initEnv, "KAFKA_SERVERS").Value).To(Equal("kafka:9092"))
			Expect(findEnvVar(initEnv, "S3_STORAGE_BUCKET").Value).To(Equal("bucket"))
			Expect(findEnvVar(initEnv, "GCP_STORAGE_BUCKET").Value).To(Equal("gcs-bucket"))
		})

		It("Should exclude checks listed in Exclude", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "KAFKA_SERVERS", Value: "kafka:9092"},
							{Name: "S3_STORAGE_BUCKET", Value: "bucket"},
							{Name: "S3_STORAGE_REGION", Value: "us-east-1"},
						},
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       300,
							RetryIntervalSeconds: 5,
							Exclude:              []humiov1alpha1.DependencyCheckType{"s3"},
						},
					},
				},
			}

			By("Constructing pod with S3 excluded")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying Kafka check is present but S3 check is excluded")
			initEnv := pod.Spec.InitContainers[0].Env

			Expect(findEnvVar(initEnv, "CHECK_KAFKA")).NotTo(BeNil())
			Expect(findEnvVar(initEnv, "CHECK_S3")).To(BeNil())
		})
	})

	Context("Dependency Check secretKeyIndex", func() {
		It("Should use secretKeyRef for env vars sourced from a Secret", func() {
			secretName := "my-s3-secret"
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       300,
							RetryIntervalSeconds: 5,
						},
					},
				},
			}

			// S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY come from a Secret; bucket is a plain env var.
			mainEnv := []corev1.EnvVar{
				{Name: "S3_STORAGE_BUCKET", Value: "my-bucket"},
				{Name: "S3_STORAGE_REGION", Value: "us-east-1"},
			}
			secretKeyIdx := map[string]string{
				"S3_ACCESS_KEY_ID":     secretName,
				"S3_SECRET_ACCESS_KEY": secretName,
			}

			By("Calling configureDependencyCheckEnv directly")
			envVars := configureDependencyCheckEnv(hc.Spec.DependencyCheck, mainEnv, secretKeyIdx, nil)

			By("Verifying S3_STORAGE_BUCKET is a plain value")
			bucket := findEnvVar(envVars, "S3_STORAGE_BUCKET")
			Expect(bucket).NotTo(BeNil())
			Expect(bucket.Value).To(Equal("my-bucket"))
			Expect(bucket.ValueFrom).To(BeNil())

			By("Verifying S3_ACCESS_KEY_ID uses secretKeyRef pointing to the correct secret")
			accessKey := findEnvVar(envVars, "S3_ACCESS_KEY_ID")
			Expect(accessKey).NotTo(BeNil())
			Expect(accessKey.Value).To(BeEmpty())
			Expect(accessKey.ValueFrom).NotTo(BeNil())
			Expect(accessKey.ValueFrom.SecretKeyRef).NotTo(BeNil())
			Expect(accessKey.ValueFrom.SecretKeyRef.Name).To(Equal(secretName))
			Expect(accessKey.ValueFrom.SecretKeyRef.Key).To(Equal("S3_ACCESS_KEY_ID"))

			By("Verifying S3_SECRET_ACCESS_KEY uses secretKeyRef pointing to the correct secret")
			secretKey := findEnvVar(envVars, "S3_SECRET_ACCESS_KEY")
			Expect(secretKey).NotTo(BeNil())
			Expect(secretKey.Value).To(BeEmpty())
			Expect(secretKey.ValueFrom).NotTo(BeNil())
			Expect(secretKey.ValueFrom.SecretKeyRef).NotTo(BeNil())
			Expect(secretKey.ValueFrom.SecretKeyRef.Name).To(Equal(secretName))
			Expect(secretKey.ValueFrom.SecretKeyRef.Key).To(Equal("S3_SECRET_ACCESS_KEY"))

			By("Verifying global timeout env vars are emitted")
			timeout := findEnvVar(envVars, "DEPENDENCY_CHECK_TIMEOUT")
			Expect(timeout).NotTo(BeNil())
			Expect(timeout.Value).To(Equal("300"))

			retryInterval := findEnvVar(envVars, "DEPENDENCY_CHECK_RETRY_INTERVAL")
			Expect(retryInterval).NotTo(BeNil())
			Expect(retryInterval.Value).To(Equal("5"))
		})

		It("Should forward ValueFrom entries from EnvironmentVariables as-is to the init container", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						EnvironmentVariables: []corev1.EnvVar{
							{
								Name: "S3_ACCESS_KEY_ID",
								ValueFrom: &corev1.EnvVarSource{
									SecretKeyRef: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{Name: "my-creds-secret"},
										Key:                  "accessKeyId",
									},
								},
							},
							{Name: "S3_STORAGE_BUCKET", Value: "my-bucket"},
							{Name: "S3_STORAGE_REGION", Value: "us-east-1"},
						},
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement: "required",
						},
					},
				},
			}

			By("Calling configureDependencyCheckEnv with a ValueFrom entry in mainEnv")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			envVars := configureDependencyCheckEnv(hc.Spec.DependencyCheck, hnp.GetEnvironmentVariables(), nil, nil)

			By("Verifying S3_ACCESS_KEY_ID is forwarded with its ValueFrom intact")
			accessKey := findEnvVar(envVars, "S3_ACCESS_KEY_ID")
			Expect(accessKey).NotTo(BeNil(), "S3_ACCESS_KEY_ID should be present in init container env")
			Expect(accessKey.ValueFrom).NotTo(BeNil(), "S3_ACCESS_KEY_ID should retain its ValueFrom source")
			Expect(accessKey.ValueFrom.SecretKeyRef).NotTo(BeNil())
			Expect(accessKey.ValueFrom.SecretKeyRef.Name).To(Equal("my-creds-secret"))
			Expect(accessKey.ValueFrom.SecretKeyRef.Key).To(Equal("accessKeyId"))

			By("Verifying plain-value env vars are still forwarded correctly")
			bucket := findEnvVar(envVars, "S3_STORAGE_BUCKET")
			Expect(bucket).NotTo(BeNil())
			Expect(bucket.Value).To(Equal("my-bucket"))
		})

		It("Should not emit DEPENDENCY_CHECK_TIMEOUT when TimeoutSeconds is zero", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement: "required",
							// TimeoutSeconds and RetryIntervalSeconds deliberately left as zero
						},
					},
				},
			}

			mainEnv := []corev1.EnvVar{{Name: "KAFKA_SERVERS", Value: "kafka:9092"}}

			By("Calling configureDependencyCheckEnv with zero timeout")
			envVars := configureDependencyCheckEnv(hc.Spec.DependencyCheck, mainEnv, nil, nil)

			By("Verifying DEPENDENCY_CHECK_TIMEOUT is absent so the helper uses its built-in default")
			Expect(findEnvVar(envVars, "DEPENDENCY_CHECK_TIMEOUT")).To(BeNil())
			Expect(findEnvVar(envVars, "DEPENDENCY_CHECK_RETRY_INTERVAL")).To(BeNil())
		})
	})

	Context("EnvVarSourceData Discovery", func() {
		It("Should discover checks when trigger env vars come from envVarSourceData", func() {
			depConfig := &humiov1alpha1.DependencyCheckConfig{
				Enforcement: "required",
			}
			mainEnv := []corev1.EnvVar{} // empty — Kafka comes from envVarSourceData
			envVarSourceData := &map[string]string{"KAFKA_SERVERS": "kafka:9092"}

			envVars := configureDependencyCheckEnv(depConfig, mainEnv, nil, envVarSourceData)

			checkKafka := findEnvVar(envVars, "CHECK_KAFKA")
			Expect(checkKafka).NotTo(BeNil())
			Expect(checkKafka.Value).To(Equal("true"))

			kafkaServers := findEnvVar(envVars, "KAFKA_SERVERS")
			Expect(kafkaServers).NotTo(BeNil())
			Expect(kafkaServers.Value).To(Equal("kafka:9092"))
		})

		It("Should discover S3 with bucket from envVarSourceData and region from mainEnv", func() {
			depConfig := &humiov1alpha1.DependencyCheckConfig{
				Enforcement: "required",
			}
			mainEnv := []corev1.EnvVar{
				{Name: "S3_STORAGE_REGION", Value: "us-west-2"},
			}
			envVarSourceData := &map[string]string{"S3_STORAGE_BUCKET": "my-bucket"}

			envVars := configureDependencyCheckEnv(depConfig, mainEnv, nil, envVarSourceData)

			checkS3 := findEnvVar(envVars, "CHECK_S3")
			Expect(checkS3).NotTo(BeNil())
			Expect(checkS3.Value).To(Equal("true"))

			bucket := findEnvVar(envVars, "S3_STORAGE_BUCKET")
			Expect(bucket).NotTo(BeNil())
			Expect(bucket.Value).To(Equal("my-bucket"))

			region := findEnvVar(envVars, "S3_STORAGE_REGION")
			Expect(region).NotTo(BeNil())
			Expect(region.Value).To(Equal("us-west-2"))
		})
	})

	Context("ValidateExcludeList", func() {
		It("Should accept valid exclude values", func() {
			Expect(ValidateExcludeList([]humiov1alpha1.DependencyCheckType{"kafka", "s3", "gcs"})).To(BeEmpty())
		})

		It("Should reject invalid exclude values", func() {
			Expect(ValidateExcludeList([]humiov1alpha1.DependencyCheckType{"kafka", "invalid", "redis"})).To(ConsistOf(humiov1alpha1.DependencyCheckType("invalid"), humiov1alpha1.DependencyCheckType("redis")))
		})

		It("Should handle empty exclude list", func() {
			Expect(ValidateExcludeList([]humiov1alpha1.DependencyCheckType{})).To(BeEmpty())
		})

		It("Should handle nil exclude list", func() {
			Expect(ValidateExcludeList(nil)).To(BeEmpty())
		})
	})

	Context("Init Container Resource Limits", func() {
		It("Should use base resource limits without dependency checks", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
					},
				},
			}

			By("Constructing pod without dependency check")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying init container has base resource limits")
			initContainer := pod.Spec.InitContainers[0]

			// Base limits: 100m CPU, 50MB RAM
			cpuLimit := initContainer.Resources.Limits[corev1.ResourceCPU]
			Expect(cpuLimit.MilliValue()).To(Equal(int64(100)))

			memLimit := initContainer.Resources.Limits[corev1.ResourceMemory]
			Expect(memLimit.Value()).To(Equal(int64(50 * 1024 * 1024)))
		})

		It("Should increase resource limits with dependency checks enabled", func() {
			hc := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
						Image: versions.DefaultHumioImageVersion(),
						EnvironmentVariables: []corev1.EnvVar{
							{Name: "KAFKA_SERVERS", Value: "kafka:9092"},
						},
						DependencyCheck: &humiov1alpha1.DependencyCheckConfig{
							Enforcement:          "required",
							TimeoutSeconds:       300,
							RetryIntervalSeconds: 5,
						},
					},
				},
			}

			By("Constructing pod with dependency check")
			hnp := NewHumioNodeManagerFromHumioCluster(hc)
			pod, err := ConstructPod(hnp, "", &podAttachments{})
			Expect(err).ToNot(HaveOccurred())

			By("Verifying init container has increased resource limits")
			initContainer := pod.Spec.InitContainers[0]

			// Enhanced limits: 200m CPU, 128MB RAM
			cpuLimit := initContainer.Resources.Limits[corev1.ResourceCPU]
			Expect(cpuLimit.MilliValue()).To(Equal(int64(200)))

			memLimit := initContainer.Resources.Limits[corev1.ResourceMemory]
			Expect(memLimit.Value()).To(Equal(int64(128 * 1024 * 1024)))
		})
	})
})

func TestGetNodeCount_NilPointer(t *testing.T) {
	tests := []struct {
		name     string
		spec     humiov1alpha1.HumioNodeSpec
		expected int
	}{
		{
			name:     "explicit value",
			spec:     humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(5))},
			expected: 5,
		},
		{
			name:     "explicit zero",
			spec:     humiov1alpha1.HumioNodeSpec{NodeCount: ptr.To(int32(0))},
			expected: 0,
		},
		{
			name:     "nil default",
			spec:     humiov1alpha1.HumioNodeSpec{NodeCount: nil},
			expected: 2,
		},
		{
			name: "nil with autoscaling min",
			spec: humiov1alpha1.HumioNodeSpec{
				NodeCount:   nil,
				Autoscaling: &humiov1alpha1.AutoscalingSpec{MinReplicas: ptr.To(int32(3)), MaxReplicas: 10},
			},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hnp := &HumioNodePool{humioNodeSpec: tt.spec}
			result := hnp.GetNodeCount()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoleConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"NodeRoleIngestOnly matches ingestonly", NodeRoleIngestOnly, "ingestonly"},
		{"NodeRoleLightweightIngestOnly matches lightweightingestonly", NodeRoleLightweightIngestOnly, "lightweightingestonly"},
		{"EnvVarNodeRoles matches NODE_ROLES", EnvVarNodeRoles, "NODE_ROLES"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("got %q, want %q", tt.constant, tt.expected)
			}
		})
	}
}

func TestGetNodePoolFeatureAllowedAPIRequestTypes_IngestOnly(t *testing.T) {
	hnp := &HumioNodePool{
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			EnvironmentVariables: []corev1.EnvVar{
				{Name: "NODE_ROLES", Value: "ingestonly"},
			},
			NodePoolFeatures: humiov1alpha1.HumioNodePoolFeatures{
				AllowedAPIRequestTypes: nil,
			},
		},
	}
	result := hnp.GetNodePoolFeatureAllowedAPIRequestTypes()
	if len(result) != 0 {
		t.Errorf("got %v, want empty slice for ingestonly", result)
	}
}

func TestGetNodePoolFeatureAllowedAPIRequestTypes_LightweightIngestOnly(t *testing.T) {
	hnp := &HumioNodePool{
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			EnvironmentVariables: []corev1.EnvVar{
				{Name: "NODE_ROLES", Value: "lightweightingestonly"},
			},
			NodePoolFeatures: humiov1alpha1.HumioNodePoolFeatures{
				AllowedAPIRequestTypes: nil,
			},
		},
	}
	result := hnp.GetNodePoolFeatureAllowedAPIRequestTypes()
	if len(result) != 0 {
		t.Errorf("got %v, want empty slice for lightweightingestonly", result)
	}
}

func TestGetNodePoolFeatureAllowedAPIRequestTypes_NonIngest(t *testing.T) {
	tests := []struct {
		name      string
		nodeRoles string
		envVars   []corev1.EnvVar
	}{
		{"empty NODE_ROLES", "", []corev1.EnvVar{{Name: "NODE_ROLES", Value: ""}}},
		{"all role", "all", []corev1.EnvVar{{Name: "NODE_ROLES", Value: "all"}}},
		{"httponly role", "httponly", []corev1.EnvVar{{Name: "NODE_ROLES", Value: "httponly"}}},
		{"NODE_ROLES unset", "", []corev1.EnvVar{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hnp := &HumioNodePool{
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{
					EnvironmentVariables: tt.envVars,
					NodePoolFeatures: humiov1alpha1.HumioNodePoolFeatures{
						AllowedAPIRequestTypes: nil,
					},
				},
			}
			result := hnp.GetNodePoolFeatureAllowedAPIRequestTypes()
			if len(result) != 1 || result[0] != "OperatorInternal" {
				t.Errorf("got %v, want [OperatorInternal] for %s", result, tt.name)
			}
		})
	}
}

func TestGetNodePoolFeatureAllowedAPIRequestTypes_ExplicitOverride(t *testing.T) {
	tests := []struct {
		name      string
		override  *[]string
		nodeRoles string
		expected  []string
	}{
		{
			"explicit OperatorInternal on ingest-only",
			&[]string{"OperatorInternal"},
			"ingestonly",
			[]string{"OperatorInternal"},
		},
		{
			"explicit empty on query-capable",
			&[]string{},
			"",
			[]string{},
		},
		{
			"explicit custom on httponly",
			&[]string{"Custom"},
			"httponly",
			[]string{"Custom"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hnp := &HumioNodePool{
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{
					EnvironmentVariables: []corev1.EnvVar{
						{Name: "NODE_ROLES", Value: tt.nodeRoles},
					},
					NodePoolFeatures: humiov1alpha1.HumioNodePoolFeatures{
						AllowedAPIRequestTypes: tt.override,
					},
				},
			}
			result := hnp.GetNodePoolFeatureAllowedAPIRequestTypes()
			if !slicesEqual(result, tt.expected) {
				t.Errorf("got %v, want %v", result, tt.expected)
			}
		})
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGetNodePoolFeatureAllowedAPIRequestTypes_ValueFrom(t *testing.T) {
	// [DESIGN F-001] ValueFrom not resolved — EnvVarValue returns ""
	// Ingest-only pool using ValueFrom gets OperatorInternal (broken behavior)
	// User MUST set AllowedAPIRequestTypes: [] explicitly to exclude from service
	hnp := &HumioNodePool{
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			EnvironmentVariables: []corev1.EnvVar{
				{
					Name: "NODE_ROLES",
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: "roles-config"},
							Key:                  "node_roles",
						},
					},
				},
			},
			NodePoolFeatures: humiov1alpha1.HumioNodePoolFeatures{
				AllowedAPIRequestTypes: nil,
			},
		},
	}
	result := hnp.GetNodePoolFeatureAllowedAPIRequestTypes()
	// EnvVarValue returns "" for ValueFrom → treated as unset → default included
	if len(result) != 1 || result[0] != "OperatorInternal" {
		t.Errorf("got %v, want [OperatorInternal] for ValueFrom NODE_ROLES", result)
	}
}

func TestGetWorkloadTypes_AutoDerive(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected []string
	}{
		{"all role", "all", []string{"digest", "ingest"}},
		{"empty/unset", "", []string{"digest", "ingest"}},
		{"ingestonly", "ingestonly", []string{"ingest"}},
		{"lightweightingestonly", "lightweightingestonly", []string{"ingest"}},
		{"httponly", "httponly", []string{"ingest"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var envVars []corev1.EnvVar
			if tt.envValue != "" {
				envVars = []corev1.EnvVar{{Name: "NODE_ROLES", Value: tt.envValue}}
			}
			hnp := &HumioNodePool{
				humioNodeSpec: humiov1alpha1.HumioNodeSpec{
					EnvironmentVariables: envVars,
				},
			}
			result := hnp.GetWorkloadTypes()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetWorkloadTypes_ExplicitOverride(t *testing.T) {
	override := []string{"custom-type"}
	hnp := &HumioNodePool{
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			EnvironmentVariables: []corev1.EnvVar{
				{Name: "NODE_ROLES", Value: "all"},
			},
			WorkloadTypes: &override,
		},
	}
	result := hnp.GetWorkloadTypes()
	assert.Equal(t, []string{"custom-type"}, result)
}

func TestGetPodLabels_WorkloadTypeLabels(t *testing.T) {
	hnp := &HumioNodePool{
		clusterName: "test-cluster",
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			EnvironmentVariables: []corev1.EnvVar{
				{Name: "NODE_ROLES", Value: "all"},
			},
		},
		workloadServicesEnabled: true,
	}
	labels := hnp.GetPodLabels()
	assert.Equal(t, "true", labels[kubernetes.WorkloadTypeLabelPrefix+"digest"])
	assert.Equal(t, "true", labels[kubernetes.WorkloadTypeLabelPrefix+"ingest"])
}

func TestGetPodLabels_NoWorkloadLabelsWhenDisabled(t *testing.T) {
	hnp := &HumioNodePool{
		clusterName: "test-cluster",
		humioNodeSpec: humiov1alpha1.HumioNodeSpec{
			EnvironmentVariables: []corev1.EnvVar{
				{Name: "NODE_ROLES", Value: "all"},
			},
		},
		workloadServicesEnabled: false,
	}
	labels := hnp.GetPodLabels()
	_, hasDigest := labels[kubernetes.WorkloadTypeLabelPrefix+"digest"]
	_, hasIngest := labels[kubernetes.WorkloadTypeLabelPrefix+"ingest"]
	assert.False(t, hasDigest, "should not have digest label when workload services disabled")
	assert.False(t, hasIngest, "should not have ingest label when workload services disabled")
}

func TestNodePoolServiceEnabled(t *testing.T) {
	t.Run("nil defaults to true", func(t *testing.T) {
		hnp := &HumioNodePool{humioNodeSpec: humiov1alpha1.HumioNodeSpec{}}
		assert.True(t, hnp.NodePoolServiceEnabled())
	})

	t.Run("explicit true", func(t *testing.T) {
		trueVal := true
		hnp := &HumioNodePool{humioNodeSpec: humiov1alpha1.HumioNodeSpec{EnableNodePoolService: &trueVal}}
		assert.True(t, hnp.NodePoolServiceEnabled())
	})

	t.Run("explicit false", func(t *testing.T) {
		falseVal := false
		hnp := &HumioNodePool{humioNodeSpec: humiov1alpha1.HumioNodeSpec{EnableNodePoolService: &falseVal}}
		assert.False(t, hnp.NodePoolServiceEnabled())
	})
}
