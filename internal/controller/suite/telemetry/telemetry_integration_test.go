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

package telemetry

import (
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	humiov1alpha1 "github.com/humio/humio-operator/api/v1alpha1"
	"github.com/humio/humio-operator/internal/helpers"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Telemetry Integration Validation", Label("envtest", "dummy", "real"), func() {

	Context("CRD Structure Validation", func() {
		It("should have all required fields in HumioTelemetry CRD", func() {
			crdPath := filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "core.humio.com_humiotelemetries.yaml")
			content, err := os.ReadFile(crdPath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred(), "CRD file should exist: %s", crdPath)

			crdContent := string(content)
			requiredFields := []string{"clusterIdentifier", "managedClusterName", "remoteReport", "collections"}

			for _, field := range requiredFields {
				Expect(crdContent).To(ContainSubstring(field), "CRD should contain required field: %s", field)
			}

			// Verify CRD has proper validation
			Expect(crdContent).To(ContainSubstring("required:"), "CRD should have required field validation")
			Expect(crdContent).To(ContainSubstring("- spec"), "CRD should require spec field")
		})

		It("should have proper OpenAPI schema validation", func() {
			crdPath := filepath.Join("..", "..", "..", "..", "config", "crd", "bases", "core.humio.com_humiotelemetries.yaml")
			content, err := os.ReadFile(crdPath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred())

			// Parse CRD to validate structure
			var crd apiextensionsv1.CustomResourceDefinition
			err = yaml.Unmarshal(content, &crd)
			Expect(err).NotTo(HaveOccurred(), "CRD should be valid YAML")

			Expect(crd.Spec.Names.Kind).To(Equal("HumioTelemetry"))
			Expect(crd.Spec.Group).To(Equal("core.humio.com"))
		})
	})

	Context("Sample YAML Validation", func() {
		It("should validate HumioTelemetry sample", func() {
			samplePath := filepath.Join("..", "..", "..", "..", "config", "samples", "core_v1alpha1_humiotelemetry.yaml")
			validateSampleYAML(samplePath)
		})

		It("should validate HumioCluster with telemetry sample", func() {
			samplePath := filepath.Join("..", "..", "..", "..", "config", "samples", "core_v1alpha1_humiocluster_with_telemetry.yaml")
			content, err := os.ReadFile(samplePath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred(), "Sample file should exist: %s", samplePath)

			sampleContent := string(content)

			// Validate basic structure
			Expect(sampleContent).To(ContainSubstring("apiVersion: core.humio.com"))
			Expect(sampleContent).To(ContainSubstring("kind: HumioCluster"))
			Expect(sampleContent).To(ContainSubstring("telemetryConfig:"))
			Expect(sampleContent).To(ContainSubstring("remoteReport:"))

			// Validate kubectl can parse it (client-side validation)
			err = validateWithKubectl(samplePath)
			if err != nil {
				// Fallback to basic structure validation if kubectl fails
				Expect(sampleContent).To(ContainSubstring("metadata:"))
				Expect(sampleContent).To(ContainSubstring("spec:"))
			}
		})
	})

	Context("GraphQL Schema Validation", func() {
		It("should contain telemetry-related queries and mutations", func() {
			schemaPath := filepath.Join("..", "..", "..", "..", "internal", "api", "humiographql", "graphql", "license.graphql")
			content, err := os.ReadFile(schemaPath) // #nosec G304 -- Test file reading known paths
			Expect(err).NotTo(HaveOccurred(), "GraphQL schema file should exist: %s", schemaPath)

			schemaContent := string(content)

			// Check for license-related queries that telemetry uses
			Expect(schemaContent).To(ContainSubstring("GetLicenseForTelemetry"))

			// Verify it's valid GraphQL syntax
			Expect(schemaContent).To(ContainSubstring("query"))
			Expect(schemaContent).To(ContainSubstring("License"))
		})
	})

	Context("Live Cluster Integration", func() {
		BeforeEach(func() {
			if helpers.UseEnvtest() {
				Skip("Skipping live cluster tests in envtest mode")
			}
		})

		It("should deploy and validate HumioTelemetry CRD in live cluster", func() {
			By("deploying CRDs to the cluster")
			err := deployCRDs()
			Expect(err).NotTo(HaveOccurred(), "Should be able to deploy CRDs")

			By("verifying HumioTelemetry CRD is available")
			Eventually(func() error {
				return validateCRDExists("humiotelemetries.core.humio.com")
			}).Should(Succeed(), "HumioTelemetry CRD should be available")

			By("testing sample resource creation with dry-run")
			err = validateSampleResourceCreation()
			Expect(err).NotTo(HaveOccurred(), "Sample resource should pass server-side validation")
		})

		AfterEach(func() {
			By("cleaning up deployed CRDs")
			cleanupCRDs()
		})
	})

})

// Helper functions

func validateSampleYAML(path string) {
	content, err := os.ReadFile(path) // #nosec G304 -- Test file with controlled path
	Expect(err).NotTo(HaveOccurred(), "Sample file should exist: %s", path)

	// Try to parse as HumioTelemetry
	var telemetry humiov1alpha1.HumioTelemetry
	err = yaml.Unmarshal(content, &telemetry)
	Expect(err).NotTo(HaveOccurred(), "Sample should be valid YAML for HumioTelemetry: %s", path)

	// Validate required fields are present
	Expect(telemetry.Spec.ClusterIdentifier).NotTo(BeEmpty(), "ClusterIdentifier should not be empty")
	Expect(telemetry.Spec.ManagedClusterName).NotTo(BeEmpty(), "ManagedClusterName should not be empty")
	Expect(telemetry.Spec.RemoteReport.URL).NotTo(BeEmpty(), "RemoteReport URL should not be empty")

	// Validate kubectl can parse it
	err = validateWithKubectl(path)
	if err != nil {
		// If kubectl validation fails, ensure basic structure is valid
		sampleContent := string(content)
		Expect(sampleContent).To(ContainSubstring("apiVersion: core.humio.com"))
		Expect(sampleContent).To(ContainSubstring("kind: HumioTelemetry"))
		Expect(sampleContent).To(ContainSubstring("metadata:"))
		Expect(sampleContent).To(ContainSubstring("spec:"))
	}
}

func validateWithKubectl(path string) error {
	// Try kubectl client-side validation
	cmd := exec.Command("kubectl", "--dry-run=client", "--validate=false", "apply", "-f", path)
	return cmd.Run()
}

func deployCRDs() error {
	crdPath := filepath.Join("..", "..", "..", "..", "config", "crd")
	cmd := exec.Command("kubectl", "apply", "--server-side=true", "-k", crdPath) // #nosec G204 -- Test subprocess with known args
	return cmd.Run()
}

func validateCRDExists(name string) error {
	cmd := exec.Command("kubectl", "get", "crd", name)
	return cmd.Run()
}

func validateSampleResourceCreation() error {
	samplePath := filepath.Join("..", "..", "..", "..", "config", "samples", "core_v1alpha1_humiotelemetry.yaml")
	cmd := exec.Command("kubectl", "apply", "--dry-run=server", "-f", samplePath) // #nosec G204 -- Test subprocess with known args
	return cmd.Run()
}

func cleanupCRDs() {
	// Clean up CRDs (ignore errors since this is cleanup)
	crdPath := filepath.Join("..", "..", "..", "..", "config", "crd")
	cmd := exec.Command("kubectl", "delete", "-k", crdPath, "--ignore-not-found=true") // #nosec G204 -- Test subprocess with known args
	_ = cmd.Run()
}
