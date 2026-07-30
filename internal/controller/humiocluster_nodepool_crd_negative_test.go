package controller

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestHumioNodePoolCRDManifestGenerationNegative is a RED-phase test
// that would fail if the CRD manifest contained the INCORRECT nested path
// from the POC implementation (.spec.spec.nodeCount).
//
// This test documents the expected failure mode and serves as regression protection.
func TestHumioNodePoolCRDManifestGenerationNegative(t *testing.T) {
	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("failed to find project root: %v", err)
	}

	crdPath := filepath.Join(projectRoot, "config/crd/bases/core.humio.com_humionodepools.yaml")

	data, err := os.ReadFile(crdPath) //nolint:gosec // test reads from known project path
	if err != nil {
		t.Fatalf("failed to read CRD manifest at %s: %v", crdPath, err)
	}

	var crd map[string]interface{}
	if err := yaml.Unmarshal(data, &crd); err != nil {
		t.Fatalf("failed to parse CRD YAML: %v", err)
	}

	spec, ok := crd["spec"].(map[string]interface{})
	if !ok {
		t.Fatal("CRD missing 'spec' field")
	}

	versions, ok := spec["versions"].([]interface{})
	if !ok || len(versions) == 0 {
		t.Fatal("CRD missing 'spec.versions' array")
	}

	version := versions[0].(map[string]interface{})
	subresources, ok := version["subresources"].(map[string]interface{})
	if !ok {
		t.Fatal("CRD version missing 'subresources' field")
	}

	scale, ok := subresources["scale"].(map[string]interface{})
	if !ok {
		t.Fatal("CRD subresources missing 'scale' field")
	}

	specReplicasPath, ok := scale["specReplicasPath"].(string)
	if !ok {
		t.Fatal("scale subresource missing 'specReplicasPath' field")
	}

	// NEGATIVE TEST: This would be the INCORRECT path from POC implementation
	// If this test fails, it means the CRD was incorrectly generated with nested path
	if strings.Contains(specReplicasPath, ".spec.spec.nodeCount") {
		t.Fatalf("CRD contains INCORRECT nested path '.spec.spec.nodeCount' - Task 2 not completed correctly")
	}

	// POSITIVE ASSERTION: Ensure we actually have the correct flat path
	// (This is redundant with TestHumioNodePoolCRDManifestGeneration but demonstrates
	// the RED-to-GREEN transition)
	if specReplicasPath != ".spec.nodeCount" {
		t.Fatalf("CRD specReplicasPath = %q, expected '.spec.nodeCount' for flat structure", specReplicasPath)
	}
}

// TestHumioNodePoolRBACGenerationNegative is a RED-phase test
// that would fail if the ClusterRole was missing required permissions
// for the humionodepools scale subresource.
func TestHumioNodePoolRBACGenerationNegative(t *testing.T) {
	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Fatalf("failed to find project root: %v", err)
	}

	rolePath := filepath.Join(projectRoot, "config/rbac/role.yaml")

	data, err := os.ReadFile(rolePath) //nolint:gosec // test reads from known project path
	if err != nil {
		t.Fatalf("failed to read ClusterRole manifest at %s: %v", rolePath, err)
	}

	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var clusterRole map[string]interface{}

	for {
		var doc map[string]interface{}
		if err := decoder.Decode(&doc); err != nil {
			break
		}
		if kind, ok := doc["kind"].(string); ok && kind == "ClusterRole" {
			clusterRole = doc
			break
		}
	}

	if clusterRole == nil {
		t.Fatal("ClusterRole document not found in role.yaml")
	}

	rules, ok := clusterRole["rules"].([]interface{})
	if !ok {
		t.Fatal("ClusterRole missing 'rules' field")
	}

	// NEGATIVE TEST: Fail if scale subresource permission is missing
	hasScalePermission := false
	for _, r := range rules {
		rule := r.(map[string]interface{})
		resources, ok := rule["resources"].([]interface{})
		if !ok {
			continue
		}

		for _, res := range resources {
			if res.(string) == "humionodepools/scale" {
				hasScalePermission = true
				break
			}
		}
		if hasScalePermission {
			break
		}
	}

	if !hasScalePermission {
		t.Fatal("ClusterRole missing 'humionodepools/scale' permission - RBAC markers not applied correctly")
	}
}
