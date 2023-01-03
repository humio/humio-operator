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

package controllers

import (
	"strings"
	"testing"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	"github.com/humio/humio-operator/pkg/kubernetes"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
			toCreate.Spec.EnvironmentVariables = AppendEnvVarToEnvVarsIfNotAlreadyPresent(toCreate.Spec.EnvironmentVariables,
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
			toCreate.Spec.EnvironmentVariables = AppendEnvVarToEnvVarsIfNotAlreadyPresent(toCreate.Spec.EnvironmentVariables,
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

			By("Confirming the humio node manager correctly overrides the PUBLIC_URL")
			toCreate.Spec.EnvironmentVariables = AppendEnvVarToEnvVarsIfNotAlreadyPresent(toCreate.Spec.EnvironmentVariables,
				corev1.EnvVar{
					Name:  "PUBLIC_URL",
					Value: "test",
				})
			hnp := NewHumioNodeManagerFromHumioCluster(toCreate)
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
			"no cpu resource settings, ephemeral disks and init container, using zk and version 1.56.3",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							Image: "humio/humio-core:1.56.3",
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "USING_EPHEMERAL_DISKS",
									Value: "true",
								},
								{
									Name:  "ZOOKEEPER_URL",
									Value: "dummy",
								},
							},
						},
					},
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
					"export ZONE=",
				},
				[]string{},
			},
		},
		{
			"no cpu resource settings, ephemeral disks and init container, without zk and version 1.70.0",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							Image: "humio/humio-core:1.70.0",
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
				[]string{
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
				},
			},
		},
		{
			"no cpu resource settings, ephemeral disks and init container, using zk and version 1.70.0",
			fields{
				&humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						HumioNodeSpec: humiov1alpha1.HumioNodeSpec{
							Image: "humio/humio-core:1.70.0",
							EnvironmentVariables: []corev1.EnvVar{
								{
									Name:  "USING_EPHEMERAL_DISKS",
									Value: "true",
								},
								{
									Name:  "ZOOKEEPER_URL",
									Value: "dummy",
								},
							},
						},
					},
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
								{
									Name:  "ZOOKEEPER_URL",
									Value: "dummy",
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
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
								{
									Name:  "ZOOKEEPER_URL",
									Value: "dummy",
								},
							},
							DisableInitContainer: true,
						},
					},
				},
				[]string{
					"export CORES=",
					"export HUMIO_OPTS=",
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
								{
									Name:  "ZOOKEEPER_URL",
									Value: "dummy",
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
				[]string{
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
				},
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
				[]string{
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
				},
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
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
									Name:  "ZOOKEEPER_URL",
									Value: "dummy",
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
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
									Name:  "ZOOKEEPER_URL",
									Value: "dummy",
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
				[]string{
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
				},
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
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
					"export ZOOKEEPER_PREFIX_FOR_NODE_UUID=",
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
