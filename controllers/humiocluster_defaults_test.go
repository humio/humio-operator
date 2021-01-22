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

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/pkg/helpers"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
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

			setEnvironmentVariableDefaults(toCreate)
			numEnvVars := len(toCreate.Spec.EnvironmentVariables)
			Expect(numEnvVars).ToNot(BeNumerically("<", 2))
			Expect(toCreate.Spec.EnvironmentVariables).Should(ContainElements([]corev1.EnvVar{
				{
					Name:  "PUBLIC_URL",
					Value: "http://$(THIS_POD_IP):$(HUMIO_PORT)",
				},
			}))
			additionalEnvVar := corev1.EnvVar{
				Name:  "test",
				Value: "test",
			}
			appendEnvironmentVariableDefault(toCreate, additionalEnvVar)
			Expect(len(toCreate.Spec.EnvironmentVariables)).To(BeIdenticalTo(numEnvVars + 1))

			updatedPublicURL := corev1.EnvVar{
				Name:  "PUBLIC_URL",
				Value: "test",
			}

			appendEnvironmentVariableDefault(toCreate, updatedPublicURL)
			Expect(len(toCreate.Spec.EnvironmentVariables)).To(BeIdenticalTo(numEnvVars + 1))
		})
	})

	Context("Humio Cluster with overriding PUBLIC_URL", func() {
		It("Should handle cluster defaults correctly", func() {
			spec := humiov1alpha1.HumioClusterSpec{
				EnvironmentVariables: []corev1.EnvVar{
					{
						Name:  "PUBLIC_URL",
						Value: "test",
					},
				},

				TLS: &humiov1alpha1.HumioClusterTLSSpec{
					Enabled: helpers.BoolPtr(false),
				},
			}

			toCreate := &humiov1alpha1.HumioCluster{
				Spec: spec,
			}

			setEnvironmentVariableDefaults(toCreate)
			numEnvVars := len(toCreate.Spec.EnvironmentVariables)
			Expect(numEnvVars).ToNot(BeNumerically("<", 2))
			Expect(toCreate.Spec.EnvironmentVariables).Should(ContainElements([]corev1.EnvVar{
				{
					Name:  "PUBLIC_URL",
					Value: "test",
				},
			}))

			updatedPublicURL := corev1.EnvVar{
				Name:  "PUBLIC_URL",
				Value: "updated",
			}
			appendEnvironmentVariableDefault(toCreate, updatedPublicURL)
			Expect(toCreate.Spec.EnvironmentVariables).Should(ContainElements([]corev1.EnvVar{
				{
					Name:  "PUBLIC_URL",
					Value: "test",
				},
			}))
		})
	})

	Context("Humio Cluster Log4j Environment Variable", func() {
		It("Should contain legacy Log4J Environment Variable", func() {
			toCreate := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					Image: "humio/humio-core:1.18.1",
				},
			}

			setEnvironmentVariableDefaults(toCreate)
			Expect(toCreate.Spec.EnvironmentVariables).Should(ContainElements([]corev1.EnvVar{
				{
					Name:  "LOG4J_CONFIGURATION",
					Value: "log4j2-stdout-json.xml",
				},
			}))
		})

		It("Should contain supported Log4J Environment Variable", func() {
			toCreate := &humiov1alpha1.HumioCluster{
				Spec: humiov1alpha1.HumioClusterSpec{
					Image: "humio/humio-core:1.19.0",
				},
			}

			setEnvironmentVariableDefaults(toCreate)
			Expect(toCreate.Spec.EnvironmentVariables).Should(ContainElements([]corev1.EnvVar{
				{
					Name:  "HUMIO_LOG4J_CONFIGURATION",
					Value: "log4j2-stdout-json.xml",
				},
			}))
		})

		It("Should contain supported Log4J Environment Variable", func() {
			versions := []string{"1.20.1", "master", "latest"}
			for _, version := range versions {
				image := "humio/humio-core"
				if version != "" {
					image = strings.Join([]string{image, version}, ":")
				}
				toCreate := &humiov1alpha1.HumioCluster{
					Spec: humiov1alpha1.HumioClusterSpec{
						Image: image,
					},
				}

				setEnvironmentVariableDefaults(toCreate)
				Expect(toCreate.Spec.EnvironmentVariables).Should(ContainElements([]corev1.EnvVar{
					{
						Name:  "HUMIO_LOG4J_CONFIGURATION",
						Value: "log4j2-json-stdout.xml",
					},
				}))
			}
		})
	})
})
